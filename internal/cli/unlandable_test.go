package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// unlandableConfig claims git's amend! subject as unlandable — the shape the
// key was made for (t-t84a). It is written out rather than derived from a
// preset so these tests assert the key's mechanism whatever the presets ship.
const unlandableConfig = `schema = 1
exclude_authors = ['dependabot[bot]']

[[patterns]]
pattern = '^(?P<subject>:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) .+)'

[[patterns]]
pattern = '^Merge '
skip = true

[[patterns]]
pattern = '^(fixup|squash)! '
skip = true

[[patterns]]
pattern = '^amend! '
unlandable = 'autosquash replaces the target message with this body; rebase with --autosquash before it lands'

[note]
line = '- $subject'

[[note.sections]]
semver = 'major'
title = 'Breaking Changes'

[[note.sections]]
semver = 'patch'
title = 'Fixes'
`

const unlandableReason = "autosquash replaces the target message with this body; rebase with --autosquash before it lands"

// unlandableRepo is testRepo carrying unlandableConfig, committed, with v1.0.0
// tagged on that commit so a major reads as v2.0.0 rather than through the
// 0.x clamp.
func unlandableRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir, _ = testRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "glyph.toml"), []byte(unlandableConfig), 0o600); err != nil {
		t.Fatalf("write glyph.toml: %v", err)
	}
	testGit(t, dir, "akira-toriyama", "add", "glyph.toml")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":wrench:= claim amend! as unlandable")
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")
	return dir, "v1.0.0"
}

