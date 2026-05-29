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
  "theme": "auto"
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

## Current prototype

- Detects tmux / zellij / plain terminal
- Lists directories under configured workspace roots
- Sorts active tmux sessions first, then newest directories first
- Shows git worktrees as children of their parent project
- Lists tmux sessions when running inside tmux
- Switches to selected tmux sessions with `Enter`
- Creates missing tmux sessions for selected projects, then switches to them
- Shows tmux sessions outside configured workspace roots in the same general list
- Shows session status: attached/detached, window count, activity age, and path basename
- Refreshes every 2 seconds so closed/opened sessions update live
- Provides a filterable terminal UI

## Next ideas

- Open selected workspace in a tmux/zellij pane/session
- Track configured agent commands like Claude, Codex, OpenCode, Pi
- Add config file for workspace roots and agent launchers
- Add zellij adapter
