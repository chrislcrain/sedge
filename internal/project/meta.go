package project

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// createPRURLRe matches GitHub's "Create a pull request" hint that `git push`
// emits to stderr when pushing a brand-new branch.
var createPRURLRe = regexp.MustCompile(`https?://[^\s]+/pull/new/[^\s]+`)

// extractCreatePRURL pulls the "Create a pull request" URL out of git push
// stderr, or returns "" if none is present (e.g., subsequent push to an
// existing branch, or a non-GitHub remote).
func extractCreatePRURL(pushOutput string) string {
	return createPRURLRe.FindString(pushOutput)
}

// compareURLForOrigin derives a GitHub-style compare URL
// (https://<host>/<owner>/<repo>/compare/<base>...<head>?expand=1) from a
// repo's origin URL. Returns "" for non-GitHub remotes or unrecognised URL
// formats. Used as a fallback when gh is unavailable and git push didn't
// emit a "Create a pull request" hint (i.e. the branch was already on origin).
func compareURLForOrigin(workDir, base, head string) string {
	out, err := exec.Command("git", "-C", workDir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	host, owner, repo := parseRemote(raw)
	if host == "" || owner == "" || repo == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/%s/%s/compare/%s...%s?expand=1",
		host, owner, repo, url.PathEscape(base), url.PathEscape(head))
}

// parseRemote returns (host, owner, repo) from a git remote URL. Handles
// SSH (git@host:owner/repo.git), ssh:// (ssh://git@host/owner/repo.git),
// and https:// forms. Strips any trailing ".git". Returns zero values if
// the URL doesn't look like a host/owner/repo triple.
func parseRemote(raw string) (host, owner, repo string) {
	s := raw
	switch {
	case strings.HasPrefix(s, "git@"):
		// git@host:owner/repo.git
		s = strings.TrimPrefix(s, "git@")
		i := strings.Index(s, ":")
		if i < 0 {
			return "", "", ""
		}
		host, s = s[:i], s[i+1:]
	case strings.HasPrefix(s, "ssh://"):
		s = strings.TrimPrefix(s, "ssh://")
		s = strings.TrimPrefix(s, "git@")
		i := strings.Index(s, "/")
		if i < 0 {
			return "", "", ""
		}
		host, s = s[:i], s[i+1:]
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		i := strings.Index(s, "/")
		if i < 0 {
			return "", "", ""
		}
		host, s = s[:i], s[i+1:]
	default:
		return "", "", ""
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return "", "", ""
	}
	return host, parts[0], parts[1]
}

// existingPRURL returns the URL of an open PR on origin with the given
// head/base, or "" if none exists or gh can't tell us. Best-effort: any gh
// error (missing CLI, not authenticated, no remote) is treated as "no PR" so
// ShipWorktree falls through to its normal create path.
func existingPRURL(workDir, head, base string) string {
	cmd := exec.Command("gh", "pr", "list",
		"--head", head,
		"--base", base,
		"--state", "open",
		"--json", "url",
		"--jq", ".[0].url")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pickShipBranchName returns a "sedge/<session>" branch name that does not
// already exist locally or on origin. Falls back to appending -1, -2, … on
// collision. Returns "" if no unused name was found within a reasonable bound.
func pickShipBranchName(repoPath, session string) string {
	base := BranchName(session)
	for i := 0; i < 100; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if HasLocalBranch(repoPath, candidate) {
			continue
		}
		if exec.Command("git", "-C", repoPath, "rev-parse", "--verify",
			"refs/remotes/origin/"+candidate).Run() == nil {
			continue
		}
		return candidate
	}
	return ""
}

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
	ExistingPRURL  string // non-empty when an open PR already exists for worktreeBranch → sourceBranch; ShipWorktree will push only and skip gh pr create
	RebranchTo     string // non-empty when ShipWorktree will carve commits onto a new branch before pushing (set when WorktreeBranch == SourceBranch)
	Err            error
}

