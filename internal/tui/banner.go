package tui

import (
	_ "embed"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/chrislcrain/sedge/internal/xdg"
)

//go:embed default_nameplate.txt
var defaultNameplate string

var (
	nameplateMu    sync.Mutex
	nameplateInit  bool
	nameplateLines []string
	nameplateMaxW  int
)

// loadNameplateOnce reads ~/.sedge/nameplate.txt if present, otherwise uses
// the embedded default. Result is cached for the process lifetime; call
// reloadNameplate to drop the cache (e.g. on the `r` reload key).
func loadNameplateOnce() {
	nameplateMu.Lock()
	defer nameplateMu.Unlock()
	if nameplateInit {
		return
	}
	nameplateInit = true
	raw := defaultNameplate
	if path, err := xdg.NameplateFile(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			raw = string(data)
		}
	}
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return
	}
	nameplateLines = strings.Split(raw, "\n")
	for _, line := range nameplateLines {
		if w := lipgloss.Width(line); w > nameplateMaxW {
			nameplateMaxW = w
		}
	}
}

// reloadNameplate drops the cached nameplate so the next call re-reads it.
func reloadNameplate() {
	nameplateMu.Lock()
	defer nameplateMu.Unlock()
	nameplateInit = false
	nameplateLines = nil
	nameplateMaxW = 0
}

// NameplateWidth returns the visual width of the widest row in the
// configured nameplate.
func NameplateWidth() int {
	loadNameplateOnce()
	return nameplateMaxW
}

// WriteDefaultNameplateIfMissing seeds ~/.sedge/nameplate.txt with the
// embedded default when no file exists yet. Idempotent.
func WriteDefaultNameplateIfMissing() error {
	path, err := xdg.NameplateFile()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := xdg.EnsureDirs(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultNameplate), 0o644)
}

func renderBanner() string {
	loadNameplateOnce()
	var b strings.Builder
	for i, line := range nameplateLines {
		b.WriteString(bannerStyle.Render(line))
		if i < len(nameplateLines)-1 {
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
