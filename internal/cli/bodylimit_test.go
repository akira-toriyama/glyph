package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/preview"
)

// TestReleaseBodyCapBoundary pins the measured cap at its exact edge, in the
// measured UNIT: 125000 is a character count, so a body of 125000 two-byte
// runes — twice the cap in bytes — must pass, and a len() reading of the guard
// fails here. The over-by-one case must name both numbers, because the message
// is what a release job's log shows the operator.
func TestReleaseBodyCapBoundary(t *testing.T) {
	if err := checkReleaseBody(strings.Repeat("x", releaseBodyMaxChars)); err != nil {
		t.Fatalf("a body of exactly %d chars must pass, got %v", releaseBodyMaxChars, err)
	}
	if err := checkReleaseBody(strings.Repeat("é", releaseBodyMaxChars)); err != nil {
		t.Fatalf("the cap is characters, not bytes (measured): %d two-byte runes must pass, got %v", releaseBodyMaxChars, err)
	}
	err := checkReleaseBody(strings.Repeat("x", releaseBodyMaxChars+1))
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("one char over must refuse with the API/refusal code, got %v", err)
	}
	if !strings.Contains(ce.Msg, "125001") || !strings.Contains(ce.Msg, "125000") {
		t.Fatalf("the refusal must name the size and the cap: %q", ce.Msg)
	}
}

// TestCommentTruncationBoundary: under and at the cap the body passes through
// byte-identical and silent; over it, the result fits the cap, ends in the
// notice, cuts at a line boundary, and says so on the diagnostic stream.
func TestCommentTruncationBoundary(t *testing.T) {
	var errBuf bytes.Buffer
	oldErr := errOut
	errOut = &errBuf
	defer func() { errOut = oldErr }()

	small := strings.Repeat("line\n", 10)
	if got := truncateComment(small); got != small {
		t.Fatalf("an under-cap body must pass through byte-identical")
	}
	exact := strings.Repeat("x", commentBodyMaxChars)
	if got := truncateComment(exact); got != exact {
		t.Fatalf("a body of exactly %d chars must pass through untouched", commentBodyMaxChars)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("no warning may fire below the cap: %q", errBuf.String())
	}

	over := strings.Repeat("0123456789012345678901234567890123456789\n", 2000) // 82000 chars
	got := truncateComment(over)
	if n := utf8.RuneCountInString(got); n > commentBodyMaxChars {
		t.Fatalf("truncated body is %d chars, still over the %d cap", n, commentBodyMaxChars)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("the cut must be marked in the comment itself:\n%.200s", got)
	}
	head := got[:strings.Index(got, "\n\n---\n\n")]
	if !strings.HasSuffix(head, "9") || strings.HasSuffix(head, "\n") {
		// The kept text must end at the end of a fixture line — a mid-line cut
		// would leave a half construct ahead of the notice.
		t.Fatalf("the cut is not at a line boundary: kept text ends %q", head[len(head)-20:])
	}
	if !strings.Contains(errBuf.String(), "::warning::") || !strings.Contains(errBuf.String(), "65536") {
		t.Fatalf("the truncation must be announced on stderr with the cap named: %q", errBuf.String())
	}
}

// foldLines counts the lines of body that open and that close a <details>
// block — whole lines, the only form the renderer writes them in.
func foldLines(body string) (opened, closed int) {
	for line := range strings.SplitSeq(body, "\n") {
		switch line {
		case "<details>":
			opened++
		case "</details>":
			closed++
		}
	}
	return opened, closed
}

