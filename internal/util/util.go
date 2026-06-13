package util

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

func Filter[T any](ss []T, test func(T) bool) (ret []T) {
	for _, s := range ss {
		if test(s) {
			ret = append(ret, s)
		}
	}
	return
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ExpandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func RightAlignLine(left, right string, width int) string {
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		left,
		strings.Repeat(" ", gap),
		right,
	)
}

func UnixTime(value string) time.Time {
	unix, err := strconv.ParseInt(value, 10, 64)
	if err != nil || unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
