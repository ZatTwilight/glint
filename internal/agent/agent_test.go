package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastJSONLTimestampEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty jsonl: %v", err)
	}

	if got := lastJSONLTimestamp(path); !got.IsZero() {
		t.Fatalf("lastJSONLTimestamp returned %s, want zero time", got)
	}
}

func TestLastJSONLTimestampMultipleLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	want := time.Date(2026, 6, 13, 12, 34, 56, 789000000, time.UTC)
	contents := strings.Join([]string{
		`{"timestamp":"2026-06-13T12:00:00Z"}`,
		`{"timestamp":"` + want.Format(time.RFC3339Nano) + `"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	if got := lastJSONLTimestamp(path); !got.Equal(want) {
		t.Fatalf("lastJSONLTimestamp returned %s, want %s", got, want)
	}
}

func TestLastJSONLTimestampLargeFileUsesFinalTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	want := time.Date(2026, 6, 13, 13, 45, 0, 123000000, time.UTC)
	var b strings.Builder
	filler := `{"timestamp":"2026-06-13T12:00:00Z","message":"` + strings.Repeat("x", 200) + `"}` + "\n"
	for b.Len() <= jsonlTailWindow+1024 {
		b.WriteString(filler)
	}
	b.WriteString(`{"timestamp":"` + want.Format(time.RFC3339Nano) + `"}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write large jsonl: %v", err)
	}

	if got := lastJSONLTimestamp(path); !got.Equal(want) {
		t.Fatalf("lastJSONLTimestamp returned %s, want %s", got, want)
	}
}

func TestLastJSONLTimestampMalformedFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.jsonl")
	contents := strings.Join([]string{
		`{"timestamp":"2026-06-13T12:00:00Z"}`,
		`{not-json}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write malformed jsonl: %v", err)
	}

	if got := lastJSONLTimestamp(path); !got.IsZero() {
		t.Fatalf("lastJSONLTimestamp returned %s, want zero time", got)
	}
}
