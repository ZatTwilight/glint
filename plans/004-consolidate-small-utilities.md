# Plan 004: Consolidate small duplicated utilities

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving on. If a STOP condition occurs, stop and report — do not improvise. When done, update this plan's status row in `plans/README.md` unless your reviewer tells you they maintain the index.
>
> **Drift check (run first)**: `jj diff --from 2602dd26 --to @ -- cmd/glint/main.go internal/agent/store.go internal/ui/ui.go internal/ui/delegate.go internal/ui/sidebar_state.go internal/util/util.go internal/util/util_test.go`
> If any in-scope file changed since this plan was written, compare the excerpts below against live code before proceeding; on mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: Plan 002
- **Category**: tech-debt
- **Planned at**: commit `2602dd26`, 2026-06-13

## Why this matters

The repo has several tiny helper functions copied across packages. They are simple today, but copy/paste helpers invite drift and make future bug fixes harder to apply consistently. `internal/util` already exists for shared helpers, so this is a small mechanical cleanup.

## Current state

Relevant files:

- `internal/util/util.go` — existing shared utility package.
- `cmd/glint/main.go` — has a local `firstNonEmpty` helper used for config/env fallback.
- `internal/agent/store.go` — has local `firstNonEmpty` and `expandHome` helpers.
- `internal/ui/ui.go` and `internal/ui/delegate.go` — duplicate `plural` logic; `ui.go` already imports `internal/util`.
- `internal/ui/sidebar_state.go` — has another `expandHome` copy.

Current excerpts:

```go
// internal/util/util.go:1-28
package util
...
func UnixTime(value string) time.Time { ... }
```

```go
// cmd/glint/main.go:122-129
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
```

```go
// internal/agent/store.go:385-392
func firstNonEmpty(values ...string) string { ... }
```

```go
// internal/agent/store.go:405-416
func expandHome(path string) string { ... }
```

```go
// internal/ui/delegate.go:248-253 and internal/ui/ui.go:2390-2395
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
```

Repo conventions: package-level helpers use clear exported names only when used across packages. Tests use standard `testing` with table cases.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Utility tests | `go test ./internal/util -count=1` | exits 0 |
| Full suite | `go test ./...` | exits 0, assuming Plan 002 is done |
| Duplication check | `rg -n "func (firstNonEmpty|expandHome|plural)\(" cmd internal` | no duplicate local helper definitions remain except any intentionally private helpers you did not touch |

## Scope

**In scope**:

- `internal/util/util.go`
- `internal/util/util_test.go` (create)
- `cmd/glint/main.go`
- `internal/agent/store.go`
- `internal/ui/sidebar_state.go`
- `internal/ui/ui.go`
- `internal/ui/delegate.go`
- `plans/README.md` status row only

**Out of scope**:

- Large refactors of `internal/ui/ui.go`.
- Changing helper behavior.
- Moving unrelated helpers such as fuzzy matching, time formatting, or VCS functions.

## JJ workflow

- Start only after Plan 002 is done so full-suite verification is meaningful.
- After verification, run `jj describe -m "refactor: consolidate small utility helpers"`.
- Do not push or publish anything.

## Steps

### Step 1: Add shared helpers to `internal/util`

Add exported helpers with behavior matching the existing copies:

- `FirstNonEmpty(values ...string) string` — trims each value and returns the first non-empty trimmed string.
- `ExpandHome(path string) string` — expands `~` and `~/...` using `os.UserHomeDir()`; returns the original path if home lookup fails or no expansion applies.
- `Plural(n int) string` — returns `""` for `1`, otherwise `"s"`.

Add imports to `internal/util/util.go` as needed (`os`, `path/filepath` are likely needed for `ExpandHome`).

**Verify**: `go test ./internal/util -count=1` may initially show no tests until Step 2; after Step 2 it must pass.

### Step 2: Add utility tests

Create `internal/util/util_test.go` with table-driven tests for:

- `FirstNonEmpty("", "  alpha  ") == "alpha"` and all-empty returns `""`.
- `Plural(1) == ""`, `Plural(0) == "s"`, `Plural(2) == "s"`.
- `ExpandHome` expands `~` and `~/child` to paths under the current user's home when `os.UserHomeDir()` is available; for non-tilde paths, returns the input unchanged.

**Verify**: `go test ./internal/util -count=1` → exits 0.

### Step 3: Replace duplicate call sites

Update call sites to use the shared helpers:

- In `cmd/glint/main.go`, import `github.com/ZatTwilight/glint/internal/util`, replace `firstNonEmpty(...)` with `util.FirstNonEmpty(...)`, then delete the local helper.
- In `internal/agent/store.go`, import `github.com/ZatTwilight/glint/internal/util`, replace local `firstNonEmpty` and `expandHome` calls with `util.FirstNonEmpty` and `util.ExpandHome`, then delete both local helpers.
- In `internal/ui/sidebar_state.go`, replace local `expandHome` with `util.ExpandHome`; add the util import if missing; delete the local helper.
- In `internal/ui/ui.go` and `internal/ui/delegate.go`, replace `plural(...)` calls with `util.Plural(...)`; delete duplicate local `plural` definitions.

Use `gofmt` on modified Go files.

**Verify**: `rg -n "func (firstNonEmpty|expandHome|plural)\(" cmd internal` → no local definitions remain for these three helpers.

### Step 4: Run full tests

Run:

```bash
go test ./...
```

**Verify**: exits 0.

### Step 5: Record the jj change description

Run:

```bash
jj describe -m "refactor: consolidate small utility helpers"
```

**Verify**: `jj log -r @ --no-graph -T 'description.first_line() ++ "\n"'` prints `refactor: consolidate small utility helpers`.

## Test plan

- New `internal/util` unit tests cover the moved helper behavior.
- Existing tests ensure callers still compile and behave.

## Done criteria

- [ ] Shared helpers exist in `internal/util` and are covered by tests.
- [ ] Duplicate local definitions of `firstNonEmpty`, `expandHome`, and `plural` are removed.
- [ ] `go test ./internal/util -count=1` exits 0.
- [ ] `go test ./...` exits 0.
- [ ] `gofmt` has been run on modified Go files.
- [ ] The jj change description is `refactor: consolidate small utility helpers`.
- [ ] `plans/README.md` status row updated.

## STOP conditions

Stop and report if:

- Moving a helper creates an import cycle.
- Any helper's behavior must change to make tests pass.
- The cleanup expands beyond these three helper families.

## Maintenance notes

Keep `internal/util` small. Do not use this as precedent for dumping domain-specific UI, VCS, or agent behavior into util; only generic helpers belong there.
