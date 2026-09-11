package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/akira-toriyama/glyph/v3/internal/core"
)

// The config a verdict command loads is the YARDSTICK, not the subject. On
// lint / bump / notes / preview / release the thing being judged is a commit,
// so a glyph.toml that will not load means no commit was judged at all —
// reporting the gate code 3 there tells a CI gate a commit was rejected when
// none was read (t-c6r5). DESIGN §5 already scopes 3 that way ("a commit
// message under `lint`, a repository's own configuration under `doctor`");
// these tests are what makes the code obey it.
//
// Two live consequences ride on this, which is why the exact integers are
// asserted and not merely "non-zero":
//
//   - lint.yml's default-branch push arm swallows exit 3 alone. An unreadable
//     glyph.toml classified as 3 returned a GREEN gate having judged nothing.
//   - the installed commit-msg and pre-push hooks block on exit 3 alone. A
//     typo in glyph.toml classified as 3 blocked every commit in the clone —
//     including the commit that would repair the typo.

// seedConfig overwrites the fixture repo's glyph.toml with body.
func seedConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "glyph.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seeding glyph.toml: %v", err)
	}
	return path
}

// TestLintOnAnUnparseableConfigIsUsageNotTheGateCode pins exit 2 for a
// glyph.toml that is present and readable but says something invalid. It is
// the same position as a MISSING config — an unusable configuration a human
// repairs by editing a file — and a missing one is already 2, so the two take
// the same code rather than a third answer for one condition.
func TestLintOnAnUnparseableConfigIsUsageNotTheGateCode(t *testing.T) {
	dir, _ := testRepo(t)
	seedConfig(t, dir, "schema = 1 {{{ this is not toml\n")
	t.Chdir(dir)

	setStdin(t, ":bug:(cli)~ fix a thing\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != int(core.CodeUsage) {
		t.Fatalf("exit = %d, want %d (usage); 3 would tell the hooks to block the commit that "+
			"repairs this very file, and tell lint.yml's push arm to swallow it\nstderr: %s", code, core.CodeUsage, stderr)
	}
	env := decodeErrorEnvelope(t, stderr)
	if env.Code != int(core.CodeUsage) {
		t.Errorf("envelope code = %d, want %d", env.Code, core.CodeUsage)
	}
}

// TestLintOnAnUnreadableConfigIsTheNoAnswerCode pins exit 4 when the
// filesystem refuses the read. `doctor` (internal/doctor/config.go, on
// *fs.PathError) and the hook installer (internal/hook/hook.go, core.APIf)
// already call this identical event 4; the verdict commands called it 3,
// which made one event carry three different codes depending on who asked.
func TestLintOnAnUnreadableConfigIsTheNoAnswerCode(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 is still readable, so the refusal cannot be staged")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits do not deny the owner a read on Windows")
	}
	dir, _ := testRepo(t)
	path := seedConfig(t, dir, "schema = 1\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("staging the read refusal: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	t.Chdir(dir)

	setStdin(t, ":bug:(cli)~ fix a thing\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != int(core.CodeAPI) {
		t.Fatalf("exit = %d, want %d (no trustworthy answer); an I/O failure is not a commit "+
			"violating the convention, and on 3 lint.yml's push arm returns green having judged "+
			"nothing\nstderr: %s", code, core.CodeAPI, stderr)
	}
	env := decodeErrorEnvelope(t, stderr)
	if env.Code != int(core.CodeAPI) {
		t.Errorf("envelope code = %d, want %d", env.Code, core.CodeAPI)
	}
}

// TestMissingConfigStaysUsage is the unchanged third arm, kept beside the two
// that moved so the whole classification is read in one place — and as the
// positive control for the pair above: it proves this fixture reaches the
// config load at all, so an exit 2 there is the classification answering and
// not the command failing earlier for some unrelated reason.
func TestMissingConfigStaysUsage(t *testing.T) {
	dir, _ := testRepo(t)
	if err := os.Remove(filepath.Join(dir, "glyph.toml")); err != nil {
		t.Fatalf("removing glyph.toml: %v", err)
	}
	t.Chdir(dir)

	setStdin(t, ":bug:(cli)~ fix a thing\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != int(core.CodeUsage) {
		t.Fatalf("exit = %d, want %d (usage)\nstderr: %s", code, core.CodeUsage, stderr)
	}
}

// TestValidConfigStillPasses is the non-vacuity control for all three: if the
// fixture stopped accepting this message, every assertion above would be
// satisfied by a command that never got as far as reading a config.
func TestValidConfigStillPasses(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	setStdin(t, ":bug:(cli)~ fix a thing\n")
	if code, _, stderr := runGlyph(t, "lint", "--stdin"); code != int(core.CodeOK) {
		t.Fatalf("exit = %d, want 0 on the untouched fixture; the three tests above are then "+
			"asserting against a command that fails before the config load\nstderr: %s", code, stderr)
	}
}
