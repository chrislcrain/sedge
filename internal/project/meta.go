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

// ShipPreview is the dry-run result of P — what would happen on push +
// `gh pr create` without actually doing either.
type ShipPreview struct {
	SourceBranch   string // branch the PR will target
	WorktreeBranch string // branch being pushed
	CommitsAhead   int    // commits in worktreeBranch not in sourceBranch
	HasRemote      bool   // origin exists in repo
	BranchExists   bool   // worktreeBranch already on origin (push will be a fast-forward update)
	Err            error
}

// PreviewShip looks up commit count and remote state without mutating
// anything. Used to populate the P confirm prompt.
func PreviewShip(repoPath string, m WorktreeMeta) ShipPreview {
	prev := ShipPreview{
		SourceBranch:   m.SourceBranch,
		WorktreeBranch: m.WorktreeBranch,
	}
	if m.SourceBranch == "" || m.WorktreeBranch == "" {
		prev.Err = errors.New("worktree metadata missing branches")
		return prev
	}
	// Commits ahead.
	if out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count",
		m.SourceBranch+".."+m.WorktreeBranch).Output(); err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &prev.CommitsAhead)
	}
	// origin remote present?
	if err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Run(); err == nil {
		prev.HasRemote = true
	}
	// Branch already on origin?
	if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify",
		"refs/remotes/origin/"+m.WorktreeBranch).Run(); err == nil {
		prev.BranchExists = true
	}
	return prev
}

// ShipWorktree pushes worktreeBranch to origin and opens a PR via the gh CLI
// targeting sourceBranch. Returns the PR URL gh prints on success, or the
// combined output on failure. Requires `gh auth login` to have been run.
func ShipWorktree(repoPath, wtPath string, m WorktreeMeta) (string, error) {
	if m.WorktreeBranch == "" || m.SourceBranch == "" {
		return "", errors.New("worktree metadata missing branches")
	}
	// Push from the worktree (its checked-out branch == m.WorktreeBranch).
	pushDir := wtPath
	if pushDir == "" {
		pushDir = repoPath
	}
	pushOut, err := exec.Command("git", "-C", pushDir, "push", "-u", "origin", m.WorktreeBranch).CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(pushOut)), fmt.Errorf("git push: %w", err)
	}
	// Create the PR. --fill auto-derives title/body from the branch's commits.
	cmd := exec.Command("gh", "pr", "create",
		"--base", m.SourceBranch,
		"--head", m.WorktreeBranch,
		"--fill")
	cmd.Dir = pushDir
	prOut, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(prOut)), fmt.Errorf("gh pr create: %w", err)
	}
	return strings.TrimSpace(string(prOut)), nil
}

