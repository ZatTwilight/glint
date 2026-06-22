package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
		case "ptyd":
			if err := runPtyDaemon(); err != nil {
				fmt.Fprintf(os.Stderr, "glint ptyd: %v\n", err)
				os.Exit(1)
			}
			return
		case "pty":
			if err := runPty(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "glint pty: %v\n", err)
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
	mux := multiplexer.Detect()
	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = os.Args[0]
	}

	switch mux.Kind {
	case multiplexer.Tmux:
		currentSession, err := mux.CurrentSession()
		if err != nil {
			return err
		}
		if err := multiplexer.EnsureShelfWindow(currentSession); err != nil {
			return err
		}
		cmd := shellQuote(bin) + " sidebar"
		return exec.Command("tmux", "split-window", "-h", "-b", "-l", "36", cmd).Run()
	case multiplexer.Zellij:
		cwd, _ := os.Getwd()
		args := []string{"action", "new-pane", "--direction", "right", "--name", "glint-sidebar"}
		if strings.TrimSpace(cwd) != "" {
			args = append(args, "--cwd", cwd)
		}
		args = append(args, "--", bin, "sidebar")
		return exec.Command("zellij", args...).Run()
	default:
		return fmt.Errorf("attach requires tmux or zellij")
	}
}

func runPopup(args []string) error {
	mux := multiplexer.Detect()
	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = os.Args[0]
	}

	switch mux.Kind {
	case multiplexer.Tmux:
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
	case multiplexer.Zellij:
		cwd, _ := os.Getwd()
		popupArgs := []string{"action", "new-pane", "--floating", "--width", "80%", "--height", "60%", "--x", "10%", "--y", "20%", "--name", "glint-palette"}
		if strings.TrimSpace(cwd) != "" {
			popupArgs = append(popupArgs, "--cwd", cwd)
		}
		popupArgs = append(popupArgs, "--", bin, "palette")
		popupArgs = append(popupArgs, args...)
		return exec.Command("zellij", popupArgs...).Run()
	default:
		return fmt.Errorf("popup requires tmux or zellij")
	}
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
	state, refresh := appState(appStateOptions{SidebarMode: sidebarMode})
	model := ui.New(state, refresh)
	runProgram(model)
}

func runPalette(args []string) {
	paletteOptions, err := parsePaletteOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "glint palette: %v\n", err)
		os.Exit(1)
	}
	state, refresh := appState(appStateOptions{UseCache: true, Palette: paletteOptions})
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

func paletteNeedsAgentData(options ui.PaletteOptions) bool {
	if options == (ui.PaletteOptions{}) {
		options = ui.DefaultPaletteOptions()
	}
	return options.IncludeAgents
}

type appStateCache struct {
	WrittenAt      time.Time             `json:"written_at"`
	Multiplexer    multiplexer.Info      `json:"multiplexer"`
	Workspaces     []workspace.Workspace `json:"workspaces"`
	WorkspaceRoots []string              `json:"workspace_roots"`
	CurrentWindow  string                `json:"current_window"`
	CurrentSession string                `json:"current_session"`
}

