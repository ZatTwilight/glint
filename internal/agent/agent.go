package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	debuglog "github.com/ZatTwilight/glint/internal/debug"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/ptydaemon"
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
	PID        int
	StartTime  time.Time
	Window     string
	Pane       string
	PtyID      string
	Current    bool
	History    bool
	Activity   time.Time
	Source     string
	Confidence int
}

func ScanWorkspace(workspaceName string, workspacePath string) []Agent {
	return ScanWorkspaceWithPrograms(workspaceName, workspacePath, nil)
}

func ScanWorkspaceWithPrograms(workspaceName string, workspacePath string, programs []multiplexer.MultiplexerProgram) []Agent {
	// For now, rely on explicit hook state plus persisted agent session history.
	// Direct pane inspection is intentionally disabled because pane titles/activity
	// are too noisy for stable chat status.
	// _ = scanLiveMultiplexer(workspaceName, workspacePath)
	agents := ScanHookState(workspacePath)
	agents = mergeHistory(agents, scanPiHistory(workspacePath))
	agents = mergeHistory(agents, scanOpenCodeHistory(workspacePath))
	agents = mergeNativePTY(agents, scanNativePTY(workspacePath))
	agents = populateMultiplexer(agents, workspacePath, programs)
	sortAgents(agents)
	return agents
}

func populateMultiplexer(agents []Agent, workspacePath string, programs []multiplexer.MultiplexerProgram) []Agent {
	if programs == nil && (os.Getenv("TMUX") != "" || os.Getenv("ZELLIJ") != "") {
		programs = multiplexer.MultiplexerPrograms(workspacePath, AgentName, DescendantCommands)
	} else {
		programs = multiplexer.FilterProgramsByWorkspace(programs, workspacePath)
	}

	usedPrograms := make(map[int]bool, len(programs))
	for i := range agents {
		best := -1
		bestScore := 0
		for j := range programs {
			if usedPrograms[j] || programs[j].ProgramName != agents[i].Name {
				continue
			}
			if agents[i].Pane != "" && agents[i].Pane != programs[j].MultiplexerId {
				continue
			}
			score := multiplexerMatchScore(agents[i], programs[j])
			if score > bestScore {
				best = j
				bestScore = score
			}
		}
		if best == -1 || bestScore < 60 {
			continue
		}
		usedPrograms[best] = true
		applyMultiplexerProgram(&agents[i], programs[best], bestScore)
	}

	return agents
}

func multiplexerMatchScore(agent Agent, program multiplexer.MultiplexerProgram) int {
	if agent.Pane == "" && agent.PID <= 0 && (agent.Status == Completed || agent.History || strings.Contains(agent.Source, "history")) {
		return 0
	}
	score := 0
	strong := false
	if samePathOrChild(agent.Path, program.Path) {
		score += 70
	} else if filepath.Base(filepath.Clean(agent.Path)) == filepath.Base(filepath.Clean(program.Path)) {
		score += 25
	}
	if !agent.StartTime.IsZero() && !program.StartTime.IsZero() {
		delta := agent.StartTime.Sub(program.StartTime)
		if delta < 0 {
			delta = -delta
		}
		switch {
		case delta <= 2*time.Minute:
			score += 70
			strong = true
		case delta <= 10*time.Minute:
			score += 45
			strong = true
		case delta <= time.Hour:
			score += 20
			strong = true
		default:
			// Same workspace is not enough to attach an old history/hook record to
			// whichever agent happens to be running there now.
			if agent.Status == Completed || agent.History || strings.Contains(agent.Source, "history") {
				return 0
			}
		}
	}
	if agent.PID > 0 && agent.PID == program.PID {
		score += 100
		strong = true
	}
	if agent.Pane != "" && agent.Pane == program.MultiplexerId {
		score += 100
		strong = true
	}
	if !strong {
		return 0
	}
	return score
}

func samePathOrChild(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return left == right || strings.HasPrefix(left, right+string(os.PathSeparator)) || strings.HasPrefix(right, left+string(os.PathSeparator))
}

func applyMultiplexerProgram(agent *Agent, program multiplexer.MultiplexerProgram, score int) {
	agent.PID = firstNonZero(agent.PID, program.PID)
	agent.Path = firstNonEmpty(agent.Path, program.Path)
	agent.Session = program.Session
	agent.Window = program.Window
	agent.Pane = program.MultiplexerId
	agent.Current = program.Current
	if agent.Source == "" {
		agent.Source = "multiplexer"
	} else if !strings.Contains(agent.Source, "multiplexer") {
		agent.Source += "+multiplexer"
	}
	if score > agent.Confidence {
		agent.Confidence = score
	}
}

