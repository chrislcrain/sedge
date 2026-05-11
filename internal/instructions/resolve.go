package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrislcrain/sedge/internal/xdg"
)

// DelegationPolicy is the per-spawn parallelism/depth guidance prepended to
// the merged AGENTS.md. Mirrors Mux's maxParallelAgentTasks /
// maxTaskNestingDepth knobs but as a soft prompt-level constraint, since
// sedge can't intercept Claude Code's built-in Agent tool the way Mux does.
type DelegationPolicy struct {
	MaxParallel int
	MaxDepth    int
}

// Resolve merges agent instruction files in hierarchical order and writes the
// merged result to a cache file. Returns the absolute path to the merged file.
// If policy is non-zero, a delegation-policy preamble is prepended.
//
// Layers (appended in order, missing files silently skipped):
//  1. (preamble) sedge delegation policy
//  2. ~/.sedge/AGENTS.md
//  3. ~/.sedge/AGENTS.local.md
//  4. <repoPath>/AGENTS.md
//  5. <repoPath>/AGENTS.local.md
func Resolve(repoPath, projectName, sessionName string, policy DelegationPolicy) (string, error) {
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
	if preamble := policy.preamble(); preamble != "" {
		b.WriteString(preamble)
	}
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

	// Always include the built-in defaults if no global file existed and the
	// preamble alone would otherwise be the only content.
	hasGlobal := false
	for _, p := range layers[:2] {
		if _, err := os.Stat(p); err == nil {
			hasGlobal = true
			break
		}
	}
	if !hasGlobal {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
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

func (p DelegationPolicy) preamble() string {
	if p.MaxParallel <= 0 && p.MaxDepth <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# === sedge delegation policy ===\n\n")
	b.WriteString("You are the orchestrator for this sedge session. When delegating work via the Agent tool:\n\n")
	if p.MaxParallel > 0 {
		fmt.Fprintf(&b, "- Spawn at most **%d sub-agents in parallel**. If you need more, run them serially.\n", p.MaxParallel)
	}
	if p.MaxDepth > 0 {
		fmt.Fprintf(&b, "- Maximum delegation depth is **%d**. Sub-agents you spawn must not delegate further.\n", p.MaxDepth)
	}
	b.WriteString("- Sedge's built-in sub-agents (explorer, planner, reviewer, validator) are pre-configured to refuse recursion.\n")
	b.WriteString("- These are sedge harness limits, not Claude Code limits — honor them.\n")
	return b.String()
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
