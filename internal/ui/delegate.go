package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
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
	styles       itemRendererStyles
	collapsed    map[string]bool
	spinnerIndex int
}

type itemRendererStyles struct {
	Title         lipgloss.Style
	Description   lipgloss.Style
	SelectedTitle lipgloss.Style
	SelectedDesc  lipgloss.Style
	ActiveTitle   lipgloss.Style
}

type spinnerChoice struct {
	name    string
	spinner spinner.Spinner
}

var spinnerChoices = []spinnerChoice{
	{name: "points", spinner: spinner.Points},
	{name: "dot", spinner: spinner.Dot},
	{name: "minidot", spinner: spinner.MiniDot},
	{name: "line", spinner: spinner.Line},
	{name: "jump", spinner: spinner.Jump},
	{name: "pulse", spinner: spinner.Pulse},
	{name: "meter", spinner: spinner.Meter},
	{name: "hamburger", spinner: spinner.Hamburger},
	{name: "ellipsis", spinner: spinner.Ellipsis},
	{name: "globe", spinner: spinner.Globe},
	{name: "moon", spinner: spinner.Moon},
	{name: "monkey", spinner: spinner.Monkey},
}

func spinnerIndex(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for idx, choice := range spinnerChoices {
		if choice.name == name {
			return idx
		}
	}
	return 0
}

func newItemRenderer(t theme.Theme, collapsed map[string]bool, spinnerName string) itemRenderer {
	if collapsed == nil {
		collapsed = map[string]bool{}
	}
	return itemRenderer{
		collapsed:    collapsed,
		spinnerIndex: spinnerIndex(spinnerName),
		styles: itemRendererStyles{
			Title:       lipgloss.NewStyle().Foreground(t.Text).PaddingLeft(2),
			Description: lipgloss.NewStyle().Foreground(t.Muted).PaddingLeft(4),
			SelectedTitle: lipgloss.NewStyle().
				Foreground(t.Accent).
				Background(t.Selection).
				PaddingLeft(2),
			ActiveTitle: lipgloss.NewStyle().
				Foreground(t.Text).
				Bold(true).
				PaddingLeft(2),
			SelectedDesc: lipgloss.NewStyle().
				Foreground(t.Muted).
				Background(t.Selection).
				PaddingLeft(4),
		},
	}
}

func (r itemRenderer) IsCollapsed(path string) bool {
	return r.collapsed[path]
}

func (r itemRenderer) SpinnerName() string {
	return spinnerChoices[r.spinnerIndex].name
}

func (r *itemRenderer) CycleSpinner() {
	r.spinnerIndex = (r.spinnerIndex + 1) % len(spinnerChoices)
}

func (r itemRenderer) RenderVisible(item visibleItem, selected bool, width int, currentWindow string) string {
	if item.Kind == kindAgent && item.AgentIndex >= 0 && item.AgentIndex < len(item.Workspace.Agents) {
		ag := item.Workspace.Agents[item.AgentIndex]
		rowWidth := width - r.styles.Title.GetHorizontalFrameSize()
		icon := "  "
		if ag.PID > 0 {
			icon = " "
		}
		if item.AgentIndex == item.AgentStart && item.AgentStart > 0 {
			icon = "↑ "
		}
		if item.AgentIndex == item.AgentEnd-1 && item.AgentEnd < len(item.Workspace.Agents) {
			icon = "↓ "
		}
		left := fmt.Sprintf("%s%s %s", icon, agent.Icon(ag.Name), quoteTask(ag.Task))
		right := r.agentTimeStatus(ag)
		line := util.RightAlignLine(left, right, rowWidth)
		if selected {
			return r.styles.SelectedTitle.Width(width).Render(line)
		}
		if ag.Window == currentWindow {
			return r.styles.ActiveTitle.Render(line)
		}
		return r.styles.Title.Render(line)
	}
	return r.Render(item.Workspace, selected, width)
}

func (r itemRenderer) Render(i workspace.Workspace, selected bool, width int) string {
	titleStyle := r.styles.Title
	descStyle := r.styles.Description
	if selected {
		titleStyle = r.styles.SelectedTitle
		descStyle = r.styles.SelectedDesc
	}
	if selected {
		titleStyle = titleStyle.Width(width)
		descStyle = descStyle.Width(width)
	}
	titleWidth := width - titleStyle.GetHorizontalFrameSize()
	titleTxt := workspaceTitle(i, len(i.Agents) > 0, r.collapsed[i.Path])
	icon := ""
	switch i.VCS {
	case workspace.VCSJujutsu:
		icon = "󱗆"
	case workspace.VCSGit:
		icon = "󰊢"
	}
	right := strings.TrimSpace(strings.Join([]string{icon, relativeTime(workspaceActivityTime(i))}, " "))
	rightText := right
	if !selected {
		rightText = r.styles.Description.Render(right)
	}
	titleTxt = util.RightAlignLine(titleTxt, rightText, titleWidth)

	pathParts := strings.Split(i.Path, "/")
	shortPath := strings.Join(pathParts[len(pathParts)-2:], "/")
	leftParts := []string{"└ " + shortPath}
	if i.Branch != "" {
		leftParts = append(leftParts, branchLabel(i))
	}
	if len(i.Agents) > 0 {
		leftParts = append(leftParts, fmt.Sprintf("%d agent%s", len(i.Agents), util.Plural(len(i.Agents))))
	}
	if i.Head != "" {
		leftParts = append(leftParts, shortSha(i.Head))
	}
	descTxt := strings.Join(leftParts, " · ")

	title := titleStyle.Render(titleTxt)
	desc := descStyle.Render(descTxt)

	lines := []string{title, desc}

	return strings.Join(lines, "\n")
}

func workspaceTitle(ws workspace.Workspace, hasAgents bool, collapsed bool) string {
	marker := "•"
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

func branchLabel(ws workspace.Workspace) string {
	branch := strings.TrimSpace(ws.Branch)
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "refs/remotes/")
	if ws.VCS == workspace.VCSJujutsu {
		return "󱗆 " + branch
	}
	return " " + branch
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

func (r itemRenderer) agentTimeStatus(ag agent.Agent) string {
	rel := relativeTime(ag.Activity)
	switch agent.DisplayStatus(ag.Status) {
	case "working":
		return strings.TrimSpace(r.spinnerFrame() + " " + rel)
	case "done":
		if rel == "now" {
			return "✓ now"
		}
	}
	return rel
}

func (r itemRenderer) spinnerFrame() string {
	selected := spinnerChoices[r.spinnerIndex].spinner
	if len(selected.Frames) == 0 {
		return ""
	}
	frame := (time.Now().UnixNano() / selected.FPS.Nanoseconds()) % int64(len(selected.Frames))
	return selected.Frames[frame]
}

func shortSha(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
