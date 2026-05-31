package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/util"
	"github.com/ZatTwilight/glint/internal/workspace"
)

type State struct {
	Multiplexer    multiplexer.Info
	Workspaces     []workspace.Workspace
	WorkspaceRoots []string
	CurrentWindow  string
	Theme          theme.Theme
}

type RefreshFunc func() (State, error)

type Model struct {
	state    State
	viewport viewport.Model
	selected int
	status   string
	refresh  RefreshFunc
	styles   theme.Styles
	renderer itemRenderer
	spans    []itemSpan
}

type itemKind int

const (
	kindWorkspace itemKind = iota
	kindAgent
)

type visibleItem struct {
	Kind       itemKind
	Workspace  workspace.Workspace
	AgentIndex int
}

type itemSpan struct {
	start int
	end   int
}

func New(state State, refresh RefreshFunc) Model {
	styles := theme.NewStyles(state.Theme)
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 1
	vp.Style = styles.Body

	status := "Enter switches or creates sessions"
	if len(state.Workspaces) == 0 && refresh != nil {
		status = "Loading workspaces…"
	}

	m := Model{
		state:    state,
		viewport: vp,
		status:   status,
		refresh:  refresh,
		styles:   styles,
		renderer: newItemRenderer(state.Theme),
	}
	m.rebuildItems()
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(initialRefreshTick(), refreshTick(), animationTick()) }

type refreshMsg struct {
	state State
	err   error
}

type refreshTickMsg time.Time
type animationTickMsg time.Time

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			return m.activateSelected()
		case " ", "tab", "c":
			m.toggleSelected()
			return m, nil
		case "up", "k":
			m.moveSelection(-1)
			return m, nil
		case "down", "j":
			m.moveSelection(1)
			return m, nil
		case "home":
			m.selected = 0
			m.renderContent()
			m.ensureSelectedVisible()
			return m, nil
		case "end":
			m.selected = max(0, len(m.visibleItems())-1)
			m.renderContent()
			m.ensureSelectedVisible()
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.viewport.SetWidth(max(20, msg.Width-4))
		headerHeight := lipgloss.Height(m.viewHeader())
		footerHeight := lipgloss.Height(m.viewHeader())
		verticalMarginHeight := headerHeight + footerHeight
		m.viewport.SetHeight(msg.Height - verticalMarginHeight)
		m.renderContent()
	case refreshTickMsg:
		return m, tea.Batch(m.doRefresh(), refreshTick())
	case animationTickMsg:
		m.renderContent()
		return m, animationTick()
	case refreshMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Refresh failed: %v", msg.err)
			return m, nil
		}
		if m.status == "Loading workspaces…" {
			m.status = "Enter switches or creates sessions"
		}
		idx := m.selected
		m.state = msg.state
		m.rebuildItems()
		if len(m.visibleItems()) > 0 {
			m.selected = min(idx, len(m.visibleItems())-1)
			m.renderContent()
			m.ensureSelectedVisible()
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func initialRefreshTick() tea.Cmd {
	return tea.Tick(time.Millisecond, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

func refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

func animationTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return animationTickMsg(t) })
}

func (m Model) doRefresh() tea.Cmd {
	return func() tea.Msg {
		if m.refresh == nil {
			return refreshMsg{state: m.state}
		}
		state, err := m.refresh()
		return refreshMsg{state: state, err: err}
	}
}

func (m *Model) rebuildItems() {
	if m.selected >= len(m.state.Workspaces) {
		m.selected = max(0, len(m.state.Workspaces)-1)
	}
	m.renderContent()
}

func (m Model) getItems() []workspace.Workspace {
	return util.Filter(m.state.Workspaces, func(w workspace.Workspace) bool {
		return w.GitType != 1
	})
}

func (m Model) visibleItems() []visibleItem {
	workspaces := m.getItems()
	items := make([]visibleItem, 0, len(workspaces))
	for _, ws := range workspaces {
		items = append(items, visibleItem{Kind: kindWorkspace, Workspace: ws, AgentIndex: -1})
		if len(ws.Agents) == 0 || m.renderer.IsCollapsed(ws.Path) {
			continue
		}
		for idx := range ws.Agents {
			items = append(items, visibleItem{Kind: kindAgent, Workspace: ws, AgentIndex: idx})
		}
	}
	return items
}

func (m Model) viewportInnerWidth() int {
	return max(0, m.viewport.Width()-m.viewport.Style.GetHorizontalFrameSize())
}

func (m Model) viewportInnerHeight() int {
	return max(0, m.viewport.Height()-m.viewport.Style.GetVerticalFrameSize())
}

