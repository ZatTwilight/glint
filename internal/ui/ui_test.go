package ui

import (
	"strings"
	"testing"

	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/workspace"
)

func TestMoveWorkspaceSelectionSkipsAgents(t *testing.T) {
	m := New(State{Workspaces: []workspace.Workspace{
		{Name: "alpha", Path: "/tmp/alpha", Agents: []agent.Agent{{Name: "pi"}, {Name: "claude"}}},
		{Name: "bravo", Path: "/tmp/bravo", Agents: []agent.Agent{{Name: "pi"}}},
		{Name: "charlie", Path: "/tmp/charlie"},
	}}, nil)

	m.moveWorkspaceSelection(1)
	if got := m.visibleItems()[m.selected].Workspace.Name; got != "bravo" {
		t.Fatalf("next project selected %q, want bravo", got)
	}

	m.moveSelection(1)
	if m.visibleItems()[m.selected].Kind != kindAgent {
		t.Fatalf("setup failed: expected an agent selection")
	}

	m.moveWorkspaceSelection(1)
	if got := m.visibleItems()[m.selected].Workspace.Name; got != "charlie" {
		t.Fatalf("next project from agent selected %q, want charlie", got)
	}

	m.moveWorkspaceSelection(-1)
	if got := m.visibleItems()[m.selected].Workspace.Name; got != "bravo" {
		t.Fatalf("previous project selected %q, want bravo", got)
	}
}

func TestPaletteSearchMatchesMultiWordWorkQueries(t *testing.T) {
	m := New(State{Workspaces: []workspace.Workspace{{
		Name: "glint",
		Path: "/tmp/glint",
		Agents: []agent.Agent{
			{Name: "pi", Task: "Add dotfiles discovery to the workspace list"},
			{Name: "pi", Task: "Fix command palette rendering"},
		},
	}}}, nil)

	m.paletteQuery = "adding dotfiles"
	targets := m.paletteTargets()
	if len(targets) == 0 {
		t.Fatal("palette returned no targets for multi-word query")
	}
	if got := targets[0].Title; !strings.Contains(got, "Add dotfiles") {
		t.Fatalf("top palette target = %q, want dotfiles agent", got)
	}
}

func TestSessionForPathOrNameIgnoresMismatchedNamedSession(t *testing.T) {
	m := New(State{Multiplexer: multiplexer.Info{Kind: multiplexer.Zellij, Sessions: []multiplexer.Session{
		{Name: "feature", Path: "/tmp/other"},
		{Name: "feature-2", Path: "/tmp/feature"},
	}}}, nil)

	session := m.sessionForPathOrName("/tmp/feature", "feature")
	if session == nil {
		t.Fatal("expected path-matching session")
	}
	if got, want := session.Name, "feature-2"; got != want {
		t.Fatalf("session = %q, want %q", got, want)
	}
}

func TestSessionNameForNewPathAvoidsMismatchedName(t *testing.T) {
	m := New(State{Multiplexer: multiplexer.Info{Kind: multiplexer.Zellij, Sessions: []multiplexer.Session{
		{Name: "feature", Path: "/tmp/other"},
	}}}, nil)

	if got, want := m.sessionNameForNewPath("/tmp/feature", "feature"), "feature-2"; got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}

func TestAgentListIsCappedAndScrollsWithinWorkspace(t *testing.T) {
	agents := make([]agent.Agent, 7)
	for idx := range agents {
		agents[idx] = agent.Agent{Name: "pi", Task: string(rune('a' + idx))}
	}
	m := New(State{Workspaces: []workspace.Workspace{
		{Name: "alpha", Path: "/tmp/alpha", Agents: agents},
		{Name: "bravo", Path: "/tmp/bravo"},
	}}, nil)

	items := m.visibleItems()
	if got, want := len(items), 7; got != want {
		t.Fatalf("visible item count = %d, want %d", got, want)
	}
	if got, want := items[5].AgentIndex, 4; got != want {
		t.Fatalf("last visible agent index = %d, want %d", got, want)
	}

	m.selected = 5
	m.moveSelection(1)
	items = m.visibleItems()
	if got, want := items[m.selected].AgentIndex, 5; got != want {
		t.Fatalf("selected agent after internal scroll = %d, want %d", got, want)
	}
	if got, want := m.agentOffsets["/tmp/alpha"], 1; got != want {
		t.Fatalf("agent offset = %d, want %d", got, want)
	}

	m.moveSelection(-1)
	items = m.visibleItems()
	if got, want := items[m.selected].AgentIndex, 4; got != want {
		t.Fatalf("selected agent after reverse scroll = %d, want %d", got, want)
	}
}
