package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/cleanup"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// hookCutLine is git's scissors line as the tests type it into a message.
const hookCutLine = "# ------------------------ >8 ------------------------"

// TestHookCutMatchesGit asks real git, in every cell of what the commit-msg
// hook can read — commit.cleanup × whether an editor runs × commit.verbose
// (unset, true, and -1, which git reads as unset) — and of the -v flag it
// cannot, whether the text the hook judges is the text git records, for a
// message carrying git's exact cut line with text under it.
//
// It compares TEXT because a verdict cannot see the cut: under the presets'
// subject-anchored grammar a message and its cut-short form get the same
// answer, so the hook cut in the wrong cells while every row of
// TestHookVerdictMatchesWhatGitRecords stayed green (t-3p3k (2): it cut only
// when an editor ran, and git cuts under commit.verbose with no editor too).
//
// Two families of cells cannot agree, and they are pinned in the direction
// DESIGN §2.1's residual paragraph states rather than skipped — if git ever
// changes, that paragraph is what has gone stale:
//   - an editor ran, nothing on (commit.verbose unset or -1), the mode is not
//     scissors: the hook cuts, because the -v it cannot see would make git cut
//     too, and git keeps the text under a line the author typed;
//   - -v on the command line and no editor: git cuts, and nothing tells the hook.
func TestHookCutMatchesGit(t *testing.T) {
	testutil.GitOrSkip(t)
	const below = "kept only where git does not cut"
	message := ":bug:(x)~ fix the thing\n\n" + hookCutLine + "\n" + below + "\n"

	dir := testutil.NewRepo(t)
	handed := filepath.Join(t.TempDir(), "handed-to-the-hook.txt")
	editorEnv := filepath.Join(t.TempDir(), "git-editor-env.txt")
	writeExecutable(t, filepath.Join(dir, ".git", "hooks", "commit-msg"),
		"#!/bin/sh\ncp \"$1\" "+handed+"\nprintf '%s' \"${GIT_EDITOR-UNSET}\" > "+editorEnv+"\nexit 0\n")
	buffer := filepath.Join(t.TempDir(), "buffer.txt")
	if err := os.WriteFile(buffer, []byte(message), 0o600); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	// The message goes ABOVE git's own buffer, so under -v git's cut line and
	// diff sit below the one the author typed, and the typed one is the first.
	editor := filepath.Join(t.TempDir(), "editor.sh")
	writeExecutable(t, editor, "#!/bin/sh\ncat \""+buffer+"\" \"$1\" > \"$1.glyph\" && mv \"$1.glyph\" \"$1\"\n")
	t.Chdir(dir)

	n := 0
	for _, mode := range []string{"", "default", "verbatim", "whitespace", "strip", "scissors"} {
		for _, edited := range []bool{true, false} {
			for _, verbose := range []string{"unset", "-v", "commit.verbose=true", "commit.verbose=-1"} {
				name := "cleanup=" + mode
				if mode == "" {
					name = "cleanup unset"
				}
				if edited {
					name += "/editor/"
				} else {
					name += "/-F/"
				}
				t.Run(name+verbose, func(t *testing.T) {
					configure(t, dir, "commit.cleanup", mode)
					configure(t, dir, "commit.verbose", map[string]string{"commit.verbose=true": "true", "commit.verbose=-1": "-1"}[verbose])
					configure(t, dir, "core.editor", map[bool]string{true: editor}[edited])

					n++
					if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Repeat("x", n)+"\n"), 0o600); err != nil {
						t.Fatalf("write file: %v", err)
					}
					testGit(t, dir, "akira-toriyama", "add", "a.txt")
					args := []string{"commit", "-q"}
					if verbose == "-v" {
						args = append(args, "-v")
					}
					if !edited {
						args = append(args, "-F", buffer)
					}
					testGit(t, dir, "akira-toriyama", args...)

					raw, err := os.ReadFile(handed) //nolint:gosec // a path this test just wrote
					if err != nil {
						t.Fatalf("the commit-msg hook did not run: %v", err)
					}
					env, err := os.ReadFile(editorEnv) //nolint:gosec // a path this test just wrote
					if err != nil {
						t.Fatalf("read the hook's GIT_EDITOR: %v", err)
					}
					if string(env) == "UNSET" {
						unsetEnv(t, "GIT_EDITOR")
					} else {
						t.Setenv("GIT_EDITOR", string(env))
					}
					// No message file to ask about: git commit hands the hook
					// COMMIT_EDITMSG, never git merge's MERGE_MSG.
					judged := cleanup.Apply(string(raw), hookCleanupMode(t.Context(), nil))
					// The object's own bytes: testGit trims, and a verbatim message
					// keeps trailing blank lines that are part of what git recorded.
					object, err := exec.Command("git", "-C", dir, "cat-file", "commit", "HEAD").Output()
					if err != nil {
						t.Fatalf("git cat-file: %v", err)
					}
					_, recorded, _ := strings.Cut(string(object), "\n\n")
					recorded = strings.TrimSuffix(recorded, "\n")

					hookCuts, gitCuts := !strings.Contains(judged, below), !strings.Contains(recorded, below)
					switch {
					case edited && (verbose == "unset" || verbose == "commit.verbose=-1") && mode != "scissors":
						if !hookCuts || gitCuts {
							t.Fatalf("DESIGN §2.1's editor residual moved: an editor with no verbose should be the hook cutting (%v) where git keeps (git cut %v)\n  judged:   %q\n  recorded: %q", hookCuts, gitCuts, judged, recorded)
						}
					case !edited && verbose == "-v":
						if hookCuts || !gitCuts {
							t.Fatalf("DESIGN §2.1's -v residual moved: -v with no editor should be git cutting (%v) where the hook cannot know (hook cut %v)\n  judged:   %q\n  recorded: %q", gitCuts, hookCuts, judged, recorded)
						}
					default:
						if judged != recorded {
							t.Fatalf("the hook judges text git does not record\n  handed:   %q\n  judged:   %q\n  recorded: %q", raw, judged, recorded)
						}
					}
				})
			}
		}
	}
}

