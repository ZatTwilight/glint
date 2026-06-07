package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/config"
	debuglog "github.com/ZatTwilight/glint/internal/debug"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/ui"
	"github.com/ZatTwilight/glint/internal/workspace"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--debug" || args[0] == "-d") {
		debuglog.Set(true)
		debuglog.Println("debug logging enabled")
		args = args[1:]
	}

	if len(args) > 0 {
		switch args[0] {
		case "attach":
			if err := runAttach(); err != nil {
				fmt.Fprintf(os.Stderr, "glint attach: %v\n", err)
				os.Exit(1)
			}
			return
		case "sidebar":
			runApp(true)
			return
		case "palette":
			runPalette(args[1:])
			return
		case "popup":
			if err := runPopup(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint popup: %v\n", err)
				os.Exit(1)
			}
			return
		case "hook":
			if err := runHook(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint hook: %v\n", err)
				os.Exit(1)
			}
			return
		case "events":
			if err := runEvents(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint events: %v\n", err)
				os.Exit(1)
			}
			return
		case "hooks":
			if err := runHooks(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint hooks: %v\n", err)
				os.Exit(1)
			}
			return
		case "debug":
			if err := runDebug(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint debug: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	runApp(false)
}

func runAttach() error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("attach requires tmux")
	}
	mux := multiplexer.Detect()
	currentSession, err := mux.CurrentSession()
	if err != nil {
		return err
	}
	if err := multiplexer.EnsureShelfWindow(currentSession); err != nil {
		return err
	}

	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = os.Args[0]
	}
	cmd := shellQuote(bin) + " sidebar"
	return exec.Command("tmux", "split-window", "-h", "-b", "-l", "36", cmd).Run()
}

func runPopup(args []string) error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("popup requires tmux")
	}
	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = os.Args[0]
	}
	cmd := shellQuote(bin) + " palette"
	for _, arg := range args {
		cmd += " " + shellQuote(arg)
	}
	popupArgs := []string{"display-popup", "-E", "-w", "80%", "-h", "60%"}
	if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_current_path}").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		popupArgs = append(popupArgs, "-d", strings.TrimSpace(string(out)))
	}
	popupArgs = append(popupArgs, cmd)
	return exec.Command("tmux", popupArgs...).Run()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runApp(sidebarMode bool) {
	if sidebarMode {
		_ = multiplexer.MarkCurrentPaneSidebar()
	}
	state, refresh := appState(sidebarMode)
	model := ui.New(state, refresh)
	runProgram(model)
}

func runPalette(args []string) {
	paletteOptions, err := parsePaletteOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "glint palette: %v\n", err)
		os.Exit(1)
	}
	state, refresh := appState(false)
	state.Palette = paletteOptions
	model := ui.NewPalette(state, refresh)
	runProgram(model)
}

func parsePaletteOptions(args []string) (ui.PaletteOptions, error) {
	options := ui.DefaultPaletteOptions()
	for _, arg := range args {
		switch arg {
		case "--movement", "--workspace", "--workspaces", "--movement-only":
			options = ui.MovementPaletteOptions()
		case "--local", "--local-first":
			options.LocalFirst = true
		case "--global":
			options.LocalFirst = false
		case "--no-agents":
			options.IncludeAgents = false
			options.IncludeNewAgent = false
		case "--no-actions":
			options.IncludeNewAgent = false
			options.IncludeShelveMain = false
			options.IncludeCreateWorktree = false
			options.IncludeCleanupWorktrees = false
		case "--no-workspaces":
			options.IncludeWorkspaces = false
		case "--no-create":
			options.IncludeCreateWorktree = false
		case "--no-cleanup":
			options.IncludeCleanupWorktrees = false
		case "--agents-only":
			options = ui.PaletteOptions{IncludeAgents: true}
		default:
			return options, fmt.Errorf("unknown option %q", arg)
		}
	}
	return options, nil
}

func appState(sidebarMode bool) (ui.State, ui.RefreshFunc) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	appTheme := theme.Resolve(cfg.Theme)
	spinnerName := firstNonEmpty(os.Getenv("GLINT_SPINNER"), cfg.Spinner)

	refresh := func() (ui.State, error) {
		mux := multiplexer.Detect()
		programs := multiplexer.TmuxProgramsAll(agent.AgentName, agent.NewLazyDescendantCommands())
		workspaces, err := workspace.ScanWithPrograms(cfg.WorkspaceRoots, mux.SessionNames(), mux.SessionPaths(), programs)
		if err != nil {
			return ui.State{}, err
		}
		currentWindow, err := mux.CurrentWindow()
		if err != nil {
			return ui.State{}, err
		}
		currentSession, err := mux.CurrentSession()
		if err != nil {
			return ui.State{}, err
		}
		currentPath, _ := os.Getwd()
		if sidebarMode {
			_ = multiplexer.CleanupShelfScratchPanes()
		}

		return ui.State{
			Multiplexer:    mux,
			Workspaces:     workspaces,
			WorkspaceRoots: cfg.WorkspaceRoots,
			CurrentWindow:  currentWindow,
			CurrentSession: currentSession,
			CurrentPath:    currentPath,
			SidebarMode:    sidebarMode,
			Theme:          appTheme,
			Spinner:        spinnerName,
		}, nil
	}

	currentPath, _ := os.Getwd()
	return ui.State{
		WorkspaceRoots: cfg.WorkspaceRoots,
		CurrentPath:    currentPath,
		SidebarMode:    sidebarMode,
		Theme:          appTheme,
		Spinner:        spinnerName,
	}, refresh
}

func runProgram(model tea.Model) {
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "glint: %v\n", err)
		os.Exit(1)
	}
}
