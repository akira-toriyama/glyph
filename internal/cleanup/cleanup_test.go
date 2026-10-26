package cleanup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// The four modes as a commit-msg hook resolves them, named once so the tables
// below read as the situations they are rather than as three booleans.
var (
	editedDefault  = Mode{Space: true, Comments: true, Truncate: true} // git commit (an editor runs)
	noEditorGiven  = Mode{Space: true}                                 // git commit -m / -F
	editedScissors = Mode{Space: true, Truncate: true}                 // commit.cleanup=scissors + an editor
	verbatimGiven  = Mode{}                                            // commit.cleanup=verbatim, no editor
)

// cutLine is git's scissors line with no comment prefix configured.
const cutLine = DefaultComment + cutTail

func TestCleanup(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
		in   string
		want string
	}{
		{
			name: "leading blank line does not become the subject",
			// git's editor buffer starts empty and git drops leading blank
			// lines, so a subject typed on line 2 is the subject git records.
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
			// whole message, and cutting here hides text git records (under
			// v1's footer rule, the hidden NON-BREAKING: footer made the hook
			// refuse a commit CI accepted).
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
			// trailer at the hook and prose in CI (v1's undeclared-removal footer
			// rule: hook 0 / CI 3 — measured).
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
			// linted line 2 instead — hook 0, CI 3 (v1's malformed-subject).
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
// will DO. Every row is a real invocation; the ones that carry the incidents are
// the -m/-F rows, where assuming the editor's cleanup is what made the hook and
// CI disagree about the same commit, the commit.verbose rows without an editor,
// where git cuts and the hook used not to (t-3p3k (2)), and the git merge rows,
// where commit.verbose means nothing to git. The real-git oracle for the grid is
// internal/cli's TestHookCutMatchesGit.
func TestResolveCleanupMode(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		edited     bool
		verbose    Verbose
		want       Mode
		wantKnown  bool
	}{
		{"unset + an editor is git's strip, cut because -v may have appended a diff",
			"", true, VerboseUnseen, Mode{Space: true, Comments: true, Truncate: true}, true},
		{"unset + no editor (-m / -F) is whitespace: comments are content, nothing is cut",
			"", false, VerboseUnseen, Mode{Space: true}, true},
		{"unset + no editor under commit.verbose is whitespace, cut at the scissors line",
			"", false, VerboseOn, Mode{Space: true, Truncate: true}, true},
		{"an explicit 'default' reads exactly as unset",
			"default", false, VerboseUnseen, Mode{Space: true}, true},
		{"whitespace keeps comments even under an editor",
			"whitespace", true, VerboseUnseen, Mode{Space: true, Truncate: true}, true},
		{"strip drops comments even without an editor",
			"strip", false, VerboseUnseen, Mode{Space: true, Comments: true}, true},
		{"strip without an editor under commit.verbose drops comments and cuts",
			"strip", false, VerboseOn, Mode{Space: true, Comments: true, Truncate: true}, true},
		{"scissors cuts when a message is edited",
			"scissors", true, VerboseUnseen, Mode{Space: true, Truncate: true}, true},
		{"scissors without an editor does not cut",
			"scissors", false, VerboseUnseen, Mode{Space: true}, true},
		{"scissors without an editor cuts under commit.verbose",
			"scissors", false, VerboseOn, Mode{Space: true, Truncate: true}, true},
		{"verbatim cleans nothing, and an editor cuts: -v may be on and no hook sees it",
			"verbatim", true, VerboseUnseen, Mode{Truncate: true}, true},
		{"verbatim without an editor is the identity",
			"verbatim", false, VerboseUnseen, Mode{}, true},
		{"verbatim without an editor under commit.verbose cleans nothing and cuts",
			"verbatim", false, VerboseOn, Mode{Truncate: true}, true},
		{"an unknown mode falls back to default and says so",
			"stirp", true, VerboseUnseen, Mode{Space: true, Comments: true, Truncate: true}, false},
		{"an unknown mode keeps commit.verbose's cut on its fallback",
			"stirp", false, VerboseOn, Mode{Space: true, Truncate: true}, false},
		{"git merge with an editor strips and does not cut: merge cleans with verbose 0",
			"", true, VerboseNever, Mode{Space: true, Comments: true}, true},
		{"git merge in scissors mode with an editor cuts",
			"scissors", true, VerboseNever, Mode{Space: true, Truncate: true}, true},
		{"git merge without an editor does not cut, whatever commit.verbose says",
			"strip", false, VerboseNever, Mode{Space: true, Comments: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := ResolveMode(tt.configured, tt.edited, tt.verbose)
			if got != tt.want || known != tt.wantKnown {
				t.Errorf("ResolveMode(%q, %v, %v) = %+v, %v; want %+v, %v",
					tt.configured, tt.edited, tt.verbose, got, known, tt.want, tt.wantKnown)
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

// pinnedGit runs the installed git in dir with no personal or system config —
// the oracle answers for git, not for whoever runs the test — and returns its
// stdout.
func pinnedGit(t *testing.T, dir, stdin string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = testutil.GitEnv("t", extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return stdout.String()
}

// TestCutLineIsTheOneGitWrites is the real-git oracle cleanup.go's cutTail
// constant names: `git commit -v` writes the scissors line into the message
// file, and this test asserts it writes EXACTLY the comment prefix and the
// constant — matched by strstr in git's wt_status_locate_end, so one drifted
// byte in a future git silently moves the cut. It asks under the default
// prefix and under two set ones, because the prefix is the part glyph now
// builds rather than copies.
// The editor deliberately aborts (exit 1): the message file is already
// written when the editor runs, so the scissors line is captured without a
// commit ever being made.
func TestCutLineIsTheOneGitWrites(t *testing.T) {
	for _, prefix := range []string{"", ";", "//"} {
		t.Run("core.commentChar="+prefix, func(t *testing.T) {
			dir := t.TempDir()
			pinnedGit(t, dir, "", nil, "init", "-q", "-b", "main")
			if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			pinnedGit(t, dir, "", nil, "add", "a.txt")

			captured := filepath.Join(dir, "captured.txt")
			editor := filepath.Join(dir, "editor.sh")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\ncp \"$1\" "+captured+"\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{"commit", "-v"}
			if prefix != "" {
				args = append([]string{"-c", "core.commentChar=" + prefix}, args...)
			}
			commit := exec.Command("git", args...)
			commit.Dir = dir
			commit.Env = testutil.GitEnv("t", "GIT_EDITOR="+editor)
			_ = commit.Run() // the editor aborts by design; the message file is what matters

			body, err := os.ReadFile(captured)
			if err != nil {
				t.Fatalf("the editor never captured the message file: %v", err)
			}
			want := CommentPrefix(prefix, prefix != "") + cutTail
			if !strings.Contains(string(body), "\n"+want) {
				t.Fatalf("git commit -v wrote no line equal to %q — git's scissors changed, and truncateAtCutLine now cuts on a line git does not write:\n%s", want, body)
			}
		})
	}
}

// TestCommentPrefix pins how a configured value becomes the prefix. Every row
// is a measurement on git 2.54 (CommentPrefix's doc names them); the
// precedence between core.commentChar and core.commentString is not here
// because it is gitsource.ConfigLastOf's, held by internal/cli's
// TestHookVerdictMatchesWhatGitRecords.
func TestCommentPrefix(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		set        bool
		want       string
	}{
		{"unset is git's default", "", false, "#"},
		{"a character is itself", ";", true, ";"},
		{"a multi-character string is itself", "//", true, "//"},
		{"a quoted trailing space belongs to the prefix", "// ", true, "// "},
		{"auto is '#': the per-commit pick reaches no hook", "auto", true, "#"},
		{"git compares auto without case", "AUTO", true, "#"},
		{"an empty value git refuses to start under is '#'", "", true, "#"},
		{"a newline git refuses to start under is '#'", "a\nb", true, "#"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CommentPrefix(tt.configured, tt.set); got != tt.want {
				t.Errorf("CommentPrefix(%q, %v) = %q, want %q", tt.configured, tt.set, got, tt.want)
			}
		})
	}
}

// TestApplyMatchesGitStripspace is the differential DESIGN §2.1 names: for
// every message × comment prefix, Apply's whitespace and strip cleanups equal
// `git -c core.commentChar=<prefix> stripspace [--strip-comments]` run on the
// same bytes. `git stripspace` is the strbuf_stripspace git commit calls, so
// this holds the port line for line; the prefixes include a multi-character one
// and regex metacharacters, which git matches literally. The scissors line
// under a set prefix is a comment like any other here — the cut itself is
// TestScissorsUnderACommentPrefixMatchesGit's.
func TestApplyMatchesGitStripspace(t *testing.T) {
	dir := t.TempDir()
	prefixes := []string{"#", ";", "//", ".*", "$"}
	for _, prefix := range prefixes {
		messages := []string{
			"",
			"\n\n",
			"a\n# hash\n; semi\n// slash\n.* dotstar\n$ dollar\nb\n",
			prefix + " only a comment\n",
			"subject\n\n" + prefix + "\n" + prefix + "x\n  " + prefix + " indented\nbody  \r\n\n\n" + prefix + prefix + " doubled\ntail\t\n",
			"subject\n" + prefix + " between\n\n" + prefix + " after a blank\nTrailer: kept\n",
			"/ half of a two-byte prefix\n. half again\n*\n",
			"subject\n\nbody\n" + prefix + cutTail + "below the scissors\n",
			"\n" + prefix + " template first\n\n:sparkles:(x)^ add the thing\n",
		}
		for i, msg := range messages {
			for _, strip := range []bool{false, true} {
				args := []string{"-c", "core.commentChar=" + prefix, "stripspace"}
				if strip {
					args = append(args, "--strip-comments")
				}
				want := strings.TrimSuffix(pinnedGit(t, dir, msg, nil, args...), "\n")
				got := Apply(msg, Mode{Space: true, Comments: strip, Comment: prefix})
				if got != want {
					t.Errorf("prefix %q, message %d, strip=%v:\n  in:   %q\n  glyph %q\n  git   %q", prefix, i, strip, msg, got, want)
				}
			}
		}
	}
}

// TestScissorsUnderACommentPrefixMatchesGit holds the cut to a real commit:
// under a set prefix git cuts at that prefix's scissors line and not at the
// '#' one, which is content to it. `git stripspace` does not truncate, so each
// row is a `git commit --cleanup=scissors` through an editor that writes the
// message, read back from the commit object.
func TestScissorsUnderACommentPrefixMatchesGit(t *testing.T) {
	dir := t.TempDir()
	pinnedGit(t, dir, "", nil, "init", "-q", "-b", "main")
	for _, prefix := range []string{";", "//", ".*"} {
		t.Run(prefix, func(t *testing.T) {
			message := "subject\n\nbody\n" + cutLine + "kept under a set prefix\n" + prefix + cutTail + "below the scissors\n"
			buffer := filepath.Join(t.TempDir(), "message.txt")
			if err := os.WriteFile(buffer, []byte(message), 0o600); err != nil {
				t.Fatal(err)
			}
			editor := filepath.Join(t.TempDir(), "editor.sh")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\ncp \""+buffer+"\" \"$1\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			pinnedGit(t, dir, "", []string{"GIT_EDITOR=" + editor},
				"-c", "core.commentChar="+prefix, "commit", "-q", "--allow-empty", "--cleanup=scissors")
			object := pinnedGit(t, dir, "", nil, "cat-file", "commit", "HEAD")
			_, recorded, _ := strings.Cut(object, "\n\n")
			recorded = strings.TrimSuffix(recorded, "\n")

			got := Apply(message, Mode{Space: true, Truncate: true, Comment: prefix})
			if got != recorded {
				t.Fatalf("prefix %q:\n  glyph    %q\n  recorded %q", prefix, got, recorded)
			}
			if strings.Contains(recorded, "below the scissors") || !strings.Contains(recorded, "kept under a set prefix") {
				t.Fatalf("prefix %q: git no longer cuts where this test says it does — re-measure before trusting the row:\n%q", prefix, recorded)
			}
		})
	}
}
