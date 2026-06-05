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

	"github.com/ZatTwilight/glint/internal/agent"
	"github.com/ZatTwilight/glint/internal/multiplexer"
)

type VCSType int

type GitType int

const (
	VCSNone VCSType = iota
	VCSGit
	VCSJujutsu
)

const (
	none GitType = iota
	bare
	worktree
	detatched
)

type Workspace struct {
	Name         string
	Path         string
	Root         string
	ParentName   string
	IsWorktree   bool
	ActiveInTmux bool
	ModifiedAt   time.Time
	GitType      GitType
	VCS          VCSType
	Branch       string
	Head         string
	Agents       []agent.Agent
}

func Scan(roots []string, activeSessions map[string]bool, activePaths map[string]bool) ([]Workspace, error) {
	return ScanWithPrograms(roots, activeSessions, activePaths, nil)
}

func ScanWithPrograms(roots []string, activeSessions map[string]bool, activePaths map[string]bool, programs []multiplexer.MultiplexerProgram) ([]Workspace, error) {
	if programs == nil {
		programs = multiplexer.TmuxProgramsAll(agent.AgentName, agent.DescendantCommands)
		if programs == nil {
			programs = []multiplexer.MultiplexerProgram{}
		}
	}
	seen := map[string]bool{}
	workspaces := []Workspace{}

	for _, root := range roots {
		rootWorkspaces, err := scanRoot(root, activeSessions, activePaths, programs)
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

func scanRoot(root string, activeSessions map[string]bool, activePaths map[string]bool, programs []multiplexer.MultiplexerProgram) ([]Workspace, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	workspaces := make([]Workspace, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		isJJDir := isJJRepository(path)
		isGitDir := !isJJDir && isGitRepository(path)

		parent := Workspace{
			Name:         entry.Name(),
			Path:         path,
			Root:         root,
			ActiveInTmux: activeSessions[entry.Name()] || activePaths[filepath.Clean(path)],
			ModifiedAt:   info.ModTime(),
			GitType:      none,
			VCS:          VCSNone,
		}
		if isJJDir {
			for _, ws := range scanJJWorkspaces(parent, activeSessions, activePaths, programs) {
				workspaces = append(workspaces, ws)
			}
		} else if !isGitDir {
			parent.Agents = agent.ScanWorkspaceWithPrograms(parent.Name, parent.Path, programs)
			workspaces = append(workspaces, parent)

			// s, _ := json.MarshalIndent(parent, "	", "  ")
			// fmt.Printf("Parent	%s\n", string(s))
		} else {
			parent.VCS = VCSGit
			// fmt.Printf("Checking %s\n", path)
			for _, wt := range scanWorktrees(parent, activeSessions, activePaths, programs) {
				workspaces = append(workspaces, wt)
				// s, _ := json.MarshalIndent(wt, "	", "  ")
				// fmt.Printf("Wt	%s\n", string(s))
			}
		}

	}

	// fmt.Println("Workspaces")
	// for _, wp := range workspaces {
	// 	s, _ := json.MarshalIndent(wp, "", "  ")
	// 	fmt.Println(string(s))
	// }
	return workspaces, nil
}

// worktree /home/kait/Documents/dev/worktree
// bare
//
// worktree /home/kait/Documents/dev/worktree/hi
// HEAD 4079aa8f8ce1765cfcee16f8137bec929f51dddf
// branch refs/heads/hi
//
// worktree /home/kait/Documents/dev/worktree/main
// HEAD cb0ae40467f1cf8ea17ead338ed35086b5919ca5
// branch refs/heads/main

type WorktreeResp struct {
	Path       string
	Kind       GitType
	Head       string
	Branch     string
	IsWorktree bool
	ModTime    time.Time
}

func isJJRepository(path string) bool {
	jjPath := filepath.Join(path, ".jj")
	if info, err := os.Stat(jjPath); err == nil && info.IsDir() {
		return true
	}
	cmd := exec.Command("jj", "-R", path, "--ignore-working-copy", "workspace", "root")
	return cmd.Run() == nil
}

func jjSharedRepoRoot(workspacePath string) string {
	jjPath := filepath.Join(workspacePath, ".jj")
	repoPath := filepath.Join(jjPath, "repo")
	info, err := os.Stat(repoPath)
	if err == nil && info.IsDir() {
		return filepath.Clean(workspacePath)
	}
	contents, err := os.ReadFile(repoPath)
	if err != nil {
		return filepath.Clean(workspacePath)
	}
	sharedRepoPath := strings.TrimSpace(string(contents))
	if sharedRepoPath == "" {
		return filepath.Clean(workspacePath)
	}
	if !filepath.IsAbs(sharedRepoPath) {
		sharedRepoPath = filepath.Join(jjPath, sharedRepoPath)
	}
	sharedRepoPath = filepath.Clean(sharedRepoPath)
	if filepath.Base(sharedRepoPath) != "repo" || filepath.Base(filepath.Dir(sharedRepoPath)) != ".jj" {
		return filepath.Clean(workspacePath)
	}
	return filepath.Dir(filepath.Dir(sharedRepoPath))
}

func isGitRepository(path string) bool {
	gitPath := filepath.Join(path, ".git")
	if info, err := os.Stat(gitPath); err == nil {
		return info.IsDir() || info.Mode().IsRegular()
	}
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	return cmd.Run() == nil
}

func isLinkedWorktree(path string) bool {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

type JJWorkspaceResp struct {
	Name    string
	Path    string
	ModTime time.Time
}

func scanJJWorkspaces(parent Workspace, activeSessions map[string]bool, activePaths map[string]bool, programs []multiplexer.MultiplexerProgram) []Workspace {
	repoRoot := jjSharedRepoRoot(parent.Path)
	out, err := exec.Command("jj", "-R", parent.Path, "--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\t\" ++ root ++ \"\\n\"").Output()
	if err != nil {
		return nil
	}

	var response []JJWorkspaceResp
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		path := filepath.Clean(parts[1])
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		response = append(response, JJWorkspaceResp{Name: parts[0], Path: path, ModTime: info.ModTime()})
	}

	workspaces := make([]Workspace, 0, len(response))
	for _, jjws := range response {
		name := filepath.Base(jjws.Path)
		workspaces = append(workspaces, Workspace{
			Name:         name,
			Path:         jjws.Path,
			Root:         repoRoot,
			ParentName:   filepath.Base(repoRoot),
			IsWorktree:   filepath.Clean(jjws.Path) != filepath.Clean(repoRoot),
			ActiveInTmux: activeSessions[name] || activePaths[filepath.Clean(jjws.Path)],
			ModifiedAt:   jjws.ModTime,
			GitType:      none,
			VCS:          VCSJujutsu,
			Branch:       jjws.Name,
			Agents:       agent.ScanWorkspaceWithPrograms(name, jjws.Path, programs),
		})
	}
	return workspaces
}

func scanWorktrees(parent Workspace, activeSessions map[string]bool, activePaths map[string]bool, programs []multiplexer.MultiplexerProgram) []Workspace {
	out, err := exec.Command("git", "-C", parent.Path, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var response []WorktreeResp
	inRep := false
	var curWorktree WorktreeResp
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "worktree ") && !inRep {
			inRep = true
			curWorktree = WorktreeResp{}
		} else if !inRep {
			continue
		}

		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			path := after
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			curWorktree.Path = path
			curWorktree.ModTime = info.ModTime()
			curWorktree.IsWorktree = isLinkedWorktree(path)
		} else if after, ok := strings.CutPrefix(line, "HEAD "); ok {
			curWorktree.Head = after
		} else if line == "bare" {
			curWorktree.Kind = bare
		} else if after, ok := strings.CutPrefix(line, "branch "); ok {
			curWorktree.Branch = after
			curWorktree.Kind = worktree
		} else if line == "detached" {
			curWorktree.Kind = detatched
		} else if line == "" {
			// End of section
			response = append(response, curWorktree)
			curWorktree = WorktreeResp{}
			inRep = false
		}
	}
	if inRep {
		response = append(response, curWorktree)
		curWorktree = WorktreeResp{}
		inRep = false
	}
	var worktrees []Workspace
	for _, wt := range response {
		name := filepath.Base(wt.Path)
		root := wt.Path
		if wt.IsWorktree {
			root = parent.Path
		} else if wt.Kind == bare {
			root = parent.Root
		}
		worktrees = append(worktrees, Workspace{
			Name:         name,
			Path:         wt.Path,
			Root:         root,
			ParentName:   parent.Name,
			IsWorktree:   wt.IsWorktree,
			ActiveInTmux: activeSessions[name] || activePaths[filepath.Clean(wt.Path)],
			ModifiedAt:   wt.ModTime,
			GitType:      wt.Kind,
			VCS:          VCSGit,
			Branch:       wt.Branch,
			Head:         wt.Head,
			Agents:       agent.ScanWorkspaceWithPrograms(name, wt.Path, programs),
		})
	}

	// os.Exit(0)
	// name := filepath.Base(path)
	// worktrees = append(worktrees, Workspace{
	// 	Name:         name,
	// 	Path:         path,
	// 	Root:         parent.Root,
	// 	ParentName:   parent.Name,
	// 	IsWorktree:   true,
	// 	ActiveInTmux: activeSessions[name] || activePaths[filepath.Clean(path)],
	// 	ModifiedAt:   info.ModTime(),
	// })

	return worktrees
}

