package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// This check exists for a failure class NO runtime diagnosis can see —
// glyph's included (checkCallerInputs guards the other member: a missing
// required input). A caller workflow granting less than the reusable it calls
// declares never starts: the run dies as startup_failure before any job, so
// there is no step to print an error, no exit code to classify, and nothing
// red in the caller's own YAML. Measured in akira-toriyama/.github#186 (a
// commit-lint caller granting only contents: read) and recorded in the
// distributed caller stub; the v2.0.0 rollout fixed all 35 fleet repos at the
// canonical source, but nothing has guarded a consumer outside the fleet, or a
// hand edit since. Reading the file is the only vantage point that works,
// which is doctor's. It judges a caller at the release it pins, and only where
// this binary can speak for that release (callerpin.go).

// reusableNeeds is what each glyph reusable DECLARES — workflow level plus any
// job-level elevation — and therefore the minimum a caller must grant, with
// the newest release whose reusable declared otherwise (After; callerpin.go).
// Needs mirror the permissions blocks in this repo's own workflow files and the
// caller stubs those files distribute; TestReusableNeedsMatchTheShippedWorkflows
// holds the two in lockstep, so a grant added to a reusable without a row here
// fails a test instead of shipping a check that blesses broken callers, and
// TestCallerDeclarationBoundsMatchReleasedTags holds After to glyph's tags.
var reusableNeeds = map[string]permRow{
	"lint.yml":       {After: "v1.0.0", Needs: []permNeed{{"contents", "read"}, {"pull-requests", "read"}}},
	"release.yml":    {After: "v0.2.0", Needs: []permNeed{{"contents", "write"}}},
	"pr-verdict.yml": {After: "v0.3.0", Needs: []permNeed{{"contents", "read"}, {"pull-requests", "write"}}},
}

// permRow is one reusable's declaration and the bound below which this
// binary cannot speak for it.
type permRow struct {
	After string
	Needs []permNeed
}

// permNeed is one scope a reusable declares, at the level it declares it.
type permNeed struct {
	Scope string
	Level string // "read" or "write"
}

// permsFloor is the release workflow-caller-permissions first shipped in —
// the oldest glyph whose own doctor can judge its own reusables' grants.
const permsFloor = "v2.1.0"

