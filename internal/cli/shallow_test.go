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
		if code != 3 || !strings.Contains(stderr, "touches no declared package") {
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

// TestPackagesWalkCarriesAShallowBoundaryOnNoLine: the walk's side of the same
// read. A --depth 1 clone's HEAD touched haiku/ alone, and its whole-tree diff
// moved curry too (measured: both lines patch). The boundary is carried on no
// line with a warning — the FilesCapped answer to files the walk could not
// read — so no line moves on a diff nobody could compute. The full clone is
// the control: haiku moves, curry does not.
func TestPackagesWalkCarriesAShallowBoundaryOnNoLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	fix := touch(t, dir, "akira-toriyama", ":bug:(haiku)~ fix a line", "haiku/haiku.go")
	clone := shallowClone(t, dir, 1)

	levels := func(v packagesVerdict) map[string]string {
		out := map[string]string{}
		for _, p := range v.Packages {
			out[p.Path] = p.Level
		}
		return out
	}
	t.Run("the full clone moves haiku alone", func(t *testing.T) {
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD", "--json")
		if got := levels(decodePackagesVerdict(t, stdout)); code != 0 || got["haiku"] != "patch" || got["curry"] != "none" {
			t.Fatalf("bump --range exited %d with levels %v, want 0 with haiku patch and curry none\nstderr: %s", code, got, stderr)
		}
	})
	t.Run("the shallow clone moves no line on an unreadable diff", func(t *testing.T) {
		t.Chdir(clone)
		code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD", "--json")
		if got := levels(decodePackagesVerdict(t, stdout)); code != 1 || got["haiku"] != "none" || got["curry"] != "none" {
			t.Fatalf("bump --range exited %d with levels %v, want 1 with every line none\nstderr: %s", code, got, stderr)
		}
		if !strings.Contains(stderr, "commit "+fix[:7]+" is this shallow clone's boundary") {
			t.Fatalf("the boundary carried on no line must be named:\n%s", stderr)
		}
	})
}
