package tmux

import (
	"os/exec"
	"strings"
)

// SpawnClaudeOpts is everything SpawnClaudePane needs.
type SpawnClaudeOpts struct {
	WorktreeDir     string   // cwd for the new pane
	ProjectPath     string   // passed to --add-dir
	PromptFile      string   // passed to --append-system-prompt-file
	SessionName     string   // passed to claude -n
	PermissionMode  string   // claude --permission-mode value
	Model           string   // optional: claude --model value
	ExtraClaudeArgs []string // extra args appended to the claude invocation
	Env             []string // additional KEY=VAL pairs (currently unused; tmux env handling is per-session)
	AgentsJSON      string   // if non-empty, passed via `claude --agents <json>` so the orchestrator can delegate
}

// SpawnClaudePane splits the current tmux window horizontally and starts claude
// in the new pane. Returns the pane id of the new pane.
func SpawnClaudePane(opts SpawnClaudeOpts) (string, error) {
	claudeArgs := []string{
		"claude",
		"--permission-mode", opts.PermissionMode,
		"--add-dir", opts.ProjectPath,
		"--append-system-prompt-file", opts.PromptFile,
		"-n", opts.SessionName,
	}
	if opts.Model != "" {
		claudeArgs = append(claudeArgs, "--model", opts.Model)
	}
	if strings.TrimSpace(opts.AgentsJSON) != "" {
		claudeArgs = append(claudeArgs, "--agents", opts.AgentsJSON)
	}
	claudeArgs = append(claudeArgs, opts.ExtraClaudeArgs...)

	args := []string{
		"split-window",
		"-h",
		"-p", "75",
		"-c", opts.WorktreeDir,
		"-P", "-F", "#{pane_id}",
	}
	args = append(args, claudeArgs...)

	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return trimNewline(string(out)), nil
}

// LivePaths returns the set of pane_current_path values across all tmux panes.
// Used by the TUI to decide which worktrees have an active claude session.
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

// PaneRef identifies a specific tmux pane within a session.
type PaneRef struct {
	PaneID    string // e.g. "%17"
	WindowID  string // e.g. "@4"
	Session   string // e.g. "sedge"
	CurPath   string // pane_current_path
}

// FindPaneByCwd looks across all tmux panes for one whose current working
// directory is exactly `cwd`. Returns nil if no match.
func FindPaneByCwd(cwd string) (*PaneRef, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_id}|#{window_id}|#{session_name}|#{pane_current_path}").Output()
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		if parts[3] == cwd {
			return &PaneRef{
				PaneID:   parts[0],
				WindowID: parts[1],
				Session:  parts[2],
				CurPath:  parts[3],
			}, nil
		}
	}
	return nil, nil
}

// FocusPane moves the active selection to the given pane. Switches the client
// to the right session/window first if needed.
func FocusPane(p PaneRef) error {
	if _, err := run("switch-client", "-t", p.Session+":"+p.WindowID); err != nil {
		// If switch-client fails (e.g. not driving a client), fall back to
		// select-window + select-pane on whatever client is attached.
		if _, err := run("select-window", "-t", p.Session+":"+p.WindowID); err != nil {
			return err
		}
	}
	_, err := run("select-pane", "-t", p.PaneID)
	return err
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
