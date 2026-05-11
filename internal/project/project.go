// Package project models registered repos and their on-disk state.
package project

type Project struct {
	Name          string            `toml:"name"`
	Path          string            `toml:"path"`
	DefaultBranch string            `toml:"default_branch,omitempty"`
	Isolation     string            `toml:"isolation,omitempty"` // "worktree" | "inplace"
	Env           map[string]string `toml:"env,omitempty"`
}

func (p Project) ResolvedIsolation(fallback string) string {
	if p.Isolation != "" {
		return p.Isolation
	}
	if fallback != "" {
		return fallback
	}
	return "worktree"
}

func (p Project) ResolvedDefaultBranch() string {
	if p.DefaultBranch != "" {
		return p.DefaultBranch
	}
	return "main"
}
