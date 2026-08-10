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
	case strings.Contains(installed, hook.Marker):
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
