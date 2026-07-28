package parser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
func gitStripspace(t *testing.T, in string, args ...string) string {
	t.Helper()
	out, err := gitStripspaceOut(in, args...)
	if err != nil {
		t.Fatalf("git stripspace: %v", err)
	}
	return out
}

// gitStripspaceOut is gitStripspace without the *testing.T, so the differential
// test can call it from worker goroutines — where a t.Fatalf would end the wrong
// goroutine and let the test pass on a git that never ran.
func gitStripspaceOut(in string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"stripspace"}, args...)...) //nolint:gosec // args are this file's own literals
	cmd.Stdin = strings.NewReader(in)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.Output()
	return string(out), err
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

// TestCleanupMatchesGitStripspace is the differential half of the same house
// rule: Cleanup does not merely agree with git about the subjects a human would
// think to write, it IS git's strbuf_stripspace over generated input.
//
// `git stripspace` exposes exactly that function — with --strip-comments it is
// `--cleanup=strip`, without it `--cleanup=whitespace` — so every combination of
// the atoms below can be checked against git rather than against a belief about
// git. The alphabet is chosen from where a port drifts: whitespace-only lines,
// runs of blank lines, comments at column 0 and indented, and CARRIAGE RETURNS.
// The CR atoms are not decoration — an earlier port of this function trimmed
// " \t" and passed every hand-written case; the first differential run failed 82
// of 882 messages, all of them a line ending "\r ".
func TestCleanupMatchesGitStripspace(t *testing.T) {
	gitOrSkip(t)

	atoms := []string{
		"", "\t", " \r",
		"text", "text ", "text\t ", "text\r", "text\r ",
		"#c", "  # c", "text\v",
	}
	modes := []struct {
		name string
		mode CleanupMode
		args []string
	}{
		{"whitespace", CleanupMode{Space: true}, nil},
		{"strip", CleanupMode{Space: true, Comments: true}, []string{"--strip-comments"}},
	}

	var messages []string
	for _, a := range atoms {
		for _, b := range atoms {
			for _, c := range atoms {
				messages = append(messages, a+"\n"+b+"\n"+c+"\n")
			}
		}
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			// One `git stripspace` per message is thousands of processes, so
			// they run concurrently: serially this test took 30 seconds, which
			// is how a slow test becomes a test somebody narrows.
			want := make([]string, len(messages))
			errs := make([]error, len(messages))
			sem := make(chan struct{}, runtime.NumCPU())
			var wg sync.WaitGroup
			for i, in := range messages {
				wg.Go(func() {
					sem <- struct{}{}
					defer func() { <-sem }()
					out, err := gitStripspaceOut(in, m.args...)
					want[i], errs[i] = strings.TrimSuffix(out, "\n"), err
				})
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("git stripspace %v on %q: %v", m.args, messages[i], err)
				}
			}

			mismatch := 0
			for i, in := range messages {
				if got := Cleanup(in, m.mode); got != want[i] {
					mismatch++
					if mismatch <= 5 {
						t.Errorf("Cleanup disagrees with `git stripspace %s`:\n  in   %q\n  got  %q\n  git  %q",
							strings.Join(m.args, " "), in, got, want[i])
					}
				}
			}
			if mismatch > 5 {
				t.Errorf("... and %d more of %d generated messages", mismatch-5, len(messages))
			}
		})
	}
}

// TestCutLineIsTheOneGitWrites keeps the exact-match cut line honest against the
// git that is actually installed. Matching loosely cuts messages git records
// (the hook refusing what CI accepts); matching a line git no longer writes
// leaves the whole verbose diff in the linted text (the same, louder). Only git
// can settle which string it writes, so this asks it: a real `commit -v`, with
// the editor replaced by a dump of the buffer git prepared.
func TestCutLineIsTheOneGitWrites(t *testing.T) {
	gitOrSkip(t)

	dir := t.TempDir()
	dump := filepath.Join(dir, "buffer.txt")
	editor := filepath.Join(dir, "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ncp \"$1\" "+dump+"\nprintf ':bug: probe\\n' > \"$1\"\n"), 0o700); err != nil { //nolint:gosec // a test editor must be executable
		t.Fatalf("write editor: %v", err)
	}

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_EDITOR="+editor,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-v")

	buf, err := os.ReadFile(dump) //nolint:gosec // a path this test just wrote
	if err != nil {
		t.Fatalf("read the buffer git prepared: %v", err)
	}
	if !strings.Contains(string(buf), "\n"+cutLine) {
		t.Fatalf("git no longer writes the cut line glyph cuts at — Cleanup would lint the whole diff.\n"+
			"glyph cuts at %q; git wrote:\n%s", cutLine, buf)
	}
	// The dump is the buffer git PREPARED — template, status block, cut line and
	// the whole diff, with no message in it yet. Writing the subject on top is
	// what an author does, and the cleaned result must be that subject alone.
	if got := Cleanup(":bug: probe\n"+string(buf), CleanupMode{Space: true, Comments: true, Truncate: true}); got != ":bug: probe" {
		t.Errorf("Cleanup did not reduce a real `commit -v` buffer to its message: %q", got)
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
