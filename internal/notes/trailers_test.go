package notes

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// The table is the measurement. Every `want` below was read off git's commit
// reader — `git log --format='%(trailers:only,unfold)'`, git 2.54.0 — before
// the parser answered it, and the test re-reads each row off git on every run
// (gitTrailersBatch): a row that pins an answer git does not give fails by
// name, which is how the URL row that pinned a guard git has none of was
// found (review of t-hd7d, 2026-11-08). The silent-total-loss cases (one
// prose line inside a block git did not write into, an unindented wrap) are
// the reason this is a parser and not a grep; the `---` rows are the shapes
// `git interpret-trailers --parse` answers differently, because it reads a
// patch mail where `---` starts the diff — the commit reader, and glyph,
// apply no divider (t-pspd, re-ruled 2026-11-08).
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
			name: "a --- line above the block is message text, and the block below it is read",
			msg:  "subject\n\nbody\n\n---\n\nWhy: a reason\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a --- line inside the block is a prose line, and voids it",
			msg:  "subject\n\nWhy: a reason\n---\nCo-authored-by: A B <a@b.c>",
			want: nil,
		},
		{
			name: "a --- followed by text hides nothing (interpret-trailers' patch divider, not the commit reader's)",
			msg:  "subject\n\nbody\n\n--- text\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a --- followed by a tab hides nothing",
			msg:  "subject\n\nbody\n\n---\tx\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a CRLF --- hides nothing",
			msg:  "subject\n\nbody\n\n---\r\n\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a wrapped body line opening with --- hides nothing (glyph e2b2bec's shape)",
			msg:  "subject\n\nbody that wraps onto a\n--- line like e2b2bec\n\nCo-authored-by: Probe <p@x.y>\n",
			want: []trailer{{"Co-authored-by", "Probe <p@x.y>"}},
		},
		{
			name: "a CRLF squash with a YAML --- and GitHub's --------- separator keeps its sign-off and credit (AzooKey 55ffe3c's shape)",
			msg:  "subject\r\n\r\n---\r\nkey: value\r\n\r\n* a bullet\r\n\r\n---------\r\n\r\nSigned-off-by: dependabot[bot] <support@github.com>\r\nCo-authored-by: dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>",
			want: []trailer{{"Signed-off-by", "dependabot[bot] <support@github.com>"}, {"Co-authored-by", "dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>"}},
		},
		{
			name: "a scissors line ends the message, and the block above it is read",
			msg:  "subject\n\nbody\n\nWhy: a reason\n\n# ------------------------ >8 ------------------------\n\nCo-authored-by: Mallory <m@x.y>\n",
			want: []trailer{{"Why", "a reason"}},
		},
		{
			name: "a scissors line opening the message leaves no message",
			msg:  "# ------------------------ >8 ------------------------\n\nCo-authored-by: Mallory <m@x.y>\n",
			want: nil,
		},
		{
			name: "a scissors line needs git's exact spelling: one dash short is a comment line in the block",
			msg:  "subject\n\nbody\n\nWhy: a reason\n# ----------------------- >8 ------------------------\nCo-authored-by: A B <a@b.c>\n",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a trailing Conflicts list is not message, so the block above it is read",
			msg:  "subject\n\nbody\n\nCo-authored-by: Peggy <p@x.y>\n\nConflicts:\n\tf1\n",
			want: []trailer{{"Co-authored-by", "Peggy <p@x.y>"}},
		},
		{
			name: "a Conflicts list glued under a 25% block does not count toward its trailers",
			msg:  "subject\n\nbody\n\nSigned-off-by: S <s@x.y>\np1\np2\np3\np4\np5\np6\np7\nCo-authored-by: Trent <t@x.y>\nConflicts:\n\tf1\n",
			want: nil,
		},
		{
			name: "Conflicts: with no newline after it is an ordinary empty trailer",
			msg:  "subject\n\nbody\n\nWhy: a reason\nConflicts:",
			want: []trailer{{"Why", "a reason"}, {"Conflicts", ""}},
		},
		{
			name: "a CRLF Conflicts: line is not the list, but a trailer with its paths folded in",
			msg:  "subject\n\nbody\n\nCo-authored-by: P <p@x.y>\n\nConflicts:\r\n\tf1\n",
			want: []trailer{{"Conflicts", "f1"}},
		},
		{
			name: "trailing comment lines and empty lines are not message",
			msg:  "subject\n\nbody\n\nWhy: a reason\n\n# a trailing comment\n\n\n",
			want: []trailer{{"Why", "a reason"}},
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
			name: "a URL's first colon is the separator, as git reads it — there is no URL rule",
			msg:  "subject\n\nbody\n\nhttps://example.com: a reason\nCo-authored-by: A B <a@b.c>",
			want: []trailer{{"https", "//example.com: a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
		},
		{
			name: "a bare URL line is the trailer `https`, and the credit beside it stands",
			msg:  "subject\n\nbody\n\nhttps://github.com/o/r/issues/1\nCo-authored-by: Alice <alice@example.com>\n",
			want: []trailer{{"https", "//github.com/o/r/issues/1"}, {"Co-authored-by", "Alice <alice@example.com>"}},
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
			name: "a CRLF message reads as its LF twin",
			msg:  "subject\r\n\r\nbody\r\n\r\nWhy: a reason\r\nCo-authored-by: A B <a@b.c>\r\n",
			want: []trailer{{"Why", "a reason"}, {"Co-authored-by", "A B <a@b.c>"}},
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
			name: "a continuation under prose counts toward the 25% line: four prose lines each wrapped once are eight",
			msg:  "subj\n\nbody\n\nSigned-off-by: S <s@x.y>\np1\n cont\np2\n cont\np3\n cont\np4\n cont\nCo-authored-by: A <a@b.c>\n",
			want: nil,
		},
		{
			name: "a prose line holding a colon is a trailer inside a Signed-off-by block",
			msg:  "subj\n\nbody\n\nSigned-off-by: A <a@b.c>\nsee: https://x\nCo-authored-by: Eve <e@x.y>",
			want: []trailer{{"Signed-off-by", "A <a@b.c>"}, {"see", "https://x"}, {"Co-authored-by", "Eve <e@x.y>"}},
		},
		{
			name: "a BREAKING CHANGE footer is prose, and a Signed-off-by beside it keeps the block",
			msg:  "subj\n\nbody\n\nBREAKING CHANGE: the old flag is gone\nSigned-off-by: P <p@x.y>\nCo-authored-by: Victor <v@x.y>\n",
			want: []trailer{{"Signed-off-by", "P <p@x.y>"}, {"Co-authored-by", "Victor <v@x.y>"}},
		},
		{
			name: "a BREAKING CHANGE footer in a block git did not write into voids it",
			msg:  "subj\n\nbody\n\nBREAKING CHANGE: the old flag is gone\nCo-authored-by: Victor <v@x.y>\n",
			want: nil,
		},
		{
			name: "the git-generated prefix is byte-exact in case",
			msg:  "subj\n\nbody\n\nsigned-off-by: A <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
			want: nil,
		},
		{
			name: "the git-generated prefix is byte-exact in its trailing space: Signed-off-by:S is an ordinary trailer",
			msg:  "subj\n\nbody\n\nSigned-off-by:S <s@x.y>\nprose\nCo-authored-by: A <a@b.c>\n",
			want: nil,
		},
		{
			name: "a Signed-off-by block is still never the title paragraph",
			msg:  "Signed-off-by: A <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
			want: nil,
		},
	}

	msgs := make([]string, len(tests))
	for i, tc := range tests {
		msgs[i] = tc.msg
	}
	oracle := gitTrailersBatch(t, msgs)

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !sameTrailers(tc.want, oracle[i]) {
				t.Fatalf("the row pins %+v, and git's commit reader reads %+v — the table is the measurement, so the row is wrong, not git", tc.want, oracle[i])
			}
			got := trailers(tc.msg)
			if !sameTrailers(got, tc.want) {
				t.Fatalf("trailers() = %+v, want %+v", got, tc.want)
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
		{
			name: "a co-author below a --- line is credited, as git's commit reader and GitHub credit it",
			msg:  "subject\n\nbody\n\n--- (和訳)\nprose\n\nCo-authored-by: Claude Fable 5 <noreply@anthropic.com>",
			want: []string{"Claude Fable 5"},
		},
		{
			name: "a co-author below a scissors line credits nobody",
			msg:  "subject\n\nbody\n\n# ------------------------ >8 ------------------------\n\nCo-authored-by: Mallory <m@x.y>\n",
			want: nil,
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

// differentialHeads, differentialBlocks and differentialTails are the shapes
// the differential crosses: every head above every block, closed by every
// tail, so combinations nobody thought to write down are covered too. The
// heads carry the `---` spellings interpret-trailers reads as a divider and
// the commit reader does not, a scissors line, a CRLF paragraph and a
// comment title; the blocks carry the strict shapes and the git-generated
// ones; the tails carry what the commit reader ignores at the end of a
// message — empty lines, comment lines, a Conflicts list, a scissors line —
// and the near-misses it does not.
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
	"subject\n\nbody that wraps onto a\n--- line\n\n",
	"subject\r\n\r\nbody\r\n\r\n",
	"# a comment title\n\n",
	"\n",
	"subject\n\nbody\n\n# ------------------------ >8 ------------------------\nbelow the cut\n\n",
	"subject\n\nConflicts:\n\tf1\n\n",
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
	"https://github.com/o/r/issues/1\nCo-authored-by: A B <a@b.c>",
	"see https://example.com/x\nCo-authored-by: A B <a@b.c>",
	"Why:\nCo-authored-by: <a@b.c>",
	"  orphaned continuation",
	"Why: first\nWhy: second",
	"---\nCo-authored-by: A B <a@b.c>",
	"Co-authored-by: A B <a@b.c>\n--- text",
	"Signed-off-by: A B <a@b.c>\nWhy: a reason",
	"Signed-off-by: A B <a@b.c>\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
	"(cherry picked from commit 1234567890abcdef1234567890abcdef12345678)\nthis is a prose line\nCo-authored-by: Eve <e@x.y>",
	"this is prose\nSigned-off-by: A B <a@b.c>\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\np1\np2\np3\np4\np5\np6\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\np1\np2\np3\np4\np5\np6\np7\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nprose line\n  continued\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nsee: https://x\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\n# a comment\n  stray\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\n---\nCo-authored-by: Eve <e@x.y>",
	"  orphaned\nSigned-off-by: A B <a@b.c>\nCo-authored-by: Eve <e@x.y>",
	"signed-off-by: A B <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by:A B <a@b.c>\nprose\nCo-authored-by: Eve <e@x.y>",
	"Signed-off-by: A B <a@b.c>\nWhy not: a reason\nCo-authored-by: Eve <e@x.y>",
	"BREAKING CHANGE: the old flag is gone\nSigned-off-by: A B <a@b.c>\nCo-authored-by: Eve <e@x.y>",
}

var differentialTails = []string{
	"",
	"\n",
	"\n\n",
	"\n   \n",
	"\n\r\n",
	"\n# a trailing comment\n",
	"\n\n# c1\n# c2\n\n",
	"\nConflicts:\n\tf1\n",
	"\nConflicts:\n\tf1\n\tf2\n",
	"\n\nConflicts:\n\tf1\n",
	"\nConflicts:\r\n\tf1\n",
	"\nConflicts:",
	"\n# ------------------------ >8 ------------------------\nbelow\nCo-authored-by: Mallory <m@x.y>\n",
	"\n\n# ------------------------ >8 ------------------------\nCo-authored-by: Mallory <m@x.y>\n",
	"\n# ------------------------ >8 ------------------------",
	"\n#------------------------ >8 ------------------------\n",
}

// TestTrailersEqualGit is the oracle. Modelling git's rule and then testing
// the model against itself proves nothing, so the real parser is asked the
// same messages git's commit reader is, and the answers must be EQUAL on
// every shape — a parser that reports a trailer git does not names a person
// on a public release page as an author of code the commit never claimed,
// and one that reports fewer drops a credit GitHub shows (the five fleet
// commits in DESIGN §3). The shapes: the head × block × tail crossing above,
// every byte after `---` and in four other positions, the 25% boundary from
// both sides under every git-generated prefix spelling, and a seeded random
// mix of all of it (GLYPH_TRAILER_SHAPES scales the random part; the seed
// never moves, so a failure reproduces by name).
func TestTrailersEqualGit(t *testing.T) {
	var msgs []string
	for _, h := range differentialHeads {
		for _, b := range differentialBlocks {
			for _, tl := range differentialTails {
				msgs = append(msgs, h+b+tl)
			}
		}
	}
	msgs = append(msgs, byteShapes()...)
	msgs = append(msgs, boundaryShapes()...)
	randomN := 1500
	if s := os.Getenv("GLYPH_TRAILER_SHAPES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("GLYPH_TRAILER_SHAPES=%q is not an integer", s)
		}
		randomN = n
	}
	msgs = append(msgs, randomShapes(randomN)...)

	oracle := gitTrailersBatch(t, msgs)

	distinct := map[string]bool{}
	spoke, credited, over, under := 0, 0, 0, 0
	for i, msg := range msgs {
		distinct[msg] = true
		mine := trailers(msg)
		if len(oracle[i]) > 0 {
			spoke++
		}
		for _, tr := range oracle[i] {
			if strings.EqualFold(tr.Token, coAuthoredBy) {
				credited++
				break
			}
		}
		if sameTrailers(mine, oracle[i]) {
			continue
		}
		for _, got := range mine {
			if !slices.Contains(oracle[i], got) {
				over++
			}
		}
		for _, want := range oracle[i] {
			if !slices.Contains(mine, want) {
				under++
			}
		}
		t.Errorf("for %q:\n  glyph: %+v\n  git:   %+v", msg, mine, oracle[i])
	}
	if spoke == 0 || credited == 0 {
		t.Fatalf("the differential ran %d shapes of which git answered %d and credited a co-author on %d — a positive control is required", len(msgs), spoke, credited)
	}
	t.Logf("%d shapes (%d distinct), git answered %d and credited a co-author on %d; glyph over-reported %d trailers and under-reported %d", len(msgs), len(distinct), spoke, credited, over, under)
}

// byteShapes puts every byte but NUL and LF after `---` above the block and
// inside it, where a blank line would be, where a continuation would open,
// and inside a token — the positions where git's ASCII tables decide, so a
// Go-wide notion of space or letter would show.
func byteShapes() []string {
	var out []string
	for b := 1; b < 256; b++ {
		if b == '\n' {
			continue
		}
		s := string(rune(b))
		if b >= 0x80 {
			s = string([]byte{byte(b)})
		}
		out = append(out,
			"subject\n\nbody\n\n---"+s+"x\n\nCo-authored-by: A B <a@b.c>\n",
			"subject\n\nWhy: a\n---"+s+"\nCo-authored-by: A B <a@b.c>\n",
			"subject\n\nbody\n"+s+"\nCo-authored-by: A B <a@b.c>\n",
			"subject\n\nbody\n\nWhy: a\n"+s+"cont\nCo-authored-by: A B <a@b.c>\n",
			"subject\n\nbody\n\nWh"+s+"y: a\nCo-authored-by: A B <a@b.c>\n",
		)
	}
	return out
}

// boundaryShapes walks the 25% line from both sides: each git-generated
// prefix spelling (and its lowercase and no-space near-misses, and none),
// zero to two extra trailers, zero to twelve prose lines, in five layouts —
// prose below the prefix, above it, interleaved with the trailers, each prose
// line wrapped with a continuation, and a comment with a stray continuation
// under it.
func boundaryShapes() []string {
	prefixes := []string{
		"Signed-off-by: S <s@x.y>",
		"(cherry picked from commit 1234567890abcdef1234567890abcdef12345678)",
		"signed-off-by: S <s@x.y>",
		"Signed-off-by:S <s@x.y>",
		"",
	}
	extras := [][]string{{}, {"Why: a"}, {"Why: a", "Ref: b"}}
	const credit = "Co-authored-by: Eve <e@x.y>"
	var out []string
	for _, p := range prefixes {
		for _, ex := range extras {
			for n := 0; n <= 12; n++ {
				prose := make([]string, n)
				for i := range prose {
					prose[i] = "p" + strconv.Itoa(i+1)
				}
				var layouts [][]string
				below := appendNonEmpty(nil, p)
				below = append(below, prose...)
				below = append(below, ex...)
				layouts = append(layouts, append(below, credit))

				above := append([]string{}, prose...)
				above = appendNonEmpty(above, p)
				above = append(above, ex...)
				layouts = append(layouts, append(above, credit))

				var inter []string
				inter = appendNonEmpty(inter, p)
				for i, l := range prose {
					inter = append(inter, l)
					if i < len(ex) {
						inter = append(inter, ex[i])
					}
				}
				if len(prose) < len(ex) {
					inter = append(inter, ex[len(prose):]...)
				}
				layouts = append(layouts, append(inter, credit))

				wrapped := appendNonEmpty(nil, p)
				for _, l := range prose {
					wrapped = append(wrapped, l, " cont")
				}
				wrapped = append(wrapped, ex...)
				layouts = append(layouts, append(wrapped, credit))

				commented := appendNonEmpty(nil, p)
				commented = append(commented, prose...)
				commented = append(commented, "# a comment", "  stray")
				commented = append(commented, ex...)
				layouts = append(layouts, append(commented, credit))

				for _, l := range layouts {
					out = append(out, "subj\n\nbody\n\n"+strings.Join(l, "\n")+"\n")
				}
			}
		}
	}
	return out
}

func appendNonEmpty(to []string, s string) []string {
	if s == "" {
		return to
	}
	return append(to, s)
}

// randomShapes mixes the pools under a fixed seed: a head, one to eight block
// lines, a tail, and in one shape of six a CRLF rewrite of the whole message,
// in one of five the final newline dropped.
func randomShapes(n int) []string {
	heads := append([]string{
		"subject\nsecond title line\n\n",
		"subject\n\nbody\n\n  indented body\n\n",
		"subject\n\n# a comment in the body\n\n",
	}, differentialHeads...)
	lines := []string{
		"Co-authored-by: A B <a@b.c>",
		"Co-authored-by: A B <a@b.c>",
		"Co-authored-by: A B <a@b.c>",
		"Co-Authored-By: C D <c@d.e>",
		"Why: a reason",
		"Ref: #12",
		"Signed-off-by: S <s@x.y>",
		"Signed-off-by: S <s@x.y>",
		"(cherry picked from commit 1234567890abcdef1234567890abcdef12345678)",
		"signed-off-by: S <s@x.y>",
		"Signed-off-by:S <s@x.y>",
		"Fix#1: x",
		"Révisé: x",
		"https://x: y",
		"https://github.com/o/r/issues/1",
		"Why not: x",
		"Why : x",
		"Why\t:x",
		"-: x",
		":x",
		"Why:",
		"Why:\t x  ",
		"BREAKING CHANGE: x",
		"p1",
		"p2",
		"see https://x",
		"a line: with a colon inside",
		"  cont",
		"\tcont",
		" ",
		"\r",
		"\fnot blank",
		"\vnot a continuation",
		"# c",
		"#c: d",
		"---",
		"--- x",
		"---\r",
		"---x",
		"Conflicts:",
		"\tpath",
		"# ------------------------ >8 ------------------------",
	}
	tails := append([]string{"\n\n\n", "\n# c\n\tpath\n", "\nConflicts:\n\tf1\n\n\tf2\n", "\nConflicts:\n\tf1\nprose\n\tf2\n", "\nConflicts:\n\tf1\n# c\n\tf2\n"}, differentialTails...)

	r := rand.New(rand.NewPCG(0x676c797068, 0x74726c7273))
	out := make([]string, 0, n)
	for len(out) < n {
		var b strings.Builder
		b.WriteString(heads[r.IntN(len(heads))])
		for range 1 + r.IntN(8) {
			b.WriteString(lines[r.IntN(len(lines))])
			b.WriteByte('\n')
		}
		b.WriteString(tails[r.IntN(len(tails))])
		m := b.String()
		if r.IntN(6) == 0 {
			m = strings.ReplaceAll(m, "\n", "\r\n")
		}
		if r.IntN(5) == 0 {
			m = strings.TrimSuffix(strings.TrimSuffix(m, "\n"), "\r")
		}
		out = append(out, m)
	}
	return out
}

// gitTrailersBatch asks git's commit reader what each message's trailers
// are: the messages become commits of a throwaway repository through one
// `git fast-import` (which stores a message byte for byte — no cleanup, no
// added newline, measured 2026-11-08), and one `git log` reads
// `%(trailers:only,unfold)` off each. `only` keeps the answer to trailer
// lines, `unfold` folds continuations the way this package does, so the two
// answers are comparable. `git interpret-trailers --parse` is deliberately
// not the oracle: it applies the `---` patch divider the commit reader does
// not (DESIGN §3).
//
// The oracle is git with no config but its defaults: trailer.separators and
// trailer.<token>.* change what git answers, and a personal
// `trailer.separators = "#"` failed this test with glyph's parser untouched.
// glyph's notes never read that config, so the defaults are the comparison.
func gitTrailersBatch(t *testing.T, msgs []string) [][]trailer {
	t.Helper()
	testutil.RequireGit(t)
	dir := t.TempDir()
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testutil.GitEnv("akira-toriyama")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			var exit *exec.ExitError
			stderr := ""
			if errors.As(err, &exit) {
				stderr = string(exit.Stderr)
			}
			t.Fatalf("git %v: %v\n%s", args, err, stderr)
		}
		return string(out)
	}
	run("", "init", "-q")

	var stream strings.Builder
	for i, m := range msgs {
		fmt.Fprintf(&stream, "commit refs/heads/shapes\nmark :%d\ncommitter c <c@example.invalid> 0 +0000\ndata %d\n%s\n", i+1, len(m), m)
	}
	marks := filepath.Join(dir, "marks")
	run(stream.String(), "fast-import", "--quiet", "--export-marks="+marks)
	raw, err := os.ReadFile(marks)
	if err != nil {
		t.Fatal(err)
	}
	shas := make([]string, len(msgs))
	index := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		mark, sha, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("marks line %q", line)
		}
		n, err := strconv.Atoi(strings.TrimPrefix(mark, ":"))
		if err != nil || n < 1 || n > len(msgs) {
			t.Fatalf("marks line %q", line)
		}
		shas[n-1] = sha
		index[sha] = n - 1
	}
	for i, s := range shas {
		if s == "" {
			t.Fatalf("message %d got no commit", i)
		}
	}

	out := run(strings.Join(shas, "\n")+"\n", "log", "--no-walk=unsorted", "--stdin", "--format=%H%x00%(trailers:only,unfold)%x00")
	got := make([][]trailer, len(msgs))
	seen := 0
	for rec := range strings.SplitSeq(out, "\x00\n") {
		if rec == "" {
			continue
		}
		sha, text, ok := strings.Cut(rec, "\x00")
		if !ok {
			t.Fatalf("log record %q", rec)
		}
		i, known := index[sha]
		if !known {
			t.Fatalf("log answered for %s, which no message produced", sha)
		}
		seen++
		for l := range strings.SplitSeq(text, "\n") {
			if l == "" {
				continue
			}
			token, rest, ok := splitTrailer(l)
			if !ok {
				t.Fatalf("%%(trailers:only) printed a line splitTrailer cannot read: %q", l)
			}
			got[i] = append(got[i], trailer{Token: token, Value: strings.Trim(rest, gitSpace)})
		}
	}
	if seen != len(msgs) {
		t.Fatalf("git log answered for %d of %d commits", seen, len(msgs))
	}
	return got
}

func sameTrailers(a, b []trailer) bool { return slices.Equal(a, b) }
