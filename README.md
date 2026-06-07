# glint

Glint is a terminal-native workspace switcher and agent sidebar written in Go with Bubble Tea. It scans your local project roots, correlates them with tmux sessions and agent lifecycle state, and gives you a fast keyboard UI for jumping between projects, worktrees, and running agent chats.

This is still a prototype, but the current shape is usable primarily with tmux. Zellij/plain-terminal detection exists; most pane/session actions are tmux-only today.

## Install / build

```bash
# run from source
go run ./cmd/glint

# build a local binary
make build
./bin/glint

# or install to GOPATH/bin
go install ./cmd/glint
```

Useful development commands:

```bash
make run    # go run ./cmd/glint
make test   # go test ./...
make fmt    # gofmt -w .
```

## Commands

```bash
glint                    # full-screen workspace UI
glint sidebar            # same UI, marked as the Glint sidebar pane inside tmux
glint attach             # create a left tmux sidebar split running `glint sidebar`
glint popup [options]    # open the command palette in a tmux popup
glint palette [options]  # run only the command palette
glint hook ...           # record an agent lifecycle event
glint events [limit]     # print recent recorded hook events
glint hooks install pi   # install the Pi extension that emits hook events
glint hooks uninstall pi # remove the Pi extension
glint debug data         # dump scanned config/session/workspace data as JSON
```

Palette options include `--movement`, `--local`, `--global`, `--no-agents`, `--no-actions`, `--no-workspaces`, `--no-create`, `--no-cleanup`, and `--agents-only`.

## tmux usage

Glint works best from inside tmux. Mouse support in tmux is optional, but if you want to use it enable:

```bash
tmux set -g mouse on
```

Start a persistent sidebar:

```bash
glint attach
```

Or manually split a pane:

```bash
tmux split-window -h -l 36 'glint sidebar'
```

## Configuration

Glint reads JSON config from:

```text
~/.config/glint/config.json
```

If the file is missing, defaults are:

```json
{
  "workspace_roots": ["."],
  "theme": "auto",
  "spinner": "points"
}
```

Example:

```json
{
  "workspace_roots": [
    "~/Documents/dev",
    "~/work"
  ],
  "theme": "kanagawa",
  "spinner": "moon"
}
```

Supported themes: `auto`, `dark`, `light`, `dracula`, `catppuccin`/`mocha`, and `kanagawa`/`wave`.

Supported spinners: `points`, `dot`, `minidot`, `line`, `jump`, `pulse`, `meter`, `hamburger`, `ellipsis`, `globe`, `moon`, and `monkey`.

Environment overrides:

```bash
GLINT_SPINNER=moon glint
GLINT_AGENT_COMMAND=claude glint attach  # command used by the "new chat" action; default is pi
GLINT_BIN=/path/to/glint glint hooks install pi
```

## Keyboard shortcuts

Main UI:

- `↑`/`↓` or `j`/`k`: move selection
- `/`: search/filter workspaces and agents
- `Enter`: switch to an existing tmux session or create one for the selected workspace
- `ctrl+p`: command palette
- `ctrl+w`: create/switch VCS worktree or jj workspace
- `ctrl+r`: cleanup/remove a worktree or jj workspace
- `ctrl+x`: delete the matching tmux session/workspace flow for the selection
- `n`: start a new agent chat in the sidebar main pane (`GLINT_AGENT_COMMAND`, default `pi`)
- `b`: shelve the current main pane or selected live agent pane into Glint's tmux shelf
- `c`, `space`, or `tab`: collapse/expand visible agent entries
- `[`/`]` or `h`/`l`: jump between top-level projects
- `s`: cycle spinner style
- `q`, `esc`, or `ctrl+c`: quit

Palette:

- type to filter
- `↑`/`↓` or `j`/`k`: move
- `Enter`: run/open selected target
- `Tab`/`h`/`l`: toggle local/global ordering
- `ctrl+d`/`ctrl+x`: cleanup selected workspace
- `Esc`: close

## Agent hooks

Glint can record reliable agent lifecycle events from shell hooks, plugins, or extensions. Events are written to:

```text
~/.local/state/glint/agents/events.jsonl
~/.local/state/glint/agents/latest.json
```

Manual examples:

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

Supported event/status signals include `session-start`, `prompt-submit`, `stop`, `session-end`, `notification`, `permissionrequest`, `busy`, `idle`, and `error`.

```bash
glint hook claude notification \
  --workspace "$PWD" \
  --session claude-123 \
  --status needs_attention \
  --task "Approve shell command"
```

Install the Pi extension after building/installing `glint`:

```bash
# install glint on PATH, then install the Pi extension
go install ./cmd/glint
glint hooks install pi

# or use an explicit binary path
go build -o ./bin/glint ./cmd/glint
./bin/glint hooks install pi --bin "$PWD/bin/glint"
```

Then restart Pi or run `/reload` inside Pi. The installer writes `glint-session.ts` under Pi's extension directory (`$PI_CODING_AGENT_DIR/extensions` or `~/.pi/agent/extensions`).

Currently only the Pi hook installer exists. Manual `glint hook <agent> <event>` calls can record other agent names.

## What Glint currently detects

- tmux, zellij, or plain terminal environment; tmux is the only fully actionable backend.
- Directories under configured workspace roots.
- Git repositories/worktrees and Jujutsu (`jj`) repos/workspaces, grouped under their parent project.
- tmux sessions by name and current path, including sessions outside configured workspace roots.
- Live tmux panes for known agent programs (Pi, Claude, Codex, Aider, OpenCode, Goose) when they can be matched to hook/history records.
- Pi persisted session history under `~/.pi/agent/sessions`.
- Hook-recorded agent status, merged with tmux pane metadata when available.

Workspaces are sorted with active tmux sessions first, then by recent project/agent activity.

## Debugging

```bash
glint debug data --timing > glint-debug.json
go run ./cmd/glint-perf -n 5
```

Use `--debug` or `-d` before a command to enable internal debug logging where available:

```bash
glint --debug
```

## Current limitations / next work

- zellij support is mostly detection; session/pane switching and creation are not implemented.
- Automatic hook installers only exist for Pi.
- Agent command configuration is currently via `GLINT_AGENT_COMMAND`, not the JSON config file.
- Historical session scanning is focused on Pi; other agents need hooks or live tmux correlation for reliable status.
