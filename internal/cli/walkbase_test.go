package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// This file pins the walk base (DESIGN §4, "The walk base is a release HEAD
// contains"; internal/walk/base.go) through the CLI: the candidate set every
// resolved form reads from — the highest tag on the line among the tags
// HEAD's history holds — and how each --since-tag form resolves its base:
// auto, a named tag, a tag that names no version, below:, the whole-history
// arm and its cap, and the empty range. The first fixtures are two shapes
// where the candidate set differs from the repository's tags — a maintenance
// branch's tag, and a tag cut later on a descendant, on the bare line and
// per line — and the release run at an older commit that the published
// floor refuses; the side branch's tag and the shallow checkout are
// internal/walk/base_test.go's.

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

// TestBumpSinceTagAutoDetectsTheTag: a bare --since-tag resolves the walk base
// itself — the highest parseable v* tag — so the release job never has to
// duplicate glyph's version-tag policy in shell. Only the one commit after
// v0.1.0 may be walked: the tagged base commit reaching the API would show up
// as an unexpected request.
func TestBumpSinceTagAutoDetectsTheTag(t *testing.T) {
	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(3, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(3):    `[` + apiCommit("c1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bare bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("bare bump --since-tag stdout = %q, want %q", stdout, "v0.1.1\n")
	}
}

// TestBumpSinceTagExplicitTagIsTheStepBase: naming a tag names the release
// being redone — the walk base and the step base must be the SAME tag, or the
// verdict is computed from one range and versioned from another. --current
// still wins when given.
func TestBumpSinceTagExplicitTagIsTheStepBase(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0") // base also carries a HIGHER tag
	sha := squashCommit(t, dir, "Fix a crash", 3)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(3, "2026-07-15T00:00:00Z", sha) + `]`,
		pullCommitsPath(3):   `[` + apiCommit("c1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=v0.1.0")
	if code != 0 {
		t.Fatalf("bump --since-tag=v0.1.0 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 — the named tag is the step base, not the higher v0.2.0", stdout)
	}

	code, stdout, _ = runGlyph(t, "bump", "--since-tag=v0.1.0", "--current", "v3.0.0")
	if code != 0 || stdout != "v3.0.1\n" {
		t.Fatalf("--current must still win: exit %d stdout %q, want 0 / v3.0.1", code, stdout)
	}
}

// TestSinceTagAutoWithoutTagsWalksTheWholeHistory: a repository before its
// first release has no walk base — auto walks everything from the root, and
// the bump steps from v0.0.0.
func TestSinceTagAutoWithoutTagsWalksTheWholeHistory(t *testing.T) {
	dir, base := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	sha1 := squashCommit(t, dir, "Add a menu", 2)
	srv := walkServer(t, map[string]string{
		commitPullsPath(base): `[]`, // the root :tada:= commit: fallback, none
		commitPullsPath(sha1): `[` + apiPullRef(2, "2026-07-13T00:00:00Z", sha1) + `]`,
		pullCommitsPath(2):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag before the first release exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.0\n" {
		t.Fatalf("stdout = %q, want v0.1.0 (minor step from v0.0.0)", stdout)
	}
	if !strings.Contains(stderr, "whole history") {
		t.Fatalf("walking everything is a cost the caller should see named (one API round-trip per commit):\n%s", stderr)
	}
}

// TestSinceTagEmptyWalkIsNoRelease: nothing on main since the tag is the quiet
// week — a soft no-release (1) naming the range it read, not an error.
func TestSinceTagEmptyWalkIsNoRelease(t *testing.T) {
	dir, _ := testRepo(t)
	srv := walkServer(t, map[string]string{})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 1 {
		t.Fatalf("an empty walk exited %d, want 1 (no release)", code)
	}
	if stdout != "" {
		t.Fatalf("a no-release must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "v0.1.0..HEAD") {
		t.Fatalf("the no-release reason should name the walked range:\n%s", stderr)
	}
}

// TestBumpSinceTagNonVersionTag: an explicit --since-tag that is not a semver
// version still anchors the WALK at that tag, but names no version base — the
// bump falls back to the highest parseable v* tag. This is the third arm of
// the base resolution (auto / version tag / other tag), and the one a caller
// hits when releasing past a non-release tag (a marker like rc-1).
func TestBumpSinceTagNonVersionTag(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "rc-1") // same commit as v0.1.0
	sha := squashCommit(t, dir, "Fix a crash", 8)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=rc-1")
	if code != 0 {
		t.Fatalf("bump --since-tag=rc-1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 (walk from rc-1, step from the highest v* tag)", stdout)
	}
}

// TestSinceTagBelowResolvesThePredecessorAsAVersion: --since-tag=below:TAG is
// the tag-push question — the new tag is already the highest v* tag, so the
// walk base is the highest version STRICTLY below it. goreleaser.yml used to
// re-derive that answer in shell over `git tag --sort=v:refname`, which is a
// refname sort, not a version order: with a prerelease tag present it handed
// back the prerelease as the predecessor (measured: prev for v1.0.1 =>
// v1.0.0-rc1), and the release notes re-folded commits v1.0.0 had already
// shipped. glyph's own resolver rejects the unparseable prerelease and answers
// v1.0.0 — only the one commit after it may reach the API, and the answer must
// equal the tag being cut.
func TestSinceTagBelowResolvesThePredecessorAsAVersion(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":bug:~ an rc-era fix")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0-rc1")
	testCommit(t, dir, "akira-toriyama", ":bug:~ the 1.0 fix")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")
	sha := squashCommit(t, dir, "Fix a crash", 7)
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.1")

	// The premise: git's version sort really does slot the prerelease between
	// the two releases, so the retired shell derivation (awk over this exact
	// listing) picked v1.0.0-rc1 as v1.0.1's predecessor. Without this, the
	// assertions below could pass on a git whose sort changed, proving nothing.
	if listing := testGit(t, dir, "akira-toriyama", "tag", "--list", "v*", "--sort=v:refname"); !strings.Contains(listing, "v1.0.0\nv1.0.0-rc1\nv1.0.1") {
		t.Fatalf("premise gone: git --sort=v:refname no longer orders the prerelease above its release:\n%s", listing)
	}

	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):   `[` + apiCommit("a1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=below:v1.0.1")
	if code != 0 {
		t.Fatalf("bump --since-tag=below:v1.0.1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v1.0.1\n" {
		t.Fatalf("stdout = %q, want v1.0.1 (a patch step from v1.0.0 — the predecessor by VERSION, not by git's sort)", stdout)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag=below:v1.0.1")
	if code != 0 {
		t.Fatalf("notes --since-tag=below:v1.0.1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "fix a crash") || strings.Contains(stdout, "the 1.0 fix") {
		t.Fatalf("notes must carry exactly the walk above v1.0.0 — nothing v1.0.0 already shipped:\n%s", stdout)
	}
}

// TestSinceTagBelowAcceptsAReleaseCandidate is the release job glyph cut on
// itself and lost. goreleaser.yml triggers on `tags: ['v*']` and hands
// $GITHUB_REF_NAME to --since-tag=below:, and .goreleaser.yaml asks GitHub to
// mark a hyphenated tag as a pre-release — so the two files together promise
// candidates work. They did not: pushing v3.0.0-rc.1 died here at exit 2
// ("needs a version-shaped tag"), before GoReleaser ran, leaving no notes, no
// binaries, no cask and no attestation behind a tag that already existed —
// the exact failure below: was introduced to end (t-s5n4), through the one
// input nobody had handed it.
//
// The candidate resolves to the predecessor of the RELEASE it is a candidate
// for, and this fixture pins both halves at once: v1.0.0-rc1 is tagged in the
// history too, so an implementation that merely loosened ParseVersion would
// answer v1.0.0-rc1 here and re-fold nothing, while the correct answer walks
// from v1.0.0 and carries exactly the commit above it.
func TestSinceTagBelowAcceptsAReleaseCandidate(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":bug:~ an rc-era fix")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0-rc1")
	testCommit(t, dir, "akira-toriyama", ":bug:~ the 1.0 fix")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")
	sha := squashCommit(t, dir, "Add the candidate feature", 11)
	// The tag being cut IS the candidate — the tag-push shape, where auto
	// would resolve the tag being cut and walk an empty range.
	testGit(t, dir, "akira-toriyama", "tag", "v1.1.0-rc.1")

	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(11, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(11):  `[` + apiCommit("c1", "akira-toriyama", ":sparkles:^ add the candidate feature") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=below:v1.1.0-rc.1")
	if code != 0 {
		t.Fatalf("bump --since-tag=below:v1.1.0-rc.1 exited %d, want 0 — a candidate is a tag being cut, "+
			"which is the whole situation below: exists for\nstderr: %s", code, stderr)
	}
	if stdout != "v1.1.0\n" {
		t.Fatalf("stdout = %q, want v1.1.0 (a minor step from v1.0.0). v1.0.0-rc1 answering here would mean "+
			"candidates had been let into the ANSWER set, not just the question", stdout)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag=below:v1.1.0-rc.1")
	if code != 0 {
		t.Fatalf("notes --since-tag=below:v1.1.0-rc.1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "add the candidate feature") || strings.Contains(stdout, "the 1.0 fix") {
		t.Fatalf("a candidate's notes cover everything since the last real RELEASE, and nothing v1.0.0 "+
			"already shipped:\n%s", stdout)
	}
}

// TestSinceTagBelowIsStrictlyBelowNotHighestOther: cutting a v0.8.3 hotfix
// while v0.9.0 exists must resolve v0.8.2 and walk forward from it — never
// backwards from v0.9.0. The wrong reading is detectable because it walks an
// EMPTY range (v0.9.0 is HEAD here) and answers "no release" where the hotfix
// verdict is due.
func TestSinceTagBelowIsStrictlyBelowNotHighestOther(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":bug:~ the 0.8.2 fix")
	testGit(t, dir, "akira-toriyama", "tag", "v0.8.2")
	sha := squashCommit(t, dir, "Fix the hotfix crash", 9)
	testGit(t, dir, "akira-toriyama", "tag", "v0.9.0")
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(9, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(9):   `[` + apiCommit("h1", "akira-toriyama", ":bug:~ fix the hotfix crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=below:v0.8.3")
	if code != 0 {
		t.Fatalf("bump --since-tag=below:v0.8.3 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "v0.8.3\n" {
		t.Fatalf("stdout = %q, want v0.8.3 (stepping from v0.8.2, the highest version strictly below the bound)", stdout)
	}
}

// TestSinceTagBelowFirstVersionWalksTheWholeHistory: nothing sits below the
// repository's first version tag, and the answer is the same walk auto makes
// before the first release — the whole history, with the cost named. The shell
// derivation this replaces answered an EMPTY predecessor here and exited 1:
// no binaries, no cask, no attestation, behind a tag that already existed.
func TestSinceTagBelowFirstVersionWalksTheWholeHistory(t *testing.T) {
	dir, base := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	sha := squashCommit(t, dir, "Add a menu", 2)
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")
	srv := walkServer(t, map[string]string{
		commitPullsPath(base): `[]`, // the root :tada:= commit: fallback, none
		commitPullsPath(sha):  `[` + apiPullRef(2, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(2):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "notes", "--since-tag=below:v1.0.0")
	if code != 0 {
		t.Fatalf("notes --since-tag=below: on the first version tag exited %d, want 0 — a first release has notes, not a dead job\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "whole history") {
		t.Fatalf("walking everything is a cost the caller should see named:\n%s", stderr)
	}
}

// TestSinceTagUnboundedWalkRefused: the release-floor guard on the untagged
// arm (t-354v). A resolved --since-tag with no tag to bound it walks the WHOLE
// history at one API round-trip per visited commit, and release.yml runs that
// on every push of a repository that has not released yet — measured on the
// v0.11.1 rollout, four untagged fleet repositories walked their entire
// histories. Past walk.Cap glyph refuses BEFORE the first API call
// (fail-loud 4, the draftplan.CheckPublishedFloor family: nothing broken, no
// retry clears it, the escape is in the message). Under the cap the walk runs
// exactly as before — a skip would fold zero commits and verdict the first
// release out of existence — and the count excludes the bot authors the walk
// itself never asks about.
func TestSinceTagUnboundedWalkRefused(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	// 199 human commits (plus the fixture's root) and 10 bot commits: 200
	// walk-visible, exactly AT the cap. The bot commits prove the guard counts
	// what the walk VISITS, not raw history length.
	for i := range 199 {
		testCommit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= human commit %d", i))
	}
	for i := range 10 {
		testCommit(t, dir, "dependabot[bot]", fmt.Sprintf(":robot:= chore(fleet): sync %d", i))
	}
	t.Chdir(dir)

	// One more visible commit crosses the cap: both resolved forms must refuse
	// before anything reaches the API — the server holds no routes, so any
	// request fails the test.
	testCommit(t, dir, "akira-toriyama", ":memo:= the 201st visible commit")
	srv := walkServer(t, map[string]string{})
	usePR(t, srv)

	for _, flag := range []string{"--since-tag", "--since-tag=below:v9.9.9"} {
		code, _, stderr := runGlyph(t, "bump", flag)
		if code != 4 {
			t.Fatalf("bump %s over 201 walk-visible untagged commits exited %d, want 4 (refuse "+
				"the unbounded walk)\nstderr: %s", flag, code, stderr)
		}
		for _, want := range []string{"201", "200", "--since-tag=TAG"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the refusal for %s must name the real cost and the escape (missing %q):\n%s", flag, want, stderr)
			}
		}
	}
}
