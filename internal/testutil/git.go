// Package testutil is the one home of the fixtures two or more packages'
// tests share: the hermetic git fixture glyph's tests run git under, and the
// loaded gemoji config (Config). The environment pin below lives here once
// and callers import it, never copy it: a copy of an incident-bearing block is
// how one side quietly loses the incident (DESIGN §5). Test-only, imported by
// any package's tests, importing nothing above internal/config; nothing
// shipping depends on it.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// RequireGit fails the test when git is not on PATH. Never a skip: every
// caller runs git, so a host without it has no green to earn.
func RequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is not on PATH, and this test runs it — install it rather than skip: %v", err)
	}
}

// GitEnv pins a hermetic git environment: fixed identity, the user's real
// global/system config held out (a global commit.gpgsign would otherwise break
// the test commits, under GitHub's noreply address so a local commit carries
// the login the notes credit — the fleet's commits do), and background
// maintenance off. Extra entries are
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
		"GIT_AUTHOR_EMAIL=1+"+author+"@users.noreply.github.com",
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

// NewRepo creates a temp repository with one root commit and returns its
// path. The root commit carries the gemoji-preset glyph.toml — glyph
// reads the config from the checkout's top level, so a fixture without one
// answers every command with "not initialized".
func NewRepo(t *testing.T) string {
	t.Helper()
	RequireGit(t)
	dir := t.TempDir()
	Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("gemoji preset missing")
	}
	if err := os.WriteFile(filepath.Join(dir, "glyph.toml"), preset, 0o644); err != nil {
		t.Fatalf("write glyph.toml: %v", err)
	}
	Git(t, dir, "akira-toriyama", "add", "glyph.toml")
	Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= begin the project")
	return dir
}

// NewTaggedRepo is NewRepo with a v0.1.0 tag on the root commit, and returns
// the root sha beside the path: the shape the walk-base and --range tests
// start from, so bump has a current version to step from.
func NewTaggedRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = NewRepo(t)
	Git(t, dir, "akira-toriyama", "tag", "v0.1.0")
	return dir, Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// NewPackagesRepo is NewRepo with lines declared: snippet — the [[packages]]
// entries — appended to its glyph.toml and files (path under the top level →
// content) written, in one commit on the root and with no tag: the shape the
// packages tests start from, each tagging the lines its case needs.
func NewPackagesRepo(t *testing.T, snippet string, files map[string]string) string {
	t.Helper()
	dir := NewRepo(t)
	preset, _ := config.Preset("gemoji") // NewRepo has failed the test if it is missing
	WriteFile(t, dir, "glyph.toml", string(preset)+snippet)
	for rel, content := range files {
		WriteFile(t, dir, rel, content)
	}
	Git(t, dir, "akira-toriyama", "add", ".")
	Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare the lines")
	return dir
}

// PackagesSnippet declares two lines, haiku/ and curry/: the [[packages]]
// entries the packages fixtures append to the gemoji preset.
const PackagesSnippet = "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \"curry\"\n"

// NewTwoLineRepo is NewPackagesRepo declaring PackagesSnippet's two lines with
// one file under each, untagged: each caller tags the lines its case needs.
func NewTwoLineRepo(t *testing.T) string {
	t.Helper()
	return NewPackagesRepo(t, PackagesSnippet, map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n"})
}

// WriteFile writes content at rel under dir, creating the directories above it.
func WriteFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// touchSeq makes every Touch write distinct content, so two commits with the
// same message under the same path still each have a diff.
var touchSeq atomic.Int64

// Touch commits one change under each of paths, as author, under message — a
// landed identity whose files local git answers for — and returns its sha.
func Touch(t *testing.T, dir, author, message string, paths ...string) string {
	t.Helper()
	n := touchSeq.Add(1)
	for _, p := range paths {
		WriteFile(t, dir, p, fmt.Sprintf("%s\n%d\n", p, n))
	}
	Git(t, dir, author, "add", "-A", ".")
	Git(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
	return Git(t, dir, author, "rev-parse", "HEAD")
}

// Commit adds one empty commit authored by author with the given message.
func Commit(t *testing.T, dir, author, message string) {
	t.Helper()
	Git(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
}

// CommitFrom is Commit under an explicit author address instead of GitEnv's
// noreply one — a contributor whose commits GitHub cannot map to an account.
//
// #nosec G204 -- the binary is the fixed literal "git"; every argument comes
// from the calling test's own source.
func CommitFrom(t *testing.T, dir, author, email, message string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", message)
	cmd.Env = GitEnv(author, "GIT_AUTHOR_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit as %s <%s>: %v\n%s", author, email, err, out)
	}
}

// SignCommits makes dir sign every later commit for real, and `git tag -s` with
// the same key: ssh signing with a key generated in the test's own temp dir,
// which needs no agent and no user config, and an allowed-signers file so git
// verifies what it signed. A missing ssh-keygen FAILS the test rather than
// skipping it — every CI runner has one, and a skip would leave the signing
// developer's path unguarded in silence.
//
// #nosec G204 -- the binary is ssh-keygen from PATH; every argument is a path
// this function just made.
func SignCommits(t *testing.T, dir string) {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatalf("ssh-keygen is required to make a signed commit: %v", err)
	}
	keys := t.TempDir()
	key := filepath.Join(keys, "signer")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "signer@example.invalid", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub") //nolint:gosec // a path this function just wrote
	if err != nil {
		t.Fatalf("read the public key: %v", err)
	}
	f := strings.Fields(string(pub))
	if len(f) < 2 {
		t.Fatalf("unexpected public key %q", pub)
	}
	allowed := filepath.Join(keys, "allowed_signers")
	if err := os.WriteFile(allowed, []byte("signer@example.invalid "+f[0]+" "+f[1]+"\n"), 0o600); err != nil {
		t.Fatalf("write allowed signers: %v", err)
	}
	for _, kv := range [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingKey", key},
		{"gpg.ssh.allowedSignersFile", allowed},
		{"commit.gpgSign", "true"},
	} {
		Git(t, dir, "akira-toriyama", "config", kv[0], kv[1])
	}
}

// Config is the gemoji preset loaded — the glyph.toml every NewRepo fixture
// carries — for tests that call a package's plumbing directly rather than
// through a command.
func Config(t *testing.T) *config.Config {
	t.Helper()
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("gemoji preset missing")
	}
	cfg, err := config.Load(preset)
	if err != nil {
		t.Fatalf("load gemoji preset: %v", err)
	}
	return cfg
}
