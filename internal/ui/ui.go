package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kait/agentbar/internal/multiplexer"
	"github.com/kait/agentbar/internal/workspace"
)

type State struct {
	Multiplexer    multiplexer.Info
	Workspaces     []workspace.Workspace
	WorkspaceRoots []string
}

type RefreshFunc func() (State, error)

type Model struct {
	state   State
	list    list.Model
	status  string
	refresh RefreshFunc
}

type itemKind int

const (
	kindHeader itemKind = iota
	kindWorkspace
	kindSession
)

type item struct {
	title       string
	desc        string
	kind        itemKind
	sessionName string
	path        string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

var (
	panelStyle = lipgloss.NewStyle().Padding(1, 2)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Padding(0, 1)
)

func New(state State, refresh RefreshFunc) Model {
	sessionsByName := state.Multiplexer.SessionByName()
	sessionsByPath := state.Multiplexer.SessionByPath()
	workspaceSessions := make(map[string]bool, len(state.Workspaces))
	items := make([]list.Item, 0, len(state.Workspaces)+len(state.Multiplexer.Sessions)+1)

	for _, ws := range state.Workspaces {
		title := "▸ " + ws.Name
		desc := ws.Path
		if ws.IsWorktree {
			title = "  └ " + ws.Name
		}

		session, active := sessionsByName[ws.Name]
		if !active {
			session, active = sessionsByPath[filepath.Clean(ws.Path)]
		}
		if active {
			workspaceSessions[session.Name] = true
			title = "● " + ws.Name
			if ws.IsWorktree {
				title = "  └● " + ws.Name
			}
			desc = sessionSummary(session)
		}

		items = append(items, item{title: title, desc: desc, kind: kindWorkspace, sessionName: sessionNameForWorkspace(ws, session, active), path: ws.Path})
	}

	other := otherSessions(state.Multiplexer.Sessions, workspaceSessions)
	for i := len(other) - 1; i >= 0; i-- {
		session := other[i]
		row := item{title: "● " + session.Name, desc: sessionSummary(session), kind: kindSession, sessionName: session.Name, path: session.Path}
		items = append(items[:1], append([]list.Item{row}, items[1:]...)...)
	}

	l := list.New(items, list.NewDefaultDelegate(), 34, 24)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)

	return Model{state: state, list: l, status: "Enter switches or creates tmux sessions", refresh: refresh}
}

func (m Model) Init() tea.Cmd { return refreshTick() }

type refreshMsg struct {
	state State
	err   error
}

type tickMsg time.Time

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if m.list.FilterState() != list.Unfiltered {
				m.list.SetFilterState(list.Unfiltered)
				return m, nil
			}
			return m, tea.Quit
		case "enter":
			return m.activateSelected()
		}
	case tea.WindowSizeMsg:
		m.list.SetSize(max(20, msg.Width-4), max(10, msg.Height-6))
	case tickMsg:
		return m, tea.Batch(m.doRefresh(), refreshTick())
	case refreshMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Refresh failed: %v", msg.err)
			return m, nil
		}
		idx := m.list.Index()
		m.state = msg.state
		m.rebuildItems()
		if len(m.list.Items()) > 0 {
			m.list.Select(min(idx, len(m.list.Items())-1))
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) doRefresh() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{state: m.state}
		// if m.refresh == nil {
		// }
		// state, err := m.refresh()
		// return refreshMsg{state: state, err: err}
	}
}

func (m *Model) rebuildItems() {
	items := New(m.state, m.refresh).list.Items()
	m.list.SetItems(items)
}

func (m Model) View() tea.View {
	badge := badgeStyle.Render(strings.ToUpper(string(m.state.Multiplexer.Kind)))
	header := fmt.Sprintf("%s  %d projects", badge, len(m.state.Workspaces))
	footer := helpStyle.Render("↑/↓ move · scroll/click · Enter switch/create · / filter · q quit")
	v := tea.NewView(panelStyle.Render(header + "\n" + m.list.View() + "\n" + mutedStyle.Render(m.status) + "\n" + footer))
	v.AltScreen = true
	return v
}

func (m Model) activateSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok || selected.kind == kindHeader {
		m.status = "Pick a project or session"
		return m, nil
	}

	if selected.kind == kindWorkspace && !m.state.Multiplexer.SessionNames()[selected.sessionName] {
		if err := multiplexer.NewSession(m.state.Multiplexer.Kind, selected.sessionName, selected.path); err != nil {
			m.status = fmt.Sprintf("Create failed: %v", err)
			return m, nil
		}
	}

	if err := multiplexer.SwitchSession(m.state.Multiplexer.Kind, selected.sessionName); err != nil {
		m.status = fmt.Sprintf("Switch failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Switched to %s", selected.sessionName)
	return m, nil
}

func sessionNameForWorkspace(ws workspace.Workspace, session multiplexer.Session, active bool) string {
	if active {
		return session.Name
	}
	if ws.IsWorktree && ws.ParentName != "" {
		return ws.ParentName + "/" + ws.Name
	}
	return ws.Name
}

func otherSessions(sessions []multiplexer.Session, workspaceSessions map[string]bool) []multiplexer.Session {
	other := make([]multiplexer.Session, 0, len(sessions))
	for _, session := range sessions {
		if !workspaceSessions[session.Name] {
			other = append(other, session)
		}
	}
	return other
}

func sessionSummary(session multiplexer.Session) string {
	parts := []string{}
	if session.Attached {
		parts = append(parts, "attached")
	} else {
		parts = append(parts, "detached")
	}
	if session.Windows > 0 {
		parts = append(parts, fmt.Sprintf("%d window%s", session.Windows, plural(session.Windows)))
	}
	if !session.Activity.IsZero() {
		parts = append(parts, relativeTime(session.Activity))
	}
	if session.Path != "" {
		parts = append(parts, filepath.Base(session.Path))
	}
	return strings.Join(parts, " · ")
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
