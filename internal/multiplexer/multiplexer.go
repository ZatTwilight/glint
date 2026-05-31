package multiplexer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type Kind string

const (
	None   Kind = "none"
	Tmux   Kind = "tmux"
	Zellij Kind = "zellij"
)

const ShelfSessionName = "glint-shelf"

type Info struct {
	Kind     Kind
	Sessions []Session
}

type Session struct {
	Name     string
	Path     string
	Windows  int
	Attached bool
	Activity time.Time
}

type PaneGeometry struct {
	ID     string
	Left   int
	Top    int
	Width  int
	Height int
}

type TmuxPane struct {
	ID      string
	Session string
	Window  string
	Title   string
	Command string
	Role    string
	Dead    bool
}

func (i Info) CurrentWindow() (string, error) {
	switch i.Kind {
	case Tmux:
		out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case Zellij:
		return "", fmt.Errorf("zellij session switching is not implemented yet")
	default:
		return "", fmt.Errorf("not running inside a supported multiplexer")
	}
}

func (i Info) CurrentSession() (string, error) {
	switch i.Kind {
	case Tmux:
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case Zellij:
		return "", fmt.Errorf("zellij session switching is not implemented yet")
	default:
		return "", fmt.Errorf("not running inside a supported multiplexer")
	}
}

func (i Info) SessionNames() map[string]bool {
	names := make(map[string]bool, len(i.Sessions))
	for _, session := range i.Sessions {
		names[session.Name] = true
	}
	return names
}

func (i Info) SessionByName() map[string]Session {
	sessions := make(map[string]Session, len(i.Sessions))
	for _, session := range i.Sessions {
		sessions[session.Name] = session
	}
	return sessions
}

func (i Info) SessionByPath() map[string]Session {
	sessions := make(map[string]Session, len(i.Sessions))
	for _, session := range i.Sessions {
		if session.Path != "" {
			sessions[filepath.Clean(session.Path)] = session
		}
	}
	return sessions
}

func (i Info) SessionPaths() map[string]bool {
	paths := make(map[string]bool, len(i.Sessions))
	for _, session := range i.Sessions {
		if session.Path != "" {
			paths[filepath.Clean(session.Path)] = true
		}
	}
	return paths
}

func Detect() Info {
	info := Info{Kind: None}
	if os.Getenv("TMUX") != "" {
		info.Kind = Tmux
		info.Sessions = tmuxSessions()
		return info
	}
	if os.Getenv("ZELLIJ") != "" {
		info.Kind = Zellij
		return info
	}
	return info
}

