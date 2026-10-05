package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The second member of the failure class checkCallerPermissions exists for: a
// caller omitting an input its reusable marks `required: true` also dies as
// startup_failure before any job — and unlike the permissions death, GitHub
// surfaces NO error anywhere for it: not in the run, not in the check suite,
// not in the API (measured on glyph-test3, 2026-08-26, t-wzsw — three pushes,
// three silent startup_failures). Reading the caller's `with:` block against
// what the reusable requires at the caller's pin is again the only vantage
// point that works, which is doctor's (callerpin.go).

// reusableRequiredInputs is every input a glyph reusable marks required — the
// minimum a caller's `with:` must name — with the newest release whose
// reusable required otherwise (After; callerpin.go). Required mirrors the
// workflow_call.inputs blocks in this repo's own workflow files;
// TestReusableRequiredInputsMatchTheShippedWorkflows holds the two in
// lockstep, so a reusable growing a required input without a row here fails a
// test instead of shipping a check that blesses callers GitHub will kill, and
// TestCallerDeclarationBoundsMatchReleasedTags holds After to glyph's tags.
var reusableRequiredInputs = map[string]inputRow{
	"lint.yml":       {After: "v0.1.0"},
	"release.yml":    {After: "v0.7.0", Required: []string{"install-notes"}},
	"pr-verdict.yml": {After: "v0.3.0"},
}

// inputRow is one reusable's required inputs and the bound below which this
// binary cannot speak for them.
type inputRow struct {
	After    string
	Required []string
}

// inputsFloor is the release workflow-caller-inputs first shipped in — the
// oldest glyph whose own doctor can judge its own reusables' inputs.
const inputsFloor = "v3.1.0"

// checkCallerInputs scans the local checkout's workflow files for callers of
// glyph's reusable workflows and verifies that each caller's `with:` names
// every input the reusable requires at the release the caller pins. glyph is
// the running binary's version (judgeAt).
func checkCallerInputs(root string, rootVerified bool, glyph string) Check {
	c := Check{ID: IDCallerInputs, Expected: inputsExpected(glyph)}
	entries, unknown := listWorkflows(root, rootVerified, &c,
		"A caller omitting a required input dies as startup_failure before any job — unverified here, not verified")
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
			// Unread is unknown, never "no caller" — see checkCallerPermissions.
			unreadable = append(unreadable, fmt.Sprintf("%s could not be read: %v", path, rerr))
			continue
		}
		lines := strings.Split(string(body), "\n")
		called := false
		for _, ref := range scanUses(name, string(body)) {
			spec, _, _ := strings.Cut(ref.Uses, "@")
			if !strings.Contains(strings.ToLower(spec), "/.github/workflows/") {
				continue
			}
			base := spec[strings.LastIndex(spec, "/")+1:]
			called = true
			row, known := reusableRequiredInputs[strings.ToLower(base)]
			nj := unknownReusable(base)
			if known {
				nj = judgeAt(ref.Ref, row.After, glyph, inputsFloor)
			}
			if nj != nil {
				skipped = append(skipped, fmt.Sprintf(".github/workflows/%s calls %s@%s — not judged: %s", name, base, ref.Ref, nj.Why))
				remedies = remember(remedies, nj.Remedy)
				continue
			}
			if len(row.Required) == 0 {
				continue
			}
			keys := jobWithKeys(lines, ref.Line)
			for _, want := range row.Required {
				if !keys[want] {
					findings = append(findings, fmt.Sprintf(
						".github/workflows/%s calls %s@%s but its with: never names %s", name, base, ref.Ref, want))
				}
			}
		}
		if called {
			callers++
		}
	}
	sort.Strings(findings)

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d missing required input(s) across the %d workflow file(s) that call a glyph reusable", len(findings), callers)
		c.Details = slices.Concat(findings, skipped, unreadable)
		c.Message = "GitHub kills a reusable call that omits a required input as startup_failure before any job — and " +
			"it surfaces no error anywhere: not in the run, not in the check suite, not in the API (measured, " +
			"glyph-test3 2026-08-26). This static read is the only check that can see it coming"
		c.Fix = "add the missing input to the caller's with: block — the commented stub in each reusable's header is the known-good copy"
		return c
	}
	if unverifiedCallers(&c, skipped, remedies, unreadable, "whether every caller gets past GitHub's silent startup input gate") {
		return c
	}
	c.Status = StatusPass
	if callers == 0 {
		c.Observed = "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to starve)"
		c.Message = "nothing to judge, observed — not assumed"
		return c
	}
	c.Observed = fmt.Sprintf("%d workflow file(s) call glyph reusables; every judged caller passes what its pinned release requires", callers)
	c.Message = "these runs get past GitHub's startup input gate"
	return c
}