// TestLintAuthoringPassesUnlandableWithAWarning: the commit-msg hook's modes
// let an unlandable message through at 0, and say so — the reason, and that
// the later gates refuse it. Silence here would be the hook blessing a
// message CI rejects without a word (DESIGN §2.1).
func TestLintAuthoringPassesUnlandableWithAWarning(t *testing.T) {
	dir, _ := unlandableRepo(t)
	t.Chdir(dir)
	msg := "amend! :bug:~ fix b\n\n:boom:! fix b\n"

	for name, run := range map[string]func() (int, string, string){
		"message": func() (int, string, string) { return runGlyph(t, "lint", "--message", msg) },
		"stdin": func() (int, string, string) {
			setStdin(t, msg)
			return runGlyph(t, "lint", "--stdin")
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := run()
			if code != int(core.CodeOK) {
				t.Fatalf("lint --%s exited %d, want 0: an author cannot make git spell amend! differently\nstderr: %s", name, code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			for _, want := range []string{"::warning::", unlandableReason, "refuses it"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr is missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}

// TestLintRangeRefusesUnlandable: the gate CI runs (and the pre-push hook,
// through the same lintRaws) refuses the commit with the pattern's reason —
// not the no-match sentence, whose remedy is wrong for a message a pattern
// did claim.
func TestLintRangeRefusesUnlandable(t *testing.T) {
	dir, base := unlandableRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix b")
	testCommit(t, dir, "akira-toriyama", "amend! :bug:~ fix b\n\n:boom:! fix b")
	testCommit(t, dir, "dependabot[bot]", "amend! Bump x")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != int(core.CodeLint) {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "\n{")+1:])
	var details []rangeViolation
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("details: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("details = %+v, want exactly the human amend! (the bot is excluded before any message rule)", details)
	}
	if !strings.Contains(details[0].Detail, "unlandable: "+unlandableReason) || strings.Contains(details[0].Detail, "matches none") {
		t.Errorf("detail = %q, want the unlandable reason and not the no-match sentence", details[0].Detail)
	}
}

// TestLintPRRefusesUnlandable: a pull request's title is the subject a squash
// merge lands, so --pr takes the history verdict, never the authoring one — a
// title git's amend! prefix still leads would otherwise lint green at the
// merge gate.
func TestLintPRRefusesUnlandable(t *testing.T) {
	dir, _ := unlandableRepo(t)
	t.Chdir(dir)
	usePR(t, walkServer(t, map[string]string{pullPath(7): apiOnePullBody("amend! :bug:~ fix b", "akira-toriyama")}))

	code, stdout, stderr := runGlyph(t, "lint", "--pr", "7")
	if code != int(core.CodeLint) || stdout != "" {
		t.Fatalf("lint --pr = %d stdout %q, want 3\nstderr: %s", code, stdout, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "\n{")+1:])
	var details []rangeViolation
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("details: %v", err)
	}
	if len(details) != 1 || !strings.Contains(details[0].Detail, "unlandable: "+unlandableReason) {
		t.Errorf("details = %+v, want the unlandable reason", details)
	}
}

// TestUnlandableAmendEndToEnd asks git for everything: the installed hook
// fires on the subjects `--fixup=amend:` and `--fixup=reword:` write, the
// range gates judge the history git recorded, and `rebase --autosquash` does
// the rewrite the refusal asks for. The claim under test is t-t84a's: no gate
// may fold the pre-autosquash history, because autosquash REPLACES the
// target's message with the amend! body — a skip read ':bug:~' plus a
// reword to ':boom:!' as patch, where the history that lands reads major.
func TestUnlandableAmendEndToEnd(t *testing.T) {
	glyphBin := buildGlyph(t)
	dir, base := unlandableRepo(t)
	t.Chdir(dir)
	if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
		t.Fatalf("hook install exit = %d\nstderr: %s", code, stderr)
	}
	pathWithGlyph := filepath.Dir(glyphBin) + string(os.PathListSeparator) + os.Getenv("PATH")

	appendFile(t, dir, "b.txt")
	testGit(t, dir, "akira-toriyama", "add", "-A")
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix b")
	target := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	t.Run("the hook lets --fixup=amend: through, loudly", func(t *testing.T) {
		appendFile(t, dir, "b2.txt")
		testGit(t, dir, "akira-toriyama", "add", "-A")
		out, err := commitFlagsWith(dir, pathWithGlyph, "--fixup=amend:"+target)
		if err != nil {
			t.Fatalf("git commit --fixup=amend: was blocked by the hook: %v\n%s", err, out)
		}
		if !strings.Contains(out, unlandableReason) {
			t.Errorf("the hook passed the amend! silently; it must say the later gates refuse it:\n%s", out)
		}
	})

	// The editor writes the body a reword is for: the target's sigil,
	// corrected. git refuses -m and -F with --fixup=reword:, so an editor is
	// the only way to hand it one; the subject line stays git's.
	editor := filepath.Join(t.TempDir(), "reword-editor")
	script := "#!/bin/sh\n{ head -n1 \"$1\"; printf '\\n:boom:! fix b\\n'; } > \"$1.new\" && mv \"$1.new\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil { // #nosec G306 -- an editor must be executable
		t.Fatalf("write editor: %v", err)
	}
	t.Run("the hook lets --fixup=reword: through, loudly", func(t *testing.T) {
		cmd := exec.Command("git", "-C", dir, "commit", "-q", "--fixup=reword:"+target)
		cmd.Env = testutil.GitEnv("akira-toriyama", "PATH="+pathWithGlyph, "GIT_EDITOR="+editor)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git commit --fixup=reword: was blocked by the hook: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), unlandableReason) {
			t.Errorf("the hook passed the amend! silently:\n%s", out)
		}
		if got := testGit(t, dir, "akira-toriyama", "log", "-1", "--format=%B"); got != "amend! :bug:~ fix b\n\n:boom:! fix b" {
			t.Fatalf("git recorded %q, want git's amend! subject over the reworded body", got)
		}
	})

	t.Run("every range gate refuses the unsquashed history", func(t *testing.T) {
		if code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD"); code != int(core.CodeLint) || !strings.Contains(stderr, unlandableReason) {
			t.Errorf("lint --range = %d, want 3 carrying the reason\nstderr: %s", code, stderr)
		}
		code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
		if code != int(core.CodeLint) || stdout != "" {
			t.Errorf("bump --range = %d stdout %q, want 3 and no version — the only foldable reading of this history is the pre-autosquash one\nstderr: %s", code, stdout, stderr)
		}
		if !strings.Contains(stderr, unlandableReason) {
			t.Errorf("bump's refusal does not carry the reason:\n%s", stderr)
		}
	})

	t.Run("after autosquash the reworded sigil is the verdict", func(t *testing.T) {
		// -i with a no-op sequence editor: autosquash without -i needs a
		// newer git than every runner is guaranteed to carry.
		cmd := exec.Command("git", "-C", dir, "rebase", "-q", "-i", "--autosquash", base)
		cmd.Env = testutil.GitEnv("akira-toriyama", "PATH="+pathWithGlyph, "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git rebase --autosquash: %v\n%s", err, out)
		}
		if got := testGit(t, dir, "akira-toriyama", "log", "--format=%s", base+"..HEAD"); got != ":boom:! fix b" {
			t.Fatalf("autosquash left %q, want the one commit carrying the reworded message", got)
		}
		code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
		if code != int(core.CodeOK) || stdout != "v2.0.0\n" {
			t.Errorf("bump --range = %d %q, want 0 and v2.0.0\nstderr: %s", code, stdout, stderr)
		}
	})
}

