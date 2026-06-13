# Plan 001: Ignore root build artifacts

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving on. If a STOP condition occurs, stop and report — do not improvise. When done, update this plan's status row in `plans/README.md` unless your reviewer tells you they maintain the index.
>
> **Drift check (run first)**: `jj diff --from 2602dd26 --to @ -- .gitignore Makefile README.md`
> Expected: no source diff in `.gitignore`, `Makefile`, or `README.md` other than this plan/index work. If any in-scope file changed since this plan was written, compare the excerpts below against live code before proceeding; on mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: dx
- **Planned at**: commit `2602dd26`, 2026-06-13

## Why this matters

The documented `make build` command writes to `bin/glint`, which is ignored, but common bare Go build commands write root binaries named `glint` and `glint-perf`. This checkout already has those root binaries as untracked files, and `jj status` warns that they are too large to snapshot. Ignoring the predictable root binary names removes noise and reduces accidental binary-commit risk.

## Current state

Relevant files:

- `.gitignore` — currently only ignores generated dirs.
- `Makefile` — documents the official build target.
- `README.md` — documents local build/test commands; only read this file unless you decide a tiny doc note is necessary.

Current excerpts:

```gitignore
# .gitignore:1-2
bin/
.tmp/
```

```make
# Makefile:6-8
build:
	go build -o bin/glint ./cmd/glint
```

Observed recon state:

```text
jj status --no-pager
Warning: Refused to snapshot some files:
  glint: ...; the maximum size allowed is 1.0MiB
  glint-perf: ...; the maximum size allowed is 1.0MiB
Untracked paths:
? glint
? glint-perf
```

Repo conventions: keep root config minimal; `.gitignore` currently uses simple path entries, one per line.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Status | `jj status --no-pager` | exits 0; after the change, root `glint`/`glint-perf` are not reported as untracked |
| Inspect | `rg -n "^/?glint" .gitignore` | shows ignore entries for root binary names |
| Baseline note | `go test ./...` | may fail before Plan 002 only at `internal/agent/TestRecordHookStopUpdatesLatest`; do not fix tests in this plan |

## Scope

**In scope**:

- `.gitignore`
- `plans/README.md` status row only

**Out of scope**:

- Removing existing `glint` or `glint-perf` files from the working tree.
- Changing build commands, adding clean targets, or editing README unless the maintainer explicitly asks.
- Fixing the known failing hook-state test; Plan 002 covers that.

## JJ workflow

- Work in the current jj change unless the operator asks for separate changes.
- After verification, describe the jj change with a concise message such as `chore: ignore root build artifacts` using `jj describe -m "chore: ignore root build artifacts"`.
- Do not push or publish anything.

## Steps

### Step 1: Add root binary ignore entries

Edit `.gitignore` to keep the existing entries and add root-only binary names:

```gitignore
bin/
.tmp/
/glint
/glint-perf
```

Use leading slashes so similarly named files in subdirectories are not ignored accidentally.

**Verify**: `rg -n "^/(glint|glint-perf)$" .gitignore` → exactly two matching lines, one for `/glint` and one for `/glint-perf`.

### Step 2: Confirm status noise is gone

Run `jj status --no-pager`.

**Verify**: command exits 0. If root binaries exist locally, they should no longer appear as `? glint` or `? glint-perf`, and jj should not warn about refusing to snapshot them. It is okay if `plans/` files or `.gitignore` are reported as modified/untracked during execution.

### Step 3: Record the jj change description

Run:

```bash
jj describe -m "chore: ignore root build artifacts"
```

**Verify**: `jj log -r @ --no-graph -T 'description.first_line() ++ "\n"'` prints `chore: ignore root build artifacts`.

## Test plan

No Go behavior changes are intended. Run `go test ./...` only as a baseline note: before Plan 002 lands, the expected result is the known single failure in `internal/agent/TestRecordHookStopUpdatesLatest`. Do not change code or tests in this plan.

## Done criteria

- [ ] `.gitignore` contains `/glint` and `/glint-perf`.
- [ ] `jj status --no-pager` no longer reports root `glint`/`glint-perf` as untracked if they exist.
- [ ] No source files outside `.gitignore` were modified.
- [ ] The jj change description is `chore: ignore root build artifacts`.
- [ ] `plans/README.md` status row updated.

## STOP conditions

Stop and report if:

- `.gitignore` has been replaced by a generated or policy-managed file and the excerpt above is gone.
- Removing existing binary files seems necessary to satisfy status; do not remove files in this plan.
- You need to modify build scripts or README to make this work.

## Maintenance notes

If future commands add more root binaries, prefer either adding explicit root-only ignore entries or changing the commands to write under `bin/`; do not broaden ignores to `*` patterns that may hide source files.