func SwitchSession(kind Kind, name string) error {
	switch kind {
	case Tmux:
		return exec.Command("tmux", "switch-client", "-t", name).Run()
	case Zellij:
		return fmt.Errorf("zellij session switching is not implemented yet")
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func SwitchPaneById(kind Kind, paneId string) error {
	switch kind {
	case Tmux:
		if err := exec.Command("tmux", "switch-client", "-t", paneId).Run(); err != nil {
			return err
		}
		return nil
	case Zellij:
		return fmt.Errorf("zellij pane switching is not implemented yet")
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func CurrentPaneID() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func TmuxPaneIDsAll() map[string]bool {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, id := range strings.Fields(string(out)) {
		ids[id] = true
	}
	return ids
}

func TmuxPanesAll() ([]TmuxPane, error) {
	format := strings.Join([]string{"#{pane_id}", "#{session_name}", "#{window_name}", "#{pane_title}", "#{pane_current_command}", "#{@glint_role}", "#{pane_dead}"}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, err
	}
	var panes []TmuxPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		panes = append(panes, TmuxPane{ID: fields[0], Session: fields[1], Window: fields[2], Title: fields[3], Command: fields[4], Role: fields[5], Dead: fields[6] == "1"})
	}
	return panes, nil
}

func TmuxPanesInCurrentWindow() ([]PaneGeometry, error) {
	format := strings.Join([]string{"#{pane_id}", "#{pane_left}", "#{pane_top}", "#{pane_width}", "#{pane_height}"}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-F", format).Output()
	if err != nil {
		return nil, err
	}

	var panes []PaneGeometry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		left, err1 := strconv.Atoi(fields[1])
		top, err2 := strconv.Atoi(fields[2])
		width, err3 := strconv.Atoi(fields[3])
		height, err4 := strconv.Atoi(fields[4])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		panes = append(panes, PaneGeometry{ID: fields[0], Left: left, Top: top, Width: width, Height: height})
	}
	return panes, nil
}

func FindPaneRightOf(sidebarPaneID string) (string, error) {
	panes, err := TmuxPanesInCurrentWindow()
	if err != nil {
		return "", err
	}

	var sidebar *PaneGeometry
	for i := range panes {
		if panes[i].ID == sidebarPaneID {
			sidebar = &panes[i]
			break
		}
	}
	if sidebar == nil {
		return "", fmt.Errorf("sidebar pane %s not found in current window", sidebarPaneID)
	}

	bestIdx := -1
	bestOverlap := -1
	bestDistance := 0
	for i := range panes {
		pane := panes[i]
		if pane.ID == sidebar.ID || pane.Left < sidebar.Left+sidebar.Width {
			continue
		}
		overlap := verticalOverlap(*sidebar, pane)
		if overlap <= 0 {
			continue
		}
		distance := pane.Left - (sidebar.Left + sidebar.Width)
		if bestIdx == -1 || distance < bestDistance || (distance == bestDistance && overlap > bestOverlap) {
			bestIdx = i
			bestDistance = distance
			bestOverlap = overlap
		}
	}
	if bestIdx == -1 {
		return "", fmt.Errorf("no pane found to the right of sidebar pane %s", sidebarPaneID)
	}
	return panes[bestIdx].ID, nil
}

func SwapPanes(sourcePaneID, targetPaneID string) error {
	if sourcePaneID == "" || targetPaneID == "" {
		return fmt.Errorf("source and target pane IDs are required")
	}
	if sourcePaneID == targetPaneID {
		return nil
	}
	return runTmux("swap-pane", "-s", sourcePaneID, "-t", targetPaneID)
}

func BringPaneToSidebarMain(sourcePaneID string) error {
	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return err
	}
	mainPaneID, err := FindPaneRightOf(sidebarPaneID)
	if err != nil {
		return err
	}
	return SwapPanes(sourcePaneID, mainPaneID)
}

func ShelveSidebarMain(originSession string) error {
	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return err
	}
	mainPaneID, err := FindPaneRightOf(sidebarPaneID)
	if err != nil {
		return err
	}
	return ShelvePane(originSession, mainPaneID)
}

func ShelvePane(originSession, paneID string) error {
	if strings.TrimSpace(originSession) == "" {
		return fmt.Errorf("current session is required")
	}
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("pane ID is required")
	}
	path, err := PaneCurrentPath(paneID)
	if err != nil {
		return err
	}
	scratchPaneID, err := NewShelfScratchPane(originSession, path)
	if err != nil {
		return err
	}
	if err := SwapPanes(scratchPaneID, paneID); err != nil {
		return err
	}
	_ = clearPaneRole(scratchPaneID)
	return nil
}

func PaneCurrentPath(paneID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_current_path}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func NewShelfScratchPane(originSession, path string) (string, error) {
	return newShelfPane(originSession, path, "", "scratch")
}

func NewShelfCommandPane(originSession, path, shellCommand string) (string, error) {
	if strings.TrimSpace(shellCommand) == "" {
		return "", fmt.Errorf("command is required")
	}
	return newShelfPane(originSession, path, shellCommand, "command")
}

func LaunchSidebarMainCommand(originSession, path, shellCommand string) error {
	if strings.TrimSpace(originSession) == "" {
		return fmt.Errorf("current session is required")
	}
	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return err
	}
	mainPaneID, err := FindPaneRightOf(sidebarPaneID)
	if err != nil {
		return err
	}
	commandPaneID, err := NewShelfCommandPane(originSession, path, shellCommand)
	if err != nil {
		return err
	}
	return SwapPanes(commandPaneID, mainPaneID)
}

func newShelfPane(originSession, path, shellCommand, label string) (string, error) {
	target, err := EnsureShelfWindowTarget(originSession)
	if err != nil {
		return "", err
	}
	args := []string{"split-window", "-d", "-t", target, "-P", "-F", "#{pane_id}"}
	if path != "" {
		args = append(args, "-c", path)
	}
	if shellCommand != "" {
		args = append(args, shellCommand)
	}
	out, err := outputTmux(args...)
	if err != nil && (strings.Contains(err.Error(), "no space for new pane") || strings.Contains(err.Error(), "can't find window")) {
		out, err = outputTmux(newShelfWindowArgs(originSession, path, shellCommand)...)
	}
	if err != nil {
		return "", err
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return "", fmt.Errorf("created shelf %s pane but tmux returned no pane ID", label)
	}
	if label == "scratch" {
		_ = setPaneRole(paneID, "scratch")
	} else {
		_ = clearPaneRole(paneID)
	}
	return paneID, nil
}

