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
