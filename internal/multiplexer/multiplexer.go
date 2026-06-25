package multiplexer

import (
	"encoding/json"
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
	Exited   bool
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

type zellijPane struct {
	ID           int    `json:"id"`
	IsPlugin     bool   `json:"is_plugin"`
	IsFocused    bool   `json:"is_focused"`
	IsSuppressed bool   `json:"is_suppressed"`
	Exited       bool   `json:"exited"`
	Title        string `json:"title"`
	PaneCommand  string `json:"pane_command"`
	PaneCWD      string `json:"pane_cwd"`
	TabID        int    `json:"tab_id"`
	TabPosition  int    `json:"tab_position"`
	TabName      string `json:"tab_name"`
	PaneX        int    `json:"pane_x"`
	PaneY        int    `json:"pane_y"`
	PaneRows     int    `json:"pane_rows"`
	PaneColumns  int    `json:"pane_columns"`
}

var (
	zellijPanesMu      sync.Mutex
	zellijPanesCache   []zellijPane
	zellijPanesCacheAt time.Time
)

func (i Info) CurrentWindow() (string, error) {
	switch i.Kind {
	case Tmux:
		args := []string{"display-message", "-p"}
		if paneID := strings.TrimSpace(os.Getenv("TMUX_PANE")); paneID != "" {
			args = append(args, "-t", paneID)
		}
		args = append(args, "#{window_id}")
		out, err := exec.Command("tmux", args...).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case Zellij:
		return zellijCurrentTabID()
	default:
		return "", fmt.Errorf("not running inside a supported multiplexer")
	}
}

func (i Info) CurrentSession() (string, error) {
	switch i.Kind {
	case Tmux:
		args := []string{"display-message", "-p"}
		if paneID := strings.TrimSpace(os.Getenv("TMUX_PANE")); paneID != "" {
			args = append(args, "-t", paneID)
		}
		args = append(args, "#{session_name}")
		out, err := exec.Command("tmux", args...).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case Zellij:
		if name := strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME")); name != "" {
			return name, nil
		}
		return zellijCurrentSessionName()
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
		info.Sessions = zellijSessions()
		return info
	}
	return info
}

func SwitchSession(kind Kind, name string) error {
	switch kind {
	case Tmux:
		return exec.Command("tmux", "switch-client", "-t", name).Run()
	case Zellij:
		return runZellij("action", "switch-session", name)
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
		return runZellij("action", "focus-pane-id", normalizeZellijPaneID(paneId))
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func CurrentPaneID() (string, error) {
	if paneID := strings.TrimSpace(os.Getenv("TMUX_PANE")); paneID != "" {
		return paneID, nil
	}
	if paneID := strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID")); paneID != "" {
		return normalizeZellijPaneID(paneID), nil
	}
	if os.Getenv("TMUX") != "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	if os.Getenv("ZELLIJ") != "" {
		return zellijFocusedPaneID()
	}
	return "", fmt.Errorf("not running inside a supported multiplexer")
}

