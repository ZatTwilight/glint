package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

func TestOpeningPaletteResetsViewportOffset(t *testing.T) {
	workspaces := make([]workspace.Workspace, 12)
	for idx := range workspaces {
		workspaces[idx] = workspace.Workspace{Name: string(rune('a' + idx)), Path: "/tmp/project" + string(rune('a'+idx))}
	}
	m := New(State{Workspaces: workspaces}, nil)
	m.viewport.SetHeight(4)
	m.viewport.SetWidth(80)
	m.viewport.SetYOffset(5)

	model, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	m = model.(Model)

	if got := m.viewport.YOffset(); got != 0 {
		t.Fatalf("palette viewport offset = %d, want 0", got)
	}
}

func TestStandalonePaletteKeepsPromptVisibleWhenFirstItemSelected(t *testing.T) {
	m := NewPalette(State{Workspaces: []workspace.Workspace{{Name: "alpha", Path: "/tmp/alpha"}}}, nil)
	m.viewport.SetHeight(4)
	m.viewport.SetWidth(80)
	m.renderContent()
	m.viewport.SetYOffset(1)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = model.(Model)

	if got := m.viewport.YOffset(); got != 0 {
		t.Fatalf("standalone palette viewport offset = %d, want 0", got)
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

func TestSessionForPathOrNameMatchesExitedZellijSessionByName(t *testing.T) {
	m := New(State{Multiplexer: multiplexer.Info{Kind: multiplexer.Zellij, Sessions: []multiplexer.Session{
		{Name: "zlast", Exited: true},
	}}}, nil)

	session := m.sessionForPathOrName("/tmp/zlast", "zlast")
	if session == nil {
		t.Fatal("expected exited named session")
	}
	if got, want := session.Name, "zlast"; got != want {
		t.Fatalf("session = %q, want %q", got, want)
	}
	if got, want := m.sessionNameForNewPath("/tmp/zlast", "zlast"), "zlast"; got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
}

func TestPaletteIncludesUnscannedMultiplexerSessions(t *testing.T) {
	m := New(State{
		Multiplexer: multiplexer.Info{Kind: multiplexer.Tmux, Sessions: []multiplexer.Session{{
			Name:     "scratch",
			Path:     "/tmp/scratch",
			Attached: true,
			Activity: time.Now(),
		}}},
		Palette: MovementPaletteOptions(),
	}, nil)

	targets := m.paletteTargets()
	if len(targets) != 1 {
		t.Fatalf("palette target count = %d, want 1", len(targets))
	}
	if got, want := targets[0].Label, "session"; got != want {
		t.Fatalf("target label = %q, want %q", got, want)
	}
	if got, want := targets[0].Title, "scratch"; got != want {
		t.Fatalf("target title = %q, want %q", got, want)
	}
	if got, want := targets[0].Action.Kind, paletteActionSwitchSession; got != want {
		t.Fatalf("target action = %v, want %v", got, want)
	}
}

func TestPaletteOrdersSessionBackedWorkspacesFirstWhenUnfiltered(t *testing.T) {
	m := New(State{
		Multiplexer: multiplexer.Info{Kind: multiplexer.Tmux, Sessions: []multiplexer.Session{{Name: "bravo", Path: "/tmp/bravo"}}},
		Workspaces: []workspace.Workspace{
			{Name: "alpha", Path: "/tmp/alpha"},
			{Name: "bravo", Path: "/tmp/bravo"},
			{Name: "charlie", Path: "/tmp/charlie"},
		},
	}, nil)

	targets := m.paletteTargets()
	if len(targets) < 3 {
		t.Fatalf("palette target count = %d, want at least 3", len(targets))
	}
	for idx, want := range []string{"bravo", "alpha", "charlie"} {
		if got := targets[idx].Title; got != want {
			t.Fatalf("target %d = %q, want %q", idx, got, want)
		}
	}
}

func TestPaletteDoesNotDuplicateWorkspaceBackedSessions(t *testing.T) {
	m := New(State{
		Multiplexer: multiplexer.Info{Kind: multiplexer.Tmux, Sessions: []multiplexer.Session{
			{Name: "glint", Path: "/tmp/glint"},
			{Name: "glint-subdir", Path: "/tmp/glint/internal"},
			{Name: "glint"},
			{Name: "glint", Path: "/tmp/other"},
			{Name: multiplexer.ShelfSessionName, Path: "/tmp/shelf"},
		}},
		Workspaces: []workspace.Workspace{{Name: "glint", Path: "/tmp/glint"}},
	}, nil)

	targets := m.paletteTargets()
	for _, target := range targets {
		if target.Label == "session" {
			t.Fatalf("unexpected session target for workspace-backed session: %+v", target)
		}
	}
}

func TestLocalPaletteDoesNotDuplicateSessionForProjectRef(t *testing.T) {
	m := New(State{
		Multiplexer: multiplexer.Info{Kind: multiplexer.Tmux, Sessions: []multiplexer.Session{{Name: "feature", Path: "/tmp/repo/feature"}}},
		Workspaces:  []workspace.Workspace{{Name: "repo", Path: "/tmp/repo", VCS: workspace.VCSGit}},
		CurrentPath: "/tmp/repo",
		Palette:     MovementPaletteOptions(),
	}, nil)

	base := []paletteTarget{{Item: visibleItem{Kind: kindWorkspace, Workspace: workspace.Workspace{Name: "feature", Path: "/tmp/repo/feature"}}}}
	if targets := m.multiplexerSessionPaletteTargets(m.state.Palette, base); len(targets) != 0 {
		t.Fatalf("session targets = %+v, want none", targets)
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
