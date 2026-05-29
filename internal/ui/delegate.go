package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/theme"
	"github.com/ZatTwilight/glint/internal/util"
	"github.com/ZatTwilight/glint/internal/workspace"
)

// itemRenderer is the small rendering layer for each row in the sidebar.
//
// Unlike bubbles/list delegates, this renderer can return any number of lines
// per item. That makes it a good playground for dynamic item heights.
type itemRenderer struct {
	styles    itemRendererStyles
	collapsed map[string]bool
}

type itemRendererStyles struct {
	Title         lipgloss.Style
	Description   lipgloss.Style
	SelectedTitle lipgloss.Style
	SelectedDesc  lipgloss.Style
}

func newItemRenderer(t theme.Theme) itemRenderer {
	return itemRenderer{
		collapsed: map[string]bool{},
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

func (r itemRenderer) IsCollapsed(path string) bool {
	return r.collapsed[path]
}

func (r itemRenderer) RenderVisible(item visibleItem, selected bool, width int) string {
	if item.Kind == kindAgent && item.AgentIndex >= 0 && item.AgentIndex < len(item.Workspace.Agents) {
		ag := item.Workspace.Agents[item.AgentIndex]
		rowWidth := width - r.styles.Title.GetHorizontalFrameSize()
		left := fmt.Sprintf("  %s %s %s %s", agent.Symbol(ag.Status), agent.Icon(ag.Name), agent.DisplayStatus(ag.Status), quoteTask(ag.Task))
		right := relativeTime(ag.Activity)
		if ag.History {
			right = right + " history"
		}
		line := util.RightAlignLine(left, right, rowWidth)
		if ag.Current {
			line = line + " *"
		}
		if selected {
			return r.styles.SelectedTitle.Render(line)
		}
		return r.styles.Title.Render(line)
	}
	return r.Render(item.Workspace, selected, width)
}

func (r itemRenderer) Render(i workspace.Workspace, selected bool, width int) string {
	descWidth := width - r.styles.Description.GetHorizontalFrameSize()
	titleTxt := i.Name
	if len(i.Agents) > 0 {
		marker := "▾"
		if r.collapsed[i.Path] {
			marker = "▸"
		}
		titleTxt = fmt.Sprintf("%s %s", marker, titleTxt)
	}

	pathParts := strings.Split(i.Path, "/")
	shortPath := strings.Join(pathParts[len(pathParts)-2:], "/")
	leftDesc := shortPath
	if len(i.Agents) > 0 {
		leftDesc = fmt.Sprintf("%s · %d agent%s", shortPath, len(i.Agents), plural(len(i.Agents)))
	}
	descTxt := util.RightAlignLine(leftDesc, relativeTime(i.ModifiedAt), descWidth)

	// if i.GitType != 0 {
	// 	titleTxt = "󰊢  " + titleTxt
	// }
	// if i.ActiveInTmux {
	// 	titleTxt = "  " + i.Name
	// }

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
	// if i.IsWorktree {
	// 	lines = append(lines, r.styles.Description.Render("worktree: "+i.Path))
	// }

	return strings.Join(lines, "\n")
}

func quoteTask(task string) string {
	const maxLen = 28
	task = strings.TrimSpace(task)
	if task == "" {
		task = "agent session"
	}
	if len(task) > maxLen {
		task = task[:maxLen-1] + "…"
	}
	return "“" + task + "”"
}
