package main

import (
  "fmt"
  "time"
  "github.com/ZatTwilight/glint/internal/agent"
  "github.com/ZatTwilight/glint/internal/config"
  "github.com/ZatTwilight/glint/internal/multiplexer"
  "github.com/ZatTwilight/glint/internal/theme"
  "github.com/ZatTwilight/glint/internal/ui"
  "github.com/ZatTwilight/glint/internal/workspace"
)

func lap(name string, start time.Time) time.Time { now:=time.Now(); fmt.Printf("%-28s %8.2fms\n", name, float64(now.Sub(start).Microseconds())/1000); return now }
func main(){
  t0:=time.Now(); t:=t0
  cfg, err := config.Load(); if err != nil { panic(err) }
  t=lap("config.Load", t)
  appTheme := theme.Resolve(cfg.Theme)
  t=lap("theme.Resolve", t)
  mux := multiplexer.Detect()
  t=lap("multiplexer.Detect", t)
  programs := multiplexer.TmuxProgramsAll(agent.AgentName, agent.DescendantCommands)
  t=lap(fmt.Sprintf("TmuxProgramsAll (%d)", len(programs)), t)
  names := mux.SessionNames(); paths := mux.SessionPaths()
  t=lap("session maps", t)
  wss, err := workspace.ScanWithPrograms(cfg.WorkspaceRoots, names, paths, programs); if err != nil { panic(err) }
  t=lap(fmt.Sprintf("workspace.Scan (%d)", len(wss)), t)
  _ = ui.New(ui.State{Multiplexer:mux, Workspaces:wss, WorkspaceRoots:cfg.WorkspaceRoots, Theme:appTheme}, nil)
  t=lap("ui.New", t)
  fmt.Printf("%-28s %8.2fms\n", "TOTAL", float64(time.Since(t0).Microseconds())/1000)
}
