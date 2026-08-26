// Package hook installs the git hooks that lint through glyph itself: the
// commit-msg hook, which judges one message as it is written, and the pre-push
// hook, which judges the commits a push would add.
//
// The hook holds NO copy of the convention — it shells straight back to
// `glyph lint --stdin`. That is the whole point: the per-repo hand-written
// regexes it replaces encoded the retired Conventional form and silently fell
// out of lockstep when the convention moved, so four repos could not commit in
// the current style while CI happily accepted it. With the binary as the only
// source of the rules, a hook installed once cannot drift.
//
// It decides file contents and the overwrite policy; the filesystem writes live
// in Install, and the CLI wiring in internal/cli.
package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
)

// Marker identifies a hook this command wrote. Re-installing over one is a
// no-questions-asked refresh (that is how a repo picks up a new script), while
// a hook WITHOUT it is somebody else's file and is never clobbered silently.
// The string must stay stable across versions — changing it would make every
// previously installed hook look foreign.
const Marker = "installed-by: glyph hook install"

// Script is the commit-msg hook. Three properties are load-bearing:
//
//   - It never matches the convention itself. No regex, no gitmoji list — those
//     live in the binary, so the hook cannot go stale.
//   - It stops a commit for ONE reason only: glyph's lint gate code, a real
//     convention violation. Any other failure means glyph could not answer —
//     not on PATH, no source clone behind the wrapper, a broken build — and the
//     commit proceeds with a warning. The hook is an early-warning device on
//     developer machines; the commit-lint CI job is the authority. Blocking a
//     commit because the tool is unwell would make glyph a hard dependency of
//     committing to six repos, which is a worse failure than a late lint.
//   - Consequently it must NEVER exit non-zero on its own account. Every path
//     that is not "glyph said violation" ends in exit 0.
//
// The gate code is INTERPOLATED from core.CodeLint rather than typed as a shell
// literal, which is the fourth property and the one that had to be built. Plenty
// of shell branches on glyph's exit codes — lint.yml, release.yml and
// goreleaser.yml all do, and so do the fleet's CI wrappers — but this file is the
// only one glyph WRITES, so it is the only one glyph can keep honest. The
// compiler stops checking at the backtick: the number appeared twice as text, and
// renumbering the constant would have left a hook that compares against a code
// glyph no longer emits — i.e. one that waves every violation through — with the
// whole suite green, because the test pinned the literal too. A generated hook
// cannot disagree with the constant it is generated from.
//
// The interpolated words are constants since profiles left (their bytes must
// stay IDENTICAL to every installed copy, or a fleet refresh reports every
// clone drifted); the gate code is interpolated exactly as before — generated,
// never retyped. The founding property holds: the hook holds no knowledge,
// it asks glyph.
func commitMsgScript() string {
	const argSuffix, convention = "", "the repository's glyph.toml convention"
	return fmt.Sprintf(`#!/bin/sh
# glyph commit-msg hook — %s
#
# Lints the message being written against %[3]s by calling
# glyph, which holds the rules. Do not add a regex here: a local copy of the
# convention is exactly what drifts out of lockstep when the rules move.
#
# Regenerate with: glyph hook install --force
if ! command -v glyph >/dev/null 2>&1; then
	echo "glyph: not on PATH — skipping the commit-msg lint (CI still enforces it)" >&2
	exit 0
fi

glyph lint --stdin%[4]s <"$1"
status=$?

# %[2]d is glyph's commit-convention violation code — the one answer that should
# stop a commit. Anything else non-zero means glyph could not reach a verdict
# (missing source clone behind the PATH wrapper, a build failure, a bad flag);
# failing the commit on that would gate committing on the health of a tool
# whose whole job here is advisory.
if [ "$status" -eq %[2]d ]; then
	exit %[2]d
fi
if [ "$status" -ne 0 ]; then
	echo "glyph: could not lint this message (exit $status) — letting the commit through; CI still enforces the convention" >&2
fi
exit 0
`, Marker, int(core.CodeLint), convention, argSuffix)
}

