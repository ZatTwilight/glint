package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	Running        Status = "running"
	Idle           Status = "idle"
	Thinking       Status = "thinking"
	Completed      Status = "completed"
	NeedsAttention Status = "needs_attention"
	Failed         Status = "failed"
	WaitingInput   Status = "waiting_input"
)

type Agent struct {
	ID         string
	Name       string
	Task       string
	Status     Status
	Path       string
	Session    string
	Window     string
	Pane       string
	Current    bool
	History    bool
	Activity   time.Time
	Source     string
	Confidence int
}

func ScanWorkspace(_ string, workspacePath string) []Agent {
	// For now, rely on explicit hook state plus Pi's persisted session history.
	// tmux pane inspection is intentionally disabled because pane titles/activity
	// are too noisy for stable chat status.
	agents := ScanHookState(workspacePath)
	agents = mergePiHistory(agents, scanPiHistory(workspacePath))
	sortAgents(agents)
	return agents
}

func scanLiveTmux(workspaceName, workspacePath string) []Agent {
	if os.Getenv("TMUX") == "" {
		return nil
	}

	format := strings.Join([]string{
		"#{session_name}",
		"#{window_id}",
		"#{window_name}",
		"#{pane_id}",
		"#{pane_current_path}",
		"#{pane_current_command}",
		"#{pane_pid}",
		"#{pane_title}",
		"#{pane_active}",
		"#{window_activity}",
	}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil
	}

	workspacePath = filepath.Clean(workspacePath)
	var agents []Agent
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 10 {
			continue
		}
		panePath := filepath.Clean(fields[4])
		inWorkspace := panePath == workspacePath || strings.HasPrefix(panePath, workspacePath+string(os.PathSeparator))
		inSession := fields[0] == workspaceName || strings.HasSuffix(fields[0], "/"+workspaceName)
		if !inWorkspace && !inSession {
			continue
		}
		name, ok := agentName(append([]string{fields[5], fields[7], fields[2], fields[0]}, descendantCommands(fields[6])...)...)
		if !ok {
			continue
		}
		activity := unixTime(fields[9])
		task := "agent session"
		agents = append(agents, Agent{
			Name: name, Task: task, Status: inferStatus(name, activity), Path: panePath,
			Session: fields[0], Window: fields[1], Pane: fields[3], Current: fields[8] == "1", Activity: activity,
			Source: "tmux", Confidence: 40,
		})
	}

	return agents
}

func mergeHistory(live, history []Agent) []Agent {
	seen := map[string]bool{}
	for _, ag := range live {
		seen[ag.Name+"\x00"+ag.Task] = true
	}
	for _, ag := range history {
		if seen[ag.Name+"\x00"+ag.Task] {
			continue
		}
		live = append(live, ag)
	}
	return live
}

func mergePiHistory(live, history []Agent) []Agent {
	for _, hist := range history {
		matched := false
		for i := range live {
			if !sameAgentSession(live[i], hist) && !sameAgentContext(live[i], hist) {
				continue
			}
			matched = true
			live[i].ID = firstNonEmpty(live[i].ID, hist.ID)
			if hist.Task != "" && hist.Task != "agent session" {
				live[i].Task = hist.Task
			}
			if hist.Activity.After(live[i].Activity) {
				live[i].Activity = hist.Activity
			}
			if live[i].Status == "" || live[i].Status == Idle {
				live[i].Status = hist.Status
			}
			if live[i].Source == "hook" {
				live[i].Source = "hook+history"
			}
			break
		}
		if !matched {
			live = append(live, hist)
		}
	}
	return dedupeAgents(live)
}

func mergeHookState(live, hooks []Agent) []Agent {
	for _, hook := range hooks {
		matched := false
		for i := range live {
			if !sameAgentSession(live[i], hook) {
				continue
			}
			matched = true
			if hook.Confidence >= live[i].Confidence {
				live[i].ID = firstNonEmpty(hook.ID, live[i].ID)
				live[i].Status = hook.Status
				live[i].Source = hook.Source
				live[i].Confidence = hook.Confidence
				if hook.Task != "" && hook.Task != "agent session" {
					live[i].Task = hook.Task
				}
				if !hook.Activity.IsZero() {
					live[i].Activity = hook.Activity
				}
			}
			break
		}
		if !matched {
			live = append(live, hook)
		}
	}
	return live
}

func sameAgentSession(left, right Agent) bool {
	if left.Name != right.Name {
		return false
	}
	if left.Pane != "" && right.Pane != "" && left.Pane == right.Pane {
		return true
	}
	if left.ID != "" && right.ID != "" && left.ID == right.ID {
		return true
	}
	return false
}

