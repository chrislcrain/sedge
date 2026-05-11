package tmux

import "os"

// InsideTmux reports whether the current process is running inside a tmux pane.
func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// CurrentSession returns the name of the tmux session sedge is currently
// running inside, or empty string if not inside tmux.
func CurrentSession() (string, error) {
	if !InsideTmux() {
		return "", nil
	}
	return run("display-message", "-p", "#S")
}
