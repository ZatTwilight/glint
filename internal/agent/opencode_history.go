package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

const (
	openCodeHistoryLimit    = 500
	openCodeHistoryCacheTTL = 10 * time.Second
	t3HistoryCacheTTL       = 10 * time.Second
	openCodeSQLiteTimeout   = 750 * time.Millisecond
	openCodeCLITimeout      = 2 * time.Second
)

type openCodeSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	ProjectID string `json:"projectId"`
}

type t3ThreadRecord struct {
	ThreadID          string `json:"thread_id"`
	Title             string `json:"title"`
	WorkspaceRoot     string `json:"workspace_root"`
	ProviderSessionID string `json:"provider_session_id"`
	UpdatedAt         string `json:"updated_at"`
}

type t3ThreadIndex struct {
	ByThreadID          map[string]t3ThreadRecord
	ByProviderSessionID map[string]t3ThreadRecord
}

var openCodeHistoryCache struct {
	sync.Mutex
	loadedAt time.Time
	sessions []openCodeSession
	err      error
}

var t3ThreadCache struct {
	sync.Mutex
	loadedAt time.Time
	index    t3ThreadIndex
	err      error
}

var t3CodeTitlePattern = regexp.MustCompile(`(?i)^T3 Code ([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

func scanOpenCodeHistory(workspacePath string) []Agent {
	sessions, err := cachedOpenCodeSessions()
	if err != nil {
		debuglog.Printf("opencode history unavailable: %v\n", err)
		return nil
	}
	t3Threads, err := cachedT3Threads()
	if err != nil {
		debuglog.Printf("t3code thread names unavailable: %v\n", err)
	}
	return openCodeHistoryAgentsFromSessions(sessions, workspacePath, t3Threads)
}

func cachedOpenCodeSessions() ([]openCodeSession, error) {
	now := time.Now()
	openCodeHistoryCache.Lock()
	defer openCodeHistoryCache.Unlock()

	if !openCodeHistoryCache.loadedAt.IsZero() && now.Sub(openCodeHistoryCache.loadedAt) < openCodeHistoryCacheTTL {
		return openCodeHistoryCache.sessions, openCodeHistoryCache.err
	}

	sessions, err := loadOpenCodeSessions()
	openCodeHistoryCache.loadedAt = time.Now()
	openCodeHistoryCache.sessions = sessions
	openCodeHistoryCache.err = err
	return sessions, err
}

func cachedT3Threads() (t3ThreadIndex, error) {
	now := time.Now()
	t3ThreadCache.Lock()
	defer t3ThreadCache.Unlock()

	if !t3ThreadCache.loadedAt.IsZero() && now.Sub(t3ThreadCache.loadedAt) < t3HistoryCacheTTL {
		return t3ThreadCache.index, t3ThreadCache.err
	}

	index, err := loadT3Threads()
	t3ThreadCache.loadedAt = time.Now()
	t3ThreadCache.index = index
	t3ThreadCache.err = err
	return index, err
}

func loadOpenCodeSessions() ([]openCodeSession, error) {
	sessions, sqliteErr := loadOpenCodeSessionsFromSQLite()
	if sqliteErr == nil {
		return sessions, nil
	}

	sessions, cliErr := loadOpenCodeSessionsFromCLI()
	if cliErr == nil {
		return sessions, nil
	}

	return nil, fmt.Errorf("sqlite: %v; cli: %w", sqliteErr, cliErr)
}

func loadOpenCodeSessionsFromSQLite() ([]openCodeSession, error) {
	dbPath := openCodeDBPath()
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT id, title, directory, time_created AS created, time_updated AS updated FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC LIMIT %d;`, openCodeHistoryLimit)
	ctx, cancel := context.WithTimeout(context.Background(), openCodeSQLiteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-json", dbPath, query)
	out, err := commandOutput(cmd)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return parseOpenCodeSessionsJSON(out)
}

func loadOpenCodeSessionsFromCLI() ([]openCodeSession, error) {
	bin, err := openCodeBinary()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), openCodeCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--pure", "session", "list", "--format", "json", "--max-count", strconv.Itoa(openCodeHistoryLimit))
	out, err := commandOutput(cmd)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return parseOpenCodeSessionsJSON(out)
}

func loadT3Threads() (t3ThreadIndex, error) {
	dbPath := t3DBPath()
	if _, err := os.Stat(dbPath); err != nil {
		return t3ThreadIndex{}, err
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return t3ThreadIndex{}, err
	}

	query := `SELECT t.thread_id AS thread_id, t.title AS title, p.workspace_root AS workspace_root, COALESCE(s.provider_session_id, '') AS provider_session_id, t.updated_at AS updated_at FROM projection_threads t LEFT JOIN projection_projects p ON p.project_id = t.project_id LEFT JOIN projection_thread_sessions s ON s.thread_id = t.thread_id WHERE t.deleted_at IS NULL;`
	ctx, cancel := context.WithTimeout(context.Background(), openCodeSQLiteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlite, "-readonly", "-json", dbPath, query)
	out, err := commandOutput(cmd)
	if ctx.Err() != nil {
		return t3ThreadIndex{}, ctx.Err()
	}
	if err != nil {
		return t3ThreadIndex{}, err
	}
	return parseT3ThreadsJSON(out)
}

