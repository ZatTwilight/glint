package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/kait/agentbar/internal/theme"
)

// itemRenderer is the small rendering layer for each row in the sidebar.
//
// Unlike bubbles/list delegates, this renderer can return any number of lines
// per item. That makes it a good playground for dynamic item heights.
type itemRenderer struct {
	styles itemRendererStyles
}

type itemRendererStyles struct {
	Title         lipgloss.Style
	Description   lipgloss.Style
	SelectedTitle lipgloss.Style
	SelectedDesc  lipgloss.Style
}

func newItemRenderer(t theme.Theme) itemRenderer {
	return itemRenderer{
		styles: itemRendererStyles{
			Title:       lipgloss.NewStyle().Foreground(t.Text).PaddingLeft(2),
			Description: lipgloss.NewStyle().Foreground(t.Muted).PaddingLeft(2),
			SelectedTitle: lipgloss.NewStyle().
				Foreground(t.Accent).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(t.Accent).
				PaddingLeft(1),
			SelectedDesc: lipgloss.NewStyle().
				Foreground(t.Muted).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(t.Accent).
				PaddingLeft(1),
		},
	}
}

func (r itemRenderer) Render(i item, selected bool) string {
	titleTxt := i.title
	descTxt := i.desc

	if i.workspace.GitType != 0 {
		titleTxt = "󰊢 " + titleTxt
	}

	title := r.styles.Title.Render(titleTxt)
	desc := r.styles.Description.Render(descTxt)
	if selected {
		title = r.styles.SelectedTitle.Render(titleTxt)
		desc = r.styles.SelectedDesc.Render(descTxt)
	}

	lines := []string{title, desc}

	// Playground for dynamic heights:
	// Add/remove lines here per item without changing any global Height().
	// For example:
	//
	// if i.workspace.IsWorktree {
	// 	lines = append(lines, r.styles.Description.Render("worktree: "+i.path))
	// }

	return strings.Join(lines, "\n")
}
