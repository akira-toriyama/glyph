package parser

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitOrSkip skips the test when git is absent (never on CI, where git is a
// given — this guards exotic local environments only). Same shape as
// internal/gitsource's; internal/parser has no git dependency of its own, and
// borrowing one across packages would be worse than eight lines.
func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitStripspace is THE oracle for "what will git record": git's own
// strbuf_stripspace, exposed as `git stripspace`. glyph's rules judge a message
// a developer is writing, but the thing that ends up in history — and that CI
// then lints — is git's cleaned-up version of it, so anywhere glyph models that
// cleanup it must be measured against git rather than against a belief about
// git. (House rule: code that models an external system's behaviour carries one
// test that asks the real system.)
//
// The environment is pinned so a personal core.commentChar or a global config
// cannot move the answer.
func gitStripspace(t *testing.T, in string) string {
	t.Helper()
	cmd := exec.Command("git", "stripspace")
	cmd.Stdin = strings.NewReader(in)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git stripspace: %v", err)
	}
	return string(out)
}

// TestSubjectVerdictSurvivesGitsCleanup: glyph must reach the SAME verdict on
// the message a developer wrote and on the message git records from it. Any gap
// is a lie in one direction or the other — the commit-msg hook blessing what CI
// will reject, or rejecting what CI accepts.
//
// The gap this pins was real and it was not confined to the hook: the subject
// line was read verbatim, so a trailing space hid the period behind it and
// `trailing-period` — a rule DESIGN §2 states outright — did not fire in ANY
// mode, `--range` and CI included. git strips that whitespace (space, tab and
// CR; a \v is content and stays), so the period it records is at the end of the
// line after all.
func TestSubjectVerdictSurvivesGitsCleanup(t *testing.T) {
	gitOrSkip(t)

	cases := []struct {
		name string
		raw  string
	}{
		{"a trailing period behind trailing spaces", ":bug:(cli) fix the thing.   "},
		{"a trailing period behind a tab", ":bug:(cli) fix the thing.\t"},
		{"a trailing period behind a CR", ":bug:(cli) fix the thing.\r"},
		{"an uppercase subject with trailing spaces", ":bug:(cli) Fix the thing  "},
		{"a clean subject with trailing spaces", ":bug:(cli) fix the thing  "},
		{"trailing whitespace on a body line", ":bug:(cli) fix the thing\n\nwhy it broke   \n"},
		{"a vertical tab is content, not whitespace", ":bug:(cli) fix the thing\v"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorded := gitStripspace(t, tc.raw+"\n")

			rawC, rawErr := Parse(tc.raw)
			recC, recErr := Parse(recorded)
			if (rawErr == nil) != (recErr == nil) {
				t.Fatalf("glyph parses the written message and the recorded one differently:\n"+
					"  written  %q -> %v\n  recorded %q -> %v", tc.raw, rawErr, recorded, recErr)
			}
			if rawErr == nil && rawC.Subject != recC.Subject {
				t.Errorf("subject differs from what git records:\n  written  %q\n  recorded %q",
					rawC.Subject, recC.Subject)
			}

			if got, want := rules(Lint(tc.raw, LintOptions{})), rules(Lint(recorded, LintOptions{})); got != want {
				t.Errorf("the verdict on the written message is %q but git records a message that "+
					"lints %q — the hook and CI would disagree about the same commit\n  written  %q\n  recorded %q",
					got, want, tc.raw, recorded)
			}
		})
	}
}

// rules renders a verdict as a comparable string: the rule ids in order.
func rules(vs []Violation) string {
	ids := make([]string, len(vs))
	for i, v := range vs {
		ids[i] = v.Rule
	}
	return strings.Join(ids, ",")
}
