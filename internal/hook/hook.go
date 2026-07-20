// Package hook installs the commit-msg hook that lints an in-progress commit
// message through glyph itself.
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
//   - It stops a commit for ONE reason only: glyph's lint exit code 3, a real
//     convention violation. Any other failure means glyph could not answer —
//     not on PATH, no source clone behind the wrapper, a broken build — and the
//     commit proceeds with a warning. The hook is an early-warning device on
//     developer machines; the commit-lint CI job is the authority. Blocking a
//     commit because the tool is unwell would make glyph a hard dependency of
//     committing to six repos, which is a worse failure than a late lint.
//   - Consequently it must NEVER exit non-zero on its own account. Every path
//     that is not "glyph said violation" ends in exit 0.
const Script = `#!/bin/sh
# glyph commit-msg hook — ` + Marker + `
#
# Lints the message being written against the gitmoji convention by calling
# glyph, which holds the rules. Do not add a regex here: a local copy of the
# convention is exactly what drifts out of lockstep when the rules move.
#
# Regenerate with: glyph hook install --force
if ! command -v glyph >/dev/null 2>&1; then
	echo "glyph: not on PATH — skipping the commit-msg lint (CI still enforces it)" >&2
	exit 0
fi

glyph lint --stdin <"$1"
status=$?

# 3 is glyph's commit-convention violation code — the one answer that should
# stop a commit. Anything else non-zero means glyph could not reach a verdict
# (missing source clone behind the PATH wrapper, a build failure, a bad flag);
# failing the commit on that would gate committing on the health of a tool
# whose whole job here is advisory.
if [ "$status" -eq 3 ]; then
	exit 3
fi
if [ "$status" -ne 0 ]; then
	echo "glyph: could not lint this message (exit $status) — letting the commit through; CI still enforces the convention" >&2
fi
exit 0
`

// Result reports what Install did, so the CLI can print an accurate line and
// tests can assert the branch taken without re-reading the disk.
type Result struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "installed" | "refreshed" | "unchanged"
	Existed bool   `json:"existed"`
}

// Install writes the commit-msg hook into hooksDir, creating the directory when
// core.hooksPath points somewhere that does not exist yet (a fresh clone of a
// repo that tracks its hooks).
//
// The overwrite policy is the one decision this command has to make:
//
//   - nothing there            -> write it
//   - a hook carrying Marker   -> rewrite it (idempotent refresh)
//   - identical to Script      -> leave it alone, report "unchanged"
//   - any other file           -> refuse (usage), unless force
//
// Refusing by default matters because the repos this targets track a real
// commit-msg hook in git; overwriting one unasked would silently stage a
// content change the developer never requested.
func Install(hooksDir string, force bool) (Result, error) {
	path := filepath.Join(hooksDir, "commit-msg")
	res := Result{Path: path}

	existing, err := os.ReadFile(path) //nolint:gosec // path is derived from git's own hooks dir
	switch {
	case err == nil:
		res.Existed = true
		switch {
		case string(existing) == Script:
			res.Action = "unchanged"
			// Still re-assert the mode: a hook that lost its execute bit is a
			// hook git silently ignores.
			if cerr := os.Chmod(path, 0o755); cerr != nil { //nolint:gosec // a hook must be executable
				return res, core.APIf("making %s executable: %v", path, cerr)
			}
			return res, nil
		case strings.Contains(string(existing), Marker):
			res.Action = "refreshed"
		case force:
			res.Action = "refreshed"
		default:
			return res, core.Usagef(
				"%s already exists and was not written by glyph — refusing to overwrite it. "+
					"Inspect it, then re-run with --force to replace it (its rules, if any, are "+
					"superseded: the new hook calls `glyph lint --stdin`)", path)
		}
	case os.IsNotExist(err):
		res.Action = "installed"
	default:
		return res, core.APIf("reading %s: %v", path, err)
	}

	if mkErr := os.MkdirAll(hooksDir, 0o755); mkErr != nil { //nolint:gosec // git requires a traversable hooks dir
		return res, core.APIf("creating %s: %v", hooksDir, mkErr)
	}
	if wErr := os.WriteFile(path, []byte(Script), 0o755); wErr != nil { //nolint:gosec // a hook must be executable
		return res, core.APIf("writing %s: %v", path, wErr)
	}
	return res, nil
}

// Summary renders the one human line the CLI prints for a Result.
func (r Result) Summary() string {
	switch r.Action {
	case "unchanged":
		return fmt.Sprintf("commit-msg hook already current at %s", r.Path)
	case "refreshed":
		return fmt.Sprintf("commit-msg hook refreshed at %s", r.Path)
	default:
		return fmt.Sprintf("commit-msg hook installed at %s", r.Path)
	}
}
