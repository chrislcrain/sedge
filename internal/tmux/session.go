package tmux

import (
	"os"
	"os/exec"
	"syscall"
)

const DefaultSessionName = "sedge"

// HasSession reports whether a tmux session with the given name exists.
func HasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// SpawnAndAttach creates a detached tmux session named `name` whose first pane
// runs `selfPath --inside-tmux`, then replaces the current process with
// `tmux attach-session -t name`.
func SpawnAndAttach(name, selfPath string) error {
	if !HasSession(name) {
		if _, err := run("new-session", "-d", "-s", name, "-n", "sedge", selfPath+" --inside-tmux"); err != nil {
			return err
		}
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	return syscall.Exec(tmuxBin, []string{"tmux", "attach-session", "-t", name}, os.Environ())
}
