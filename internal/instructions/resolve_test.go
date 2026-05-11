package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLayersAndAppends(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(tmp, ".sedge", "AGENTS.md"), "GLOBAL\n")
	mustWrite(t, filepath.Join(tmp, ".sedge", "AGENTS.local.md"), "GLOBAL-LOCAL\n")
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "REPO\n")
	mustWrite(t, filepath.Join(repo, "AGENTS.local.md"), "REPO-LOCAL\n")

	out, err := Resolve(repo, "proj", "sess1", DelegationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"GLOBAL", "GLOBAL-LOCAL", "REPO", "REPO-LOCAL"} {
		if !strings.Contains(s, want) {
			t.Errorf("merged output missing %q\n---\n%s", want, s)
		}
	}
	if idx := strings.Index(s, "GLOBAL\n"); idx == -1 || idx > strings.Index(s, "REPO\n") {
		t.Error("expected GLOBAL to appear before REPO")
	}
}

func TestResolveMissingFilesYieldsBuiltin(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Resolve(repo, "proj", "sess1", DelegationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sedge built-in") {
		t.Errorf("expected built-in fallback marker; got: %s", string(data))
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
