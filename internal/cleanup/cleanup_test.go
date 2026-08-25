package cleanup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The four modes as a commit-msg hook resolves them, named once so the tables
// below read as the situations they are rather than as three booleans.
var (
	editedDefault  = Mode{Space: true, Comments: true, Truncate: true} // git commit (an editor runs)
	noEditorGiven  = Mode{Space: true}                                 // git commit -m / -F
	editedScissors = Mode{Space: true, Truncate: true}                 // commit.cleanup=scissors + an editor
	verbatimGiven  = Mode{}                                            // commit.cleanup=verbatim, no editor
)

func TestCleanup(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
		in   string
		want string
	}{
		{
			name: "leading blank line does not become the subject",
			// git's editor buffer starts empty; a developer who types the
			// subject on line 2 used to be told the message was empty.
			mode: editedDefault,
			in:   "\n:sparkles:(hook) add the thing\n",
			want: ":sparkles:(hook) add the thing",
		},
		{
			name: "editor template is stripped",
			mode: editedDefault,
			in: ":bug: fix it\n" +
				"\n" +
				"# Please enter the commit message for your changes. Lines starting\n" +
				"# with '#' will be ignored, and an empty message aborts the commit.\n" +
				"#\n" +
				"# On branch main\n",
			want: ":bug: fix it",
		},
		{
			name: "template above the message does not become the subject",
			mode: editedDefault,
			in: "# Please enter the commit message for your changes.\n" +
				"\n" +
				":memo: write the docs\n",
			want: ":memo: write the docs",
		},
		{
			name: "verbose diff below the scissors line is cut, not linted",
			mode: editedDefault,
			in: ":zap: speed it up\n" +
				"\n" +
				cutLine +
				"# Do not modify or remove the line above.\n" +
				"diff --git a/x b/x\n" +
				"+BREAKING CHANGE: this line lives in the diff, not the message\n",
			want: ":zap: speed it up",
		},
		{
			name: "an INDENTED scissors line is not a cut and not a comment",
			// git matches its cut line at column 0 only, and a line that does
			// not start with '#' is not a comment either — so git records this
			// whole message. Cutting here hid a footer git keeps, which is the
			// hook refusing a commit CI accepts.
			mode: editedDefault,
			in:   ":fire:(x) drop it\n\n  # ------------------------ >8 ------------------------\nNON-BREAKING: it was dead\n",
			want: ":fire:(x) drop it\n\n  # ------------------------ >8 ------------------------\nNON-BREAKING: it was dead",
		},
		{
			name: "a body is preserved verbatim",
			mode: editedDefault,
			in:   ":sparkles: add\n\nwhy this matters\n\nBREAKING CHANGE: it moved\n",
			want: ":sparkles: add\n\nwhy this matters\n\nBREAKING CHANGE: it moved",
		},
		{
			name: "comments-only reduces to empty",
			mode: editedDefault,
			in:   "# Please enter the commit message\n#\n",
			want: "",
		},
		{
			name: "already-clean message is untouched",
			mode: editedDefault,
			in:   ":bug: fix\n",
			want: ":bug: fix",
		},
		{
			name: "an indented comment is content, not a comment",
			// git only ever drops a '#' in column 0. Dropping this one closed
			// the blank-line gap above the footer, making NON-BREAKING: a
			// trailer at the hook and prose in CI (undeclared-removal, hook 0 /
			// CI 3 — measured).
			mode: editedDefault,
			in:   ":fire:(x) drop the thing\n\n  # why: leftover note here\nNON-BREAKING: it was dead\n",
			want: ":fire:(x) drop the thing\n\n  # why: leftover note here\nNON-BREAKING: it was dead",
		},
		{
			name: "a comment does not separate the lines around it",
			// git's stripspace skips a comment line without counting it as
			// blank, so the trailer below stays stacked under the one above.
			mode: editedDefault,
			in:   ":fire:(x) drop it\n\nCloses #12\n# a note\nNON-BREAKING: it was dead\n",
			want: ":fire:(x) drop it\n\nCloses #12\nNON-BREAKING: it was dead",
		},
		{
			name: "no editor: a '#' line is content, because git records it",
			// `git commit -F` is cleanup=whitespace: comments are never
			// stripped, so line 1 IS the subject git will record. Dropping it
			// linted line 2 instead — hook 0, CI 3 (malformed-subject).
			mode: noEditorGiven,
			in:   "# reminder from my notes\n:sparkles:(x) add the thing\n",
			want: "# reminder from my notes\n:sparkles:(x) add the thing",
		},
		{
			name: "no editor: the cut line is not a cut",
			// Measured: with commit.cleanup=scissors and -F, git records the cut
			// line and everything under it.
			mode: noEditorGiven,
			in:   ":fire:(x) drop it\n\n" + cutLine + "NON-BREAKING: it was dead\n",
			want: ":fire:(x) drop it\n\n" + "# ------------------------ >8 ------------------------\nNON-BREAKING: it was dead",
		},
		{
			name: "scissors with an editor cuts but keeps comments",
			mode: editedScissors,
			in:   ":zap: go\n\n# a note git keeps in this mode\n" + cutLine + "diff --git a/x b/x\n",
			want: ":zap: go\n\n# a note git keeps in this mode",
		},
		{
			name: "verbatim touches nothing",
			mode: verbatimGiven,
			in:   "\n#  a note   \n\n\n:bug: fix   \n",
			want: "\n#  a note   \n\n\n:bug: fix   ",
		},
		{
			name: "trailing whitespace goes, interior blank runs collapse",
			mode: editedDefault,
			in:   ":bug: fix\t \r\n\n\n\nwhy   \n\n\n",
			want: ":bug: fix\n\nwhy",
		},
		{
			name: "a message that opens with the cut line cleans to nothing",
			mode: editedDefault,
			in:   cutLine + ":bug: this is below the cut\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Apply(tt.in, tt.mode); got != tt.want {
				t.Errorf("Apply(%q, %+v)\n got %q\nwant %q", tt.in, tt.mode, got, tt.want)
			}
		})
	}
}