func newShelfWindowArgs(originSession, path, shellCommand string) []string {
	args := []string{"new-window", "-d", "-t", ShelfSessionName, "-n", originSession, "-P", "-F", "#{pane_id}"}
	if path != "" {
		args = append(args, "-c", path)
	}
	if shellCommand != "" {
		args = append(args, shellCommand)
	}
	return args
}

func EnsureShelfWindow(originSession string) error {
	_, err := EnsureShelfWindowTarget(originSession)
	return err
}

func EnsureShelfWindowTarget(originSession string) (string, error) {
	originSession = strings.TrimSpace(originSession)
	if originSession == "" {
		return "", fmt.Errorf("origin session is required")
	}
	if exec.Command("tmux", "has-session", "-t", ShelfSessionName).Run() != nil {
		if err := runTmux("new-session", "-d", "-s", ShelfSessionName, "-n", originSession); err != nil {
			return "", err
		}
	}
	if target := shelfWindowTarget(originSession); target != "" {
		_ = runTmux("set-option", "-w", "-t", target, "automatic-rename", "off")
		_ = runTmux("set-option", "-w", "-t", target, "@glint_origin_session", originSession)
		_ = runTmux("rename-window", "-t", target, originSession)
		return target, nil
	}
	out, err := outputTmux("new-window", "-d", "-t", ShelfSessionName, "-n", originSession, "-P", "-F", "#{window_id}")
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(string(out))
	if target == "" {
		return "", fmt.Errorf("created shelf window but tmux returned no window ID")
	}
	_ = runTmux("set-option", "-w", "-t", target, "automatic-rename", "off")
	_ = runTmux("set-option", "-w", "-t", target, "@glint_origin_session", originSession)
	_ = runTmux("rename-window", "-t", target, originSession)
	_ = markOnlyPaneScratch(target)
	return target, nil
}

func markOnlyPaneScratch(target string) error {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_id}").Output()
	if err != nil {
		return err
	}
	ids := strings.Fields(string(out))
	if len(ids) != 1 {
		return nil
	}
	return setPaneRole(ids[0], "scratch")
}

func setPaneRole(paneID, role string) error {
	return runTmux("set-option", "-p", "-t", paneID, "@glint_role", role)
}

func clearPaneRole(paneID string) error {
	return runTmux("set-option", "-p", "-u", "-t", paneID, "@glint_role")
}

func CleanupShelfScratchPanes() error {
	panes, err := TmuxPanesAll()
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, pane := range panes {
		if pane.Session != ShelfSessionName || (pane.Role != "scratch" && pane.Title != "glint-scratch") {
			continue
		}
		key := pane.Session + "\x00" + pane.Window
		if pane.Dead || kept[key] {
			_ = exec.Command("tmux", "kill-pane", "-t", pane.ID).Run()
			continue
		}
		kept[key] = true
	}
	return nil
}

func shelfWindowTarget(originSession string) string {
	out, err := exec.Command("tmux", "list-windows", "-t", ShelfSessionName, "-F", "#{window_id}\t#{window_name}\t#{@glint_origin_session}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		if fields[2] == originSession || fields[1] == originSession {
			return fields[0]
		}
	}
	return ""
}

