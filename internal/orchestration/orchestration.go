// Package orchestration implements sedge's "W on a worktree" workflow: a
// planner claude interviews the user, writes a Plan to plan.json inside
// the worktree, and the user approves it in sedge. On approval sedge
// spawns one claude pane per session, each with a per-session system
// prompt baking in the task description, dependencies, and a file-based
// signaling protocol for inter-session coordination.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Plan is the structured output of the planner claude. The planner writes
// it to <wt>/.sedge/orchestration/plan.json; sedge polls for it, displays
// it for approval, and then spawns one worker per session.
type Plan struct {
	Name     string    `json:"name"`
	Summary  string    `json:"summary"`
	Sessions []Session `json:"sessions"`
}

// Session is one task in the plan, executed by a single worker claude
// pane. DependsOn lists other session ids whose `done` markers must
// exist before this session can begin.
type Session struct {
	ID        string   `json:"id"`
	Task      string   `json:"task"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// PlanPath returns the on-disk location of plan.json for a worktree.
func PlanPath(wtPath string) string {
	return filepath.Join(wtPath, ".sedge", "orchestration", "plan.json")
}

// DoneDir returns the directory the workers touch their `done/<id>`
// completion markers into. Workers read each other's markers here.
func DoneDir(wtPath string) string {
	return filepath.Join(wtPath, ".sedge", "orchestration", "done")
}

// LoadAt reads the plan file for a worktree. Returns (zero, mtime, err)
// on any failure; callers typically just check err.
func LoadAt(wtPath string) (Plan, time.Time, error) {
	path := PlanPath(wtPath)
	info, err := os.Stat(path)
	if err != nil {
		return Plan{}, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, time.Time{}, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, info.ModTime(), fmt.Errorf("parse plan.json: %w", err)
	}
	return p, info.ModTime(), nil
}

// Delete removes the plan file for a worktree. Used when the user
// rejects a plan or after workers are spawned, so a fresh `W` produces
// a fresh plan instead of re-firing the review modal on the stale one.
func Delete(wtPath string) error {
	if err := os.Remove(PlanPath(wtPath)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Validate reports the first structural problem with a plan that would
// prevent worker spawn, or nil if the plan is internally consistent.
func (p Plan) Validate() error {
	if len(p.Sessions) == 0 {
		return fmt.Errorf("plan has no sessions")
	}
	ids := make(map[string]bool, len(p.Sessions))
	for _, s := range p.Sessions {
		if s.ID == "" {
			return fmt.Errorf("session has empty id")
		}
		if ids[s.ID] {
			return fmt.Errorf("duplicate session id %q", s.ID)
		}
		ids[s.ID] = true
	}
	for _, s := range p.Sessions {
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("session %q depends on unknown session %q", s.ID, dep)
			}
			if dep == s.ID {
				return fmt.Errorf("session %q depends on itself", s.ID)
			}
		}
	}
	return nil
}

// WorkerPrompt returns the system prompt to give the claude running
// session `s` in the context of plan `p`. The prompt bakes in the task
// description, the wait-for-deps protocol, and the touch-done-marker
// completion protocol so workers coordinate without sedge mediation.
func WorkerPrompt(p Plan, s Session) string {
	var b strings.Builder
	b.WriteString("# === sedge orchestration worker ===\n\n")
	fmt.Fprintf(&b, "You are session **%s** in the orchestration *%s*.\n\n", s.ID, p.Name)
	if p.Summary != "" {
		b.WriteString("## Overall goal\n\n")
		b.WriteString(strings.TrimSpace(p.Summary))
		b.WriteString("\n\n")
	}
	b.WriteString("## Your task\n\n")
	b.WriteString(strings.TrimSpace(s.Task))
	b.WriteString("\n\n")
	if len(s.DependsOn) > 0 {
		b.WriteString("## Wait for dependencies\n\n")
		b.WriteString("Before doing *any* work, poll for these completion markers ")
		b.WriteString("to exist. Run a tight check loop (1-2 second sleep) and ")
		b.WriteString("only proceed once **all** are present:\n\n")
		for _, dep := range s.DependsOn {
			fmt.Fprintf(&b, "- `./.sedge/orchestration/done/%s` (from session `%s`)\n", dep, dep)
		}
		b.WriteString("\nSuggested first action: a Bash command like\n\n")
		b.WriteString("```\nwhile ! ")
		conds := make([]string, 0, len(s.DependsOn))
		for _, dep := range s.DependsOn {
			conds = append(conds, fmt.Sprintf("[ -e ./.sedge/orchestration/done/%s ]", dep))
		}
		b.WriteString(strings.Join(conds, " && "))
		b.WriteString("; do sleep 2; done\n```\n\n")
	} else {
		b.WriteString("## Dependencies\n\n")
		b.WriteString("None — you can start immediately.\n\n")
	}
	b.WriteString("## Signalling completion\n\n")
	fmt.Fprintf(&b, "When your task is fully complete (tests passing, code committed, "+
		"whatever the acceptance criteria are), run:\n\n"+
		"```\nmkdir -p ./.sedge/orchestration/done && touch ./.sedge/orchestration/done/%s\n```\n\n", s.ID)
	b.WriteString("Sibling sessions that depend on you are polling for that file.\n\n")
	b.WriteString("## Constraints\n\n")
	b.WriteString("- Stay inside this worktree. Don't push, merge, or modify other worktrees.\n")
	b.WriteString("- Coordinate via the `done/<id>` markers — don't ask the user to wait or signal manually.\n")
	b.WriteString("- If a dependency hasn't finished after a long time (>10 min), report it to the user and stop polling.\n")
	return b.String()
}