// PreviewShip looks up commit count and remote state without mutating
// anything. Used to populate the P confirm prompt.
func PreviewShip(repoPath, wtPath string, m WorktreeMeta) ShipPreview {
	prev := ShipPreview{
		SourceBranch:   m.SourceBranch,
		WorktreeBranch: m.WorktreeBranch,
	}
	if m.SourceBranch == "" || m.WorktreeBranch == "" {
		prev.Err = errors.New("worktree metadata missing branches")
		return prev
	}
	// Commits ahead. Compute from HEAD when the worktree is on the source
	// branch (so the preview reflects what's actually on disk, not whatever
	// the local source branch points at).
	tip := m.WorktreeBranch
	if m.WorktreeBranch == m.SourceBranch {
		tip = "HEAD"
	}
	if out, err := exec.Command("git", "-C", repoPath, "rev-list", "--count",
		m.SourceBranch+".."+tip).Output(); err == nil {
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
	// If the worktree branch IS the source branch, pushing it would write
	// straight to origin/<source>. Surface that ShipWorktree will instead
	// carve the commits onto a fresh branch and push that.
	if m.WorktreeBranch == m.SourceBranch && wtPath != "" {
		prev.RebranchTo = pickShipBranchName(repoPath, filepath.Base(wtPath))
	}
	// Open PR already targeting this head/base? Use the branch ShipWorktree
	// will actually push (RebranchTo wins when set).
	headForPR := m.WorktreeBranch
	if prev.RebranchTo != "" {
		headForPR = prev.RebranchTo
	}
	if prev.HasRemote && headForPR != m.SourceBranch {
		prev.ExistingPRURL = existingPRURL(repoPath, headForPR, m.SourceBranch)
	}
	return prev
}

// ShipWorktree pushes worktreeBranch to origin and opens a PR via the gh CLI
// targeting sourceBranch. Returns the PR URL gh prints on success, or the
// combined output on failure. Requires `gh auth login` to have been run.
//
// When the worktree is checked out on the source branch itself (e.g. user
// picked main via "check out existing"), pushing m.WorktreeBranch would write
// straight to origin/<source>. To avoid that footgun, ShipWorktree first
// carves the commits onto a fresh "sedge/<session>" branch, resets the local
// source branch back to origin/<source> when safe, and pushes the new branch
// instead. The updated branch name is persisted to .sedge-meta.toml.
func ShipWorktree(repoPath, wtPath string, m WorktreeMeta) (string, error) {
	if m.WorktreeBranch == "" || m.SourceBranch == "" {
		return "", errors.New("worktree metadata missing branches")
	}
	pushDir := wtPath
	if pushDir == "" {
		pushDir = repoPath
	}

	if m.WorktreeBranch == m.SourceBranch {
		newBranch := pickShipBranchName(repoPath, filepath.Base(wtPath))
		if newBranch == "" {
			return "", fmt.Errorf("could not derive an unused sedge/* branch name")
		}
		if out, err := exec.Command("git", "-C", pushDir, "checkout", "-b", newBranch).CombinedOutput(); err != nil {
			return strings.TrimSpace(string(out)), fmt.Errorf("git checkout -b %s: %w", newBranch, err)
		}
		// Reset the local source branch back to origin/<source> if available,
		// so the commits live only on the new branch and the user isn't left
		// with a polluted local main/master. Only safe when <source> isn't
		// checked out in another worktree (whose working tree would then
		// drift from HEAD). Skip silently otherwise.
		checkedOut, _ := CheckedOutBranches(repoPath)
		safeToReset := !checkedOut[m.SourceBranch]
		if safeToReset && exec.Command("git", "-C", pushDir, "rev-parse", "--verify",
			"refs/remotes/origin/"+m.SourceBranch).Run() == nil {
			_ = exec.Command("git", "-C", pushDir, "update-ref",
				"refs/heads/"+m.SourceBranch,
				"refs/remotes/origin/"+m.SourceBranch).Run()
		}
		m.WorktreeBranch = newBranch
		_ = WriteWorktreeMeta(wtPath, m)
	}

	pushOut, err := exec.Command("git", "-C", pushDir, "push", "-u", "origin", m.WorktreeBranch).CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(pushOut)), fmt.Errorf("git push: %w", err)
	}
	pushOutput := strings.TrimSpace(string(pushOut))

	// If an open PR already exists for this head→base, the push above just
	// updated it. Don't try to create a duplicate.
	if url := existingPRURL(pushDir, m.WorktreeBranch, m.SourceBranch); url != "" {
		return "updated PR " + url, nil
	}

	// Try `gh pr create`. --fill auto-derives title/body from the commits.
	cmd := exec.Command("gh", "pr", "create",
		"--base", m.SourceBranch,
		"--head", m.WorktreeBranch,
		"--fill")
	cmd.Dir = pushDir
	prOut, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(prOut)), nil
	}

	// gh failed (missing, not authed, or otherwise unhappy). Fall back to
	// the "create PR" hint URL that git push itself prints — same behaviour
	// as pushing manually. If that's not present (the branch already exists
	// on origin so GitHub didn't emit the hint), synthesise a compare URL
	// from the origin remote.
	if hint := extractCreatePRURL(pushOutput); hint != "" {
		return "pushed; open PR at " + hint, nil
	}
	if hint := compareURLForOrigin(pushDir, m.SourceBranch, m.WorktreeBranch); hint != "" {
		return "pushed; open PR at " + hint, nil
	}
	return "pushed; PR not auto-created (gh: " + strings.TrimSpace(string(prOut)) + ")", nil
}

