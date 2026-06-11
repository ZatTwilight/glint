package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/util"
	"github.com/ZatTwilight/glint/internal/vcs"
	"github.com/ZatTwilight/glint/internal/workspace"
	"github.com/sahilm/fuzzy"
)

type State struct {
	Multiplexer    multiplexer.Info
	Workspaces     []workspace.Workspace
	WorkspaceRoots []string
	CurrentWindow  string
	CurrentSession string
	CurrentPath    string
	SidebarMode    bool
	Theme          theme.Theme
	Spinner        string
	Palette        PaletteOptions
}

type PaletteOptions struct {
	IncludeWorkspaces       bool
	IncludeAgents           bool
	IncludeNewAgent         bool
	IncludeShelveMain       bool
	IncludeCreateWorktree   bool
	IncludeCleanupWorktrees bool
	LocalFirst              bool
}

func DefaultPaletteOptions() PaletteOptions {
	return PaletteOptions{
		IncludeWorkspaces:       true,
		IncludeAgents:           true,
		IncludeNewAgent:         true,
		IncludeShelveMain:       true,
		IncludeCreateWorktree:   true,
		IncludeCleanupWorktrees: true,
	}
}

func MovementPaletteOptions() PaletteOptions {
	return PaletteOptions{
		IncludeWorkspaces:       true,
		IncludeCreateWorktree:   true,
		IncludeCleanupWorktrees: true,
		LocalFirst:              true,
	}
}

type RefreshFunc func() (State, error)

type Model struct {
	state                 State
	viewport              viewport.Model
	selected              int
	status                string
	refresh               RefreshFunc
	styles                theme.Styles
	width                 int
	renderer              itemRenderer
	spans                 []itemSpan
	searchActive          bool
	searchQuery           string
	paletteActive         bool
	paletteStandalone     bool
	paletteFiltering      bool
	paletteQuery          string
	localPaletteCachePath string
	localPaletteCache     []paletteTarget
	worktreeFlow          worktreeFlow
	cleanupFlow           cleanupFlow
	agentOffsets          map[string]int
}

type itemKind int

const (
	kindWorkspace itemKind = iota
	kindAgent
)

const maxVisibleAgents = 5

type visibleItem struct {
	Kind       itemKind
	Workspace  workspace.Workspace
	AgentIndex int
	AgentStart int
	AgentEnd   int
}

type itemSpan struct {
	start int
	end   int
}

type paletteTarget struct {
	Item     visibleItem
	Action   paletteAction
	Label    string
	Title    string
	Subtitle string
	Search   string
	Score    int
}

type paletteActionKind int

const (
	paletteActionNone paletteActionKind = iota
	paletteActionNewAgent
	paletteActionShelveMain
	paletteActionCreateWorktree
	paletteActionCleanupWorktrees
)

type paletteAction struct {
	Kind      paletteActionKind
	Workspace workspace.Workspace
	Source    vcs.Source
}

type worktreeFlowStep int

const (
	worktreeStepNone worktreeFlowStep = iota
	worktreeStepBranch
	worktreeStepName
	worktreeStepPath
	worktreeStepConfirmBranch
	worktreeStepNewBranch
)

type worktreeFlow struct {
	Active        bool
	Step          worktreeFlowStep
	Backend       vcs.Backend
	Workspace     workspace.Workspace
	Sources       []vcs.Source
	CheckedOut    map[string]bool
	Query         string
	Pristine      bool
	Selected      int
	Source        vcs.Source
	WorkspaceName string
	WorkspacePath string
	NewBranch     bool
}

type cleanupFlow struct {
	Active        bool
	Backend       vcs.Backend
	Workspace     workspace.Workspace
	WorkspaceRefs []vcs.WorkspaceRef
	Query         string
	Selected      int
	Confirm       bool
	DirtyConfirm  bool
}

func New(state State, refresh RefreshFunc) Model {
	return newModel(state, refresh, false)
}

func NewPalette(state State, refresh RefreshFunc) Model {
	return newModel(state, refresh, true)
}

