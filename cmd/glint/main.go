package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/ZatTwilight/glint/internal/config"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/ui"
	"github.com/ZatTwilight/glint/internal/workspace"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	appTheme := theme.Resolve(cfg.Theme)

	refresh := func() (ui.State, error) {
		mux := multiplexer.Detect()
		workspaces, err := workspace.Scan(cfg.WorkspaceRoots, mux.SessionNames(), mux.SessionPaths())
		if err != nil {
			return ui.State{}, err
		}
		return ui.State{
			Multiplexer:    mux,
			Workspaces:     workspaces,
			WorkspaceRoots: cfg.WorkspaceRoots,
			Theme:          appTheme,
		}, nil
	}

	state, err := refresh()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan workspaces: %v\n", err)
		os.Exit(1)
	}

	model := ui.New(state, refresh)

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "glint: %v\n", err)
		os.Exit(1)
	}
}
