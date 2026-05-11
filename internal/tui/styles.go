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

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			MarginTop(1)

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	heavyDividerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")). // medium gray, more visible
				Bold(true)

	lightDividerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("239"))

	treeBranchStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("242"))

	// dot indicators
	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")). // bright green
			Bold(true)

	backgroundStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // amber
			Bold(true)

	dormantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")) // dim gray

	newSessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")). // blue
			Italic(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true).
			MarginTop(1)
)