// inputsExpected renders the check's Expected from the table itself, so the
// text cannot drift from the rows it describes.
func inputsExpected(glyph string) string {
	names := make([]string, 0, len(reusableRequiredInputs))
	for name := range reusableRequiredInputs {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]string, 0, len(names))
	for _, name := range names {
		row := reusableRequiredInputs[name]
		required := "none"
		if len(row.Required) > 0 {
			required = strings.Join(row.Required, ", ")
		}
		rows = append(rows, fmt.Sprintf("%s after %s: %s", strings.TrimSuffix(name, ".yml"), row.After, required))
	}
	return "every workflow calling a glyph reusable passes each input that reusable marks required at the release it pins, " +
		"judged for " + judgedRange(glyph) + " (" + strings.Join(rows, "; ") + ") — any other pin is could-not-run"
}

// jobWithKeys returns the top-level keys of the `with:` mapping belonging to
// the job whose `uses:` sits on usesLine (1-indexed). The job block's bounds
// are walked out from the uses line itself — up to the job-name line (the
// first shallower-indented content line), then forward until the block closes
// — so a `with:` written above its `uses:` is still seen; YAML orders siblings
// freely and a miss in either direction would cry wolf over a healthy caller.
// Block scalars are skipped whole with the same discipline scanUses carries:
// an install-notes heredoc's content must not read as keys.
func jobWithKeys(lines []string, usesLine int) map[string]bool {
	keys := map[string]bool{}
	if usesLine < 1 || usesLine > len(lines) {
		return keys
	}
	jobIndent := indentOf(lines[usesLine-1])
	start := usesLine - 1
	for i := usesLine - 2; i >= 0; i-- {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indentOf(lines[i]) < jobIndent {
			start = i + 1
			break
		}
		start = i
	}
	inWith := false
	withKeyIndent := -1 // indent of the keys directly inside with:, learned from the first one
	block := -1         // indent of the key that opened a block scalar, or -1
	for i := start; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if block >= 0 {
			if indentOf(line) > block {
				continue
			}
			block = -1
		}
		ind := indentOf(line)
		if ind < jobIndent {
			break // the job block closed
		}
		if ind == jobIndent {
			rest, isWith := strings.CutPrefix(trimmed, "with:")
			inWith = isWith
			if isWith {
				// Flow form: with: { a: 1, b: 2 } — the keys sit on this line.
				if rest = strings.TrimSpace(rest); strings.HasPrefix(rest, "{") {
					for part := range strings.SplitSeq(strings.Trim(rest, "{} "), ",") {
						if k, _, found := strings.Cut(part, ":"); found {
							keys[strings.TrimSpace(k)] = true
						}
					}
					inWith = false
				}
			}
			continue
		}
		if !inWith {
			continue
		}
		if withKeyIndent < 0 {
			withKeyIndent = ind
		}
		if ind != withKeyIndent {
			continue // deeper content: a nested value, never a with: key
		}
		k, rest, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		keys[strings.TrimSpace(k)] = true
		if v := strings.TrimSpace(rest); strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") {
			block = ind
		}
	}
	return keys
}
