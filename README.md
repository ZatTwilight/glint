# glint

A tiny Go/Bubble Tea prototype for a terminal-native workspace + agent sidebar.

## Run

```bash
go run ./cmd/glint
```

## In tmux

Mouse support needs tmux mouse mode enabled:

```bash
tmux set -g mouse on
```

Then run Glint as a sidebar:

```bash
tmux split-window -h -l 36 'cd ~/Documents/dev/glint && go run ./cmd/glint'
```

## Config

Glint reads workspace roots from:

```text
~/.config/glint/config.json
```

Example:

```json
{
  "workspace_roots": [
    "~/Documents/dev",
    "~/work"
  ],
  "theme": "auto",
  "spinner": "points"
}
```

If the file is missing, Glint defaults to `~/Documents/dev`.

Themes:

- `auto` - infer light/dark from the terminal when possible
- `dark`
- `light`
- `dracula`
- `catppuccin`
- `kanagawa`

Spinners:

- Set `spinner` to `points`, `dot`, `minidot`, `line`, `jump`, `pulse`, `meter`, `hamburger`, `ellipsis`, `globe`, `moon`, or `monkey`.
- Or override temporarily with `GLINT_SPINNER=moon glint`.
- Press `s` in the UI to cycle spinners while testing.

## Agent hooks

Glint can record reliable agent lifecycle events from shell hooks, plugins, or extensions:

```bash
# Mark an agent turn as running. JSON on stdin is optional but useful.
printf '{"session_id":"abc","cwd":"%s","prompt":"Refactor auth"}' "$PWD" \
  | glint hook pi prompt-submit

# Mark that same turn as complete.
printf '{"session_id":"abc","cwd":"%s","last_assistant_message":"Done"}' "$PWD" \
  | glint hook pi stop

# Inspect recent recorded lifecycle events.
glint events 20
```

Install the Pi extension after building/installing `glint`:

```bash
# Option A: install glint on PATH, then install the Pi extension
go install ./cmd/glint
glint hooks install pi

# Option B: use an explicit binary path
go build -o ./bin/glint ./cmd/glint
./bin/glint hooks install pi --bin "$PWD/bin/glint"
```

Then restart Pi or run `/reload` inside Pi.

Events are written to:

```text
~/.local/state/glint/agents/events.jsonl
~/.local/state/glint/agents/latest.json
```

Supported status events include `session-start`, `prompt-submit`, `stop`, `session-end`, `notification`, `permissionrequest`, `busy`, `idle`, and `error`. Pass explicit values when needed:

```bash
glint hook claude notification --workspace "$PWD" --session claude-123 --status needs_attention --task "Approve shell command"
```

Hook status is merged with tmux pane detection and wins over activity-based guesses.

## Current prototype

- Detects tmux / zellij / plain terminal
- Lists directories under configured workspace roots
- Sorts active tmux sessions first, then newest directories first
- Shows Git worktrees and Jujutsu (`jj`) workspaces as children of their parent project
- Lists tmux sessions when running inside tmux
- Switches to selected tmux sessions with `Enter`
- Creates missing tmux sessions for selected projects, then switches to them
- Shows tmux sessions outside configured workspace roots in the same general list
- Shows session status: attached/detached, window count, activity age, and path basename
- Detects live per-workspace agent panes in tmux for Pi, Claude, Codex, Aider, OpenCode, and Goose
- Detects historical Pi, Codex, and Claude sessions from local JSONL transcript stores
- Shows agent status with `●` running, `◌` idle, and `…` thinking based on pane activity or hook events
- Expands/collapses workspace agent entries with `c`, `space`, or `tab`
- Jumps between top-level projects with `[` and `]`
- Refreshes every 2 seconds so closed/opened sessions update live
- Provides a filterable terminal UI

## Next ideas

- Open selected workspace in a tmux/zellij pane/session
- Add `glint hooks install <agent>` helpers for Pi, Claude, Codex, and OpenCode
- Launch configured agent commands like Claude, Codex, OpenCode, Pi
- Add config file for agent commands and launchers
- Add zellij adapter

