package util

import (
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