func sameAgentContext(left, right Agent) bool {
	return left.Name == right.Name && left.Task != "" && right.Task != "" && left.Task == right.Task
}

func dedupeAgents(agents []Agent) []Agent {
	result := make([]Agent, 0, len(agents))
	for _, ag := range agents {
		duplicate := false
		for i := range result {
			if !sameAgentSession(result[i], ag) {
				continue
			}
			duplicate = true
			if ag.Activity.After(result[i].Activity) {
				if result[i].Status == Running || result[i].Status == NeedsAttention || result[i].Status == WaitingInput {
					ag.Status = result[i].Status
				}
				if result[i].Task != "" && result[i].Task != "agent session" {
					ag.Task = result[i].Task
				}
				result[i] = ag
			}
			break
		}
		if !duplicate {
			result = append(result, ag)
		}
	}
	return result
}

func sortAgents(agents []Agent) {
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].Current != agents[j].Current {
			return agents[i].Current
		}
		if agents[i].History != agents[j].History {
			return !agents[i].History
		}
		if !agents[i].Activity.Equal(agents[j].Activity) {
			return agents[i].Activity.After(agents[j].Activity)
		}
		return agents[i].Name < agents[j].Name
	})
}

func scanPiHistory(workspacePath string) []Agent {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".pi", "agent", "sessions", encodedWorkspace(workspacePath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	agents := make([]Agent, 0, min(len(entries), 5))
	for i := len(entries) - 1; i >= 0 && len(agents) < 5; i-- {
		entry := entries[i]
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		lastTime := lastJSONLTimestamp(path)
		if lastTime.IsZero() {
			lastTime = historyTime(path, info)
		}

		agents = append(agents, Agent{
			ID:         piSessionIDFromFile(entry.Name()),
			Name:       "pi",
			Task:       piTask(path),
			Status:     Completed,
			Path:       path,
			History:    true,
			Activity:   lastTime,
			Source:     "pi-history",
			Confidence: 80,
		})
	}
	return agents
}

func lastJSONLTimestamp(path string) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer file.Close()

	var lastLine string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		lastLine = scanner.Text()
	}
	if lastLine == "" {
		return time.Time{}
	}
	var data struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal([]byte(lastLine), &data) != nil {
		return time.Time{}
	}
	if t := parseTime(data.Timestamp); !t.IsZero() {
		return t
	}
	t, _ := time.Parse("2006-01-02T15:04:05.000Z", data.Timestamp)
	return t
}

func piSessionIDFromFile(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.Split(base, "_")
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[1])
	}
	return base
}

func scanCodexHistory(workspacePath string) []Agent {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".codex", "sessions")
	return scanJSONLHistory(root, workspacePath, "codex", codexHistoryInfo)
}

func scanClaudeHistory(workspacePath string) []Agent {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".claude", "projects")
	return scanJSONLHistory(root, workspacePath, "claude", claudeHistoryInfo)
}

type historyInfo struct {
	CWD  string
	Task string
	Time time.Time
}

func scanJSONLHistory(root, workspacePath, name string, readInfo func(string, fs.FileInfo) historyInfo) []Agent {
	var agents []Agent
	workspacePath = filepath.Clean(workspacePath)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		history := readInfo(path, info)
		cwd := filepath.Clean(history.CWD)
		if cwd != workspacePath && !strings.HasPrefix(cwd, workspacePath+string(os.PathSeparator)) {
			return nil
		}
		if history.Task == "" {
			history.Task = "previous session"
		}
		agents = append(agents, Agent{Name: name, Task: history.Task, Status: Completed, Path: path, History: true, Activity: history.Time})
		return nil
	})
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Activity.After(agents[j].Activity) })
	if len(agents) > 5 {
		agents = agents[:5]
	}
	return agents
}

func codexHistoryInfo(path string, info fs.FileInfo) historyInfo {
	result := historyInfo{Time: info.ModTime()}
	scanJSONL(path, func(raw json.RawMessage) bool {
		var event struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				CWD     string `json:"cwd"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Role string `json:"role"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &event) != nil {
			return true
		}
		if t := parseTime(event.Timestamp); !t.IsZero() {
			result.Time = t
		}
		if event.Type == "session_meta" && event.Payload.CWD != "" {
			result.CWD = event.Payload.CWD
		}
		if event.Type == "response_item" && event.Payload.Role == "user" && result.Task == "" {
			for _, part := range event.Payload.Content {
				if (part.Type == "input_text" || part.Type == "text") && strings.TrimSpace(part.Text) != "" && !strings.HasPrefix(part.Text, "<environment_context>") {
					result.Task = strings.TrimSpace(part.Text)
					return false
				}
			}
		}
		return true
	})
	return result
}