// PrePushScript is the pre-push hook. It carries the same three properties as
// Script and one more that is specific to it: it computes NOTHING about the
// push. git's protocol arrives on stdin and reaches glyph untouched, and argv
// is forwarded verbatim rather than mapped onto flags.
//
// That last part is a rollout decision, not a style one. This file is installed
// once into ~34 clones and nothing refreshes one — no pull, no fleet-sync, no
// CI job — so a script that named git's arguments would freeze today's argv
// shape into every copy, and the day git passes a third argument every frozen
// copy turns a push into a usage error. The same reasoning put the commit-msg
// hook's cleanup-mode derivation inside the binary (DESIGN 2.1); here it keeps
// the range arithmetic there too, which matters more, because a range computed
// by a stale script is a wrong verdict rather than a loud failure.
func prePushScript() string {
	const argSuffix = ""
	return fmt.Sprintf(`#!/bin/sh
# glyph pre-push hook — %s
#
# Hands git's pre-push protocol (four fields per line, on stdin) to glyph, which
# owns the range arithmetic and the rules. Do not compute a range here and do
# not add a regex: this file is installed once and nothing ever refreshes it.
#
# Regenerate with: glyph hook install --force
if ! command -v glyph >/dev/null 2>&1; then
	echo "glyph: not on PATH — skipping the pre-push lint (CI still enforces it)" >&2
	exit 0
fi

glyph hook pre-push%[3]s -- "$@"
status=$?

# %[2]d is glyph's commit-convention violation code — the one answer that should
# stop a push. Anything else non-zero means glyph could not reach a verdict
# (missing source clone behind the PATH wrapper, a build failure, a bad flag);
# failing the push on that would gate pushing on the health of a tool whose
# whole job here is advisory.
if [ "$status" -eq %[2]d ]; then
	exit %[2]d
fi
if [ "$status" -ne 0 ]; then
	echo "glyph: could not lint the outgoing range (exit $status) — letting the push through; CI still enforces the convention" >&2
fi
exit 0
`, Marker, int(core.CodeLint), argSuffix)
}

// Kind is one hook glyph writes: the name git looks the file up by, and the
// script that goes there.
//
// One ordered list rather than a switch in each caller, because the hook name
// was previously spelled independently in the installer, the doctor check and
// the tests — three places a second kind would have had to be added to by hand,
// with nothing failing if one were missed.
type Kind struct {
	Name   string
	Script string
	// Asks is the glyph invocation this hook makes, for messages that have to
	// name it. It lives here rather than in each message so the doctor check
	// and the help text cannot describe the hook differently from what it does.
	Asks string
}

// Kinds is every hook glyph writes, in install order. A bare
// `glyph hook install` writes all of them. The v1 profile interpolation is
// gone with profiles themselves: the convention lives in the repository's
// glyph.toml, which the binary reads — the hook still carries no copy of
// anything.
func Kinds() []Kind {
	return []Kind{
		{Name: "commit-msg", Script: commitMsgScript(), Asks: "glyph lint --stdin"},
		{Name: "pre-push", Script: prePushScript(), Asks: "glyph hook pre-push"},
	}
}

// Stale reports whether an installed hook body is one GLYPH wrote that no
// longer matches what this binary would write.
//
// The three-way split matters and is why this is one function rather than a
// comparison at each call site: identical is fine, a body glyph did not write is
// somebody else's standing choice, and only the third state — glyph's marker on
// bytes that have moved — is drift. Everything a hook decides was frozen by
// whichever glyph was on PATH the day it was installed, including the exit code
// it treats as a violation, and it fails in the quiet direction: a stale hook
// still exits 0, which is what a clean message looks like.
func (k Kind) Stale(installed string) bool {
	return installed != k.Script && strings.Contains(installed, Marker)
}

// KindByName resolves a hook name, or reports that glyph does not write one.
func KindByName(name string) (Kind, bool) {
	for _, k := range Kinds() {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// KindNames is the name list, for cobra's ValidArgs and for help text — so the
// completion a user gets and the set Install accepts cannot disagree.
func KindNames() []string {
	names := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		names = append(names, k.Name)
	}
	return names
}

// Result reports what Install did to ONE hook, so the CLI can print an accurate
// line and tests can assert the branch taken without re-reading the disk.
type Result struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Action  string `json:"action"` // "installed" | "refreshed" | "unchanged"
	Existed bool   `json:"existed"`
}

// Report is one install run's answer for every kind it was asked about.
type Report struct {
	Hooks []Result `json:"hooks"`
}