// TestShippedPresetRefusesAnUnsquashedAmend is t-t84a's reproduction under
// the preset `glyph init --gemoji` writes, with git spelling every message:
// a reword through `--fixup=reword:` must not leave any gate folding the
// history it has not rewritten yet. With amend! skipped, this history folded
// to v1.0.1 with lint green; the history autosquash writes folds to v2.0.0.
func TestShippedPresetRefusesAnUnsquashedAmend(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "v1.0.0")
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix b")
	target := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	editor := filepath.Join(t.TempDir(), "reword-editor")
	script := "#!/bin/sh\n{ head -n1 \"$1\"; printf '\\n:boom:! fix b\\n'; } > \"$1.new\" && mv \"$1.new\" \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o700); err != nil { // #nosec G306 -- an editor must be executable
		t.Fatalf("write editor: %v", err)
	}
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "--fixup=reword:"+target)
	cmd.Env = testutil.GitEnv("akira-toriyama", "GIT_EDITOR="+editor)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit --fixup=reword: %v\n%s", err, out)
	}
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "lint", "--range", "v1.0.0..HEAD"); code != int(core.CodeLint) || !strings.Contains(stderr, "amend!") {
		t.Errorf("lint --range = %d, want 3 naming the amend! commit\nstderr: %s", code, stderr)
	}
	if code, stdout, stderr := runGlyph(t, "bump", "--range", "v1.0.0..HEAD"); code != int(core.CodeLint) || stdout != "" {
		t.Errorf("bump --range = %d %q, want 3 and no version — v1.0.1 is the skip's answer\nstderr: %s", code, stdout, stderr)
	}

	rebase := exec.Command("git", "-C", dir, "rebase", "-q", "-i", "--autosquash", "v1.0.0")
	rebase.Env = testutil.GitEnv("akira-toriyama", "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
	if out, err := rebase.CombinedOutput(); err != nil {
		t.Fatalf("git rebase --autosquash: %v\n%s", err, out)
	}
	if code, stdout, stderr := runGlyph(t, "bump", "--range", "v1.0.0..HEAD"); code != 0 || stdout != "v2.0.0\n" {
		t.Errorf("after autosquash bump --range = %d %q, want v2.0.0\nstderr: %s", code, stdout, stderr)
	}
}

// autosquashClaim is the sentence the shipped gemoji preset claims git's
// fixup!/squash! subjects with — the text the hook's warning and every range
// refusal carry. Read from the preset rather than retyped. An empty one fails
// the test here, because a preset that skips the pair would make every
// Contains against it hold vacuously; the test goes on, so the gates' own
// answers on that history are what the failure shows.
func autosquashClaim(t *testing.T) string {
	t.Helper()
	m, err := testCfg(t).Match("fixup! :sparkles:^ add a season")
	if err != nil || m.Matched || m.Unlandable == "" {
		t.Errorf("the gemoji preset reads a fixup! as %+v (err %v), want an unlandable claim", m, err)
	}
	return m.Unlandable
}

