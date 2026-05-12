package agentlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// FindAgentFile locates the agent JSONL file for a sub-agent in a worktree.
// Matching is by description text (the only stable user-visible key in the
// .meta.json that we can correlate with the parent's tool_use.input.description).
// If multiple files share a description, the most recently modified one wins.
// Returns ("", nil) if nothing matches.
func FindAgentFile(wtPath, description string) (string, error) {
	root := xdg.ClaudeProjectsDir()
	if root == "" {
		return "", nil
	}
	base := filepath.Join(root, encode(wtPath))
	sessionDirs, err := filepath.Glob(filepath.Join(base, "*"))
	if err != nil {
		return "", err
	}

	type candidate struct {
		path string
		mod  time.Time
	}
	var matches []candidate
	for _, sd := range sessionDirs {
		info, err := os.Stat(sd)
		if err != nil || !info.IsDir() {
			continue
		}
		metaFiles, _ := filepath.Glob(filepath.Join(sd, "subagents", "*.meta.json"))
		for _, mf := range metaFiles {
			data, err := os.ReadFile(mf)
			if err != nil {
				continue
			}
			var m struct {
				Description string `json:"description"`
			}
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if m.Description != description {
				continue
			}
			jsonlPath := mf[:len(mf)-len(".meta.json")] + ".jsonl"
			st, err := os.Stat(jsonlPath)
			if err != nil {
				continue
			}
			matches = append(matches, candidate{path: jsonlPath, mod: st.ModTime()})
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].mod.After(matches[j].mod) })
	return matches[0].path, nil
}