// TestCommentTruncationClosesTheFoldItCutsInside: the notes preview is the
// body's last section and sits in a <details> block, so a comment over the cap
// whose tables fit under it is cut INSIDE that block (t-rrw0 (5)). Cut at a
// line boundary and no more, the posted comment held one <details> and no
// </details>: GitHub closes the block at the end of the comment, so the
// truncation notice — there because a cut preview otherwise reads as a whole
// one — is rendered inside it, folded away with the notes (asked of GitHub's
// renderer, DESIGN §4). The cut closes what it leaves open, and the notice
// follows it.
//
// The body is the renderer's own (preview.Render), not a string typed to
// look like one; TestCommentTruncationBoundary's fixture is flat and cannot
// see this. Mutation row preview-truncation-leaves-the-notes-fold-open.
func TestCommentTruncationClosesTheFoldItCutsInside(t *testing.T) {
	var errBuf bytes.Buffer
	oldErr := errOut
	errOut = &errBuf
	defer func() { errOut = oldErr }()

	const notice = "\n\n---\n\n… truncated: "
	var notes strings.Builder
	notes.WriteString("## Fixes\n\n")
	for i := range 2000 {
		fmt.Fprintf(&notes, "- :bug:~ fix the crash numbered %04d @akira-toriyama\n", i)
	}
	body := preview.Render(preview.Input{
		Current: "v1.0.0",
		PR:      preview.Verdict{Level: bump.LevelPatch, Next: "v1.0.1", Commits: []preview.Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "fix a crash"}}},
		Pending: preview.Verdict{Level: bump.LevelNone},
		Notes:   notes.String(),
	})
	opens, closes := strings.Index(body, "\n<details>\n"), strings.Index(body, "\n</details>\n")
	if opens < 0 || opens > 1000 || closes < commentBodyMaxChars || !strings.HasSuffix(body, "Pushing more commits updates this comment.\n") {
		t.Fatalf("positive control: the fixture must open its notes block early and close it past the cap, the footer after it (opens at %d, closes at %d of %d)", opens, closes, len(body))
	}

	got := truncateComment(body)
	if n := utf8.RuneCountInString(got); n > commentBodyMaxChars || n < commentBodyMaxChars-200 {
		t.Fatalf("the truncated body is %d chars: it must fit the %d cap, and use it", n, commentBodyMaxChars)
	}
	if opened, closed := foldLines(got); opened != 1 || closed != 1 {
		t.Fatalf("the truncated comment opens %d <details> block(s) and closes %d — an open one swallows everything after it, the truncation notice included:\n…%s", opened, closed, got[len(got)-300:])
	}
	kept, rest, cut := strings.Cut(got, "\n\n</details>")
	if !cut || !strings.HasPrefix(rest, notice) || strings.Contains(rest, "<details>") {
		t.Fatalf("the notice must follow the closed block, outside it:\n…%s", got[len(got)-300:])
	}
	if !strings.HasPrefix(body, kept+"\n") || !strings.HasSuffix(kept, "@akira-toriyama") {
		t.Fatalf("what is kept must be the body's own text up to the end of a notes line, got …%q", kept[len(kept)-80:])
	}

	// The closer counts against the cap. Padding the subject a character at a
	// time walks the cut's line end across every distance from the budget, the
	// dozen shorter than a closer among them: there a closer appended to a head
	// that already spent the budget posts a comment GitHub answers 422, and the
	// verdict is missing on exactly the oversized pulls (mutation row
	// preview-truncation-closers-overflow-the-cap). The budget is read off the
	// notice as posted, so a reworded notice cannot empty the sweep, and
	// `tight` is its positive control.
	budget := commentBodyMaxChars - utf8.RuneCountInString(got[strings.Index(got, notice):])
	tight := 0
	for pad := range 64 {
		padded := strings.Replace(body, "fix a crash", "fix a crash"+strings.Repeat("x", pad), 1)
		if budget-utf8.RuneCountInString(cutAtLine(padded, budget)) < utf8.RuneCountInString(detailsCloser) {
			tight++
		}
		out := truncateComment(padded)
		opened, closed := foldLines(out)
		if n := utf8.RuneCountInString(out); n > commentBodyMaxChars || opened != 1 || closed != 1 {
			t.Fatalf("with the subject padded by %d the truncated comment is %d chars against the %d cap, %d block(s) opened and %d closed", pad, n, commentBodyMaxChars, opened, closed)
		}
	}
	if tight == 0 {
		t.Fatalf("positive control: no padding left the first cut within a closer of the budget, so the sweep never asked whether the closer fits")
	}

	// Depth, not a flag: every block open at the cut is closed, and one closed
	// before it is left alone.
	long := strings.Repeat("0123456789012345678901234567890123456789\n", 2000)
	nested := truncateComment("<details>\n<details>\ninner\n\n</details>\n<details>\n" + long + "\n</details>\n\n</details>\n")
	if opened, closed := foldLines(nested); opened != 3 || closed != 3 || utf8.RuneCountInString(nested) > commentBodyMaxChars {
		t.Fatalf("two blocks are open at the cut and one closed before it: got %d opened, %d closed, %d chars", opened, closed, utf8.RuneCountInString(nested))
	}
	if !strings.HasSuffix(strings.SplitN(nested, notice, 2)[0], "9\n\n</details>\n\n</details>") {
		t.Fatalf("both open blocks must close ahead of the notice:\n…%s", nested[len(nested)-300:])
	}
	shut := truncateComment("<details>\ninner\n\n</details>\n" + long)
	if opened, closed := foldLines(shut); opened != 1 || closed != 1 || !strings.HasSuffix(strings.SplitN(shut, notice, 2)[0], "9") {
		t.Fatalf("a block closed before the cut gets no second closer: %d opened, %d closed", opened, closed)
	}
}

