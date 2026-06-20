package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/config"
	"github.com/ZatTwilight/glint/internal/multiplexer"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/workspace"
)

type debugSnapshot struct {
	Config      config.Config                    `json:"config"`
	Theme       string                           `json:"theme"`
	Multiplexer multiplexer.Info                 `json:"multiplexer"`
	SessionMaps debugSessionMaps                 `json:"session_maps"`
	Programs    []multiplexer.MultiplexerProgram `json:"programs"`
	Workspaces  []workspace.Workspace            `json:"workspaces"`
	Timings     []debugTiming                    `json:"timings,omitempty"`
}

type debugSessionMaps struct {
	Names map[string]bool `json:"names"`
	Paths map[string]bool `json:"paths"`
}

type debugTiming struct {
	Name string  `json:"name"`
	MS   float64 `json:"ms"`
}

type debugTimer struct {
	last    time.Time
	timings []debugTiming
}

func newDebugTimer() *debugTimer {
	return &debugTimer{last: time.Now()}
}

func (t *debugTimer) lap(name string) {
	now := time.Now()
	t.timings = append(t.timings, debugTiming{Name: name, MS: float64(now.Sub(t.last).Microseconds()) / 1000})
	t.last = now
}

func runDebug(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: glint debug data [--timing]")
	}

	switch args[0] {
	case "data":
		return runDebugData(args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown debug command %q; usage: glint debug data [--timing]", args[0])
	}
}

func runDebugData(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("glint debug data", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	includeTiming := fs.Bool("timing", false, "include timing information in the JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	t := newDebugTimer()

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
	t.lap("multiplexer.MultiplexerProgramsAll")

	sessionNames := mux.SessionNames()
	sessionPaths := mux.SessionPaths()
	workspaces, err := workspace.ScanWithPrograms(cfg.WorkspaceRoots, sessionNames, sessionPaths, programs)
	if err != nil {
		return fmt.Errorf("scan workspaces: %w", err)
	}
	t.lap("workspace.ScanWithPrograms")

	snapshot := debugSnapshot{
		Config:      cfg,
		Theme:       string(appTheme.Name),
		Multiplexer: mux,
		SessionMaps: debugSessionMaps{Names: sessionNames, Paths: sessionPaths},
		Programs:    programs,
		Workspaces:  workspaces,
	}
	if *includeTiming {
		snapshot.Timings = t.timings
	}

	contents, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(contents))
	return err
}
