package tui

import "github.com/chrislcrain/sedge/internal/xdg"

func configPath() (string, error) {
	return xdg.ConfigFile()
}