// checkCallerPermissions scans the local checkout's workflow files for callers
// of glyph's reusable workflows and verifies that each caller's explicit
// `permissions:` grants cover what the reusable declares at the release the
// caller pins. glyph is the running binary's version (judgeAt).
//
// Two deliberate boundaries, both on the side of never crying wolf:
//
//   - A caller with NO permissions block anywhere is not judged. GitHub then
//     applies the repository's default token, which may be permissive (fine)
//     or restricted (the same startup death) — but which one is repository
//     configuration this file cannot see, and a red over a caller that may be
//     perfectly healthy teaches the fleet to ignore the report. This runs
//     BEFORE the pin rule: the other order turns every block-less caller at
//     an old pin unknown (D2a, measured on the prototype).
//   - Grants are unioned across every permissions block in the file, workflow
//     level and job level alike. GitHub only counts a grant on the calling
//     job or above, so a grant on a sibling job could in principle satisfy
//     this check while the run still dies — accepted, because the opposite
//     reading (workflow level only) reds every caller that grants on the job,
//     which is legal and real. A false pass here is the pin check's own
//     documented trade, made for the same reason.
func checkCallerPermissions(root string, rootVerified bool, glyph string) Check {
	c := Check{ID: IDCallerPerms, Expected: permsExpected(glyph)}
	entries, unknown := listWorkflows(root, rootVerified, &c,
		"A caller granting less than its reusable declares dies as startup_failure before any job — "+
			"unverified here, not verified")
	if unknown {
		return c
	}

	callers := 0
	var findings, skipped, remedies, unreadable []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(root, ".github", "workflows", name)
		body, rerr := os.ReadFile(path) // #nosec G304 -- the caller's own checkout, listed above
		if rerr != nil {
			// Not left to the pin check: its unknown answers whether a ref is
			// concrete, not whether a grant covers a reusable, and skipping the
			// file here turned chmod 000 on a failing caller into this check's
			// "nothing to judge" pass (t-fdd8 (3)).
			unreadable = append(unreadable, fmt.Sprintf("%s could not be read: %v", path, rerr))
			continue
		}
		calls := reusablesCalled(name, string(body))
		if len(calls) == 0 {
			continue
		}
		callers++
		grants, declared := callerGrants(string(body))
		if !declared {
			// No explicit block: the repository's default token decides, and
			// this file cannot see that setting. Not judged — see the header.
			continue
		}
		for _, call := range calls {
			row, known := reusableNeeds[strings.ToLower(call.Reusable)]
			nj := unknownReusable(call.Reusable)
			if known {
				nj = judgeAt(call.Ref, row.After, glyph, permsFloor)
			}
			if nj != nil {
				skipped = append(skipped, fmt.Sprintf(".github/workflows/%s calls %s@%s — not judged: %s", name, call.Reusable, call.Ref, nj.Why))
				remedies = remember(remedies, nj.Remedy)
				continue
			}
			for _, n := range row.Needs {
				if !satisfies(grants, n.Scope, n.Level) {
					findings = append(findings, fmt.Sprintf(
						".github/workflows/%s calls %s@%s but its permissions never grant %s: %s",
						name, call.Reusable, call.Ref, n.Scope, n.Level))
				}
			}
		}
	}
	sort.Strings(findings)

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d missing grant(s) across the %d workflow file(s) that call a glyph reusable", len(findings), callers)
		c.Details = slices.Concat(findings, skipped, unreadable)
		c.Message = "a reusable can only downgrade the caller's token, never raise it, so a caller granting less than " +
			"the reusable declares never starts: the run dies as startup_failure before any job (measured in " +
			"akira-toriyama/.github#186) — no step runs, nothing prints, and no runtime diagnosis can see it. " +
			"This static read is the only check that can"
		c.Fix = "add the missing grant to the caller's permissions block — the commented stub in each reusable's " +
			"header is the known-good copy"
		return c
	}
	if unverifiedCallers(&c, skipped, remedies, unreadable, "whether every caller gets past GitHub's startup permission gate") {
		return c
	}
	c.Status = StatusPass
	if callers == 0 {
		c.Observed = "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to misgrant)"
		c.Message = "nothing to judge, observed — not assumed"
		return c
	}
	c.Observed = fmt.Sprintf("%d workflow file(s) call glyph reusables; every judged caller covers what its pinned release declares", callers)
	c.Message = "these runs get past GitHub's startup permission gate"
	return c
}

// permsExpected renders the check's Expected from the table itself, so the
// text cannot drift from the rows it describes.
func permsExpected(glyph string) string {
	names := make([]string, 0, len(reusableNeeds))
	for name := range reusableNeeds {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]string, 0, len(names))
	for _, name := range names {
		row := reusableNeeds[name]
		grants := make([]string, 0, len(row.Needs))
		for _, n := range row.Needs {
			grants = append(grants, n.Scope+": "+n.Level)
		}
		rows = append(rows, fmt.Sprintf("%s after %s: %s", strings.TrimSuffix(name, ".yml"), row.After, strings.Join(grants, ", ")))
	}
	return "every workflow calling a glyph reusable grants at least what that reusable declares at the release it pins, " +
		"judged for " + judgedRange(glyph) + " (" + strings.Join(rows, "; ") + ") — any other pin is could-not-run"
}

// listWorkflows lists root/.github/workflows for the caller-side checks. An
// absent directory under a git-named root is an observed absence — the caller
// loops simply see zero entries — while any other failure, or an absence under
// an unverified bare ".", fills c with the UNKNOWN verdict and returns
// unknown=true. The consequence line is each check's own sentence about what
// an unverified read costs.
func listWorkflows(root string, rootVerified bool, c *Check, consequence string) ([]os.DirEntry, bool) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil && (!rootVerified || !errors.Is(err, fs.ErrNotExist)) {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("%s could not be listed: %v", dir, err)
		c.Message = "doctor reads the LOCAL checkout for this check, so it must run from the repository root. " + consequence
		c.Fix = "re-run from the repository root (cd into the checkout)"
		return nil, true
	}
	return entries, false
}

