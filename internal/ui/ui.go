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
	SidebarMode    bool
	Theme          theme.Theme
}

type RefreshFunc func() (State, error)

type Model struct {
	state         State
	viewport      viewport.Model
	selected      int
	status        string
	refresh       RefreshFunc
	styles        theme.Styles
	renderer      itemRenderer
	spans         []itemSpan
	searchActive  bool
	searchQuery   string
	paletteActive bool
	paletteQuery  string
	worktreeFlow  worktreeFlow
	cleanupFlow   cleanupFlow
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
	Active       bool
	Step         worktreeFlowStep
	Backend      vcs.Backend
	Workspace    workspace.Workspace
	Branches     []vcs.Branch
	CheckedOut   map[string]bool
	Query        string
	Pristine     bool
	Selected     int
	Branch       vcs.Branch
	WorktreeName string
	WorktreePath string
	NewBranch    bool
}

type cleanupFlow struct {
	Active    bool
	Backend   vcs.Backend
	Workspace workspace.Workspace
	Worktrees []vcs.Worktree
	Query     string
	Selected  int
	Confirm   bool
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
		renderer: newItemRenderer(state.Theme, loadCollapsedProjects()),
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
		if m.searchActive {
			m.updateSearchStatus()
		}
		if m.paletteActive {
			m.updatePaletteStatus()
		}
		m.rebuildItems()
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
	case "up":
		m.moveSelection(-1)
		return m, nil
	case "down":
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

	if printableKey(msg.String()) {
		m.searchQuery += msg.String()
		m.selected = 0
		m.updateSearchStatus()
		m.renderContent()
		m.ensureSelectedVisible()
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
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.paletteActive = false
		m.paletteQuery = ""
		m.status = "Enter switches or creates sessions"
		m.selected = min(m.selected, max(0, len(m.visibleItems())-1))
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "enter":
		return m.activatePaletteSelected()
	case "up":
		m.movePaletteSelection(-1)
		return m, nil
	case "down":
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
	case "backspace", "ctrl+h":
		m.paletteQuery = dropLastRune(m.paletteQuery)
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "ctrl+u":
		m.paletteQuery = ""
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	case "space":
		m.paletteQuery += " "
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
		return m, nil
	}

	if printableKey(msg.String()) {
		m.paletteQuery += msg.String()
		m.selected = 0
		m.updatePaletteStatus()
		m.renderContent()
		m.ensureSelectedVisible()
	}
	return m, nil
}

func (m *Model) updatePaletteStatus() {
	count := len(m.paletteTargets())
	if strings.TrimSpace(m.paletteQuery) == "" {
		m.status = fmt.Sprintf("Command palette · %d target%s", count, plural(count))
		return
	}
	m.status = fmt.Sprintf("Palette: %s · %d match%s", m.paletteQuery, count, plural(count))
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
		m.status = "Pick a repo/workspace"
		return m, nil
	}
	return m.startWorktreeFlow(ws)
}

func (m Model) startCleanupFlowForSelected() (tea.Model, tea.Cmd) {
	ws, ok := m.selectedWorkspace()
	if !ok {
		m.status = "Pick a repo/workspace"
		return m, nil
	}
	return m.startCleanupFlow(ws)
}

func (m Model) startCleanupFlow(ws workspace.Workspace) (tea.Model, tea.Cmd) {
	backend := vcs.ForPath(ws.Path)
	worktrees, err := backend.Worktrees(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("Worktrees failed: %v", err)
		return m, nil
	}
	removable := []vcs.Worktree{}
	for _, wt := range worktrees {
		if isBareWorktree(wt) {
			continue
		}
		removable = append(removable, wt)
	}
	if len(removable) == 0 {
		m.status = "No removable worktrees"
		return m, nil
	}
	m.searchActive = false
	m.paletteActive = false
	m.cleanupFlow = cleanupFlow{Active: true, Backend: backend, Workspace: ws, Worktrees: removable}
	m.status = "Clean up worktrees: pick one to remove"
	m.renderContent()
	m.ensureSelectedVisible()
	return m, nil
}

