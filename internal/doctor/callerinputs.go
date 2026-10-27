package doctor

import (
	"fmt"
	"sort"
	"strings"
)

// The second member of the failure class checkCallerPermissions exists for: a
// caller omitting an input its reusable marks `required: true` also dies as
// startup_failure before any job, and GitHub surfaces NO error anywhere for it
// (DESIGN §7, measured on glyph-test3). This check reads the caller's `with:`
// block against what the reusable requires at the caller's pin (callerpin.go).

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
func checkCallerInputs(root string, rootVerified bool, workflows []workflowFile, workflowsErr error, glyph string) Check {
	return judgeCallers(root, rootVerified, workflows, workflowsErr, callerSpec[inputRow]{
		id:          idCallerInputs,
		expected:    inputsExpected(glyph),
		consequence: "A caller omitting a required input dies as startup_failure before any job — unverified here, not verified",
		glyph:       glyph,
		floor:       inputsFloor,
		rows:        reusableRequiredInputs,
		file: func(f workflowFile) func(reusableCall, inputRow) []string {
			lines := strings.Split(f.Body, "\n")
			return func(call reusableCall, row inputRow) []string {
				var missing []string
				keys := jobWithKeys(lines, call.Line)
				for _, want := range row.Required {
					if !keys[want] {
						missing = append(missing, fmt.Sprintf(
							".github/workflows/%s calls %s@%s but its with: never names %s", f.Name, call.Reusable, call.Ref, want))
					}
				}
				return missing
			}
		},
		failObserved: "%d missing required input(s) across the %d workflow file(s) that call a glyph reusable",
		failMessage: "GitHub kills a reusable call that omits a required input as startup_failure before any job — and " +
			"it surfaces no error anywhere: not in the run, not in the check suite, not in the API (measured, " +
			"glyph-test3 2026-08-26). This static read is the only check that can see it coming",
		failFix:      "add the missing input to the caller's with: block — the commented stub in each reusable's header is the known-good copy",
		death:        "whether every caller gets past GitHub's silent startup input gate",
		noCaller:     "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to starve)",
		passObserved: "%d workflow file(s) call glyph reusables; every judged caller passes what its pinned release requires",
		passMessage:  "these runs get past GitHub's startup input gate",
	})
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
// The forward walk reads only contentLines: an install-notes heredoc's content
// must not read as keys.
func jobWithKeys(lines []string, usesLine int) map[string]bool {
	keys := map[string]bool{}
	if usesLine < 1 || usesLine > len(lines) {
		return keys
	}
	jobIndent := indentOf(lines[usesLine-1])
	start := usesLine - 1
	for i := usesLine - 2; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(strings.TrimLeft(lines[i], " \t"), "#") {
			continue // blank as contentLines means it: a CRLF file's lone `\r` included
		}
		if indentOf(lines[i]) < jobIndent {
			start = i + 1
			break
		}
		start = i
	}
	inWith := false
	withKeyIndent := -1 // indent of the keys directly inside with:, learned from the first one
	for _, line := range contentLines(lines, start) {
		trimmed := strings.TrimLeft(line, " \t")
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
		if k, _, found := strings.Cut(trimmed, ":"); found {
			keys[strings.TrimSpace(k)] = true
		}
	}
	return keys
}
