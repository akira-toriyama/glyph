package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins the baseline remedy of the whole-history warning (DESIGN
// §4.1, the priced remedy): offered only where a commit precedes the line's
// first change, over the line's OWN files.

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

// nestedLinesRepo declares travel and travel/onsen, the root commit adding
// glyph.toml and onsen's file, a second commit adding travel's own: onsen's
// first change is the root, travel's is the second commit.
func nestedLinesRepo(t *testing.T) (dir, root, plan string) {
	t.Helper()
	dir = testutil.NewRepo(t)
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \"travel\"\n\n[[packages]]\npath = \"travel/onsen\"\n")
	testutil.WriteFile(t, dir, "travel/onsen/o.md", "onsen\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":tada:= open the onsen")
	root = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	plan = testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(travel)^ plan a trip", "travel/t.md")
	return dir, root, plan
}

// travelAloneIsBaselined runs bump --since-tag in the working directory and
// asserts the nestedLinesRepo answer: travel's own first change has a
// parent, onsen's is the root.
func travelAloneIsBaselined(t *testing.T) {
	t.Helper()
	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "; to start from a tag instead, cut travel/v0.0.0 at the commit before that line's first change") {
		t.Errorf("travel's own first change has a parent, so its baseline is named:\n%s", stderr)
	}
	if strings.Contains(stderr, "travel/onsen/v0.0.0") {
		t.Errorf("onsen's first change is the root commit, so no baseline can precede it:\n%s", stderr)
	}
}

// TestSinceTagPackagesNestedPackageIsNotItsParentsFirstChange: a nested
// package's files are taken out of its parent's (DESIGN §4.1, rule 1), so
// the root commit that opened travel/onsen is not travel's first change —
// travel's is the later commit adding travel/t.md, which has a parent, and
// travel is offered a baseline where onsen is not. With the exclusion gone,
// travel's "first change" is the root and the remedy for a line that can
// take one disappears (review of #361, 2026-11-08). Mutation row
// packages-baseline-counts-a-nested-package-as-its-parents-first-change.
func TestSinceTagPackagesNestedPackageIsNotItsParentsFirstChange(t *testing.T) {
	dir, root, plan := nestedLinesRepo(t)
	usePR(t, walkServer(t, map[string]string{commitPullsPath(root): `[]`, commitPullsPath(plan): `[]`}))
	t.Chdir(dir)
	travelAloneIsBaselined(t)
}

// TestSinceTagPackagesBaselineReadsThePathFromTheTopLevel: the line's path
// is read from the top level (`:(top)`), so `bump --since-tag` run from a
// subdirectory asks the same question as one run from the root. Read
// relative to the working directory, "travel" from travel/onsen/ names a
// path no commit touches and the remedy disappears (review of #361,
// 2026-11-08). Mutation row
// packages-baseline-reads-the-path-from-the-working-directory.
func TestSinceTagPackagesBaselineReadsThePathFromTheTopLevel(t *testing.T) {
	dir, root, plan := nestedLinesRepo(t)
	usePR(t, walkServer(t, map[string]string{commitPullsPath(root): `[]`, commitPullsPath(plan): `[]`}))
	t.Chdir(filepath.Join(dir, "travel", "onsen"))
	travelAloneIsBaselined(t)
}

// TestSinceTagPackagesRootPackageExcludesTheOtherLines: the root package's
// own files are the tree minus every other package, so a root line declared
// after haiku has a first change of its own — the declaring commit, with a
// parent — and is offered a baseline. Owning the whole tree, its "first
// change" is the root commit that added haiku/ and the remedy disappears
// (review of #361, 2026-11-08). haiku's first change IS the root, so it is
// offered none either way: the two lines tell the two readings apart.
// Mutation row packages-baseline-root-package-owns-every-line.
func TestSinceTagPackagesRootPackageExcludesTheOtherLines(t *testing.T) {
	testutil.RequireGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	first := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(haiku)^ add haiku", "haiku/h.go")
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatal("gemoji preset missing")
	}
	testutil.WriteFile(t, dir, "glyph.toml", string(preset)+"\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \".\"\nname = \"core\"\n")
	testutil.WriteFile(t, dir, "README.md", "core\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":wrench:(core)= declare the lines")
	declared := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(first): `[]`, commitPullsPath(declared): `[]`}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump exited %d, want 0 (haiku's ^ moves it)\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "; to start from a tag instead, cut v0.0.0 at the commit before that line's first change") {
		t.Errorf("the root package's first change is the declaring commit, which has a parent, so its baseline is named:\n%s", stderr)
	}
	if strings.Contains(stderr, "haiku/v0.0.0") {
		t.Errorf("haiku's first change is the root commit, so no baseline can precede it:\n%s", stderr)
	}
}

