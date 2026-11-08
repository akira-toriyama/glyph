package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// The caller's input is judged before the environment, on every verdict
// command: a flag that is wrong is usage (2) whatever glyph.toml, git, the
// API or the ref guard would have answered. Every test here asserts the
// exact code, in the shape of TestLintRangeUsageGuards, and the stderr
// fragment that proves WHICH refusal spoke: a 2 from a later gate (the
// repository guard, a missing config) would satisfy the code alone.

// unreadableConfig stages the fixture's glyph.toml as a file the filesystem
// refuses to read, the shape that answered 4 ahead of every flag check.
func unreadableConfig(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 is still readable, so the refusal cannot be staged")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits do not deny the owner a read on Windows")
	}
	path := filepath.Join(dir, "glyph.toml")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("staging the read refusal: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

// TestVerdictCommandsJudgeTheirFlagsBeforeTheConfig (t-h9m7): bump, notes
// and preview check the shape of the source they were given — an empty or
// option-shaped --range, a --pr below 1, a malformed --since-tag — before
// glyph.toml is loaded, as lint's arms always did. Measured at 2f9428a over a
// mode-000 glyph.toml: every bump / notes / preview case below answered the
// config's 4 ("permission denied") while `lint --range=` answered 2.
func TestVerdictCommandsJudgeTheirFlagsBeforeTheConfig(t *testing.T) {
	usePR(t, walkServer(t, nil))
	dir, _ := testutil.NewTaggedRepo(t)
	unreadableConfig(t, dir)
	t.Chdir(dir)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bump", "--range="}, "--range needs a git revision range"},
		{[]string{"bump", "--range", "-x"}, "looks like an option, not a revision range"},
		{[]string{"bump", "--pr", "0"}, "--pr 0 is not a pull-request number"},
		{[]string{"bump", "--since-tag="}, "--since-tag= names an empty tag"},
		{[]string{"bump", "--since-tag=-x"}, "looks like an option, not a tag"},
		{[]string{"bump", "--since-tag=a..b"}, "looks like a revision range"},
		{[]string{"notes", "--range="}, "--range needs a git revision range"},
		{[]string{"notes", "--pr", "0"}, "--pr 0 is not a pull-request number"},
		{[]string{"notes", "--since-tag="}, "--since-tag= names an empty tag"},
		{[]string{"preview", "--pr", "0"}, "--pr 0 is not a pull-request number"},
		{[]string{"lint", "--range="}, "--range needs a git revision range"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != 2 || stdout != "" {
				t.Fatalf("%v exited %d with %q on stdout, want 2 and nothing\nstderr: %s", tc.args, code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "permission denied") {
				t.Errorf("%v must be refused as the flag (%q), before the config is read:\n%s", tc.args, tc.want, stderr)
			}
		})
	}
}

// TestCurrentIsJudgedBeforeTheWalk (t-s9ap): --current is parsed at bump's
// and release's entrance, with the source's shape — before the config, the
// range, the API and release's ref guard. Measured at 2f9428a: `bump
// --current=garbage --range nosuch..HEAD` answered git's 4 ("bad revision"),
// `bump --since-tag --current=bogus` the dead API's 4, and `release
// --current=bogus` from refs/heads/topic the ref guard's 4 — all for a flag no
// retry, fetch or branch fixes. The stand-in API fails the test on any
// request, so the 2 is proven to come before the walk.
func TestCurrentIsJudgedBeforeTheWalk(t *testing.T) {
	usePR(t, walkServer(t, nil))
	dir, _ := testutil.NewTaggedRepo(t)
	unreadableConfig(t, dir)
	t.Chdir(dir)
	useActionsRun(t, "refs/heads/topic", "main")

	for _, args := range [][]string{
		{"bump", "--current=garbage", "--range", "nosuch..HEAD"},
		{"bump", "--current=garbage", "--since-tag"},
		{"release", "--current=garbage", "--dry-run"},
		{"release", "--current=garbage"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, args...)
			if code != 2 || stdout != "" {
				t.Fatalf("%v exited %d with %q on stdout, want 2 and nothing\nstderr: %s", args, code, stdout, stderr)
			}
			if !strings.Contains(stderr, "--current") || strings.Contains(stderr, "default branch") || strings.Contains(stderr, "permission denied") {
				t.Errorf("%v must be refused as --current, before the config, the walk and the ref guard:\n%s", args, stderr)
			}
		})
	}
}

