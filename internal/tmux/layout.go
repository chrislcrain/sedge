package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	return FindWindowForCwd(cwd, "")
}

// FindWindowForCwd is FindWorktreeWindow with an excluded pane id. Useful when
// the caller wants to avoid matching its own pane (e.g. the sedge pane when
// sedge was launched from inside the very directory we're searching for, as
// happens with adhoc-chat against a project root).
func FindWindowForCwd(cwd, excludePaneID string) (windowID, paneID string, err error) {
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
		if parts[1] == excludePaneID {
			continue
		}
		if parts[2] == cwd {
			return parts[0], parts[1], nil
		}
	}
	return "", "", nil
}

// SwapInPane makes the worktree containing targetPaneID the visible slot
// next to sedge. sedgeCols is the absolute number of columns sedge keeps;
// the joined panes share the rest of the window. If sedgeCols <= 0 a
// percentage fallback is used.
//
// Algorithm:
//  1. If sedgePaneID and targetPaneID are already in the same window, just
//     focus the target.
//  2. Otherwise, break the current slot subtree back to its own background
//     window so the processes inside keep running.
//  3. Bring EVERY pane from the target worktree's window into sedge's
//     window. The first one is sized so sedge ends up at sedgeCols columns;
//     additional panes (user-added shells, sub-agent viewers, etc.) are
//     stacked next to it preserving the multi-pane slot per worktree.
//  4. Focus the newly-joined primary pane.
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

	// Resolve every pane sharing the target's window — these are the
	// worktree's full slot subtree (claude plus any user splits).
	targetWin, err := paneWindow(targetPaneID)
	if err != nil {
		return err
	}
	targetPanes, err := paneIDsInWindow(targetWin)
	if err != nil {
		return err
	}
	if len(targetPanes) == 0 {
		return fmt.Errorf("target window %q has no panes", targetWin)
	}
	// Put the explicitly requested pane first so it lands in the primary
	// slot position and is the one we focus at the end.
	targetPanes = movePaneFirst(targetPanes, targetPaneID)

	// Capture the user's current sedge pane width BEFORE detaching the slot
	// so we can preserve manual resizes across swaps. Only meaningful when a
	// slot pane is currently sharing the window with sedge — otherwise sedge
	// already fills the window and the captured width is the full width.
	slot, err := findSlotPane(sedgePaneID)
	if err != nil {
		return err
	}
	preservedCols := 0
	if slot != "" {
		preservedCols = paneCols(sedgePaneID)
	}

	if err := DetachSlotPane(sedgePaneID); err != nil {
		return err
	}

	// If we captured a user-set width, honour it; only fall back to the
	// configured/computed sedgeCols when we don't have one (first swap).
	desiredSedgeCols := sedgeCols
	if preservedCols > 0 {
		desiredSedgeCols = preservedCols
	}
	sizeArg := computeJoinSize(sedgePaneID, desiredSedgeCols, fallbackSlotPct)
	if _, err := run("join-pane", "-h", "-l", sizeArg, "-s", targetPanes[0], "-t", sedgePaneID); err != nil {
		return err
	}
	// Bring any extra panes the worktree carried (shells, viewers, manual
	// splits) into sedge's window too, parking each next to the primary
	// pane. tmux will subdivide that region; best-effort on layout.
	for _, p := range targetPanes[1:] {
		if _, jerr := run("join-pane", "-h", "-s", p, "-t", targetPanes[0]); jerr != nil {
			// If a particular pane refuses to join, leave it in its
			// source window rather than losing the process.
			continue
		}
	}
	_, err = run("select-pane", "-t", targetPanes[0])
	return err
}

// paneIDsInWindow returns every pane in the given window, in tmux list
// order. Used to scoop up a worktree's full multi-pane slot subtree.
func paneIDsInWindow(windowID string) ([]string, error) {
	if windowID == "" {
		return nil, nil
	}
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}").Output()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, id := range strings.Fields(string(out)) {
		ids = append(ids, id)
	}
	return ids, nil
}

