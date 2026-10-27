package doctor

import (
	"fmt"
	"sort"
	"strings"
)

// This check exists for a failure class NO runtime diagnosis can see —
// glyph's included (checkCallerInputs guards the other member: a missing
// required input). A caller workflow granting less than the reusable it calls
// declares never starts: the run dies as startup_failure before any job
// (DESIGN §7, measured in akira-toriyama/.github#186). It judges a caller at
// the release it pins, and only where this binary can speak for that release
// (callerpin.go).

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
// Two deliberate boundaries, both on the side of never crying wolf (DESIGN §7):
//
//   - A caller with NO permissions block anywhere is not judged: the
//     repository's default token then decides, and this file cannot see it.
//     This runs BEFORE the pin rule, so a block-less caller at an old pin is
//     the exemption's pass, never unknown.
//   - Grants are unioned across every permissions block in the file, workflow
//     level and job level alike. GitHub only counts a grant on the calling
//     job or above, so a grant on a sibling job could in principle satisfy
//     this check while the run still dies — accepted, because the opposite
//     reading (workflow level only) reds every caller that grants on the job,
//     which is legal and real.
func checkCallerPermissions(root string, rootVerified bool, workflows []workflowFile, workflowsErr error, glyph string) Check {
	return judgeCallers(root, rootVerified, workflows, workflowsErr, callerSpec[permRow]{
		id:       idCallerPerms,
		expected: permsExpected(glyph),
		consequence: "A caller granting less than its reusable declares dies as startup_failure before any job — " +
			"unverified here, not verified",
		glyph: glyph,
		floor: permsFloor,
		rows:  reusableNeeds,
		file: func(f workflowFile) func(reusableCall, permRow) []string {
			grants, declared := callerGrants(f.Body)
			if !declared {
				// No explicit block: the repository's default token decides, and
				// this file cannot see that setting. Not judged — see the header.
				return nil
			}
			return func(call reusableCall, row permRow) []string {
				var missing []string
				for _, n := range row.Needs {
					if !satisfies(grants, n.Scope, n.Level) {
						missing = append(missing, fmt.Sprintf(
							".github/workflows/%s calls %s@%s but its permissions never grant %s: %s",
							f.Name, call.Reusable, call.Ref, n.Scope, n.Level))
					}
				}
				return missing
			}
		},
		failObserved: "%d missing grant(s) across the %d workflow file(s) that call a glyph reusable",
		failMessage: "a reusable can only downgrade the caller's token, never raise it, so a caller granting less than " +
			"the reusable declares never starts: the run dies as startup_failure before any job (measured in " +
			"akira-toriyama/.github#186) — no step runs, nothing prints, and no runtime diagnosis can see it. " +
			"This static read is the only check that can",
		failFix: "add the missing grant to the caller's permissions block — the commented stub in each reusable's " +
			"header is the known-good copy",
		death:        "whether every caller gets past GitHub's startup permission gate",
		noCaller:     "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to misgrant)",
		passObserved: "%d workflow file(s) call glyph reusables; every judged caller covers what its pinned release declares",
		passMessage:  "these runs get past GitHub's startup permission gate",
	})
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

// reusableCall is one call a workflow file makes into glyph's
// .github/workflows, with the ref it pins and the line its `uses:` sits on.
type reusableCall struct {
	Reusable string // the file name as the caller spelled it
	Ref      string
	Line     int // 1-indexed, the job's `uses:` line (jobWithKeys reads its block from here)
}

// reusablesCalled returns every glyph reusable this workflow file executes,
// with its pin. It rides on scanUses — the same comment, block-scalar and
// list-dash discipline, for the same traps — and keeps only references into
// .github/workflows/, because an action reference (the install) declares
// nothing the caller must match. The pin is kept: GitHub starts the caller
// against the reusable at that ref, and judgeAt decides whether this binary
// can speak for it. It is the ONE answer to "which uses: calls a glyph
// reusable" — both caller checks read it.
func reusablesCalled(file, body string) []reusableCall {
	var calls []reusableCall
	for _, ref := range scanUses(file, body) {
		spec, _, _ := strings.Cut(ref.Uses, "@")
		if !strings.Contains(strings.ToLower(spec), "/.github/workflows/") {
			continue
		}
		calls = append(calls, reusableCall{Reusable: spec[strings.LastIndex(spec, "/")+1:], Ref: ref.Ref, Line: ref.Line})
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
// Only contentLines are read, for the scanUses incident: a fleet-sync heredoc
// that WRITES a caller stub contains a permissions block that is data, not a
// grant.
func callerGrants(body string) (map[string]string, bool) {
	grants := map[string]string{}
	seen := false
	permIndent := -1 // indent of an open permissions: mapping, or -1
	for _, line := range contentLines(strings.Split(body, "\n"), 0) {
		trimmed := strings.TrimLeft(line, " \t")
		indent := indentOf(line)
		if permIndent >= 0 {
			if indent > permIndent {
				recordGrant(grants, trimmed)
				continue
			}
			permIndent = -1
		}
		if _, opens := blockScalarKey(line); opens {
			// contentLines yields the key line of a block scalar, and a
			// `permissions: >-` over `read-all` is a scalar this reader does
			// not parse: read as a declared block granting nothing, it would
			// fail a lint caller GitHub starts.
			continue
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
