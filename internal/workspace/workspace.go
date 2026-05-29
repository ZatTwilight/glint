package workspace

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Workspace struct {
	Name         string
	Path         string
	Root         string
	ParentName   string
	IsWorktree   bool
	ActiveInTmux bool
	ModifiedAt   time.Time
}

func Scan(roots []string, activeSessions map[string]bool, activePaths map[string]bool) ([]Workspace, error) {
	seen := map[string]bool{}
	workspaces := []Workspace{}

	for _, root := range roots {
		rootWorkspaces, err := scanRoot(root, activeSessions, activePaths)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, ws := range rootWorkspaces {
			if seen[ws.Path] {
				continue
			}
			seen[ws.Path] = true
			workspaces = append(workspaces, ws)
		}
	}

	Sort(workspaces)
	return workspaces, nil
}

func scanRoot(root string, activeSessions map[string]bool, activePaths map[string]bool) ([]Workspace, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	workspaces := make([]Workspace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}

		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		parent := Workspace{
			Name:         entry.Name(),
			Path:         path,
			Root:         root,
			ActiveInTmux: activeSessions[entry.Name()] || activePaths[filepath.Clean(path)],
			ModifiedAt:   info.ModTime(),
		}
		workspaces = append(workspaces, parent)

		for _, wt := range scanWorktrees(parent, activeSessions, activePaths) {
			workspaces = append(workspaces, wt)
		}
	}
	return workspaces, nil
}

func scanWorktrees(parent Workspace, activeSessions map[string]bool, activePaths map[string]bool) []Workspace {
	out, err := exec.Command("git", "-C", parent.Path, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}

	var worktrees []Workspace
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}

		path := strings.TrimPrefix(line, "worktree ")
		if path == parent.Path {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}

		name := filepath.Base(path)
		worktrees = append(worktrees, Workspace{
			Name:         name,
			Path:         path,
			Root:         parent.Root,
			ParentName:   parent.Name,
			IsWorktree:   true,
			ActiveInTmux: activeSessions[name] || activePaths[filepath.Clean(path)],
			ModifiedAt:   info.ModTime(),
		})
	}
	return worktrees
}

func Sort(workspaces []Workspace) {
	groupActive := map[string]bool{}
	groupModified := map[string]time.Time{}
	for _, ws := range workspaces {
		group := groupName(ws)
		groupActive[group] = groupActive[group] || ws.ActiveInTmux
		if ws.ModifiedAt.After(groupModified[group]) {
			groupModified[group] = ws.ModifiedAt
		}
	}

	sort.SliceStable(workspaces, func(i, j int) bool {
		left := workspaces[i]
		right := workspaces[j]
		leftGroup := groupName(left)
		rightGroup := groupName(right)

		if leftGroup != rightGroup {
			if groupActive[leftGroup] != groupActive[rightGroup] {
				return groupActive[leftGroup]
			}
			if !groupModified[leftGroup].Equal(groupModified[rightGroup]) {
				return groupModified[leftGroup].After(groupModified[rightGroup])
			}
			return leftGroup < rightGroup
		}

		if left.IsWorktree != right.IsWorktree {
			return !left.IsWorktree
		}
		if left.ActiveInTmux != right.ActiveInTmux {
			return left.ActiveInTmux
		}
		if !left.ModifiedAt.Equal(right.ModifiedAt) {
			return left.ModifiedAt.After(right.ModifiedAt)
		}
		return left.Name < right.Name
	})
}

func groupName(ws Workspace) string {
	if ws.ParentName != "" {
		return ws.ParentName
	}
	return ws.Name
}