func (m *Model) renderContent() {
	lines := []string{}
	m.spans = make([]itemSpan, 0, len(m.state.Workspaces))
	items := m.visibleItems()
	if len(items) == 0 && m.status == "Loading workspaces…" {
		lines = append(lines, "Loading workspaces…")
	}
	for idx, item := range items {
		start := len(lines)
		rendered := m.renderer.RenderVisible(item, idx == m.selected, m.viewportInnerWidth(), m.state.CurrentWindow)
		lines = append(lines, strings.Split(rendered, "\n")...)

		if idx != len(items)-1 {
			next := items[idx+1]
			if item.Kind == kindWorkspace && next.Kind == kindWorkspace {
				lines = append(lines, "")
			}
			if item.Kind == kindAgent && next.Kind == kindWorkspace {
				lines = append(lines, "")
			}
		}
		end := len(lines)
		m.spans = append(m.spans, itemSpan{start: start, end: end})
	}
	lines = append(lines, "", "", "")

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m *Model) moveSelection(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}
	m.selected = min(max(0, m.selected+delta), len(items)-1)
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m *Model) toggleSelected() {
	items := m.visibleItems()
	if len(items) == 0 || m.selected < 0 || m.selected >= len(items) {
		return
	}
	item := items[m.selected]
	if item.Kind == kindAgent {
		item = visibleItem{Kind: kindWorkspace, Workspace: item.Workspace}
	}
	if len(item.Workspace.Agents) == 0 {
		m.status = "No agents in " + item.Workspace.Name
		return
	}
	m.renderer.collapsed[item.Workspace.Path] = !m.renderer.collapsed[item.Workspace.Path]
	if m.selected >= len(m.visibleItems()) {
		m.selected = max(0, len(m.visibleItems())-1)
	}
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m *Model) ensureSelectedVisible() {
	if m.selected < 0 || m.selected >= len(m.spans) {
		return
	}
	span := m.spans[m.selected]
	top := m.viewport.YOffset()
	bottom := top + m.viewportInnerHeight()
	if span.start < top {
		m.viewport.SetYOffset(span.start)
		return
	}
	if span.end > bottom {
		m.viewport.SetYOffset(span.end - m.viewportInnerHeight())
	}
}

func (m Model) viewHeader() string {
	badge := m.styles.Badge.Render(strings.ToUpper(string(m.state.Multiplexer.Kind)))
	header := fmt.Sprintf("%s  %d projects", badge, len(m.state.Workspaces))
	return m.styles.Header.Render(header)
}

func (m Model) viewFooter() string {
	content := fmt.Sprintf("status: %s\n↑/↓ move · c/space collapse · Enter switch/create · q quit", m.status)
	return m.styles.Help.Render(content)
}

func (m Model) View() tea.View {
	body := m.viewport.View()
	v := tea.NewView(m.viewHeader() + body + m.viewFooter())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) activateSelected() (tea.Model, tea.Cmd) {
	items := m.visibleItems()
	if len(items) == 0 || m.selected < 0 || m.selected >= len(items) {
		m.status = "Pick a project"
		return m, nil
	}
	item := items[m.selected]
	if item.Kind == kindAgent {
		ag := item.Workspace.Agents[item.AgentIndex]
		if ag.Pane != "" {
			if err := multiplexer.SwitchPaneById(m.state.Multiplexer.Kind, ag.Pane); err != nil {
				m.status = fmt.Sprintf("Switch failed: %v -- %+v, %+v, %+v", err, ag.Session, ag.Window, ag.Pane)
				return m, nil
			}
			m.status = fmt.Sprintf("Switched to %s in %s:%s", ag.Name, ag.Session, ag.Pane)
			return m, nil
		}
		return m.activateWorkspace(item.Workspace)
	}
	return m.activateWorkspace(item.Workspace)
}

func (m Model) activateWorkspace(selected workspace.Workspace) (tea.Model, tea.Cmd) {
	session := m.sessionFromWorkspace(selected)
	sessionName := selected.Name

	if session != nil {
		sessionName = session.Name
	} else if !m.state.Multiplexer.SessionNames()[selected.Name] {
		if err := multiplexer.NewSession(m.state.Multiplexer.Kind, selected.Name, selected.Path); err != nil {
			m.status = fmt.Sprintf("Create failed: %v", err)
			return m, nil
		}
	}

	if err := multiplexer.SwitchSession(m.state.Multiplexer.Kind, sessionName); err != nil {
		m.status = fmt.Sprintf("Switch failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Switched to %s", sessionName)
	return m, nil
}

func (m Model) sessionFromWorkspace(ws workspace.Workspace) *multiplexer.Session {
	sessionNames := m.state.Multiplexer.SessionByName()
	sessionPaths := m.state.Multiplexer.SessionByPath()

	session, active := sessionNames[ws.Name]
	if !active {
		session, active = sessionPaths[filepath.Clean(ws.Path)]
	}
	if !active {
		return nil
	} else {
		return &session
	}
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
	if t.IsZero() {
		return ""
	}
	d := max(time.Since(t), 0)
	if d < time.Minute {
		return "now"
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
