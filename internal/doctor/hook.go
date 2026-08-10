package doctor

// This file is doctor's second local check, and the only one whose subject is a
// file glyph itself WROTE: the commit-msg hook installed in this checkout must
// be byte-identical to the one this binary would write today.
//
// It exists because internal/hook's central safeguard stops at the moment of
// writing. That package interpolates the lint gate code from core.CodeLint
// rather than typing it as a shell literal, precisely so a renumbered constant
// cannot leave behind a hook that compares against a code glyph no longer emits
// — one that waves every violation through, with the whole suite green. The
// interpolation guarantees that about the hook glyph WRITES. It guarantees
// nothing about the hook already sitting in a developer's .git/hooks, which was
// written by whichever glyph was on PATH that day and is never revisited: hooks
// are not tracked by git, so no pull, no fleet-sync and no CI job can refresh
// one. The installed copy is the only artefact in this system that drifts with
// nobody watching, and it drifts in the direction of silence — a stale hook
// still exits 0, so its verdict looks exactly like a clean message.
//
// Read-only like every other check: it reports the refresh command and leaves
// running it to the developer.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/hook"
)

// checkCommitMsgHook asks whether a STALE glyph-written commit-msg hook is
// installed here. Not "is a hook installed" — the difference decides three of
// the four verdicts:
//
//   - identical -> pass.
//   - written by glyph, and different -> FAIL. This is the drift, and it fails
//     in the quiet direction. What breaks is concrete: the gate code it compares
//     against and the arguments it hands `glyph lint` were both frozen at
//     install time.
//   - nothing installed -> PASS, and this is the argued one. Absence is not
//     drift: the hook is opt-in, CI is the authority, and hooks do not clone, so
//     a checkout on an Actions runner has none BY CONSTRUCTION. Grading that as
//     advice would post a notice on every doctor run in every repository, for a
//     condition no repository can avoid — which is how a fleet learns to skip
//     the report, the one failure mode a voluntary check cannot survive (§7).
//     `glyph hook install` is offered where it belongs, in the pass message.
//   - a hook somebody else wrote -> advice. glyph refuses to overwrite it
//     (hook.Install), so this is a standing choice rather than drift, and unlike
//     absence it is rare enough that saying so once costs nothing.
func checkHook(k hook.Kind, id, dir string, dirErr error) Check {
	c := Check{
		ID: id,
		Expected: "no stale glyph-written " + k.Name + " hook: either none is installed, or the installed one is " +
			"byte-identical to the hook this binary writes",
	}

	// The hooks directory is git's answer, not .git/hooks: core.hooksPath
	// relocates it and the family's older repos set it to scripts/hooks. When
	// git could not be asked, the check has no subject — which is unknown, never
	// "no hook installed", because those recommend opposite actions.
	if dirErr != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("git could not report where hooks live: %v", dirErr)
		c.Message = "the hooks directory is git's to name (core.hooksPath relocates it), so without that answer there is " +
			"no file to compare. Whether a hook is installed here is unverified, not verified-absent"
		c.Fix = "re-run from inside a git checkout; if git is unavailable, `glyph doctor` cannot see this repository's hooks at all"
		return c
	}

	path := filepath.Join(dir, k.Name)
	body, err := os.ReadFile(path) // #nosec G304 -- the path git itself reported for this checkout
	switch {
	case os.IsNotExist(err):
		c.Status = StatusPass
		c.Observed = "no " + k.Name + " hook at " + path + ", so none can be stale"
		c.Message = "the local gate is opt-in and CI is the authority, so its absence costs a round trip rather than a " +
			"wrong verdict. `glyph hook install` adds it: it holds no copy of the rules (it shells back to " +
			"`" + k.Asks + "`) and blocks for a real convention violation and nothing else"
		return c
	case err != nil:
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("%s could not be read: %v", path, err)
		c.Message = "a file doctor cannot read could hold anything, including a hook written by a glyph that is no longer current"
		c.Fix = "fix the file permissions and re-run"
		return c
	}

	installed := string(body)
	switch {
	case installed == k.Script:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("%s matches this glyph's hook exactly", path)
		c.Message = "the local gate and this binary agree on the exit code that stops a commit and on what is handed to lint"
	case k.Stale(installed):
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%s was written by glyph and no longer matches this binary's hook (%s)", path, diffSummary(installed, k.Script))
		c.Message = "a hook is written once and never refreshed by anything — hooks are untracked, so no pull, no fleet-sync " +
			"and no CI job can update one. Everything it decides was therefore frozen at install time: the exit code it " +
			"treats as a convention violation (glyph interpolates that from core.CodeLint precisely because a stale copy " +
			"waves every violation through) and the arguments it hands `glyph lint`. A hook that judges a message by rules " +
			"other than this binary's is the hook-green/CI-red split, and it fails in the quiet direction — exit 0"
		c.Fix = "glyph hook install (re-installing over a glyph-written hook is a no-questions refresh)"
	default:
		c.Status = StatusAdvice
		c.Observed = fmt.Sprintf("%s exists and glyph did not write it (no `%s` marker)", path, hook.Marker)
		c.Message = "glyph never silently overwrites somebody else's hook, so this is a standing choice and not drift. What " +
			"it costs is that nothing here is known to lint the convention locally; if this hook does call glyph, it does " +
			"so on terms this binary cannot see"
		c.Fix = "inspect it, then `glyph hook install --force` to replace it with the generated hook"
	}
	return c
}

