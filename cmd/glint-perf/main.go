package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/config"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/ui"
	"github.com/ZatTwilight/glint/internal/workspace"
)

type timer struct {
	start time.Time
	last  time.Time
}

func newTimer() timer {
	now := time.Now()
	return timer{start: now, last: now}
}

func (t *timer) lap(name string) {
	now := time.Now()
	fmt.Printf("%-30s %9.2fms\n", name, float64(now.Sub(t.last).Microseconds())/1000)
	t.last = now
}

func (t timer) total() {
	fmt.Printf("%-30s %9.2fms\n", "TOTAL", float64(time.Since(t.start).Microseconds())/1000)
}

func main() {
	iterations := flag.Int("n", 1, "number of profiling iterations to run")
	mode := flag.String("mode", "full", "profiling mode: full, palette-launcher, or all")
	flag.Parse()

	if *iterations < 1 {
		fmt.Fprintln(os.Stderr, "-n must be >= 1")
		os.Exit(2)
	}
	if *mode != "full" && *mode != "palette-launcher" && *mode != "all" {
		fmt.Fprintln(os.Stderr, "-mode must be one of: full, palette-launcher, all")
		os.Exit(2)
	}

	for i := 0; i < *iterations; i++ {
		if *iterations > 1 {
			fmt.Printf("\niteration %d/%d\n", i+1, *iterations)
		}
		if err := runMode(*mode); err != nil {
			fmt.Fprintf(os.Stderr, "glint-perf: %v\n", err)
			os.Exit(1)
		}
	}
}

func runMode(mode string) error {
	switch mode {
	case "full":
		return runFullScan()
	case "palette-launcher":
		return runPaletteLauncher()
	case "all":
		fmt.Println("mode: full")
		if err := runFullScan(); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println("mode: palette-launcher")
		return runPaletteLauncher()
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func runFullScan() error {
	t := newTimer()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	t.lap("config.Load")

	appTheme := theme.Resolve(cfg.Theme)
	t.lap("theme.Resolve")

	mux := multiplexer.Detect()
	t.lap("multiplexer.Detect")

	programs := multiplexer.MultiplexerProgramsAll(agent.AgentName, agent.NewLazyDescendantCommands())
	t.lap(fmt.Sprintf("MultiplexerProgramsAll (%d)", len(programs)))

	sessionNames := mux.SessionNames()
	sessionPaths := mux.SessionPaths()
	t.lap("session maps")

	workspaces, err := workspace.ScanWithPrograms(cfg.WorkspaceRoots, sessionNames, sessionPaths, programs)
	if err != nil {
		return fmt.Errorf("scan workspaces: %w", err)
	}
	t.lap(fmt.Sprintf("workspace.Scan (%d)", len(workspaces)))

	_ = ui.New(ui.State{Multiplexer: mux, Workspaces: workspaces, WorkspaceRoots: cfg.WorkspaceRoots, Theme: appTheme}, nil)
	t.lap("ui.New")

	t.total()
	return nil
}

func runPaletteLauncher() error {
	t := newTimer()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	t.lap("config.Load")

	appTheme := theme.Resolve(cfg.Theme)
	t.lap("theme.Resolve")

	mux := multiplexer.Detect()
	t.lap("multiplexer.Detect")

	sessionNames := mux.SessionNames()
	sessionPaths := mux.SessionPaths()
	t.lap("session maps")

	workspaces, err := workspace.ScanProjectsWithPrograms(cfg.WorkspaceRoots, sessionNames, sessionPaths, []multiplexer.MultiplexerProgram{})
	if err != nil {
		return fmt.Errorf("scan palette projects: %w", err)
	}
	t.lap(fmt.Sprintf("workspace.ScanProjects (%d)", len(workspaces)))

	currentSession, _ := mux.CurrentSession()
	t.lap("mux.CurrentSession")

	paletteOptions := ui.MovementPaletteOptions()
	paletteOptions.LocalFirst = false
	paletteOptions.IncludeCleanupWorktrees = false
	_ = ui.NewPalette(ui.State{
		Multiplexer:    mux,
		Workspaces:     workspaces,
		WorkspaceRoots: cfg.WorkspaceRoots,
		CurrentSession: currentSession,
		Theme:          appTheme,
		Palette:        paletteOptions,
	}, nil)
	t.lap("ui.NewPalette")

	t.total()
	return nil
}
