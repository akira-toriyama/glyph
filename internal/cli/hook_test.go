package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/hook"
)

func TestHookInstallWritesIntoTheGitHooksDir(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "hook", "install")
	if code != int(core.CodeOK) {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "installed") {
		t.Errorf("stdout does not report the install: %q", stdout)
	}

	got, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "commit-msg"))
	if err != nil {
		t.Fatalf("hook was not written to .git/hooks: %v", err)
	}
	if string(got) != hook.Script {
		t.Error("installed hook does not match hook.Script")
	}
}

// core.hooksPath is why the destination is asked of git rather than assumed:
// the family's older repos relocate hooks to a tracked scripts/hooks, and a
// hook written to .git/hooks there is one git never runs.
func TestHookInstallHonoursCoreHooksPath(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "config", "core.hooksPath", "scripts/hooks")
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}

	if _, err := os.Stat(filepath.Join(dir, "scripts", "hooks", "commit-msg")); err != nil {
		t.Errorf("hook was not written to the configured core.hooksPath: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "commit-msg")); err == nil {
		t.Error("hook was written to .git/hooks despite core.hooksPath — git would never run it")
	}
}

func TestHookInstallRefusesAForeignHookWithUsageCode(t *testing.T) {
	dir, _ := testRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("seeding a foreign hook: %v", err)
	}
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "hook", "install")
	if code != int(core.CodeUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, core.CodeUsage)
	}
	env := decodeErrorEnvelope(t, stderr)
	if env.Code != int(core.CodeUsage) {
		t.Errorf("envelope code = %d, want %d", env.Code, core.CodeUsage)
	}

	if code, _, stderr := runGlyph(t, "hook", "install", "--force"); code != int(core.CodeOK) {
		t.Fatalf("--force exit = %d, want 0\nstderr: %s", code, stderr)
	}
}

// --print is the inspect-before-you-install path, and must not touch the disk.
func TestHookInstallPrintDoesNotWrite(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "hook", "install", "--print")
	if code != int(core.CodeOK) {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != hook.Script {
		t.Error("--print did not emit the script verbatim on stdout")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "commit-msg")); err == nil {
		t.Error("--print installed the hook; it must only print")
	}
}

