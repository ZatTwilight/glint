package multiplexer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	type paneInfo struct{ sessionName, windowName, paneId, path, command, pid, title, active string }
	var panes []paneInfo
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 9 {
			continue
		}
		panes = append(panes, paneInfo{fields[0], fields[2], fields[3], fields[4], fields[5], fields[6], fields[7], fields[8]})
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
			baseValues := []string{pane.command, pane.title, pane.windowName, pane.sessionName}
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
				ProgramName: name, Session: pane.sessionName, Window: pane.windowName, Current: pane.active == "1",
			})
			mu.Unlock()
		})
	}
	wg.Wait()
	return programs
}
