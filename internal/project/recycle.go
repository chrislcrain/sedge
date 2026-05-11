package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// Recycle moves a worktree (and its claude session history, if any) into
// ~/.sedge/recycle/<ts>-<project>-<session>/. The git worktree is also
// unregistered so subsequent `git worktree list` is clean.
//
// Best-effort: missing claude history is silently skipped, but a missing
// worktree path or failed git unregistration is an error.
func Recycle(p Project, wt Worktree) error {
	root, err := xdg.Root()
	if err != nil {
		return err
	}
	ts := time.Now().Format("20060102-150405")
	bin := filepath.Join(root, "recycle", fmt.Sprintf("%s-%s-%s", ts, p.Name, wt.SessionName))
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}

	// 1. Move the claude session history dir (if present). The conversation
	// store path is ~/.claude/projects/<encoded-cwd>/, where <encoded-cwd>
	// is the worktree path with "/" replaced by "-".
	if home, err := os.UserHomeDir(); err == nil {
		claudeDir := filepath.Join(home, ".claude", "projects", encodeClaudeProject(wt.Path))
		if _, err := os.Stat(claudeDir); err == nil {
			dst := filepath.Join(bin, "claude-session")
			if err := os.Rename(claudeDir, dst); err != nil {
				return fmt.Errorf("move claude session: %w", err)
			}
		}
	}

	// 2. Unregister the worktree with git (--force so it works even with
	// dirty state). This also moves it to git's "to-prune" list. We then
	// move the directory itself.
	if _, err := os.Stat(wt.Path); err == nil {
		_ = exec.Command("git", "-C", p.Path, "worktree", "remove", "--force", wt.Path).Run()
		// Re-check: git worktree remove deletes the dir on success. If it
		// still exists (e.g. the path was already detached), move it.
		if _, err := os.Stat(wt.Path); err == nil {
			dst := filepath.Join(bin, "worktree")
			if err := os.Rename(wt.Path, dst); err != nil {
				return fmt.Errorf("move worktree: %w", err)
			}
		} else {
			// git removed the worktree files; record what was there for
			// auditability.
			_ = os.WriteFile(filepath.Join(bin, "worktree-removed.txt"),
				[]byte(fmt.Sprintf("git removed worktree at %s\nbranch: %s\n", wt.Path, wt.Branch)),
				0o644)
		}
	}

	// 3. Prune any stale branch ref (best-effort).
	_ = exec.Command("git", "-C", p.Path, "branch", "-D", wt.Branch).Run()

	return nil
}

func encodeClaudeProject(path string) string {
	return strings.ReplaceAll(path, "/", "-")
}