// The end-to-end proof: git itself runs the installed hook, a violating message
// is rejected, and a conforming one commits. Everything above this test asserts
// file contents — only this one shows the hook actually gates a commit.
func TestInstalledHookGatesRealCommits(t *testing.T) {
	glyphBin := buildGlyph(t)
	dir, _ := testRepo(t)
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
		t.Fatalf("install exit = %d\nstderr: %s", code, stderr)
	}

	// The hook resolves `glyph` off PATH; point PATH at the binary just built
	// so this exercises the real lookup rather than a stubbed one.
	pathWithGlyph := filepath.Dir(glyphBin) + string(os.PathListSeparator) + os.Getenv("PATH")

	t.Run("rejects a violating message", func(t *testing.T) {
		_, err := commitWith(dir, pathWithGlyph, "no gitmoji here at all")
		if err == nil {
			t.Fatal("commit succeeded despite a message that violates the convention")
		}
	})

	t.Run("accepts a conforming message", func(t *testing.T) {
		out, err := commitWith(dir, pathWithGlyph, ":sparkles:(hook) add the thing")
		if err != nil {
			t.Fatalf("conforming commit was rejected: %v\n%s", err, out)
		}
	})

	// A developer without glyph installed must still be able to commit; CI is
	// the authority, the hook is early warning (t-7m35's ratified policy).
	t.Run("passes when glyph is absent from PATH", func(t *testing.T) {
		out, err := commitWith(dir, "/nonexistent", ":wrench: adjust something")
		if err != nil {
			t.Fatalf("commit blocked when glyph is off PATH: %v\n%s", err, out)
		}
		if !strings.Contains(out, "not on PATH") {
			t.Errorf("no warning surfaced when glyph was absent:\n%s", out)
		}
	})

	// Subjects git writes ITSELF must pass. An author cannot rewrite them, so
	// rejecting one leaves --no-verify as the only way forward — which turns the
	// gate off entirely. The retired shell hook exempted these four prefixes;
	// the first cut of this hook did not, and blocked `git commit --fixup` and
	// `git merge` fleet-wide.
	t.Run("does not block git-generated subjects", func(t *testing.T) {
		head := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

		t.Run("fixup", func(t *testing.T) {
			appendFile(t, dir, "fixup.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			if out, err := commitFlagsWith(dir, pathWithGlyph, "--fixup="+head); err != nil {
				t.Fatalf("git commit --fixup was blocked by the hook: %v\n%s", err, out)
			}
		})

		t.Run("squash", func(t *testing.T) {
			appendFile(t, dir, "squash.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			if out, err := commitFlagsWith(dir, pathWithGlyph, "--squash="+head); err != nil {
				t.Fatalf("git commit --squash was blocked by the hook: %v\n%s", err, out)
			}
		})

		// A blocked merge is worse than a blocked commit: git has already
		// written MERGE_HEAD, and the follow-up `git commit` runs the same hook
		// on MERGE_MSG, so the repository cannot be brought out of the merge.
		t.Run("merge", func(t *testing.T) {
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic")
			appendFile(t, dir, "topic.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			testCommit(t, dir, "akira-toriyama", ":sparkles: land the topic")
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")

			out, err := mergeWith(dir, pathWithGlyph, "topic")
			if err != nil {
				t.Fatalf("git merge --no-ff was blocked by the hook: %v\n%s", err, out)
			}
			if _, sErr := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); sErr == nil {
				t.Error("repository left mid-merge: MERGE_HEAD still present after the merge commit")
			}
		})
	})

	// git runs commit-msg BEFORE its own cleanup, so the file still holds the
	// editor template. A message written under the template must lint on its
	// real subject, not on the first comment line, and a leading blank line
	// must not read as "empty commit message".
	t.Run("lints the message, not git's template", func(t *testing.T) {
		t.Run("subject below a leading blank and a template", func(t *testing.T) {
			msg := "\n:sparkles:(cleanup) written under the template\n" +
				"\n" +
				"# Please enter the commit message for your changes. Lines starting\n" +
				"# with '#' will be ignored, and an empty message aborts the commit.\n"
			appendFile(t, dir, "template.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			if out, err := commitFileWith(t, dir, pathWithGlyph, msg); err != nil {
				t.Fatalf("a valid message under git's template was rejected: %v\n%s", err, out)
			}
		})

		t.Run("a violation under the template is still caught", func(t *testing.T) {
			msg := "\nno gitmoji at all\n\n# Please enter the commit message for your changes.\n"
			appendFile(t, dir, "bad.txt")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			if _, err := commitFileWith(t, dir, pathWithGlyph, msg); err == nil {
				t.Fatal("cleanup swallowed a real violation — the template path must still lint")
			}
			testGit(t, dir, "akira-toriyama", "reset", "-q")
		})
	})
}

func appendFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// commitFlagsWith runs `git commit <flag>` (no -m) so git composes the subject.
func commitFlagsWith(dir, path string, flag string) (string, error) {
	return runGit(dir, path, "commit", "-q", flag, "--no-edit")
}

// mergeWith runs a no-fast-forward merge, whose commit message git writes.
func mergeWith(dir, path, branch string) (string, error) {
	return runGit(dir, path, "merge", "--no-ff", "--no-edit", branch)
}

// commitFileWith commits with a message FILE, reproducing what an editor hands
// git — template comments and all.
func commitFileWith(t *testing.T, dir, path, message string) (string, error) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(f, []byte(message), 0o600); err != nil {
		t.Fatalf("writing the message file: %v", err)
	}
	return runGit(dir, path, "commit", "-q", "-F", f)
}

func runGit(dir, path string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=akira-toriyama",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=committer",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
		"PATH="+path,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildGlyph compiles the real binary once so the hook can invoke it as a
// developer's shell would.
func buildGlyph(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain is not available")
	}
	bin := filepath.Join(t.TempDir(), "glyph")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/akira-toriyama/glyph/cmd/glyph")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building glyph: %v\n%s", err, out)
	}
	return bin
}

// commitWith runs `git commit` in dir with the given PATH, returning git's
// combined output so the caller can assert on the hook's diagnostics.
func commitWith(dir, path, message string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", message)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=akira-toriyama",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=committer",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
		"PATH="+path,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