func newModel(state State, refresh RefreshFunc, paletteMode bool) Model {
	styles := theme.NewStyles(state.Theme)
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 1
	vp.Style = styles.Body

	status := "Enter switches or creates sessions"
	if paletteMode {
		status = "Command palette"
	}
	if len(state.Workspaces) == 0 && refresh != nil {
		status = "Loading workspaces…"
	}

	m := Model{
		state:             state,
		viewport:          vp,
		status:            status,
		refresh:           refresh,
		styles:            styles,
		renderer:          newItemRenderer(state.Theme, loadCollapsedProjects(), state.Spinner),
		paletteActive:     paletteMode,
		paletteStandalone: paletteMode,
		agentOffsets:      map[string]int{},
	}
	m.rebuildItems()
	if paletteMode {
		m.updatePaletteStatus()
	}
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
		if m.cleanupFlow.Active {
			return m.updateCleanupFlow(msg)
		}
		if m.worktreeFlow.Active {
			return m.updateWorktreeFlow(msg)
		}
		if m.paletteActive {
			return m.updatePalette(msg)
		}
		if m.searchActive {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			return m.activateSelected()
		case "ctrl+x":
			return m.removeSession()
		case "/":
			m.searchActive = true
			m.status = "Search workspaces"
			m.selected = 0
			m.renderContent()
			m.ensureSelectedVisible()
			return m, nil
		case "ctrl+p":
			m.paletteActive = true
			m.status = "Command palette"
			m.selected = 0
			m.renderContent()
			m.ensureSelectedVisible()
			return m, nil
		case "ctrl+w":
			return m.startWorktreeFlowForSelected()
		case "ctrl+r":
			return m.startCleanupFlowForSelected()
		case " ", "space", "tab", "c":
			m.toggleSelected()
			return m, nil
		case "b":
			return m.shelveMainPane()
		case "n":
			return m.newAgentForSelected()
		case "s":
			m.renderer.CycleSpinner()
			m.status = fmt.Sprintf("Spinner: %s", m.renderer.SpinnerName())
			m.renderContent()
			return m, nil
		case "up", "k":
			m.moveSelection(-1)
			return m, nil
		case "down", "j":
			m.moveSelection(1)
			return m, nil
		case "[", "h":
			m.moveWorkspaceSelection(-1)
			return m, nil
		case "]", "l":
			m.moveWorkspaceSelection(1)
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
		m.width = msg.Width
		m.viewport.SetWidth(max(20, msg.Width))
		headerHeight := lipgloss.Height(m.viewHeader())
		footerHeight := lipgloss.Height(m.viewFooter())
		verticalMarginHeight := headerHeight + footerHeight
		m.viewport.SetHeight(max(1, msg.Height-verticalMarginHeight))
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
		paletteOptions := m.state.Palette
		m.state = msg.state
		m.state.Palette = paletteOptions
		m.rebuildItems()
		if m.searchActive {
			m.updateSearchStatus()
		}
		if m.paletteActive {
			m.updatePaletteStatus()
		}
		if m.currentItemCount() > 0 {
			m.selected = min(idx, m.currentItemCount()-1)
			m.renderContent()
			m.ensureSelectedVisible()
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if printableKey(msg.String()) {
		m.searchQuery += msg.String()
		m.selected = 0
		m.updateSearchStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.searchActive = false
		m.searchQuery = ""
		m.status = "Enter switches or creates sessions"
		m.selected = min(m.selected, max(0, len(m.visibleItems())-1))
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "enter":
		return m.activateSelected()
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
	case "backspace", "ctrl+h":
		m.searchQuery = dropLastRune(m.searchQuery)
		m.selected = 0
		m.updateSearchStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "ctrl+u":
		m.searchQuery = ""
		m.selected = 0
		m.updateSearchStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "space":
		m.searchQuery += " "
		m.selected = 0
		m.updateSearchStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	}

	return m, nil
}

func (m *Model) updateSearchStatus() {
	count := len(m.visibleItems())
	if strings.TrimSpace(m.searchQuery) == "" {
		m.status = "Search workspaces"
		return
	}
	m.status = fmt.Sprintf("Search: /%s · %d match%s", m.searchQuery, count, plural(count))
}

func (m Model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.paletteFiltering && printableKey(msg.String()) {
		m.paletteQuery += msg.String()
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		if m.paletteFiltering {
			m.paletteFiltering = false
			return m, nil
		}
		if m.paletteStandalone {
			return m, tea.Quit
		}
		m.paletteActive = false
		m.paletteQuery = ""
		m.status = "Enter switches or creates sessions"
		m.selected = min(m.selected, max(0, len(m.visibleItems())-1))
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "enter":
		model, cmd := m.activatePaletteSelected()
		if m.paletteStandalone && !paletteModelNeedsInput(model) {
			return model, tea.Batch(cmd, tea.Quit)
		}
		return model, cmd
	case "tab", "h", "l":
		m.state.Palette.LocalFirst = !m.state.Palette.LocalFirst
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		return m, nil
	case "ctrl+d", "ctrl+x":
		return m.cleanupPaletteSelectedWorkspace()
	case "up", "k":
		m.movePaletteSelection(-1)
		return m, nil
	case "down", "j":
		m.movePaletteSelection(1)
		return m, nil
	case "home":
		m.selected = 0
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "end":
		m.selected = max(0, len(m.paletteTargets())-1)
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "/":
		m.paletteFiltering = true
		m.updatePaletteStatus()
		return m, nil
	case "backspace", "ctrl+h":
		if !m.paletteFiltering {
			return m, nil
		}
		m.paletteQuery = dropLastRune(m.paletteQuery)
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "ctrl+u":
		m.paletteFiltering = true
		m.paletteQuery = ""
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "space":
		if !m.paletteFiltering {
			return m, nil
		}
		m.paletteQuery += " "
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	}

	return m, nil
}

func paletteModelNeedsInput(model tea.Model) bool {
	m, ok := model.(Model)
	return ok && (m.worktreeFlow.Active || m.cleanupFlow.Active)
}

func (m *Model) updatePaletteStatus() {
	count := len(m.paletteTargets())
	mode := "global"
	if m.state.Palette.LocalFirst {
		mode = "local"
	}
	if strings.TrimSpace(m.paletteQuery) == "" {
		filterHint := "press / to filter"
		if m.paletteFiltering {
			filterHint = "filtering"
		}
		m.status = fmt.Sprintf("Command palette (%s) · %d target%s · %s", mode, count, plural(count), filterHint)
		return
	}
	m.status = fmt.Sprintf("Palette (%s): %s · %d match%s", mode, m.paletteQuery, count, plural(count))
}

func printableKey(key string) bool {
	runes := []rune(key)
	return len(runes) == 1 && unicode.IsPrint(runes[0])
}

func dropLastRune(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func (m Model) startWorktreeFlowForSelected() (tea.Model, tea.Cmd) {
	ws, ok := m.selectedWorkspace()
	if !ok {
		m.status = "Pick a project"
		return m, nil
	}
	return m.startWorktreeFlow(ws)
}

func (m Model) startCleanupFlowForSelected() (tea.Model, tea.Cmd) {
	ws, ok := m.selectedWorkspace()
	if !ok {
		m.status = "Pick a project"
		return m, nil
	}
	return m.startCleanupFlow(ws)
}

func (m Model) startCleanupFlow(ws workspace.Workspace) (tea.Model, tea.Cmd) {
	backend := vcs.ForPath(ws.Path)
	worktrees, err := backend.WorkspaceRefs(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("%ss failed: %v", titleCase(vcsUnit(backend)), err)
		return m, nil
	}
	removable := []vcs.WorkspaceRef{}
	for _, wt := range worktrees {
		if isBareWorktree(wt) {
			continue
		}
		removable = append(removable, wt)
	}
	if len(removable) == 0 {
		m.status = "No removable projects"
		return m, nil
	}
	m.searchActive = false
	m.paletteActive = false
	m.cleanupFlow = cleanupFlow{Active: true, Backend: backend, Workspace: ws, WorkspaceRefs: removable}
	m.status = fmt.Sprintf("Clean up %ss: pick one to remove", vcsUnit(m.cleanupFlow.Backend))
	m.renderContent()
	m.ensureSelectedVisible()
	return m, nil
}

func (m Model) startWorktreeFlowFromSource(ws workspace.Workspace, source vcs.Source) (tea.Model, tea.Cmd) {
	model, cmd := m.startWorktreeFlow(ws)
	m2, ok := model.(Model)
	if !ok {
		return model, cmd
	}
	m2.worktreeFlow.Source = source
	m2.worktreeFlow.Step = worktreeStepName
	m2.worktreeFlow.Query = vcs.SuggestedWorktreeName(source.Name)
	m2.worktreeFlow.Pristine = true
	m2.status = titleCase(vcsUnit(m2.worktreeFlow.Backend)) + " name"
	m2.renderContent()
	return m2, cmd
}

func (m Model) startWorktreeFlow(ws workspace.Workspace) (tea.Model, tea.Cmd) {
	backend := vcs.ForPath(ws.Path)
	branches, err := backend.Sources(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("Branches failed: %v", err)
		return m, nil
	}
	if len(branches) == 0 {
		m.status = "No branches found"
		return m, nil
	}
	worktrees, err := backend.WorkspaceRefs(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("%ss failed: %v", titleCase(vcsUnit(backend)), err)
		return m, nil
	}
	checkedOut := map[string]bool{}
	for _, wt := range worktrees {
		checkedOut[normalizeBranchRef(wt.Source)] = true
	}
	m.searchActive = false
	m.paletteActive = false
	m.worktreeFlow = worktreeFlow{Active: true, Step: worktreeStepBranch, Backend: backend, Workspace: ws, Sources: branches, CheckedOut: checkedOut}
	m.status = fmt.Sprintf("Create %s: pick base %s", vcsUnit(m.worktreeFlow.Backend), vcsSourceName(m.worktreeFlow.Backend))
	m.renderContent()
	m.ensureSelectedVisible()
	return m, nil
}

func isBareWorktree(wt vcs.WorkspaceRef) bool {
	path := filepath.Clean(wt.Path)
	base := filepath.Base(path)
	return wt.Bare || base == ".bare" || strings.HasSuffix(path, string(filepath.Separator)+".bare")
}

func vcsUnit(backend vcs.Backend) string {
	if backend != nil && backend.Kind() == vcs.Jujutsu {
		return "workspace"
	}
	return "worktree"
}

func vcsUnitForWorkspace(ws workspace.Workspace) string {
	if ws.VCS == workspace.VCSJujutsu {
		return "workspace"
	}
	return "worktree"
}

func vcsSourceName(backend vcs.Backend) string {
	if backend != nil && backend.Kind() == vcs.Jujutsu {
		return "bookmark/revision"
	}
	return "branch"
}

func vcsCleanupVerb(backend vcs.Backend) string {
	if backend != nil && backend.Kind() == vcs.Jujutsu {
		return "Forget"
	}
	return "Remove"
}

func vcsCleanupVerbForWorkspace(ws workspace.Workspace) string {
	if ws.VCS == workspace.VCSJujutsu {
		return "Forget"
	}
	return "Remove"
}

func vcsCleanupPastTense(backend vcs.Backend) string {
	if backend != nil && backend.Kind() == vcs.Jujutsu {
		return "Forgot"
	}
	return "Removed"
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (m Model) updateCleanupFlow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.cleanupFlow.Confirm && printableKey(msg.String()) {
		m.cleanupFlow.Query += msg.String()
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.cleanupFlow = cleanupFlow{}
		m.status = "Enter switches or creates sessions"
		m.renderContent()
		return m, nil
	case "up", "k":
		m.moveCleanupSelection(-1)
		return m, nil
	case "down", "j":
		m.moveCleanupSelection(1)
		return m, nil
	case "backspace", "ctrl+h":
		m.cleanupFlow.Query = dropLastRune(m.cleanupFlow.Query)
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	case "ctrl+u":
		m.cleanupFlow.Query = ""
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	case " ", "space":
		m.cleanupFlow.Query += " "
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	case "y":
		if m.cleanupFlow.Confirm {
			if !m.cleanupFlow.DirtyConfirm {
				return m.confirmCleanupWorktree()
			}
			return m.removeCleanupWorktree(true)
		}
		m.cleanupFlow.Query += "y"
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	case "n":
		if m.cleanupFlow.Confirm {
			m.cleanupFlow.Confirm = false
			m.status = "Clean up cancelled"
			m.renderContent()
			return m, nil
		}
		m.cleanupFlow.Query += "n"
		m.cleanupFlow.Selected = 0
		m.renderContent()
		return m, nil
	case "enter":
		if m.cleanupFlow.Confirm {
			m.status = "Press y to remove or n to cancel"
			return m, nil
		}
		worktrees := m.filteredCleanupWorktrees()
		if len(worktrees) == 0 || m.cleanupFlow.Selected >= len(worktrees) {
			m.status = "Pick a project"
			return m, nil
		}
		m.cleanupFlow.WorkspaceRefs = []vcs.WorkspaceRef{worktrees[m.cleanupFlow.Selected]}
		m.cleanupFlow.Selected = 0
		m.cleanupFlow.Confirm = true
		m.status = "Confirm remove " + vcsUnit(m.cleanupFlow.Backend) + "? y/n"
		m.renderContent()
		return m, nil
	}
	return m, nil
}

func (m *Model) moveCleanupSelection(delta int) {
	if m.cleanupFlow.Confirm {
		return
	}
	worktrees := m.filteredCleanupWorktrees()
	if len(worktrees) == 0 {
		return
	}
	m.cleanupFlow.Selected = min(max(0, m.cleanupFlow.Selected+delta), len(worktrees)-1)
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m Model) mostRecentOtherSession(current string) (multiplexer.Session, bool) {
	var best multiplexer.Session
	ok := false
	for _, session := range m.state.Multiplexer.Sessions {
		if session.Name == current || session.Name == multiplexer.ShelfSessionName {
			continue
		}
		if !ok || session.Activity.After(best.Activity) {
			best = session
			ok = true
		}
	}
	return best, ok
}

func (m Model) filteredCleanupWorktrees() []vcs.WorkspaceRef {
	query := strings.TrimSpace(m.cleanupFlow.Query)
	worktrees := []vcs.WorkspaceRef{}
	for _, wt := range m.cleanupFlow.WorkspaceRefs {
		text := strings.Join([]string{wt.Path, wt.Source, filepath.Base(wt.Path)}, " ")
		if query == "" || fuzzyMatch(query, text) {
			worktrees = append(worktrees, wt)
		}
	}
	if query != "" {
		sort.SliceStable(worktrees, func(i, j int) bool {
			return weightedMatchScore(query, filepath.Base(worktrees[i].Path), 100) > weightedMatchScore(query, filepath.Base(worktrees[j].Path), 100)
		})
	}
	return worktrees
}

func (m Model) confirmCleanupWorktree() (tea.Model, tea.Cmd) {
	if len(m.cleanupFlow.WorkspaceRefs) == 0 {
		m.status = "Pick a project"
		return m, nil
	}
	wt := m.cleanupFlow.WorkspaceRefs[0]
	dirty, err := vcs.WorkspaceHasLocalChanges(m.cleanupFlow.Backend, wt.Path)
	if err != nil {
		m.status = fmt.Sprintf("Could not check changes: %v", err)
		return m, nil
	}
	if dirty {
		m.cleanupFlow.DirtyConfirm = true
		m.status = "Workspace has local changes. Press y again to remove anyway · n/Esc cancel"
		m.renderContent()
		return m, nil
	}
	return m.removeCleanupWorktree(false)
}

func (m Model) removeCleanupWorktree(force bool) (tea.Model, tea.Cmd) {
	if len(m.cleanupFlow.WorkspaceRefs) == 0 {
		m.status = "Pick a project"
		return m, nil
	}
	wt := m.cleanupFlow.WorkspaceRefs[0]
	if session := m.sessionForPathOrName(wt.Path, filepath.Base(wt.Path)); session != nil {
		if session.Name == multiplexer.ShelfSessionName {
			m.status = fmt.Sprintf("Can't remove %s with protected session %s", vcsUnit(m.cleanupFlow.Backend), session.Name)
			return m, nil
		}
		if session.Name == m.state.CurrentSession {
			replacement, ok := m.mostRecentOtherSession(session.Name)
			if !ok {
				m.status = fmt.Sprintf("Can't remove current session %s; no other session to switch to", session.Name)
				return m, nil
			}
			if err := multiplexer.SwitchSession(m.state.Multiplexer.Kind, replacement.Name); err != nil {
				m.status = fmt.Sprintf("Switch before remove failed: %v", err)
				return m, nil
			}
		}
		if err := multiplexer.KillSession(m.state.Multiplexer.Kind, session.Name); err != nil {
			m.status = fmt.Sprintf("Kill session failed: %v", err)
			return m, nil
		}
	}
	if err := m.cleanupFlow.Backend.RemoveWorkspace(m.cleanupFlow.Workspace.Path, wt.Path, force); err != nil {
		m.status = fmt.Sprintf("Remove failed: %v", err)
		return m, nil
	}
	backend := m.cleanupFlow.Backend
	m.cleanupFlow = cleanupFlow{}
	m.status = fmt.Sprintf("%s %s %s", vcsCleanupPastTense(backend), vcsUnit(backend), filepath.Base(wt.Path))
	return m, m.doRefresh()
}

func (m Model) updateWorktreeFlow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.worktreeFlow.Step != worktreeStepConfirmBranch && printableKey(msg.String()) {
		m.appendWorktreeFlowInput(msg.String())
		m.renderContent()
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.worktreeFlow = worktreeFlow{}
		m.status = "Enter switches or creates sessions"
		m.renderContent()
		return m, nil
	case "up", "k":
		m.moveWorktreeFlowSelection(-1)
		return m, nil
	case "down", "j":
		m.moveWorktreeFlowSelection(1)
		return m, nil
	case "backspace", "ctrl+h":
		if m.worktreeFlow.Pristine {
			m.worktreeFlow.Query = ""
			m.worktreeFlow.Pristine = false
		} else {
			m.worktreeFlow.Query = dropLastRune(m.worktreeFlow.Query)
		}
		m.worktreeFlow.Selected = 0
		m.renderContent()
		return m, nil
	case "ctrl+u":
		m.worktreeFlow.Query = ""
		m.worktreeFlow.Pristine = false
		m.worktreeFlow.Selected = 0
		m.renderContent()
		return m, nil
	case " ", "space":
		m.appendWorktreeFlowInput(" ")
		m.renderContent()
		return m, nil
	case "y":
		if m.worktreeFlow.Step == worktreeStepConfirmBranch {
			m.worktreeFlow.NewBranch = true
			m.worktreeFlow.Step = worktreeStepNewBranch
			m.worktreeFlow.Query = m.worktreeFlow.WorkspaceName
			m.worktreeFlow.Pristine = true
			m.status = "New branch name"
			m.renderContent()
			return m, nil
		}
		m.appendWorktreeFlowInput("y")
		m.renderContent()
		return m, nil
	case "n":
		if m.worktreeFlow.Step == worktreeStepConfirmBranch {
			m.worktreeFlow.NewBranch = false
			return m.createWorktreeFromFlow()
		}
		m.appendWorktreeFlowInput("n")
		m.renderContent()
		return m, nil
	case "enter":
		switch m.worktreeFlow.Step {
		case worktreeStepBranch:
			branches := m.filteredWorktreeBranches()
			if len(branches) == 0 || m.worktreeFlow.Selected >= len(branches) {
				m.status = "Pick a branch"
				return m, nil
			}
			branch := branches[m.worktreeFlow.Selected]
			m.worktreeFlow.Source = branch
			m.worktreeFlow.Step = worktreeStepName
			m.worktreeFlow.Query = vcs.SuggestedWorktreeName(branch.Name)
			m.worktreeFlow.Pristine = true
			m.status = titleCase(vcsUnit(m.worktreeFlow.Backend)) + " name"
			m.renderContent()
			return m, nil
		case worktreeStepName:
			name := strings.TrimSpace(m.worktreeFlow.Query)
			if name == "" {
				m.status = "Enter a worktree name"
				return m, nil
			}
			m.worktreeFlow.WorkspaceName = name
			m.worktreeFlow.Step = worktreeStepPath
			m.worktreeFlow.Query = name
			m.worktreeFlow.Pristine = true
			m.status = fmt.Sprintf("%s path relative to %s", titleCase(vcsUnit(m.worktreeFlow.Backend)), m.worktreeFlow.Backend.SuggestedWorkspaceParent(m.worktreeFlow.Workspace.Path))
			m.renderContent()
			return m, nil
		case worktreeStepPath:
			relPath := strings.TrimSpace(m.worktreeFlow.Query)
			if relPath == "" {
				m.status = "Enter a worktree path"
				return m, nil
			}
			m.worktreeFlow.WorkspacePath = m.worktreeFlow.Backend.SuggestedWorkspacePath(m.worktreeFlow.Workspace.Path, relPath)
			m.worktreeFlow.Query = ""
			m.worktreeFlow.Pristine = false
			if m.worktreeFlow.Backend.Kind() == vcs.Jujutsu {
				return m.createWorktreeFromFlow()
			}
			if m.branchAlreadyCheckedOut(m.worktreeFlow.Source) {
				m.worktreeFlow.NewBranch = true
				m.worktreeFlow.Step = worktreeStepNewBranch
				m.worktreeFlow.Query = m.worktreeFlow.WorkspaceName
				m.worktreeFlow.Pristine = true
				m.status = "Branch is already checked out; enter new branch name"
				m.renderContent()
				return m, nil
			}
			m.worktreeFlow.Step = worktreeStepConfirmBranch
			m.status = "Create a new branch for this project? y/n"
			m.renderContent()
			return m, nil
		case worktreeStepConfirmBranch:
			m.worktreeFlow.NewBranch = false
			return m.createWorktreeFromFlow()
		case worktreeStepNewBranch:
			if strings.TrimSpace(m.worktreeFlow.Query) == "" {
				m.status = "Enter a new branch name"
				return m, nil
			}
			return m.createWorktreeFromFlow()
		}
	}
	return m, nil
}

func (m *Model) appendWorktreeFlowInput(value string) {
	if m.worktreeFlow.Pristine {
		m.worktreeFlow.Query = ""
		m.worktreeFlow.Pristine = false
	}
	m.worktreeFlow.Query += value
	if m.worktreeFlow.Step == worktreeStepBranch {
		m.worktreeFlow.Selected = 0
	}
}

func (m Model) branchAlreadyCheckedOut(branch vcs.Source) bool {
	return m.worktreeFlow.CheckedOut[normalizeBranchRef(branch.Ref)] || m.worktreeFlow.CheckedOut[normalizeBranchRef(branch.Name)]
}

func normalizeBranchRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/remotes/")
	return strings.TrimPrefix(ref, "origin/")
}

func (m *Model) moveWorktreeFlowSelection(delta int) {
	if m.worktreeFlow.Step != worktreeStepBranch {
		return
	}
	branches := m.filteredWorktreeBranches()
	if len(branches) == 0 {
		return
	}
	m.worktreeFlow.Selected = min(max(0, m.worktreeFlow.Selected+delta), len(branches)-1)
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m Model) filteredWorktreeBranches() []vcs.Source {
	query := strings.TrimSpace(m.worktreeFlow.Query)
	branches := []vcs.Source{}
	for _, branch := range m.worktreeFlow.Sources {
		text := strings.Join([]string{branch.Name, branch.Ref}, " ")
		if query == "" || fuzzyMatch(query, text) {
			branches = append(branches, branch)
		}
	}
	if query != "" {
		sort.SliceStable(branches, func(i, j int) bool {
			return weightedMatchScore(query, branches[i].Name, 100) > weightedMatchScore(query, branches[j].Name, 100)
		})
	}
	return branches
}

func (m Model) createWorktreeFromFlow() (tea.Model, tea.Cmd) {
	flow := m.worktreeFlow
	newBranch := ""
	if flow.NewBranch {
		newBranch = strings.TrimSpace(flow.Query)
	}
	if err := flow.Backend.CreateWorkspace(vcs.CreateWorkspaceRequest{RepoPath: flow.Workspace.Path, WorkspacePath: flow.WorkspacePath, BaseRef: flow.Source.Name, NewSourceName: newBranch}); err != nil {
		m.status = fmt.Sprintf("Create %s failed: %v", vcsUnit(flow.Backend), err)
		return m, nil
	}
	m.worktreeFlow = worktreeFlow{}
	return m.openCreatedWorktree(flow)
}

func (m Model) openCreatedWorktree(flow worktreeFlow) (tea.Model, tea.Cmd) {
	sessionName := m.sessionNameForNewPath(flow.WorkspacePath, flow.WorkspaceName)
	if m.state.Multiplexer.Kind == multiplexer.None {
		m.status = fmt.Sprintf("Created %s %s", vcsUnit(flow.Backend), flow.WorkspacePath)
		return m, m.doRefresh()
	}
	if !m.state.Multiplexer.SessionNames()[sessionName] {
		if err := multiplexer.NewSession(m.state.Multiplexer.Kind, sessionName, flow.WorkspacePath); err != nil {
			m.status = fmt.Sprintf("Created %s, session failed: %v", vcsUnit(flow.Backend), err)
			return m, m.doRefresh()
		}
	}
	switchSession := multiplexer.SwitchSession
	if m.state.SidebarMode {
		switchSession = multiplexer.SwitchSessionWithSidebar
	}
	if err := switchSession(m.state.Multiplexer.Kind, sessionName); err != nil {
		m.status = fmt.Sprintf("Created %s, switch failed: %v", vcsUnit(flow.Backend), err)
		return m, m.doRefresh()
	}
	m.status = fmt.Sprintf("Created %s and switched to %s", vcsUnit(flow.Backend), sessionName)
	return m, m.doRefresh()
}

func (m Model) sessionNameForNewPath(path, fallback string) string {
	base := filepath.Base(path)
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		base = fallback
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = "worktree"
	}
	sessions := m.state.Multiplexer.SessionByName()
	if session, ok := sessions[base]; !ok || filepath.Clean(session.Path) == filepath.Clean(path) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if session, ok := sessions[candidate]; !ok || filepath.Clean(session.Path) == filepath.Clean(path) {
			return candidate
		}
	}
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
	if m.selected >= m.currentItemCount() {
		m.selected = max(0, m.currentItemCount()-1)
	}
	m.renderContent()
}

