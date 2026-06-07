package vcs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Git     Kind = "git"
	Jujutsu Kind = "jj"
)

type Backend interface {
	Kind() Kind
	RepoRoot(path string) (string, error)
	Branches(repoPath string) ([]Branch, error)
	Worktrees(repoPath string) ([]Worktree, error)
	CreateWorktree(req CreateWorktreeRequest) error
	RemoveWorktree(repoPath, worktreePath string, force bool) error
	SuggestedWorktreeParent(repoPath string) string
	SuggestedWorktreePath(repoPath, name string) string
}

type GitBackend struct{}
type JJBackend struct{}

func (GitBackend) Kind() Kind { return Git }
func (JJBackend) Kind() Kind  { return Jujutsu }

func ForPath(path string) Backend {
	if _, err := (JJBackend{}).RepoRoot(path); err == nil {
		return JJBackend{}
	}
	return GitBackend{}
}

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

func (GitBackend) RepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git repo root: %s", strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (GitBackend) Branches(repoPath string) ([]Branch, error) {
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

func (GitBackend) Worktrees(repoPath string) ([]Worktree, error) {
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

func (GitBackend) CreateWorktree(req CreateWorktreeRequest) error {
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

func (GitBackend) RemoveWorktree(repoPath, worktreePath string, force bool) error {
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

func (JJBackend) RepoRoot(path string) (string, error) {
	cmd := exec.Command("jj", "-R", path, "--ignore-working-copy", "root")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("jj repo root: %s", strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (JJBackend) Branches(repoPath string) ([]Branch, error) {
	branches := []Branch{{Name: "@", Ref: "@"}}
	cmd := exec.Command("jj", "-R", repoPath, "--ignore-working-copy", "bookmark", "list", "-a", "-T", "name ++ \"\\t\" ++ name ++ \"\\n\"")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list bookmarks: %s", strings.TrimSpace(out.String()))
	}
	seen := map[string]bool{"@": true}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, Branch{Name: name, Ref: name})
	}
	return branches, nil
}

func (JJBackend) Worktrees(repoPath string) ([]Worktree, error) {
	cmd := exec.Command("jj", "-R", repoPath, "--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\t\" ++ root ++ \"\\n\"")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list workspaces: %s", strings.TrimSpace(out.String()))
	}
	worktrees := []Worktree{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		worktrees = append(worktrees, Worktree{Path: parts[1], Branch: parts[0]})
	}
	return worktrees, nil
}

func (JJBackend) CreateWorktree(req CreateWorktreeRequest) error {
	name := filepath.Base(req.WorktreePath)
	args := []string{"-R", req.RepoPath, "workspace", "add", "--name", name}
	if strings.TrimSpace(req.BaseRef) != "" {
		args = append(args, "-r", req.BaseRef)
	}
	args = append(args, req.WorktreePath)
	cmd := exec.Command("jj", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create workspace: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func (backend JJBackend) RemoveWorktree(repoPath, worktreePath string, force bool) error {
	workspaces, err := backend.Worktrees(repoPath)
	if err != nil {
		return err
	}
	name := ""
	for _, ws := range workspaces {
		if filepath.Clean(ws.Path) == filepath.Clean(worktreePath) {
			name = ws.Branch
			break
		}
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workspace not found for %s", worktreePath)
	}
	cmd := exec.Command("jj", "-R", repoPath, "workspace", "forget", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("forget workspace: %s", strings.TrimSpace(out.String()))
	}
	cleanPath := filepath.Clean(worktreePath)
	if cleanPath == "." || cleanPath == string(filepath.Separator) || cleanPath == filepath.Clean(repoPath) {
		return fmt.Errorf("refusing to remove unsafe workspace directory: %s", worktreePath)
	}
	if err := os.RemoveAll(cleanPath); err != nil {
		return fmt.Errorf("remove workspace directory: %w", err)
	}
	return nil
}

func (backend JJBackend) SuggestedWorktreeParent(repoPath string) string {
	root, err := backend.RepoRoot(repoPath)
	if err != nil || root == "" {
		root = repoPath
	}
	return filepath.Dir(root)
}

func (backend JJBackend) SuggestedWorktreePath(repoPath, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(backend.SuggestedWorktreeParent(repoPath), name)
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

func (GitBackend) SuggestedWorktreeParent(repoPath string) string {
	root, err := GitBackend{}.RepoRoot(repoPath)
	if err != nil || root == "" {
		root = repoPath
	}
	return filepath.Dir(root)
}

func (backend GitBackend) SuggestedWorktreePath(repoPath, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(backend.SuggestedWorktreeParent(repoPath), name)
}

func GitRepoRoot(path string) (string, error)           { return GitBackend{}.RepoRoot(path) }
func GitBranches(repoPath string) ([]Branch, error)     { return GitBackend{}.Branches(repoPath) }
func GitWorktrees(repoPath string) ([]Worktree, error)  { return GitBackend{}.Worktrees(repoPath) }
func GitCreateWorktree(req CreateWorktreeRequest) error { return GitBackend{}.CreateWorktree(req) }
func GitRemoveWorktree(repoPath, worktreePath string, force bool) error {
	return GitBackend{}.RemoveWorktree(repoPath, worktreePath, force)
}
func SuggestedWorktreeParent(repoPath string) string {
	return GitBackend{}.SuggestedWorktreeParent(repoPath)
}
func SuggestedWorktreePath(repoPath, name string) string {
	return GitBackend{}.SuggestedWorktreePath(repoPath, name)
}