// TestResolveCleanupMode pins the mapping from what a hook can SEE to what git
// will DO. Every row is a real invocation; the two that carry the incidents are
// the -m/-F rows, where assuming the editor's cleanup is what made the hook and
// CI disagree about the same commit.
func TestResolveCleanupMode(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		edited     bool
		want       Mode
		wantKnown  bool
	}{
		{"unset + an editor is git's strip, and -v may have appended a diff",
			"", true, Mode{Space: true, Comments: true, Truncate: true}, true},
		{"unset + no editor (-m / -F) is whitespace: comments are content, nothing is cut",
			"", false, Mode{Space: true}, true},
		{"an explicit 'default' reads exactly as unset",
			"default", false, Mode{Space: true}, true},
		{"whitespace keeps comments even under an editor",
			"whitespace", true, Mode{Space: true, Truncate: true}, true},
		{"strip drops comments even without an editor",
			"strip", false, Mode{Space: true, Comments: true}, true},
		{"scissors cuts only when a message is edited",
			"scissors", true, Mode{Space: true, Truncate: true}, true},
		{"scissors without an editor does not cut",
			"scissors", false, Mode{Space: true}, true},
		{"verbatim cleans nothing, but -v still truncates",
			"verbatim", true, Mode{Truncate: true}, true},
		{"verbatim without an editor is the identity",
			"verbatim", false, Mode{}, true},
		{"an unknown mode falls back to default and says so",
			"stirp", true, Mode{Space: true, Comments: true, Truncate: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := ResolveMode(tt.configured, tt.edited)
			if got != tt.want || known != tt.wantKnown {
				t.Errorf("ResolveMode(%q, %v) = %+v, %v; want %+v, %v",
					tt.configured, tt.edited, got, known, tt.want, tt.wantKnown)
			}
		})
	}
}

// A --range walk must NOT be cleaned: git log %B is already clean, and running
// Cleanup there would swallow a genuinely empty message and eat body lines a
// project chose to start with '#'. This test documents the boundary by pinning
// what Cleanup would destroy if it were ever applied there.
func TestCleanupIsNotSafeForAlreadyCleanedMessages(t *testing.T) {
	if got := Apply("#42 was the culprit\n", editedDefault); got != "" {
		t.Errorf("expected a '#'-leading line to be treated as a comment, got %q", got)
	}
}

// TestCutLineIsTheOneGitWrites is the real-git oracle cleanup.go's cutLine
// constant names: `git commit -v` writes the scissors line into the message
// file, and this test asserts it writes EXACTLY the constant — matched by
// strstr in git's wt_status_locate_end, so one drifted byte in a future git
// silently moves the cut. The v1 parser carried this oracle and it died with
// that package; the constant sat un-vouched-for until this restored it.
// The editor deliberately aborts (exit 1): the message file is already
// written when the editor runs, so the scissors line is captured without a
// commit ever being made.
func TestCutLineIsTheOneGitWrites(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")

	captured := filepath.Join(dir, "captured.txt")
	editor := filepath.Join(dir, "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ncp \"$1\" "+captured+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "commit", "-v")
	commit.Dir = dir
	commit.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_EDITOR="+editor,
	)
	_ = commit.Run() // the editor aborts by design; the message file is what matters

	body, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("the editor never captured the message file: %v", err)
	}
	if !strings.Contains(string(body), cutLine) {
		t.Fatalf("git commit -v wrote no line equal to cutLine %q — git's scissors changed, and truncateAtCutLine now cuts on a line git does not write:\n%s", cutLine, body)
	}
}