func (m Model) currentItemCount() int {
	if m.paletteActive {
		return len(m.paletteTargets())
	}
	return len(m.visibleItems())
}

func (m Model) getItems() []workspace.Workspace {
	return util.Filter(m.state.Workspaces, func(w workspace.Workspace) bool {
		return w.GitType != 1 && m.workspaceMatchesSearch(w)
	})
}

func (m Model) visibleItems() []visibleItem {
	workspaces := m.getItems()
	items := make([]visibleItem, 0, len(workspaces))
	for _, ws := range workspaces {
		items = append(items, visibleItem{Kind: kindWorkspace, Workspace: ws, AgentIndex: -1})
		if len(ws.Agents) == 0 || (m.renderer.IsCollapsed(ws.Path) && (!m.searchActive || strings.TrimSpace(m.searchQuery) == "")) {
			continue
		}
		start, end := m.agentWindow(ws)
		for idx := start; idx < end; idx++ {
			if m.searchActive && strings.TrimSpace(m.searchQuery) != "" && !m.agentMatchesSearch(ws, idx) && !fuzzyMatch(m.searchQuery, workspaceSearchText(ws)) {
				continue
			}
			items = append(items, visibleItem{Kind: kindAgent, Workspace: ws, AgentIndex: idx, AgentStart: start, AgentEnd: end})

		}
	}
	return items
}

