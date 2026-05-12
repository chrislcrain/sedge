package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SpawnClaudeOpts is everything SpawnClaudeWindow needs.
type SpawnClaudeOpts struct {
	WorktreeDir     string
	ProjectPath     string   // passed to --add-dir
	PromptFile      string   // passed to --append-system-prompt-file
	SessionName     string   // passed to claude -n and used as the window name
	PermissionMode  string   // claude --permission-mode value
	Model           string   // optional: claude --model
	ExtraClaudeArgs []string // appended to claude args
	Env             []string // reserved
	AgentsJSON      string   // if non-empty, passed via `claude --agents <json>`
	Resume          bool     // if true, pass --continue
}

// SpawnClaudeWindow creates a new tmux window in the current session running
// claude in the worktree dir. The window is created detached (does not steal
// focus). Returns the new window_id and pane_id.
//
// Each session gets its own window so the claude process can run in the
// background after sedge swaps it out of the visible slot.
func SpawnClaudeWindow(opts SpawnClaudeOpts) (windowID, paneID string, err error) {
	args := []string{
		"new-window", "-d",
		"-n", opts.SessionName,
		"-c", opts.WorktreeDir,
		"-P", "-F", "#{window_id}|#{pane_id}",
	}
	args = append(args, buildClaudeCmdline(opts)...)
	out, runErr := exec.Command("tmux", args...).Output()
	if runErr != nil {
		return "", "", runErr
	}
	parts := strings.SplitN(trimNewline(string(out)), "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected new-window output: %q", string(out))
	}
	// Enable monitor-activity on the new window so we can surface "needs
	// attention" when claude outputs while the window is hidden.
	_, _ = run("set-window-option", "-t", parts[0], "monitor-activity", "on")
	return parts[0], parts[1], nil
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

// ActiveSlotPath returns the cwd of sedge's slot pane.
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

// FindWorktreeWindow searches the current tmux session for the window/pane
// whose pane_current_path equals cwd. Returns ("","",nil) if no match.
func FindWorktreeWindow(cwd string) (windowID, paneID string, err error) {
	session, _ := CurrentSession()
	args := []string{"list-panes"}
	if session == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-s", "-t", session)
	}
	args = append(args, "-F", "#{window_id}|#{pane_id}|#{pane_current_path}")
	out, runErr := exec.Command("tmux", args...).Output()
	if runErr != nil {
		return "", "", runErr
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[2] == cwd {
			return parts[0], parts[1], nil
		}
	}
	return "", "", nil
}

