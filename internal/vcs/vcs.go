package vcs

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Git Kind = "git"
)

type Branch struct {
	Name   string
	Ref    string
	Remote bool
}

type Worktree struct {
	Path   string
	Branch string
	Bare   bool
}

type CreateWorktreeRequest struct {
	RepoPath      string
	WorktreePath  string
	BaseRef       string
	NewBranchName string
}

func GitRepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git repo root: %s", strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func GitBranches(repoPath string) ([]Branch, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-a", "--format=%(refname):%(refname:short)")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list branches: %s", strings.TrimSpace(out.String()))
	}

	seen := map[string]bool{}
	branches := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		ref, name := parts[0], parts[1]
		if strings.Contains(ref, "/HEAD") || seen[ref] {
			continue
		}
		seen[ref] = true
		branches = append(branches, Branch{Name: name, Ref: ref, Remote: strings.HasPrefix(ref, "refs/remotes/")})
	}
	return branches, nil
}

func GitWorktrees(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list worktrees: %s", strings.TrimSpace(out.String()))
	}

	worktrees := []Worktree{}
	cur := Worktree{}
	inWorktree := false
	for _, line := range strings.Split(out.String(), "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			if inWorktree && cur.Path != "" {
				worktrees = append(worktrees, cur)
			}
			cur = Worktree{Path: after}
			inWorktree = true
			continue
		}
		if !inWorktree {
			continue
		}
		if after, ok := strings.CutPrefix(line, "branch "); ok {
			cur.Branch = after
		}
		if line == "bare" {
			cur.Bare = true
		}
		if line == "" {
			if cur.Path != "" {
				worktrees = append(worktrees, cur)
			}
			cur = Worktree{}
			inWorktree = false
		}
	}
	if inWorktree && cur.Path != "" {
		worktrees = append(worktrees, cur)
	}
	return worktrees, nil
}

func GitCreateWorktree(req CreateWorktreeRequest) error {
	args := []string{"-C", req.RepoPath, "worktree", "add"}
	if strings.TrimSpace(req.NewBranchName) != "" {
		args = append(args, "-b", strings.TrimSpace(req.NewBranchName))
	}
	args = append(args, req.WorktreePath, req.BaseRef)
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create worktree: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func GitRemoveWorktree(repoPath, worktreePath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove worktree: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func SuggestedWorktreeName(branch string) string {
	branch = strings.TrimSpace(branch)
	branch = strings.TrimPrefix(branch, "origin/")
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "refs/remotes/")
	branch = strings.ReplaceAll(branch, "/", "-")
	branch = strings.ReplaceAll(branch, " ", "-")
	if branch == "" {
		return "worktree"
	}
	return branch
}

func SuggestedWorktreeParent(repoPath string) string {
	root, err := GitRepoRoot(repoPath)
	if err != nil || root == "" {
		root = repoPath
	}
	return filepath.Dir(root)
}

func SuggestedWorktreePath(repoPath, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(SuggestedWorktreeParent(repoPath), name)
}
