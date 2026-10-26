package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// The config a verdict command loads is the YARDSTICK, not the subject. On
// lint / bump / notes / preview / release the thing being judged is a commit,
// so a glyph.toml that will not load means no commit was judged at all —
// reporting the gate code 3 there tells a CI gate a commit was rejected when
// none was read (t-c6r5). DESIGN §5 already scopes 3 that way ("a commit
// message under `lint`, a repository's own configuration under `doctor`");
// these tests are what makes the code obey it.
//
// Three live consequences ride on this, which is why the exact integers are
// asserted and not merely "non-zero":
//
//   - lint.yml's default-branch push arm swallows exit 3 alone. An unreadable
//     glyph.toml classified as 3 returned a GREEN gate having judged nothing.
//   - the installed commit-msg and pre-push hooks block on exit 3 alone. A
//     typo in glyph.toml classified as 3 blocked every commit in the clone —
//     including the commit that would repair the typo.
//   - goreleaser.yml's tag-time notes step publishes exit 3 as the wedge
//     escape's placeholder. A glyph.toml classified as 3 there ships a release
//     whose body blames a commit nobody judged.

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
	dir, _ := testutil.NewTaggedRepo(t)
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
	dir, _ := testutil.NewTaggedRepo(t)
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
	dir, _ := testutil.NewTaggedRepo(t)
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
	dir, _ := testutil.NewTaggedRepo(t)
	t.Chdir(dir)

	setStdin(t, ":bug:(cli)~ fix a thing\n")
	if code, _, stderr := runGlyph(t, "lint", "--stdin"); code != int(core.CodeOK) {
		t.Fatalf("exit = %d, want 0 on the untouched fixture; the three tests above are then "+
			"asserting against a command that fails before the config load\nstderr: %s", code, stderr)
	}
}

// notAFile stages the shapes other than a regular file that t-zvax measured at
// a glyph.toml path: a directory, a named pipe, and a link that resolves to no
// file — to nothing, to itself, through a regular file, to a name past
// NAME_MAX (255). kind is the phrase config.NotAFileError names it by.
var notAFile = []struct {
	shape, kind string
	stage       func(path string) error
}{
	{"a directory", "a directory", func(p string) error { return os.Mkdir(p, 0o700) }},
	{"a named pipe", "a named pipe", func(p string) error { return syscall.Mkfifo(p, 0o600) }},
	{"a link to nothing", "a broken symbolic link", func(p string) error { return os.Symlink("nowhere.toml", p) }},
	{"a link to itself", "a broken symbolic link", func(p string) error { return os.Symlink(filepath.Base(p), p) }},
	{"a link through a file", "a broken symbolic link", func(p string) error {
		if err := os.WriteFile(filepath.Join(filepath.Dir(p), "shared.toml"), nil, 0o600); err != nil {
			return err
		}
		return os.Symlink("shared.toml/glyph.toml", p)
	}},
	{"a link to an over-long name", "a broken symbolic link", func(p string) error { return os.Symlink(strings.Repeat("a", 300), p) }},
}

// runGlyphWithin is runGlyph under a deadline, for a run a regression can
// block for good: open(2) on a named pipe waits for a writer, and a FIFO named
// glyph.toml hung every command that loaded it (t-zvax). On the deadline the
// test fails and the run's goroutine is left blocked on a FIFO inside
// t.TempDir that nothing will ever open for writing — one failed test, not a
// package hung until go test's -timeout.
func runGlyphWithin(t *testing.T, d time.Duration, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	type result struct {
		code           int
		stdout, stderr string
	}
	done := make(chan result, 1)
	go func() {
		code, stdout, stderr := runGlyph(t, args...)
		done <- result{code, stdout, stderr}
	}()
	select {
	case r := <-done:
		return r.code, r.stdout, r.stderr
	case <-time.After(d):
		t.Fatalf("glyph %s has not returned after %v", strings.Join(args, " "), d)
		return 0, "", ""
	}
}