func (m Model) paletteTargets() []paletteTarget {
	query := strings.TrimSpace(m.paletteQuery)
	options := m.state.Palette
	if options == (PaletteOptions{}) {
		options = DefaultPaletteOptions()
	}
	targets := []paletteTarget{}
	if options.LocalFirst {
		return m.localPaletteTargets(query, options)
	}
	seenRepoActions := map[string]bool{}
	if m.state.SidebarMode && options.IncludeShelveMain {
		target := paletteTarget{
			Action:   paletteAction{Kind: paletteActionShelveMain},
			Label:    "action",
			Title:    "Shelve main pane",
			Subtitle: "Move current main pane to the Glint shelf",
			Search:   "action shelve shelf main pane",
		}
		if score, ok := paletteTargetScore(query, target); ok {
			target.Score = score + 10
			targets = append(targets, target)
		}
	}
	for _, ws := range m.state.Workspaces {
		if ws.GitType == 1 {
			continue
		}
		subtitle := paletteWorkspaceSubtitle(ws)
		label := paletteWorkspaceLabel(ws)
		if options.IncludeWorkspaces {
			target := paletteTarget{
				Item:     visibleItem{Kind: kindWorkspace, Workspace: ws, AgentIndex: -1},
				Label:    label,
				Title:    ws.Name,
				Subtitle: subtitle,
				Search:   strings.Join([]string{label, "workspace", "repo", "worktree", ws.Name, subtitle, workspaceSearchText(ws)}, " "),
			}
			if score, ok := paletteTargetScore(query, target); ok {
				target.Score = score + 25
				targets = append(targets, target)
			}
		}
		if ws.VCS != workspace.VCSNone {
			repoKey := paletteRepoActionKey(ws)
			if !seenRepoActions[repoKey] {
				seenRepoActions[repoKey] = true
				unit := vcsUnitForWorkspace(ws)
				repoName := paletteRepoActionName(ws)
				if options.IncludeCreateWorktree {
					createWorktreeTarget := paletteTarget{
						Action:   paletteAction{Kind: paletteActionCreateWorktree, Workspace: ws},
						Label:    "action",
						Title:    "Create " + unit + " in " + repoName,
						Subtitle: ws.Root,
						Search:   strings.Join([]string{"action create new project worktree workspace", repoName, ws.Name, ws.Path, ws.Root, ws.ParentName, ws.Branch}, " "),
					}
					if score, ok := paletteTargetScore(query, createWorktreeTarget); ok {
						createWorktreeTarget.Score = score + 15
						targets = append(targets, createWorktreeTarget)
					}
				}
				if options.IncludeCleanupWorktrees {
					cleanupTarget := paletteTarget{
						Action:   paletteAction{Kind: paletteActionCleanupWorktrees, Workspace: ws},
						Label:    "action",
						Title:    "Clean up " + unit + "s in " + repoName,
						Subtitle: vcsCleanupVerbForWorkspace(ws) + " a selected " + unit,
						Search:   strings.Join([]string{"action cleanup clean remove delete prune project worktree workspace", repoName, ws.Name, ws.Path, ws.Root, ws.ParentName, ws.Branch}, " "),
					}
					if score, ok := paletteTargetScore(query, cleanupTarget); ok {
						cleanupTarget.Score = score + 15
						targets = append(targets, cleanupTarget)
					}
				}
			}
		}
		if m.state.SidebarMode && options.IncludeNewAgent {
			actionTarget := paletteTarget{
				Action:   paletteAction{Kind: paletteActionNewAgent, Workspace: ws},
				Label:    "action",
				Title:    "New agent in " + ws.Name,
				Subtitle: ws.Path,
				Search:   strings.Join([]string{"action new agent chat pi", ws.Name, ws.Path, ws.ParentName, ws.Branch}, " "),
			}
			if score, ok := paletteTargetScore(query, actionTarget); ok {
				actionTarget.Score = score + 15
				targets = append(targets, actionTarget)
			}
		}
		if !options.IncludeAgents {
			continue
		}
		for idx, ag := range ws.Agents {
			title := quoteTask(ag.Task)
			subtitle := strings.Join(nonEmpty([]string{ag.Name, ws.Name, string(ag.Status), relativeTime(ag.Activity)}), " · ")
			target := paletteTarget{
				Item:     visibleItem{Kind: kindAgent, Workspace: ws, AgentIndex: idx},
				Label:    "agent",
				Title:    title,
				Subtitle: subtitle,
				Search:   strings.Join([]string{"agent", ag.Name, ag.Task, string(ag.Status), ag.Source, ag.Session, ag.Pane, workspaceSearchText(ws)}, " "),
			}
			if score, ok := paletteTargetScore(query, target); ok {
				target.Score = score
				targets = append(targets, target)
			}
		}
	}
	if query != "" {
		sort.SliceStable(targets, func(i, j int) bool {
			if targets[i].Score != targets[j].Score {
				return targets[i].Score > targets[j].Score
			}
			if targetKindRank(targets[i]) != targetKindRank(targets[j]) {
				return targetKindRank(targets[i]) < targetKindRank(targets[j])
			}
			return targets[i].Title < targets[j].Title
		})
	}
	return targets
}