func runTmux(args ...string) error {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func outputTmux(args ...string) ([]byte, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func verticalOverlap(a, b PaneGeometry) int {
	top := max(a.Top, b.Top)
	bottom := min(a.Top+a.Height, b.Top+b.Height)
	return max(0, bottom-top)
}

func SwitchPane(kind Kind, session, window, pane string) error {
	switch kind {
	case Tmux:
		if err := exec.Command("tmux", "switch-client", "-t", session).Run(); err != nil {
			return err
		}
		if window != "" {
			if err := exec.Command("tmux", "select-window", "-t", window).Run(); err != nil {
				return err
			}
		}
		if pane != "" {
			return exec.Command("tmux", "select-pane", "-t", pane).Run()
		}
		return nil
	case Zellij:
		return fmt.Errorf("zellij pane switching is not implemented yet")
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func NewSession(kind Kind, name, path string) error {
	switch kind {
	case Tmux:
		return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", path).Run()
	case Zellij:
		return fmt.Errorf("zellij session creation is not implemented yet")
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func tmuxSessions() []Session {
	format := strings.Join([]string{
		"#{session_name}",
		"#{session_path}",
		"#{session_windows}",
		"#{session_attached}",
		"#{session_activity}",
	}, "\t")
	out, err := exec.Command("tmux", "list-sessions", "-F", format).Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sessions := make([]Session, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) == 0 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		session := Session{Name: fields[0]}
		if len(fields) > 1 {
			session.Path = fields[1]
		}
		if len(fields) > 2 {
			session.Windows, _ = strconv.Atoi(fields[2])
		}
		if len(fields) > 3 {
			session.Attached = fields[3] != "0"
		}
		if len(fields) > 4 {
			unix, _ := strconv.ParseInt(fields[4], 10, 64)
			if unix > 0 {
				session.Activity = time.Unix(unix, 0)
			}
		}
		sessions = append(sessions, session)
	}
	return sessions
}

type MultiplexerProgram struct {
	PID           int
	Path          string
	StartTime     time.Time
	MultiplexerId string
	ProgramName   string
	Session       string
	Window        string
	Current       bool
}

func TmuxPrograms(workspacePath string, identify func(...string) (string, bool), descendants func(string) []string) []MultiplexerProgram {
	return FilterProgramsByWorkspace(TmuxProgramsAll(identify, descendants), workspacePath)
}

func FilterProgramsByWorkspace(programs []MultiplexerProgram, workspacePath string) []MultiplexerProgram {
	workspacePath = filepath.Clean(workspacePath)
	filtered := make([]MultiplexerProgram, 0, len(programs))
	for _, program := range programs {
		panePath := filepath.Clean(program.Path)
		if panePath == workspacePath || strings.HasPrefix(panePath, workspacePath+string(os.PathSeparator)) {
			filtered = append(filtered, program)
		}
	}
	return filtered
}

func TmuxProgramsAll(identify func(...string) (string, bool), descendants func(string) []string) []MultiplexerProgram {
	if os.Getenv("TMUX") == "" {
		return nil
	}

	format := strings.Join([]string{
		"#{session_name}", "#{window_id}", "#{window_name}", "#{pane_id}", "#{pane_current_path}",
		"#{pane_current_command}", "#{pane_pid}", "#{pane_title}", "#{pane_active}",
	}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil
	}

	type paneInfo struct{ sessionName, windowId, paneId, path, command, pid, title, active string }
	var panes []paneInfo
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 9 {
			continue
		}
		panes = append(panes, paneInfo{fields[0], fields[1], fields[3], fields[4], fields[5], fields[6], fields[7], fields[8]})
	}

	programs := make([]MultiplexerProgram, 0, len(panes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, pane := range panes {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			pid, err := strconv.Atoi(pane.pid)
			if err != nil {
				return
			}
			baseValues := []string{pane.command, pane.title, pane.windowId, pane.sessionName}
			name, ok := identify(baseValues...)
			if !ok {
				name, ok = identify(append(baseValues, descendants(pane.pid)...)...)
			}
			if !ok {
				return
			}
			p, err := process.NewProcess(int32(pid))
			if err != nil {
				return
			}
			createTimeMs, err := p.CreateTime()
			if err != nil {
				return
			}

			mu.Lock()
			programs = append(programs, MultiplexerProgram{
				PID: pid, Path: pane.path, StartTime: time.UnixMilli(createTimeMs), MultiplexerId: pane.paneId,
				ProgramName: name, Session: pane.sessionName, Window: pane.windowId, Current: pane.active == "1",
			})
			mu.Unlock()
		})
	}
	wg.Wait()
	sort.SliceStable(programs, func(i, j int) bool {
		if programs[i].Session != programs[j].Session {
			return programs[i].Session < programs[j].Session
		}
		if programs[i].Window != programs[j].Window {
			return programs[i].Window < programs[j].Window
		}
		return programs[i].MultiplexerId < programs[j].MultiplexerId
	})
	return programs
}
