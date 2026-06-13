package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/util"
)

const stateDirName = "glint"

type HookRecord struct {
	Time      time.Time       `json:"time"`
	Agent     string          `json:"agent"`
	Event     string          `json:"event"`
	Status    Status          `json:"status"`
	Workspace string          `json:"workspace"`
	SessionID string          `json:"session_id,omitempty"`
	Task      string          `json:"task,omitempty"`
	Message   string          `json:"message,omitempty"`
	Pane      string          `json:"pane,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type HookInput struct {
	Workspace string
	SessionID string
	Task      string
	Status    Status
	Pane      string
	Raw       []byte
	Env       map[string]string
	Now       time.Time
}

func RecordHook(agentName, event string, input HookInput) (HookRecord, error) {
	agentName = normalizeToken(agentName)
	event = normalizeToken(event)
	if agentName == "" || event == "" {
		return HookRecord{}, fmt.Errorf("agent and event are required")
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	if input.Env == nil {
		input.Env = processEnvMap()
	}

	payload := parseHookPayload(input.Raw)
	workspace := util.FirstNonEmpty(input.Workspace, stringFromPayload(payload, "workspace", "workspace_path", "cwd", "directory"), input.Env["GLINT_WORKSPACE"], input.Env["PWD"])
	if workspace == "" {
		workspace = tmuxPaneField(input.Pane, "#{pane_current_path}")
	}
	if workspace != "" {
		if abs, err := filepath.Abs(util.ExpandHome(workspace)); err == nil {
			workspace = filepath.Clean(abs)
		} else {
			workspace = filepath.Clean(util.ExpandHome(workspace))
		}
	}

	pane := util.FirstNonEmpty(input.Pane, stringFromPayload(payload, "pane", "pane_id"), input.Env["TMUX_PANE"])
	sessionID := normalizeHookSessionID(util.FirstNonEmpty(input.SessionID, stringFromPayload(payload, "session_id", "sessionId", "sessionID", "id", "session_file", "sessionFile"), pane))
	task := util.FirstNonEmpty(input.Task, promptFromPayload(payload), stringFromPayload(payload, "task", "title"))
	message := stringFromPayload(payload, "message", "last_assistant_message", "lastAssistantMessage", "body")
	status := input.Status
	if status == "" {
		status = statusForHookEvent(event, payload)
	}

	record := HookRecord{
		Time:      input.Now,
		Agent:     agentName,
		Event:     event,
		Status:    status,
		Workspace: workspace,
		SessionID: sessionID,
		Task:      strings.TrimSpace(task),
		Message:   strings.TrimSpace(message),
		Pane:      pane,
	}
	if len(strings.TrimSpace(string(input.Raw))) > 0 && json.Valid(input.Raw) {
		record.Raw = append(json.RawMessage(nil), input.Raw...)
	}
	if err := appendHookRecord(record); err != nil {
		return HookRecord{}, err
	}
	return record, writeHookLatest(record)
}

func ScanHookState(workspacePath string) []Agent {
	records, err := readHookLatest()
	if err != nil {
		return nil
	}
	workspacePath = filepath.Clean(workspacePath)
	agents := make([]Agent, 0, len(records))
	cutoff := time.Now().Add(-14 * 24 * time.Hour)
	livePaneIDs := multiplexer.TmuxPaneIDsAll()
	latestChanged := false
	for key, rec := range records {
		if rec.Pane != "" && livePaneIDs != nil && !livePaneIDs[rec.Pane] {
			rec.Pane = ""
			if rec.Status == Running || rec.Status == Thinking || rec.Status == WaitingInput || rec.Status == NeedsAttention {
				rec.Status = Completed
			}
			records[key] = rec
			latestChanged = true
		}
		if rec.Workspace == "" || rec.Agent == "" || rec.Status == "" {
			continue
		}
		cwd := filepath.Clean(rec.Workspace)
		if cwd != workspacePath && !strings.HasPrefix(cwd, workspacePath+string(os.PathSeparator)) {
			continue
		}
		if rec.Time.Before(cutoff) && rec.Status != Running && rec.Status != WaitingInput && rec.Status != NeedsAttention {
			continue
		}
		if strings.TrimSpace(rec.Task) == "" {
			continue
		}
		task := util.FirstNonEmpty(rec.Task, "agent session")
		agents = append(agents, Agent{
			ID: rec.SessionID, Name: rec.Agent, Task: task, Status: rec.Status, Path: cwd,
			Pane: rec.Pane, Activity: rec.Time, Source: "hook", Confidence: 100,
		})
	}
	if latestChanged {
		_ = writeHookLatestRecords(records)
	}
	agents = dedupeHookAgents(agents)
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Activity.After(agents[j].Activity) })
	if len(agents) > 20 {
		agents = agents[:20]
	}
	return agents
}

func dedupeHookAgents(agents []Agent) []Agent {
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Activity.After(agents[j].Activity) })
	result := make([]Agent, 0, len(agents))
	for _, ag := range agents {
		duplicate := false
		for _, existing := range result {
			if sameAgentSession(existing, ag) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, ag)
		}
	}
	return result
}

func ReadStdinIfPiped(r io.Reader, stdin *os.File) []byte {
	info, err := stdin.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) != 0 {
		return nil
	}
	data, _ := io.ReadAll(r)
	return data
}

func appendHookRecord(record HookRecord) error {
	path, err := eventsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = file.Write(append(encoded, '\n'))
	return err
}

func writeHookLatest(record HookRecord) error {
	latest, _ := readHookLatest()
	key := hookRecordKey(record)
	if key == "" {
		key = fmt.Sprintf("%s:%s:%s", record.Agent, record.Workspace, record.Pane)
	}
	if existing, ok := latest[key]; ok {
		// Keep the original prompt as stable chat context. Later prompt-submit
		// events should update status/time, not rename the row to the newest message.
		if existing.Task != "" {
			record.Task = existing.Task
		}
		if record.SessionID == "" {
			record.SessionID = existing.SessionID
		}
		if record.Workspace == "" {
			record.Workspace = existing.Workspace
		}
		if record.Pane == "" {
			record.Pane = existing.Pane
		}
	}
	latest[key] = record
	return writeHookLatestRecords(latest)
}

func writeHookLatestRecords(latest map[string]HookRecord) error {
	path, err := latestPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(latest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func readHookLatest() (map[string]HookRecord, error) {
	path, err := latestPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]HookRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records map[string]HookRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func eventsPath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agents", "events.jsonl"), nil
}

func latestPath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agents", "latest.json"), nil
}

func stateDir() (string, error) {
	if dir := os.Getenv("GLINT_STATE_DIR"); strings.TrimSpace(dir) != "" {
		return util.ExpandHome(dir), nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); strings.TrimSpace(dir) != "" {
		return filepath.Join(util.ExpandHome(dir), stateDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", stateDirName), nil
}

func hookRecordKey(record HookRecord) string {
	id := util.FirstNonEmpty(record.Pane, record.SessionID)
	if id == "" || record.Agent == "" {
		return ""
	}
	return record.Agent + "\x00" + id
}

func normalizeHookSessionID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "%") {
		return value
	}
	base := filepath.Base(value)
	if filepath.Ext(base) == ".jsonl" || strings.Contains(base, "_") {
		return piSessionIDFromFile(base)
	}
	return value
}

func parseHookPayload(raw []byte) map[string]any {
	var payload map[string]any
	if len(strings.TrimSpace(string(raw))) == 0 || json.Unmarshal(raw, &payload) != nil {
		return map[string]any{}
	}
	return payload
}

func stringFromPayload(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text := stringify(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func promptFromPayload(payload map[string]any) string {
	if text := stringFromPayload(payload, "prompt", "user_prompt", "userPrompt", "last_user_message"); text != "" {
		return text
	}
	if msg, ok := payload["message"].(map[string]any); ok {
		if msg["role"] == "user" {
			return contentText(msg["content"])
		}
	}
	return ""
}

func statusForHookEvent(event string, payload map[string]any) Status {
	signal := strings.ToLower(util.FirstNonEmpty(event, stringFromPayload(payload, "status", "type", "hook_event_name")))
	switch signal {
	case "prompt-submit", "userpromptsubmit", "before_agent_start", "beforeagent", "session-start", "active", "busy", "running", "agent.start", "preinvocation":
		return Running
	case "stop", "agent-response", "agent_end", "afteragent", "session.idle", "idle", "completed", "complete", "turn-completion", "on_complete":
		return Completed
	case "session-end", "session.deleted", "closed", "archived":
		return Idle
	case "notification", "permissionrequest", "pretooluse", "permission.asked", "question.asked", "shell-exec", "beforeshellexecution":
		return NeedsAttention
	case "error", "failed", "on_error":
		return Failed
	}
	if status := strings.ToLower(stringFromPayload(payload, "status")); status != "" {
		if strings.Contains(status, "idle") {
			return Completed
		}
		if strings.Contains(status, "busy") || strings.Contains(status, "running") {
			return Running
		}
		if strings.Contains(status, "error") || strings.Contains(status, "fail") {
			return Failed
		}
	}
	return Running
}

func stringify(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", v))
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return ""
	}
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func processEnvMap() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func tmuxPaneField(pane, format string) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	args := []string{"display-message", "-p"}
	if pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, format)
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TailHookEvents(limit int) ([]HookRecord, error) {
	path, err := eventsPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []HookRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		var rec HookRecord
		if json.Unmarshal(scanner.Bytes(), &rec) == nil {
			records = append(records, rec)
		}
	}
	if limit > 0 && len(records) > limit {
		records = records[len(records)-limit:]
	}
	return records, scanner.Err()
}