var localPaletteBaseCache = map[string][]paletteTarget{}

func (m Model) localPaletteTargets(query string, options PaletteOptions) []paletteTarget {
	base := m.localPaletteBaseTargets(options)
	targets := []paletteTarget{}
	for _, target := range base {
		if score, ok := paletteTargetScore(query, target); ok {
			target.Score += score
			targets = append(targets, target)
		}
	}
	if query != "" {
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].Score > targets[j].Score })
	}
	return targets
}

func (m Model) localPaletteBaseTargets(options PaletteOptions) []paletteTarget {
	ws, ok := m.currentWorkspace()
	if !ok || ws.VCS == workspace.VCSNone {
		return nil
	}
	cacheKey := strings.Join([]string{filepath.Clean(ws.Path), fmt.Sprintf("%t", options.IncludeWorkspaces), fmt.Sprintf("%t", options.IncludeCreateWorktree), fmt.Sprintf("%t", options.IncludeCleanupWorktrees)}, "\x00")
	if targets, ok := localPaletteBaseCache[cacheKey]; ok {
		return targets
	}
	backend := vcs.ForPath(ws.Path)
	sources, err := backend.Sources(ws.Path)
	if err != nil {
		return nil
	}
	refs, _ := backend.WorkspaceRefs(ws.Path)
	targets := []paletteTarget{}
	if options.IncludeWorkspaces {
		for _, ref := range refs {
			if ref.Path == "" || ref.Bare {
				continue
			}
			existing, ok := m.workspaceByPath(ref.Path)
			if !ok {
				existing = workspace.Workspace{Name: filepath.Base(ref.Path), Path: ref.Path, VCS: ws.VCS}
			}
			title := existing.Name
			if ref.Source != "" {
				title = ref.Source
			}
			targets = append(targets, paletteTarget{Item: visibleItem{Kind: kindWorkspace, Workspace: existing, AgentIndex: -1}, Label: "project", Title: title, Subtitle: ref.Path, Search: strings.Join([]string{"project switch checkout workspace worktree", title, ref.Source, existing.Name, ref.Path}, " "), Score: 30})
		}
	}
	for _, source := range sources {
		if options.IncludeCreateWorktree {
			targets = append(targets, paletteTarget{Action: paletteAction{Kind: paletteActionCreateWorktree, Workspace: ws, Source: source}, Label: "create", Title: source.Name, Subtitle: source.Ref + remoteSuffix(source), Search: strings.Join([]string{"create project checkout workspace worktree branch bookmark", source.Name, source.Ref}, " "), Score: 20})
		}
	}
	localPaletteBaseCache[cacheKey] = targets
	return targets
}

func remoteSuffix(source vcs.Source) string {
	if source.Remote {
		return " [remote]"
	}
	return ""
}

func (m Model) currentWorkspace() (workspace.Workspace, bool) {
	current := filepath.Clean(m.state.CurrentPath)
	var best workspace.Workspace
	bestLen := -1
	for _, ws := range m.state.Workspaces {
		path := filepath.Clean(ws.Path)
		if (current == path || strings.HasPrefix(current, path+string(filepath.Separator))) && len(path) > bestLen {
			best = ws
			bestLen = len(path)
		}
	}
	return best, bestLen >= 0
}

func (m Model) workspaceByPath(path string) (workspace.Workspace, bool) {
	clean := filepath.Clean(path)
	for _, ws := range m.state.Workspaces {
		if filepath.Clean(ws.Path) == clean {
			return ws, true
		}
	}
	return workspace.Workspace{}, false
}

func targetKindRank(target paletteTarget) int {
	if target.Action.Kind != paletteActionNone {
		return 1
	}
	if target.Item.Kind == kindWorkspace {
		return 0
	}
	return 2
}

func paletteTargetScore(query string, target paletteTarget) (int, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, true
	}
	score := 0
	score = max(score, weightedMatchScore(query, target.Title, 100))
	score = max(score, weightedMatchScore(query, target.Subtitle, 35))
	score = max(score, weightedMatchScore(query, target.Label, 25))
	score = max(score, weightedMatchScore(query, target.Search, 10))
	return score, score > 0
}

func weightedMatchScore(query, text string, weight int) int {
	query = strings.ToLower(strings.TrimSpace(query))
	text = strings.ToLower(strings.TrimSpace(text))
	if query == "" || text == "" {
		return 0
	}

	best := singleTermMatchScore(query, text, weight)
	queryTerms := searchTerms(query)
	if len(queryTerms) <= 1 {
		return max(best, 0)
	}

	// Multi-word palette queries should behave like work matching, not just
	// phrase matching. Require every typed word to match somewhere in the
	// candidate, then average the per-word score. This lets queries like
	// "adding dotfiles" find tasks such as "Add dotfiles support".
	total := 0
	for _, term := range queryTerms {
		termBest := singleTermMatchScore(term, text, weight)
		if termBest == 0 {
			return max(best, 0)
		}
		total += termBest
	}
	best = max(best, total/len(queryTerms)+10*weight)
	return max(best, 0)
}

func singleTermMatchScore(query, text string, weight int) int {
	best := 0
	if text == query {
		best = max(best, 100*weight)
	}
	if idx := strings.Index(text, query); idx >= 0 {
		best = max(best, 80*weight-indexPenalty(idx))
	}
	for _, token := range strings.FieldsFunc(text, searchTokenSeparator) {
		if token == "" {
			continue
		}
		best = max(best, tokenMatchScore(query, token, weight))
	}
	return best
}

func tokenMatchScore(query, token string, weight int) int {
	switch {
	case token == query:
		return 95 * weight
	case strings.HasPrefix(token, query):
		return 75*weight - len(token)
	case strings.HasPrefix(query, token) && len(token) >= 3:
		return 55*weight - len(query)
	case sameLooseStem(query, token):
		return 70 * weight
	case strings.Contains(token, query):
		return 60*weight - len(token)
	case len(token) >= len(query) && len(fuzzy.Find(query, []string{token})) > 0:
		return 25*weight - len(token)
	}
	return 0
}

func searchTerms(text string) []string {
	terms := []string{}
	for _, term := range strings.FieldsFunc(text, searchTokenSeparator) {
		if term != "" {
			terms = append(terms, term)
		}
	}
	return terms
}

func sameLooseStem(a, b string) bool {
	return looseStem(a) == looseStem(b)
}

