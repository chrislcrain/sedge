package tmux

import (
	"os/exec"
	"strings"
)

// SpawnClaudeOpts is everything SpawnClaudePane needs.
type SpawnClaudeOpts struct {
	WorktreeDir     string
	ProjectPath     string   // passed to --add-dir
	PromptFile      string   // passed to --append-system-prompt-file
	SessionName     string   // passed to claude -n
	PermissionMode  string   // claude --permission-mode value
	Model           string   // optional: claude --model
	ExtraClaudeArgs []string // appended to claude args
	Env             []string // reserved; not currently applied
	AgentsJSON      string   // if non-empty, passed via `claude --agents <json>`
	Resume          bool     // if true, pass --continue so claude resumes the most recent conversation in WorktreeDir
}

// SpawnClaudePane splits sedge's current tmux window horizontally and starts
// claude in the new pane (right side, ~75% width). Returns the new pane id.
//
// Caller is responsible for first calling KillSlotPane to remove any existing
// pane in the slot.
func SpawnClaudePane(opts SpawnClaudeOpts) (paneID string, err error) {
	args := []string{
		"split-window", "-h", "-p", "75",
		"-c", opts.WorktreeDir,
		"-P", "-F", "#{pane_id}",
	}
	args = append(args, buildClaudeCmdline(opts)...)
	out, runErr := exec.Command("tmux", args...).Output()
	if runErr != nil {
		return "", runErr
	}
	return trimNewline(string(out)), nil
}

func buildClaudeCmdline(opts SpawnClaudeOpts) []string {
	args := []string{
		"claude",
		"--permission-mode", opts.PermissionMode,
		"--add-dir", opts.ProjectPath,
		"--append-system-prompt-file", opts.PromptFile,
		"-n", opts.SessionName,
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if strings.TrimSpace(opts.AgentsJSON) != "" {
		args = append(args, "--agents", opts.AgentsJSON)
	}
	if opts.Resume {
		args = append(args, "--continue")
	}
	args = append(args, opts.ExtraClaudeArgs...)
	return args
}

// LivePaths returns the set of pane_current_path values across all tmux panes.
func LivePaths() (map[string]bool, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_current_path}").Output()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// ActiveSlotPath returns the cwd of sedge's slot pane (the pane in sedge's
// window that isn't sedge). Empty string if no slot exists.
func ActiveSlotPath(sedgePaneID string) (string, error) {
	if sedgePaneID == "" {
		return "", nil
	}
	slot, err := findSlotPane(sedgePaneID)
	if err != nil || slot == "" {
		return "", err
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", slot, "#{pane_current_path}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// OpenCodePane splits the slot pane horizontally and opens a shell in the
// slot pane's actual cwd (which sedge sets to the worktree dir when spawning
// claude). Errors if there is no slot pane (no active worktree).
func OpenCodePane(sedgePaneID string) error {
	if sedgePaneID == "" {
		return errNoSedgePane
	}
	slot, err := findSlotPane(sedgePaneID)
	if err != nil {
		return err
	}
	if slot == "" {
		return errNoActiveWorktree
	}
	cwdOut, err := exec.Command("tmux", "display-message", "-p", "-t", slot, "#{pane_current_path}").Output()
	if err != nil {
		return err
	}
	cwd := strings.TrimSpace(string(cwdOut))
	if cwd == "" {
		return errNoActiveWorktree
	}
	_, err = run("split-window", "-h", "-p", "40", "-t", slot, "-c", cwd)
	return err
}

var (
	errNoSedgePane      = newConstErr("sedge pane id unknown (TMUX_PANE not set)")
	errNoActiveWorktree = newConstErr("no active worktree; select one first")
)

type constErr string

func (c constErr) Error() string { return string(c) }
func newConstErr(s string) error { return constErr(s) }

// KillSlotPane kills the slot pane (the non-sedge pane in sedge's window) if
// one exists. Safe to call when there is no slot — no-op in that case.
func KillSlotPane(sedgePaneID string) error {
	if sedgePaneID == "" {
		return nil
	}
	slot, err := findSlotPane(sedgePaneID)
	if err != nil || slot == "" {
		return err
	}
	_, err = run("kill-pane", "-t", slot)
	return err
}

func paneWindow(paneID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func findSlotPane(sedgePaneID string) (string, error) {
	win, err := paneWindow(sedgePaneID)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("tmux", "list-panes", "-t", win, "-F", "#{pane_id}").Output()
	if err != nil {
		return "", err
	}
	for _, id := range strings.Fields(string(out)) {
		if id != sedgePaneID {
			return id, nil
		}
	}
	return "", nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
