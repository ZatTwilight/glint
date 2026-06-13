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
	env := map[string]string{"PWD": "/tmp/project"}
	now := time.Now().UTC().Truncate(time.Second)
	_, err := RecordHook("pi", "prompt-submit", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","prompt":"Do work"}`),
		Now:       now,
		Env:       env,
	})
	if err != nil {
		t.Fatalf("record start: %v", err)
	}
	_, err = RecordHook("pi", "stop", HookInput{
		Workspace: "/tmp/project",
		Raw:       []byte(`{"session_id":"s1","last_assistant_message":"Done"}`),
		Now:       now.Add(time.Minute),
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
