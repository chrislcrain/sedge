package tui

import tea "github.com/charmbracelet/bubbletea"

type keyAction int

const (
	actNone keyAction = iota
	actDown
	actUp
	actEnter
	actAdd
	actEdit
	actQuit
	actReload
)

func keyFor(msg tea.KeyMsg) keyAction {
	switch msg.String() {
	case "j", "down":
		return actDown
	case "k", "up":
		return actUp
	case "enter":
		return actEnter
	case "a":
		return actAdd
	case "e":
		return actEdit
	case "r":
		return actReload
	case "q", "ctrl+c", "esc":
		return actQuit
	}
	return actNone
}