func Sort(workspaces []Workspace) {
	groupModified := map[string]time.Time{}
	for _, ws := range workspaces {
		group := groupName(ws)
		modified := effectiveModifiedAt(ws)
		if modified.After(groupModified[group]) {
			groupModified[group] = modified
		}
	}

	sort.SliceStable(workspaces, func(i, j int) bool {
		left := workspaces[i]
		right := workspaces[j]
		leftGroup := groupName(left)
		rightGroup := groupName(right)

		if leftGroup != rightGroup {
			if !groupModified[leftGroup].Equal(groupModified[rightGroup]) {
				return groupModified[leftGroup].After(groupModified[rightGroup])
			}
			return leftGroup < rightGroup
		}

		if left.IsWorktree != right.IsWorktree {
			return !left.IsWorktree
		}
		leftModified := effectiveModifiedAt(left)
		rightModified := effectiveModifiedAt(right)
		if !leftModified.Equal(rightModified) {
			return leftModified.After(rightModified)
		}
		return left.Name < right.Name
	})
}

func effectiveModifiedAt(ws Workspace) time.Time {
	modified := ws.ModifiedAt
	for _, agent := range ws.Agents {
		if agent.Activity.After(modified) {
			modified = agent.Activity
		}
	}
	return modified
}

func GroupName(ws Workspace) string {
	if ws.ParentName != "" {
		return ws.ParentName
	}
	return ws.Name
}

func groupName(ws Workspace) string {
	return GroupName(ws)
}