func MultiplexerPaneIDsAll() map[string]bool {
	if os.Getenv("TMUX") != "" {
		return TmuxPaneIDsAll()
	}
	if os.Getenv("ZELLIJ") != "" {
		// Zellij's stable pane ids are per-session, and the CLI only exposes panes
		// for the current session. Returning nil avoids marking panes in other
		// sessions as completed just because they are not visible from here.
		return nil
	}
	return nil
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

func ZellijPaneIDsCurrentSession() map[string]bool {
	panes, err := zellijPanes()
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, pane := range panes {
		if pane.IsPlugin {
			continue
		}
		ids[normalizeZellijPaneID(strconv.Itoa(pane.ID))] = true
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
	return tmuxPanesInTarget("")
}

func TmuxPanesInWindowOfPane(paneID string) ([]PaneGeometry, error) {
	windowID, err := PaneWindowID(paneID)
	if err != nil {
		return nil, err
	}
	return tmuxPanesInTarget(windowID)
}

func PaneWindowID(paneID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
	if err != nil {
		return "", err
	}
	windowID := strings.TrimSpace(string(out))
	if windowID == "" {
		return "", fmt.Errorf("no window found for pane %s", paneID)
	}
	return windowID, nil
}

func tmuxPanesInTarget(target string) ([]PaneGeometry, error) {
	format := strings.Join([]string{"#{pane_id}", "#{pane_left}", "#{pane_top}", "#{pane_width}", "#{pane_height}"}, "\t")
	args := []string{"list-panes", "-F", format}
	if strings.TrimSpace(target) != "" {
		args = append(args, "-t", target)
	}
	out, err := exec.Command("tmux", args...).Output()
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
	pane, err := findPaneBesideSidebar(sidebarPaneID, true)
	if err != nil {
		return "", err
	}
	return pane.ID, nil
}

func FindMainPaneBesideSidebar(sidebarPaneID string) (string, error) {
	if pane, err := findPaneBesideSidebar(sidebarPaneID, true); err == nil {
		return pane.ID, nil
	}
	pane, err := findPaneBesideSidebar(sidebarPaneID, false)
	if err != nil {
		return "", err
	}
	return pane.ID, nil
}

func findPaneBesideSidebar(sidebarPaneID string, right bool) (*PaneGeometry, error) {
	panes, err := TmuxPanesInWindowOfPane(sidebarPaneID)
	if err != nil {
		return nil, err
	}

	var sidebar *PaneGeometry
	for i := range panes {
		if panes[i].ID == sidebarPaneID {
			sidebar = &panes[i]
			break
		}
	}
	if sidebar == nil {
		return nil, fmt.Errorf("sidebar pane %s not found in current window", sidebarPaneID)
	}

	bestIdx := -1
	bestOverlap := -1
	bestDistance := 0
	for i := range panes {
		pane := panes[i]
		if pane.ID == sidebar.ID {
			continue
		}
		var distance int
		if right {
			if pane.Left < sidebar.Left+sidebar.Width {
				continue
			}
			distance = pane.Left - (sidebar.Left + sidebar.Width)
		} else {
			if pane.Left+pane.Width > sidebar.Left {
				continue
			}
			distance = sidebar.Left - (pane.Left + pane.Width)
		}
		overlap := verticalOverlap(*sidebar, pane)
		if overlap <= 0 {
			continue
		}
		if bestIdx == -1 || distance < bestDistance || (distance == bestDistance && overlap > bestOverlap) {
			bestIdx = i
			bestDistance = distance
			bestOverlap = overlap
		}
	}
	if bestIdx == -1 {
		side := "right"
		if !right {
			side = "left"
		}
		return nil, fmt.Errorf("no pane found to the %s of sidebar pane %s", side, sidebarPaneID)
	}
	return &panes[bestIdx], nil
}

func SidebarPaneLayout(sidebarPaneID string) (width int, before bool, err error) {
	panes, err := TmuxPanesInWindowOfPane(sidebarPaneID)
	if err != nil {
		return 0, true, err
	}
	for _, pane := range panes {
		if pane.ID == sidebarPaneID {
			return pane.Width, pane.Left == 0, nil
		}
	}
	return 0, true, fmt.Errorf("sidebar pane %s not found in current window", sidebarPaneID)
}

func ActivePaneForSession(session string) (string, error) {
	if strings.TrimSpace(session) == "" {
		return "", fmt.Errorf("session is required")
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", session, "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return "", fmt.Errorf("no active pane found for session %s", session)
	}
	return paneID, nil
}

func SwitchSessionWithSidebar(kind Kind, targetSession string) error {
	targetSession = strings.TrimSpace(targetSession)
	if targetSession == "" {
		return fmt.Errorf("session is required")
	}
	if kind != Tmux {
		return SwitchSession(kind, targetSession)
	}
	targetPaneID, err := ActivePaneForSession(targetSession)
	if err != nil {
		return err
	}
	return SwitchPaneWithSidebar(kind, targetSession, "", targetPaneID)
}

func SwitchPaneWithSidebar(kind Kind, targetSession, targetWindow, targetPane string) error {
	if kind != Tmux {
		return SwitchPane(kind, targetSession, targetWindow, targetPane)
	}
	targetSession = strings.TrimSpace(targetSession)
	targetPane = strings.TrimSpace(targetPane)
	if targetSession == "" {
		return fmt.Errorf("session is required")
	}
	if targetPane == "" {
		return SwitchSessionWithSidebar(kind, targetSession)
	}

	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return err
	}
	currentSession, err := Detect().CurrentSession()
	if err == nil && currentSession == targetSession {
		return SwitchPane(kind, targetSession, targetWindow, targetPane)
	}

	width, before, err := SidebarPaneLayout(sidebarPaneID)
	if err != nil || width <= 0 {
		width = 36
		before = true
	}
	if targetPane != sidebarPaneID {
		args := sidebarJoinPaneArgs(sidebarPaneID, targetPane, width, before, true)
		if err := runTmux(args...); err != nil {
			fallbackArgs := sidebarJoinPaneArgs(sidebarPaneID, targetPane, width, before, false)
			if fallbackErr := runTmux(fallbackArgs...); fallbackErr != nil {
				return err
			}
		}
	}
	return SwitchPane(kind, targetSession, targetWindow, targetPane)
}

func sidebarJoinPaneArgs(sidebarPaneID, targetPaneID string, width int, before bool, fullSize bool) []string {
	args := []string{"join-pane", "-d", "-h"}
	if fullSize {
		args = append(args, "-f")
	}
	if before {
		args = append(args, "-b")
	}
	return append(args, "-l", strconv.Itoa(width), "-s", sidebarPaneID, "-t", targetPaneID)
}

func MarkCurrentPaneSidebar() error {
	paneID, err := CurrentPaneID()
	if err != nil {
		return err
	}
	if os.Getenv("ZELLIJ") != "" && os.Getenv("TMUX") == "" {
		return runZellij("action", "rename-pane", "glint-sidebar")
	}
	return setPaneRole(paneID, "sidebar")
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

func CurrentNativeAgentPaneName() (string, error) {
	if os.Getenv("ZELLIJ") != "" && os.Getenv("TMUX") == "" {
		windowID, err := zellijCurrentTabID()
		if err != nil {
			return "", err
		}
		return nativeAgentPaneName("zellij-" + windowID), nil
	}
	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return "", err
	}
	windowID, err := PaneWindowID(sidebarPaneID)
	if err != nil {
		return "", err
	}
	return nativeAgentPaneName(windowID), nil
}

func EnsureNativeAgentPane(command string) (string, string, error) {
	if os.Getenv("ZELLIJ") != "" && os.Getenv("TMUX") == "" {
		paneName, err := CurrentNativeAgentPaneName()
		if err != nil {
			return "", "", err
		}
		if paneID := zellijNamedPane(paneName); paneID != "" {
			return paneName, paneID, nil
		}
		out, err := outputZellij("action", "new-pane", "--direction", "right", "--name", paneName, "--", shellPath(), "-lc", command)
		if err != nil {
			return "", "", err
		}
		paneID := normalizeZellijPaneID(strings.TrimSpace(string(out)))
		if paneID == "" {
			return "", "", fmt.Errorf("created native agent pane but zellij returned no pane ID")
		}
		return paneName, paneID, nil
	}

	sidebarPaneID, err := CurrentPaneID()
	if err != nil {
		return "", "", err
	}
	paneName, err := CurrentNativeAgentPaneName()
	if err != nil {
		return "", "", err
	}
	if paneID := nativeAgentPaneInWindow(sidebarPaneID, paneName); paneID != "" {
		return paneName, paneID, nil
	}

	// Do not respawn an arbitrary user pane. Create a dedicated viewer pane by
	// splitting the main pane to the right of the sidebar, then mark only the new
	// pane as Glint-managed.
	targetPaneID, err := FindMainPaneBesideSidebar(sidebarPaneID)
	if err != nil || strings.TrimSpace(targetPaneID) == "" {
		targetPaneID = sidebarPaneID
	}
	out, err := outputTmux("split-window", "-d", "-h", "-t", targetPaneID, "-P", "-F", "#{pane_id}", command)
	if err != nil {
		return "", "", err
	}
	paneID := strings.TrimSpace(string(out))
	if paneID == "" {
		return "", "", fmt.Errorf("created native agent pane but tmux returned no pane ID")
	}
	if err := runTmux("set-option", "-p", "-t", paneID, "@glint_role", "pty-pane"); err != nil {
		return "", "", err
	}
	if err := runTmux("set-option", "-p", "-t", paneID, "@glint_pty_pane_name", paneName); err != nil {
		return "", "", err
	}
	return paneName, paneID, nil
}

func nativeAgentPaneInWindow(referencePaneID, paneName string) string {
	windowID, err := PaneWindowID(referencePaneID)
	if err != nil {
		return ""
	}
	format := strings.Join([]string{"#{pane_id}", "#{@glint_role}", "#{@glint_pty_pane_name}", "#{pane_dead}"}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", format).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 4 && fields[1] == "pty-pane" && fields[2] == paneName && fields[3] != "1" {
			return fields[0]
		}
	}
	return ""
}

