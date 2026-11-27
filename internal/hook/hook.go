// Package hook installs the git hooks that lint through glyph itself: the
// commit-msg hook, which judges one message as it is written, and the pre-push
// hook, which judges the commits a push would add.
//
// The hook holds NO copy of the convention — it shells straight back to
// `glyph lint --stdin`. A copy falls out of lockstep when the convention
// moves, and the hook then refuses commits CI accepts; with the binary as the
// only source of the rules, a hook installed once cannot drift.
//
// It decides file contents, what an installed hook is (Read: the one
// classification every reader switches on) and the overwrite policy; the
// filesystem writes live in Install, and the CLI wiring in internal/cli.
package hook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// Marker identifies a hook this command wrote. Re-installing over one is a
// no-questions-asked refresh (that is how a repo picks up a new script), while
// a hook WITHOUT it is somebody else's file and is never clobbered silently.
// The string must stay stable across versions — changing it would make every
// previously installed hook look foreign.
const Marker = "installed-by: glyph hook install"

// commitMsgScript is the commit-msg hook. Three properties are load-bearing:
//
//   - It never matches the convention itself. No regex, no copy of glyph.toml's
//     patterns — glyph reads those at run time, so the hook cannot go stale.
//   - It stops a commit for ONE reason only: glyph's lint gate code, a real
//     convention violation. Any other failure means glyph could not answer —
//     not on PATH, no source clone behind the wrapper, a broken build — and the
//     commit proceeds with a warning. The hook is an early-warning device on
//     developer machines; the commit-lint CI job is the authority. Blocking a
//     commit because the tool is unwell would make glyph a hard dependency of
//     committing to every repository that installs it, which is a worse failure
//     than a late lint.
//   - Consequently it must NEVER exit non-zero on its own account. Every path
//     that is not "glyph said violation" ends in exit 0.
//
// The gate code is INTERPOLATED from core.CodeLint rather than typed as a shell
// literal — the fourth property. The hook scripts are the only shell glyph
// WRITES that branches on its exit codes, so they are the only such shell
// glyph can keep honest, and the compiler stops checking at the backtick: a
// literal would let a renumbered constant leave a hook that waves every
// violation through with the suite green. A generated hook cannot disagree
// with the constant it is generated from.
func commitMsgScript() string {
	return fmt.Sprintf(`#!/bin/sh
# glyph commit-msg hook — %s
#
# Lints the message being written by calling glyph, which reads the repository's
# glyph.toml at run time. Do not add a regex here: a local copy of the
# convention is exactly what drifts out of lockstep when the rules move.
#
# Regenerate with: glyph hook install commit-msg
if ! command -v glyph >/dev/null 2>&1; then
	echo "glyph: not on PATH — skipping the commit-msg lint (CI still enforces it)" >&2
	exit 0
fi

glyph lint --stdin <"$1"
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
`, Marker, int(core.CodeLint))
}

// prePushScript is the pre-push hook. It carries the same three properties as
// commitMsgScript and one more that is specific to it: it computes NOTHING about
// the push. git's protocol arrives on stdin and reaches glyph untouched, and argv
// is forwarded verbatim rather than mapped onto flags.
//
// That last part is a rollout decision, not a style one: nothing refreshes an
// installed hook (DESIGN §2.1), so a script that mapped git's arguments onto
// flags would freeze today's argv shape into every copy: an argument git adds
// later would never reach glyph.
func prePushScript() string {
	return fmt.Sprintf(`#!/bin/sh
# glyph pre-push hook — %s
#
# Hands git's pre-push protocol (four fields per line, on stdin) to glyph, which
# owns the range arithmetic and reads the rules from glyph.toml. Do not compute
# a range here and do not add a regex: this file is installed once and nothing
# ever refreshes it.
#
# Regenerate with: glyph hook install pre-push
if ! command -v glyph >/dev/null 2>&1; then
	echo "glyph: not on PATH — skipping the pre-push lint (CI still enforces it)" >&2
	exit 0
fi

glyph hook pre-push -- "$@"
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
`, Marker, int(core.CodeLint))
}

