package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// This file pins the compare link (DESIGN §4, "The compare link"; §4.1 per
// line): a body glyph renders from a walk that resolved a TAG base closes its
// notes with `**Full Changelog**: https://<host>/<owner>/<repo>/compare/<base>...<end>`.
// usePR points GITHUB_API_URL at an httptest server, so <host> is 127.0.0.1
// (apiHost drops the port) and the repository is akira-toriyama/glyph.

// linkOpening is the line's fixed opening, the string the absence guards
// below look for — each of them also asserts it present in a positive
// control, so a reworded line cannot turn them vacuous.
const linkOpening = "**Full Changelog**: "

// compareLine is the exact line a test expects, newline included.
func compareLine(base, end string) string {
	return linkOpening + "https://127.0.0.1/akira-toriyama/glyph/compare/" + base + "..." + end + "\n"
}

// writeFooter writes a --footer-file into a temp dir and returns its path.
func writeFooter(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "install.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// releaseDryRunJSON runs `release --dry-run --json` with args and decodes the
// verdict; wantCode is the exact exit code the run must give.
func releaseDryRunJSON(t *testing.T, wantCode int, args ...string) releaseVerdict {
	t.Helper()
	code, stdout, stderr := runGlyph(t, append([]string{"release", "--dry-run", "--json"}, args...)...)
	if code != wantCode {
		t.Fatalf("release --dry-run --json %v exited %d, want %d\nstderr: %s", args, code, wantCode, stderr)
	}
	var v releaseVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout)
	}
	return v
}

// TestNotesSinceTagEndsWithTheCompareLink: `notes --since-tag` is a body
// glyph PUBLISHES, not a preview of one — goreleaser.yml hands its stdout to
// GoReleaser's --release-notes, which loads it byte for byte, so glyph's own
// release page and every Go CLI releasing the same way carry exactly this
// line or none (t-v7f7: v4.2.0's published body held 0 URLs). Every
// resolution that names a tag base renders it — auto, the tag named, and
// below:, the form goreleaser.yml runs from the tagged commit — ending at
// HEAD, the commit the walk read up to.
func TestNotesSinceTagEndsWithTheCompareLink(t *testing.T) {
	usePR(t, dryServer(t, oneFixWalk(t)))
	head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")
	want := "## Fixes\n\n- :bug:~ fix a crash (#8) @akira-toriyama\n\n" + compareLine("v0.1.0", head)

	check := func(args ...string) {
		t.Helper()
		code, stdout, stderr := runGlyph(t, append([]string{"notes"}, args...)...)
		if code != 0 {
			t.Fatalf("notes %v exited %d, want 0\nstderr: %s", args, code, stderr)
		}
		if stdout != want {
			t.Errorf("notes %v stdout:\n--- got ---\n%s\n--- want ---\n%s", args, stdout, want)
		}
	}
	check("--since-tag")
	check("--since-tag=v0.1.0")
	testGit(t, ".", "akira-toriyama", "tag", "v0.1.1")
	check("--since-tag=below:v0.1.1")
}