func looseStem(value string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(value) > len(suffix)+2 && strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

func indexPenalty(idx int) int {
	if idx > 80 {
		return 80
	}
	return idx
}

func paletteRepoActionKey(ws workspace.Workspace) string {
	root := ws.Root
	if strings.TrimSpace(root) == "" {
		root = ws.Path
	}
	return fmt.Sprintf("%d:%s", ws.VCS, filepath.Clean(root))
}

func paletteRepoActionName(ws workspace.Workspace) string {
	if strings.TrimSpace(ws.ParentName) != "" {
		return ws.ParentName
	}
	if strings.TrimSpace(ws.Root) != "" {
		return filepath.Base(ws.Root)
	}
	return ws.Name
}

func paletteWorkspaceLabel(ws workspace.Workspace) string {
	return "project"
}

func paletteWorkspaceSubtitle(ws workspace.Workspace) string {
	parts := []string{ws.Path}
	if ws.ParentName != "" {
		parts = append(parts, ws.ParentName)
	}
	if ws.Branch != "" {
		parts = append(parts, ws.Branch)
	}
	if len(ws.Agents) > 0 {
		parts = append(parts, fmt.Sprintf("%d agent%s", len(ws.Agents), plural(len(ws.Agents))))
	}
	return strings.Join(parts, " · ")
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func (m Model) workspaceMatchesSearch(ws workspace.Workspace) bool {
	query := strings.TrimSpace(m.searchQuery)
	if !m.searchActive || query == "" {
		return true
	}
	if fuzzyMatch(query, workspaceSearchText(ws)) {
		return true
	}
	for idx := range ws.Agents {
		if m.agentMatchesSearch(ws, idx) {
			return true
		}
	}
	return false
}

func (m Model) agentMatchesSearch(ws workspace.Workspace, idx int) bool {
	if idx < 0 || idx >= len(ws.Agents) {
		return false
	}
	ag := ws.Agents[idx]
	return fuzzyMatch(m.searchQuery, strings.Join([]string{ag.Name, ag.Task, string(ag.Status), ag.Source, ag.Session, ag.Pane}, " "))
}

func workspaceSearchText(ws workspace.Workspace) string {
	return strings.Join([]string{ws.Name, ws.Path, ws.Root, ws.ParentName, ws.Branch, ws.Head}, " ")
}

func fuzzyMatch(query, text string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	text = strings.ToLower(strings.TrimSpace(text))
	if query == "" {
		return true
	}
	if strings.Contains(text, query) {
		return true
	}
	for _, token := range strings.FieldsFunc(text, searchTokenSeparator) {
		if len(token) >= len(query) && len(fuzzy.Find(query, []string{token})) > 0 {
			return true
		}
	}
	return false
}

func searchTokenSeparator(r rune) bool {
	return !(unicode.IsLetter(r) || unicode.IsDigit(r))
}

func (m Model) agentWindow(ws workspace.Workspace) (int, int) {
	count := len(ws.Agents)
	if count <= maxVisibleAgents {
		return 0, count
	}
	maxStart := count - maxVisibleAgents
	start := min(max(0, m.agentOffsets[ws.Path]), maxStart)
	return start, start + maxVisibleAgents
}

func (m Model) viewportInnerWidth() int {
	return max(0, m.viewport.Width()-m.viewport.Style.GetHorizontalFrameSize())
}

func (m Model) viewportInnerHeight() int {
	return max(0, m.viewport.Height()-m.viewport.Style.GetVerticalFrameSize())
}

func (m *Model) renderContent() {
	if m.cleanupFlow.Active {
		m.renderCleanupFlowContent()
		return
	}
	if m.worktreeFlow.Active {
		m.renderWorktreeFlowContent()
		return
	}
	if m.paletteActive {
		m.renderPaletteContent()
		return
	}
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

func (m *Model) renderCleanupFlowContent() {
	lines := []string{}
	m.spans = nil
	flow := m.cleanupFlow
	lines = append(lines, m.renderer.styles.Title.Render("Clean up "+vcsUnit(flow.Backend)+"s in "+flow.Workspace.Name))
	lines = append(lines, m.renderer.styles.Description.Render(flow.Workspace.Path), "")
	if flow.Confirm && len(flow.WorkspaceRefs) > 0 {
		wt := flow.WorkspaceRefs[0]
		lines = append(lines, m.renderer.styles.SelectedTitle.Render(vcsCleanupVerb(flow.Backend)+" "+filepath.Base(wt.Path)+"?"))
		lines = append(lines, m.renderer.styles.Description.Render(wt.Path))
		lines = append(lines, m.renderer.styles.Description.Render(titleCase(vcsSourceName(flow.Backend))+": "+wt.Source))
		lines = append(lines, "", m.renderer.styles.Description.Render("This will also kill a matching tmux session if one exists."))
		if flow.Backend.Kind() == vcs.Jujutsu {
			lines = append(lines, m.renderer.styles.Description.Render("Jujutsu forgets the workspace and removes its directory."))
		}
		lines = append(lines, m.renderer.styles.Description.Render("Press y to confirm · n/Esc to cancel"))
		m.viewport.SetContent(strings.Join(lines, "\n"))
		return
	}
	lines = append(lines, m.renderer.styles.Description.Render("Filter: "+flow.Query), "")
	worktrees := m.filteredCleanupWorktrees()
	for idx, wt := range worktrees {
		start := len(lines)
		title := m.styles.Badge.Render("project") + " " + filepath.Base(wt.Path)
		subtitle := wt.Path
		if wt.Source != "" {
			subtitle += " · " + wt.Source
		}
		if session := m.sessionForPathOrName(wt.Path, filepath.Base(wt.Path)); session != nil {
			subtitle += " · tmux " + session.Name
		}
		if idx == flow.Selected {
			lines = append(lines, m.renderer.styles.SelectedTitle.Render(title), m.renderer.styles.SelectedDesc.Render(subtitle))
		} else {
			lines = append(lines, m.renderer.styles.Title.Render(title), m.renderer.styles.Description.Render(subtitle))
		}
		lines = append(lines, "")
		m.spans = append(m.spans, itemSpan{start: start, end: len(lines)})
	}
	lines = append(lines, "", "", "")
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m *Model) renderWorktreeFlowContent() {
	lines := []string{}
	m.spans = nil
	flow := m.worktreeFlow
	lines = append(lines, m.renderer.styles.Title.Render("Create "+vcsUnit(flow.Backend)+" in "+flow.Workspace.Name))
	lines = append(lines, m.renderer.styles.Description.Render(flow.Workspace.Path), "")
	switch flow.Step {
	case worktreeStepBranch:
		lines = append(lines, m.renderer.styles.Description.Render(titleCase(vcsSourceName(flow.Backend))+": "+flow.Query), "")
		branches := m.filteredWorktreeBranches()
		for idx, branch := range branches {
			start := len(lines)
			label := "branch"
			if branch.Remote {
				label = "remote"
			}
			title := m.styles.Badge.Render(label) + " " + branch.Name
			subtitle := branch.Ref
			if m.branchAlreadyCheckedOut(branch) {
				subtitle += " · checked out; new branch required"
			}
			if idx == flow.Selected {
				lines = append(lines, m.renderer.styles.SelectedTitle.Render(title), m.renderer.styles.SelectedDesc.Render(subtitle))
			} else {
				lines = append(lines, m.renderer.styles.Title.Render(title), m.renderer.styles.Description.Render(subtitle))
			}
			lines = append(lines, "")
			m.spans = append(m.spans, itemSpan{start: start, end: len(lines)})
		}
	case worktreeStepName:
		lines = append(lines, m.renderer.styles.Title.Render(titleCase(vcsUnit(flow.Backend))+" name"))
		lines = append(lines, m.renderer.styles.SelectedDesc.Render(flow.Query))
		lines = append(lines, m.renderer.styles.Description.Render("Enter accepts default · typing replaces it"))
	case worktreeStepPath:
		lines = append(lines, m.renderer.styles.Title.Render(titleCase(vcsUnit(flow.Backend))+" path"))
		lines = append(lines, m.renderer.styles.Description.Render("Relative to: "+flow.Backend.SuggestedWorkspaceParent(flow.Workspace.Path)))
		lines = append(lines, m.renderer.styles.SelectedDesc.Render(flow.Query))
		lines = append(lines, m.renderer.styles.Description.Render("Enter accepts default · typing replaces it"))
	case worktreeStepConfirmBranch:
		lines = append(lines, m.renderer.styles.Title.Render("Base branch: "+flow.Source.Name))
		lines = append(lines, m.renderer.styles.Description.Render("Path: "+flow.WorkspacePath), "")
		lines = append(lines, m.renderer.styles.SelectedTitle.Render("Create a new branch for this project?"))
		lines = append(lines, m.renderer.styles.Description.Render("y yes · n no · Enter no"))
	case worktreeStepNewBranch:
		lines = append(lines, m.renderer.styles.Title.Render("New branch name"))
		lines = append(lines, m.renderer.styles.SelectedDesc.Render(flow.Query))
	}
	lines = append(lines, "", "", "")
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m *Model) renderPaletteContent() {
	targets := m.paletteTargets()
	lines := []string{}
	m.spans = make([]itemSpan, 0, len(targets))

	filterPrompt := "  › "
	if m.paletteFiltering {
		filterPrompt = m.styles.Accent.Render("  / ")
	}

	prompt := filterPrompt + m.paletteQuery
	if strings.TrimSpace(m.paletteQuery) == "" {
		if m.paletteFiltering {
			prompt = filterPrompt + m.styles.Muted.Render("type to filter")
		} else {
			prompt = filterPrompt + m.styles.Muted.Render("press / to filter")
		}
	}
	lines = append(lines, prompt)

	if len(targets) == 0 {
		lines = append(lines, m.styles.Muted.Render("No palette matches"))
	}
	for idx, target := range targets {
		start := len(lines)
		lines = append(lines, m.renderPaletteTarget(target, idx == m.selected))
		m.spans = append(m.spans, itemSpan{start: start, end: len(lines)})
	}
	lines = append(lines, "", "", "")
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m Model) renderPaletteTarget(target paletteTarget, selected bool) string {
	prefix := "  "
	label := m.styles.Muted.Render(target.Label)
	if selected {
		prefix = "› "
		label = target.Label
	}
	left := prefix + label + " " + target.Title
	right := target.Subtitle
	style := m.renderer.styles.Title
	if selected {
		style = m.renderer.styles.SelectedTitle
	}
	width := max(0, m.viewportInnerWidth()-style.GetHorizontalFrameSize()-1)
	if selected {
		line := compactPaletteLine(left, right, width)
		return style.Render(line)
	}
	line := compactPaletteLine(left, m.styles.Muted.Render(right), width)
	return style.Render(line)
}

func compactPaletteLine(left, right string, width int) string {
	if width <= 0 {
		return left
	}
	if strings.TrimSpace(right) == "" {
		return truncateDisplay(left, width)
	}
	available := width - lipgloss.Width(left) - 3
	if available < 12 {
		return truncateDisplay(left, width)
	}
	right = truncateDisplay(right, available)
	line := util.RightAlignLine(left, right, width)
	return truncateDisplay(line, width)
}

func truncateDisplay(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	out := ""
	for _, r := range value {
		if lipgloss.Width(out+string(r))+1 > width {
			break
		}
		out += string(r)
	}
	return out + "…"
}

func (m *Model) moveSelection(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}
	if m.scrollAgentWindow(delta, items) {
		m.renderContent()
		m.ensureSelectedVisible()
		return
	}
	m.selected = min(max(0, m.selected+delta), len(items)-1)
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m *Model) movePaletteSelection(delta int) {
	targets := m.paletteTargets()
	if len(targets) == 0 {
		return
	}
	m.selected = min(max(0, m.selected+delta), len(targets)-1)
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m *Model) scrollAgentWindow(delta int, items []visibleItem) bool {
	if delta == 0 || m.selected < 0 || m.selected >= len(items) {
		return false
	}
	item := items[m.selected]
	if item.Kind != kindAgent || len(item.Workspace.Agents) <= maxVisibleAgents {
		return false
	}

	path := item.Workspace.Path
	start, end := m.agentWindow(item.Workspace)
	if delta > 0 && item.AgentIndex == end-1 && end < len(item.Workspace.Agents) {
		m.agentOffsets[path] = start + 1
		return true
	}
	if delta < 0 && item.AgentIndex == start && start > 0 {
		m.agentOffsets[path] = start - 1
		return true
	}
	return false
}

func (m *Model) moveWorkspaceSelection(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}

	if delta > 0 {
		for idx := min(m.selected+1, len(items)); idx < len(items); idx++ {
			if items[idx].Kind == kindWorkspace {
				m.selected = idx
				m.renderContent()
				m.ensureSelectedVisible()
				return
			}
		}
		return
	}

	if delta < 0 {
		for idx := min(m.selected-1, len(items)-1); idx >= 0; idx-- {
			if items[idx].Kind == kindWorkspace {
				m.selected = idx
				m.renderContent()
				m.ensureSelectedVisible()
				return
			}
		}
	}
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
	workspacePath := item.Workspace.Path
	wasCollapsed := m.renderer.collapsed[workspacePath]
	if wasCollapsed {
		delete(m.renderer.collapsed, workspacePath)
	} else {
		m.renderer.collapsed[workspacePath] = true
	}
	m.selected = m.visibleWorkspaceIndex(workspacePath)
	if m.selected < 0 {
		m.selected = max(0, min(m.selected, len(m.visibleItems())-1))
	}
	if err := saveCollapsedProjects(m.renderer.collapsed); err != nil {
		m.status = fmt.Sprintf("Could not save sidebar state: %v", err)
	}
	m.renderContent()
	m.ensureSelectedVisible()
}

func (m Model) visibleWorkspaceIndex(path string) int {
	for idx, item := range m.visibleItems() {
		if item.Kind == kindWorkspace && item.Workspace.Path == path {
			return idx
		}
	}
	return -1
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
	badge := m.styles.Badge.Render("GLINT")
	parts := []string{
		fmt.Sprintf("%d projects", len(m.state.Workspaces)),
		fmt.Sprintf("%d agents", m.agentCount()),
	}
	if m.state.Multiplexer.Kind != multiplexer.None {
		parts = append(parts, string(m.state.Multiplexer.Kind))
	}
	header := fmt.Sprintf("%s  %s", badge, strings.Join(parts, " · "))
	if m.searchActive {
		header = fmt.Sprintf("%s  %s", header, m.styles.Muted.Render("/ "+m.searchQuery))
	}
	if m.paletteActive {
		header = fmt.Sprintf("%s  %s", header, m.styles.Muted.Render("> "+m.paletteQuery))
	}
	if m.worktreeFlow.Active {
		header = fmt.Sprintf("%s  %s", header, m.styles.Muted.Render(vcsUnit(m.worktreeFlow.Backend)))
	}
	if m.cleanupFlow.Active {
		header = fmt.Sprintf("%s  %s", header, m.styles.Muted.Render("cleanup"))
	}
	style := m.styles.Header
	if m.width > 0 {
		style = style.Width(max(0, m.width-style.GetHorizontalFrameSize()))
	}
	return style.Render(header)
}

func (m Model) agentCount() int {
	count := 0
	for _, ws := range m.state.Workspaces {
		count += len(ws.Agents)
	}
	return count
}

func (m Model) viewFooter() string {
	help := "↑/↓ move · / search · ctrl+p palette · ctrl+w worktree · ctrl+r cleanup · [/] projects · c/space collapse · Enter switch/create · n new chat · b shelve · s spinner · ctrl+x delete · q quit"

	if m.state.SidebarMode {
		help = "↑/↓ move · / search · ctrl+p palette · ctrl+w worktree · ctrl+r cleanup · [/] projects · Enter bring/switch · b shelve · ctrl+x delete · c collapse · s spinner · q quit"
	}
	if m.searchActive {
		help = "type to filter · ↑/↓ move · Enter select · ctrl+u clear · Esc close search"
	}
	if m.paletteActive {
		help = "↑/↓/j/k move · Enter run/open · ctrl+d cleanup · / filter · Tab/h/l local/global · Esc close palette"
	}
	if m.worktreeFlow.Active {
		help = "type to filter/edit · ↑/↓ move branches · Enter next/create · y/n choose · Esc cancel"
	}
	if m.cleanupFlow.Active {
		help = "type to filter · ↑/↓ move · Enter select · y confirm · n/Esc cancel"

	}
	content := fmt.Sprintf("%s %s\n%s", m.styles.Badge.Render("status"), m.status, help)
	style := m.styles.Help
	if m.width > 0 {
		style = style.Width(max(0, m.width-style.GetHorizontalFrameSize()))
	}
	return style.Render(content)
}

func (m Model) View() tea.View {
	body := m.viewport.View()
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), body, m.viewFooter()))
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
	return m.activateItem(items[m.selected])
}