// Kind is one hook glyph writes: the name git looks the file up by, and the
// script that goes there.
//
// One ordered list rather than a switch in each caller: the installer writes,
// and the doctor checks read, the file each entry names with the script it
// holds, so what is installed and what is checked cannot disagree about a hook.
type Kind struct {
	Name   string
	Script string
	// Asks is the glyph invocation this hook makes, for messages that have to
	// name it. It lives here rather than in each message so the doctor check
	// cannot describe the hook differently from what it does.
	Asks string
}

// SetHeadRemedy is the command that lets the pre-push hook block on remote:
// it refuses a violation only on the default branch it reads from
// refs/remotes/<remote>/HEAD, and `git remote set-head <remote> -a` records
// that. canRecord false (gitsource.RemoteCanRecordHead) is a name git cannot
// place in a ref, where neither set-head nor `git remote rename` records a
// head; DESIGN §7's pre-push-hook advice measures each arm, and
// TestSetHeadRemedyRunsPasted runs both pasted. One home for doctor's fix and
// the hook's own warning.
func SetHeadRemedy(remote string, canRecord bool) string {
	if canRecord {
		return "git remote set-head " + shellQuote(remote) + " -a"
	}
	return "pick a name git accepts for <new-name>, then " +
		"git config --rename-section " + shellQuote("remote."+remote) + " remote.<new-name> && " +
		"git config --replace-all remote.<new-name>.fetch '+refs/heads/*:refs/remotes/<new-name>/*' && " +
		"git fetch <new-name> && git remote set-head <new-name> -a"
}

// shellQuote single-quotes s for a POSIX shell, closing and reopening the
// quote around each embedded single quote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Kinds is every hook glyph writes, in install order. A bare
// `glyph hook install` writes all of them.
func Kinds() []Kind {
	return []Kind{
		{Name: "commit-msg", Script: commitMsgScript(), Asks: "glyph lint --stdin"},
		{Name: "pre-push", Script: prePushScript(), Asks: "glyph hook pre-push"},
	}
}

// State is what Read found at a hook's path. Every reader switches on it — the
// installer's overwrite policy, doctor's verdicts, the probe that fires only a
// current hook, the drift notice the hook's own run prints — so none of them
// re-derives it from the bytes.
//
// The three-way split of a file that exists matters and is why this is one
// classification rather than a comparison at each call site: identical is
// fine, a body glyph did not write is somebody else's standing choice, and
// only the third state — glyph's marker on bytes that have moved — is drift.
// Everything a hook decides was frozen by whichever glyph was on PATH the day
// it was installed, including the exit code it treats as a violation, and it
// fails in the quiet direction: a stale hook still exits 0, which is what a
// clean message looks like.
type State int

const (
	// Absent — no file at the path.
	Absent State = iota
	// Current — byte-identical to the script this binary writes.
	Current
	// Stale — glyph's Marker on bytes that differ from that script.
	Stale
	// Foreign — a file without the Marker: a hook glyph did not write.
	Foreign
)

// Installed is what Read found at one kind's path. Body is the file's bytes
// (empty when Absent), kept for doctor's diff summary.
type Installed struct {
	Path  string
	State State
	Body  string
}

