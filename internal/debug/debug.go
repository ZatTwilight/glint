package debug

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Enabled controls debug logging across glint. It defaults from GLINT_DEBUG.
var Enabled = truthy(os.Getenv("GLINT_DEBUG"))

var (
	mu     sync.Mutex
	writer io.Writer
	file   *os.File
)

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
	mu.Lock()
	defer mu.Unlock()
	Enabled = enabled
	if !Enabled {
		return
	}
	ensureWriterLocked()
}

// Path returns the file path used for debug logs.
func Path() string {
	if path := strings.TrimSpace(os.Getenv("GLINT_DEBUG_LOG")); path != "" {
		return path
	}
	base := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if base == "" {
		if cacheDir, err := os.UserCacheDir(); err == nil {
			base = cacheDir
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "glint", "debug.log")
}

func ensureWriterLocked() io.Writer {
	if writer != nil {
		return writer
	}
	path := Path()
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				file = f
				writer = f
				return writer
			}
		}
	}
	// Last-resort fallback for environments without a writable runtime/cache dir.
	writer = os.Stderr
	return writer
}

func logf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if !Enabled {
		return
	}
	w := ensureWriterLocked()
	timestamp := time.Now().Format(time.RFC3339Nano)
	fmt.Fprintf(w, "[%s] ", timestamp)
	fmt.Fprintf(w, format, args...)
}

// Printf writes a debug log line when Enabled is true.
func Printf(format string, args ...any) {
	logf(format, args...)
}

// Println writes a debug log line when Enabled is true.
func Println(args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if !Enabled {
		return
	}
	w := ensureWriterLocked()
	timestamp := time.Now().Format(time.RFC3339Nano)
	fmt.Fprintf(w, "[%s] ", timestamp)
	fmt.Fprintln(w, args...)
}