// checkHookFires reports what happened when the installed commit-msg hook was
// actually FIRED with a violating message — the live half of the hook
// diagnosis, and the half the byte-compare above cannot supply.
//
// Byte-identical bytes prove the SCRIPT is current; they prove nothing about
// the glyph the script reaches at run time. The script resolves `glyph` on
// PATH, and by design it blocks only on the lint gate code and waves every
// other failure through — so a PATH wrapper that builds a different checkout
// (CLAUDE.md documents this exactly: inside a worktree the wrapper builds the
// PRIMARY clone), or a source tree that no longer compiles, turns the local
// gate into a silent no-op while its bytes still compare clean. Firing the
// hook with a message that violates the convention asks the only question that
// covers the whole chain: does this hook, on this machine, today, block?
//
// The probe is the CALLER's (internal/cli owns every subprocess), and it fires
// ONLY the byte-identical arm. Someone else's hook is theirs to run, and a
// stale glyph hook already fails the drift check above — a read-only diagnosis
// must not execute code it does not vouch for. Everything not fired is
// therefore a pass here with the sibling check named as the owner: this check
// answers "does the current hook work", not "is a hook installed".
func checkHookFires(probe *HookProbe, dirErr error) Check {
	gate := int(core.CodeLint)
	c := Check{
		ID: IDCommitMsgFires,
		Expected: fmt.Sprintf("fired with a violating message, the byte-identical commit-msg hook blocks at exit %d — "+
			"proof the glyph it resolves on PATH can actually lint", gate),
	}
	switch {
	case probe == nil && dirErr != nil:
		c.Status = StatusUnknown
		c.Observed = "the hooks directory is unknown (see commit-msg-hook), so nothing could be fired"
		c.Message = "whether the local gate works is unverified, not verified-absent — the same distinction the " +
			"byte-compare check draws when git cannot name the directory"
		c.Fix = "re-run from inside a git checkout"
	case probe == nil:
		c.Status = StatusPass
		c.Observed = "no byte-identical glyph commit-msg hook is installed, so there was nothing to fire"
		c.Message = "absence, a foreign hook and a stale hook are all the commit-msg-hook check's verdicts; " +
			"this check only ever fires the hook that check already calls current"
	case probe.Err != nil:
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("the probe itself could not run: %v", probe.Err)
		c.Message = "an unrunnable probe is not a verdict about the hook — whether the local gate works is unverified"
		c.Fix = "check that the hook file is executable and /bin/sh is available, then re-run"
	case probe.Exit == gate:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("the hook blocked the probe message at exit %d", gate)
		c.Message = "the whole chain answered: the script, the glyph it resolves on PATH, and the verdict that " +
			"stops a commit are in working order on this machine"
	case probe.Exit == 0:
		c.Status = StatusFail
		c.Observed = "the hook let a violating message through at exit 0"
		c.Message = "the script is current but the glyph it reaches cannot lint — the hook blocks only on the gate " +
			"code and waves every other failure through by design, so a PATH wrapper building a different checkout " +
			"or a source tree that does not compile turns the local gate into a silent no-op with clean-looking bytes. " +
			"Every commit on this machine is going unlinted while CI still enforces the convention"
		c.Fix = "run `glyph lint --message 'probe'` by hand and check what `command -v glyph` resolves to; " +
			"repair the wrapper's source checkout (or the build) it points at"
	default:
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("the hook exited %d, a code the generated script never returns (it exits only 0 or %d)", probe.Exit, gate)
		c.Message = "an exit outside the script's own vocabulary means the shell or filesystem failed underneath it, " +
			"not that the hook reached a verdict"
		c.Fix = "run the hook by hand against a scratch file and read what it prints"
	}
	return c
}

// diffSummary names WHERE two hook scripts first part company, so the report is
// actionable without printing a shell script into a diagnostic. The line number
// is the developer's entry point into a `diff` they can run themselves; the byte
// counts catch the case that parts nowhere — one file being a prefix of the
// other, where every shared line matches and the difference is only what one of
// them goes on to say.
func diffSummary(installed, want string) string {
	got, expect := strings.Split(installed, "\n"), strings.Split(want, "\n")
	for i := range min(len(got), len(expect)) {
		if got[i] != expect[i] {
			return fmt.Sprintf("first difference at line %d; %d bytes vs %d", i+1, len(installed), len(want))
		}
	}
	return fmt.Sprintf("identical for %d line(s), then one runs on; %d bytes vs %d", min(len(got), len(expect)), len(installed), len(want))
}
