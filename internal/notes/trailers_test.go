package notes

import (
	"os/exec"
	"strings"
	"testing"
)

// The table is the measurement. Every `want` below was read off
// `git interpret-trailers --parse` (git 2.54.0) before the parser existed,
// not derived from it afterwards — which is why the shapes that answer
// NOTHING outnumber the ones that answer something. The silent-total-loss
// cases (a `---` above the block, one prose line inside it, an unindented
// wrap) are the reason this is a parser and not a grep.
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
			name: "a token holding a space voids the block",
			msg:  "subject\n\nbody\n\nWhy not: a reason\nCo-authored-by: A B <a@b.c>",
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CoAuthorNames(tc.msg)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("CoAuthorNames() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrailerValueTakesTheLastMatch(t *testing.T) {
	msg := "subject\n\nbody\n\nWhy: the first reason\nWhy: the corrected reason"
	if got := TrailerValue(msg, "why"); got != "the corrected reason" {
		t.Errorf("TrailerValue() = %q, want the LAST match", got)
	}
	if got := TrailerValue(msg, "nosuch"); got != "" {
		t.Errorf("TrailerValue() for an absent token = %q, want empty", got)
	}
}

// TestTrailersAreASubsetOfGit is the oracle. Modelling git's rule and then
// testing the model against itself proves nothing, so the real parser is
// asked the same messages git is, and the invariant asserted is the one that
// matters: glyph may report FEWER trailers than git, NEVER a trailer git does
// not report. Over-reporting is how a person ends up named on a public
// release page as an author of code the commit never claimed.
//
// The cases are generated by crossing the shapes the table pins one at a
// time, so combinations nobody thought to write down are covered too.
func TestTrailersAreASubsetOfGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is the oracle here and is not on PATH")
	}

	heads := []string{
		"subject\n\nbody.\n\n",
		"subject\n\nbody.\n\nmore body.\n\n",
		"subject\n\n",
		"subject\n\nbody\n\n---\n\n",
		"", // no paragraph before: the block would be the subject
	}
	blocks := []string{
		"Why: a reason",
		"Why: a reason\nCo-authored-by: A B <a@b.c>",
		"Co-authored-by: A B <a@b.c>\nCo-authored-by: C D <c@d.e>",
		"Why: a reason that\n  wraps politely",
		"Why: a reason that\nwraps rudely",
		"Why: a reason\n# a comment\nCo-authored-by: A B <a@b.c>",
		"Why not: a reason",
		"Why = a reason",
		"see https://example.com/x\nCo-authored-by: A B <a@b.c>",
		"Why:\nCo-authored-by: <a@b.c>",
		"  orphaned continuation",
		"Why: first\nWhy: second",
		"Signed-off-by: A B <a@b.c>\nWhy: a reason",
	}
	tails := []string{"", "\n", "\n\n"}

	checked := 0
	for _, h := range heads {
		for _, b := range blocks {
			for _, tl := range tails {
				msg := h + b + tl
				oracle := gitTrailers(t, msg)
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

// gitTrailers asks git what the message's trailers are. `--only-trailers`
// keeps the answer to the block itself, and `--unfold` folds continuations
// the way this package does, so the two answers are comparable.
func gitTrailers(t *testing.T, msg string) []trailer {
	t.Helper()
	cmd := exec.Command("git", "interpret-trailers", "--parse", "--only-trailers", "--unfold")
	cmd.Stdin = strings.NewReader(msg + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git interpret-trailers: %v", err)
	}
	var got []trailer
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l == "" {
			continue
		}
		token, value, ok := splitTrailer(l)
		if !ok {
			continue
		}
		got = append(got, trailer{Token: token, Value: value})
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
