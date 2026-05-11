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