func commandOutput(cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

func parseOpenCodeSessionsJSON(data []byte) ([]openCodeSession, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var sessions []openCodeSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func parseT3ThreadsJSON(data []byte) (t3ThreadIndex, error) {
	index := t3ThreadIndex{ByThreadID: map[string]t3ThreadRecord{}, ByProviderSessionID: map[string]t3ThreadRecord{}}
	if len(strings.TrimSpace(string(data))) == 0 {
		return index, nil
	}
	var records []t3ThreadRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return index, err
	}
	for _, record := range records {
		record.ThreadID = strings.TrimSpace(record.ThreadID)
		record.Title = strings.TrimSpace(record.Title)
		record.ProviderSessionID = strings.TrimSpace(record.ProviderSessionID)
		if record.ThreadID != "" {
			index.ByThreadID[record.ThreadID] = record
		}
		if record.ProviderSessionID != "" {
			index.ByProviderSessionID[record.ProviderSessionID] = record
		}
	}
	return index, nil
}

func openCodeHistoryAgentsFromSessions(sessions []openCodeSession, workspacePath string, t3Threads t3ThreadIndex) []Agent {
	workspacePath = cleanHistoryPath(workspacePath)
	if workspacePath == "" {
		return nil
	}

	agents := make([]Agent, 0, 5)
	for _, session := range sessions {
		directory := cleanHistoryPath(session.Directory)
		if !pathWithinWorkspace(directory, workspacePath) {
			continue
		}

		activity := openCodeUnixTime(session.Updated)
		if activity.IsZero() {
			activity = openCodeUnixTime(session.Created)
		}
		task, source := openCodeSessionTask(session, t3Threads)
		agents = append(agents, Agent{
			ID:         strings.TrimSpace(session.ID),
			Name:       "opencode",
			Task:       task,
			Status:     Completed,
			Path:       directory,
			StartTime:  openCodeUnixTime(session.Created),
			History:    true,
			Activity:   activity,
			Source:     source,
			Confidence: 80,
		})
	}

	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Activity.After(agents[j].Activity) })
	if len(agents) > 5 {
		agents = agents[:5]
	}
	return agents
}

func openCodeSessionTask(session openCodeSession, t3Threads t3ThreadIndex) (string, string) {
	if thread := t3ThreadForOpenCodeSession(session, t3Threads); thread.Title != "" {
		return thread.Title, "opencode-history+t3"
	}
	return firstNonEmpty(session.Title, "previous session"), "opencode-history"
}

func t3ThreadForOpenCodeSession(session openCodeSession, index t3ThreadIndex) t3ThreadRecord {
	if index.ByProviderSessionID != nil {
		if thread, ok := index.ByProviderSessionID[strings.TrimSpace(session.ID)]; ok {
			return thread
		}
	}
	if index.ByThreadID != nil {
		if id := t3ThreadIDFromOpenCodeTitle(session.Title); id != "" {
			if thread, ok := index.ByThreadID[id]; ok {
				return thread
			}
		}
	}
	return t3ThreadRecord{}
}

func t3ThreadIDFromOpenCodeTitle(title string) string {
	matches := t3CodeTitlePattern.FindStringSubmatch(strings.TrimSpace(title))
	if len(matches) != 2 {
		return ""
	}
	return strings.ToLower(matches[1])
}

func cleanHistoryPath(path string) string {
	path = strings.TrimSpace(expandHome(path))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	return filepath.Clean(path)
}

func pathWithinWorkspace(path, workspace string) bool {
	if path == "" || workspace == "" {
		return false
	}
	path = filepath.Clean(path)
	workspace = filepath.Clean(workspace)
	return path == workspace || strings.HasPrefix(path, workspace+string(os.PathSeparator))
}

func openCodeUnixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	// OpenCode currently reports millisecond timestamps. Accept seconds too so
	// older/future list formats degrade gracefully.
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0)
	}
	return time.UnixMilli(value)
}

func openCodeDBPath() string {
	if path := strings.TrimSpace(os.Getenv("GLINT_OPENCODE_DB")); path != "" {
		return expandHome(path)
	}
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dir != "" {
		return filepath.Join(expandHome(dir), "opencode.db")
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(expandHome(dir), "opencode", "opencode.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

func t3DBPath() string {
	if path := strings.TrimSpace(os.Getenv("GLINT_T3_DB")); path != "" {
		return expandHome(path)
	}
	if dir := strings.TrimSpace(os.Getenv("T3_DATA_DIR")); dir != "" {
		return filepath.Join(expandHome(dir), "userdata", "state.sqlite")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".t3", "userdata", "state.sqlite")
}

func openCodeBinary() (string, error) {
	if bin := strings.TrimSpace(os.Getenv("GLINT_OPENCODE_BIN")); bin != "" {
		return expandHome(bin), nil
	}
	if path, err := exec.LookPath("opencode"); err == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{
		filepath.Join(home, ".opencode", "bin", "opencode"),
		filepath.Join(home, ".local", "share", "opencode", "bin", "opencode"),
	} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