// Read classifies the hook of kind k in hooksDir. The error is a read failure
// other than not-exist; the Installed beside it carries only Path, since a
// file that exists and cannot be read could be in any of the three states a
// file can be in.
func (k Kind) Read(hooksDir string) (Installed, error) {
	in := Installed{Path: filepath.Join(hooksDir, k.Name)}
	body, err := os.ReadFile(in.Path) //nolint:gosec // path is derived from git's own hooks dir
	switch {
	case errors.Is(err, fs.ErrNotExist):
		in.State = Absent
		return in, nil
	case err != nil:
		return in, err
	}
	in.Body = string(body)
	switch {
	case in.Body == k.Script:
		in.State = Current
	case strings.Contains(in.Body, Marker):
		in.State = Stale
	default:
		in.State = Foreign
	}
	return in, nil
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
//
// Action is what happened to the file, and is set only once it has happened:
//
//   - "installed": written where no hook was
//   - "refreshed": rewritten over a glyph hook, or over any hook under --force
//   - "unchanged": already current, left as it was
//   - "refused": a hook glyph did not write, left as it was (no --force)
//   - "failed": reading or writing it failed (a failed write may leave it partly written)
//   - "skipped": it was to be written, and the run stopped before writing it
//
// The last three appear only in a refusal's details, where the first two name
// a write that happened before the run stopped.
//
// Existed says glyph saw a file at Path before the run. False is also what an
// lstat that could not look reports (a hooks directory glyph cannot search),
// so it is not proof that no file is there.
type Result struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Action  string `json:"action"`
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
		lines = append(lines, h.summary())
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
	// The report travels WITH every refusal, as its details: the caller's next
	// question is which hook refused and what became of the others, and an
	// emptied report answers it with silence while the error prose carries the
	// path as text nobody can branch on. So a refusal still plans every kind,
	// and each entry says what happened to its file before the run stopped.
	rep := Report{Hooks: make([]Result, 0, len(kinds))}
	type write struct {
		at    int
		kind  Kind
		earns string
	}
	var writes []write
	var refusal *core.Error
	foreign := -1 // the run's first refusal, when it is a foreign hook
	for _, k := range kinds {
		res, earns, err := plan(hooksDir, k, force)
		if earns != "" {
			writes = append(writes, write{at: len(rep.Hooks), kind: k, earns: earns})
		}
		if refusal == nil && foreign < 0 {
			if err != nil {
				refusal = err
			} else if res.Action == "refused" {
				foreign = len(rep.Hooks)
			}
		}
		rep.Hooks = append(rep.Hooks, res)
	}
	if foreign >= 0 {
		rest := make([]string, 0, len(writes))
		for _, w := range writes {
			rest = append(rest, w.kind.Name)
		}
		refusal = refuseForeign(rep.Hooks[foreign], rest)
	}
	if refusal != nil {
		return rep, refusal.WithDetails(rep.Hooks)
	}
	if len(writes) > 0 {
		if mkErr := os.MkdirAll(hooksDir, 0o755); mkErr != nil { //nolint:gosec // git requires a traversable hooks dir
			// MkdirAll names the directory it failed on, an ancestor when one is
			// missing and cannot be made: only then is the hooks directory named too.
			if pe, ok := errors.AsType[*fs.PathError](mkErr); ok && pe.Path != hooksDir {
				return rep, core.APIf("%v — so the hooks directory %s could not be created, and nothing was written", mkErr, hooksDir).WithDetails(rep.Hooks)
			}
			return rep, core.APIf("%v — the hooks directory could not be created, so nothing was written", mkErr).WithDetails(rep.Hooks)
		}
	}
	for _, w := range writes {
		path := filepath.Join(hooksDir, w.kind.Name)
		if wErr := os.WriteFile(path, []byte(w.kind.Script), 0o755); wErr != nil { //nolint:gosec // a hook must be executable
			rep.Hooks[w.at].Action = "failed"
			return rep, core.APIf("%v — the %s hook could not be written", wErr, w.kind.Name).WithDetails(rep.Hooks)
		}
		rep.Hooks[w.at].Action = w.earns
	}
	// Re-asserted on every hook present, the already-current included: a hook
	// that lost its execute bit is a hook git silently ignores.
	if cerr := chmodAll(hooksDir, kinds); cerr != nil {
		return rep, cerr.WithDetails(rep.Hooks)
	}
	return rep, nil
}

