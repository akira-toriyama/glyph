package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akira-toriyama/glyph/v4/internal/core"
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
