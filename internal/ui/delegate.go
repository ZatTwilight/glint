package ui

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kait/agentbar/internal/theme"
)

// itemDelegate is the small rendering layer for each row in the sidebar.
//
// The list.Model owns selection, scrolling, filtering, and mouse/keyboard
// behavior. The delegate only answers:
//   - how tall is a row?
//   - how much spacing is between rows?
//   - how should a given item render?
//
// This is intentionally simple so it's easy to play with.
type itemDelegate struct {
	styles itemDelegateStyles
}

type itemDelegateStyles struct {
	Title         lipgloss.Style
	Description   lipgloss.Style
	SelectedTitle lipgloss.Style
	SelectedDesc  lipgloss.Style
	Header        lipgloss.Style
}

func newItemDelegate(t theme.Theme) itemDelegate {
	return itemDelegate{
		styles: itemDelegateStyles{
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
			Header: lipgloss.NewStyle().Foreground(t.Accent).Bold(true).PaddingLeft(1),
		},
	}
}

func (d itemDelegate) Height() int {
	return 2
}

func (d itemDelegate) Spacing() int {
	return 1
}

func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return nil
}

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	titleTxt := i.title
	descTxt := i.desc

	if i.workspace.GitType != 0 {
		titleTxt = "󰊢 " + titleTxt
	}

	title := d.styles.Title.Render(titleTxt)
	desc := d.styles.Description.Render(descTxt)
	if index == m.Index() {
		title = d.styles.SelectedTitle.Render(titleTxt)
		desc = d.styles.SelectedDesc.Render(descTxt)
	}

	fmt.Fprintf(w, "%s\n%s", title, desc)
}
