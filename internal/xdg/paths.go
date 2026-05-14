// Package xdg resolves sedge's on-disk paths.
//
// The root directory defaults to ~/.sedge but can be overridden by setting
// the SEDGE_HOME environment variable (leading "~" is expanded).
package xdg

import (
	"os"
	"path/filepath"
)

// Root returns sedge's home directory: $SEDGE_HOME if set, otherwise
// $HOME/.sedge.
func Root() (string, error) {
	if h := os.Getenv("SEDGE_HOME"); h != "" {
		return Expand(h), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sedge"), nil
}

// DefaultWorktreesRoot returns the path used as the default for
// `worktrees_root` in config.toml when none is set. It tracks Root() so
// SEDGE_HOME flows through to fresh configs.
func DefaultWorktreesRoot() string {
	r, err := Root()
	if err != nil {
		return "~/.sedge/worktrees"
	}
	return filepath.Join(r, "worktrees")
}

// ClaudeProjectsDir returns the directory Claude Code uses for per-project
// session storage. It mirrors Claude Code's own resolution:
//
//   - $CLAUDE_CONFIG_DIR/projects when CLAUDE_CONFIG_DIR is set
//   - ~/.claude/projects otherwise
//
// Returns "" if neither can be resolved (no HOME, no env var). Sedge needs
// this to find a worktree's session JSONL files (for sub-agent tracking,
// --continue detection, and recycle).
func ClaudeProjectsDir() string {
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		return filepath.Join(cfg, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
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

func NameplateFile() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "nameplate.txt"), nil
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