// TestSinceTagPackagesLineNoCommitTouchesHasNoBaseline: a declared line
// whose subtree no commit of HEAD's history touches has no first change, so
// there is no commit before it to cut a baseline at — the warning says the
// walk proceeds and nothing else. Offered regardless, a `pubsub/v2` with no
// file yet was told to cut pubsub/v2.0.0 "at the commit before that line's
// first change" (review of #361, 2026-11-08). Mutation row
// packages-baseline-named-for-a-line-no-commit-touches.
func TestSinceTagPackagesLineNoCommitTouchesHasNoBaseline(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"pubsub\"\n\n[[packages]]\npath = \"pubsub/v2\"\nname = \"pubsub-v2\"\n", map[string]string{"pubsub/p.go": "package pubsub\n"})
	testutil.Git(t, dir, "akira-toriyama", "tag", "pubsub/v1.4.0")
	testutil.Touch(t, dir, "akira-toriyama", ":memo:= note the queue", "pubsub/notes.md")
	routes := map[string]string{}
	for sha := range strings.FieldsSeq(testutil.Git(t, dir, "akira-toriyama", "rev-list", "HEAD")) {
		routes[commitPullsPath(sha)] = `[]`
	}
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("bump exited %d, want 1 (a = on pubsub, nothing on pubsub/v2)\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "::warning::glyph: no version tag on the pubsub/v2.* line(s) in HEAD's history — walking the repository's whole history (") {
		t.Fatalf("the untagged locked line walks the whole history:\n%s", stderr)
	}
	if strings.Contains(stderr, "cut ") {
		t.Errorf("no commit touches pubsub/v2, so there is no first change to cut a baseline before:\n%s", stderr)
	}
}

// TestSinceTagPackagesBaselinePastAShallowBoundaryIsNotNamed: within a
// shallow clone the oldest commit touching a line the boundary's tree holds
// is the boundary itself, whose own diff the clone cannot read (DESIGN §4.1,
// "Where the files come from"), so the line's first change lies at or past
// it and no baseline can be named — the warning says so and names none,
// where the first cut read the boundary as a parentless root and said the
// single line's sentence (review of #361, 2026-11-08). The full clone is the
// control: the declaring commit has a parent, and both lines are offered
// their baseline. The walk itself still proceeds and warns (exit 1, a
// history of = commits), never 4: a shallow checkout is a warning to the
// reporting commands (§4). Mutation rows
// gitsource-first-change-reads-a-shallow-boundary-as-a-root (the clause
// vanishes) and packages-baseline-fails-at-a-shallow-boundary (exit 4).
func TestSinceTagPackagesBaselinePastAShallowBoundaryIsNotNamed(t *testing.T) {
	dir := testutil.NewTwoLineRepo(t)
	for i := range 4 {
		testutil.Commit(t, dir, "akira-toriyama", ":memo:= note "+string(rune('0'+i)))
	}
	routes := map[string]string{}
	for sha := range strings.FieldsSeq(testutil.Git(t, dir, "akira-toriyama", "rev-list", "HEAD")) {
		routes[commitPullsPath(sha)] = `[]`
	}
	usePR(t, walkServer(t, routes))
	clone := shallowClone(t, dir, 3)
	boundary := testutil.Git(t, clone, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
	if tree := testutil.Git(t, clone, "akira-toriyama", "ls-tree", "--name-only", boundary); !strings.Contains(tree, "haiku") || !strings.Contains(tree, "curry") {
		t.Fatalf("control: the boundary's tree must hold both lines, got %q", tree)
	}

	t.Chdir(clone)
	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("bump on the shallow clone exited %d, want 1 — the walk proceeds and warns\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "; no baseline can be named for the haiku/, curry/ line(s): within this shallow clone the oldest commit touching it is the boundary, whose own diff cannot be read, so the line's first change lies at or past it (check out with fetch-depth: 0)") {
		t.Errorf("the warning must say why no baseline is named on a shallow clone:\n%s", stderr)
	}
	if strings.Contains(stderr, "cut ") {
		t.Errorf("a baseline before a change the clone cannot read names a commit the clone does not hold:\n%s", stderr)
	}

	t.Chdir(dir)
	code, _, stderr = runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("bump on the full clone exited %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "; to start from a tag instead, cut haiku/v0.0.0 / curry/v0.0.0 at the commit before that line's first change") || strings.Contains(stderr, "boundary") {
		t.Errorf("the full clone reads the declaring commit's parent and offers both baselines, and says nothing of a boundary:\n%s", stderr)
	}
}
