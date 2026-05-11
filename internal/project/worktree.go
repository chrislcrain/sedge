package project

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// WorktreePath returns the absolute on-disk path for a session's worktree.
func WorktreePath(worktreesRoot string, p Project, sessionName string) string {
	return filepath.Join(xdg.Expand(worktreesRoot), p.Name, sessionName)
}

// BranchName returns the branch name sedge creates for a session.
func BranchName(sessionName string) string {
	return "sedge/" + sessionName
}

// EnsureWorktree creates a git worktree at wtPath branched from defaultBranch.
// Idempotent: if the path already exists, returns nil.
func EnsureWorktree(repoPath, defaultBranch, wtPath, sessionName string) error {
	if _, err := exec.Command("git", "-C", repoPath, "rev-parse", "--is-inside-work-tree").Output(); err != nil {
		return fmt.Errorf("%s is not a git repo: %w", repoPath, err)
	}
	if listed, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output(); strings.Contains(string(listed), wtPath) {
		return nil
	}
	branch := BranchName(sessionName)
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, wtPath, defaultBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WtState describes the runtime state of a worktree's claude session.
type WtState int

const (
	WtDormant    WtState = iota // no live tmux pane in this worktree
	WtBackground                // live pane exists but it's not the visible slot
	WtWaiting                   // live pane in background AND has new activity since last viewed
	WtActive                    // currently visible next to sedge (the slot pane)
)

// Worktree describes a sedge-managed worktree discovered on disk.
type Worktree struct {
	SessionName string         // last path component, e.g. "s1700000000"
	Path        string         // absolute worktree path
	Branch      string         // e.g. "sedge/s1700000000"
	State       WtState        // populated by the TUI loader from tmux pane info
	WindowID    string         // tmux window id for the background claude pane (when alive)
	SubAgents   []SubAgentInfo // in-flight sub-agent calls (populated by the TUI loader)
}

// SubAgentInfo is a minimal projection of agentlog.SubAgent — kept here to
// avoid the project package importing agentlog.
type SubAgentInfo struct {
	ID          string
	Type        string
	Description string
}

// ListWorktrees returns all sedge-branch worktrees for a repo. Only worktrees
// whose branch begins with "sedge/" are included; the main worktree and any
// other branches are filtered out.
func ListWorktrees(repoPath string) ([]Worktree, error) {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var results []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" && strings.HasPrefix(cur.Branch, "sedge/") {
			cur.SessionName = filepath.Base(cur.Path)
			results = append(results, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return results, nil
}

// RemoveWorktree runs `git worktree remove --force` for the given path.
func RemoveWorktree(repoPath, wtPath string) error {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", wtPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DetectDefaultBranch returns the repo's default branch (main/master fallback).
func DetectDefaultBranch(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		s := strings.TrimSpace(string(out))
		if i := strings.LastIndex(s, "/"); i >= 0 {
			return s[i+1:]
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", candidate).Run(); err == nil {
			return candidate
		}
	}
	return "main"
}
