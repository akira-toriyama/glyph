package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// shallowClone clones src at depth into a fresh directory — a checkout whose
// history stops where actions/checkout's default fetch-depth: 1 stops it.
func shallowClone(t *testing.T, src string, depth int) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "shallow")
	testGit(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", fmt.Sprint(depth), "file://"+src, clone)
	return clone
}

// TestLintRangeOnAShallowCheckoutSaysSo: git lists only the commits a shallow
// clone holds, so `lint --range` there judges part of the range and — measured
// before this, on a --depth 2 clone — answered 0 with nothing on stderr where
// the full clone refuses the same range at 3 (t-esm5 (1b)). It WARNS and keeps
// its verdict about the commits it did judge: a refusal would be a new lint
// semantics, and a shallow walk is a warning to every reporting command
// (DESIGN §4.1, "Lint"). The full clone is the control twice over: it refuses,
// and it says nothing about shallowness.
func TestLintRangeOnAShallowCheckoutSaysSo(t *testing.T) {
	dir, _ := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
	for i := range 4 {
		testCommit(t, dir, "akira-toriyama", fmt.Sprintf(":bug:~ fix number %d", i))
	}
	clone := shallowClone(t, dir, 2)

	t.Run("the full clone refuses the range", func(t *testing.T) {
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD")
		if code != 3 {
			t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
		}
		if strings.Contains(stderr, "SHALLOW") {
			t.Fatalf("a full clone must not be called shallow:\n%s", stderr)
		}
	})
	t.Run("the shallow clone judges what it holds, and says so", func(t *testing.T) {
		t.Chdir(clone)
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD")
		if code != 0 {
			t.Fatalf("lint --range on a shallow clone exited %d, want 0 — it warns, never refuses\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "SHALLOW checkout") {
			t.Fatalf("a range judged over a truncated history must say so:\n%s", stderr)
		}
	})
}

// TestBumpAndNotesRangeOnAShallowCheckoutSaySo: a range read is a range read.
// bump --range and notes --range list their commits with the same git log as
// lint --range, so on a shallow clone they too read only what the clone holds
// — measured on a --depth 2 clone before this: bump printed a version and
// notes rendered 2 of 5 commits, both at 0 with no shallow warning, where the
// full clone's bump refuses the range at 3; under [[packages]] only the
// boundary commit was warned about, never the range. They warn through lint's
// read and keep answering: they report, and release alone refuses a shallow
// checkout. The full clone is the control: bump refuses, and nothing is said
// about shallowness.
func TestBumpAndNotesRangeOnAShallowCheckoutSaySo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) string
	}{
		{"a single line", func(t *testing.T) string {
			dir, _ := testRepo(t)
			testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
			for i := range 3 {
				testCommit(t, dir, "akira-toriyama", fmt.Sprintf(":bug:~ fix number %d", i))
			}
			return dir
		}},
		{"[[packages]]", func(t *testing.T) string {
			dir, _ := packagesRepo(t)
			touch(t, dir, "akira-toriyama", "no gitmoji in this one", "haiku/a.go")
			for i := range 3 {
				touch(t, dir, "akira-toriyama", fmt.Sprintf(":bug:(haiku)~ fix number %d", i), "haiku/haiku.go")
			}
			return dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.build(t)
			clone := shallowClone(t, dir, 2)
			for _, c := range []struct {
				cmd  string
				full int
			}{{"bump", 3}, {"notes", 0}} {
				t.Chdir(dir)
				code, _, stderr := runGlyph(t, c.cmd, "--range", "HEAD")
				if code != c.full || strings.Contains(stderr, "SHALLOW") {
					t.Fatalf("%s --range on the full clone exited %d, want %d with nothing said about shallowness\nstderr: %s", c.cmd, code, c.full, stderr)
				}
				t.Chdir(clone)
				code, _, stderr = runGlyph(t, c.cmd, "--range", "HEAD")
				if code != 0 {
					t.Fatalf("%s --range on a shallow clone exited %d, want 0 — it warns, never refuses\nstderr: %s", c.cmd, code, stderr)
				}
				if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "SHALLOW checkout") {
					t.Fatalf("%s --range over a truncated history must say so:\n%s", c.cmd, stderr)
				}
			}
		})
	}
}