// TestCutAtLineKeepsEveryLineThatFits: the cut keeps whole lines only, and a
// line that ends exactly where the budget does is whole — it fit. Backing up
// from that boundary as well dropped a line the cap had room for, and
// truncateComment's second pass, which makes room for the closers, can land on
// it again. A cut inside a line still backs up to the end of the line before.
func TestCutAtLineKeepsEveryLineThatFits(t *testing.T) {
	const body = "ab\ncd\nef\n"
	for budget, want := range map[int]string{
		0:   "",
		4:   "ab",
		5:   "ab\ncd",
		6:   "ab\ncd",
		7:   "ab\ncd",
		8:   "ab\ncd\nef",
		9:   body,
		100: body,
	} {
		if got := cutAtLine(body, budget); got != want {
			t.Errorf("cutAtLine(%q, %d) = %q, want %q", body, budget, got, want)
		}
	}
	if got := cutAtLine("é\né\n", 1); got != "é" {
		t.Errorf("the budget is characters, and a two-byte line that fits is kept: got %q", got)
	}
}

// TestPreviewCutInsideTheNotesStillClosesTheFold is the same decision end to
// end: `preview --notes` over a pull whose table fits the cap and whose notes
// do not posts a comment that closes its notes block and says it was cut,
// outside the block — on stdout and in the --json body the workflow posts.
func TestPreviewCutInsideTheNotesStillClosesTheFold(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	commits := make([]string, 0, 200)
	for i := range 200 {
		commits = append(commits, apiCommit(fmt.Sprintf("c%03d", i), "akira-toriyama", fmt.Sprintf(":bug:~ fix crash %03d %s", i, strings.Repeat("y", 200))))
	}
	usePR(t, prServer(t, 7, `[`+strings.Join(commits, ",")+`]`))

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--notes")
	if code != 0 {
		t.Fatalf("preview must truncate, not refuse: exit %d\n%s", code, stderr)
	}
	summary, closer, notice := strings.Index(stdout, "<summary>Release notes preview</summary>"), strings.Index(stdout, "\n\n</details>\n"), strings.Index(stdout, "… truncated: ")
	if summary < 0 || closer < summary || notice < closer {
		t.Fatalf("the cut must fall inside the notes block, the block must close, and the notice must follow it (summary at %d, closer at %d, notice at %d):\n…%s", summary, closer, notice, stdout[len(stdout)-400:])
	}
	if opened, closed := foldLines(stdout); opened != 1 || closed != 1 {
		t.Fatalf("stdout opens %d <details> block(s) and closes %d", opened, closed)
	}

	code, stdout, stderr = runGlyph(t, "preview", "--pr", "7", "--notes", "--json")
	if code != 0 {
		t.Fatalf("preview --json exited %d\n%s", code, stderr)
	}
	var res struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("preview --json stdout is not JSON: %v", err)
	}
	if opened, closed := foldLines(res.Body); opened != 1 || closed != 1 || utf8.RuneCountInString(res.Body) > commentBodyMaxChars {
		t.Fatalf("the --json body opens %d <details> block(s) and closes %d, at %d chars", opened, closed, utf8.RuneCountInString(res.Body))
	}
}

