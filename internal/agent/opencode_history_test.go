package agent

import (
	"testing"
	"time"
)

func TestOpenCodeHistoryAgentsFromSessionsFiltersAndSorts(t *testing.T) {
	sessions := []openCodeSession{
		{
			ID:        "ses_old",
			Title:     "Old task",
			Directory: "/tmp/project/pkg",
			Created:   time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC).UnixMilli(),
			Updated:   time.Date(2026, 6, 14, 10, 5, 0, 0, time.UTC).UnixMilli(),
		},
		{
			ID:        "ses_other",
			Title:     "Other project",
			Directory: "/tmp/project-other",
			Created:   time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC).UnixMilli(),
			Updated:   time.Date(2026, 6, 14, 11, 5, 0, 0, time.UTC).UnixMilli(),
		},
		{
			ID:        "ses_new",
			Directory: "/tmp/project",
			Created:   time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).UnixMilli(),
			Updated:   time.Date(2026, 6, 14, 12, 5, 0, 0, time.UTC).UnixMilli(),
		},
	}

	agents := openCodeHistoryAgentsFromSessions(sessions, "/tmp/project", t3ThreadIndex{})
	if len(agents) != 2 {
		t.Fatalf("expected 2 opencode agents, got %#v", agents)
	}
	if agents[0].ID != "ses_new" || agents[1].ID != "ses_old" {
		t.Fatalf("agents not sorted by updated time: %#v", agents)
	}
	if agents[0].Name != "opencode" || agents[0].Task != "previous session" || agents[0].Status != Completed || !agents[0].History || agents[0].Source != "opencode-history" || agents[0].Confidence != 80 {
		t.Fatalf("unexpected opencode agent: %#v", agents[0])
	}
}

func TestOpenCodeHistoryUsesT3ThreadTitle(t *testing.T) {
	sessions := []openCodeSession{{
		ID:        "ses_1",
		Title:     "T3 Code 43efc32e-83e3-4519-be09-a7c1ebfbf1f9",
		Directory: "/tmp/project",
		Created:   time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).UnixMilli(),
		Updated:   time.Date(2026, 6, 14, 12, 5, 0, 0, time.UTC).UnixMilli(),
	}}
	t3Threads := t3ThreadIndex{ByThreadID: map[string]t3ThreadRecord{
		"43efc32e-83e3-4519-be09-a7c1ebfbf1f9": {Title: "Project review and usage guidance"},
	}}

	agents := openCodeHistoryAgentsFromSessions(sessions, "/tmp/project", t3Threads)
	if len(agents) != 1 {
		t.Fatalf("expected 1 opencode agent, got %#v", agents)
	}
	if agents[0].Task != "Project review and usage guidance" || agents[0].Source != "opencode-history+t3" {
		t.Fatalf("expected T3 title, got %#v", agents[0])
	}
}

func TestOpenCodeHistoryUsesT3ProviderSessionID(t *testing.T) {
	sessions := []openCodeSession{{
		ID:        "ses_1",
		Title:     "Raw OpenCode title",
		Directory: "/tmp/project",
		Created:   time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).UnixMilli(),
		Updated:   time.Date(2026, 6, 14, 12, 5, 0, 0, time.UTC).UnixMilli(),
	}}
	t3Threads := t3ThreadIndex{ByProviderSessionID: map[string]t3ThreadRecord{
		"ses_1": {Title: "Provider linked title"},
	}}

	agents := openCodeHistoryAgentsFromSessions(sessions, "/tmp/project", t3Threads)
	if len(agents) != 1 {
		t.Fatalf("expected 1 opencode agent, got %#v", agents)
	}
	if agents[0].Task != "Provider linked title" || agents[0].Source != "opencode-history+t3" {
		t.Fatalf("expected T3 provider-linked title, got %#v", agents[0])
	}
}

func TestParseOpenCodeSessionsJSON(t *testing.T) {
	sessions, err := parseOpenCodeSessionsJSON([]byte(`[{"id":"ses_1","title":"Do work","directory":"/tmp/project","created":1781354216989,"updated":1781397407111,"projectId":"global"}]`))
	if err != nil {
		t.Fatalf("parseOpenCodeSessionsJSON returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_1" || sessions[0].Directory != "/tmp/project" || sessions[0].Updated != 1781397407111 {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}
}