// TestVerdictCommandsRefuseAGlyphTomlThatIsNotAFile pins exit 2 for a
// glyph.toml path holding a directory, a named pipe or a link that resolves to
// no file: what is there was observed, so it takes the missing config's code
// and remedy, not the unreadable arm's 4. Before the loader stat'ed the path,
// a directory answered 4 on both commands and a named pipe answered nothing at
// all — lint --stdin and bump hung in the read (t-zvax, measured at ec53ed3).
// Read through os.Stat alone (the loader mutation row
// config-stat-reads-a-broken-link-as-missing-or-unreadable restores), a link to
// itself, through a file or to an over-long name answers 4, and a link to
// nothing the missing file's remedy, which init refuses. The message is asserted beside the code because
// 2 is also what a bad flag answers.
func TestVerdictCommandsRefuseAGlyphTomlThatIsNotAFile(t *testing.T) {
	for _, k := range notAFile {
		for _, args := range [][]string{{"lint", "--stdin"}, {"bump", "--range", "v0.1.0..HEAD"}} {
			t.Run(k.shape+"/"+args[0], func(t *testing.T) {
				dir, _ := testutil.NewTaggedRepo(t)
				top, err := filepath.EvalSymlinks(dir) // git names the real path
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(top, "glyph.toml")
				if err := os.Remove(path); err != nil {
					t.Fatalf("removing glyph.toml: %v", err)
				}
				if err := k.stage(path); err != nil {
					t.Fatalf("staging %s at %s: %v", k.kind, path, err)
				}
				t.Chdir(dir)

				setStdin(t, ":bug:(cli)~ fix a thing\n")
				code, _, stderr := runGlyphWithin(t, 30*time.Second, args...)
				if code != int(core.CodeUsage) {
					t.Fatalf("exit = %d, want %d (usage, the missing config's code) — what is at the path was "+
						"observed, nothing was left unread\nstderr: %s", code, core.CodeUsage, stderr)
				}
				env := decodeErrorEnvelope(t, stderr)
				// Both halves of the remedy: glyph init refuses a path that
				// is not a regular file, so without "remove it" the
				// refusal sends the user into a second one.
				if !strings.Contains(env.Message, path+" is "+k.kind) || !strings.Contains(env.Message, "remove it") ||
					!strings.Contains(env.Message, "glyph init") {
					t.Errorf("the refusal must name what is at the path and hand over the remedy, got %q", env.Message)
				}
			})
		}
	}
}

// configWithCommitStyle is a glyph.toml whose [commit] table is shaped as
// every preset wrote it through v5.0.0: the style label beside the template.
// The label left the schema (t-85y9); the grammar under it is the gemoji
// preset's in miniature, so the same file without the line lints a gemoji
// subject green.
const configWithCommitStyle = `schema = 1

[commit]
style = "gemoji"
template = '<:code:>[(scope)]<semver_sigil> <subject>'

[[patterns]]
pattern = '^:[a-z0-9_]+:(\([a-z0-9-]+\))?(?P<semver_sigil>[=~^!%]) .+'
`

// withoutCommitStyle is configWithCommitStyle with the line deleted — the
// whole migration a repository that still has it performs.
func withoutCommitStyle(t *testing.T) string {
	t.Helper()
	without := strings.Replace(configWithCommitStyle, "style = \"gemoji\"\n", "", 1)
	if without == configWithCommitStyle {
		t.Fatal("configWithCommitStyle carries no style line to delete")
	}
	return without
}

// TestLintCannotLoadAConfigStillCarryingCommitStyle: a glyph.toml that still
// has the removed [commit] style line does not load, so lint judges nothing
// and exits 2 naming the key — never 3, which the installed hooks block on,
// because the commit that deletes the line has to get through. The same file
// without the line lints the same subject at 0, which is also what shows the
// 2 is the key's and not the fixture's.
func TestLintCannotLoadAConfigStillCarryingCommitStyle(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want core.Code
	}{
		{"with the line", configWithCommitStyle, core.CodeUsage},
		{"without the line", withoutCommitStyle(t), core.CodeOK},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, _ := testutil.NewTaggedRepo(t)
			seedConfig(t, dir, c.body)
			t.Chdir(dir)

			setStdin(t, ":bug:(cli)~ fix a thing\n")
			code, _, stderr := runGlyph(t, "lint", "--stdin")
			if code != int(c.want) {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, c.want, stderr)
			}
			if c.want != core.CodeUsage {
				return
			}
			if env := decodeErrorEnvelope(t, stderr); !strings.Contains(env.Message, "commit.style") {
				t.Errorf("envelope message = %q, want it to name commit.style, the line to delete", env.Message)
			}
		})
	}
}