// configure sets key in dir's repository config, or unsets it for "".
func configure(t *testing.T, dir, key, value string) {
	t.Helper()
	if value == "" {
		cmd := exec.Command("git", "-C", dir, "config", "--unset-all", key)
		cmd.Env = testutil.GitEnv("akira-toriyama")
		_ = cmd.Run() // exit 5 is "was not set", which is the state wanted
		return
	}
	testGit(t, dir, "akira-toriyama", "config", key, value)
}

// TestInstalledHookJudgesAGitMergeByMergesCleanup: `git merge` cleans its
// message with verbose 0 (builtin/merge.c) whatever commit.verbose says, and
// hands the commit-msg hook its own MERGE_MSG, which the installed hook
// redirects onto stdin — so the hook can tell a merge from a commit, and must
// cut the way the merge does. Both cases open the message with a typed cut
// line and git records the subject under it (measured):
//   - `git merge -F` under commit.cleanup=strip + commit.verbose=true: a hook
//     reading commit.verbose as git commit does cuts everything, judges an
//     empty message and stops the merge git would have made;
//   - `git merge --edit` in the default mode: a hook guessing -v from the
//     editor cuts everything the same way — the merge half of the editor
//     residual, closed by the same arm (git merge has no -v to guess).
//
// Real git runs the real installed hook against the glyph just built, because
// the MERGE_MSG identity only exists through git's own invocation.
func TestInstalledHookJudgesAGitMergeByMergesCleanup(t *testing.T) {
	glyphBin := buildGlyph(t)
	pathWithGlyph := filepath.Dir(glyphBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	msg := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msg, []byte(hookCutLine+"\n:bug:~ fix it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		config [][2]string
		args   []string
	}{
		{"merge -F under commit.cleanup=strip and commit.verbose", [][2]string{{"commit.cleanup", "strip"}, {"commit.verbose", "true"}}, []string{"-F", msg}},
		{"merge --edit in the default mode", nil, []string{"--edit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := testutil.NewRepo(t)
			t.Chdir(dir)
			if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
				t.Fatalf("hook install exit = %d\nstderr: %s", code, stderr)
			}
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic")
			appendFile(t, dir, "topic.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			testGit(t, dir, "akira-toriyama", "commit", "-q", "--no-verify", "-m", ":sparkles:^ land the topic")
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
			appendFile(t, dir, "main.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			testGit(t, dir, "akira-toriyama", "commit", "-q", "--no-verify", "-m", ":sparkles:^ move main")
			for _, kv := range tc.config {
				testGit(t, dir, "akira-toriyama", "config", kv[0], kv[1])
			}
			editor := filepath.Join(t.TempDir(), "editor.sh")
			writeExecutable(t, editor, "#!/bin/sh\ncat \""+msg+"\" > \"$1\"\n")
			testGit(t, dir, "akira-toriyama", "config", "core.editor", editor)

			out, err := runGit(dir, pathWithGlyph, append([]string{"merge", "-q", "--no-ff"}, append(tc.args, "topic")...)...)
			if err != nil {
				t.Fatalf("the hook stopped a merge git records: %v\n%s", err, out)
			}
			if got := testGit(t, dir, "akira-toriyama", "log", "-1", "--format=%B"); got != ":bug:~ fix it" {
				t.Fatalf("git recorded %q, want the subject under the typed cut line", got)
			}
		})
	}
}