// plan decides what one kind needs, without touching the filesystem beyond the
// read that answers the question — and, when that read fails, the lstat that
// says whether glyph can see a file there. A kind to be written comes back
// "skipped", with the Action its write earns beside it: Install sets that
// Action only once the write has happened. A foreign hook comes back "refused"
// with no error: its refusal names the kinds the rest of the run earned writes
// for, which only Install knows, so Install builds it (refuseForeign).
func plan(hooksDir string, k Kind, force bool) (Result, string, *core.Error) {
	in, err := k.Read(hooksDir)
	res := Result{Kind: k.Name, Path: in.Path}
	if err != nil {
		// A file that exists and cannot be read still existed, and Read reports
		// absence without an error, so the lstat asks again. It can fail too:
		// under a hooks directory glyph cannot search (mode 000) it cannot look,
		// and Existed is then false for a file that is there.
		_, serr := os.Lstat(in.Path)
		res.Existed = serr == nil
		res.Action = "failed"
		return res, "", core.APIf("%v — what the %s hook holds could not be read, so nothing was written", err, k.Name)
	}
	res.Existed = in.State != Absent
	switch {
	case in.State == Absent:
		res.Action = "skipped"
		return res, "installed", nil
	case in.State == Current:
		res.Action = "unchanged"
		return res, "", nil
	case in.State == Stale, force:
		res.Action = "skipped"
		return res, "refreshed", nil
	default:
		res.Action = "refused"
		return res, "", nil
	}
}

// refuseForeign is the refusal of res, a hook glyph did not write. rest is the
// kinds whose plan in the same run earned a write, in run order: the refusal
// wrote them neither, so each command it gives names them too, or following it
// leaves a kind the run asked for missing. rest never holds a foreign kind,
// whose plan earns no write without --force: a command leaves alone every
// foreign hook it does not name, and only the refused one is named.
func refuseForeign(res Result, rest []string) *core.Error {
	replace := "glyph hook install --force " + strings.Join(append([]string{res.Kind}, rest...), " ")
	if len(rest) == 0 {
		return core.Usagef(
			"the %s hook %s already exists and was not written by glyph — refusing to overwrite it, so nothing "+
				"was written. Inspect it, then run %s to replace it (its rules, if any, are superseded: the new "+
				"hook calls glyph)", res.Kind, res.Path, replace)
	}
	return core.Usagef(
		"the %s hook %s already exists and was not written by glyph — refusing to overwrite it, so nothing "+
			"was written, %s included. Inspect it, then run %s to replace it and write the rest (its rules, if "+
			"any, are superseded: the new hook calls glyph), or glyph hook install %s to keep it and write the rest",
		res.Kind, res.Path, strings.Join(rest, " and "), replace, strings.Join(rest, " "))
}

// chmodAll re-asserts the execute bit on every hook that is present.
func chmodAll(hooksDir string, kinds []Kind) *core.Error {
	for _, k := range kinds {
		path := filepath.Join(hooksDir, k.Name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if cerr := os.Chmod(path, 0o755); cerr != nil { //nolint:gosec // a hook must be executable
			return core.APIf("%v — the hook could not be made executable", cerr)
		}
	}
	return nil
}

// summary renders the one human line the CLI prints for a Result. An Action
// outside the three a run that succeeded reports is spelled as it is, never
// as one of them: a line must not claim a write that did not happen.
func (r Result) summary() string {
	switch r.Action {
	case "installed":
		return fmt.Sprintf("%s hook installed at %s", r.Kind, r.Path)
	case "unchanged":
		return fmt.Sprintf("%s hook already current at %s", r.Kind, r.Path)
	case "refreshed":
		return fmt.Sprintf("%s hook refreshed at %s", r.Kind, r.Path)
	default:
		return fmt.Sprintf("%s hook at %s: action %q", r.Kind, r.Path, r.Action)
	}
}