// TestLintRangePackagesAtAShallowBoundaryIsNotReadAsTheWholeTree: the
// boundary commit of a --depth 1 clone is read by git as a root, and its diff
// used to be its whole tree — so a shared-only `^`, refused at 3 in the full
// clone, found a carrier under haiku/ and passed at 0 in silence (measured,
// t-esm5 (1)). The boundary's diff is unreadable now: its message is still
// judged, its attribution is not asked, and both gates say which commit went
// unchecked.
func TestLintRangePackagesAtAShallowBoundaryIsNotReadAsTheWholeTree(t *testing.T) {
	dir, _ := packagesRepo(t)
	shared := touch(t, dir, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
	clone := shallowClone(t, dir, 1)

	t.Run("the full clone refuses the shared-only bump", func(t *testing.T) {
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
		if code != 3 || !strings.Contains(stderr, "its files (go.work) belong to no declared package") {
			t.Fatalf("lint --range exited %d, want 3 with the no-carrier finding\nstderr: %s", code, stderr)
		}
	})
	t.Run("the shallow clone does not attribute the boundary's whole tree", func(t *testing.T) {
		t.Chdir(clone)
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD")
		if code != 0 {
			t.Fatalf("lint --range exited %d, want 0 — the attribution is unanswerable here, not violated\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "commit "+shared[:7]+": ") || !strings.Contains(stderr, "attribution was not checked") {
			t.Fatalf("the boundary commit's unchecked attribution must be named:\n%s", stderr)
		}
	})
}

// TestPackagesWalkPlacesAShallowBoundaryByScopeAndSigil: the walk's side of
// the same read. A --depth 1 clone's HEAD touched haiku/ alone, and its
// whole-tree diff moved curry too (measured: both lines patch). The boundary's
// diff is unread, so rules 2–3 place it over no file, exactly as they place a
// commit whose file listing GitHub answered with a 422 (DESIGN §4.1): a scope
// naming a package carries it there, a refusal is withheld — the gate code
// over files nobody read would be a verdict about nothing — and one warning
// says where it went and that no file was read. The first cut carried it on no
// line whatever its scope, so one walk placed a 422 by its scope and the
// boundary nowhere. The price is pinned by the last two cases: a scope the
// files contradict — another package's file, or one the root package owns —
// moves the line it names, where the full clone refuses at 3. So is where the
// step starts: a --depth 1 clone fetched no tag, and the line steps from
// v0.0.0.
func TestPackagesWalkPlacesAShallowBoundaryByScopeAndSigil(t *testing.T) {
	type verdict struct {
		haiku, curry string // levels
		next         string // haiku's next version, "" when it does not move
	}
	read := func(t *testing.T, stdout string) verdict {
		t.Helper()
		var v verdict
		for _, p := range decodePackagesVerdict(t, stdout).Packages {
			switch p.Path {
			case "haiku":
				v.haiku, v.next = p.Level, p.Next
			case "curry":
				v.curry = p.Level
			}
		}
		return v
	}
	for _, tc := range []struct {
		name, message, path string
		root                bool    // declare the root package too
		full                int     // the full clone's bump over the same commit
		fullVerdict         verdict // not asked at 3, which prints no verdict
		bump, notes         int     // the shallow clone's exits
		shallow             verdict
		warning             string
	}{
		{"its scope carries it, to the line its files move in the full clone",
			":bug:(haiku)~ fix a line", "haiku/haiku.go", false,
			0, verdict{"patch", "none", "v0.1.1"}, 0, 0, verdict{"patch", "none", "v0.0.1"},
			"it is carried on haiku/ by its scope alone, with no file read"},
		{"with no scope its refusal is withheld, never handed down as the gate code",
			":bug:~ fix a line", "haiku/haiku.go", false,
			0, verdict{"patch", "none", "v0.1.1"}, 1, 1, verdict{"none", "none", ""},
			// The withheld refusal speaks of what was READ: said of the commit
			// ("touches no file") it contradicted the warning around it, which
			// says a package it touched is missing.
			"over no file, attribution would refuse it (no file of this commit was read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry), or write = so it moves no line), which is not a verdict: the commit is carried nowhere"},
		{"a = with no scope is on no line",
			":memo:= reword a line", "haiku/haiku.go", false,
			1, verdict{"none", "none", ""}, 1, 1, verdict{"none", "none", ""},
			"it is carried on no line"},
		{"a scope another package's file contradicts moves the line it names — the accepted price",
			":bug:(haiku)~ fix a line", "curry/curry.go", false,
			3, verdict{}, 0, 0, verdict{"patch", "none", "v0.0.1"},
			"it is carried on haiku/ by its scope alone, with no file read"},
		{"and so does one a root-package file contradicts",
			":bug:(haiku)~ fix a line", "README.md", true,
			3, verdict{}, 0, 0, verdict{"patch", "none", "v0.0.1"},
			"it is carried on haiku/ by its scope alone, with no file read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			if tc.root {
				dir, _ = packagesRepoWithRoot(t)
			}
			boundary := touch(t, dir, "akira-toriyama", tc.message, tc.path)
			clone := shallowClone(t, dir, 1)

			t.Chdir(dir)
			code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD", "--json")
			if code != tc.full || (code != 3 && read(t, stdout) != tc.fullVerdict) {
				t.Fatalf("the full clone's bump --range exited %d with %q, want %d with %+v\nstderr: %s", code, stdout, tc.full, tc.fullVerdict, stderr)
			}
			t.Chdir(clone)
			code, stdout, stderr = runGlyph(t, "bump", "--range", "HEAD", "--json")
			if code != tc.bump || read(t, stdout) != tc.shallow {
				t.Fatalf("the shallow clone's bump --range exited %d with %q, want %d with %+v\nstderr: %s", code, stdout, tc.bump, tc.shallow, stderr)
			}
			if !strings.Contains(stderr, "commit "+boundary[:7]+" is this shallow clone's boundary") || !strings.Contains(stderr, tc.warning) {
				t.Fatalf("the boundary must be named with where it went (%q):\n%s", tc.warning, stderr)
			}
			if strings.Count(stderr, boundary[:7]) != 1 || strings.Contains(stderr, "GitHub") {
				t.Fatalf("a boundary gets one warning, in git's terms — nothing here came from GitHub:\n%s", stderr)
			}
			if code, stdout, stderr = runGlyph(t, "notes", "--range", "HEAD"); code != tc.notes || strings.Contains(stdout, "a line") != (tc.notes == 0) {
				t.Fatalf("the shallow clone's notes --range exited %d with %q, want %d and the commit rendered only where a line carries it\nstderr: %s", code, stdout, tc.notes, stderr)
			}
		})
	}
}