func (m Model) startWorktreeFlow(ws workspace.Workspace) (tea.Model, tea.Cmd) {
	backend := vcs.ForPath(ws.Path)
	branches, err := backend.Branches(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("Branches failed: %v", err)
		return m, nil
	}
	if len(branches) == 0 {
		m.status = "No branches found"
		return m, nil
	}
	worktrees, err := backend.Worktrees(ws.Path)
	if err != nil {
		m.status = fmt.Sprintf("Worktrees failed: %v", err)
		return m, nil
	}
	checkedOut := map[string]bool{}
	for _, wt := range worktrees {
		checkedOut[normalizeBranchRef(wt.Branch)] = true
	}
	m.searchActive = false
	m.paletteActive = false
	m.worktreeFlow = worktreeFlow{Active: true, Step: worktreeStepBranch, Backend: backend, Workspace: ws, Branches: branches, CheckedOut: checkedOut}
	m.status = "Create worktree: pick base branch"
	m.renderContent()
	m.ensureSelectedVisible()
	return m, nil
}

func isBareWorktree(wt vcs.Worktree) bool {
	path := filepath.Clean(wt.Path)
	base := filepath.Base(path)
	return wt.Bare || base == ".bare" || strings.HasSuffix(path, string(filepath.Separator)+".bare")
}

