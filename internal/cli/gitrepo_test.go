package cli

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// testRepo builds a hermetic throwaway repository for the --range commands:
// pinned identity, the user's real git config held out (a global
// commit.gpgsign would break the test commits), one root commit, and a
// v0.1.0 tag on the base so bump has a current version to step from.
func testRepo(t *testing.T) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	testGit(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", ":tada: begin the project")
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// testGit runs one git command in dir as author and fails the test on error.
func testGit(t *testing.T, dir, author string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=committer",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// testCommit adds one empty commit authored by author with the given message.
func testCommit(t *testing.T, dir, author, message string) {
	t.Helper()
	testGit(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
}

// runGlyph executes the root command with args, returning the exit code and
// what was written to the payload and diagnostic streams.
func runGlyph(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	oldOut, oldErr := out, errOut
	out, errOut = &outBuf, &errBuf
	defer func() { out, errOut = oldOut, oldErr }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&errBuf) // cobra's own usage/help output is diagnostics here
	root.SetErr(&errBuf)
	code = finish(root.Execute())
	return code, outBuf.String(), errBuf.String()
}
