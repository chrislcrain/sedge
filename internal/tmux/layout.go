package tmux

import (
	"os/exec"
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

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
