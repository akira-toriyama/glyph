// Package testutil is the single source of the hermetic git fixture that
// internal/cli and internal/gitsource both exercise glyph against. The
// environment pin below used to live as verbatim copies in each package (and a
// third, partial copy in the hook tests had already lost the maintenance pin)
// — copies of an incident-bearing block are how one side quietly loses the
// incident, so the block lives here once and the callers import it.
package testutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// GitOrSkip skips the test when git is absent (never on CI, where git is a
// given — this guards exotic local environments only).
func GitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// GitEnv pins a hermetic git environment: fixed identity, the user's real
// global/system config held out (a global commit.gpgsign would otherwise break
// the test commits), and background maintenance off. Extra entries are
// appended after the pin — the hook tests use that to put a controlled PATH in
// front of the shell the hook runs under.
//
// The maintenance pin carries an incident. Measured once in CI: t.TempDir's
// cleanup failed ENOTEMPTY on a fixture's .git while the test's own assertions
// all passed — something still had a hand in the directory. Every git the
// callers run is wait()ed, so the only process git could leave behind is
// detached auto-maintenance after a commit; pinning gc.auto / gc.autoDetach /
// maintenance.auto off removes that writer entirely rather than betting on the
// fixture staying too small to trigger it. (Mechanism inferred from
// elimination, not reproduced locally.)
func GitEnv(author string, extra ...string) []string {
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=committer",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=gc.auto", "GIT_CONFIG_VALUE_0=0",
		"GIT_CONFIG_KEY_1=gc.autoDetach", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=maintenance.auto", "GIT_CONFIG_VALUE_2=false",
	)
	return append(env, extra...)
}

// Git runs one git command in dir as author and fails the test on error.
//
// #nosec G204 -- the binary is the fixed literal "git"; every argument comes
// from the calling test's own source.
func Git(t *testing.T, dir, author string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = GitEnv(author)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo creates a temp repository with one root commit and returns its path.
func NewRepo(t *testing.T) string {
	t.Helper()
	GitOrSkip(t)
	dir := t.TempDir()
	Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", ":tada: begin the project")
	return dir
}

// Commit adds one empty commit authored by author with the given message.
func Commit(t *testing.T, dir, author, message string) {
	Git(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
}
