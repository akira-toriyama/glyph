package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// TestInitWritesThePresetVerbatim pins init's whole contract: the file on
// disk is the embedded preset byte for byte (no templating between the
// shipped artifact and the user's tree), and it loads under the loader.
func TestInitWritesThePresetVerbatim(t *testing.T) {
	for _, preset := range config.PresetNames() {
		t.Run(preset, func(t *testing.T) {
			t.Chdir(t.TempDir())
			code, stdout, stderr := runGlyph(t, "init", "--"+preset)
			if code != 0 {
				t.Fatalf("init --%s exited %d\nstderr: %s", preset, code, stderr)
			}
			if !strings.Contains(stdout, "glyph.toml") {
				t.Errorf("stdout should name the file written: %q", stdout)
			}
			got, err := os.ReadFile("glyph.toml")
			if err != nil {
				t.Fatalf("read glyph.toml: %v", err)
			}
			want, _ := config.Preset(preset)
			if !bytes.Equal(got, want) {
				t.Errorf("glyph.toml differs from the embedded %s preset", preset)
			}
			if _, err := config.Load(got); err != nil {
				t.Errorf("written glyph.toml does not load: %v", err)
			}
		})
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("glyph.toml", []byte("# mine\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, _, stderr := runGlyph(t, "init", "--gemoji")
	if code != 2 {
		t.Fatalf("init over an existing glyph.toml exited %d, want 2 (usage)\nstderr: %s", code, stderr)
	}
	got, _ := os.ReadFile("glyph.toml")
	if string(got) != "# mine\n" {
		t.Fatalf("refusal must leave the existing file untouched, got %q", got)
	}

	code, _, stderr = runGlyph(t, "init", "--gemoji", "--force")
	if code != 0 {
		t.Fatalf("init --force exited %d\nstderr: %s", code, stderr)
	}
	got, _ = os.ReadFile("glyph.toml")
	want, _ := config.Preset("gemoji")
	if !bytes.Equal(got, want) {
		t.Errorf("--force should have replaced the file with the preset")
	}
}

// TestInitRunsFromOptionsWithoutCobra: runInit is a function of its options
// — no command built, no flag parsed — so the preset and --force reach it as
// fields: without force a second run refuses at 2 and keeps the first file,
// with force it writes the second preset over it.
func TestInitRunsFromOptionsWithoutCobra(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, restore := captureOut()
	defer restore()
	ctx := context.Background()
	conventional, _ := config.Preset("conventional")
	gemoji, _ := config.Preset("gemoji")

	if err := runInit(ctx, initOptions{preset: "conventional"}); err != nil {
		t.Fatalf("runInit from options: %v", err)
	}
	if got, _ := os.ReadFile("glyph.toml"); !bytes.Equal(got, conventional) {
		t.Fatalf("runInit from options wrote %q, want the conventional preset", got)
	}
	if err := runInit(ctx, initOptions{preset: "gemoji"}); core.ExitCode(err) != int(core.CodeUsage) {
		t.Fatalf("a second runInit without force: exit %d (%v), want exactly %d", core.ExitCode(err), err, core.CodeUsage)
	}
	if got, _ := os.ReadFile("glyph.toml"); !bytes.Equal(got, conventional) {
		t.Fatalf("the refusal must keep the first file, got %q", got)
	}
	if err := runInit(ctx, initOptions{preset: "gemoji", force: true}); err != nil {
		t.Fatalf("runInit with force from options: %v", err)
	}
	if got, _ := os.ReadFile("glyph.toml"); !bytes.Equal(got, gemoji) {
		t.Fatalf("force from options must write the gemoji preset over the file, got %q", got)
	}
}

// TestInitRefusesWhatIsNotARegularFile: init replaces a regular file and
// nothing else. A directory, a named pipe or a link that resolves to no file
// at the path is refused at exit 2, with --force or without, naming what is
// there, and is never opened. At 43cecf2 init answered each "already exists —
// edit it in place, or pass --force", and --force then exited 4 over a
// directory, a looping link, one through a file or one to an over-long name,
// hung over a named pipe in os.WriteFile's open, and wrote through a link to
// nothing, creating its target (measured 2026-10-21); at 99f713a the
// over-long name was still a refused stat at 4, --force or not. Each run sits
// under a deadline, so that regression fails here instead of hanging the
// package.
func TestInitRefusesWhatIsNotARegularFile(t *testing.T) {
	for _, k := range notAFile {
		for _, force := range []bool{false, true} {
			name, args := k.shape, []string{"init", "--gemoji"}
			if force {
				name, args = name+"/force", append(args, "--force")
			}
			t.Run(name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				if err := k.stage("glyph.toml"); err != nil {
					t.Fatalf("staging %s as glyph.toml: %v", k.shape, err)
				}
				before, err := os.Lstat("glyph.toml")
				if err != nil {
					t.Fatal(err)
				}
				code, stdout, stderr := runGlyphWithin(t, 30*time.Second, args...)
				if code != int(core.CodeUsage) {
					t.Fatalf("glyph %s over %s exited %d, want exactly %d\nstdout: %s\nstderr: %s",
						strings.Join(args, " "), k.shape, code, core.CodeUsage, stdout, stderr)
				}
				env := decodeErrorEnvelope(t, stderr)
				if !strings.Contains(env.Message, "glyph.toml is "+k.kind) || !strings.Contains(env.Message, "remove it, then re-run glyph init") {
					t.Errorf("the refusal must name what is at the path and send the user to remove it, got %q", env.Message)
				}
				if after, err := os.Lstat("glyph.toml"); err != nil || after.Mode().Type() != before.Mode().Type() {
					t.Errorf("init replaced what was at the path: %v before, %v after (%v)", before.Mode().Type(), after, err)
				}
				if _, err := os.Lstat("nowhere.toml"); !os.IsNotExist(err) {
					t.Errorf("init wrote through the link to its target")
				}
			})
		}
	}
}

func TestInitFlagGrammar(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := runGlyph(t, "init")
	if code != 2 {
		t.Fatalf("bare init exited %d, want 2 (a preset is required)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "--conventional") || !strings.Contains(stderr, "--gemoji") {
		t.Errorf("the usage error should name every preset: %q", stderr)
	}
	code, _, _ = runGlyph(t, "init", "--gemoji", "--conventional")
	if code != 2 {
		t.Fatalf("two presets at once exited %d, want 2", code)
	}
	if _, err := os.Lstat("glyph.toml"); !os.IsNotExist(err) {
		t.Errorf("a usage error must not leave a glyph.toml behind")
	}
}

// TestInitWritesTheTopLevelFromASubdirectory: every other command reads the
// glyph.toml at the checkout's top level (loadConfig), so init writes it
// there from wherever in the checkout it runs, and the remedy a missing file
// prints, followed from the same subdirectory, produces the file the next
// command reads. Measured before (2026-09-29, 2026-10-04): from sub/ init
// wrote sub/glyph.toml at exit 0, nothing read it, and lint repeated the
// remedy — and with glyph.toml already at the top level, an init from a
// subdirectory wrote a second one there at exit 0.
func TestInitWritesTheTopLevelFromASubdirectory(t *testing.T) {
	testutil.RequireGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	top, err := filepath.EvalSymlinks(dir) // git names the real path
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(top, "haiku", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	code, _, stderr := runGlyph(t, "lint", "--message", ":bug:~ fix a crash")
	if code != int(core.CodeUsage) || !strings.Contains(stderr, "glyph init --gemoji") {
		t.Fatalf("lint in an uninitialized checkout exited %d, want exactly %d with the init remedy\nstderr: %s", code, core.CodeUsage, stderr)
	}
	code, stdout, stderr := runGlyph(t, "init", "--gemoji")
	if code != 0 {
		t.Fatalf("init --gemoji from a subdirectory exited %d\nstderr: %s", code, stderr)
	}
	if _, err := os.Lstat(filepath.Join(sub, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("init wrote glyph.toml into the subdirectory it ran from — a file no command reads")
	}
	got, err := os.ReadFile(filepath.Join(top, "glyph.toml"))
	if err != nil {
		t.Fatalf("no glyph.toml at the checkout's top level: %v", err)
	}
	if want, _ := config.Preset("gemoji"); !bytes.Equal(got, want) {
		t.Errorf("the top-level glyph.toml differs from the embedded gemoji preset")
	}
	if !strings.Contains(stdout, filepath.Join(top, "glyph.toml")) {
		t.Errorf("stdout = %q, want it to name the path written, which is not the directory init ran from", stdout)
	}
	if code, _, stderr := runGlyph(t, "lint", "--message", ":bug:~ fix a crash"); code != 0 {
		t.Fatalf("lint after following the remedy exited %d, want 0 — the file init writes is the one every command reads\nstderr: %s", code, stderr)
	}

	code, _, stderr = runGlyph(t, "init", "--gemoji")
	if code != int(core.CodeUsage) {
		t.Fatalf("a second init from the subdirectory exited %d, want exactly %d: the top level already holds glyph.toml\nstderr: %s", code, core.CodeUsage, stderr)
	}
	if _, err := os.Lstat(filepath.Join(sub, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("the refused init still left a glyph.toml in the subdirectory")
	}
}

// TestInitRefusesInsideTheGitDirectory: inside a git directory — a checkout's
// .git, or a bare repository — git names no top level, and init read that as
// "outside a checkout" and wrote glyph.toml where it stood: .git/glyph.toml at
// exit 0, a file no command reads, while lint at the top level still said
// "no glyph.toml" (measured on c89ea08). Only where git reports no repository
// at all is the current directory the answer; here init refuses at the code
// it refuses an existing file with, and names the work tree.
func TestInitRefusesInsideTheGitDirectory(t *testing.T) {
	testutil.RequireGit(t)
	checkout := t.TempDir()
	testutil.Git(t, checkout, "akira-toriyama", "init", "-q", "-b", "main")
	bare := filepath.Join(t.TempDir(), "bare.git")
	testutil.Git(t, checkout, "akira-toriyama", "init", "-q", "--bare", "-b", "main", bare)

	for name, dir := range map[string]string{
		"a checkout's .git":   filepath.Join(checkout, ".git"),
		"a .git subdirectory": filepath.Join(checkout, ".git", "refs"),
		"a bare repository":   bare,
	} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(dir)
			code, stdout, stderr := runGlyph(t, "init", "--gemoji")
			if code != int(core.CodeUsage) {
				t.Fatalf("init inside the git directory exited %d, want exactly %d\nstdout: %s\nstderr: %s", code, core.CodeUsage, stdout, stderr)
			}
			if !strings.Contains(stderr, "work tree") {
				t.Errorf("the refusal must send the user to the work tree:\n%s", stderr)
			}
			if _, err := os.Lstat(filepath.Join(dir, "glyph.toml")); !os.IsNotExist(err) {
				t.Errorf("the refused init still wrote %s", filepath.Join(dir, "glyph.toml"))
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(checkout, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("a refused init wrote the top level anyway")
	}
}

// TestInitCannotRunWhereGitWillNotReadTheRepository: a repository git refuses
// to read — dubious ownership (safe.directory, common in containers and CI) or
// a broken .git/config — fails both of init's reads with 128, and init read
// that as "no repository" and wrote sub/glyph.toml at exit 0, the inert file
// t-f2cb stopped (measured on ca118a8 for both). Only git's own "not a git
// repository" is the current directory's case; anything else is no
// trustworthy answer, exit 4, with git's message, and nothing written.
func TestInitCannotRunWhereGitWillNotReadTheRepository(t *testing.T) {
	testutil.RequireGit(t)
	for name, arrange := range map[string]func(t *testing.T, top string){
		"dubious ownership": func(t *testing.T, _ string) {
			t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
		},
		"a broken .git/config": func(t *testing.T, top string) {
			appendTo(t, top, ".git/config", "[core\n bad\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			top := t.TempDir()
			testutil.Git(t, top, "akira-toriyama", "init", "-q", "-b", "main")
			sub := filepath.Join(top, "sub")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			arrange(t, top)
			t.Chdir(sub)

			code, stdout, stderr := runGlyph(t, "init", "--gemoji")
			if code != int(core.CodeAPI) {
				t.Fatalf("init in a repository git will not read exited %d, want exactly %d\nstdout: %s\nstderr: %s", code, core.CodeAPI, stdout, stderr)
			}
			if !strings.Contains(stderr, "fatal:") {
				t.Errorf("the error must carry git's own message:\n%s", stderr)
			}
			for _, dir := range []string{sub, top} {
				if _, err := os.Lstat(filepath.Join(dir, "glyph.toml")); !os.IsNotExist(err) {
					t.Errorf("init could not run and still wrote %s", filepath.Join(dir, "glyph.toml"))
				}
			}
		})
	}
}

// TestInitOutsideARepositoryReadsGitsAnswerPastItsTrace: "not a git repository"
// is recognised on the first fatal: line, not at the start of stderr. Under
// GIT_TRACE=1 git writes trace lines before it, and matching the stream's start
// turned an ordinary outside-a-checkout init into exit 4 with nothing written
// (review u6b-3, measured on 20576c8; base wrote ./glyph.toml).
func TestInitOutsideARepositoryReadsGitsAnswerPastItsTrace(t *testing.T) {
	testutil.RequireGit(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("GIT_TRACE", "1")

	code, _, stderr := runGlyph(t, "init", "--gemoji")
	if code != 0 {
		t.Fatalf("init outside a repository under GIT_TRACE=1 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(cwd, "glyph.toml")); err != nil {
		t.Fatalf("outside a repository init writes ./glyph.toml: %v", err)
	}
}

// TestInitInterruptDuringTopLevelReadWritesNothing: init asks git for the top
// level before it writes, and a signal landing while that question is in
// flight is the user's abort — not git saying "no checkout here", the one
// failure that sends the write to the current directory. Read as the latter,
// the run writes ./glyph.toml at exit 0 after the Ctrl-C: from a
// subdirectory, the file nothing reads that init was fixed to stop writing
// (measured 2026-10-04 with the arm deleted: exit 0 and "wrote glyph.toml",
// where the arm exits 130 with nothing written). The fake git signals that
// the read is in flight, then blocks, and only then is the context cancelled
// — the shape of TestDoctorInterruptDuringTopLevelReadCarriesOut.
func TestInitInterruptDuringTopLevelReadWritesNothing(t *testing.T) {
	interruptInit(t, "the top-level read", "")
}

// TestInitInterruptDuringGitDirReadWritesNothing: the same abort one read
// later. When git names no top level, init asks whether it stands inside a git
// directory before it falls back to the current directory, and a signal during
// that question must not be read as "no repository" either. The fake answers
// the top-level read as git does outside a repository and blocks on the next.
func TestInitInterruptDuringGitDirReadWritesNothing(t *testing.T) {
	interruptInit(t, "the git-directory read",
		"case \"$*\" in *--show-toplevel*) echo 'fatal: not a git repository' >&2; exit 128;; esac\n")
}

// interruptInit runs init against a fake git that runs prelude, then signals
// that a read is in flight and blocks; the context is cancelled once it has.
func interruptInit(t *testing.T, read, prelude string) {
	t.Helper()
	cwd := t.TempDir()
	t.Chdir(cwd)

	ctx, reached := cancelOnceAsked(t, blockingGit(t, prelude))
	code, stdout, _ := runGlyphCtx(t, ctx, "init", "--gemoji")
	reached(read)
	if code != int(core.CodeInterrupted) {
		t.Errorf("init exited %d, want exactly %d — an interrupt in %s is the user's own abort, not \"outside a checkout\"", code, core.CodeInterrupted, read)
	}
	if _, err := os.Lstat(filepath.Join(cwd, "glyph.toml")); !os.IsNotExist(err) {
		t.Errorf("an interrupted init wrote glyph.toml into the directory it ran from (stdout %q)", stdout)
	}
}
