package multiplexer

import "testing"

func TestParseZellijSessionsKeepsExitedSessions(t *testing.T) {
	sessions := parseZellijSessions("glint [Created 1h ago] (EXITED - attach to resurrect)\nglint-2 [Created 1m ago]\n", "", "")

	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2: %+v", len(sessions), sessions)
	}
	if got, want := sessions[0].Name, "glint"; got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
	if !sessions[0].Exited {
		t.Fatalf("exited session was not marked exited: %+v", sessions[0])
	}
}

func TestParseZellijSessionsMarksCurrentPath(t *testing.T) {
	sessions := parseZellijSessions("glint [Created 1m ago] (current)\n", "glint", "/tmp/glint")

	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1: %+v", len(sessions), sessions)
	}
	if !sessions[0].Attached {
		t.Fatalf("current session was not marked attached: %+v", sessions[0])
	}
	if got, want := sessions[0].Path, "/tmp/glint"; got != want {
		t.Fatalf("session path = %q, want %q", got, want)
	}
}