func (m Model) updateCleanupFlow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.cleanupFlow = cleanupFlow{}
		m.status = "Enter switches or creates sessions"
		m.renderContent()
		return m, nil
	case "up":
		m.moveCleanupSelection(-1)
		return m, nil
	case "down":
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
			return m.removeCleanupWorktree()
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
			m.status = "Pick a worktree"
			return m, nil
		}
		m.cleanupFlow.Worktrees = []vcs.Worktree{worktrees[m.cleanupFlow.Selected]}
		m.cleanupFlow.Selected = 0
		m.cleanupFlow.Confirm = true
		m.status = "Confirm remove worktree? y/n"
		m.renderContent()
		return m, nil
	}
	if printableKey(msg.String()) {
		m.cleanupFlow.Query += msg.String()
		m.cleanupFlow.Selected = 0
		m.renderContent()
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

func (m Model) filteredCleanupWorktrees() []vcs.Worktree {
	query := strings.TrimSpace(m.cleanupFlow.Query)
	worktrees := []vcs.Worktree{}
	for _, wt := range m.cleanupFlow.Worktrees {
		text := strings.Join([]string{wt.Path, wt.Branch, filepath.Base(wt.Path)}, " ")
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

func (m Model) removeCleanupWorktree() (tea.Model, tea.Cmd) {
	if len(m.cleanupFlow.Worktrees) == 0 {
		m.status = "Pick a worktree"
		return m, nil
	}
	wt := m.cleanupFlow.Worktrees[0]
	if session := m.sessionForPathOrName(wt.Path, filepath.Base(wt.Path)); session != nil {
		if session.Name == multiplexer.ShelfSessionName {
			m.status = fmt.Sprintf("Can't remove worktree with protected session %s", session.Name)
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
	if err := m.cleanupFlow.Backend.RemoveWorktree(m.cleanupFlow.Workspace.Path, wt.Path, false); err != nil {
		m.status = fmt.Sprintf("Remove failed: %v", err)
		return m, nil
	}
	m.cleanupFlow = cleanupFlow{}
	m.status = fmt.Sprintf("Removed worktree %s", filepath.Base(wt.Path))
	return m, m.doRefresh()
}

func (m Model) updateWorktreeFlow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.worktreeFlow = worktreeFlow{}
		m.status = "Enter switches or creates sessions"
		m.renderContent()
		return m, nil
	case "up":
		m.moveWorktreeFlowSelection(-1)
		return m, nil
	case "down":
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
			m.worktreeFlow.Query = m.worktreeFlow.WorktreeName
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
			m.worktreeFlow.Branch = branch
			m.worktreeFlow.Step = worktreeStepName
			m.worktreeFlow.Query = vcs.SuggestedWorktreeName(branch.Name)
			m.worktreeFlow.Pristine = true
			m.status = "Worktree name"
			m.renderContent()
			return m, nil
		case worktreeStepName:
			name := strings.TrimSpace(m.worktreeFlow.Query)
			if name == "" {
				m.status = "Enter a worktree name"
				return m, nil
			}
			m.worktreeFlow.WorktreeName = name
			m.worktreeFlow.Step = worktreeStepPath
			m.worktreeFlow.Query = name
			m.worktreeFlow.Pristine = true
			m.status = fmt.Sprintf("Worktree path relative to %s", m.worktreeFlow.Backend.SuggestedWorktreeParent(m.worktreeFlow.Workspace.Path))
			m.renderContent()
			return m, nil
		case worktreeStepPath:
			relPath := strings.TrimSpace(m.worktreeFlow.Query)
			if relPath == "" {
				m.status = "Enter a worktree path"
				return m, nil
			}
			m.worktreeFlow.WorktreePath = m.worktreeFlow.Backend.SuggestedWorktreePath(m.worktreeFlow.Workspace.Path, relPath)
			m.worktreeFlow.Query = ""
			m.worktreeFlow.Pristine = false
			if m.branchAlreadyCheckedOut(m.worktreeFlow.Branch) {
				m.worktreeFlow.NewBranch = true
				m.worktreeFlow.Step = worktreeStepNewBranch
				m.worktreeFlow.Query = m.worktreeFlow.WorktreeName
				m.worktreeFlow.Pristine = true
				m.status = "Branch is already checked out; enter new branch name"
				m.renderContent()
				return m, nil
			}
			m.worktreeFlow.Step = worktreeStepConfirmBranch
			m.status = "Create a new branch for this worktree? y/n"
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
	if printableKey(msg.String()) {
		m.appendWorktreeFlowInput(msg.String())
		m.renderContent()
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

func (m Model) branchAlreadyCheckedOut(branch vcs.Branch) bool {
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

func (m Model) filteredWorktreeBranches() []vcs.Branch {
	query := strings.TrimSpace(m.worktreeFlow.Query)
	branches := []vcs.Branch{}
	for _, branch := range m.worktreeFlow.Branches {
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
	if err := flow.Backend.CreateWorktree(vcs.CreateWorktreeRequest{RepoPath: flow.Workspace.Path, WorktreePath: flow.WorktreePath, BaseRef: flow.Branch.Name, NewBranchName: newBranch}); err != nil {
		m.status = fmt.Sprintf("Create worktree failed: %v", err)
		return m, nil
	}
	m.worktreeFlow = worktreeFlow{}
	return m.openCreatedWorktree(flow)
}

func (m Model) openCreatedWorktree(flow worktreeFlow) (tea.Model, tea.Cmd) {
	sessionName := m.sessionNameForNewPath(flow.WorktreePath, flow.WorktreeName)
	if m.state.Multiplexer.Kind == multiplexer.None {
		m.status = fmt.Sprintf("Created worktree %s", flow.WorktreePath)
		return m, m.doRefresh()
	}
	if !m.state.Multiplexer.SessionNames()[sessionName] {
		if err := multiplexer.NewSession(m.state.Multiplexer.Kind, sessionName, flow.WorktreePath); err != nil {
			m.status = fmt.Sprintf("Created worktree, session failed: %v", err)
			return m, m.doRefresh()
		}
	}
	switchSession := multiplexer.SwitchSession
	if m.state.SidebarMode {
		switchSession = multiplexer.SwitchSessionWithSidebar
	}
	if err := switchSession(m.state.Multiplexer.Kind, sessionName); err != nil {
		m.status = fmt.Sprintf("Created worktree, switch failed: %v", err)
		return m, m.doRefresh()
	}
	m.status = fmt.Sprintf("Created worktree and switched to %s", sessionName)
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
		for idx := range ws.Agents {
			if m.searchActive && strings.TrimSpace(m.searchQuery) != "" && !m.agentMatchesSearch(ws, idx) && !fuzzyMatch(m.searchQuery, workspaceSearchText(ws)) {
				continue
			}
			items = append(items, visibleItem{Kind: kindAgent, Workspace: ws, AgentIndex: idx})
		}
	}
	return items
}

func (m Model) paletteTargets() []paletteTarget {
	query := strings.TrimSpace(m.paletteQuery)
	targets := []paletteTarget{}
	if m.state.SidebarMode {
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
		if ws.GitType != 0 {
			createWorktreeTarget := paletteTarget{
				Action:   paletteAction{Kind: paletteActionCreateWorktree, Workspace: ws},
				Label:    "action",
				Title:    "Create worktree in " + ws.Name,
				Subtitle: ws.Path,
				Search:   strings.Join([]string{"action create new worktree", ws.Name, ws.Path, ws.ParentName, ws.Branch}, " "),
			}
			if score, ok := paletteTargetScore(query, createWorktreeTarget); ok {
				createWorktreeTarget.Score = score + 15
				targets = append(targets, createWorktreeTarget)
			}
			cleanupTarget := paletteTarget{
				Action:   paletteAction{Kind: paletteActionCleanupWorktrees, Workspace: ws},
				Label:    "action",
				Title:    "Clean up worktrees in " + ws.Name,
				Subtitle: "Remove a selected git worktree",
				Search:   strings.Join([]string{"action cleanup clean remove delete prune worktree", ws.Name, ws.Path, ws.ParentName, ws.Branch}, " "),
			}
			if score, ok := paletteTargetScore(query, cleanupTarget); ok {
				cleanupTarget.Score = score + 15
				targets = append(targets, cleanupTarget)
			}
		}
		if m.state.SidebarMode {
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
		switch {
		case token == query:
			best = max(best, 95*weight)
		case strings.HasPrefix(token, query):
			best = max(best, 75*weight-len(token))
		case strings.Contains(token, query):
			best = max(best, 60*weight-len(token))
		case len(token) >= len(query) && len(fuzzy.Find(query, []string{token})) > 0:
			best = max(best, 25*weight-len(token))
		}
	}
	return max(best, 0)
}

func indexPenalty(idx int) int {
	if idx > 80 {
		return 80
	}
	return idx
}

func paletteWorkspaceLabel(ws workspace.Workspace) string {
	if ws.IsWorktree {
		return "worktree"
	}
	if ws.GitType != 0 {
		return "repo"
	}
	return "workspace"
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
	lines = append(lines, m.renderer.styles.Title.Render("Clean up worktrees in "+flow.Workspace.Name))
	lines = append(lines, m.renderer.styles.Description.Render(flow.Workspace.Path), "")
	if flow.Confirm && len(flow.Worktrees) > 0 {
		wt := flow.Worktrees[0]
		lines = append(lines, m.renderer.styles.SelectedTitle.Render("Remove "+filepath.Base(wt.Path)+"?"))
		lines = append(lines, m.renderer.styles.Description.Render(wt.Path))
		lines = append(lines, m.renderer.styles.Description.Render("Branch: "+wt.Branch))
		lines = append(lines, "", m.renderer.styles.Description.Render("This will also kill a matching tmux session if one exists."))
		lines = append(lines, m.renderer.styles.Description.Render("Press y to remove · n/Esc to cancel"))
		m.viewport.SetContent(strings.Join(lines, "\n"))
		return
	}
	lines = append(lines, m.renderer.styles.Description.Render("Filter: "+flow.Query), "")
	worktrees := m.filteredCleanupWorktrees()
	for idx, wt := range worktrees {
		start := len(lines)
		title := m.styles.Badge.Render("worktree") + " " + filepath.Base(wt.Path)
		subtitle := wt.Path
		if wt.Branch != "" {
			subtitle += " · " + wt.Branch
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
	lines = append(lines, m.renderer.styles.Title.Render("Create worktree in "+flow.Workspace.Name))
	lines = append(lines, m.renderer.styles.Description.Render(flow.Workspace.Path), "")
	switch flow.Step {
	case worktreeStepBranch:
		lines = append(lines, m.renderer.styles.Description.Render("Branch: "+flow.Query), "")
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
		lines = append(lines, m.renderer.styles.Title.Render("Worktree name"))
		lines = append(lines, m.renderer.styles.SelectedDesc.Render(flow.Query))
		lines = append(lines, m.renderer.styles.Description.Render("Enter accepts default · typing replaces it"))
	case worktreeStepPath:
		lines = append(lines, m.renderer.styles.Title.Render("Worktree path"))
		lines = append(lines, m.renderer.styles.Description.Render("Relative to: "+flow.Backend.SuggestedWorktreeParent(flow.Workspace.Path)))
		lines = append(lines, m.renderer.styles.SelectedDesc.Render(flow.Query))
		lines = append(lines, m.renderer.styles.Description.Render("Enter accepts default · typing replaces it"))
	case worktreeStepConfirmBranch:
		lines = append(lines, m.renderer.styles.Title.Render("Base branch: "+flow.Branch.Name))
		lines = append(lines, m.renderer.styles.Description.Render("Path: "+flow.WorktreePath), "")
		lines = append(lines, m.renderer.styles.SelectedTitle.Render("Create a new branch for this worktree?"))
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
	if len(targets) == 0 {
		lines = append(lines, "No palette matches")
	}
	for idx, target := range targets {
		start := len(lines)
		lines = append(lines, strings.Split(m.renderPaletteTarget(target, idx == m.selected), "\n")...)
		if idx != len(targets)-1 {
			lines = append(lines, "")
		}
		m.spans = append(m.spans, itemSpan{start: start, end: len(lines)})
	}
	lines = append(lines, "", "", "")
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m Model) renderPaletteTarget(target paletteTarget, selected bool) string {
	label := m.styles.Badge.Render(target.Label)
	title := label + " " + target.Title
	if selected {
		return strings.Join([]string{m.renderer.styles.SelectedTitle.Render(title), m.renderer.styles.SelectedDesc.Render(target.Subtitle)}, "\n")
	}
	return strings.Join([]string{m.renderer.styles.Title.Render(title), m.renderer.styles.Description.Render(target.Subtitle)}, "\n")
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

func (m *Model) movePaletteSelection(delta int) {
	targets := m.paletteTargets()
	if len(targets) == 0 {
		return
	}
	m.selected = min(max(0, m.selected+delta), len(targets)-1)
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
	badge := m.styles.Badge.Render("Glint")
	header := fmt.Sprintf("%s  %d projects", badge, len(m.state.Workspaces))
	if m.searchActive {
		header = fmt.Sprintf("%s  /%s", header, m.searchQuery)
	}
	if m.paletteActive {
		header = fmt.Sprintf("%s  > %s", header, m.paletteQuery)
	}
	if m.worktreeFlow.Active {
		header = fmt.Sprintf("%s  worktree", header)
	}
	if m.cleanupFlow.Active {
		header = fmt.Sprintf("%s  cleanup", header)
	}
	return m.styles.Header.Render(header)
}

func (m Model) viewFooter() string {
	help := "↑/↓ move · / search · ctrl+p palette · ctrl+w worktree · ctrl+r cleanup · c/space collapse · Enter switch/create · n new chat · b shelve · ctrl+x delete · q quit"
	if m.state.SidebarMode {
		help = "↑/↓ move · / search · ctrl+p palette · ctrl+w worktree · ctrl+r cleanup · Enter bring/switch · b shelve · ctrl+x delete · c collapse · q quit"
	}
	if m.searchActive {
		help = "type to filter · ↑/↓ move · Enter select · ctrl+u clear · Esc close search"
	}
	if m.paletteActive {
		help = "type command · ↑/↓ move · Enter run/open · ctrl+u clear · Esc close palette"
	}
	if m.worktreeFlow.Active {
		help = "type to filter/edit · ↑/↓ move branches · Enter next/create · y/n choose · Esc cancel"
	}
	if m.cleanupFlow.Active {
		help = "type to filter · ↑/↓ move · Enter select · y confirm · n/Esc cancel"
	}
	content := fmt.Sprintf("status: %s\n%s", m.status, help)
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

func (m Model) activatePaletteAction(action paletteAction) (tea.Model, tea.Cmd) {
	switch action.Kind {
	case paletteActionNewAgent:
		return m.newAgentForWorkspace(action.Workspace)
	case paletteActionShelveMain:
		return m.shelveMainPane()
	case paletteActionCreateWorktree:
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
