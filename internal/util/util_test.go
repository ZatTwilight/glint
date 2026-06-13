package util

import (
	"path/filepath"
	"testing"
)

func TestFirstNonEmpty(t *testing.T) {
	got := FirstNonEmpty("", "   ", "  value  ", "next")
	if got != "value" {
		t.Fatalf("FirstNonEmpty returned %q, want %q", got, "value")
	}
}

func TestFirstNonEmptyAllEmpty(t *testing.T) {
	if got := FirstNonEmpty("", " \t "); got != "" {
		t.Fatalf("FirstNonEmpty returned %q, want empty string", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := ExpandHome("~"); got != home {
		t.Fatalf("ExpandHome(~) returned %q, want %q", got, home)
	}
	want := filepath.Join(home, "projects", "glint")
	if got := ExpandHome("~/projects/glint"); got != want {
		t.Fatalf("ExpandHome(~/projects/glint) returned %q, want %q", got, want)
	}
	unchanged := "/tmp/~"
	if got := ExpandHome(unchanged); got != unchanged {
		t.Fatalf("ExpandHome(%q) returned %q, want unchanged", unchanged, got)
	}
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{n: 0, want: "s"},
		{n: 1, want: ""},
		{n: 2, want: "s"},
	} {
		if got := Plural(tc.n); got != tc.want {
			t.Fatalf("Plural(%d) returned %q, want %q", tc.n, got, tc.want)
		}
	}
}