func readAppStateCache() (ui.State, bool) {
	path := appStateCachePath()
	if path == "" {
		return ui.State{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ui.State{}, false
	}
	var cache appStateCache
	if json.Unmarshal(data, &cache) != nil || time.Since(cache.WrittenAt) > 10*time.Minute || len(cache.Workspaces) == 0 {
		return ui.State{}, false
	}
	return ui.State{
		Multiplexer:    cache.Multiplexer,
		Workspaces:     cache.Workspaces,
		WorkspaceRoots: cache.WorkspaceRoots,
		CurrentWindow:  cache.CurrentWindow,
		CurrentSession: cache.CurrentSession,
	}, true
}

func writeAppStateCache(state ui.State) {
	path := appStateCachePath()
	if path == "" || len(state.Workspaces) == 0 {
		return
	}
	cache := appStateCache{
		WrittenAt:      time.Now(),
		Multiplexer:    state.Multiplexer,
		Workspaces:     state.Workspaces,
		WorkspaceRoots: state.WorkspaceRoots,
		CurrentWindow:  state.CurrentWindow,
		CurrentSession: state.CurrentSession,
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

type appStateOptions struct {
	SidebarMode bool
	UseCache    bool
	Palette     ui.PaletteOptions
}

func appStateCachePath() string {
	base := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if base == "" {
		if cacheDir, err := os.UserCacheDir(); err == nil {
			base = cacheDir
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "glint", "app-state.json")
}

func appState(options appStateOptions) (ui.State, ui.RefreshFunc) {
	sidebarMode := options.SidebarMode
	useCache := options.UseCache
	paletteOptions := options.Palette

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	appTheme := theme.Resolve(cfg.Theme)
	spinnerName := firstNonEmpty(os.Getenv("GLINT_SPINNER"), cfg.Spinner)

	currentPath, _ := os.Getwd()
	initial := ui.State{
		WorkspaceRoots: cfg.WorkspaceRoots,
		CurrentPath:    currentPath,
		SidebarMode:    sidebarMode,
		Theme:          appTheme,
		Spinner:        spinnerName,
		Palette:        paletteOptions,
	}
	cachedInitial := false
	if useCache {
		if cached, ok := readAppStateCache(); ok {
			cached.WorkspaceRoots = cfg.WorkspaceRoots
			cached.CurrentPath = currentPath
			cached.SidebarMode = sidebarMode
			cached.Theme = appTheme
			cached.Spinner = spinnerName
			cached.Palette = paletteOptions
			initial = cached
			cachedInitial = true
		}
	}

	firstRefresh := true
	refresh := func() (ui.State, error) {
		fastCachedRefresh := useCache && firstRefresh
		projectOnlyRefresh := useCache && !paletteNeedsAgentData(paletteOptions)
		firstRefresh = false

		mux := multiplexer.Detect()
		if fastCachedRefresh && cachedInitial {
			currentSession, _ := mux.CurrentSession()
			currentPath, _ := os.Getwd()
			state := initial
			state.Multiplexer = mux
			state.CurrentSession = currentSession
			state.CurrentPath = currentPath
			state.Palette = paletteOptions
			return state, nil
		}

		programs := []multiplexer.MultiplexerProgram{}
		if !projectOnlyRefresh {
			programs = multiplexer.MultiplexerProgramsAll(agent.AgentName, agent.NewLazyDescendantCommands())
		}
		var workspaces []workspace.Workspace
		var err error
		if projectOnlyRefresh {
			workspaces, err = workspace.ScanProjectsWithPrograms(cfg.WorkspaceRoots, mux.SessionNames(), mux.SessionPaths(), programs)
		} else {
			workspaces, err = workspace.ScanWithPrograms(cfg.WorkspaceRoots, mux.SessionNames(), mux.SessionPaths(), programs)
		}
		if err != nil {
			return ui.State{}, err
		}
		currentWindow := ""
		currentSession := ""
		if projectOnlyRefresh {
			currentSession, _ = mux.CurrentSession()
		} else {
			currentWindow, err = mux.CurrentWindow()
			if err != nil {
				return ui.State{}, err
			}
			currentSession, err = mux.CurrentSession()
			if err != nil {
				return ui.State{}, err
			}
		}
		currentPath, _ := os.Getwd()
		if sidebarMode && mux.Kind == multiplexer.Tmux {
			_ = multiplexer.CleanupShelfScratchPanes()
		}

		state := ui.State{
			Multiplexer:    mux,
			Workspaces:     workspaces,
			WorkspaceRoots: cfg.WorkspaceRoots,
			CurrentWindow:  currentWindow,
			CurrentSession: currentSession,
			CurrentPath:    currentPath,
			SidebarMode:    sidebarMode,
			Theme:          appTheme,
			Spinner:        spinnerName,
			Palette:        paletteOptions,
		}
		writeAppStateCache(state)
		return state, nil
	}

	return initial, refresh
}

func runProgram(model tea.Model) {
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "glint: %v\n", err)
		os.Exit(1)
	}
}
