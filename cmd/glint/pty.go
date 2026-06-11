package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ZatTwilight/glint/internal/ptydaemon"
)

func runPtyDaemon() error {
	return ptydaemon.RunServer()
}

func runPty(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: glint pty <start|attach|detach|pane|switch|list|kill|socket> ...")
	}
	switch args[0] {
	case "socket":
		fmt.Println(ptydaemon.SocketPath())
		return nil
	case "list", "ls":
		resp, err := ptydaemon.List()
		if err != nil {
			return err
		}
		fmt.Print(ptydaemon.FormatSessions(resp.Sessions))
		return nil
	case "start":
		fs := flag.NewFlagSet("glint pty start", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		id := fs.String("id", "", "session id")
		cwd := fs.String("cwd", "", "working directory")
		jsonOut := fs.Bool("json", false, "print JSON response")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		command := fs.Args()
		if len(command) > 0 && command[0] == "--" {
			command = command[1:]
		}
		if *id == "" {
			return fmt.Errorf("--id is required")
		}
		if len(command) == 0 {
			return fmt.Errorf("command is required")
		}
		if strings.TrimSpace(*cwd) == "" {
			if wd, err := os.Getwd(); err == nil {
				*cwd = wd
			}
		}
		resp, err := ptydaemon.Start(*id, *cwd, command)
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(resp)
		}
		fmt.Print(ptydaemon.FormatSessions(resp.Sessions))
		return nil
	case "attach":
		if len(args) != 2 {
			return fmt.Errorf("usage: glint pty attach <id>")
		}
		return ptydaemon.Attach(args[1])
	case "pane":
		return runPtyPane(args[1:])
	case "switch":
		return runPtyPaneSwitch(args[1:])
	case "detach":
		if len(args) != 2 {
			return fmt.Errorf("usage: glint pty detach <id>")
		}
		_, err := ptydaemon.Detach(args[1])
		return err
	case "kill":
		if len(args) != 2 {
			return fmt.Errorf("usage: glint pty kill <id>")
		}
		_, err := ptydaemon.Kill(args[1])
		return err
	default:
		return fmt.Errorf("unknown pty command %q", args[0])
	}
}
