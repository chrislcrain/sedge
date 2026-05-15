// Package hookstate persists per-worktree claude-code hook events so the
// sedge TUI can render an indicator driven by real events (PreToolUse,
// Notification, Stop, …) rather than by polling JSONL session files.
//
// Two callers:
//
//   - `sedge hook <event>` writes to a state file each time claude-code
//     fires the corresponding hook.
//   - The TUI reads the state file every refresh tick to classify the
//     worktree's current activity (in-flight, awaiting approval, idle).
package hookstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// Event is one of claude-code's hook event names. We only special-case the
// few that map to UI state; everything else is recorded verbatim and
// classified as "noise".
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventSessionEnd       Event = "SessionEnd"
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventNotification     Event = "Notification"
	EventStop             Event = "Stop"
	EventSubagentStop     Event = "SubagentStop"
	EventPreCompact       Event = "PreCompact"
)

// State is the latest hook fact for a worktree. Persisted as JSON.
type State struct {
	Event     Event     `json:"event"`
	At        time.Time `json:"at"`
	ToolName  string    `json:"tool_name,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

// Write atomically replaces the state file for wtPath with s. Used by the
// `sedge hook` subcommand. Returns an error if the state dir is unreachable;
// callers may swallow it since a missing write only delays UI updates.
func Write(wtPath string, s State) error {
	dir, err := xdg.HookStateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, encode(wtPath)+".json")
	tmp, err := os.CreateTemp(dir, ".sedge-hook-*.json.tmp")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
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

// Read returns the most recent hook event recorded for wtPath, or the zero
// value if none has been written yet (returns ok=false).
func Read(wtPath string) (State, bool) {
	dir, err := xdg.HookStateDir()
	if err != nil {
		return State{}, false
	}
	path := filepath.Join(dir, encode(wtPath)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false
	}
	return s, true
}

// Activity collapses a State into one of three TUI-facing buckets, applying
// a staleness threshold so a never-cleared "in flight" doesn't blink
// forever if claude crashed before emitting Stop.
type Activity int

const (
	ActivityIdle Activity = iota
	ActivityInFlight
	ActivityApprovalPending
)

// Classify maps the recorded state to UI activity. Pure function — easy to
// unit-test if we ever feel the urge.
//
// staleAfter caps how long a non-terminal event keeps its "in flight" tag.
// Past this, we assume the session died and treat it as idle. When
// staleAfter <= 0 the demotion is disabled entirely (per SPEC §4.5,
// `hook_stale_minutes = 0`): the recorded event always wins regardless of
// how long it has been on disk. Callers thread this in from config.toml's
// `hook_stale_minutes` (default 10m).
func Classify(s State, staleAfter time.Duration) Activity {
	if s.Event == "" {
		return ActivityIdle
	}
	if staleAfter > 0 && time.Since(s.At) > staleAfter {
		return ActivityIdle
	}
	switch s.Event {
	case EventNotification:
		return ActivityApprovalPending
	case EventUserPromptSubmit, EventPreToolUse, EventPostToolUse, EventSubagentStop:
		return ActivityInFlight
	case EventStop, EventSessionEnd, EventSessionStart, EventPreCompact:
		return ActivityIdle
	}
	return ActivityIdle
}

// encode mirrors the project-path encoding used elsewhere in sedge so the
// state file name corresponds 1:1 with the worktree path.
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
