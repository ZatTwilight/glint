package ui

import (
	"fmt"
	"strings"
	"time"

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
	ActiveTitle   lipgloss.Style
}

func newItemRenderer(t theme.Theme, collapsed map[string]bool) itemRenderer {
	if collapsed == nil {
		collapsed = map[string]bool{}
	}
	return itemRenderer{
		collapsed: collapsed,
		styles: itemRendererStyles{
			Title:       lipgloss.NewStyle().Foreground(t.Text).PaddingLeft(2),
			Description: lipgloss.NewStyle().Foreground(t.Muted).PaddingLeft(2),
			SelectedTitle: lipgloss.NewStyle().
				Foreground(t.Accent).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(t.Accent).
				PaddingLeft(1),
			ActiveTitle: lipgloss.NewStyle().
				Foreground(t.Text).
				Bold(true).
				PaddingLeft(2),
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

func (r itemRenderer) RenderVisible(item visibleItem, selected bool, width int, currentWindow string) string {
	if item.Kind == kindAgent && item.AgentIndex >= 0 && item.AgentIndex < len(item.Workspace.Agents) {
		ag := item.Workspace.Agents[item.AgentIndex]
		rowWidth := width - r.styles.Title.GetHorizontalFrameSize()
		icon := "  "
		if ag.PID > 0 {
			icon = " "
		}
		left := fmt.Sprintf("%s%s %s", icon, agent.Icon(ag.Name), quoteTask(ag.Task))
		right := agentTimeStatus(ag)
		line := util.RightAlignLine(left, right, rowWidth)
		if selected {
			return r.styles.SelectedTitle.Render(line)
		}
		if ag.Window == currentWindow {
			return r.styles.ActiveTitle.Render(line)
		}
		return r.styles.Title.Render(line)
	}
	return r.Render(item.Workspace, selected, width)
}

func (r itemRenderer) Render(i workspace.Workspace, selected bool, width int) string {
	descWidth := width - r.styles.Description.GetHorizontalFrameSize()
	titleTxt := workspaceTitle(i, len(i.Agents) > 0, r.collapsed[i.Path])
	icon := ""
	switch i.VCS {
	case workspace.VCSJujutsu:
		icon = "󱗆 "
	case workspace.VCSGit:
		icon = "󰊢 "
	}
	titleTxt = util.RightAlignLine(titleTxt, r.styles.Description.Render(icon + " " + relativeTime(workspaceActivityTime(i))), descWidth)

	pathParts := strings.Split(i.Path, "/")
	shortPath := strings.Join(pathParts[len(pathParts)-2:], "/")
	leftParts := []string{shortPath}
	if len(i.Agents) > 0 {
		leftParts = append(leftParts, fmt.Sprintf("%d agent%s", len(i.Agents), plural(len(i.Agents))))
	}
	descTxt := strings.Join(leftParts, " · ")

	title := r.styles.Title.Render(titleTxt)
	desc := r.styles.Description.Render(descTxt)
	if selected {
		title = r.styles.SelectedTitle.Render(titleTxt)
		desc = r.styles.SelectedDesc.Render(descTxt)
	}

	lines := []string{title, desc}

	return strings.Join(lines, "\n")
}

func workspaceTitle(ws workspace.Workspace, hasAgents bool, collapsed bool) string {
	marker := " "
	if hasAgents {
		marker = "▾"
		if collapsed {
			marker = "▸"
		}
	}
	if ws.IsWorktree {
		return fmt.Sprintf("%s %s/%s", marker, ws.ParentName, ws.Name)
	}
	return fmt.Sprintf("%s %s", marker, ws.Name)
}

func workspaceActivityTime(ws workspace.Workspace) time.Time {
	latest := ws.ModifiedAt
	for _, ag := range ws.Agents {
		if ag.Activity.After(latest) {
			latest = ag.Activity
		}
	}
	return latest
}

func agentTimeStatus(ag agent.Agent) string {
	rel := relativeTime(ag.Activity)
	switch agent.DisplayStatus(ag.Status) {
	case "working":
		return strings.TrimSpace(spinnerFrame() + " " + rel)
	case "done":
		if rel == "now" {
			return "✓ now"
		}
	}
	return rel
}

func spinnerFrame() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[(time.Now().UnixMilli()/120)%int64(len(frames))]
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
	return task
}