// TestPackagesFixupIsUnlandableEndToEnd is t-mfny's reproduction under the
// preset `glyph init --gemoji` writes with [[packages]] appended — the way
// every packages config the house has written adopts packages — asking git
// for every step: the installed hook on the subjects --fixup and --squash
// write, the range gates on the history git recorded, and the verdict after a
// real rebase --autosquash. The target is a ^ under haiku/; the autosquash
// artifacts touch curry/. With the pair skipped, lint was green and bump left
// curry at none, while the autosquashed history moves both lines (measured on
// git 2.54 at adfc5e1) — and a squash-merge fleet's walk folds exactly the
// pre-squash commits, so curry's change shipped unversioned.
func TestPackagesFixupIsUnlandableEndToEnd(t *testing.T) {
	glyphBin := buildGlyph(t)
	dir, base := packagesRepo(t)
	target := touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	t.Chdir(dir)
	if code, _, stderr := runGlyph(t, "hook", "install"); code != int(core.CodeOK) {
		t.Fatalf("hook install exit = %d\nstderr: %s", code, stderr)
	}
	pathWithGlyph := filepath.Dir(glyphBin) + string(os.PathListSeparator) + os.Getenv("PATH")
	claim := autosquashClaim(t)

	for _, tc := range []struct{ flag, file string }{
		{"--fixup=" + target, "curry/curry.go"},
		{"--squash=" + target, "curry/menu.go"},
	} {
		t.Run("the hook lets "+strings.SplitN(tc.flag, "=", 2)[0]+" through, loudly", func(t *testing.T) {
			writeFile(t, dir, tc.file, tc.flag+"\n")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			out, err := commitFlagsWith(dir, pathWithGlyph, tc.flag)
			if err != nil {
				t.Fatalf("git commit %s was blocked by the hook: %v\n%s", tc.flag, err, out)
			}
			if !strings.Contains(out, claim) {
				t.Errorf("the hook passed git's subject without the reason; it must say why the commit cannot land:\n%s", out)
			}
		})
	}

	t.Run("every range gate refuses the unsquashed history", func(t *testing.T) {
		code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
		if code != int(core.CodeLint) {
			t.Fatalf("lint --range = %d, want 3 — green here is curry's change left at none\nstderr: %s", code, stderr)
		}
		env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "\n{")+1:])
		var details []rangeViolation
		if err := json.Unmarshal(env.Details, &details); err != nil {
			t.Fatalf("details: %v", err)
		}
		if len(details) != 2 {
			t.Fatalf("details = %+v, want the fixup! and the squash!", details)
		}
		for _, d := range details {
			if d.Detail != claim {
				t.Errorf("%s: detail = %q, want the claim %q", d.Subject, d.Detail, claim)
			}
		}
		code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
		if code != int(core.CodeLint) || stdout != "" {
			t.Errorf("bump --range = %d stdout %q, want 3 and no version — the only foldable reading of this history leaves curry at none\nstderr: %s", code, stdout, stderr)
		}
		if !strings.Contains(stderr, claim) {
			t.Errorf("bump's refusal does not carry the reason:\n%s", stderr)
		}
	})

	t.Run("after autosquash both lines move", func(t *testing.T) {
		// -i with a no-op sequence editor: autosquash without -i needs a
		// newer git than every runner is guaranteed to carry.
		cmd := exec.Command("git", "-C", dir, "rebase", "-q", "-i", "--autosquash", base)
		cmd.Env = testutil.GitEnv("akira-toriyama", "PATH="+pathWithGlyph, "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git rebase --autosquash: %v\n%s", err, out)
		}
		if got := testGit(t, dir, "akira-toriyama", "log", "--format=%s", base+"..HEAD"); got != ":sparkles:^ add a season" {
			t.Fatalf("autosquash left %q, want the target alone", got)
		}
		if code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD"); code != int(core.CodeOK) {
			t.Errorf("lint --range = %d, want 0 on the autosquashed history\nstderr: %s", code, stderr)
		}
		code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
		if code != int(core.CodeOK) {
			t.Fatalf("bump --range = %d, want 0\nstderr: %s", code, stderr)
		}
		res := decodePackagesVerdict(t, stdout)
		if len(res.Packages) != 2 {
			t.Fatalf("packages = %+v, want haiku and curry", res.Packages)
		}
		for _, p := range res.Packages {
			if p.Level != "minor" || p.Next != "v0.2.0" {
				t.Errorf("%s = %+v, want minor to v0.2.0: the target's ^ now covers the files the autosquash artifacts changed", p.Path, p)
			}
		}
	})
}