// TestSinceTagNamingNoCommitIsUsage (t-tat8, t-3dfg): a --since-tag value
// that names no commit this checkout holds is the caller's input, refused at
// 2 by the one resolver bump, notes and release share, naming the value and
// where to look — `git tag`, and fetch-depth: 0 for a checkout that fetched
// no tags. Measured at 2f9428a and v6.1.0: `bump --since-tag=v0.0.0` with no
// such tag died in `git log` at 4 ("bad revision 'v0.0.0..HEAD'"), the code
// that tells a CI wrapper a retry can help. The whitespace cases are the
// trimmed-value half: `--since-tag=' -x'` reached git as `-x..HEAD` at 4
// while `-x` was refused at 2, because CheckTag judged the untrimmed value.
// The API is never reached: the stand-in fails the test on any request.
func TestSinceTagNamingNoCommitIsUsage(t *testing.T) {
	usePR(t, walkServer(t, nil))
	dir, _ := testutil.NewTaggedRepo(t) // tags v0.1.0
	t.Chdir(dir)

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"bump", "--since-tag=v0.0.0"}, []string{`"v0.0.0" names no commit`, "`git tag`", "fetch-depth: 0"}},
		{[]string{"notes", "--since-tag=v0.0.0"}, []string{`"v0.0.0" names no commit`}},
		{[]string{"release", "--since-tag=v0.0.0", "--dry-run"}, []string{`"v0.0.0" names no commit`}},
		{[]string{"bump", "--since-tag=nosuch-branch"}, []string{`"nosuch-branch" names no commit`}},
		{[]string{"bump", "--since-tag= -x"}, []string{"looks like an option, not a tag"}},
		{[]string{"bump", "--since-tag=\t-x"}, []string{"looks like an option, not a tag"}},
		{[]string{"notes", "--since-tag= a..b"}, []string{"looks like a revision range"}},
		{[]string{"release", "--since-tag= -x", "--dry-run"}, []string{"looks like an option, not a tag"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != 2 || stdout != "" {
				t.Fatalf("%v exited %d with %q on stdout, want 2 and nothing\nstderr: %s", tc.args, code, stdout, stderr)
			}
			env := decodeErrorEnvelope(t, stderr)
			for _, want := range tc.want {
				if !strings.Contains(env.Message, want) {
					t.Errorf("%v: the refusal is missing %q: %q", tc.args, want, env.Message)
				}
			}
			if strings.Contains(env.Message, "bad revision") {
				t.Errorf("%v surfaced git's failure instead of the flag's refusal: %q", tc.args, env.Message)
			}
		})
	}

	// The positive control: the value is judged for existence, not for being
	// a tag — a branch and a sha still walk (TestCompareLinkNeedsATagBase
	// holds the compare-link half), and a leading space on a real tag is
	// trimmed and accepted.
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "v0.1.0")
	testutil.Git(t, dir, "akira-toriyama", "branch", "old-main", base)
	for _, from := range []string{"old-main", base, " v0.1.0"} {
		if code, _, stderr := runGlyph(t, "bump", "--since-tag="+from); code != 1 {
			t.Errorf("bump --since-tag=%q exited %d, want 1 (an empty range folds none) — the value names a commit\nstderr: %s", from, code, stderr)
		}
	}
}

// TestLintInputModeConflictsAreJudgedByValue (t-0b93): lint's input modes
// conflict on what SELECTS — a valued flag given, --stdin true — not on
// what was mentioned. cobra's grouping refused `--stdin=false --message=…`
// saying "[message stdin] were all set", a false statement in the envelope
// other repositories parse (measured at 2f9428a); --stdin=false selects
// nothing, as hook install's --print=false does, so the message is linted
// and the range is judged. Two modes genuinely on are still a conflict, and
// --stdin=false alone still selects no mode (TestLintStdinFalseIsUsageNotAVerdict).
func TestLintInputModeConflictsAreJudgedByValue(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	for name, tc := range map[string]struct {
		args []string
		code int
		want string
	}{
		"stdin off beside a message lints the message": {[]string{"lint", "--stdin=false", "--message", ":bug:~ fix a crash"}, 0, ""},
		"stdin off beside a range judges the range":    {[]string{"lint", "--stdin=false", "--range", base + "..HEAD"}, 0, ""},
		"stdin on beside a message is a conflict":      {[]string{"lint", "--stdin", "--message", ":bug:~ fix a crash"}, 2, "--message and --stdin cannot be combined"},
		"two valued modes are a conflict":              {[]string{"lint", "--range", base + "..HEAD", "--message", "x"}, 2, "--range and --message cannot be combined"},
		"repo beside stdin off and a range is refused": {[]string{"lint", "--repo", "o/r", "--stdin=false", "--range", base + "..HEAD"}, 2, "--repo rides only with --pr"},
	} {
		t.Run(name, func(t *testing.T) {
			setStdin(t, "")
			code, _, stderr := runGlyph(t, tc.args...)
			if code != tc.code {
				t.Fatalf("%v exited %d, want %d\nstderr: %s", tc.args, code, tc.code, stderr)
			}
			if strings.Contains(stderr, "were all set") {
				t.Errorf("%v: the envelope claims a flag was set that the caller turned off:\n%s", tc.args, stderr)
			}
			if tc.want != "" && !strings.Contains(stderr, tc.want) {
				t.Errorf("%v: the refusal must name the flags that select (%q):\n%s", tc.args, tc.want, stderr)
			}
		})
	}
}

