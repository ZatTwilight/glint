# Plan 005: Declare fuzzy as a direct dependency

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving on. If a STOP condition occurs, stop and report — do not improvise. When done, update this plan's status row in `plans/README.md` unless your reviewer tells you they maintain the index.
>
> **Drift check (run first)**: `jj diff --from 2602dd26 --to @ -- go.mod go.sum internal/ui/ui.go`
> If any in-scope file changed since this plan was written, compare the excerpts below against live code before proceeding; on mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: Plan 002
- **Category**: migration
- **Planned at**: commit `2602dd26`, 2026-06-13

## Why this matters

`internal/ui/ui.go` imports `github.com/sahilm/fuzzy` directly, but `go.mod` marks it as indirect. That makes dependency review misleading and can create noisy future `go mod tidy` diffs. The fix is manifest-only: declare the module as a direct dependency without changing runtime behavior.

## Current state

Relevant files:

- `internal/ui/ui.go` — direct import of fuzzy.
- `go.mod` — dependency declaration.
- `go.sum` — should not need manual edits; let Go tooling maintain it if needed.

Current excerpts:

```go
// internal/ui/ui.go:20
"github.com/sahilm/fuzzy"
```

```go
// go.mod:5-10
require (
	charm.land/bubbles/v2 v2.1.0
	charm.land/bubbletea/v2 v2.0.6
	charm.land/lipgloss/v2 v2.0.3
	github.com/shirou/gopsutil/v4 v4.26.4
)
```

```go
// go.mod:30
github.com/sahilm/fuzzy v0.1.1 // indirect
```

Repo conventions: `go.mod` uses a direct `require (...)` block followed by an indirect `require (...)` block.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Dependency check | `go list -m github.com/sahilm/fuzzy` | prints `github.com/sahilm/fuzzy v0.1.1` |
| Tests | `go test ./...` | exits 0, assuming Plan 002 is done |
| Manifest check | `rg -n "github.com/sahilm/fuzzy" go.mod` | one direct require entry without `// indirect` |

## Scope

**In scope**:

- `go.mod`
- `go.sum` only if Go tooling changes it
- `plans/README.md` status row only

**Out of scope**:

- Upgrading `github.com/sahilm/fuzzy`.
- Replacing the fuzzy matching library.
- Changing any UI code.

## JJ workflow

- Start only after Plan 002 is done so full-suite verification is meaningful.
- After verification, run `jj describe -m "chore: mark fuzzy as direct dependency"`.
- Do not push or publish anything.

## Steps

### Step 1: Move fuzzy to the direct require block

Edit `go.mod` so `github.com/sahilm/fuzzy v0.1.1` appears in the first/direct `require` block and is removed from the indirect block. Do not change the version.

Acceptable target shape:

```go
require (
	charm.land/bubbles/v2 v2.1.0
	charm.land/bubbletea/v2 v2.0.6
	charm.land/lipgloss/v2 v2.0.3
	github.com/sahilm/fuzzy v0.1.1
	github.com/shirou/gopsutil/v4 v4.26.4
)
```

**Verify**: `rg -n "github.com/sahilm/fuzzy" go.mod` → one line, and that line does not contain `// indirect`.

### Step 2: Let Go validate the manifest

Run:

```bash
go list -m github.com/sahilm/fuzzy
```

**Verify**: prints `github.com/sahilm/fuzzy v0.1.1`.

Do not run broad upgrades. If you run `go mod tidy`, inspect the diff carefully and keep only changes directly caused by this manifest cleanup.

### Step 3: Run full tests

Run:

```bash
go test ./...
```

**Verify**: exits 0.

### Step 4: Record the jj change description

Run:

```bash
jj describe -m "chore: mark fuzzy as direct dependency"
```

**Verify**: `jj log -r @ --no-graph -T 'description.first_line() ++ "\n"'` prints `chore: mark fuzzy as direct dependency`.

## Test plan

This is a manifest-only cleanup. `go list -m` verifies the dependency remains resolved, and `go test ./...` verifies no code behavior changed.

## Done criteria

- [ ] `go.mod` has exactly one `github.com/sahilm/fuzzy v0.1.1` entry.
- [ ] That entry is not marked `// indirect`.
- [ ] `go list -m github.com/sahilm/fuzzy` prints `github.com/sahilm/fuzzy v0.1.1`.
- [ ] `go test ./...` exits 0.
- [ ] No UI/source code files were changed.
- [ ] The jj change description is `chore: mark fuzzy as direct dependency`.
- [ ] `plans/README.md` status row updated.

## STOP conditions

Stop and report if:

- `internal/ui/ui.go` no longer imports `github.com/sahilm/fuzzy`.
- Go tooling attempts to upgrade or remove unrelated dependencies.
- Full tests fail for reasons other than a missing prerequisite Plan 002.

## Maintenance notes

When adding imports, keep directly imported modules in the direct require block. Use the indirect block only for transitive dependencies.
