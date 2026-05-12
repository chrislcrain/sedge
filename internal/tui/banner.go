package tui

import "strings"

// bannerLines is a pure-ASCII nameplate with a double (=== / |) border.
// Each visible row is exactly 30 columns wide. Inner art is figlet "small"
// style for "sedge" — all five letters.
var bannerLines = []string{
	"+============================+",
	"|                            |",
	"|              _             |",
	"|   ___ ___ __| | __ _   ___ |",
	"|  (_-</ -_) _` |/ _` |/ -_) |",
	"|  /__/\\___\\__,_|\\__, |\\___| |",
	"|                |___/       |",
	"|                            |",
	"+============================+",
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
	{"o  ", "shell @ project / worktree"},
	{"D  ", "delete (recycle)"},
	{"P  ", "push + open PR"},
	{"X  ", "kill all · quit"},
	{"e  ", "edit config"},
	{"r  ", "reload"},
	{"q  ", "quit (leave bg)"},
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
