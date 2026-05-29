package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ZatTwilight/glint/internal/agent"
)

func runHook(args []string) error {
	if len(args) < 2 || args[0] == "-h" || args[0] == "--help" {
		return fmt.Errorf("usage: glint hook <agent> <event> [--workspace <path>] [--session <id>] [--task <text>] [--status <status>] [--pane <id>]")
	}
	agentName, event := args[0], args[1]
	flags, err := parseHookFlags(args[2:])
	if err != nil {
		return err
	}
	raw := agent.ReadStdinIfPiped(os.Stdin, os.Stdin)
	record, err := agent.RecordHook(agentName, event, agent.HookInput{
		Workspace: flags["workspace"],
		SessionID: firstFlag(flags, "session", "session-id"),
		Task:      flags["task"],
		Status:    agent.Status(flags["status"]),
		Pane:      flags["pane"],
		Raw:       raw,
	})
	if err != nil {
		return err
	}
	if flags["json"] == "true" {
		encoded, _ := json.Marshal(record)
		fmt.Println(string(encoded))
		return nil
	}
	fmt.Printf("recorded %s %s as %s", record.Agent, record.Event, record.Status)
	if record.Workspace != "" {
		fmt.Printf(" in %s", record.Workspace)
	}
	fmt.Println()
	return nil
}

func runEvents(args []string) error {
	limit := 20
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" {
			return fmt.Errorf("usage: glint events [limit]")
		}
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 1 {
			return fmt.Errorf("limit must be a positive integer")
		}
		limit = parsed
	}
	records, err := agent.TailHookEvents(limit)
	if err != nil {
		return err
	}
	for _, rec := range records {
		fmt.Printf("%s %-8s %-14s %-16s %s\n", rec.Time.Format("2006-01-02 15:04:05"), rec.Agent, rec.Status, rec.Event, rec.Task)
	}
	return nil
}

func parseHookFlags(args []string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected argument %q", arg)
		}
		key := strings.TrimPrefix(arg, "--")
		if key == "json" || key == "yes" || key == "y" {
			flags[key] = "true"
			continue
		}
		if idx := strings.IndexByte(key, '='); idx >= 0 {
			flags[key[:idx]] = key[idx+1:]
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("missing value for --%s", key)
		}
		flags[key] = args[i+1]
		i++
	}
	return flags, nil
}

func firstFlag(flags map[string]string, keys ...string) string {
	for _, key := range keys {
		if strings.TrimSpace(flags[key]) != "" {
			return strings.TrimSpace(flags[key])
		}
	}
	return ""
}
