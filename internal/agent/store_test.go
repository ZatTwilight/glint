package agent

import (
	"testing"
	"time"
)

func TestRecordHookAndScanHookState(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","prompt":"Do work"}`),
		Now:       now,
		Env:       map[string]string{"PWD": "/tmp/project"},
	})
	if err != nil {
		t.Fatalf("RecordHook returned error: %v", err)
	}

	agents := ScanHookState("/tmp/project")
	if len(agents) != 1 {
		t.Fatalf("expected 1 hook agent, got %d", len(agents))
	}
	ag := agents[0]
	if ag.Name != "pi" || ag.Status != Running || ag.Task != "Do work" || ag.Source != "hook" || ag.Confidence != 100 {
		t.Fatalf("unexpected agent: %#v", ag)
	}
}

func TestRecordHookKeepsOriginalPromptAsContext(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	env := map[string]string{"PWD": "/tmp/project"}
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","prompt":"First prompt"}`),
		Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		Env:       env,
	})
	if err != nil {
		t.Fatalf("record first prompt: %v", err)
	}
	_, err = RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","prompt":"Newest prompt"}`),
		Now:       time.Date(2026, 5, 29, 12, 1, 0, 0, time.UTC),
		Env:       env,
	})
	if err != nil {
		t.Fatalf("record newest prompt: %v", err)
	}

	agents := ScanHookState("/tmp/project")
	if len(agents) != 1 {
		t.Fatalf("expected 1 hook agent, got %d", len(agents))
	}
	if agents[0].Task != "First prompt" || agents[0].Status != Running {
		t.Fatalf("unexpected latest agent: %#v", agents[0])
	}
}

func TestRecordHookNormalizesPiSessionFileID(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_file":"/home/me/.pi/agent/sessions/--tmp-project--/2026-05-29T12-00-00.000Z_abc123.jsonl","prompt":"Do work"}`),
		Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		Env:       map[string]string{"PWD": "/tmp/project"},
	})
	if err != nil {
		t.Fatalf("RecordHook returned error: %v", err)
	}
	agents := ScanHookState("/tmp/project")
	if len(agents) != 1 || agents[0].ID != "abc123" {
		t.Fatalf("expected normalized id abc123, got %#v", agents)
	}
}

func TestMergePiHistoryEnhancesHookState(t *testing.T) {
	hook := Agent{Name: "pi", ID: "s1", Task: "First prompt", Status: Completed, Activity: time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC), Source: "hook", Confidence: 100}
	history := Agent{Name: "pi", ID: "s1", Task: "First prompt from history", Status: Completed, History: true, Activity: time.Date(2026, 5, 29, 12, 2, 0, 0, time.UTC), Source: "pi-history", Confidence: 80}

	merged := mergePiHistory([]Agent{hook}, []Agent{history})
	if len(merged) != 1 {
		t.Fatalf("expected one merged agent, got %#v", merged)
	}
	if merged[0].Task != "First prompt from history" || !merged[0].Activity.Equal(history.Activity) || merged[0].Source != "hook+history" {
		t.Fatalf("unexpected merged agent: %#v", merged[0])
	}
}

func TestRecordHookStopUpdatesLatest(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	t.Setenv("GLINT_HOOK_CUTOFF_DAYS", "365")
	env := map[string]string{"PWD": "/tmp/project"}
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","prompt":"Do work"}`),
		Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		Env:       env,
	})
	if err != nil {
		t.Fatalf("record start: %v", err)
	}
	_, err = RecordHook("pi", "stop", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","last_assistant_message":"Done"}`),
		Now:       time.Date(2026, 5, 29, 12, 1, 0, 0, time.UTC),
		Env:       env,
	})
	if err != nil {
		t.Fatalf("record stop: %v", err)
	}

	agents := ScanHookState("/tmp/project")
	if len(agents) != 1 {
		t.Fatalf("expected 1 hook agent, got %d", len(agents))
	}
	if agents[0].Status != Completed || agents[0].Task != "Do work" {
		t.Fatalf("unexpected latest agent: %#v", agents[0])
	}
}