func (m Model) activatePaletteSelected() (tea.Model, tea.Cmd) {
	targets := m.paletteTargets()
	if len(targets) == 0 || m.selected < 0 || m.selected >= len(targets) {
		m.status = "Pick a command"
		return m, nil
	}
	target := targets[m.selected]
	if target.Action.Kind != paletteActionNone {
		return m.activatePaletteAction(target.Action)
	}
	return m.activateItem(target.Item)
}

func (m Model) cleanupPaletteSelectedWorkspace() (tea.Model, tea.Cmd) {
	targets := m.paletteTargets()
	if len(targets) == 0 || m.selected < 0 || m.selected >= len(targets) || targets[m.selected].Item.Kind != kindWorkspace {
		m.status = "Pick a project to clean up"
		return m, nil
	}
	selected := targets[m.selected].Item.Workspace
	repo, ok := m.currentWorkspace()
	if !ok || !m.state.Palette.LocalFirst {
		repo = selected
	}
	backend := vcs.ForPath(repo.Path)
	m.cleanupFlow = cleanupFlow{Active: true, Backend: backend, Workspace: repo, WorkspaceRefs: []vcs.WorkspaceRef{{Path: selected.Path}}, Confirm: true}
	m.paletteActive = false
	m.status = "Confirm remove " + vcsUnit(m.cleanupFlow.Backend) + "? y/n"
	m.renderContent()
	return m, nil
}

