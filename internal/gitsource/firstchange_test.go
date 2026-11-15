package gitsource

import (
	"context"
	"fmt"
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

// TestFirstChangeAtAShallowBoundaryIsUnreadable: a shallow clone's boundary
// is no root, and its whole tree is not its diff (DESIGN §4.1, "Where the
// files come from"; diffTreeFiles answers it unreadable). git prints no %P
// for it and reads every path its tree holds as changed there, so the
// oldest commit touching haiku within a --depth 3 clone IS the boundary —
// read as a parentless first change, it offered the single line's sentence
// for a line whose real first change lies past the clone (review of #361,
// measured 2026-11-08). firstChange answers the same error diffTreeFiles
// does; a true root inside the depth still answers as a root (the control:
// a --depth clone deep enough to hold the root). Mutation row
// gitsource-first-change-reads-a-shallow-boundary-as-a-root.
func TestFirstChangeAtAShallowBoundaryIsUnreadable(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	added := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(haiku)^ add haiku", "haiku/h.go")
	for i := range 4 {
		testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
	}
	clone := func(depth int) string {
		dst := filepath.Join(t.TempDir(), "shallow")
		testutil.Git(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", fmt.Sprint(depth), "file://"+dir, dst)
		return dst
	}
	ctx := context.Background()

	shallow := clone(3)
	boundary := testutil.Git(t, shallow, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
	if boundary == added || boundary == root {
		t.Fatalf("control: the depth-3 clone's boundary %.7s must be a commit after haiku's first change %.7s", boundary, added)
	}
	sha, parents, err := firstChange(ctx, shallow, "HEAD", []string{":(top)haiku"})
	if !IsShallowBoundary(err) {
		t.Fatalf("haiku's first change within the clone = %.7s (%d parent(s)), %v; want the shallow-boundary error — haiku was added by %.7s, outside the clone, and the boundary's own diff is unreadable", sha, parents, err, added)
	}
	if !strings.Contains(err.Error(), "commit "+boundary[:7]+" is a shallow clone's boundary") {
		t.Errorf("the error must name the boundary as DiffTreeFiles does:\n%s", err)
	}

	// A path the boundary's tree does not hold has its first change inside
	// the clone, with a parent: the boundary is not in its way.
	later := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(curry)^ add curry", "curry/c.go")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= note 4")
	shallow = clone(3)
	if sha, parents, err = firstChange(ctx, shallow, "HEAD", []string{":(top)curry"}); err != nil || sha != later || parents != 1 {
		t.Errorf("curry, added inside the clone = %.7s, %d, %v; want %.7s with 1 parent", sha, parents, err, later)
	}

	// Control: a true root inside the depth is a root. The whole history is
	// 8 commits; depth 20 holds it all and .git/shallow lists the root.
	deep := clone(20)
	if sha, parents, err = firstChange(ctx, deep, "HEAD", []string{":(top)", ":(top,exclude)haiku", ":(top,exclude)curry"}); err != nil || sha != root || parents != 0 {
		t.Errorf("the whole tree minus the lines in a clone holding the root = %.7s, %d, %v; want the root %.7s with no parent and no error", sha, parents, err, root)
	}
}
