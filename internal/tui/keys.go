package tui

import tea "github.com/charmbracelet/bubbletea"

type keyAction int

const (
	actNone keyAction = iota
	actDown
	actUp
	actEnter
	actEdit
	actReload
	actDelete     // capital D — delete worktree (only valid on rowWorktree)
	actAddProject // n — new project prompt
	actOpenCode   // o — spawn shell pane to the right of the claude slot pane
	actCleanExit  // capital X — kill all sedge windows and quit
	actMerge      // capital M — merge worktree back into source branch
	actQuit
)

func keyFor(msg tea.KeyMsg) keyAction {
	switch msg.String() {
	case "j", "down":
		return actDown
	case "k", "up":
		return actUp
	case "enter":
		return actEnter
	case "e":
		return actEdit
	case "r":
		return actReload
	case "D":
		return actDelete
	case "n":
		return actAddProject
	case "o":
		return actOpenCode
	case "X":
		return actCleanExit
	case "M":
		return actMerge
	case "q", "ctrl+c":
		// esc deliberately omitted: it's used to cancel prompts and we
		// don't want it to terminate sedge if the user pressed it while
		// the sedge pane unexpectedly held focus.
		return actQuit
	}
	return actNone
}
