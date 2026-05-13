// Package agentlog parses Claude Code's session JSONL files to surface
// in-flight sub-agent (Agent tool) invocations. sedge uses this to render
// ephemeral sub-rows under each worktree in the TUI, which clear themselves
// as soon as a tool_result for the matching tool_use_id arrives.
package agentlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// SubAgent is one in-flight Agent-tool call.
type SubAgent struct {
	ID          string    // tool_use id (toolu_XXX)
	Type        string    // subagent_type (e.g. "explorer", "Plan")
	Description string    // input.description
	StartedAt   time.Time // timestamp on the assistant message that launched it
}

// LatestJSONLMtime returns the most recent mtime across all *.jsonl files in
// the worktree's Claude project session dir, or the zero time if no files
// exist. sedge uses this as a "did claude do anything since I last viewed
// this worktree" signal — it's strictly more reliable than tmux's
// window_activity_flag because it only ticks on real claude turns/tool
// events, never on incidental terminal output.
func LatestJSONLMtime(wtPath string) time.Time {
	base := xdg.ClaudeProjectsDir()
	if base == "" {
		return time.Time{}
	}
	dir := filepath.Join(base, encode(wtPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mt := info.ModTime(); mt.After(latest) {
			latest = mt
		}
	}
	return latest
}

// ActiveSubAgents returns the list of Agent tool calls that have been
// launched in any session jsonl file under the worktree's claude project
// dir but do not yet have a matching tool_result. Ordered by start time.
func ActiveSubAgents(wtPath string) ([]SubAgent, error) {
	base := xdg.ClaudeProjectsDir()
	if base == "" {
		return nil, nil
	}
	dir := filepath.Join(base, encode(wtPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	active := map[string]SubAgent{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if err := scanFile(filepath.Join(dir, e.Name()), active); err != nil {
			return nil, err
		}
	}
	out := make([]SubAgent, 0, len(active))
	for _, sa := range active {
		out = append(out, sa)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// minimal subset of claude's jsonl record format we care about
type record struct {
	Type      string  `json:"type"`
	Timestamp string  `json:"timestamp"`
	Message   message `json:"message"`
}

type message struct {
	Role    string    `json:"role"`
	Content []content `json:"content"`
}

type content struct {
	Type       string                 `json:"type"`
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	Input      map[string]interface{} `json:"input,omitempty"`
}

func scanFile(path string, active map[string]SubAgent) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Allow large lines; some jsonl records are huge (full prompts).
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // skip malformed lines
		}
		for _, c := range r.Message.Content {
			switch c.Type {
			case "tool_use":
				if c.Name != "Agent" && c.Name != "Task" {
					continue
				}
				sa := SubAgent{
					ID:          c.ID,
					Type:        stringOf(c.Input, "subagent_type"),
					Description: stringOf(c.Input, "description"),
					StartedAt:   parseTime(r.Timestamp),
				}
				active[c.ID] = sa
			case "tool_result":
				delete(active, c.ToolUseID)
			}
		}
	}
	return sc.Err()
}

func stringOf(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// encode mirrors recycle.go's encodeClaudeProject.
func encode(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		switch r {
		case '/', '.', ' ', '~':
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
