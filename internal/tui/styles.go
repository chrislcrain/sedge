package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)

	itemStyle = lipgloss.NewStyle().PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(lipgloss.Color("170")).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			MarginTop(1)

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))

	busyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")). // green
			Bold(true)

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")) // dim gray

	newSessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")). // blue
			Italic(true)
)