func TestRecordHookStoresPTYTransportSeparately(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	t.Setenv("GLINT_HOOK_PTY_LIVENESS", "0")
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"pi-session","prompt":"Do work"}`),
		Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		Env:       map[string]string{"PWD": "/tmp/project", "GLINT_PTY_SESSION": "pty-session"},
	})
	if err != nil {
		t.Fatalf("RecordHook returned error: %v", err)
	}

	agents := ScanHookState("/tmp/project")
	if len(agents) != 1 {
		t.Fatalf("expected 1 hook agent, got %d", len(agents))
	}
	if agents[0].ID != "pi-session" || agents[0].PtyID != "pty-session" {
		t.Fatalf("unexpected ids: %#v", agents[0])
	}
}

func TestScanHookStateSkipsSyntheticNativePTYStart(t *testing.T) {
	t.Setenv("GLINT_STATE_DIR", t.TempDir())
	_, err := RecordHook("pi", "session-start", HookInput{
		Workspace: "/tmp/project",
		SessionID: "project-pi-123",
		Task:      "new agent",
		Status:    Running,
		Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
		Env:       map[string]string{"PWD": "/tmp/project"},
	})
	if err != nil {
		t.Fatalf("RecordHook returned error: %v", err)
	}

	if agents := ScanHookState("/tmp/project"); len(agents) != 0 {
		t.Fatalf("expected synthetic placeholder to be hidden, got %#v", agents)
	}
}

func TestMergeNativePTYHydratesNoPaneHookRowsByLifetime(t *testing.T) {
	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	agents := []Agent{
		{Name: "pi", ID: "tmux", Task: "normal tmux", Status: Running, Path: "/tmp/project", Pane: "%1", Activity: base.Add(10 * time.Minute), Source: "hook"},
		{Name: "pi", ID: "first", Task: "first prompt", Status: Completed, Path: "/tmp/project", Activity: base.Add(1 * time.Minute), Source: "hook"},
		{Name: "pi", ID: "second", Task: "second prompt", Status: Completed, Path: "/tmp/project", Activity: base.Add(5 * time.Minute), Source: "hook"},
	}
	native := []Agent{
		{ID: "pty-1", PtyID: "pty-1", Name: "pi", Task: "new pi session", Status: WaitingInput, Path: "/tmp/project", StartTime: base, Activity: base, Source: "pty"},
		{ID: "pty-2", PtyID: "pty-2", Name: "pi", Task: "new pi session", Status: WaitingInput, Path: "/tmp/project", StartTime: base.Add(4 * time.Minute), Activity: base.Add(4 * time.Minute), Source: "pty"},
	}

	merged := mergeNativePTY(agents, native)
	byTask := map[string]Agent{}
	for _, ag := range merged {
		byTask[ag.Task] = ag
	}
	if byTask["first prompt"].PtyID != "pty-1" {
		t.Fatalf("first prompt not hydrated with pty-1: %#v", byTask["first prompt"])
	}
	if byTask["second prompt"].PtyID != "pty-2" {
		t.Fatalf("second prompt not hydrated with pty-2: %#v", byTask["second prompt"])
	}
	if byTask["normal tmux"].PtyID != "" {
		t.Fatalf("tmux row should not receive native pty: %#v", byTask["normal tmux"])
	}
}

func TestMergeNativePTYSurfacesUnmatchedPTY(t *testing.T) {
	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	merged := mergeNativePTY(nil, []Agent{{ID: "pty-1", PtyID: "pty-1", Name: "pi", Task: "new pi session", Status: WaitingInput, Path: "/tmp/project", StartTime: base, Activity: base, Source: "pty"}})
	if len(merged) != 1 {
		t.Fatalf("expected unmatched pty placeholder, got %#v", merged)
	}
	if merged[0].PtyID != "pty-1" || merged[0].Status != WaitingInput || merged[0].Task != "new pi session" {
		t.Fatalf("unexpected placeholder: %#v", merged[0])
	}
}

func TestMergeNativePTYHidesUnmatchedCompletedPTY(t *testing.T) {
	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	merged := mergeNativePTY(nil, []Agent{{ID: "pty-1", PtyID: "pty-1", Name: "pi", Task: "new pi session", Status: Completed, Path: "/tmp/project", StartTime: base, Activity: base, Source: "pty"}})
	if len(merged) != 0 {
		t.Fatalf("expected completed unmatched pty to be hidden, got %#v", merged)
	}
}

func TestNativePTYPlaceholderFilter(t *testing.T) {
	cases := []struct {
		name  string
		event string
		task  string
		want  bool
	}{
		{"new agent", "session-start", "new agent", true},
		{"new pi session", "session-start", "new pi session", true},
		{"new claude session", "session-start", "new claude session", true},
		{"real prompt", "session-start", "refactor auth", false},
		{"prompt submit", "prompt-submit", "new agent", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isNativePTYPlaceholderRecord(HookRecord{Event: tc.event, Task: tc.task})
			if got != tc.want {
				t.Fatalf("isNativePTYPlaceholderRecord(%q, %q) = %v, want %v", tc.event, tc.task, got, tc.want)
			}
		})
	}
}
