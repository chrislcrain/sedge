package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/chrislcrain/sedge/internal/hookstate"
)

// cmdHook is invoked by claude-code on each hook event. It reads the stdin
// payload, plus the event name from argv, and writes a one-line state file
// the TUI consumes. Designed to be cheap to invoke — claude blocks on hook
// execution, so we keep this tight.
func cmdHook() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <event-name>",
		Short:  "Internal: record a claude-code hook event for the TUI",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(_ *cobra.Command, args []string) error {
			event := hookstate.Event(args[0])
			payload, _ := io.ReadAll(os.Stdin)
			var in claudeHookInput
			_ = json.Unmarshal(payload, &in) // empty payload is fine

			wtPath := in.CWD
			if wtPath == "" {
				wtPath = os.Getenv("CLAUDE_PROJECT_DIR")
			}
			if wtPath == "" {
				// Without a worktree to attribute this to, there's nothing
				// useful to record. Exit cleanly so claude doesn't see an
				// error from a missing field.
				return nil
			}
			return hookstate.Write(wtPath, hookstate.State{
				Event:     event,
				At:        time.Now(),
				ToolName:  in.ToolName,
				SessionID: in.SessionID,
			})
		},
	}
}

// claudeHookInput is the subset of the hook stdin payload sedge reads.
// claude-code passes a JSON object with at least these keys for each event.
type claudeHookInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
}

// hookEvents enumerates every claude-code event sedge subscribes to. Order
// matters only for deterministic test output.
var hookEvents = []hookstate.Event{
	hookstate.EventSessionStart,
	hookstate.EventSessionEnd,
	hookstate.EventUserPromptSubmit,
	hookstate.EventPreToolUse,
	hookstate.EventPostToolUse,
	hookstate.EventNotification,
	hookstate.EventStop,
	hookstate.EventSubagentStop,
}

// cmdInstallHooks merges sedge's hook entries into the user's claude-code
// settings.json. Idempotent — re-running it is safe and adds nothing new
// once the entries are already present.
func cmdInstallHooks() *cobra.Command {
	return &cobra.Command{
		Use:   "install-hooks",
		Short: "Register sedge as a claude-code hook so the TUI can show real-time agent state",
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := claudeSettingsPath()
			if err != nil {
				return err
			}
			settings, err := loadJSONObject(path)
			if err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			changed := injectSedgeHooks(settings, self)
			if !changed {
				fmt.Println("sedge hooks already installed in", path)
				return nil
			}
			if err := saveJSONObject(path, settings); err != nil {
				return err
			}
			fmt.Println("installed sedge hooks into", path)
			return nil
		},
	}
}

// claudeSettingsPath returns the claude-code settings file path, mirroring
// claude-code's own resolution ($CLAUDE_CONFIG_DIR/settings.json or
// ~/.claude/settings.json).
func claudeSettingsPath() (string, error) {
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		return filepath.Join(cfg, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// loadJSONObject reads path as a JSON object, returning an empty map if the
// file does not exist yet.
func loadJSONObject(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m, nil
}

// saveJSONObject writes m to path with two-space indentation, atomic rename.
func saveJSONObject(path string, m map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sedge-settings-*.json.tmp")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// injectSedgeHooks ensures `settings["hooks"][event]` contains an entry that
// invokes `<self> hook <event>`. Returns true if any new entry was added.
//
// The schema mirrors plugin hook configs we found in the wild:
//
//	{"hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "..."}]}], ...}}
func injectSedgeHooks(settings map[string]interface{}, self string) bool {
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	changed := false
	for _, event := range hookEvents {
		want := fmt.Sprintf("%s hook %s", quoteIfNeeded(self), event)
		entries, _ := hooks[string(event)].([]interface{})
		if hasSedgeEntry(entries, want) {
			continue
		}
		entries = append(entries, map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": want,
				},
			},
		})
		hooks[string(event)] = entries
		changed = true
	}
	if changed {
		// Keep keys deterministic so re-runs and diffs are sane.
		settings["hooks"] = sortMapKeys(hooks)
	}
	return changed
}

// hasSedgeEntry reports whether one of the existing entries already invokes
// our hook command — looks for the substring " hook <event>" so we recognise
// our installation regardless of which `self` path was used.
func hasSedgeEntry(entries []interface{}, wantCmd string) bool {
	for _, entry := range entries {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		inner, _ := em["hooks"].([]interface{})
		for _, h := range inner {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			cmd, _ := hm["command"].(string)
			if cmd == wantCmd {
				return true
			}
			if strings.Contains(cmd, " hook ") && strings.HasSuffix(strings.TrimSpace(cmd), strings.TrimSpace(wantCmd[strings.Index(wantCmd, " hook "):])) {
				return true
			}
		}
	}
	return false
}

// quoteIfNeeded shell-quotes self if it contains characters that would be
// re-interpreted when claude execs the command via a shell.
func quoteIfNeeded(self string) string {
	if strings.ContainsAny(self, " \t\"'$") {
		return "'" + strings.ReplaceAll(self, "'", `'\''`) + "'"
	}
	return self
}

// sortMapKeys returns a copy of m with keys iterated deterministically.
// Go maps already serialise to JSON in key-sorted order via json.Marshal, so
// this is just defence in depth for any future schema printing.
func sortMapKeys(m map[string]interface{}) map[string]interface{} {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]interface{}, len(m))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