// movePaneFirst returns ids with `first` placed at index 0 if it's present,
// preserving the relative order of the rest. Used so SwapInPane joins the
// primary claude pane before any user-added extras.
func movePaneFirst(ids []string, first string) []string {
	out := make([]string, 0, len(ids))
	out = append(out, first)
	for _, id := range ids {
		if id != first {
			out = append(out, id)
		}
	}
	return out
}

// WindowActivity returns the time of the window's last activity (any pane
// output), or the zero time if it can't be determined. Used alongside claude
// JSONL mtime so that a shell, test runner, or other tool sharing the
// worktree's background window also surfaces as "needs attention".
func WindowActivity(windowID string) time.Time {
	if windowID == "" {
		return time.Time{}
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_activity}").Output()
	if err != nil {
		return time.Time{}
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}
	}
	// tmux emits window_activity as a Unix epoch (seconds).
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// paneCols returns the current width (in columns) of the given pane, or 0
// if it can't be determined.
func paneCols(paneID string) int {
	if paneID == "" {
		return 0
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_width}").Output()
	if err != nil {
		return 0
	}
	return atoiSafe(strings.TrimSpace(string(out)))
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

// OpenShellPaneAt splits a shell pane in the given cwd. If a slot pane is
// already next to sedge, the new pane lands to the right of it; otherwise
// it lands to the right of sedge directly. Used by `o` on a project row to
// open a shell at the project's path even when no claude session is selected.
func OpenShellPaneAt(sedgePaneID, cwd string) error {
	if sedgePaneID == "" {
		return errNoSedgePane
	}
	target := sedgePaneID
	if slot, _ := findSlotPane(sedgePaneID); slot != "" {
		target = slot
	}
	_, err := run("split-window", "-h", "-l", "40%", "-t", target, "-c", cwd)
	return err
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

// DetachSlotPane breaks every non-sedge pane in sedge's window back to a
// single background tmux window so the processes inside keep running while no
// longer sharing the sedge window. No-op if sedge is alone in its window.
//
// All non-sedge panes are evacuated — not just the "primary" slot — so that
// user-opened extras (a shell via `o`, a sub-agent viewer split, etc.) don't
// linger in the sedge window and squash sedge to a sliver on the next swap.
func DetachSlotPane(sedgePaneID string) error {
	if sedgePaneID == "" {
		return nil
	}
	slots, err := findAllSlotPanes(sedgePaneID)
	if err != nil || len(slots) == 0 {
		return err
	}
	name := slotWindowName(slots[0])
	args := []string{"break-pane", "-d", "-P", "-F", "#{window_id}", "-s", slots[0]}
	if name != "" {
		args = append(args, "-n", name)
	}
	out, brErr := exec.Command("tmux", args...).CombinedOutput()
	if brErr != nil {
		return fmt.Errorf("break-pane: %w: %s", brErr, strings.TrimSpace(string(out)))
	}
	newWinID := strings.TrimSpace(string(out))
	if newWinID != "" {
		_, _ = run("set-window-option", "-t", newWinID, "monitor-activity", "on")
	}
	// Move any remaining non-sedge panes into the same background window so
	// they survive but stop crowding sedge. Target slots[0] (the just-broken
	// pane, now in the new window) directly rather than the window id, since
	// `join-pane -t` expects a pane spec.
	for _, p := range slots[1:] {
		if _, jerr := run("join-pane", "-d", "-h", "-s", p, "-t", slots[0]); jerr != nil {
			// Last-resort fallback: if we somehow can't park it next to the
			// broken-out pane, kill it rather than leave it crushing sedge.
			_, _ = run("kill-pane", "-t", p)
		}
	}
	return nil
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

// findAllSlotPanes returns every non-sedge pane sharing sedge's window, in
// tmux list-panes order. Used when we need to clear the window of everything
// but sedge (e.g. before joining a fresh slot pane on worktree swap).
func findAllSlotPanes(sedgePaneID string) ([]string, error) {
	win, err := paneWindow(sedgePaneID)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("tmux", "list-panes", "-t", win, "-F", "#{pane_id}").Output()
	if err != nil {
		return nil, err
	}
	var slots []string
	for _, id := range strings.Fields(string(out)) {
		if id != sedgePaneID {
			slots = append(slots, id)
		}
	}
	return slots, nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
