package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	debuglog "github.com/ZatTwilight/glint/internal/debug"
	"github.com/ZatTwilight/glint/internal/ptydaemon"
)

func runPtyPane(args []string) error {
	fs := flag.NewFlagSet("glint pty pane", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "main", "pane name")
	initial := fs.String("session", "", "initial PTY session id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(*initial) != "" {
		if err := ptydaemon.WritePaneState(*name, ptydaemon.PaneState{Session: strings.TrimSpace(*initial), Updated: time.Now()}); err != nil {
			return err
		}
		debuglog.Printf("pty pane %q: initial session %q\n", *name, *initial)
	}

	for {
		state, err := ptydaemon.ReadPaneState(*name)
		if err != nil && !os.IsNotExist(err) {
			debuglog.Printf("pty pane %q: read state error: %v\n", *name, err)
			return err
		}
		target := strings.TrimSpace(state.Session)
		if target == "" {
			resetTerminalPane()
			fmt.Printf("glint pty pane %q waiting. Switch it with:\n\n  glint pty switch --pane %s <session-id>\n", *name, *name)
			for {
				time.Sleep(300 * time.Millisecond)
				next, err := ptydaemon.ReadPaneState(*name)
				if err != nil && !os.IsNotExist(err) {
					debuglog.Printf("pty pane %q: polling state error: %v\n", *name, err)
				}
				if strings.TrimSpace(next.Session) != "" {
					debuglog.Printf("pty pane %q: woke from wait for %q\n", *name, next.Session)
					break
				}
			}
			continue
		}

		resetTerminalPane()
		debuglog.Printf("pty pane %q: attaching to %q\n", *name, target)
		err = ptydaemon.Attach(target)
		fmt.Printf("\x1b[0m\x1b[?25h")
		if err != nil {
			fmt.Fprintf(os.Stderr, "glint pty pane: attach %s: %v\n", target, err)
			debuglog.Printf("pty pane %q: attach %q failed: %v\n", *name, target, err)
			time.Sleep(time.Second)
		}

		next, _ := ptydaemon.ReadPaneState(*name)
		nextTarget := strings.TrimSpace(next.Session)
		if nextTarget != "" && nextTarget != target {
			debuglog.Printf("pty pane %q: external switch %q -> %q\n", *name, target, nextTarget)
			// External switch: the pane state already points at the next target.
			continue
		}
		if !ptySessionRunning(target) {
			debuglog.Printf("pty pane %q: session %q not running, returning to shell\n", *name, target)
			_ = ptydaemon.WritePaneState(*name, ptydaemon.PaneState{Session: "", Updated: time.Now()})
			return execUserShell()
		}
		debuglog.Printf("pty pane %q: session %q still running, returning to wait\n", *name, target)
		// Manual detach or transient connection close: remain managed and wait.
		_ = ptydaemon.WritePaneState(*name, ptydaemon.PaneState{Session: "", Updated: time.Now()})
	}
}

func runPtyPaneSwitch(args []string) error {
	fs := flag.NewFlagSet("glint pty switch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pane := fs.String("pane", "main", "pane name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: glint pty switch [--pane main] <session-id>")
	}
	target := strings.TrimSpace(fs.Arg(0))
	if target == "" {
		return fmt.Errorf("session id is required")
	}
	return ptydaemon.SwitchPane(*pane, target)
}

func ptySessionRunning(id string) bool {
	resp, err := ptydaemon.List()
	if err != nil {
		return false
	}
	for _, session := range resp.Sessions {
		if session.ID == id {
			return session.Running
		}
	}
	return false
}

func execUserShell() error {
	if pane := strings.TrimSpace(os.Getenv("TMUX_PANE")); pane != "" {
		unsetTmuxPaneOption(pane, "@glint_role")
		unsetTmuxPaneOption(pane, "@glint_pty_pane_name")
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	resetTerminalPane()
	return syscall.Exec(shell, []string{shell, "-l"}, os.Environ())
}

func resetTerminalPane() {
	fmt.Print("\x1b[0m\x1b[?25h\x1b[3J\x1b[2J\x1b[H")
	if pane := strings.TrimSpace(os.Getenv("TMUX_PANE")); pane != "" {
		_ = exec.Command("tmux", "clear-history", "-t", pane).Run()
	}
}

func unsetTmuxPaneOption(pane, option string) {
	_ = exec.Command("tmux", "set-option", "-p", "-u", "-t", pane, option).Run()
}
