# Plan 003: Optimize JSONL last-timestamp reads

> **Executor instructions**: Follow this plan step by step. Run every verification command and confirm the expected result before moving on. If a STOP condition occurs, stop and report — do not improvise. When done, update this plan's status row in `plans/README.md` unless your reviewer tells you they maintain the index.
>
> **Drift check (run first)**: `jj diff --from 2602dd26 --to @ -- internal/agent/agent.go internal/agent/agent_test.go internal/agent/store_test.go`
> If any in-scope file changed since this plan was written, compare the excerpts below against live code before proceeding; on mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: Plan 002
- **Category**: perf
- **Planned at**: commit `2602dd26`, 2026-06-13

## Why this matters

`scanPiHistory` checks up to five Pi session JSONL files per workspace. Today `lastJSONLTimestamp` scans every line of each file just to parse the final timestamp. Long-running agent sessions can produce large JSONL files, so refresh work grows with historical file size even though only the tail is needed.

## Current state

Relevant files:

- `internal/agent/agent.go` — agent history scanning and timestamp parsing.
- `internal/agent/agent_test.go` — create this if no suitable test file exists.

Current excerpts:

```go
// internal/agent/agent.go:287-303
for i := len(entries) - 1; i >= 0 && len(agents) < 5; i-- {
	...
	lastTime := lastJSONLTimestamp(path)
	jsonlTime := historyTimeFromFile(path, info)
	if lastTime.IsZero() {
		lastTime = jsonlTime
	}
```

```go
// internal/agent/agent.go:323-345
func lastJSONLTimestamp(path string) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer file.Close()

	var lastLine string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		lastLine = scanner.Text()
	}
	if lastLine == "" {
		return time.Time{}
	}
	var data struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal([]byte(lastLine), &data) != nil {
		return time.Time{}
	}
	...
}
```

Repo conventions: parsing helpers in `internal/agent/agent.go` return zero values on unreadable/malformed history rather than surfacing errors to the UI.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Agent package tests | `go test ./internal/agent -count=1` | exits 0 |
| Full suite | `go test ./...` | exits 0, assuming Plan 002 is done |

## Scope

**In scope**:

- `internal/agent/agent.go`
- `internal/agent/agent_test.go` (create if needed)
- `plans/README.md` status row only

**Out of scope**:

- Reworking `scanJSONL`, `piTask`, Codex/Claude scanning, or the history merge algorithm.
- Adding sidecar metadata files.
- Changing which history files are selected or how many are displayed.

## JJ workflow

- Start only after Plan 002 is done so `go test ./...` is meaningful.
- After verification, run `jj describe -m "perf: read jsonl timestamps from file tails"`.
- Do not push or publish anything.

## Steps

### Step 1: Add a tail-reading helper

In `internal/agent/agent.go`, replace the full-file scan inside `lastJSONLTimestamp` with a helper that reads only the end of the file for normal large files.

Target behavior:

- Open the file once and `defer file.Close()` as today.
- Use `file.Stat()` to get size.
- For empty files, return `time.Time{}`.
- Read at most a fixed tail window, e.g. `64 * 1024` bytes, from `max(0, size-window)` using `file.Seek`/`io.ReadAll` or `file.ReadAt`.
- Trim trailing `\n`/`\r`; find the last newline in the buffer; parse the bytes after it as the last JSONL record.
- If seek/read fails, fall back to the existing scanner approach so behavior remains conservative.
- Preserve existing timestamp parsing: first `parseTime`, then `time.Parse("2006-01-02T15:04:05.000Z", ...)`.

Keep malformed/missing timestamps returning zero time.

**Verify**: `go test ./internal/agent -run TestLastJSONLTimestamp -count=1` may initially report no tests if Step 2 is not done yet; after Step 2 it must pass.

### Step 2: Add focused tests

Create `internal/agent/agent_test.go` if it does not exist. Add table or focused tests for `lastJSONLTimestamp`:

- Empty file returns zero.
- File with multiple JSONL lines returns the timestamp from the last line, not the first.
- Large file over the tail window still returns the last timestamp. You can write a large padding line or repeated harmless JSON lines before the final record; do not allocate huge memory unnecessarily.
- Malformed final line returns zero or preserves current behavior.

Use `t.TempDir()` and `os.WriteFile`/`os.Create` as in standard Go tests.

**Verify**: `go test ./internal/agent -run TestLastJSONLTimestamp -count=1` → exits 0.

### Step 3: Run package and full tests

Run:

```bash
go test ./internal/agent -count=1
go test ./...
```

**Verify**: both commands exit 0.

### Step 4: Record the jj change description

Run:

```bash
jj describe -m "perf: read jsonl timestamps from file tails"
```

**Verify**: `jj log -r @ --no-graph -T 'description.first_line() ++ "\n"'` prints `perf: read jsonl timestamps from file tails`.

## Test plan

New tests in `internal/agent/agent_test.go` should directly exercise `lastJSONLTimestamp`. Existing package tests cover that history scanning still compiles and interacts with hook state correctly.

## Done criteria

- [ ] `lastJSONLTimestamp` no longer scans the whole file for normal non-empty files.
- [ ] Existing zero-value behavior for unreadable/malformed files is preserved.
- [ ] `go test ./internal/agent -run TestLastJSONLTimestamp -count=1` exits 0.
- [ ] `go test ./internal/agent -count=1` exits 0.
- [ ] `go test ./...` exits 0.
- [ ] The jj change description is `perf: read jsonl timestamps from file tails`.
- [ ] `plans/README.md` status row updated.

## STOP conditions

Stop and report if:

- `lastJSONLTimestamp` has already been rewritten and no longer resembles the excerpt.
- Correct behavior requires changing `scanPiHistory` file selection or history display semantics.
- Tests require real Pi session files outside `t.TempDir()`.

## Maintenance notes

If future history scanners need tail metadata, share the tail-line helper rather than duplicating file seek logic. Keep history parsing best-effort: UI refresh should not fail because one history file is malformed.
