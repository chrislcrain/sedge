package tmux

import (
	"fmt"
	"os"
	"os/exec"
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

// OpenShellPaneAt splits a shell pane to the right of sedge's pane in the
// given cwd. Used by `o` on a project row when no worktree window is the
// target — the new pane lands inside sedge's own window.
func OpenShellPaneAt(sedgePaneID, cwd string) error {
	if sedgePaneID == "" {
		return errNoSedgePane
	}
	_, err := run("split-window", "-h", "-l", "40%", "-t", sedgePaneID, "-c", cwd)
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

// SelectWindow makes windowID the active window for the current tmux client.
// Used when activating a worktree: each worktree owns its own tmux window so
// activation is a window switch rather than a pane swap into sedge's window.
func SelectWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("window id empty")
	}
	_, err := run("select-window", "-t", windowID)
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

// OpenSubAgentViewer splits the worktree's claude pane vertically (below)
// and runs `sedge watch-agent <jsonlPath>` so the user can watch a sub-agent's
// conversation alongside the parent claude. Operates inside the worktree's
// own tmux window — caller does not need to be on that window first.
func OpenSubAgentViewer(wtPath, jsonlPath string) error {
	_, paneID, err := FindWorktreeWindow(wtPath)
	if err != nil {
		return err
	}
	if paneID == "" {
		return errNoActiveWorktree
	}
	selfPath, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = run("split-window", "-v", "-l", "40%", "-t", paneID, selfPath+" watch-agent "+jsonlPath)
	return err
}

// OpenCodePane opens a shell pane to the right of the worktree's claude
// pane, in the worktree's own tmux window, cwd'd at the worktree dir.
func OpenCodePane(wtPath string) error {
	if wtPath == "" {
		return errNoActiveWorktree
	}
	_, paneID, err := FindWorktreeWindow(wtPath)
	if err != nil {
		return err
	}
	if paneID == "" {
		return errNoActiveWorktree
	}
	_, err = run("split-window", "-h", "-l", "40%", "-t", paneID, "-c", wtPath)
	return err
}

var (
	errNoSedgePane      = newConstErr("sedge pane id unknown (TMUX_PANE not set)")
	errNoActiveWorktree = newConstErr("no active worktree; select one first")
)

type constErr string

func (c constErr) Error() string { return string(c) }
func newConstErr(s string) error { return constErr(s) }

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
