package ui

import (
	"testing"

	"github.com/ZatTwilight/glint/internal/agent"
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