// Summary renders one human line per hook.
func (r Report) Summary() string {
	lines := make([]string, 0, len(r.Hooks))
	for _, h := range r.Hooks {
		lines = append(lines, h.Summary())
	}
	return strings.Join(lines, "\n")
}

// Install writes each requested hook into hooksDir, creating the directory when
// core.hooksPath points somewhere that does not exist yet (a fresh clone of a
// repo that tracks its hooks).
//
// The overwrite policy is the one decision this command has to make, and it is
// per hook:
//
//   - nothing there            -> write it
//   - a hook carrying Marker   -> rewrite it (idempotent refresh)
//   - identical to its script  -> leave it alone, report "unchanged"
//   - any other file           -> refuse (usage), unless force
//
// Refusing by default matters because the repos this targets track a real
// commit-msg hook in git; overwriting one unasked would silently stage a
// content change the developer never requested.
//
// Every kind is PLANNED before any is written. A run that refuses halfway
// reports failure while the tree already carries a file the developer did not
// ask for, and "exit 2, nothing happened" is what a caller reads that as.
func Install(hooksDir string, force bool, kinds ...Kind) (Report, error) {
	var rep Report
	plans := make([]Kind, 0, len(kinds))
	for _, k := range kinds {
		res, write, err := plan(hooksDir, k, force)
		// The partial report travels WITH the refusal: the caller's next
		// question is which hook refused and what was found there, and an
		// emptied report answers it with silence while the error prose carries
		// the path as text nobody can branch on.
		rep.Hooks = append(rep.Hooks, res)
		if err != nil {
			return rep, err
		}
		if write {
			plans = append(plans, k)
		}
	}
	if len(plans) == 0 {
		// Still re-assert the mode on everything that was already current: a
		// hook that lost its execute bit is a hook git silently ignores.
		return rep, chmodAll(hooksDir, kinds)
	}
	if mkErr := os.MkdirAll(hooksDir, 0o755); mkErr != nil { //nolint:gosec // git requires a traversable hooks dir
		return Report{}, core.APIf("creating %s: %v", hooksDir, mkErr)
	}
	for _, k := range plans {
		path := filepath.Join(hooksDir, k.Name)
		if wErr := os.WriteFile(path, []byte(k.Script), 0o755); wErr != nil { //nolint:gosec // a hook must be executable
			return Report{}, core.APIf("writing %s: %v", path, wErr)
		}
	}
	return rep, chmodAll(hooksDir, kinds)
}

// plan decides what one kind needs, without touching the filesystem beyond the
// read that answers the question.
func plan(hooksDir string, k Kind, force bool) (Result, bool, error) {
	path := filepath.Join(hooksDir, k.Name)
	res := Result{Kind: k.Name, Path: path}

	existing, err := os.ReadFile(path) //nolint:gosec // path is derived from git's own hooks dir
	switch {
	case err == nil:
		res.Existed = true
		switch {
		case string(existing) == k.Script:
			res.Action = "unchanged"
			return res, false, nil
		case strings.Contains(string(existing), Marker), force:
			res.Action = "refreshed"
		default:
			return res, false, core.Usagef(
				"%s already exists and was not written by glyph — refusing to overwrite it. "+
					"Inspect it, then re-run with --force to replace it (its rules, if any, are "+
					"superseded: the new hook calls glyph)", path)
		}
	case os.IsNotExist(err):
		res.Action = "installed"
	default:
		return res, false, core.APIf("reading %s: %v", path, err)
	}
	return res, true, nil
}

// chmodAll re-asserts the execute bit on every hook that is present.
func chmodAll(hooksDir string, kinds []Kind) error {
	for _, k := range kinds {
		path := filepath.Join(hooksDir, k.Name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if cerr := os.Chmod(path, 0o755); cerr != nil { //nolint:gosec // a hook must be executable
			return core.APIf("making %s executable: %v", path, cerr)
		}
	}
	return nil
}

// Summary renders the one human line the CLI prints for a Result.
func (r Result) Summary() string {
	switch r.Action {
	case "unchanged":
		return fmt.Sprintf("%s hook already current at %s", r.Kind, r.Path)
	case "refreshed":
		return fmt.Sprintf("%s hook refreshed at %s", r.Kind, r.Path)
	default:
		return fmt.Sprintf("%s hook installed at %s", r.Kind, r.Path)
	}
}
