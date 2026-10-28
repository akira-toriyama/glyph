package testutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFixtureCommitsSkipTheCommitMsgAndPreCommitHooks: Commit, CommitFrom and
// Touch commit into repositories a test may have installed glyph's hooks into,
// where the commit-msg hook runs whatever `glyph` is first on the developer's
// PATH — a binary the test did not build. The planted pre-commit hook records
// that it ran and passes; the planted commit-msg hook records that it ran and
// refuses. The positive control comes first: a plain `git commit` under
// GitEnv in the same repository must be refused with both hooks on record, so
// git reaches them both — a GitEnv that steered git away from them
// (core.hooksPath at a missing directory), or a commit-msg hook git would not
// run, would otherwise pass the helpers with no hook in reach. Each helper must
// then land its commit with neither hook on record.
func TestFixtureCommitsSkipTheCommitMsgAndPreCommitHooks(t *testing.T) {
	dir := NewRepo(t)
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", hooks, err)
	}
	ran := filepath.Join(t.TempDir(), "ran")
	for name, exit := range map[string]int{"pre-commit": 0, "commit-msg": 1} {
		script := fmt.Sprintf("#!/bin/sh\necho %s >>'%s'\nexit %d\n", name, ran, exit)
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write the %s hook: %v", name, err)
		}
	}

	before := commitCount(t, dir)
	plain := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", ":bug:~ fix a crash")
	plain.Env = GitEnv("akira-toriyama")
	if out, err := plain.CombinedOutput(); err == nil {
		t.Fatalf("a plain git commit landed past a commit-msg hook that refuses every commit — git never ran it, so the helpers below would pass with no hook in reach:\n%s", out)
	}
	if got := commitCount(t, dir); got != before {
		t.Fatalf("HEAD carries %d commits after the refused plain commit, want %d", got, before)
	}
	if got := hooksRan(t, ran); got != "pre-commit commit-msg" {
		t.Fatalf("the refused plain commit ran %q, want both planted hooks, pre-commit then commit-msg", got)
	}

	for _, tc := range []struct {
		name   string
		commit func(t *testing.T)
	}{
		{"Commit", func(t *testing.T) { Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash") }},
		{"CommitFrom", func(t *testing.T) { CommitFrom(t, dir, "Saleh", "root@lr0.org", ":bug:~ fix the parser") }},
		{"Touch", func(t *testing.T) { Touch(t, dir, "akira-toriyama", ":memo:= note the season", "notes.md") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(ran); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("reset the hook record: %v", err)
			}
			before := commitCount(t, dir)
			tc.commit(t)
			if got := commitCount(t, dir); got != before+1 {
				t.Errorf("HEAD carries %d commits after %s, want %d", got, tc.name, before+1)
			}
			if got := hooksRan(t, ran); got != "" {
				t.Errorf("%s ran %q, want neither planted hook", tc.name, got)
			}
		})
	}
}

// hooksRan is the planted hooks' record in the order they ran, "" when none did.
func hooksRan(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read the hook record: %v", err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// commitCount is the number of commits HEAD reaches in dir.
func commitCount(t *testing.T, dir string) int {
	t.Helper()
	out := Git(t, dir, "akira-toriyama", "rev-list", "--count", "HEAD")
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("rev-list --count printed %q: %v", out, err)
	}
	return n
}