func mergePiHistory(live, history []Agent) []Agent {
	return mergeHistory(live, history)
}

func mergeHistory(live, history []Agent) []Agent {
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
			live[i].StartTime = firstNonZeroTime(live[i].StartTime, hist.StartTime)
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

func scanNativePTY(workspacePath string) []Agent {
	resp, err := ptydaemon.ListIfRunning()
	if err != nil {
		return nil
	}
	workspacePath = filepath.Clean(workspacePath)
	var agents []Agent
	for _, session := range resp.Sessions {
		if filepath.Clean(session.CWD) != workspacePath {
			continue
		}
		status := WaitingInput
		activity := session.StartedAt
		if !session.Running {
			status = Completed
			if !session.EndedAt.IsZero() {
				activity = session.EndedAt
			}
		}
		name := "agent"
		if len(session.Command) > 0 {
			name = identifyCommandName(session.Command)
		}
		agents = append(agents, Agent{
			ID: session.ID, PtyID: session.ID, Name: name, Task: nativePTYTask(name, session.Command), Status: status, Path: session.CWD,
			PID: session.PID, StartTime: session.StartedAt, Activity: activity,
			Source: "pty", Confidence: 100,
		})
	}
	return agents
}

func nativePTYTask(name string, command []string) string {
	name = strings.TrimSpace(name)
	if name != "" && name != "agent" {
		return "new " + name + " session"
	}
	if len(command) > 0 {
		return strings.Join(command, " ")
	}
	return "native PTY session"
}

func mergeNativePTY(agents, native []Agent) []Agent {
	if len(native) == 0 {
		return dedupeAgents(agents)
	}
	sort.SliceStable(native, func(i, j int) bool { return native[i].StartTime.Before(native[j].StartTime) })
	usedAgents := map[int]bool{}
	for idx, ptyAgent := range native {
		best := -1
		bestScore := 0
		windowEnd := nextNativePTYStart(native, idx)
		for i := range agents {
			if usedAgents[i] {
				continue
			}
			if score := nativePTYMatchScore(agents[i], ptyAgent, windowEnd); score > bestScore {
				best = i
				bestScore = score
			}
		}
		if best == -1 || bestScore < 100 {
			if ptyAgent.Status != Completed {
				agents = append(agents, ptyAgent)
			}
			continue
		}
		mergeNativePTYInto(&agents[best], ptyAgent)
		usedAgents[best] = true
	}
	return dedupeAgents(agents)
}

func nextNativePTYStart(native []Agent, idx int) time.Time {
	if idx < 0 || idx >= len(native) {
		return time.Time{}
	}
	current := native[idx]
	var next time.Time
	for i := range native {
		if i == idx || native[i].Name != current.Name || filepath.Clean(native[i].Path) != filepath.Clean(current.Path) {
			continue
		}
		if native[i].StartTime.After(current.StartTime) && (next.IsZero() || native[i].StartTime.Before(next)) {
			next = native[i].StartTime
		}
	}
	return next
}

func nativePTYMatchScore(left, right Agent, windowEnd time.Time) int {
	if right.Source != "pty" {
		return 0
	}
	if left.PtyID != "" {
		if left.PtyID != right.ID {
			return 0
		}
		return 1000 + nativePTYTaskBonus(left)
	}
	if left.ID != "" && right.ID != "" && left.ID == right.ID {
		return 900 + nativePTYTaskBonus(left)
	}
	// A real multiplexer pane is already switchable through tmux/Zellij. Do not
	// hydrate it with an unrelated native PTY just because it shares the workspace.
	if left.Pane != "" || left.History || left.Name != right.Name {
		return 0
	}
	if left.Path == "" || right.Path == "" || filepath.Clean(left.Path) != filepath.Clean(right.Path) {
		return 0
	}
	if left.Activity.IsZero() || right.StartTime.IsZero() {
		return 0
	}
	if left.Activity.Before(right.StartTime.Add(-5 * time.Second)) {
		return 0
	}
	if !windowEnd.IsZero() && !left.Activity.Before(windowEnd.Add(5*time.Second)) {
		return 0
	}
	score := 100 + nativePTYTaskBonus(left)
	age := left.Activity.Sub(right.StartTime)
	if age < 0 {
		age = -age
	}
	// Prefer the hook row that happened inside this PTY's lifetime, but avoid
	// letting timestamp proximity beat a real prompt over a synthetic placeholder.
	proximity := int((2*time.Minute - minDuration(age, 2*time.Minute)) / time.Second)
	return score + proximity
}

func nativePTYTaskBonus(agent Agent) int {
	if strings.EqualFold(strings.TrimSpace(agent.Task), "new agent") {
		return -75
	}
	if strings.TrimSpace(agent.Task) == "" || agent.Task == "agent session" {
		return 0
	}
	return 250
}

func mergeNativePTYInto(target *Agent, ptyAgent Agent) {
	// Treat the daemon PTY as transport/liveness metadata, not as the canonical
	// chat description. Keep hook/history task text when it has real prompt info,
	// but make the row selectable via the PTY id.
	target.PtyID = firstNonEmpty(ptyAgent.PtyID, ptyAgent.ID)
	if target.ID == "" {
		target.ID = ptyAgent.ID
	}
	target.PID = firstNonZero(target.PID, ptyAgent.PID)
	target.Path = firstNonEmpty(target.Path, ptyAgent.Path)
	target.StartTime = firstNonZeroTime(target.StartTime, ptyAgent.StartTime)
	if target.Activity.IsZero() {
		target.Activity = ptyAgent.Activity
	}
	if ptyAgent.Status == Completed && (target.Status == Running || target.Status == Thinking || target.Status == WaitingInput || target.Status == NeedsAttention) {
		target.Status = Completed
	} else if target.Status == "" {
		target.Status = ptyAgent.Status
	}
	if target.Task == "" || target.Task == "agent session" {
		target.Task = ptyAgent.Task
	}
	if !strings.Contains(target.Source, "pty") {
		if target.Source == "" {
			target.Source = "pty"
		} else {
			target.Source += "+pty"
		}
	}
}

func identifyCommandName(command []string) string {
	for _, part := range command {
		base := filepath.Base(part)
		switch base {
		case "pi", "claude", "codex", "aider", "opencode", "goose":
			return base
		}
	}
	if len(command) == 0 {
		return "agent"
	}
	return filepath.Base(command[0])
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func firstNonZero(left, right int) int {
	if left != 0 {
		return left
	}
	return right
}

func firstNonZeroTime(left, right time.Time) time.Time {
	if !left.IsZero() {
		return left
	}
	return right
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
		jsonlTime := historyTimeFromFile(path, info)
		if lastTime.IsZero() {
			lastTime = jsonlTime
		}
		debuglog.Printf("checking pi history %s: %s\n", piSessionIDFromFile(entry.Name()), jsonlTime)

		agents = append(agents, Agent{
			ID:         piSessionIDFromFile(entry.Name()),
			Name:       "pi",
			Task:       piTask(path),
			Status:     Completed,
			Path:       path,
			PID:        -1,
			StartTime:  jsonlTime,
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

func historyTimeFromFile(path string, info fs.FileInfo) time.Time {
	base := filepath.Base(path)
	if idx := strings.Index(base, "_"); idx > 0 {
		stamp := strings.ReplaceAll(base[:idx], "-", ":")
		var re = regexp.MustCompile(`:\d\d\dZ`)
		stamp = re.ReplaceAllString(stamp, ".000Z")
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

func AgentName(values ...string) (string, bool) {
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

func DescendantCommands(pid string) []string {
	return NewLazyDescendantCommands()(pid)
}

type processInfo struct {
	pid     int
	command string
}

type processSnapshot struct {
	children map[int][]processInfo
}

func NewLazyDescendantCommands() func(string) []string {
	var snapshot processSnapshot
	var snapshotErr error
	var once sync.Once
	return func(pid string) []string {
		parent, err := strconv.Atoi(strings.TrimSpace(pid))
		if err != nil {
			return nil
		}
		once.Do(func() { snapshot, snapshotErr = loadProcessSnapshot() })
		if snapshotErr != nil {
			return descendantCommandsBySubprocess(pid)
		}
		return snapshot.descendantCommands(parent)
	}
}

func loadProcessSnapshot() (processSnapshot, error) {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return processSnapshot{}, err
	}
	snapshot := processSnapshot{children: map[int][]processInfo{}}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		snapshot.children[ppid] = append(snapshot.children[ppid], processInfo{pid: pid, command: strings.Join(fields[2:], " ")})
	}
	return snapshot, nil
}

func (s processSnapshot) descendantCommands(parent int) []string {
	seen := map[int]bool{}
	var commands []string
	var walk func(int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		for _, child := range s.children[pid] {
			commands = append(commands, child.command)
			walk(child.pid)
		}
	}
	walk(parent)
	return commands
}

func descendantCommandsBySubprocess(pid string) []string {
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
		for child := range strings.FieldsSeq(string(out)) {
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