func nativeAgentPaneName(windowID string) string {
	name := strings.TrimSpace(windowID)
	name = strings.TrimPrefix(name, "@")
	if name == "" {
		name = "main"
	}
	if strings.HasPrefix(name, "zellij-") {
		return name + "-agent"
	}
	return "tmux-" + name + "-agent"
}

func PaneOption(paneID, option string) (string, error) {
	out, err := exec.Command("tmux", "show-option", "-p", "-v", "-t", paneID, option).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

func runZellij(args ...string) error {
	return runZellijInDir("", args...)
}

func runZellijInDir(dir string, args ...string) error {
	cmd := exec.Command("zellij", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("zellij %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func outputZellij(args ...string) ([]byte, error) {
	out, err := exec.Command("zellij", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return nil, err
		}
		return nil, fmt.Errorf("zellij %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func zellijSessionPanes(session string) ([]zellijPane, error) {
	out, err := outputZellij("--session", session, "action", "list-panes", "--json", "--all")
	if err != nil {
		return nil, err
	}
	var panes []zellijPane
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, err
	}
	return panes, nil
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
		if strings.TrimSpace(session) != "" {
			args := []string{"action", "switch-session"}
			if strings.TrimSpace(pane) != "" {
				args = append(args, "--pane-id", normalizeZellijPaneID(pane))
			}
			args = append(args, session)
			if err := runZellij(args...); err != nil {
				return err
			}
		}
		if strings.TrimSpace(window) != "" {
			if err := runZellij("action", "go-to-tab-by-id", window); err != nil {
				return err
			}
		}
		if strings.TrimSpace(pane) != "" {
			return runZellij("action", "focus-pane-id", normalizeZellijPaneID(pane))
		}
		return nil
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func NewSession(kind Kind, name, path string) error {
	switch kind {
	case Tmux:
		return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", path).Run()
	case Zellij:
		if strings.TrimSpace(path) == "" {
			return runZellij("attach", "--create-background", name)
		}
		return runZellijInDir(path, "attach", "--create-background", name)
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func KillSession(kind Kind, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name is required")
	}
	switch kind {
	case Tmux:
		return exec.Command("tmux", "kill-session", "-t", name).Run()
	case Zellij:
		return runZellij("kill-session", name)
	default:
		return fmt.Errorf("not running inside a supported multiplexer")
	}
}

func KillPane(kind Kind, paneID string) error {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return fmt.Errorf("pane id is required")
	}
	switch kind {
	case Tmux:
		return exec.Command("tmux", "kill-pane", "-t", paneID).Run()
	case Zellij:
		return runZellij("action", "close-pane", "--pane-id", normalizeZellijPaneID(paneID))
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

func ZellijSessionPath(session string) string {
	return zellijSessionPrimaryPath(session)
}

func zellijSessionPrimaryPath(session string) string {
	panes, err := zellijSessionPanes(session)
	if err != nil {
		return ""
	}
	fallback := ""
	for _, pane := range panes {
		if pane.IsPlugin || pane.Exited || strings.TrimSpace(pane.PaneCWD) == "" || isGlintZellijPane(pane) {
			continue
		}
		path := strings.TrimSpace(pane.PaneCWD)
		if pane.IsFocused || strings.TrimSpace(pane.Title) == "main" {
			return path
		}
		if fallback == "" {
			fallback = path
		}
	}
	return fallback
}

func zellijSessions() []Session {
	out, err := exec.Command("zellij", "list-sessions", "--no-formatting").Output()
	if err != nil {
		return nil
	}
	current := strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME"))
	currentPath, _ := os.Getwd()
	return parseZellijSessions(string(out), current, currentPath)
}

func parseZellijSessions(output, current, currentPath string) []Session {
	var sessions []Session
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		exited := strings.Contains(strings.ToLower(line), "(exited")
		name := line
		if idx := strings.Index(name, " ["); idx >= 0 {
			name = strings.TrimSpace(name[:idx])
		} else if idx := strings.Index(name, " ("); idx >= 0 {
			name = strings.TrimSpace(name[:idx])
		} else if fields := strings.Fields(name); len(fields) > 0 {
			name = fields[0]
		}
		if name == "" {
			continue
		}
		session := Session{Name: name, Attached: strings.Contains(line, "(current)") || name == current, Exited: exited}
		if session.Attached && currentPath != "" {
			session.Path = currentPath
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

func MultiplexerPrograms(workspacePath string, identify func(...string) (string, bool), descendants func(string) []string) []MultiplexerProgram {
	return FilterProgramsByWorkspace(MultiplexerProgramsAll(identify, descendants), workspacePath)
}

func MultiplexerProgramsAll(identify func(...string) (string, bool), descendants func(string) []string) []MultiplexerProgram {
	if os.Getenv("TMUX") != "" {
		return TmuxProgramsAll(identify, descendants)
	}
	if os.Getenv("ZELLIJ") != "" {
		return ZellijProgramsAll(identify)
	}
	return nil
}

func TmuxPrograms(workspacePath string, identify func(...string) (string, bool), descendants func(string) []string) []MultiplexerProgram {
	return FilterProgramsByWorkspace(TmuxProgramsAll(identify, descendants), workspacePath)
}

func ZellijPrograms(workspacePath string, identify func(...string) (string, bool)) []MultiplexerProgram {
	return FilterProgramsByWorkspace(ZellijProgramsAll(identify), workspacePath)
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
		"#{pane_current_command}", "#{pane_pid}", "#{pane_title}", "#{pane_active}", "#{@glint_role}",
	}, "\t")
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil
	}

	type paneInfo struct{ sessionName, windowId, paneId, path, command, pid, title, active string }
	var panes []paneInfo
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 10 {
			continue
		}
		if role := strings.TrimSpace(fields[9]); role == "sidebar" || role == "pty-pane" {
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
	sortPrograms(programs)
	return programs
}

func ZellijProgramsAll(identify func(...string) (string, bool)) []MultiplexerProgram {
	if os.Getenv("ZELLIJ") == "" || os.Getenv("TMUX") != "" {
		return nil
	}
	panes, err := zellijPanes()
	if err != nil {
		return nil
	}
	session := strings.TrimSpace(os.Getenv("ZELLIJ_SESSION_NAME"))
	currentPane := strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID"))
	programs := make([]MultiplexerProgram, 0, len(panes))
	for _, pane := range panes {
		if pane.IsPlugin || pane.Exited || strings.TrimSpace(pane.PaneCWD) == "" || isGlintZellijPane(pane) {
			continue
		}
		paneID := normalizeZellijPaneID(strconv.Itoa(pane.ID))
		baseValues := []string{pane.PaneCommand, pane.Title, pane.TabName, strconv.Itoa(pane.TabID), session}
		name, ok := identify(baseValues...)
		if !ok {
			continue
		}
		programs = append(programs, MultiplexerProgram{
			Path: pane.PaneCWD, MultiplexerId: paneID, ProgramName: name, Session: session,
			Window: strconv.Itoa(pane.TabID), Current: zellijPaneIDMatches(currentPane, paneID),
		})
	}
	sortPrograms(programs)
	return programs
}

func sortPrograms(programs []MultiplexerProgram) {
	sort.SliceStable(programs, func(i, j int) bool {
		if programs[i].Session != programs[j].Session {
			return programs[i].Session < programs[j].Session
		}
		if programs[i].Window != programs[j].Window {
			return programs[i].Window < programs[j].Window
		}
		return programs[i].MultiplexerId < programs[j].MultiplexerId
	})
}

func zellijPanes() ([]zellijPane, error) {
	zellijPanesMu.Lock()
	if time.Since(zellijPanesCacheAt) < 750*time.Millisecond && zellijPanesCache != nil {
		panes := append([]zellijPane(nil), zellijPanesCache...)
		zellijPanesMu.Unlock()
		return panes, nil
	}
	zellijPanesMu.Unlock()

	out, err := exec.Command("zellij", "action", "list-panes", "--json", "--all").Output()
	if err != nil {
		return nil, err
	}
	var panes []zellijPane
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, err
	}

	zellijPanesMu.Lock()
	zellijPanesCache = append([]zellijPane(nil), panes...)
	zellijPanesCacheAt = time.Now()
	zellijPanesMu.Unlock()
	return panes, nil
}

func zellijCurrentSessionName() (string, error) {
	out, err := exec.Command("zellij", "list-sessions", "--no-formatting").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.Contains(line, "(current)") {
			continue
		}
		name := strings.TrimSpace(line)
		if idx := strings.Index(name, " ["); idx >= 0 {
			name = strings.TrimSpace(name[:idx])
		}
		if name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("no current zellij session found")
}

func zellijCurrentTabID() (string, error) {
	currentPane := strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID"))
	panes, err := zellijPanes()
	if err != nil {
		return "", err
	}
	for _, pane := range panes {
		if !pane.IsPlugin && zellijPaneIDMatches(currentPane, normalizeZellijPaneID(strconv.Itoa(pane.ID))) {
			return strconv.Itoa(pane.TabID), nil
		}
	}
	for _, pane := range panes {
		if !pane.IsPlugin && pane.IsFocused {
			return strconv.Itoa(pane.TabID), nil
		}
	}
	return "", fmt.Errorf("no current zellij tab found")
}

func zellijFocusedPaneID() (string, error) {
	panes, err := zellijPanes()
	if err != nil {
		return "", err
	}
	for _, pane := range panes {
		if !pane.IsPlugin && pane.IsFocused {
			return normalizeZellijPaneID(strconv.Itoa(pane.ID)), nil
		}
	}
	return "", fmt.Errorf("no focused zellij pane found")
}

func zellijCurrentPaneCWD(paneID string) string {
	panes, err := zellijPanes()
	if err != nil {
		return ""
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		paneID = strings.TrimSpace(os.Getenv("ZELLIJ_PANE_ID"))
	}
	for _, pane := range panes {
		if !pane.IsPlugin && zellijPaneIDMatches(paneID, normalizeZellijPaneID(strconv.Itoa(pane.ID))) {
			return strings.TrimSpace(pane.PaneCWD)
		}
	}
	for _, pane := range panes {
		if !pane.IsPlugin && pane.IsFocused && strings.TrimSpace(pane.PaneCWD) != "" {
			return strings.TrimSpace(pane.PaneCWD)
		}
	}
	return ""
}

func zellijNamedPane(name string) string {
	panes, err := zellijPanes()
	if err != nil {
		return ""
	}
	for _, pane := range panes {
		if pane.IsPlugin || pane.Exited {
			continue
		}
		if strings.TrimSpace(pane.Title) == name {
			return normalizeZellijPaneID(strconv.Itoa(pane.ID))
		}
	}
	return ""
}

func normalizeZellijPaneID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "terminal_") || strings.HasPrefix(id, "plugin_") {
		return id
	}
	return "terminal_" + id
}

func zellijPaneIDMatches(left, right string) bool {
	left = normalizeZellijPaneID(left)
	right = normalizeZellijPaneID(right)
	return left != "" && left == right
}

func isGlintZellijPane(pane zellijPane) bool {
	title := strings.TrimSpace(pane.Title)
	command := strings.TrimSpace(pane.PaneCommand)
	return title == "glint-sidebar" || strings.HasSuffix(title, "-agent") && strings.HasPrefix(title, "zellij-") || strings.Contains(command, "glint sidebar") || strings.Contains(command, "glint pty pane")
}

func shellPath() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
