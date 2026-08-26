package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This check exists for the one failure class NO runtime diagnosis can see —
// glyph's included. A caller workflow granting less than the reusable it calls
// declares never starts: the run dies as startup_failure before any job, so
// there is no step to print an error, no exit code to classify, and nothing
// red in the caller's own YAML. Measured in akira-toriyama/.github#186 (a
// commit-lint caller granting only contents: read) and recorded in the
// distributed caller stub; the v2.0.0 rollout fixed all 35 fleet repos at the
// canonical source, but nothing has guarded a consumer outside the fleet, or a
// hand edit since. Reading the file is the only vantage point that works,
// which is doctor's.

// reusableNeeds is what each glyph reusable DECLARES — workflow level plus any
// job-level elevation — and therefore the minimum a caller must grant. The
// values mirror the permissions blocks in this repo's own workflow files and
// the caller stubs those files distribute; TestReusableNeedsMatchTheShippedWorkflows
// holds the two in lockstep, so a grant added to a reusable without a row here
// fails a test instead of shipping a check that blesses broken callers.
var reusableNeeds = map[string][]permNeed{
	"lint.yml":       {{"contents", "read"}, {"pull-requests", "read"}},
	"release.yml":    {{"contents", "write"}},
	"pr-verdict.yml": {{"contents", "read"}, {"pull-requests", "write"}},
}

// permNeed is one scope a reusable declares, at the level it declares it.
type permNeed struct {
	Scope string
	Level string // "read" or "write"
}

// checkCallerPermissions scans the local checkout's workflow files for callers
// of glyph's reusable workflows and verifies that each caller's explicit
// `permissions:` grants cover what the pinned reusable declares.
//
// Two deliberate boundaries, both on the side of never crying wolf:
//
//   - A caller with NO permissions block anywhere is not judged. GitHub then
//     applies the repository's default token, which may be permissive (fine)
//     or restricted (the same startup death) — but which one is repository
//     configuration this file cannot see, and a red over a caller that may be
//     perfectly healthy teaches the fleet to ignore the report.
//   - Grants are unioned across every permissions block in the file, workflow
//     level and job level alike. GitHub only counts a grant on the calling
//     job or above, so a grant on a sibling job could in principle satisfy
//     this check while the run still dies — accepted, because the opposite
//     reading (workflow level only) reds every caller that grants on the job,
//     which is legal and real. A false pass here is the pin check's own
//     documented trade, made for the same reason.
func checkCallerPermissions(root string) Check {
	c := Check{
		ID: IDCallerPerms,
		Expected: "every workflow calling a glyph reusable grants at least what that reusable declares " +
			"(lint: contents: read, pull-requests: read; release: contents: write; " +
			"pr-verdict: contents: read, pull-requests: write)",
	}
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("%s could not be listed: %v", dir, err)
		c.Message = "doctor reads the LOCAL checkout for this check, so it must run from the repository root. " +
			"A caller granting less than its reusable declares dies as startup_failure before any job — " +
			"unverified here, not verified"
		c.Fix = "re-run from the repository root (cd into the checkout)"
		return c
	}

	callers := 0
	var findings []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(dir, name)
		body, rerr := os.ReadFile(path) // #nosec G304 -- the caller's own checkout, listed above
		if rerr != nil {
			// The pin check already reports unreadable files; a second copy of
			// the same finding would double every remediation list.
			continue
		}
		needs := reusablesCalled(name, string(body))
		if len(needs) == 0 {
			continue
		}
		callers++
		grants, declared := callerGrants(string(body))
		if !declared {
			// No explicit block: the repository's default token decides, and
			// this file cannot see that setting. Not judged — see the header.
			continue
		}
		for _, n := range needs {
			if !satisfies(grants, n.Scope, n.Level) {
				findings = append(findings, fmt.Sprintf(
					".github/workflows/%s calls %s but its permissions never grant %s: %s",
					name, n.Reusable, n.Scope, n.Level))
			}
		}
	}
	sort.Strings(findings)

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d missing grant(s) across the %d workflow file(s) that call a glyph reusable", len(findings), callers)
		c.Details = findings
		c.Message = "a reusable can only downgrade the caller's token, never raise it, so a caller granting less than " +
			"the reusable declares never starts: the run dies as startup_failure before any job (measured in " +
			"akira-toriyama/.github#186) — no step runs, nothing prints, and no runtime diagnosis can see it. " +
			"This static read is the only check that can"
		c.Fix = "add the missing grant to the caller's permissions block — the commented stub in each reusable's " +
			"header is the known-good copy"
		return c
	}
	c.Status = StatusPass
	if callers == 0 {
		c.Observed = "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to misgrant)"
		c.Message = "nothing to judge, observed — not assumed"
		return c
	}
	c.Observed = fmt.Sprintf("%d workflow file(s) call glyph reusables; every explicit permissions block covers what the pinned reusable declares", callers)
	c.Message = "these runs get past GitHub's startup permission gate"
	return c
}

// calledNeed is one reusable a workflow file calls, with one scope it must
// therefore grant.
type calledNeed struct {
	Reusable string
	Scope    string
	Level    string
}

// reusablesCalled returns the permission needs implied by every glyph reusable
// this workflow file executes. It rides on scanUses — the same comment,
// block-scalar and list-dash discipline, for the same traps — and keeps only
// references into .github/workflows/, because an action reference (the
// install) declares nothing the caller must match.
func reusablesCalled(file, body string) []calledNeed {
	var needs []calledNeed
	for _, ref := range scanUses(file, body) {
		spec, _, _ := strings.Cut(ref.Uses, "@")
		base := spec[strings.LastIndex(spec, "/")+1:]
		if !strings.Contains(strings.ToLower(spec), "/.github/workflows/") {
			continue
		}
		for _, n := range reusableNeeds[strings.ToLower(base)] {
			needs = append(needs, calledNeed{Reusable: base, Scope: n.Scope, Level: n.Level})
		}
	}
	return needs
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
