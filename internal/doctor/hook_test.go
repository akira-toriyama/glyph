package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/hook"
)

// hooksDirWith writes body as the commit-msg hook in a throwaway hooks
// directory and returns the directory. An empty body installs nothing, which is
// the state of every fresh clone.
func hooksDirWith(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body == "" {
		return dir
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return dir
}

// TestCommitMsgHookCurrentPasses is the case the check exists to distinguish
// everything else FROM, so it is asserted against hook.Script itself rather
// than a copy: a literal here would be a second source of truth for the hook's
// text and would go stale in exactly the way the check reports.
func TestCommitMsgHookCurrentPasses(t *testing.T) {
	c := checkHook(hook.Kinds[0], IDCommitMsgHook, hooksDirWith(t, "commit-msg", hook.Script), nil)
	if c.Status != StatusPass {
		t.Errorf("an up-to-date hook is %s, want pass: %s", c.Status, c.Observed)
	}
	if c.ID != IDCommitMsgHook {
		t.Errorf("check id is %q — the ids are the report's machine surface", c.ID)
	}
}

// TestCommitMsgHookDriftFails is the finding. The stale hook here is the exact
// shape internal/hook's doc comment names as the failure the code interpolation
// prevents at WRITE time and cannot prevent afterwards: a script that compares
// glyph's status against a number this binary no longer emits, so every real
// violation exits 0 and the commit lands.
func TestCommitMsgHookDriftFails(t *testing.T) {
	stale := strings.Replace(hook.Script, `-eq 3 `, `-eq 9 `, 1)
	if stale == hook.Script {
		t.Fatalf("the stale-hook fixture no longer differs from hook.Script — re-derive it from the current " +
			"script, or this test asserts drift is detected using a hook that has not drifted")
	}
	if !strings.Contains(stale, hook.Marker) {
		t.Fatal("the fixture lost the marker, so it exercises the foreign-hook branch instead of drift")
	}

	c := checkHook(hook.Kinds[0], IDCommitMsgHook, hooksDirWith(t, "commit-msg", stale), nil)
	if c.Status != StatusFail {
		t.Errorf("a glyph-written hook that no longer matches this binary is %s, want fail: %s", c.Status, c.Observed)
	}
	if !strings.Contains(c.Observed, "first difference at line") {
		t.Errorf("the finding must name where the two part company so a developer can diff from there, got %q", c.Observed)
	}
	if !strings.Contains(c.Fix, "glyph hook install") {
		t.Errorf("the fix must be the refresh command, got %q", c.Fix)
	}
}

// TestCommitMsgHookAbsentPasses pins the argued severity. Absence is not drift:
// hooks are untracked, so an Actions checkout cannot have one, and grading this
// as advice would post a notice on every doctor run in every repository — the
// noise that teaches a fleet to stop reading the report. The check must stay
// silent AND still name the command, which is why the message is asserted too.
func TestCommitMsgHookAbsentPasses(t *testing.T) {
	c := checkHook(hook.Kinds[0], IDCommitMsgHook, hooksDirWith(t, "commit-msg", ""), nil)
	if c.Status != StatusPass {
		t.Errorf("no installed hook is %s, want pass — absence is the default state of every clone: %s", c.Status, c.Observed)
	}
	if !strings.Contains(c.Message, "glyph hook install") {
		t.Errorf("a passing check still owes the reader the command that adds the gate, got %q", c.Message)
	}
	if c.Fix != "" {
		t.Errorf("a pass must carry no fix line, got %q", c.Fix)
	}
}

// TestCommitMsgHookForeignIsAdvice: glyph refuses to overwrite a hook it did not
// write (hook.Install), so finding one is a standing choice rather than drift.
// Reporting it as a failure would fail a repository over a decision glyph itself
// declines to override.
func TestCommitMsgHookForeignIsAdvice(t *testing.T) {
	c := checkHook(hook.Kinds[0], IDCommitMsgHook, hooksDirWith(t, "commit-msg", "#!/bin/sh\ngrep -q '^feat' \"$1\" || exit 1\n"), nil)
	if c.Status != StatusAdvice {
		t.Errorf("somebody else's hook is %s, want advice: %s", c.Status, c.Observed)
	}
	if !strings.Contains(c.Fix, "--force") {
		t.Errorf("replacing a foreign hook needs --force, and the fix must say so: %q", c.Fix)
	}
}

// TestCommitMsgHookUnaskableIsUnknown: git names the hooks directory because
// core.hooksPath relocates it. Without that answer there is no file to compare,
// and the verdict must be could-not-run rather than the "no hook installed"
// pass — the two recommend opposite actions, and only one of them is a claim
// this check is entitled to make.
func TestCommitMsgHookUnaskableIsUnknown(t *testing.T) {
	c := checkHook(hook.Kinds[0], IDCommitMsgHook, "", errors.New("not a git repository"))
	if c.Status != StatusUnknown {
		t.Errorf("an unaskable hooks directory is %s, want unknown: %s", c.Status, c.Observed)
	}
}

// TestCommitMsgHookUnreadableIsUnknown: a hook that exists and cannot be read
// could hold anything, including a stale one. Not-exists is the only absence
// this check is allowed to read as clean.
func TestCommitMsgHookUnreadableIsUnknown(t *testing.T) {
	dir := hooksDirWith(t, "commit-msg", hook.Script)
	path := filepath.Join(dir, "commit-msg")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil { //nolint:gosec // the fixture this test just wrote
		t.Skip("this filesystem (or a root-equivalent uid) ignores mode 000, so unreadable cannot be staged")
	}

	c := checkHook(hook.Kinds[0], IDCommitMsgHook, dir, nil)
	if c.Status != StatusUnknown {
		t.Errorf("an unreadable hook is %s, want unknown: %s", c.Status, c.Observed)
	}
}

// TestDiffSummaryNamesAPrefixRatherThanALine covers the shape that parts
// nowhere: one script being a prefix of the other, where every shared line
// matches and a line-number report would have nothing to point at.
func TestDiffSummaryNamesAPrefixRatherThanALine(t *testing.T) {
	got := diffSummary("a\nb", "a\nb\nc")
	if strings.Contains(got, "first difference at line") {
		t.Errorf("a prefix has no differing line to name, got %q", got)
	}
	if !strings.Contains(got, "then one runs on") {
		t.Errorf("the summary must say the two agree as far as they overlap, got %q", got)
	}
}

// TestEveryHookKindIsCheckedTheSameWay: the check is generic over the kind, so
// the pre-push hook gets the same four verdicts the commit-msg one does — and
// in particular the argued one, absence passing.
//
// Absence is not drift. A hook is opt-in, CI is the authority, and hooks do not
// clone, so a checkout on an Actions runner has none by construction. Grading
// that as advice would post a notice on every doctor run in every repository,
// for a condition no repository can avoid — the noise that teaches a fleet to
// skip the report (§7). The drift arm is asserted beside it because a check
// that only ever passes is not a check.
func TestEveryHookKindIsCheckedTheSameWay(t *testing.T) {
	ids := map[string]string{"commit-msg": IDCommitMsgHook, "pre-push": IDPrePushHook}
	for _, k := range hook.Kinds {
		t.Run(k.Name, func(t *testing.T) {
			absent := t.TempDir()
			if c := checkHook(k, ids[k.Name], absent, nil); c.Status != StatusPass {
				t.Errorf("no %s hook installed reported %q, want pass — absence is opt-out, not drift", k.Name, c.Status)
			}

			stale := hooksDirWith(t, k.Name, "#!/bin/sh\n# "+hook.Marker+"\nexit 0\n")
			c := checkHook(k, ids[k.Name], stale, nil)
			if c.Status != StatusFail {
				t.Errorf("a glyph-written %s hook that no longer matches reported %q, want fail — "+
					"it fails in the quiet direction, so nothing else notices", k.Name, c.Status)
			}
			if c.ID != ids[k.Name] {
				t.Errorf("check id = %q, want %q — each kind carries its own observed/expected pair", c.ID, ids[k.Name])
			}
		})
	}
}