func (m Model) activatePaletteAction(action paletteAction) (tea.Model, tea.Cmd) {
	switch action.Kind {
	case paletteActionNewAgent:
		return m.newAgentForWorkspace(action.Workspace)
	case paletteActionShelveMain:
		return m.shelveMainPane()
	case paletteActionCreateWorktree:
		if action.Source.Name != "" || action.Source.Ref != "" {
			return m.startWorktreeFlowFromSource(action.Workspace, action.Source)
		}
		return m.startWorktreeFlow(action.Workspace)
	case paletteActionCleanupWorktrees:
		return m.startCleanupFlow(action.Workspace)
	default:
		m.status = "Unknown palette action"
		return m, nil
	}
}

func (m Model) activateItem(item visibleItem) (tea.Model, tea.Cmd) {
	if item.Kind == kindAgent {
		ag := item.Workspace.Agents[item.AgentIndex]
		if ag.Pane != "" {
			if m.state.SidebarMode {
				canSwap := ag.Session == "" || ag.Session == m.state.CurrentSession || ag.Session == multiplexer.ShelfSessionName
				if !canSwap {
					if err := multiplexer.SwitchPaneWithSidebar(m.state.Multiplexer.Kind, ag.Session, ag.Window, ag.Pane); err != nil {
						m.status = fmt.Sprintf("Switch failed: %v -- %+v, %+v, %+v", err, ag.Session, ag.Window, ag.Pane)
						return m, nil
					}
					m.status = fmt.Sprintf("Switched to %s in %s:%s", ag.Name, ag.Session, ag.Pane)
					return m, nil
				}
				if err := multiplexer.BringPaneToSidebarMain(ag.Pane); err != nil {
					m.status = fmt.Sprintf("Bring failed: %v -- %+v, %+v, %+v", err, ag.Session, ag.Window, ag.Pane)
					return m, nil
				}
				m.status = fmt.Sprintf("Brought %s to main slot", ag.Name)
				return m, nil
			}
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

func (m Model) removeSession() (tea.Model, tea.Cmd) {
	items := m.visibleItems()
	if len(items) == 0 || m.selected < 0 || m.selected >= len(items) {
		m.status = "Pick a project or agent"
		return m, nil
	}

	item := items[m.selected]
	if item.Kind == kindAgent {
		return m.removeAgentPane(item)
	}
	return m.removeWorkspaceSession(item.Workspace)
}

func (m Model) removeAgentPane(item visibleItem) (tea.Model, tea.Cmd) {
	if item.AgentIndex < 0 || item.AgentIndex >= len(item.Workspace.Agents) {
		m.status = "Pick an agent"
		return m, nil
	}
	ag := item.Workspace.Agents[item.AgentIndex]
	if ag.Pane == "" {
		m.status = fmt.Sprintf("No live pane to delete for %s", ag.Name)
		return m, nil
	}
	if err := multiplexer.KillPane(m.state.Multiplexer.Kind, ag.Pane); err != nil {
		m.status = fmt.Sprintf("Delete failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Deleted pane for %s", ag.Name)
	return m, m.doRefresh()
}

func (m Model) removeWorkspaceSession(ws workspace.Workspace) (tea.Model, tea.Cmd) {
	session := m.sessionFromWorkspace(ws)
	if session == nil {
		m.status = fmt.Sprintf("No session to delete for %s", ws.Name)
		return m, nil
	}
	sessionName := session.Name
	if sessionName == m.state.CurrentSession {
		m.status = fmt.Sprintf("Can't delete current session %s", sessionName)
		return m, nil
	}
	if sessionName == multiplexer.ShelfSessionName {
		m.status = fmt.Sprintf("Can't delete shelf session %s", sessionName)
		return m, nil
	}

	if err := multiplexer.KillSession(m.state.Multiplexer.Kind, sessionName); err != nil {
		m.status = fmt.Sprintf("Delete failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Deleted session %s", sessionName)
	return m, m.doRefresh()
}

func (m Model) shelveMainPane() (tea.Model, tea.Cmd) {
	if !m.state.SidebarMode {
		m.status = "Shelving is only available in sidebar mode"
		return m, nil
	}
	if m.state.Multiplexer.Kind != multiplexer.Tmux {
		m.status = "Shelving requires tmux"
		return m, nil
	}

	items := m.visibleItems()
	if m.selected >= 0 && m.selected < len(items) && items[m.selected].Kind == kindAgent {
		ag := items[m.selected].Workspace.Agents[items[m.selected].AgentIndex]
		switch {
		case ag.Pane == "":
			m.status = fmt.Sprintf("Can't shelve %s; no live pane", ag.Name)
			return m, nil
		case ag.Session == multiplexer.ShelfSessionName:
			if err := multiplexer.ShelveSidebarMain(m.state.CurrentSession); err != nil {
				m.status = fmt.Sprintf("Shelve failed: %v", err)
				return m, nil
			}
			m.status = "Shelved current main pane"
			return m, m.doRefresh()
		case ag.Session == "" || ag.Session == m.state.CurrentSession:
			if err := multiplexer.ShelvePane(m.state.CurrentSession, ag.Pane); err != nil {
				m.status = fmt.Sprintf("Shelve failed: %v", err)
				return m, nil
			}
			m.status = fmt.Sprintf("Shelved %s", ag.Name)
			return m, m.doRefresh()
		default:
			m.status = fmt.Sprintf("Can't shelve pane from session %s", ag.Session)
			return m, nil
		}
	}

	if err := multiplexer.ShelveSidebarMain(m.state.CurrentSession); err != nil {
		m.status = fmt.Sprintf("Shelve failed: %v", err)
		return m, nil
	}
	m.status = "Shelved current main pane"
	return m, m.doRefresh()
}

func (m Model) newAgentForSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.status = "Pick a project"
		return m, nil
	}
	return m.newAgentForWorkspace(selected)
}

func (m Model) newAgentForWorkspace(selected workspace.Workspace) (tea.Model, tea.Cmd) {
	if !m.state.SidebarMode {
		m.status = "New chat is only available in sidebar mode"
		return m, nil
	}
	if m.state.Multiplexer.Kind != multiplexer.Tmux {
		m.status = "New chat requires tmux"
		return m, nil
	}
	command := newAgentCommand()
	if err := multiplexer.LaunchSidebarMainCommand(m.state.CurrentSession, selected.Path, command); err != nil {
		m.status = fmt.Sprintf("New chat failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Started %s in %s", command, selected.Name)
	return m, m.doRefresh()
}

func (m Model) selectedWorkspace() (workspace.Workspace, bool) {
	items := m.visibleItems()
	if len(items) == 0 || m.selected < 0 || m.selected >= len(items) {
		return workspace.Workspace{}, false
	}
	return items[m.selected].Workspace, true
}

func newAgentCommand() string {
	if command := strings.TrimSpace(os.Getenv("GLINT_AGENT_COMMAND")); command != "" {
		return command
	}
	return "pi"
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

	switchSession := multiplexer.SwitchSession
	if m.state.SidebarMode {
		switchSession = multiplexer.SwitchSessionWithSidebar
	}
	if err := switchSession(m.state.Multiplexer.Kind, sessionName); err != nil {
		m.status = fmt.Sprintf("Switch failed: %v", err)
		return m, nil
	}
	m.status = fmt.Sprintf("Switched to %s", sessionName)
	return m, nil
}

func (m Model) sessionFromWorkspace(ws workspace.Workspace) *multiplexer.Session {
	return m.sessionForPathOrName(ws.Path, ws.Name)
}

func (m Model) sessionForPathOrName(path, name string) *multiplexer.Session {
	sessionNames := m.state.Multiplexer.SessionByName()
	sessionPaths := m.state.Multiplexer.SessionByPath()

	session, active := sessionNames[name]
	if !active {
		session, active = sessionPaths[filepath.Clean(path)]
	}
	if !active {
		return nil
	}
	return &session
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
