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
	Sources(repoPath string) ([]Source, error)
	WorkspaceRefs(repoPath string) ([]WorkspaceRef, error)
	CreateWorkspace(req CreateWorkspaceRequest) error
	RemoveWorkspace(repoPath, workspacePath string, force bool) error
	SuggestedWorkspaceParent(repoPath string) string
	SuggestedWorkspacePath(repoPath, name string) string
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

type Source struct {
	Name   string
	Ref    string
	Remote bool
}

type WorkspaceRef struct {
	Path   string
	Source string
	Bare   bool
}

type CreateWorkspaceRequest struct {
	RepoPath      string
	WorkspacePath string
	BaseRef       string
	NewSourceName string
}

// Backwards-compatible aliases while the rest of Glint migrates to neutral names.
type Branch = Source
type Worktree = WorkspaceRef
type CreateWorktreeRequest = CreateWorkspaceRequest

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

func (GitBackend) Sources(repoPath string) ([]Source, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-a", "--format=%(refname):%(refname:short)")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list branches: %s", strings.TrimSpace(out.String()))
	}

	seen := map[string]bool{}
	branches := []Source{}
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
		branches = append(branches, Source{Name: name, Ref: ref, Remote: strings.HasPrefix(ref, "refs/remotes/")})
	}
	return branches, nil
}

func (GitBackend) WorkspaceRefs(repoPath string) ([]WorkspaceRef, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list worktrees: %s", strings.TrimSpace(out.String()))
	}

	worktrees := []WorkspaceRef{}
	cur := WorkspaceRef{}
	inWorktree := false
	for _, line := range strings.Split(out.String(), "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			if inWorktree && cur.Path != "" {
				worktrees = append(worktrees, cur)
			}
			cur = WorkspaceRef{Path: after}
			inWorktree = true
			continue
		}
		if !inWorktree {
			continue
		}
		if after, ok := strings.CutPrefix(line, "branch "); ok {
			cur.Source = after
		}
		if line == "bare" {
			cur.Bare = true
		}
		if line == "" {
			if cur.Path != "" {
				worktrees = append(worktrees, cur)
			}
			cur = WorkspaceRef{}
			inWorktree = false
		}
	}
	if inWorktree && cur.Path != "" {
		worktrees = append(worktrees, cur)
	}
	return worktrees, nil
}

func (GitBackend) CreateWorkspace(req CreateWorkspaceRequest) error {
	args := []string{"-C", req.RepoPath, "worktree", "add"}
	if strings.TrimSpace(req.NewSourceName) != "" {
		args = append(args, "-b", strings.TrimSpace(req.NewSourceName))
	}
	args = append(args, req.WorkspacePath, req.BaseRef)
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create worktree: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func (GitBackend) RemoveWorkspace(repoPath, workspacePath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove", workspacePath}
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

func (JJBackend) Sources(repoPath string) ([]Source, error) {
	branches := []Source{{Name: "@", Ref: "@"}}
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
		branches = append(branches, Source{Name: name, Ref: name})
	}
	return branches, nil
}

func (JJBackend) WorkspaceRefs(repoPath string) ([]WorkspaceRef, error) {
	cmd := exec.Command("jj", "-R", repoPath, "--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\t\" ++ root ++ \"\\n\"")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list workspaces: %s", strings.TrimSpace(out.String()))
	}
	worktrees := []WorkspaceRef{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		worktrees = append(worktrees, WorkspaceRef{Path: parts[1], Source: parts[0]})
	}
	return worktrees, nil
}

func (JJBackend) CreateWorkspace(req CreateWorkspaceRequest) error {
	name := filepath.Base(req.WorkspacePath)
	args := []string{"-R", req.RepoPath, "workspace", "add", "--name", name}
	if strings.TrimSpace(req.BaseRef) != "" {
		args = append(args, "-r", req.BaseRef)
	}
	args = append(args, req.WorkspacePath)
	cmd := exec.Command("jj", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create workspace: %s", strings.TrimSpace(out.String()))
	}
	return nil
}

func (backend JJBackend) RemoveWorkspace(repoPath, workspacePath string, force bool) error {
	workspaces, err := backend.WorkspaceRefs(repoPath)
	if err != nil {
		return err
	}
	name := ""
	for _, ws := range workspaces {
		if filepath.Clean(ws.Path) == filepath.Clean(workspacePath) {
			name = ws.Source
			break
		}
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("workspace not found for %s", workspacePath)
	}
	cmd := exec.Command("jj", "-R", repoPath, "workspace", "forget", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("forget workspace: %s", strings.TrimSpace(out.String()))
	}
	cleanPath := filepath.Clean(workspacePath)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return fmt.Errorf("refusing to remove unsafe workspace directory: %s", workspacePath)
	}
	if info, err := os.Stat(filepath.Join(cleanPath, ".jj")); err != nil || !info.IsDir() {
		return fmt.Errorf("refusing to remove %s; no .jj directory found", workspacePath)
	}
	if err := os.RemoveAll(cleanPath); err != nil {
		return fmt.Errorf("remove workspace directory: %w", err)
	}
	return nil
}

func (backend JJBackend) SuggestedWorkspaceParent(repoPath string) string {
	root, err := backend.RepoRoot(repoPath)
	if err != nil || root == "" {
		root = repoPath
	}
	return filepath.Dir(root)
}

func (backend JJBackend) SuggestedWorkspacePath(repoPath, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(backend.SuggestedWorkspaceParent(repoPath), name)
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

func (GitBackend) SuggestedWorkspaceParent(repoPath string) string {
	root, err := GitBackend{}.RepoRoot(repoPath)
	if err != nil || root == "" {
		root = repoPath
	}
	return filepath.Dir(root)
}

func (backend GitBackend) SuggestedWorkspacePath(repoPath, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(backend.SuggestedWorkspaceParent(repoPath), name)
}

func GitRepoRoot(path string) (string, error)           { return GitBackend{}.RepoRoot(path) }
func GitBranches(repoPath string) ([]Branch, error)     { return GitBackend{}.Sources(repoPath) }
func GitWorktrees(repoPath string) ([]Worktree, error)  { return GitBackend{}.WorkspaceRefs(repoPath) }
func GitCreateWorktree(req CreateWorktreeRequest) error { return GitBackend{}.CreateWorkspace(req) }
func GitRemoveWorktree(repoPath, worktreePath string, force bool) error {
	return GitBackend{}.RemoveWorkspace(repoPath, worktreePath, force)
}
func SuggestedWorktreeParent(repoPath string) string {
	return GitBackend{}.SuggestedWorkspaceParent(repoPath)
}
func SuggestedWorktreePath(repoPath, name string) string {
	return GitBackend{}.SuggestedWorkspacePath(repoPath, name)
}

func WorkspaceHasLocalChanges(backend Backend, workspacePath string) (bool, error) {
	switch backend.Kind() {
	case Git:
		cmd := exec.Command("git", "-C", workspacePath, "status", "--porcelain")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("git status: %s", strings.TrimSpace(out.String()))
		}
		return strings.TrimSpace(out.String()) != "", nil
	case Jujutsu:
		cmd := exec.Command("jj", "-R", workspacePath, "--ignore-working-copy", "diff", "--quiet")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if err == nil {
			return false, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("jj diff: %s", strings.TrimSpace(out.String()))
	default:
		return false, nil
	}
}