// TestReleasePackagesShallowBoundaryIsTheCheckoutsShortfall: a boundary's
// unread diff is the checkout's shortfall, not the commit's. Recorded beside
// the unread listings it was refused in a 422's words with a 422's remedy —
// GitHub answered 422 for its file listing, re-run, else cut a tag at or past
// it (measured on that mutant) — though nothing was asked of GitHub and no tag
// past the boundary gives the clone its parents. The boundary is this walk's
// only unread input, so the refusal names the shallow checkout and nothing
// else.
func TestReleasePackagesShallowBoundaryIsTheCheckoutsShortfall(t *testing.T) {
	dir, _ := packagesRepo(t)
	boundary := touch(t, dir, "akira-toriyama", ":bug:(curry)~ thicken the roux", "curry/curry.go")
	head := touch(t, dir, "akira-toriyama", ":bug:(haiku)~ fix a line", "haiku/haiku.go")
	usePR(t, dryServer(t, map[string]string{commitPullsPath(boundary): `[]`, commitPullsPath(head): `[]`}))
	t.Chdir(shallowClone(t, dir, 2))

	code, _, stderr := runGlyph(t, "release", "--dry-run", "--json")
	if code != 4 {
		t.Fatalf("release --dry-run exited %d, want 4 (an incomplete walk)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "commit "+boundary[:7]+" is this shallow clone's boundary") {
		t.Fatalf("the fixture's boundary was never met — this test guards nothing:\n%s", stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if env.Code != 4 || !strings.Contains(env.Message, "this is a shallow checkout") {
		t.Fatalf("the refusal must name the shallow checkout (code %d):\n%s", env.Code, env.Message)
	}
	for _, foreign := range []string{"422", "tag at or past", boundary[:7]} {
		if strings.Contains(env.Message, foreign) {
			t.Errorf("the refusal reads the boundary as an unread listing (%q):\n%s", foreign, env.Message)
		}
	}
}

// TestSinceTagPackagesPlacesABoundaryAndAnUnlistedCommitAlike: one walk, two
// commits whose files nobody read — a landed `:bug:(curry)~` that is a --depth
// 2 clone's boundary, and a squash-merged pull's inner `:bug:(curry)~` whose
// file listing GitHub answers with a 422. Both name curry and the full clone
// carries both there; the shallow walk carried the 422 on curry and the
// boundary on no line (measured on the source before this rule: curry patch
// over 1 commit). One rule places both. The shallow checkout is an incomplete
// walk either way, so release still refuses at 4.
func TestSinceTagPackagesPlacesABoundaryAndAnUnlistedCommitAlike(t *testing.T) {
	dir, _ := packagesRepo(t)
	landed := touch(t, dir, "akira-toriyama", ":bug:(curry)~ thicken the roux", "curry/curry.go")
	squash := touch(t, dir, "akira-toriyama", "Swap an ingredient (#9)", "curry/curry.go")
	routes := map[string]string{
		commitPullsPath(landed): `[]`,
		commitPullsPath(squash): `[` + apiPullRef(9, "2026-09-10T00:00:00Z", squash) + `]`,
		pullCommitsPath(9):      `[` + apiCommit("c9", "akira-toriyama", ":bug:(curry)~ swap an ingredient") + `]`,
		commitFilesPath("c9"):   apiUnknownSHA,
	}
	usePR(t, dryServer(t, routes))
	clone := shallowClone(t, dir, 2)

	for _, where := range []struct {
		name, dir string
	}{{"the full clone", dir}, {"the shallow clone", clone}} {
		t.Chdir(where.dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
		if code != 0 {
			t.Fatalf("%s: bump --since-tag exited %d, want 0\nstdout: %s\nstderr: %s", where.name, code, stdout, stderr)
		}
		res := decodePackagesVerdict(t, stdout)
		h, c := res.Packages[0], res.Packages[1]
		if h.Level != "none" || c.Level != "patch" || len(c.Commits) != 2 {
			t.Fatalf("%s: haiku = %s, curry = %s over %d commit(s); want none, and patch over both\nstderr: %s", where.name, h.Level, c.Level, len(c.Commits), stderr)
		}
	}
	if code, _, stderr := runGlyph(t, "bump", "--since-tag", "--json"); code != 0 || !strings.Contains(stderr, "commit "+landed[:7]+" is this shallow clone's boundary") || !strings.Contains(stderr, "it is carried on curry/ by its scope alone") {
		t.Fatalf("the shallow walk exited %d; it must say the boundary was placed by its scope with no file read:\n%s", code, stderr)
	}
	if code, _, stderr := runGlyph(t, "release", "--dry-run", "--json"); code != 4 {
		t.Fatalf("release --dry-run on the shallow clone exited %d, want 4 (an incomplete walk)\nstderr: %s", code, stderr)
	}
}