// TestReleaseRefusesABodyGitHubRejects wires the guard end to end: a walk whose
// notes compose past the release cap must refuse at exit 4 BEFORE any write —
// without the guard this run computes its verdict, spends the retry schedule on
// a POST GitHub always 422s, and exits 4 anyway, one draft later and none the
// wiser. The subject is a single oversized one because that is the measured
// hole: subject length is unbounded on every input path.
func TestReleaseRefusesABodyGitHubRejects(t *testing.T) {
	var writes []apiWrite
	dir, _ := testRepo(t)
	sha := squashCommit(t, dir, "Fix a crash", 8)
	t.Chdir(dir)
	walk := map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(8):   `[` + apiCommit("b1", "akira-toriyama", ":bug:~ "+strings.Repeat("x", releaseBodyMaxChars+100)) + `]`,
	}
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)

	code, _, stderr := runGlyph(t, "release")
	if code != 4 {
		t.Fatalf("an over-cap body must refuse at exit 4, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "125000") {
		t.Fatalf("the refusal must name the cap: %s", stderr)
	}
	if len(writes) != 0 {
		t.Fatalf("the refusal must land before any write, got %+v", writes)
	}
}

// TestReleaseBodyCapCountsTheCompareLink: the guard sizes the FINAL body, the
// compare link included (t-v7f7 (9)). The footer is padded until the body
// without the link sits exactly at the cap — the control, read at a --target
// that is no full sha and so renders none — and the same footer at a full
// sha must then refuse before any write. Sized ahead of the link, that run
// passes the guard and spends itself on the write GitHub 422s. Per line under
// [[packages]] the same way: each draft is sized on its own, link included.
func TestReleaseBodyCapCountsTheCompareLink(t *testing.T) {
	refusedOverTheLink := func(t *testing.T, writes *[]apiWrite, longest func(args ...string) int) {
		t.Helper()
		pad := releaseBodyMaxChars - longest("--footer-file", writeFooter(t, "y\n")) + 1
		footer := writeFooter(t, strings.Repeat("y", pad)+"\n")
		if n := longest("--footer-file", footer); n != releaseBodyMaxChars {
			t.Fatalf("positive control: without the link the padded body must sit at the cap, got %d characters", n)
		}

		code, _, stderr := runGlyph(t, "release", "--footer-file", footer, "--target", goldenTarget)
		if code != 4 {
			t.Fatalf("a body the compare link takes over the cap must refuse at exit 4, got %d\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "125000") {
			t.Errorf("the refusal must name the cap: %s", stderr)
		}
		if len(*writes) != 0 {
			t.Errorf("the refusal must land before any write, got %+v", *writes)
		}
	}

	t.Run("single line", func(t *testing.T) {
		var writes []apiWrite
		usePR(t, releaseServer(t, oneFixWalk(t), `[]`, &writes))
		refusedOverTheLink(t, &writes, func(args ...string) int {
			return utf8.RuneCountInString(releaseDryRunJSON(t, 0, append([]string{"--target", "main"}, args...)...).Body)
		})
	})

	t.Run("per line", func(t *testing.T) {
		var writes []apiWrite
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		usePR(t, releaseServer(t, routes, `[]`, &writes))
		t.Chdir(dir)
		refusedOverTheLink(t, &writes, func(args ...string) int {
			code, stdout, stderr := runGlyph(t, append([]string{"release", "--dry-run", "--json", "--target", "main"}, args...)...)
			if code != 0 {
				t.Fatalf("release --dry-run %v exited %d, want 0\nstderr: %s", args, code, stderr)
			}
			n := 0
			for _, p := range decodeReleaseLines(t, stdout).Packages {
				n = max(n, utf8.RuneCountInString(p.Body))
			}
			return n
		})
	})
}

// TestPreviewCommentStaysUnderTheCap wires the other half end to end, with the
// opposite policy: the sticky comment is advisory, so an oversized preview is
// truncated (marked, warned) rather than refused — and the JSON body the
// workflow actually posts is the truncated one.
func TestPreviewCommentStaysUnderTheCap(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	body := `[` + apiCommit("aaa1111", "akira-toriyama", ":sparkles:^ "+strings.Repeat("y", commentBodyMaxChars+100)) + `]`
	usePR(t, prServer(t, 7, body))

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--notes")
	if code != 0 {
		t.Fatalf("preview must truncate, not refuse: exit %d\n%s", code, stderr)
	}
	if n := utf8.RuneCountInString(stdout); n > commentBodyMaxChars {
		t.Fatalf("preview body is %d chars, over the %d comment cap", n, commentBodyMaxChars)
	}
	if !strings.Contains(stdout, "truncated") {
		t.Fatalf("the cut must be marked in the body:\n%.200s", stdout)
	}
	if !strings.Contains(stderr, "::warning::") {
		t.Fatalf("the truncation must be announced: %s", stderr)
	}
}
