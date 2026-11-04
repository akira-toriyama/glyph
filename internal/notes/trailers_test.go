package notes

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// The table is the measurement. Every `want` below was read off
// `git interpret-trailers --parse` (git 2.54.0) before the parser answered
// it, not derived from the parser afterwards — which is why the shapes that
// answer NOTHING outnumber the ones that answer something. The silent-total-
// loss cases (a `---` above the block, one prose line inside a block git did
// not write into, an unindented wrap) are the reason this is a parser and not
// a grep; the divider and git-generated rows are the shapes that made the
// parser answer where git did not, or stay silent where git spoke (t-pspd,
// t-hd7d).
func TestTrailers(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want []trailer
	}{
		{
			name: "a block under a body paragraph",
			msg:  "subject\n\nbody.\n\nWhy: a reason\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a trailer in its OWN paragraph above the footer binds nothing",
			msg:  "subject\n\nWhy: a reason\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "an unindented wrap voids the block, credit included",
			msg:  "subject\n\nbody.\n\nWhy: a reason that\nruns on\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "an indented wrap folds with one space",
			msg:  "subject\n\nbody.\n\nWhy: a reason that\n  runs on\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason that runs on"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a tab continuation folds too",
			msg:  "subject\n\nbody.\n\nWhy: a\n\tb\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a b"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a wrap keeps the first line's trailing space inside the fold, as git does",
			msg:  "subject\n\nbody.\n\nWhy: a reason  \n  runs on",
			want: []trailer{{"Why", "a reason   runs on"}},
		},
		{
			name: "a --- above the block hides all of it",
			msg:  "subject\n\nbody\n\n---\n\nWhy: a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a --- inside the block cuts what follows",
			msg:  "subject\n\nWhy: a reason\n---\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}},
		},
		{
			name: "a --- followed by text is the divider too",
			msg:  "subject\n\nbody\n\n--- text\n\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a --- followed by a tab is the divider",
			msg:  "subject\n\nbody\n\n---\tx\n\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a CRLF --- is the divider",
			msg:  "subject\n\nbody\n\n---\r\n\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a --- with a space and a CR is the divider",
			msg:  "subject\n\nbody\n\n--- \r\n\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a --- with two spaces and more dashes is the divider",
			msg:  "subject\n\nbody\n\n---  -- x\n\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "---x is message text, not the divider",
			msg:  "subject\n\nbody\n\n---x\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "---- is message text, not the divider",
			msg:  "subject\n\nbody\n\n----\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a --- followed by a vertical tab is not the divider (git's isspace is ASCII space, tab, CR, LF)",
			msg:  "subject\n\nbody\n\n---\vx\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a --- followed by a form feed is not the divider",
			msg:  "subject\n\nbody\n\n---\fx\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "the fleet footer inside the block voids it",
			msg:  "subject\n\nbody\n\nWhy: a reason\nCo-authored-by: A B <a@b.c>\nGenerated with [Claude Code](https://claude.com/claude-code)",
			want: nil,
		},
		{
			name: "a comment line is tolerated",
			msg:  "subject\n\nbody\n\nWhy: a reason\n# a comment\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a continuation under a comment continues nothing",
			msg:  "subject\n\nbody\n\nWhy: a reason\n# a comment\n  stray\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a token holding a space voids the block",
			msg:  "subject\n\nbody\n\nWhy not: a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a space before the colon is still the token",
			msg:  "subject\n\nbody\n\nWhy : a reason\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a token holding a byte outside letters, digits and hyphens voids the block",
			msg:  "subject\n\nbody\n\nFix#1: a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a token holding a non-ASCII letter voids the block",
			msg:  "subject\n\nbody\n\nRévisé: a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a URL's first colon is not a separator",
			msg:  "subject\n\nbody\n\nhttps://example.com: a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "an equals separator voids the block",
			msg:  "subject\n\nbody\n\nWhy = a reason\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a prose line holding a colon voids the block",
			msg:  "subject\n\nbody\n\nsee https://example.com/x\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a one-paragraph message has no trailers however shaped",
			msg:  "Why: a reason",
			want: nil,
		},
		{
			name: "a trailer glued under the subject is not a block",
			msg:  "subject\nWhy: a reason",
			want: nil,
		},
		{
			name: "an empty value parses, and is the reader's to drop",
			msg:  "subject\n\nbody\n\nWhy:\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", ""}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a repeated token keeps both, in order",
			msg:  "subject\n\nbody\n\nWhy: first\nWhy: second",
			want: []trailer{{"Why", "first"}, {"Why", "second"}},
		},
		{
			name: "trailing space is trimmed off the value",
			msg:  "subject\n\nbody\n\nWhy: a reason   \nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a continuation with nothing above it voids the block",
			msg:  "subject\n\nbody\n\n  orphaned\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a Signed-off-by lets one prose line stand in the block",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "a cherry-pick line does the same, and is itself no trailer",
			msg:  "subj\n\nbody\n\n(cherry picked from commit 1234567890abcdef1234567890abcdef12345678)\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "prose above the Signed-off-by is inside the block too",
			msg:  "subj\n\nbody\n\nthis is prose\nSigned-off-by: A <a@b.c>\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "two trailers carry six prose lines (25%)",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\np1\np2\np3\np4\np5\np6\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "two trailers do not carry seven (under 25%)",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\np1\np2\np3\np4\np5\np6\np7\nCo-authored-by: Eve <e@x.y>",
			want: nil,
		},
		{
			name: "a continuation under prose counts as prose and folds onto nothing",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\nprose line\n  continued\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "a prose line holding a colon is a trailer inside a Signed-off-by block",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\nsee: https://x\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"see", "https://x"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "the git-generated prefix is byte-exact",
			msg:  "subj\n\nbody\n\nsigned-off-by: A <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
			want: nil,
		},
		{
			name: "a Signed-off-by block is still never the title paragraph",
			msg:  "Signed-off-by: A <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := trailers(tc.msg)
			if len(got) != len(tc.want) {
				t.Fatalf("trailers() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("trailer %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCoAuthorNames(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want []string
	}{
		{
			name: "one credit, address removed",
			msg:  "subject\n\nbody\n\nCo-authored-by: Claude Opus 5 (1M context) <noreply@anthropic.com>",
			want: []string{"Claude Opus 5 (1M context)"},
		},
		{
			name: "two credits keep message order",
			msg:  "subject\n\nbody\n\nCo-authored-by: A B <a@b.c>\nCo-authored-by: C D <c@d.e>",
			want: []string{"A B", "C D"},
		},
		{
			name: "the token matches case-insensitively",
			msg:  "subject\n\nbody\n\nCo-Authored-By: A B <a@b.c>",
			want: []string{"A B"},
		},
		{
			name: "a credit with no name is dropped, never rendered as its address",
			msg:  "subject\n\nbody\n\nCo-authored-by: <a@b.c>",
			want: nil,
		},
		{
			name: "a credit with no address keeps its whole value",
			msg:  "subject\n\nbody\n\nCo-authored-by: A B",
			want: []string{"A B"},
		},
		{
			name: "an empty credit is dropped",
			msg:  "subject\n\nbody\n\nCo-authored-by:\nWhy: a reason",
			want: nil,
		},
		{
			name: "a co-author inside a body paragraph credits nobody",
			msg:  "subject\n\nCo-authored-by: A B <a@b.c>\n\nplain prose closes the message",
			want: nil,
		},
		{
			name: "a co-author beside a Signed-off-by and a prose line is credited",
			msg:  "subject\n\nbody\n\nSigned-off-by: A B <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
			want: []string{"Eve"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := coAuthorNames(tc.msg)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("coAuthorNames() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrailerValueTakesTheLastMatch(t *testing.T) {
	msg := "subject\n\nbody\n\nWhy: the first reason\nWhy: the corrected reason"
	if got := trailerValue(msg, "why"); got != "the corrected reason" {
		t.Errorf("trailerValue() = %q, want the LAST match", got)
	}
	if got := trailerValue(msg, "nosuch"); got != "" {
		t.Errorf("trailerValue() for an absent token = %q, want empty", got)
	}
}

// differentialHeads and differentialBlocks are the shapes the two
// differentials below cross: every head above every block, closed by every
// tail, so combinations nobody thought to write down are covered too. The
// heads carry the divider spellings (a bare `---`, `---` with text, a tab,
// a CR, and the `---x` and `----` that are message text); the blocks carry
// the strict shapes and the git-generated ones.
var differentialHeads = []string{
	"subject\n\nbody.\n\n",
	"subject\n\nbody.\n\nmore body.\n\n",
	"subject\n\n",
	"subject\n\nbody\n\n---\n\n",
	"subject\n\nbody\n\n--- text\n\n",
	"subject\n\nbody\n\n---\tx\n\n",
	"subject\n\nbody\n\n---\r\n\n",
	"subject\n\nbody\n\n---x\n\n",
	"subject\n\nbody\n\n----\n\n",
	"", // no paragraph before: the block would be the subject
}

var differentialBlocks = []string{
	"Why: a reason",
	"Why: a reason\nCo-authored-by: A B <a@b.c>",
	"Co-authored-by: A B <a@b.c>\nCo-authored-by: C D <c@d.e>",
	"Why: a reason that\n  wraps politely",
	"Why: a reason that\nwraps rudely",
	"Why: a reason  \n  wraps after trailing space",
	"Why: a reason\n# a comment\nCo-authored-by: A B <a@b.c>",
	"Why: a reason\n# a comment\n  stray\nCo-authored-by: A B <a@b.c>",
	"Why not: a reason",
	"Why : a reason\nCo-authored-by: A B <a@b.c>",
	"Why = a reason",
	"Fix#1: a reason\nCo-authored-by: A B <a@b.c>",
	"Révisé: a reason\nCo-authored-by: A B <a@b.c>",
	"https://example.com: a reason\nCo-authored-by: A B <a@b.c>",
	"see https://example.com/x\nCo-authored-by: A B <a@b.c>",
	"Why:\nCo-authored-by: <a@b.c>",
	"  orphaned continuation",
	"Why: first\nWhy: second",
	"Signed-off-by: A B <a@b.c>\nWhy: a reason",
	"Signed-off-by: A B <a@b.c>\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
	"(cherry picked from commit 1234567890abcdef1234567890abcdef12345678)\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
	"this is prose\nSigned-off-by: A B <a@b.c>\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\np1\np2\np3\np4\np5\np6\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\np1\np2\np3\np4\np5\np6\np7\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nprose line\n  continued\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nsee: https://x\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\n# a comment\n  stray\nCo-authored-by: Eve <e@x.y>",
	"  orphaned\nSigned-off-by: A B <a@b.c>\nCo-authored-by: Eve <e@x.y>",
	"signed-off-by: A B <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nWhy not: a reason\nCo-authored-by: Eve <e@x.y>",
}

var differentialTails = []string{"", "\n", "\n\n"}

// TestTrailersAreASubsetOfGit is the oracle. Modelling git's rule and then
// testing the model against itself proves nothing, so the real parser is
// asked the same messages git is, and the invariant asserted is the one that
// matters: glyph may report FEWER trailers than git, NEVER a trailer git does
// not report. Over-reporting is how a person ends up named on a public
// release page as an author of code the commit never claimed.
func TestTrailersAreASubsetOfGit(t *testing.T) {
	testutil.RequireGit(t)

	// Outside any repository, so no repository config answers either.
	dir := t.TempDir()
	checked := 0
	for _, h := range differentialHeads {
		for _, b := range differentialBlocks {
			for _, tl := range differentialTails {
				msg := h + b + tl
				oracle := gitTrailers(t, dir, msg)
				mine := trailers(msg)
				checked++

				// The subset invariant, asserted per entry so a failure names
				// the exact line glyph invented.
				for _, got := range mine {
					if !containsTrailer(oracle, got) {
						t.Errorf("glyph reported a trailer git does not, for %q:\n  glyph: %+v\n  git:   %+v", msg, got, oracle)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the differential ran no cases")
	}
}

// TestTrailersMatchGitOnAGitGeneratedBlock is the other half for the one
// rule where under-reporting was the defect (t-hd7d): a block holding
// `Signed-off-by: ` or `(cherry picked from commit ` keeps its trailers past
// a prose line, and glyph reported none of them, so `git commit -s` and
// `git cherry-pick -x` authors lost every credit. Here the answer must EQUAL
// git's on every git-generated block the differential crosses, not merely
// stay inside it — a parser that returns nil passes the subset test
// (mutation row signed-off-block-voided-by-one-prose-line).
func TestTrailersMatchGitOnAGitGeneratedBlock(t *testing.T) {
	testutil.RequireGit(t)

	dir := t.TempDir()
	checked, spoke := 0, 0
	for _, h := range differentialHeads {
		for _, b := range differentialBlocks {
			if !strings.Contains(b, "Signed-off-by: ") && !strings.Contains(b, "(cherry picked from commit ") {
				continue
			}
			for _, tl := range differentialTails {
				msg := h + b + tl
				oracle := gitTrailers(t, dir, msg)
				mine := trailers(msg)
				checked++
				if len(oracle) > 0 {
					spoke++
				}
				if len(mine) != len(oracle) {
					t.Errorf("for %q:\n  glyph: %+v\n  git:   %+v", msg, mine, oracle)
					continue
				}
				for i := range mine {
					if !strings.EqualFold(mine[i].Token, oracle[i].Token) || mine[i].Value != oracle[i].Value {
						t.Errorf("entry %d for %q:\n  glyph: %+v\n  git:   %+v", i, msg, mine[i], oracle[i])
					}
				}
			}
		}
	}
	if checked == 0 || spoke == 0 {
		t.Fatalf("the differential ran %d git-generated cases of which git answered %d — a positive control is required", checked, spoke)
	}
}

// gitTrailers asks git what the message's trailers are. `--only-trailers`
// keeps the answer to the block itself, and `--unfold` folds continuations
// the way this package does, so the two answers are comparable.
//
// The oracle is git with no config but its defaults: trailer.separators and
// trailer.<token>.* change what interpret-trailers answers, and a personal
// `trailer.separators = "#"` failed this test with glyph's parser untouched.
// glyph's notes never read that config, so the defaults are the comparison.
func gitTrailers(t *testing.T, dir, msg string) []trailer {
	t.Helper()
	cmd := exec.Command("git", "interpret-trailers", "--parse", "--only-trailers", "--unfold")
	cmd.Dir = dir
	cmd.Env = testutil.GitEnv("akira-toriyama")
	cmd.Stdin = strings.NewReader(msg + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git interpret-trailers: %v", err)
	}
	var got []trailer
	for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if l == "" {
			continue
		}
		token, rest, ok := splitTrailer(l)
		if !ok {
			continue
		}
		got = append(got, trailer{Token: token, Value: strings.Trim(rest, gitSpace)})
	}
	return got
}

func containsTrailer(in []trailer, want trailer) bool {
	for _, t := range in {
		if strings.EqualFold(t.Token, want.Token) && t.Value == want.Value {
			return true
		}
	}
	return false
}
