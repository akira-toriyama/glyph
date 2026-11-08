package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestSinceTagPackagesRootFirstChangeNamesNoBaseline: a baseline is cut at
// the commit BEFORE a line's first change, so a line whose first change is
// the root commit has nowhere to put one — "cut travel/v0.0.0 at the commit
// before that line's first change" named a commit that does not exist
// (t-vc5w, measured 2026-11-04 on glyph-monorepo-test-2 `release --dry-run`,
// both lines added by root 8da42f0). Such a line gets the single line's
// sentence: the walk proceeds over the whole history, no tag to cut. With a
// later-declared third line the remedy returns, for that line alone, and
// the root lines are still not offered one. Mutation row
// packages-baseline-named-at-a-root-first-change.
func TestSinceTagPackagesRootFirstChangeNamesNoBaseline(t *testing.T) {
	dir := testutil.NewTwoLineRepo(t)
	first := testutil.Git(t, dir, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "reset", "-q", "--soft", first)
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":tada:= begin with two lines")
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(root): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 1 {
		t.Fatalf("bump --since-tag exited %d, want 1 (a root commit of one = moves nothing)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "::warning::glyph: no version tag on the haiku/, curry/ line(s) in HEAD's history — walking the repository's whole history (1 commit(s) the walk will ask GitHub about)\n") {
		t.Fatalf("a root first change gets the single line's sentence, the walk proceeding and nothing else:\n%s", stderr)
	}
	if strings.Contains(stderr, "cut ") || strings.Contains(stderr, "v0.0.0") {
		t.Fatalf("no baseline can be cut before the root commit, and the warning names one:\n%s", stderr)
	}

	// A line declared later has a first change with a parent: its remedy is
	// named, the root lines' still are not.
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \"fish\"\n")
	testutil.WriteFile(t, dir, "fish/fish.go", "package fish\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:(fish)^ declare a third line")
	declared := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(root): `[]`, commitPullsPath(declared): `[]`}))

	code, stdout, stderr = runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "; to start from a tag instead, cut fish/v0.0.0 at the commit before that line's first change") {
		t.Fatalf("the later-declared line's baseline exists and must be named:\n%s", stderr)
	}
	if strings.Contains(stderr, "haiku/v0.0.0") || strings.Contains(stderr, "curry/v0.0.0") {
		t.Fatalf("the root lines still have no commit to cut a baseline at:\n%s", stderr)
	}
}
