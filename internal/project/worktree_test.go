package project

import "testing"

func TestWorktreePath(t *testing.T) {
	p := Project{Name: "myrepo", Path: "/home/me/code/myrepo"}
	got := WorktreePath("/tmp/wt", p, "s100")
	want := "/tmp/wt/myrepo/s100"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBranchName(t *testing.T) {
	if got := BranchName("s200"); got != "sedge/s200" {
		t.Errorf("got %q, want sedge/s200", got)
	}
}

func TestResolvedDefaults(t *testing.T) {
	cases := []struct {
		p             Project
		fallbackIso   string
		wantIso       string
		wantBranchDef string
	}{
		{Project{}, "", "worktree", "main"},
		{Project{Isolation: "inplace"}, "worktree", "inplace", "main"},
		{Project{DefaultBranch: "develop"}, "", "worktree", "develop"},
	}
	for i, c := range cases {
		if got := c.p.ResolvedIsolation(c.fallbackIso); got != c.wantIso {
			t.Errorf("case %d isolation: got %q want %q", i, got, c.wantIso)
		}
		if got := c.p.ResolvedDefaultBranch(); got != c.wantBranchDef {
			t.Errorf("case %d branch: got %q want %q", i, got, c.wantBranchDef)
		}
	}
}
