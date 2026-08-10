package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestFirstLine pins the contract firstLine's comment states and every
// exclusion path relies on (range.go, cmd_lint.go, sincetag.go): the subject
// the participation rules match against is the first line with its CR trimmed.
// A CRLF message — what the GitHub API hands back verbatim, unlike git's own
// cleanup — must not present `subject\r` to the prefix rules.
func TestFirstLine(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"LF subject and body":   {":bug: fix a crash\n\nBody.", ":bug: fix a crash"},
		"CRLF subject and body": {"fixup! :bug: fix a crash\r\nBody.", "fixup! :bug: fix a crash"},
		"lone trailing CR":      {":bug: fix a crash\r", ":bug: fix a crash"},
		"single line":           {":bug: fix a crash", ":bug: fix a crash"},
		"empty message":         {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := firstLine(tc.in); got != tc.want {
				t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBumpRangeReturnsEveryViolation pins the accumulating walk: a range with
// three unparsable commits is ONE red run carrying all three, each with its
// stable rule id and fix, not three runs each revealing the next (measured:
// `bump --range --json` returned one commit with no rule id and no details,
// and the caller re-ran lint in a loop to find the rest). The Msg keeps the
// exact `commit <sha>:` head the first failure always had — wedgeHint prefixes
// that string and the wedge needs one commit to point at.
func TestBumpRangeReturnsEveryViolation(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "no gitmoji one")
	testCommit(t, dir, "akira-toriyama", ":bug: a fine commit rides along")
	testCommit(t, dir, "akira-toriyama", "no gitmoji two")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 3 {
		t.Fatalf("bump over unparsable commits exited %d, want 3\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("a refused range wrote a payload: %q", stdout)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if !strings.HasPrefix(env.Message, "commit ") || !strings.Contains(env.Message, "+1 more") {
		t.Errorf("message = %q — the first failure keeps its `commit <sha>:` head (wedgeHint prefixes "+
			"it) and the rest ride as a count", env.Message)
	}
	var details []rangeViolation
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("decoding details: %v\n%s", err, stderr)
	}
	if len(details) != 2 {
		t.Fatalf("details carry %d finding(s), want 2 — every unparsable commit in one answer:\n%s", len(details), stderr)
	}
	shas := map[string]bool{}
	for _, d := range details {
		if d.Rule != "malformed-subject" {
			t.Errorf("finding carries rule %q, want malformed-subject — the walk's refusal now speaks "+
				"lint's machine vocabulary", d.Rule)
		}
		shas[d.SHA] = true
	}
	if len(shas) != 2 {
		t.Errorf("findings anchor %d distinct commit(s), want 2:\n%s", len(shas), stderr)
	}
}

// TestBumpRangeStaysLenientAboutAuthoringRules is the control that keeps the
// accumulation from quietly strictening the walk — §2's ratified split: the
// walk is lenient, authoring is strict. A commit that PARSES but violates the
// authoring rules (uppercase subject, trailing period) must still classify and
// bump; only a message the parser cannot read at all refuses the range.
func TestBumpRangeStaysLenientAboutAuthoringRules(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: Fix The Crash.")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("bump over a parsing-but-unlinted commit exited %d, want 0 — the walk judging "+
			"authoring rules is the strictening §2 ratified against\nstderr: %s", code, stderr)
	}
	if stdout != "v0.1.1\n" {
		t.Fatalf("stdout = %q, want v0.1.1 — the commit classifies as the patch it is", stdout)
	}
}
