# Plan 002: Make hook-state tests time-independent

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving on. If a STOP condition occurs, stop and report — do not improvise. When done, update this plan's status row in `plans/README.md` unless your reviewer tells you they maintain the index.
>
> **Drift check (run first)**: `jj diff --from 2602dd26 --to @ -- internal/agent/store.go internal/agent/store_test.go`
> If either in-scope file changed since this plan was written, compare the excerpts below against live code before proceeding; on mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: tests
- **Planned at**: commit `2602dd26`, 2026-06-13

## Why this matters

The repository's main verification command is currently red because one test uses fixed May 2026 timestamps. Production code intentionally filters completed hook records older than 14 days, so the fixture aged out. This plan restores a stable `go test ./...` baseline without changing production behavior.

## Current state

Relevant files:

- `internal/agent/store.go` — production hook-state scan and 14-day retention behavior.
- `internal/agent/store_test.go` — unit tests for hook recording/scanning.

Current excerpts:

```go
// internal/agent/store.go:107
cutoff := time.Now().Add(-14 * 24 * time.Hour)
```

```go
// internal/agent/store.go:126
if rec.Time.Before(cutoff) && rec.Status != Running && rec.Status != WaitingInput && rec.Status != NeedsAttention {
	continue
}
```

```go
// internal/agent/store_test.go:98 and :107
Now:       time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
...
Now:       time.Date(2026, 5, 29, 12, 1, 0, 0, 0, time.UTC),
```

Observed failure:

```text
go test ./...
--- FAIL: TestRecordHookStopUpdatesLatest
    store_test.go:116: expected 1 hook agent, got 0
```

Repo conventions: tests use the standard Go `testing` package and `t.Setenv("GLINT_STATE_DIR", t.TempDir())` to isolate state.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Targeted test | `go test ./internal/agent -run TestRecordHookStopUpdatesLatest -count=1` | exits 0 |
| Package tests | `go test ./internal/agent -count=1` | exits 0 |
| Full suite | `go test ./...` | exits 0 |

## Scope

**In scope**:

- `internal/agent/store_test.go`
- `internal/agent/store.go` only if you choose to add a minimal test seam; prefer test-only changes if possible.
- `plans/README.md` status row only

**Out of scope**:

- Changing the 14-day retention policy.
- Changing hook persistence format or permissions.
- Adding file locks/atomic writes; that is a separate hook-state safety plan.

## JJ workflow

- Keep this as a small jj change.
- After verification, run `jj describe -m "test: make hook retention fixtures relative"`.
- Do not push or publish anything.

## Steps

### Step 1: Replace aged fixed timestamps in the failing test

In `TestRecordHookStopUpdatesLatest`, define a stable relative base time near the start of the test, for example:

```go
now := time.Now().UTC().Truncate(time.Second)
```

Use `now` for the prompt-submit event and `now.Add(time.Minute)` for the stop event. Keep the order and expectations unchanged: the stopped session should still scan as one completed hook agent whose task remains `"Do work"`.

**Verify**: `go test ./internal/agent -run TestRecordHookStopUpdatesLatest -count=1` → exits 0.

### Step 2: Check for any other retention-sensitive completed fixtures

Search the test file for fixed May 2026 timestamps:

```bash
rg -n "time\.Date\(2026, 5, 29" internal/agent/store_test.go
```

If any remaining fixed timestamp feeds a `Completed` hook record that is passed through `ScanHookState`, make it relative too. Do not rewrite unrelated tests just for style.

**Verify**: `go test ./internal/agent -count=1` → exits 0.

### Step 3: Restore the repository baseline

Run the full suite.

**Verify**: `go test ./...` → exits 0 for all packages.

### Step 4: Record the jj change description

Run:

```bash
jj describe -m "test: make hook retention fixtures relative"
```

**Verify**: `jj log -r @ --no-graph -T 'description.first_line() ++ "\n"'` prints `test: make hook retention fixtures relative`.

## Test plan

- Update the existing `TestRecordHookStopUpdatesLatest`; it already covers the regression.
- Do not add a production clock abstraction unless a test-only fix is impossible.
- Verification is the targeted test, the package test, and `go test ./...`.

## Done criteria

- [ ] `go test ./internal/agent -run TestRecordHookStopUpdatesLatest -count=1` exits 0.
- [ ] `go test ./internal/agent -count=1` exits 0.
- [ ] `go test ./...` exits 0.
- [ ] No retention policy behavior changed in `internal/agent/store.go` unless clearly justified.
- [ ] The jj change description is `test: make hook retention fixtures relative`.
- [ ] `plans/README.md` status row updated.

## STOP conditions

Stop and report if:

- The failing test no longer resembles the excerpt above.
- Making the test relative still leaves `go test ./internal/agent` failing for a different production behavior.
- The fix appears to require changing the retention cutoff semantics.

## Maintenance notes

When adding hook-state tests that call `ScanHookState`, avoid absolute dates for non-running statuses unless the test is explicitly about retention boundaries.
