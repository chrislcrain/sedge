package project

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// WorktreeMeta is sidecar metadata sedge writes alongside each worktree at
// <wtPath>/.sedge-meta.toml. Recording the source branch lets us offer
// "merge back to source" later.
type WorktreeMeta struct {
	SourceBranch   string `toml:"source_branch"`
	WorktreeBranch string `toml:"worktree_branch"`
}

const metaFileName = ".sedge-meta.toml"

func metaPath(wtPath string) string {
	return filepath.Join(wtPath, metaFileName)
}

// WriteWorktreeMeta writes sidecar metadata. Best-effort; non-fatal errors
// are returned but typically ignored by callers.
func WriteWorktreeMeta(wtPath string, m WorktreeMeta) error {
	f, err := os.Create(metaPath(wtPath))
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(m)
}

// ReadWorktreeMeta reads sidecar metadata. Returns os.ErrNotExist if no
// metadata is on disk.
func ReadWorktreeMeta(wtPath string) (WorktreeMeta, error) {
	var m WorktreeMeta
	data, err := os.ReadFile(metaPath(wtPath))
	if err != nil {
		return m, err
	}
	if _, err := toml.Decode(string(data), &m); err != nil {
		return m, err
	}
	return m, nil
}

// MergePreview is the dry-run result of a merge — what would happen if the
// user confirmed M. Used to show conflicts before sedge mutates the project
// repo.
type MergePreview struct {
	SourceBranch   string   // resolved source branch (from sidecar or fallback)
	WorktreeBranch string   // resolved worktree branch
	CurrentBranch  string   // project dir's current branch (empty if detached)
	NeedsCheckout  bool     // project dir isn't on source — sedge will switch first
	AlreadyMerged  bool     // worktree branch is an ancestor of source — no-op
	Clean          bool     // merge would be conflict-free
	Conflicts      []string // file paths that would conflict (only when Clean=false)
	Err            error    // non-nil if the dry-run itself failed
}

// PreviewMerge runs `git merge-tree` against repoPath to predict whether
// merging worktreeBranch into sourceBranch would conflict, plus reads the
// project dir's current branch so we can warn the user if a checkout is
// needed first. Does not mutate anything.
func PreviewMerge(repoPath string, m WorktreeMeta) MergePreview {
	prev := MergePreview{
		SourceBranch:   m.SourceBranch,
		WorktreeBranch: m.WorktreeBranch,
	}
	if m.SourceBranch == "" || m.WorktreeBranch == "" {
		prev.Err = errors.New("worktree metadata missing branches")
		return prev
	}

	// Project dir's current branch.
	if out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output(); err == nil {
		prev.CurrentBranch = strings.TrimSpace(string(out))
		prev.NeedsCheckout = prev.CurrentBranch != "" && prev.CurrentBranch != m.SourceBranch
	}

	// Already-merged check.
	if err := exec.Command("git", "-C", repoPath, "merge-base", "--is-ancestor",
		m.WorktreeBranch, m.SourceBranch).Run(); err == nil {
		prev.AlreadyMerged = true
		prev.Clean = true
		return prev
	}

	// Conflict preview via git merge-tree (git >= 2.38).
	out, err := exec.Command("git", "-C", repoPath, "merge-tree", "--write-tree", "--name-only",
		m.SourceBranch, m.WorktreeBranch).Output()
	if err == nil {
		// Exit 0 = clean merge.
		prev.Clean = true
		return prev
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// Exit 1 = conflicts. stdout: tree SHA, blank line, conflicted file list.
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		// First line is the tree SHA. Skip blank lines.
		var conflicts []string
		for i, l := range lines {
			l = strings.TrimSpace(l)
			if i == 0 || l == "" {
				continue
			}
			conflicts = append(conflicts, l)
		}
		prev.Conflicts = conflicts
		return prev
	}
	prev.Err = fmt.Errorf("merge-tree dry run: %w", err)
	return prev
}

// MergeWorktreeBack runs `git merge --no-ff <worktreeBranch>` against
// sourceBranch in the main repo (repoPath). If sourceBranch isn't currently
// checked out, sedge will attempt to switch the main repo to it first.
// Returns the combined git output and any error.
func MergeWorktreeBack(repoPath string, m WorktreeMeta) (string, error) {
	if m.WorktreeBranch == "" || m.SourceBranch == "" {
		return "", errors.New("worktree metadata missing branches")
	}
	if m.WorktreeBranch == m.SourceBranch {
		return "", errors.New("worktree branch equals source branch; nothing to merge")
	}

	out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return string(out), fmt.Errorf("read current branch: %w", err)
	}
	current := strings.TrimSpace(string(out))
	var transcript strings.Builder
	if current != m.SourceBranch {
		coOut, coErr := exec.Command("git", "-C", repoPath, "checkout", m.SourceBranch).CombinedOutput()
		transcript.WriteString(string(coOut))
		if coErr != nil {
			return transcript.String(), fmt.Errorf("checkout %s: %w", m.SourceBranch, coErr)
		}
	}
	mergeOut, mergeErr := exec.Command("git", "-C", repoPath, "merge", "--no-ff", m.WorktreeBranch).CombinedOutput()
	transcript.WriteString(string(mergeOut))
	if mergeErr != nil {
		return transcript.String(), fmt.Errorf("merge %s into %s: %w", m.WorktreeBranch, m.SourceBranch, mergeErr)
	}
	return transcript.String(), nil
}