// WindowActivity reports whether the named tmux window currently has the
// monitor-activity "new output since last viewed" flag set. Returns false if
// the window doesn't exist or the flag isn't set.
func WindowActivity(windowID string) bool {
	if windowID == "" {
		return false
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_activity_flag}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// SwapInPane makes targetPaneID the visible slot next to sedge. sedgeCols is
// the absolute number of columns sedge keeps; claude (the joined pane) gets
// the rest of the window. If sedgeCols <= 0 a percentage fallback is used.
//
// Algorithm:
//  1. If sedgePaneID and targetPaneID are already in the same window, just
//     focus the target.
//  2. Otherwise, break the current slot back to its own background window
//     (named after its worktree session) so its process keeps running.
//  3. join-pane the target into sedge's window sized so sedge ends up at
//     sedgeCols columns.
//  4. Focus the newly-joined pane.
func SwapInPane(sedgePaneID, targetPaneID string, sedgeCols int, fallbackSlotPct int) error {
	if sedgePaneID == "" {
		return fmt.Errorf("sedge pane id unknown (TMUX_PANE not set)")
	}
	if targetPaneID == "" {
		return fmt.Errorf("target pane id empty")
	}

	same, err := panesInSameWindow(sedgePaneID, targetPaneID)
	if err != nil {
		return err
	}
	if same {
		_, err := run("select-pane", "-t", targetPaneID)
		return err
	}

	slot, err := findSlotPane(sedgePaneID)
	if err != nil {
		return err
	}
	if slot != "" {
		// Detach the previous slot into its own background window, named
		// after its worktree (so it shows up in `tmux list-windows`).
		name := slotWindowName(slot)
		args := []string{"break-pane", "-d", "-s", slot}
		if name != "" {
			args = append(args, "-n", name)
		}
		out, brErr := exec.Command("tmux", args...).CombinedOutput()
		if brErr != nil {
			return fmt.Errorf("break-pane: %w: %s", brErr, strings.TrimSpace(string(out)))
		}
		// Re-enable activity monitoring on the new (background) window.
		if name != "" {
			_, _ = run("set-window-option", "-t", "@:"+name, "monitor-activity", "on")
		}
	}

	sizeArg := computeJoinSize(sedgePaneID, sedgeCols, fallbackSlotPct)
	if _, err := run("join-pane", "-h", "-l", sizeArg, "-s", targetPaneID, "-t", sedgePaneID); err != nil {
		return err
	}
	_, err = run("select-pane", "-t", targetPaneID)
	return err
}

// computeJoinSize derives the -l argument for join-pane. If sedgeCols > 0,
// claude is sized so sedge ends up at sedgeCols (claude takes the rest).
// Otherwise we fall back to slotPct as a percentage.
func computeJoinSize(sedgePaneID string, sedgeCols, slotPct int) string {
	if sedgeCols > 0 {
		out, err := exec.Command("tmux", "display-message", "-p", "-t", sedgePaneID, "#{window_width}").Output()
		if err == nil {
			if w := atoiSafe(strings.TrimSpace(string(out))); w > 0 {
				claudeWidth := w - sedgeCols
				if claudeWidth < 20 {
					claudeWidth = w / 2 // fallback if window is tiny
				}
				return fmt.Sprintf("%d", claudeWidth)
			}
		}
	}
	if slotPct <= 0 || slotPct >= 100 {
		slotPct = 80
	}
	return fmt.Sprintf("%d%%", slotPct)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// slotWindowName derives a reasonable tmux window name for the slot pane,
// using the basename of its cwd (which is the sedge session name when sedge
// spawned it). Empty string if we can't determine one.
func slotWindowName(paneID string) string {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_current_path}").Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

// FocusPane explicitly selects a tmux pane, used after swap-in to make sure
// the keyboard focus reliably lands on the newly-visible claude pane.
func FocusPane(paneID string) error {
	if paneID == "" {
		return nil
	}
	_, err := run("select-pane", "-t", paneID)
	return err
}

// KillSlotPane kills the slot pane if one exists. Used by the delete flow.
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

// KillWindow kills the named tmux window.
func KillWindow(windowID string) error {
	if windowID == "" {
		return nil
	}
	_, err := run("kill-window", "-t", windowID)
	return err
}

// KillAllWorktreeWindows kills every window in the current session whose
// primary pane is rooted inside `worktreesRoot` (i.e. one of sedge's
// worktrees). Returns the count killed.
func KillAllWorktreeWindows(worktreesRoot string) (int, error) {
	session, _ := CurrentSession()
	args := []string{"list-panes"}
	if session == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-s", "-t", session)
	}
	args = append(args, "-F", "#{window_id}|#{pane_current_path}")
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	root := strings.TrimSuffix(worktreesRoot, "/") + "/"
	killed := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		win, cwd := parts[0], parts[1]
		if seen[win] {
			continue
		}
		if strings.HasPrefix(cwd, root) {
			seen[win] = true
			if err := KillWindow(win); err == nil {
				killed++
			}
		}
	}
	return killed, nil
}

// OpenSubAgentViewer splits the slot pane vertically (below) and runs
// `sedge watch-agent <jsonlPath>` so the user can watch a sub-agent's
// conversation alongside the parent claude.
func OpenSubAgentViewer(sedgePaneID, jsonlPath string) error {
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
	selfPath, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = run("split-window", "-v", "-l", "40%", "-t", slot, selfPath+" watch-agent "+jsonlPath)
	return err
}

// OpenCodePane opens a shell pane to the right of the claude slot pane,
// using the slot pane's actual cwd (the worktree dir).
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
	_, err = run("split-window", "-h", "-l", "40%", "-t", slot, "-c", cwd)
	return err
}

var (
	errNoSedgePane      = newConstErr("sedge pane id unknown (TMUX_PANE not set)")
	errNoActiveWorktree = newConstErr("no active worktree; select one first")
)

type constErr string

func (c constErr) Error() string { return string(c) }
func newConstErr(s string) error { return constErr(s) }

func panesInSameWindow(a, b string) (bool, error) {
	aw, err := paneWindow(a)
	if err != nil {
		return false, err
	}
	bw, err := paneWindow(b)
	if err != nil {
		return false, err
	}
	return aw == bw, nil
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
