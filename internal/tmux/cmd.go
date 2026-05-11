// Package tmux drives a tmux session that hosts sedge's TUI on the left and
// claude panes on the right.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// run shells out to tmux with args; returns trimmed stdout or a CombinedOutput error.
func run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