// TestShippedPresetRefusesAFixupAutosquashCannotFold: on one line, a fixup!
// whose target is outside the commits autosquash rebases is left by
// autosquash as it is, so a skip was never safe for it: measured on git 2.54
// at adfc5e1 with the pair skipped, lint --range was green and bump answered
// "no release: 0 commit(s)", the change shipped as a silent none. Two shapes:
// the target released, rebased from its tag; and the target on the parent
// topic a stacked branch was cut from, rebased from that parent — neither
// merged nor released, which is why the reason conditions its reword on what
// autosquash did rather than on where the target lives. The claim refuses
// both after autosquash, and the reword its reason names is the way out.
func TestShippedPresetRefusesAFixupAutosquashCannotFold(t *testing.T) {
	for _, tc := range []struct {
		name string
		// cut commits the target and returns it with the ref the author
		// rebases from, which the target already sits behind.
		cut      func(t *testing.T, dir string) (target, base string)
		wantBump string
	}{
		{"released", func(t *testing.T, dir string) (string, string) {
			target := touch(t, dir, "akira-toriyama", ":sparkles:^ add x", "x.txt")
			testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
			return target, "v0.2.0"
		}, "v0.2.1\n"},
		{"stacked", func(t *testing.T, dir string) (string, string) {
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "p1")
			target := touch(t, dir, "akira-toriyama", ":sparkles:^ add x", "x.txt")
			testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "p2")
			return target, "p1"
		}, "v0.1.1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := testRepo(t)
			target, base := tc.cut(t, dir)
			writeFile(t, dir, "x.txt", "finished\n")
			testGit(t, dir, "akira-toriyama", "add", "-A")
			testGit(t, dir, "akira-toriyama", "commit", "-q", "--fixup="+target)
			t.Chdir(dir)
			claim := autosquashClaim(t)
			rng := base + "..HEAD"

			rebase := exec.Command("git", "-C", dir, "rebase", "-q", "-i", "--autosquash", base)
			rebase.Env = testutil.GitEnv("akira-toriyama", "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
			if out, err := rebase.CombinedOutput(); err != nil {
				t.Fatalf("git rebase --autosquash: %v\n%s", err, out)
			}
			if got := testGit(t, dir, "akira-toriyama", "log", "--format=%s", rng); got != "fixup! :sparkles:^ add x" {
				t.Fatalf("autosquash left %q, want the fixup! untouched: its target is behind the rebase base", got)
			}

			if code, _, stderr := runGlyph(t, "lint", "--range", rng); code != int(core.CodeLint) || !strings.Contains(stderr, claim) {
				t.Errorf("lint --range = %d, want 3 carrying the claim — 0 here ships the fix as a silent none\nstderr: %s", code, stderr)
			}
			if code, stdout, stderr := runGlyph(t, "bump", "--range", rng); code != int(core.CodeLint) || stdout != "" {
				t.Errorf("bump --range = %d %q, want 3 and no version — \"no release\" is the skip's answer\nstderr: %s", code, stdout, stderr)
			}

			testGit(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":bug:~ finish x")
			if code, _, stderr := runGlyph(t, "lint", "--range", rng); code != int(core.CodeOK) {
				t.Errorf("after the reword lint --range = %d, want 0\nstderr: %s", code, stderr)
			}
			if code, stdout, stderr := runGlyph(t, "bump", "--range", rng); code != int(core.CodeOK) || stdout != tc.wantBump {
				t.Errorf("after the reword bump --range = %d %q, want %q\nstderr: %s", code, stdout, tc.wantBump, stderr)
			}
		})
	}
}
