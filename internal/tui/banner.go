package tui

import "strings"

// bannerLines is the rendered header. Kept narrow enough to fit a 28-col
// pane (sedge runs as a left pane around 25-30 cols wide).
var bannerLines = []string{
	"╭────────────────────────╮",
	"│                        │",
	"│   🦢  sedge  🪶       │",
	"│  ────────────────────  │",
	"│      tui · claude      │",
	"│                        │",
	"╰────────────────────────╯",
}

func renderBanner() string {
	var b strings.Builder
	for i, line := range bannerLines {
		b.WriteString(bannerStyle.Render(line))
		if i < len(bannerLines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// helpEntries lists the keybinding hints rendered vertically in the
// footer. Each entry is a (keys, description) pair.
var helpEntries = [][2]string{
	{"j/k", "navigate"},
	{"⏎  ", "expand · swap · new"},
	{"n  ", "add project"},
	{"o  ", "open code pane"},
	{"D  ", "delete (recycle)"},
	{"e  ", "edit config"},
	{"r  ", "reload"},
	{"q  ", "quit"},
}

func renderHelp() string {
	var b strings.Builder
	for i, entry := range helpEntries {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(helpKeyStyle.Render(entry[0]))
		b.WriteString(" ")
		b.WriteString(helpDescStyle.Render(entry[1]))
	}
	return b.String()
}