// TestReleaseBodyClosesTheNotesWithTheCompareLink: the link closes the NOTES,
// before --footer-file's block, and ends at the draft's target — a sha,
// because the draft's own tag does not exist until a human publishes and
// `compare/<base>...<tag>` 404s for the draft's whole life (measured: t-v7f7
// (6)). The --target here is not HEAD, so an end taken from HEAD fails too.
// At the default target the machine region is then exactly `notes
// --since-tag`'s stdout for the same walk followed by the footer block — one
// body, rendered by one helper, whichever command publishes it.
func TestReleaseBodyClosesTheNotesWithTheCompareLink(t *testing.T) {
	const install = "## Install\n\n`brew install x`\n"
	footer := writeFooter(t, install)
	usePR(t, dryServer(t, oneFixWalk(t)))

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", footer, "--target", goldenTarget)
	if code != 0 {
		t.Fatalf("release --dry-run exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "v0.1.1\n\n" + handMarker + "\n\n" +
		"## Fixes\n\n- :bug:~ fix a crash (#8) @akira-toriyama\n\n" +
		compareLine("v0.1.0", goldenTarget) +
		"\n---\n\n" + install
	if stdout != want {
		t.Errorf("release --dry-run stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}

	code, notesOut, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	v := releaseDryRunJSON(t, 0, "--footer-file", footer)
	if machine := strings.TrimPrefix(v.Body, handMarker+"\n\n"); machine != notesOut+"\n---\n\n"+install {
		t.Errorf("the draft's machine region is not notes' output plus the footer:\n--- machine region ---\n%s\n--- notes ---\n%s", machine, notesOut)
	}
}

// TestCompareLinkKeepsTheBaseTagsSpelling: the left side is the tag the walk
// resolved, in its own spelling. ParseVersion accepts a bare `1.0.0`, and
// Version.String() re-adds the v — the verdict's current reads v1.0.0 — so a
// base re-spelled from the parsed version names a ref the repository does
// not have, and GitHub answers a ref it does not have with 404 (measured:
// t-v7f7 (7) on cli/cli, `compare/2.0.0...<sha>` 404 beside v2.0.0's 200).
// Both commands that render the link.
func TestCompareLinkKeepsTheBaseTagsSpelling(t *testing.T) {
	dir := testutil.NewRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "1.0.0")
	sha := squashCommit(t, dir, "Fix a crash", 8)
	t.Chdir(dir)
	usePR(t, dryServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	}))

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if want := compareLine("1.0.0", sha); !strings.HasSuffix(stdout, "\n\n"+want) {
		t.Errorf("notes must close with %q:\n%s", want, stdout)
	}
	v := releaseDryRunJSON(t, 0, "--target", goldenTarget)
	if want := compareLine("1.0.0", goldenTarget); !strings.HasSuffix(v.Body, "\n\n"+want) {
		t.Errorf("the draft body must close with %q:\n%s", want, v.Body)
	}
}

// TestCompareLinkNeedsATagBase: the link is rendered only from a base that
// is a TAG. A whole-history walk has no left side, and both stand-ins lie:
// `v0.0.0` 404s and `HEAD` answers 200 with an empty diff (measured
// 2026-10-05 on glyph: `compare/HEAD...<main sha>` 200, "identical", 0
// commits; `compare/v0.0.0...<main sha>` 404). An explicit base that is no
// tag — a branch, a sha — is resolved by GitHub against GITHUB's refs, not
// the checkout's: `main...<sha>` answered 200 "identical" and
// `origin/main...<sha>` 404 (same day). And `notes --range` names no
// repository and no release base, tagged left side or not. Each absence has
// its positive control on the same fixture: a tag base renders the line.
func TestCompareLinkNeedsATagBase(t *testing.T) {
	assertNoLink := func(t *testing.T, what, body string) {
		t.Helper()
		if strings.Contains(body, linkOpening) {
			t.Errorf("%s carries a compare link it has no tag base for:\n%s", what, body)
		}
	}

	t.Run("a whole-history walk", func(t *testing.T) {
		dir := testRepoUntagged(t)
		root := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		sha := squashCommit(t, dir, "Fix a crash", 8)
		t.Chdir(dir)
		usePR(t, dryServer(t, map[string]string{
			commitPullsPath(root): `[]`,
			commitPullsPath(sha):  `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
			pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
		}))

		code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
		if code != 0 || !strings.Contains(stdout, "fix a crash") {
			t.Fatalf("notes --since-tag exited %d, want 0 with the fix rendered\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertNoLink(t, "notes over the whole history", stdout)
		assertNoLink(t, "release over the whole history", releaseDryRunJSON(t, 0, "--target", goldenTarget).Body)

		testGit(t, dir, "akira-toriyama", "tag", "v0.1.0", root)
		if _, stdout, _ := runGlyph(t, "notes", "--since-tag"); !strings.HasSuffix(stdout, compareLine("v0.1.0", sha)) {
			t.Errorf("positive control: tagged at the root, notes must close with the link:\n%s", stdout)
		}
		if body := releaseDryRunJSON(t, 0, "--target", goldenTarget).Body; !strings.HasSuffix(body, compareLine("v0.1.0", goldenTarget)) {
			t.Errorf("positive control: tagged at the root, the draft must close with the link:\n%s", body)
		}
	})

	t.Run("an explicit base that is no tag", func(t *testing.T) {
		usePR(t, dryServer(t, oneFixWalk(t)))
		head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")
		base := testGit(t, ".", "akira-toriyama", "rev-parse", "v0.1.0")
		testGit(t, ".", "akira-toriyama", "branch", "old-main", base)

		for _, from := range []string{"old-main", base} {
			code, stdout, stderr := runGlyph(t, "notes", "--since-tag="+from)
			if code != 0 || !strings.Contains(stdout, "fix a crash") {
				t.Fatalf("notes --since-tag=%s exited %d, want 0 with the fix rendered\nstdout: %s\nstderr: %s", from, code, stdout, stderr)
			}
			assertNoLink(t, "notes from "+from, stdout)
			assertNoLink(t, "release from "+from, releaseDryRunJSON(t, 0, "--since-tag="+from, "--target", goldenTarget).Body)
		}
		if _, stdout, _ := runGlyph(t, "notes", "--since-tag=v0.1.0"); !strings.HasSuffix(stdout, compareLine("v0.1.0", head)) {
			t.Errorf("positive control: the tag on the same commit must render the link:\n%s", stdout)
		}
	})

	t.Run("notes --range", func(t *testing.T) {
		dir, base := testRepo(t)
		testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "notes", "--range", "v0.1.0..HEAD")
		if code != 0 || !strings.Contains(stdout, "fix a crash") {
			t.Fatalf("notes --range exited %d, want 0 with the fix rendered\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		assertNoLink(t, "notes --range from a tag", stdout)
		code, stdout, _ = runGlyph(t, "notes", "--range", base+"..HEAD")
		if code != 0 {
			t.Fatalf("notes --range exited %d, want 0", code)
		}
		assertNoLink(t, "notes --range from a sha", stdout)
	})
}

// TestCompareLinkNeedsAFullShaEnd: the right side is a full sha or the line
// is not rendered. --target reaches the API verbatim as target_commitish,
// which takes a branch too, and GitHub resolves a ref on the right when the
// page is opened (measured 2026-10-05 on glyph-test: `compare/v2.0.1...main`
// 200 at the branch's head) — so a published body linking `...main` lists
// every later push, commits the release never shipped. An abbreviation is
// held to the same rule: GitHub resolved one down to four digits the same
// day, which lasts only while no second object shares them. The draft's
// target is untouched: the verdict carries what the flag named.
func TestCompareLinkNeedsAFullShaEnd(t *testing.T) {
	t.Run("single line", func(t *testing.T) {
		usePR(t, dryServer(t, oneFixWalk(t)))
		for _, target := range []string{"main", "release/1.x", "cafe1234"} {
			v := releaseDryRunJSON(t, 0, "--target", target)
			if v.Target != target {
				t.Errorf("--target %s: the verdict's target = %q, want the flag's value", target, v.Target)
			}
			if !strings.Contains(v.Body, "fix a crash") || strings.Contains(v.Body, linkOpening) {
				t.Errorf("--target %s is no full sha, so the draft carries its notes and no link:\n%s", target, v.Body)
			}
		}
		for _, target := range []string{goldenTarget, strings.ToUpper(goldenTarget)} {
			if body := releaseDryRunJSON(t, 0, "--target", target).Body; !strings.HasSuffix(body, "\n\n"+compareLine("v0.1.0", target)) {
				t.Errorf("positive control: --target %s is a full sha, so the draft closes with the link:\n%s", target, body)
			}
		}
	})

	t.Run("per line", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)

		lines := func(target string) releaseVerdictLines {
			t.Helper()
			code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json", "--target", target)
			if code != 0 {
				t.Fatalf("release --target %s exited %d, want 0\nstderr: %s", target, code, stderr)
			}
			res := decodeReleaseLines(t, stdout)
			if len(res.Packages) != 2 {
				t.Fatalf("packages = %+v, want both lines", res.Packages)
			}
			return res
		}
		for _, p := range lines("main").Packages {
			if !strings.Contains(p.Body, "## ") || strings.Contains(p.Body, linkOpening) {
				t.Errorf("--target main is no full sha, so %s's draft carries its notes and no link:\n%s", p.Path, p.Body)
			}
		}
		for _, p := range lines(goldenTarget).Packages {
			if !strings.HasSuffix(p.Body, "\n\n"+compareLine(p.Path+"/v0.1.0", goldenTarget)) {
				t.Errorf("positive control: at a full sha %s's draft closes with its link:\n%s", p.Path, p.Body)
			}
		}
	})
}

// TestCompareLinkNeedsNotes: the link closes a NON-EMPTY notes body — the
// condition under which `notes` prints one at all. A draft_on_none
// placeholder over an empty fold carries the marker and nothing else, as
// `notes` over the same walk prints nothing and exits 1; a link there would
// break the machine region's equality with notes' output and leave a stray
// blank line. Its positive control is a placeholder whose notes are not
// empty — a dependabot direct push, which the fold excludes and the
// Dependencies section renders — and that one closes with the link. Per line
// under [[packages]] the same way.
func TestCompareLinkNeedsNotes(t *testing.T) {
	t.Run("single line", func(t *testing.T) {
		usePR(t, dryServer(t, noneWalk(t)))
		enableDraftOnNone(t)

		code, _, _ := runGlyph(t, "notes", "--since-tag")
		if code != 1 {
			t.Fatalf("notes over a fold with nothing to say exited %d, want 1", code)
		}
		if v := releaseDryRunJSON(t, 1, "--target", goldenTarget); v.Tag != "Unreleased" || v.Body != handMarker+"\n\n" {
			t.Errorf("the placeholder over an empty fold = tag %q body %q, want Unreleased carrying the marker alone", v.Tag, v.Body)
		}

		testCommit(t, ".", "dependabot[bot]", "Bump a dep from 1 to 2")
		v := releaseDryRunJSON(t, 1, "--target", goldenTarget)
		if v.Tag != "Unreleased" || !strings.Contains(v.Body, "Bump a dep from 1 to 2") || !strings.HasSuffix(v.Body, "\n\n"+compareLine("v0.1.0", goldenTarget)) {
			t.Errorf("positive control: a placeholder with notes must close them with the link, got tag %q:\n%s", v.Tag, v.Body)
		}
	})

	t.Run("per line", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		sha := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
		t.Chdir(dir)
		enableDraftOnNone(t)
		usePR(t, dryServer(t, map[string]string{commitPullsPath(sha): `[]`}))

		code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json", "--target", goldenTarget)
		if code != 1 {
			t.Fatalf("release exited %d, want 1\nstderr: %s", code, stderr)
		}
		res := decodeReleaseLines(t, stdout)
		if len(res.Packages) != 2 {
			t.Fatalf("packages = %+v, want both lines", res.Packages)
		}
		for _, p := range res.Packages {
			if p.Tag != p.Path+"/Unreleased" || p.Body != handMarker+"\n\n" {
				t.Errorf("%s's placeholder over an empty fold = tag %q body %q, want the marker alone", p.Path, p.Tag, p.Body)
			}
		}

		// Staged by path: touch's `add -A` would commit the draft_on_none
		// rewrite enableDraftOnNone leaves in the working tree.
		writeFile(t, dir, "haiku/go.mod", "module haiku\n")
		testGit(t, dir, "dependabot[bot]", "add", "haiku/go.mod")
		testGit(t, dir, "dependabot[bot]", "commit", "-q", "-m", "Bump a haiku dep from 1 to 2")
		code, stdout, stderr = runGlyph(t, "release", "--dry-run", "--json", "--target", goldenTarget)
		if code != 1 {
			t.Fatalf("release exited %d, want 1\nstderr: %s", code, stderr)
		}
		res = decodeReleaseLines(t, stdout)
		if len(res.Packages) != 2 {
			t.Fatalf("packages = %+v, want both lines", res.Packages)
		}
		h, c := res.Packages[0], res.Packages[1]
		if !strings.Contains(h.Body, "Bump a haiku dep") || !strings.HasSuffix(h.Body, "\n\n"+compareLine("haiku/v0.1.0", goldenTarget)) {
			t.Errorf("positive control: haiku's placeholder has notes and must close them with its link:\n%s", h.Body)
		}
		if c.Body != handMarker+"\n\n" {
			t.Errorf("curry's placeholder over an empty fold must carry the marker alone, got %q", c.Body)
		}
	})
}

// TestCompareLinkEscapesTheBase: git accepts `(`, `)`, `%` and `#` in a tag
// name (git check-ref-format), and pasted verbatim into a URL `#` starts a
// fragment and `%41` decodes to another ref. Each `/`-separated segment is
// path-escaped, so a line's prefix keeps its slash and these are encoded;
// GitHub decodes an encoded segment once, back to the tag (measured
// 2026-10-05 on glyph-test, REST endpoint and page alike, with this test's
// tag on v2.0.1's commit: `compare/rel%281%29%2541%23x...<sha>` 200 with
// v2.0.1's four commits; `%41` left bare 404 as `rel(1)A#x`; pasted verbatim
// 404 as `main...rel(1)A`).
func TestCompareLinkEscapesTheBase(t *testing.T) {
	usePR(t, dryServer(t, oneFixWalk(t)))
	head := testGit(t, ".", "akira-toriyama", "rev-parse", "HEAD")
	const tag = "rel(1)%41#x"
	testGit(t, ".", "akira-toriyama", "tag", tag, "v0.1.0")

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag="+tag)
	if code != 0 {
		t.Fatalf("notes --since-tag=%s exited %d, want 0\nstderr: %s", tag, code, stderr)
	}
	if want := compareLine("rel%281%29%2541%23x", head); !strings.HasSuffix(stdout, "\n\n"+want) {
		t.Errorf("notes must close with %q:\n%s", want, stdout)
	}
}

// TestReleasePackagesCompareLinkPerLine: each line's draft links ITS OWN
// range — `compare/<that line's base tag>...<target>` — the single line's
// rule applied per line, as §4.1 applies every other. Never the union's
// merge base (a sha that reaches back past lines that did not need it) and
// never a sibling's tag: a curry/ tag baselining haiku through the link is
// the cross-line leak the walk itself refuses. Prefixed refs resolve on
// GitHub (measured: t-v7f7 (6) on glyph-monorepo-test, curry/v1.1.0...<sha>
// 200).
func TestReleasePackagesCompareLinkPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, dryServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json", "--target", goldenTarget)
	if code != 0 {
		t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodeReleaseLines(t, stdout)
	if len(res.Packages) != 2 {
		t.Fatalf("packages = %+v, want both lines", res.Packages)
	}
	for i, want := range []struct{ path, own, other string }{
		{"haiku", "haiku/v0.1.0", "curry/"},
		{"curry", "curry/v0.1.0", "haiku/"},
	} {
		p := res.Packages[i]
		if p.Path != want.path {
			t.Fatalf("packages[%d] = %s, want %s", i, p.Path, want.path)
		}
		if !strings.HasSuffix(p.Body, "\n\n"+compareLine(want.own, goldenTarget)) {
			t.Errorf("%s's draft must close with its own line's link from %s:\n%s", p.Path, want.own, p.Body)
		}
		if strings.Contains(p.Body, "/compare/"+want.other) {
			t.Errorf("%s's draft cites the other line's base:\n%s", p.Path, p.Body)
		}
	}
}

// TestNotesPackagesCompareLinkPerLine: `notes --since-tag` closes each line's
// body with that line's link, under its `# <path>` heading, and the bare body
// a tag selecting one line prints (the tag-time rendering a packages
// repository's goreleaser step would run) closes with that line's alone.
func TestNotesPackagesCompareLinkPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, dryServer(t, routes))
	t.Chdir(dir)
	head := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "# haiku\n\n## Features\n\n- :sparkles:(haiku)^ add a season (#7) @akira-toriyama\n\n" + compareLine("haiku/v0.1.0", head) +
		"\n# curry\n\n## Fixes\n\n- :bug:~ swap an ingredient (#7) @akira-toriyama\n\n" + compareLine("curry/v0.1.0", head)
	if stdout != want {
		t.Errorf("notes --since-tag stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag=curry/v0.1.0")
	if code != 0 {
		t.Fatalf("notes --since-tag=curry/v0.1.0 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if want := "## Fixes\n\n- :bug:~ swap an ingredient (#7) @akira-toriyama\n\n" + compareLine("curry/v0.1.0", head); stdout != want {
		t.Errorf("notes --since-tag=curry/v0.1.0 stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestPackagesCompareLinkNeedsATagBase: per line, the single line's rule — a
// line whose walk has no base carries no link, and an explicit base that is
// no tag gives no line one. The positive controls are the tagged lines on the
// same fixtures, and a tag on the same commit as the branch.
func TestPackagesCompareLinkNeedsATagBase(t *testing.T) {
	t.Run("a line with no tag", func(t *testing.T) {
		dir := packagesRepoWithUntaggedLine(t)
		routes := map[string]string{}
		for sha := range strings.FieldsSeq(testGit(t, dir, "akira-toriyama", "log", "--format=%H")) {
			routes[commitPullsPath(sha)] = `[]`
		}
		for _, c := range []struct{ msg, path string }{{":bug:(fish)~ fix the fish", "fish/fish.go"}, {":bug:(haiku)~ fix the haiku", "haiku/haiku.go"}} {
			routes[commitPullsPath(touch(t, dir, "akira-toriyama", c.msg, c.path))] = `[]`
		}
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)

		res := decodeReleaseLines(t, func() string {
			code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--json", "--target", goldenTarget)
			if code != 0 {
				t.Fatalf("release exited %d, want 0\nstderr: %s", code, stderr)
			}
			return stdout
		}())
		bodies := map[string]string{}
		for _, p := range res.Packages {
			bodies[p.Path] = p.Body
		}
		if !strings.Contains(bodies["fish"], "fix the fish") || strings.Contains(bodies["fish"], linkOpening) {
			t.Errorf("fish has no tag, so its draft carries its notes and no link:\n%s", bodies["fish"])
		}
		if !strings.HasSuffix(bodies["haiku"], "\n\n"+compareLine("haiku/v0.1.0", goldenTarget)) {
			t.Errorf("positive control: haiku is tagged, so its draft closes with its link:\n%s", bodies["haiku"])
		}
	})

	t.Run("an explicit base that is no tag", func(t *testing.T) {
		dir, base := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)
		head := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testGit(t, dir, "akira-toriyama", "branch", "old-main", base)
		testGit(t, dir, "akira-toriyama", "tag", "snapshot", base)

		code, stdout, stderr := runGlyph(t, "notes", "--since-tag=old-main")
		if code != 0 || !strings.Contains(stdout, "# curry") {
			t.Fatalf("notes --since-tag=old-main exited %d, want 0 with both lines\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		if strings.Contains(stdout, linkOpening) {
			t.Errorf("a branch base gives no line a link:\n%s", stdout)
		}
		_, stdout, _ = runGlyph(t, "notes", "--since-tag=snapshot")
		if !strings.Contains(stdout, compareLine("snapshot", head)+"\n# curry") || !strings.HasSuffix(stdout, compareLine("snapshot", head)) {
			t.Errorf("positive control: a tag that names no line is every line's base, and each closes with it:\n%s", stdout)
		}
	})
}
