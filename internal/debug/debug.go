package debug

import (
	"fmt"
	"os"
	"strings"
)

// Enabled controls debug logging across glint. It defaults from GLINT_DEBUG.
var Enabled = truthy(os.Getenv("GLINT_DEBUG"))

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

// Set updates the global debug flag.
func Set(enabled bool) {
	Enabled = enabled
}

// Printf writes a debug log line to stderr when Enabled is true.
func Printf(format string, args ...any) {
	if !Enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "[debug] "+format, args...)
}

// Println writes a debug log line to stderr when Enabled is true.
func Println(args ...any) {
	if !Enabled {
		return
	}
	fmt.Fprint(os.Stderr, "[debug] ")
	fmt.Fprintln(os.Stderr, args...)
}
