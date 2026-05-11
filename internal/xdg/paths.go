// Package xdg resolves sedge's on-disk paths under ~/.sedge.
package xdg

import (
	"os"
	"path/filepath"
)

func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sedge"), nil
}

func ConfigFile() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "config.toml"), nil
}

func GlobalAgentsFile() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "AGENTS.md"), nil
}

func GlobalAgentsLocalFile() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "AGENTS.local.md"), nil
}

func WorktreesRoot() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "worktrees"), nil
}

func PromptCacheDir() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "cache", "prompts"), nil
}

func EnsureDirs() error {
	r, err := Root()
	if err != nil {
		return err
	}
	for _, sub := range []string{"", "worktrees", filepath.Join("cache", "prompts")} {
		if err := os.MkdirAll(filepath.Join(r, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Expand resolves a leading "~" to the user's home directory.
func Expand(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if len(path) == 1 {
		return home
	}
	if path[1] == '/' || path[1] == filepath.Separator {
		return filepath.Join(home, path[2:])
	}
	return path
}