// reusableCall is one call a workflow file makes into glyph's
// .github/workflows, with the ref it pins.
type reusableCall struct {
	Reusable string // the file name as the caller spelled it
	Ref      string
}

// reusablesCalled returns every glyph reusable this workflow file executes,
// with its pin. It rides on scanUses — the same comment, block-scalar and
// list-dash discipline, for the same traps — and keeps only references into
// .github/workflows/, because an action reference (the install) declares
// nothing the caller must match. The pin is kept: GitHub starts the caller
// against the reusable at that ref, and judgeAt decides whether this binary
// can speak for it.
func reusablesCalled(file, body string) []reusableCall {
	var calls []reusableCall
	for _, ref := range scanUses(file, body) {
		spec, _, _ := strings.Cut(ref.Uses, "@")
		if !strings.Contains(strings.ToLower(spec), "/.github/workflows/") {
			continue
		}
		calls = append(calls, reusableCall{Reusable: spec[strings.LastIndex(spec, "/")+1:], Ref: ref.Ref})
	}
	return calls
}

// callerGrants returns the union of every `scope: level` pair granted by any
// permissions block in the file — block form, flow form (`{contents: read}`)
// and the `read-all` / `write-all` scalars, which come back under the pseudo
// scope "*". The boolean reports whether any permissions key was seen at all,
// because "no block" and "a block granting nothing" are different verdicts:
// the first hands the decision to the repository's default token, the second
// is an explicit downgrade the startup gate will enforce.
//
// Comments and block scalars are skipped with the same state scanUses carries,
// and for the same incident: a fleet-sync heredoc that WRITES a caller stub
// contains a permissions block that is data, not a grant.
func callerGrants(body string) (map[string]string, bool) {
	grants := map[string]string{}
	seen := false
	block := -1      // indent of the key that opened a block scalar, or -1
	permIndent := -1 // indent of an open permissions: mapping, or -1
	for line := range strings.SplitSeq(body, "\n") {
		if block >= 0 {
			if strings.TrimSpace(line) == "" || indentOf(line) > block {
				continue
			}
			block = -1
		}
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue // a blank or comment line closes nothing in YAML
		}
		if indent, opens := blockScalarKey(line); opens {
			block = indent
			permIndent = -1
			continue
		}
		indent := indentOf(line)
		if permIndent >= 0 {
			if indent > permIndent {
				recordGrant(grants, trimmed)
				continue
			}
			permIndent = -1
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || key != "permissions" {
			continue
		}
		seen = true
		fields := strings.Fields(value)
		switch {
		case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
			permIndent = indent // block form: the grants are the indented lines
		case strings.HasPrefix(fields[0], "{"):
			flow := strings.TrimSpace(value)
			flow = strings.TrimPrefix(flow, "{")
			if i := strings.Index(flow, "}"); i >= 0 {
				flow = flow[:i]
			}
			for pair := range strings.SplitSeq(flow, ",") {
				recordGrant(grants, strings.TrimSpace(pair))
			}
		case fields[0] == "read-all":
			record(grants, "*", "read")
		case fields[0] == "write-all":
			record(grants, "*", "write")
		}
	}
	return grants, seen
}

// recordGrant parses one `scope: level` line (trailing comment tolerated) into
// the grant set. A line that is not that shape grants nothing.
func recordGrant(grants map[string]string, pair string) {
	scope, level, found := strings.Cut(pair, ":")
	if !found || scope == "" || strings.ContainsAny(scope, " \t{") {
		return
	}
	fields := strings.Fields(level)
	if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
		return
	}
	record(grants, scope, strings.Trim(fields[0], `"'`))
}

// record keeps the strongest level seen for a scope — two blocks in one file
// (workflow level and a job's) union in the caller's favour.
func record(grants map[string]string, scope, level string) {
	if level != "read" && level != "write" {
		return // `none` and typos grant nothing
	}
	if grants[scope] == "write" {
		return
	}
	grants[scope] = level
}

// satisfies reports whether the collected grants cover one need. write covers
// read (GitHub's levels nest), and the read-all/write-all scalars cover every
// scope at their level.
func satisfies(grants map[string]string, scope, need string) bool {
	for _, got := range []string{grants[scope], grants["*"]} {
		if got == "write" || got == need {
			return true
		}
	}
	return false
}
