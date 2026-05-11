package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// Resolve merges agent instruction files in hierarchical order and writes the
// merged result to a cache file. Returns the absolute path to the merged file.
//
// Layers (appended in order, missing files silently skipped):
//  1. ~/.sedge/AGENTS.md
//  2. ~/.sedge/AGENTS.local.md
//  3. <repoPath>/AGENTS.md
//  4. <repoPath>/AGENTS.local.md
func Resolve(repoPath, projectName, sessionName string) (string, error) {
	root, err := xdg.Root()
	if err != nil {
		return "", err
	}
	layers := []string{
		filepath.Join(root, "AGENTS.md"),
		filepath.Join(root, "AGENTS.local.md"),
		filepath.Join(repoPath, "AGENTS.md"),
		filepath.Join(repoPath, "AGENTS.local.md"),
	}

	var b strings.Builder
	for _, p := range layers {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "# === %s ===\n\n", p)
		b.Write(data)
	}

	if b.Len() == 0 {
		fmt.Fprintf(&b, "# === sedge built-in ===\n\n%s", DefaultAgentsMD)
	}

	cacheDir, err := xdg.PromptCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(cacheDir, fmt.Sprintf("%s-%s.md", projectName, sessionName))
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

// WriteDefaultGlobalIfMissing creates ~/.sedge/AGENTS.md from the embedded
// default if it doesn't already exist. Idempotent.
func WriteDefaultGlobalIfMissing() error {
	path, err := xdg.GlobalAgentsFile()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := xdg.EnsureDirs(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(DefaultAgentsMD), 0o644)
}
