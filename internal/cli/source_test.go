package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/walk"
)

// captureOut swaps the payload and diagnostic streams for the buffers it
// returns, until restore is called — around a command (runGlyph) or a direct
// call below RunE.
func captureOut() (stdout, stderr *bytes.Buffer, restore func()) {
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := out, errOut
	out, errOut = stdout, stderr
	return stdout, stderr, func() { out, errOut = oldOut, oldErr }
}

func rangeSource(revRange string) source {
	return source{kind: sourceRange, sourceFlags: sourceFlags{rng: revRange}}
}

// TestReadSourceAnswersOneLineWithoutPackages: whichever source names the
// commits — a local range, a pull's listing, the walk — a repository that
// declares no [[packages]] comes back as one bare line holding every commit,
// so bump and notes run one per-line loop and no second arm. The walk is
// the one source with a tag base (the compare link's); the pull's commits
// keep their sha beside the pull, which a `notes --pr` body cites both of.
func TestReadSourceAnswersOneLineWithoutPackages(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
	head := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(head): `[]`,
		pullCommitsPath(7):    `[` + apiCommit("a1b2c3d4", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	}))
	t.Chdir(dir)
	cfg := testCfg(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		src     source
		named   string
		baseTag string
		pull    int
	}{
		{"range", rangeSource(base + "..HEAD"), base + "..HEAD", "", 0},
		{"pull", source{kind: sourcePull, sourceFlags: sourceFlags{pull: 7}}, "akira-toriyama/glyph#7", "", 7},
		{"walk", source{kind: sourceWalk, sourceFlags: sourceFlags{tag: walk.Auto}}, "v0.1.0..HEAD", "v0.1.0", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := readSource(ctx, cfg, tc.src)
			if err != nil {
				t.Fatalf("readSource: %v", err)
			}
			if len(w.Lines) != 1 || len(w.All) != 1 || len(w.Lines[0].Commits) != 1 {
				t.Fatalf("lines = %d, walked = %d, on the line = %d — want one bare line holding the one commit", len(w.Lines), len(w.All), len(w.Lines[0].Commits))
			}
			lw := w.Lines[0]
			if w.Source != tc.named || lw.Source != tc.named || lw.BaseTag != tc.baseTag || lw.Package.Path != "" {
				t.Fatalf("source = %q / %q, base tag = %q, package = %q — want source %q, base tag %q on the bare line", w.Source, lw.Source, lw.BaseTag, lw.Package.Path, tc.named, tc.baseTag)
			}
			if cited := walk.NoteCommits(lw.Commits)[0]; cited.SHA == "" || cited.Pull != tc.pull {
				t.Fatalf("the notes would cite sha %q pull %d, want the sha kept and pull %d", cited.SHA, cited.Pull, tc.pull)
			}
		})
	}
}

// TestReadSourceRefusesAPullUnderPackages: with [[packages]] declared a pull
// is refused as a source before its number is examined — usage (2), the
// answer both arms gave — because its listing carries no files to place a
// commit by; a local range answers every declared line.
func TestReadSourceRefusesAPullUnderPackages(t *testing.T) {
	dir, base := packagesRepo(t)
	touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	t.Chdir(dir)
	ctx := context.Background()
	cfg, err := loadConfig(ctx)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	_, err = readSource(ctx, cfg, source{kind: sourcePull, sourceFlags: sourceFlags{pull: 0}})
	if core.ExitCode(err) != 2 || err == nil || !strings.Contains(err.Error(), "[[packages]]") {
		t.Fatalf("a pull under packages: exit %d, %v — want usage (2) naming [[packages]], before --pr 0 is judged", core.ExitCode(err), err)
	}
	w, err := readSource(ctx, cfg, rangeSource(base+"..HEAD"))
	if err != nil {
		t.Fatalf("readSource --range: %v", err)
	}
	if len(w.Lines) != len(cfg.Packages) || len(cfg.Packages) < 2 {
		t.Fatalf("lines = %d, want one per declared package (%d)", len(w.Lines), len(cfg.Packages))
	}
	for i, lw := range w.Lines {
		if lw.Package.Path != cfg.Packages[i].Path || lw.Source != base+"..HEAD" || lw.BaseTag != "" {
			t.Fatalf("line %d = %+v, want package %q, source %q, no tag base", i, lw.Line, cfg.Packages[i].Path, base+"..HEAD")
		}
	}
}

// TestBumpRunsFromOptionsWithoutCobra: the verdict below RunE is a function
// of its options — no command built, no flag parsed — so --current reaches
// the step as a field, not as package state a cobra run has to set first.
func TestBumpRunsFromOptionsWithoutCobra(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
	t.Chdir(dir)
	stdout, stderr, restore := captureOut()
	defer restore()
	ctx := context.Background()

	if err := bumpRun(ctx, bumpOptions{source: rangeSource(base + "..HEAD")}); err != nil || stdout.String() != "v0.2.0\n" {
		t.Fatalf("bumpRun from options: %v, stdout %q, want v0.2.0\nstderr: %s", err, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if err := bumpRun(ctx, bumpOptions{source: rangeSource(base + "..HEAD"), current: "v1.4.0"}); err != nil || stdout.String() != "v1.5.0\n" {
		t.Fatalf("bumpRun with current from options: %v, stdout %q, want v1.5.0", err, stdout.String())
	}
}

// TestNotesRunsFromOptionsWithoutCobra: notesRun is the same kind of
// function — the body renders from the options alone.
func TestNotesRunsFromOptionsWithoutCobra(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ add a menu")
	t.Chdir(dir)
	stdout, stderr, restore := captureOut()
	defer restore()

	if err := notesRun(context.Background(), notesOptions{source: rangeSource(base + "..HEAD")}); err != nil || !strings.Contains(stdout.String(), "add a menu") {
		t.Fatalf("notesRun from options: %v, stdout %q, want the commit rendered\nstderr: %s", err, stdout.String(), stderr.String())
	}
}

// TestOptionShapedRangeIsUsage: a --range that looks like an option is the
// caller's input, refused at 2 before git runs (checkRangeFlag, asked by
// readSource) — git would otherwise answer "bad revision" at 4, a retryable
// code for a mistake no retry fixes. The empty form is pinned for every
// string flag (TestEveryStringFlagRejectsItsEmptyForm); this is the other
// shape the guard names.
func TestOptionShapedRangeIsUsage(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)
	for _, cmd := range []string{"bump", "notes"} {
		code, _, stderr := runGlyph(t, cmd, "--range", "-x")
		if code != 2 || !strings.Contains(stderr, "looks like an option") {
			t.Errorf("%s --range -x exited %d, want 2 saying it looks like an option\nstderr: %s", cmd, code, stderr)
		}
	}
}