func claudeHistoryInfo(path string, info fs.FileInfo) historyInfo {
	result := historyInfo{Time: info.ModTime()}
	scanJSONL(path, func(raw json.RawMessage) bool {
		var event struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			CWD       string `json:"cwd"`
			Message   struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &event) != nil {
			return true
		}
		if event.CWD != "" {
			result.CWD = event.CWD
		}
		if t := parseTime(event.Timestamp); !t.IsZero() {
			result.Time = t
		}
		if event.Type == "user" && event.Message.Role == "user" && result.Task == "" {
			if text := contentText(event.Message.Content); text != "" {
				result.Task = text
				return false
			}
		}
		return true
	})
	return result
}

func scanJSONL(path string, visit func(json.RawMessage) bool) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		if !visit(json.RawMessage(scanner.Bytes())) {
			return
		}
	}
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func contentText(value any) string {
	switch content := value.(type) {
	case string:
		return strings.TrimSpace(content)
	case []any:
		for _, item := range content {
			part, ok := item.(map[string]any)
			if !ok || part["type"] != "text" {
				continue
			}
			if text, ok := part["text"].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func encodedWorkspace(path string) string {
	path = filepath.Clean(path)
	if path == string(os.PathSeparator) {
		return "--"
	}
	return "--" + strings.Trim(strings.ReplaceAll(path, string(os.PathSeparator), "-"), "-") + "--"
}

func historyTime(path string, info fs.FileInfo) time.Time {
	base := filepath.Base(path)
	if idx := strings.Index(base, "_"); idx > 0 {
		stamp := strings.ReplaceAll(base[:idx], "-", ":")
		if len(stamp) >= 10 {
			stamp = strings.Replace(stamp, ":", "-", 2)
		}
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", stamp); err == nil {
			return t
		}
	}
	return info.ModTime()
}

func piTask(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "previous session"
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "message" || event.Message.Role != "user" {
			continue
		}
		for _, part := range event.Message.Content {
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				return strings.TrimSpace(part.Text)
			}
		}
	}
	return fmt.Sprintf("session %s", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
}

func agentName(values ...string) (string, bool) {
	aliases := map[string]string{"pi": "pi", "claude": "claude", "codex": "codex", "aider": "aider", "opencode": "opencode", "goose": "goose"}
	for _, value := range values {
		for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return r == ' ' || r == '-' || r == '_' || r == ':' || r == '/' || r == '.'
		}) {
			if name, ok := aliases[token]; ok {
				return name, true
			}
		}
		cmd := strings.ToLower(filepath.Base(value))
		if name, ok := aliases[cmd]; ok {
			return name, true
		}
	}
	return "", false
}

func descendantCommands(pid string) []string {
	seen := map[string]bool{}
	var commands []string
	var walk func(string)
	walk = func(parent string) {
		if seen[parent] {
			return
		}
		seen[parent] = true
		out, err := exec.Command("pgrep", "-P", parent).Output()
		if err != nil {
			return
		}
		for _, child := range strings.Fields(string(out)) {
			cmd, err := exec.Command("ps", "-p", child, "-o", "comm=").Output()
			if err == nil {
				commands = append(commands, strings.TrimSpace(string(cmd)))
			}
			walk(child)
		}
	}
	walk(pid)
	return commands
}

func inferStatus(_ string, activity time.Time) Status {
	if activity.IsZero() {
		return Idle
	}
	age := time.Since(activity)
	if age < 0 {
		return Running
	}
	if age < 20*time.Second {
		return Running
	}
	if age < 2*time.Minute {
		return Thinking
	}
	return Idle
}

func taskText(title, window, session string) string {
	for _, value := range []string{title, window, session} {
		value = strings.TrimSpace(value)
		if value != "" && value != "bash" && value != "zsh" && value != "fish" {
			return value
		}
	}
	return "agent session"
}

func unixTime(value string) time.Time {
	unix, err := strconv.ParseInt(value, 10, 64)
	if err != nil || unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

func Icon(name string) string {
	switch strings.ToLower(name) {
	case "pi":
		return "π"
	case "claude":
		return "✶"
	case "codex":
		return "⌘"
	case "aider":
		return "A"
	case "opencode":
		return "◇"
	case "goose":
		return "G"
	default:
		return "•"
	}
}

func Symbol(status Status) string {
	switch DisplayStatus(status) {
	case "working":
		return "●"
	case "waiting":
		return "?"
	case "done":
		return "✓"
	default:
		return "◌"
	}
}

func DisplayStatus(status Status) string {
	switch status {
	case Running, Thinking:
		return "working"
	case NeedsAttention, WaitingInput, Failed:
		return "waiting"
	case Completed, Idle:
		return "done"
	default:
		return "done"
	}
}
