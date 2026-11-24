package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestInstalledHookJudgesUnderTheAuthorGitExports: git exports to the
// commit-msg hook the author it will record — --amend, --author= and a rebase
// reword carry the bot's name — and exports none under git merge. The hook
// skips an exclude_authors author's message as `lint --range` skips the commit,
// and judges a merge under the empty author (t-cgz9; measured 2026-11-15 on
// git 2.54.0 and 2.56.0 before the fix: the hook refused at 3 every bot commit
// --range skipped at 0, so amending a dependabot commit needed --no-verify).
//
// The commands run with GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL out of the
// environment and the identity in config, so every author the hook sees is
// one git exported: testutil.GitEnv's own export would reach the hook
// whatever git did. The merge arm configures the bot as the identity, so a
// hook that guessed the author from config instead of reading git's export
// would skip the merge — it must be judged, and refused.
func TestInstalledHookJudgesUnderTheAuthorGitExports(t *testing.T) {
	glyphBin := buildGlyph(t)
	path := filepath.Dir(glyphBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	const (
		bot      = "dependabot[bot]"
		botIdent = bot + " <49699333+" + bot + "@users.noreply.github.com>"
		skipped  = `nothing linted: the commit's author "dependabot[bot]" is excluded from the convention (exclude_authors)`
		refused  = "matches none of the"
	)
	// repo has a bot commit on main whose message no pattern claims, as it
	// arrives from the remote, and the installed hooks.
	repo := func(t *testing.T, user string) string {
		t.Helper()
		dir := testutil.NewRepo(t)
		t.Chdir(dir)
		if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
			t.Fatalf("hook install exit = %d\nstderr: %s", code, stderr)
		}
		testutil.Git(t, dir, bot, "commit", "-q", "--allow-empty", "--no-verify", "-m", "Bump the thing from 1 to 2")
		testutil.Git(t, dir, "x", "config", "user.name", user)
		testutil.Git(t, dir, "x", "config", "user.email", "1+someone@users.noreply.github.com")
		return dir
	}
	for _, tc := range []struct {
		name   string
		user   string
		args   []string
		code   int
		author string
		stderr string
	}{
		{"commit --amend --no-edit of the bot's commit", "akira-toriyama", []string{"commit", "-q", "--allow-empty", "--amend", "--no-edit"}, 0, bot, skipped},
		{"commit --author= the bot", "akira-toriyama", []string{"commit", "-q", "--allow-empty", "--author=" + botIdent, "-m", "Bump another thing from 2 to 3"}, 0, bot, skipped},
		{"rebase -i reword of the bot's commit", "akira-toriyama", []string{"rebase", "-q", "-i", "HEAD~1"}, 0, bot, skipped},
		{"control: commit --amend --reset-author", "akira-toriyama", []string{"commit", "-q", "--allow-empty", "--amend", "--reset-author", "--no-edit"}, 1, bot, refused},
		{"control: a human's commit", "akira-toriyama", []string{"commit", "-q", "--allow-empty", "-m", "Bump by a human"}, 1, bot, refused},
		{"git merge exports no author: judged under the empty one", bot, []string{"merge", "-q", "--no-ff", "-m", "Bump through a merge", "topic"}, 1, bot, refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := repo(t, tc.user)
			if tc.args[0] == "merge" {
				testutil.Git(t, dir, "x", "checkout", "-q", "-b", "topic", "HEAD~1")
				testutil.WriteFile(t, dir, "topic.txt", "x\n")
				testutil.Git(t, dir, "x", "add", "-A")
				testutil.Git(t, dir, "x", "commit", "-q", "--no-verify", "-m", ":sparkles:^ land the topic")
				testutil.Git(t, dir, "x", "checkout", "-q", "main")
			}
			sequence := filepath.Join(t.TempDir(), "sequence.sh")
			writeExecutable(t, sequence, "#!/bin/sh\nsed 's/^pick /reword /' \"$1\" > \"$1.glyph\" && mv \"$1.glyph\" \"$1\"\n")
			editor := filepath.Join(t.TempDir(), "editor.sh")
			writeExecutable(t, editor, "#!/bin/sh\nprintf 'Bump the thing from 1 to 3\\n' > \"$1\"\n")
			code, out := runGitUnexported(dir, path, []string{"GIT_SEQUENCE_EDITOR=" + sequence, "GIT_EDITOR=" + editor}, tc.args...)
			if code != tc.code {
				t.Fatalf("git %v exit = %d, want %d\n%s", tc.args, code, tc.code, out)
			}
			if !strings.Contains(out, tc.stderr) {
				t.Fatalf("git %v output lacks %q:\n%s", tc.args, tc.stderr, out)
			}
			if got := testutil.Git(t, dir, "x", "log", "-1", "--format=%an", "main"); tc.code == 0 && got != tc.author {
				t.Fatalf("git recorded author %q, want %q", got, tc.author)
			}
		})
	}
}

// runGitUnexported is runGit with GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL removed
// from the environment, so the author the commit-msg hook sees is the one git
// itself exports. The exit code comes back exact: 1 is git refusing on the
// hook's verdict.
func runGitUnexported(dir, path string, extra []string, args ...string) (int, string) {
	var env []string
	for _, kv := range testutil.GitEnv("unused", append([]string{"PATH=" + path}, extra...)...) {
		if !strings.HasPrefix(kv, "GIT_AUTHOR_NAME=") && !strings.HasPrefix(kv, "GIT_AUTHOR_EMAIL=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		return -1, string(out) + err.Error()
	}
	return 0, string(out)
}
