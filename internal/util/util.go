package util

import (
	"strings"

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
   gap := max(width - lipgloss.Width(left) - lipgloss.Width(right), 0)
   return lipgloss.JoinHorizontal(
      lipgloss.Top,
      left,
      strings.Repeat(" ", gap),
      right,
   )
}

