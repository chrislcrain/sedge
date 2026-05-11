package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)

	itemStyle = lipgloss.NewStyle().PaddingLeft(2)

	// selectedItemStyle wraps the cursor row in a darkened background that
	// spans the full pane width — neotree-style highlight, no leading arrow.
	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("231")). // near-white for contrast
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
			Foreground(lipgloss.Color("244")) // dim cool gray for idle background

	waitingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // amber — needs attention
			Bold(true).
			Blink(true)

	dormantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")) // dim gray

	newSessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")). // blue
			Italic(true)

	subAgentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141")). // soft violet — distinguishes sub-agent rows
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true).
			MarginTop(1)

	bannerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // amber, matches background dot
			Bold(true)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)
