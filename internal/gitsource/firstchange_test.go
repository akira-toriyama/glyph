package gitsource

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestFirstChangeReadsTheOldestCommitTouchingThePathspec: the baseline a
// packages walk names is cut at the commit BEFORE a line's first change, so
// the walk asks git for that change and its parent count (walk.baselineOn).
// Three things are measured rather than assumed. `:(top)` reads the path
// from the top level whatever the working directory, so the answer from a
// subdirectory is the answer from the root. `:(top,exclude)` takes a nested
// package out of its parent, so travel's first change is its own file and not
// onsen's. And the parent count is the object's own: git's --parents would
// have rewritten it to the simplified history's and called every oldest
// touching commit a root (the control: t1 has a parent, and only the root
// commit, asked over the whole tree, has none).
func TestFirstChangeReadsTheOldestCommitTouchingThePathspec(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	o1 := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(onsen)^ open the onsen", "travel/onsen/o.md")
	t1 := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(travel)^ plan a trip", "travel/t.md")
	testutil.Touch(t, dir, "akira-toriyama", ":bug:(travel)~ fix the plan", "travel/t.md")
	ctx := context.Background()

	sha, parents, err := firstChange(ctx, dir, "HEAD", []string{":(top)travel", ":(top,exclude)travel/onsen"})
	if err != nil || sha != t1 || parents != 1 {
		t.Fatalf("travel minus onsen = %s, %d parent(s), %v; want t1 %s with 1 parent — the nested package's commit is not its parent's first change, and the parent count is the object's", sha, parents, err, t1)
	}
	sha, parents, err = firstChange(ctx, dir, "HEAD", []string{":(top)travel/onsen"})
	if err != nil || sha != o1 || parents != 1 {
		t.Fatalf("travel/onsen = %s, %d, %v; want o1 %s with 1 parent", sha, parents, err, o1)
	}
	sha, parents, err = firstChange(ctx, filepath.Join(dir, "travel", "onsen"), "HEAD", []string{":(top)travel", ":(top,exclude)travel/onsen"})
	if err != nil || sha != t1 || parents != 1 {
		t.Fatalf("from a subdirectory = %s, %d, %v; want the same t1 — :(top) must not read the path relative to the working directory", sha, parents, err)
	}
	sha, parents, err = firstChange(ctx, dir, "HEAD", []string{":(top)", ":(top,exclude)travel"})
	if err != nil || sha != root || parents != 0 {
		t.Fatalf("the whole tree minus travel = %s, %d, %v; want the root commit %s with no parent", sha, parents, err, root)
	}
	sha, parents, err = firstChange(ctx, dir, "HEAD", []string{":(top)fish"})
	if err != nil || sha != "" || parents != 0 {
		t.Fatalf("a path no commit touches = %q, %d, %v; want no commit and no error", sha, parents, err)
	}
	// Control for the parent-count claim: --parents rewrites to the
	// simplified history, so the same pathspec reports t1 as a root there.
	out := testutil.Git(t, dir, "akira-toriyama", "rev-list", "--reverse", "--parents", "HEAD", "--", ":(top)travel", ":(top,exclude)travel/onsen")
	if first, _, _ := strings.Cut(out, "\n"); strings.TrimSpace(first) != t1 {
		t.Fatalf("control: rev-list --parents over the same pathspec reports t1 as %q — the rewritten parent list is what firstChange must not read", first)
	}
}