// TestOutsideARepositoryIsUsage (t-ckvh, ruled 2026-11-08): the caller ran
// glyph where no work tree is — outside any repository, or inside a
// checkout's .git — and that is the caller's input (2) on every verdict
// command and on hook install, the answer `init` already gave both places;
// git was reached and said so, so this is not the no-answer code. Measured
// at 2f9428a: every verdict command and `hook install` answered git's 4
// ("not a git repository" / "must be run in a work tree"). The two
// exemptions stand: `hook install --print` writes a script and needs no
// repository, and inside .git the hooks directory is git's own answer, so
// the install proceeds. The commit-msg hook's contract rides on this too:
// the installed script blocks on 3 alone, and 2 passes the commit through as
// 4 did (TestLintStdinOutsideACheckoutIsNeverAViolation).
func TestOutsideARepositoryIsUsage(t *testing.T) {
	testutil.RequireGit(t)
	usePR(t, walkServer(t, nil))
	checkout := t.TempDir()
	testutil.Git(t, checkout, "akira-toriyama", "init", "-q", "-b", "main")

	verdicts := [][]string{
		{"lint", "--message", ":bug:~ fix a crash"},
		{"lint", "--stdin"},
		{"lint", "--range", "main..HEAD"},
		{"bump", "--range", "main..HEAD"},
		{"notes", "--range", "main..HEAD"},
		{"preview", "--pr", "1"},
		{"release", "--dry-run"},
	}
	for where, tc := range map[string]struct {
		dir  string
		args [][]string
		want string
	}{
		"outside a repository": {t.TempDir(), append(verdicts, []string{"hook", "install"}), "not a git repository"},
		"inside .git":          {filepath.Join(checkout, ".git"), verdicts, "inside a git directory, which has no work tree"},
	} {
		t.Run(where, func(t *testing.T) {
			t.Chdir(tc.dir)
			for _, args := range tc.args {
				setStdin(t, ":bug:~ fix a crash\n")
				code, stdout, stderr := runGlyph(t, args...)
				if code != int(core.CodeUsage) || stdout != "" {
					t.Errorf("%v %s exited %d with %q on stdout, want exactly %d and nothing\nstderr: %s", args, where, code, stdout, core.CodeUsage, stderr)
					continue
				}
				if !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "fatal:") {
					t.Errorf("%v %s must be refused as the caller's place (%q), not with git's raw failure:\n%s", args, where, tc.want, stderr)
				}
			}
		})
	}

	t.Run("hook install --print needs no repository", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if code, stdout, stderr := runGlyph(t, "hook", "install", "--print", "commit-msg"); code != 0 || !strings.Contains(stdout, "glyph") {
			t.Fatalf("hook install --print outside a repository exited %d, want 0 with the script\nstderr: %s", code, stderr)
		}
	})
	t.Run("hook install inside .git writes the hooks directory", func(t *testing.T) {
		t.Chdir(filepath.Join(checkout, ".git"))
		if code, _, stderr := runGlyph(t, "hook", "install", "commit-msg"); code != 0 {
			t.Fatalf("hook install inside .git exited %d, want 0 — the hooks directory is git's own answer there\nstderr: %s", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(checkout, ".git", "hooks", "commit-msg")); err != nil {
			t.Errorf("the hook was not written where git runs it: %v", err)
		}
	})
}

// TestReleaseFooterFileUnreadableIsTheNoAnswerCode (t-f5x9): --footer-file
// is classified as glyph.toml is (DESIGN §5, the 2/4 line): a path holding
// nothing, or something other than a regular file, was observed and is the
// caller's input (2); a read the filesystem refused observed nothing and is
// 4, the code the same event carries for glyph.toml, a hook and init's
// write. Measured at 86eab14: a mode-000 footer answered 2 where a mode-000
// glyph.toml answered 4. The path is named once — the *fs.PathError already
// carries it.
func TestReleaseFooterFileUnreadableIsTheNoAnswerCode(t *testing.T) {
	usePR(t, walkServer(t, nil))
	dir, _ := testutil.NewTaggedRepo(t)
	t.Chdir(dir)

	footer := filepath.Join(t.TempDir(), "install.md")
	if err := os.WriteFile(footer, []byte("## Install\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("a read the filesystem refused", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: mode 000 is still readable, so the refusal cannot be staged")
		}
		if runtime.GOOS == "windows" {
			t.Skip("POSIX mode bits do not deny the owner a read on Windows")
		}
		if err := os.Chmod(footer, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(footer, 0o600) })
		code, _, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", footer)
		if code != int(core.CodeAPI) {
			t.Fatalf("an unreadable --footer-file exited %d, want exactly %d (no trustworthy answer)\nstderr: %s", code, core.CodeAPI, stderr)
		}
		if env := decodeErrorEnvelope(t, stderr); env.Code != int(core.CodeAPI) || strings.Count(env.Message, footer) != 1 || !strings.Contains(env.Message, "permission denied") {
			t.Errorf("the envelope must carry code %d and name the path once with the refused read: %q", core.CodeAPI, env.Message)
		}
	})
	t.Run("a directory at the path", func(t *testing.T) {
		code, _, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", t.TempDir())
		if code != int(core.CodeUsage) {
			t.Fatalf("a directory as --footer-file exited %d, want exactly %d (what is there was observed)\nstderr: %s", code, core.CodeUsage, stderr)
		}
		if !strings.Contains(stderr, "is a directory, not a regular file") {
			t.Errorf("the refusal must say what was found:\n%s", stderr)
		}
	})
}
