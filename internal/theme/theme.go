package theme

import (
	"image/color"
	"os"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

type Theme struct {
	Name       string
	Dark       bool
	Accent     color.Color
	Text       color.Color
	Muted      color.Color
	Subtle     color.Color
	Background color.Color
	BadgeText  color.Color
}

type Styles struct {
	Help   lipgloss.Style
	Header lipgloss.Style
	Muted  lipgloss.Style
	Badge  lipgloss.Style
	Body   lipgloss.Style
}

func Resolve(name string) Theme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "light":
		return Theme{Name: "light", Dark: false, Accent: lipgloss.Color("#6C5CE7"), Text: lipgloss.Color("#1A1A1A"), Muted: lipgloss.Color("#6B7280"), Subtle: lipgloss.Color("#D1D5DB"), Background: lipgloss.Color("#FFFFFF"), BadgeText: lipgloss.Color("#FFFFFF")}
	case "dracula":
		return Theme{Name: "dracula", Dark: true, Accent: lipgloss.Color("#BD93F9"), Text: lipgloss.Color("#F8F8F2"), Muted: lipgloss.Color("#6272A4"), Subtle: lipgloss.Color("#44475A"), Background: lipgloss.Color("#282A36"), BadgeText: lipgloss.Color("#282A36")}
	case "catppuccin", "mocha":
		return Theme{Name: "catppuccin", Dark: true, Accent: lipgloss.Color("#CBA6F7"), Text: lipgloss.Color("#CDD6F4"), Muted: lipgloss.Color("#7F849C"), Subtle: lipgloss.Color("#45475A"), Background: lipgloss.Color("#1E1E2E"), BadgeText: lipgloss.Color("#1E1E2E")}
	case "kanagawa", "wave":
		return Theme{Name: "kanagawa", Dark: true, Accent: lipgloss.Color("#957FB8"), Text: lipgloss.Color("#DCD7BA"), Muted: lipgloss.Color("#727169"), Subtle: lipgloss.Color("#363646"), Background: lipgloss.Color("#1F1F28"), BadgeText: lipgloss.Color("#1F1F28")}
	case "dark":
		return dark()
	default:
		if inferLightTerminal() {
			return Resolve("light")
		}
		return dark()
	}
}

func NewStyles(t Theme) Styles {
	return Styles{
		Help:   lipgloss.NewStyle().Foreground(t.Muted).MarginTop(1).Padding(0, 1),
		Header: lipgloss.NewStyle().MarginBottom(1).Padding(0, 1),
		Muted:  lipgloss.NewStyle().Foreground(t.Muted),
		Badge:  lipgloss.NewStyle().Foreground(t.BadgeText).Background(t.Accent).Padding(0, 1),
		Body:   lipgloss.NewStyle().Padding(1, 0),
	}
}

func ApplyListTheme(delegate *list.DefaultDelegate, t Theme) {
	delegate.Styles.NormalTitle = lipgloss.NewStyle().Foreground(t.Text).Padding(0, 0, 0, 2)
	delegate.Styles.NormalDesc = delegate.Styles.NormalTitle.Foreground(t.Muted)
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(t.Accent).
		Foreground(t.Accent).
		Padding(0, 0, 0, 1)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedTitle.Foreground(t.Muted)
	delegate.Styles.DimmedTitle = lipgloss.NewStyle().Foreground(t.Muted).Padding(0, 0, 0, 2)
	delegate.Styles.DimmedDesc = delegate.Styles.DimmedTitle.Foreground(t.Subtle)
	delegate.Styles.FilterMatch = lipgloss.NewStyle().Foreground(t.Accent).Underline(true)
}

func dark() Theme {
	return Theme{Name: "dark", Dark: true, Accent: lipgloss.Color("#A78BFA"), Text: lipgloss.Color("#E5E7EB"), Muted: lipgloss.Color("#6B7280"), Subtle: lipgloss.Color("#374151"), Background: lipgloss.Color("#111827"), BadgeText: lipgloss.Color("#111827")}
}

func inferLightTerminal() bool {
	// Many terminals expose COLORFGBG as "foreground;background" using ANSI color indexes.
	// Background indexes 0-6 are usually dark; 7 and 15 are usually light.
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if len(parts) == 0 {
		return false
	}
	bg := parts[len(parts)-1]
	return bg == "7" || bg == "15"
}
