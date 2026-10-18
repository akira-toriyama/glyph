package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// This file pins the walk base's candidate set (DESIGN §4, "The walk base is
// a release HEAD contains"): every base read out of the tags is the highest
// tag on the line among the tags HEAD's history holds. The fixtures are the
// three shapes where the two sets differ — a side branch's tag, a
// maintenance branch's, a tag cut later on a descendant — and the shallow
// checkout, where git cannot say which set is which.

// TestSinceTagMaintenanceBranchStepsItsOwnLine: release/v1 forked at v1.9.0,
// and v1.9.1 was cut there after main cut v2.0.0. On the maintenance branch
// the base is v1.9.1, and only the one unreleased backport is asked about —
// the server holds no route for the released one, so asking about it fails
// the test (measured at adfc5e1: v2.0.0 -> v2.0.1 over both backports).
func TestSinceTagMaintenanceBranchStepsItsOwnLine(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ two")
	testGit(t, dir, "akira-toriyama", "tag", "v1.9.0")
	testGit(t, dir, "akira-toriyama", "branch", "release/v1")
	testCommit(t, dir, "akira-toriyama", ":boom:! break the api")
	testGit(t, dir, "akira-toriyama", "tag", "v2.0.0")
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "release/v1")
	testCommit(t, dir, "akira-toriyama", ":bug:~ backport one")
	testGit(t, dir, "akira-toriyama", "tag", "v1.9.1")
	testCommit(t, dir, "akira-toriyama", ":bug:~ backport two")
	two := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{commitPullsPath(two): `[]`})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag on release/v1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct{ Current, Next string }
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if res.Current != "v1.9.1" || res.Next != "v1.9.2" {
		t.Errorf("current/next = %s/%s, want v1.9.1/v1.9.2 — the maintenance line steps from its own release, never from the next major's tag", res.Current, res.Next)
	}
}

// TestSinceTagFrozenCoordinateSeesItsOwnTags: a checkout at an older commit
// sees a tag cut later on a descendant. Read as the base, every commit HEAD
// holds is inside that tag, so the walk folds none — glyph-monorepo-test's
// frozen-coordinate tier collapsed to exactly that at v3.3.0 (#15 there),
// and at adfc5e1 this exits 1.
func TestSinceTagFrozenCoordinateSeesItsOwnTags(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ second")
	frozen := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "akira-toriyama", ":bug:~ third")
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "--detach", frozen)
	srv := walkServer(t, map[string]string{commitPullsPath(frozen): `[]`})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "v0.2.0\n" {
		t.Fatalf("bump --since-tag at the frozen coordinate = exit %d %q, want 0 \"v0.2.0\\n\" (v0.1.0 + ^)\nstderr: %s", code, stdout, stderr)
	}
}

// TestReleaseAtAnOlderCommitRefusesAtTheFloor: a release run at an older
// commit — a GitHub re-run keeps the original event's sha — with v0.2.0
// published on a descendant and the v0.3.0 rolling draft standing. Scoped to
// a range that folds ABOVE none: baselined on v0.2.0, the tag HEAD does not
// contain, the walk folded none and DELETED the draft, hand region and all
// (measured at adfc5e1); over the release HEAD holds it steps to the version
// v0.2.0 already published, and the floor refuses before any write. A range
// that folds none still converges the draft away under either rule — the
// stale writer DESIGN §4 narrows and does not close.
func TestReleaseAtAnOlderCommitRefusesAtTheFloor(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ second")
	older := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "akira-toriyama", ":bug:~ third")
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "--detach", older)
	var writes []apiWrite
	srv := releaseServer(t, map[string]string{commitPullsPath(older): `[]`},
		`[`+publishedJSON(1, "v0.1.0")+`,`+publishedJSON(2, "v0.2.0")+`,`+draftJSON(3, "v0.3.0")+`]`, &writes)
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "release")
	if len(writes) != 0 {
		t.Fatalf("a release at an older commit wrote %v — it must write nothing", writes)
	}
	if code != 4 || !strings.Contains(stderr, "v0.2.0 is not greater than the latest published release v0.2.0") {
		t.Fatalf("exit %d, want 4 at the published floor\nstderr: %s", code, stderr)
	}
}

// TestPackagesFrozenCoordinateSeesItsOwnTags is the frozen coordinate per
// line: the line tags cut on a descendant are not this coordinate's bases, so
// haiku steps from its own v0.1.0 over the poem and curry stays at v0.1.0
// (measured at adfc5e1: both lines folded none, exit 1).
func TestPackagesFrozenCoordinateSeesItsOwnTags(t *testing.T) {
	dir, _ := packagesRepo(t) // haiku/v0.1.0, curry/v0.1.0
	poem := touch(t, dir, "akira-toriyama", ":sparkles:(haiku)^ a poem", "haiku/poem.go")
	touch(t, dir, "akira-toriyama", ":bug:(curry)~ a spice", "curry/curry.go")
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	testGit(t, dir, "akira-toriyama", "tag", "curry/v0.1.1")
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "--detach", poem)
	srv := walkServer(t, map[string]string{commitPullsPath(poem): `[]`})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag at the frozen coordinate exited %d, want 0\nstderr: %s", code, stderr)
	}
	got := map[string]string{}
	for _, p := range decodePackagesVerdict(t, stdout).Packages {
		got[p.Path] = p.Current + ":" + p.Level
	}
	if got["haiku"] != "v0.1.0:minor" || got["curry"] != "v0.1.0:none" {
		t.Errorf("lines = %v, want haiku v0.1.0:minor and curry v0.1.0:none", got)
	}
}
