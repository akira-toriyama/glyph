package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// callerWith renders a minimal workflow calling one glyph reusable, with the
// given with: block spliced in verbatim under the job ("" omits it).
func callerWith(reusable, with string) string {
	b := "name: caller\non:\n  pull_request:\njobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/" + reusable + "@v1.2.3\n"
	if with != "" {
		b += with + "\n"
	}
	return b
}

// TestReusableRequiredInputsMatchTheShippedWorkflows holds the requirements
// table to the reusables it describes, exactly as the permissions table is
// held: a reusable growing a `required: true` input without a row here would
// ship a check that blesses callers GitHub kills at startup, and this turns
// that into a red diff instead. The shipped file is parsed with the same
// crude discipline the check itself lives by — a `required: true` line inside
// the workflow_call inputs block belongs to the nearest input key above it.
func TestReusableRequiredInputsMatchTheShippedWorkflows(t *testing.T) {
	for reusable, want := range reusableRequiredInputs {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", reusable))
		if err != nil {
			t.Fatalf("reading the shipped reusable: %v", err)
		}
		got := shippedRequiredInputs(t, string(body))
		wantSet := map[string]bool{}
		for _, w := range want {
			wantSet[w] = true
		}
		if len(got) != len(wantSet) {
			t.Fatalf("%s requires %v but reusableRequiredInputs says %v", reusable, got, want)
		}
		for name := range got {
			if !wantSet[name] {
				t.Errorf("%s marks %q required but the table does not list it — the check now blesses callers GitHub kills", reusable, name)
			}
		}
	}
}

// shippedRequiredInputs extracts the required input names from a reusable's
// workflow_call inputs block.
func shippedRequiredInputs(t *testing.T, body string) map[string]bool {
	t.Helper()
	required := map[string]bool{}
	inInputs := false
	inputsIndent := -1
	current := ""
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		ind := indentOf(line)
		if trimmed == "inputs:" && !inInputs {
			inInputs = true
			inputsIndent = ind
			continue
		}
		if !inInputs {
			continue
		}
		if ind <= inputsIndent {
			break // outputs:, secrets:, or the next top-level key — inputs is done
		}
		if ind == inputsIndent+2 && strings.HasSuffix(trimmed, ":") {
			current = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if trimmed == "required: true" && current != "" {
			required[current] = true
		}
	}
	return required
}

// TestCallerInputsOutcomes walks the verdicts over real files: the measured
// silent startup_failure (a release caller omitting install-notes,
// glyph-test3 2026-08-26), the healthy shapes in both YAML orders and both
// with: forms, and the not-a-caller cases.
func TestCallerInputsOutcomes(t *testing.T) {
	blockWith := "    with:\n      install-notes: |\n        ## Install\n        nothing: to install\n"
	tests := []struct {
		name       string
		files      map[string]string
		want       Status
		wantDetail string
	}{
		{
			name:  "the measured incident: a release caller with no with: at all",
			files: map[string]string{"release.yml": callerWith("release.yml", "")},
			want:  StatusFail, wantDetail: "install-notes",
		},
		{
			name: "a with: block that names other keys but not the required one",
			files: map[string]string{"release.yml": callerWith("release.yml",
				"    with:\n      dry-run: true\n")},
			want: StatusFail, wantDetail: "install-notes",
		},
		{
			name:  "install-notes passed as a block scalar",
			files: map[string]string{"release.yml": callerWith("release.yml", blockWith)},
			want:  StatusPass,
		},
		{
			name: "with: written ABOVE its uses: — YAML orders siblings freely",
			files: map[string]string{"release.yml": "name: caller\non:\n  push:\njobs:\n  j:\n" +
				blockWith + "    uses: akira-toriyama/glyph/.github/workflows/release.yml@v1.2.3\n"},
			want: StatusPass,
		},
		{
			name: "flow-form with: { install-notes: x }",
			files: map[string]string{"release.yml": callerWith("release.yml",
				"    with: { install-notes: nothing, dry-run: false }")},
			want: StatusPass,
		},
		{
			name:  "a lint caller needs no inputs",
			files: map[string]string{"commit-lint.yml": callerWith("lint.yml", "")},
			want:  StatusPass,
		},
		{
			name:  "no glyph caller at all",
			files: map[string]string{"ci.yml": "name: ci\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"},
			want:  StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkCallerInputs(checkoutWith(t, tt.files), true)
			if c.Status != tt.want {
				t.Fatalf("status = %s, want %s (observed %q, details %v)", c.Status, tt.want, c.Observed, c.Details)
			}
			if tt.wantDetail != "" {
				joined := strings.Join(c.Details, "\n")
				if !strings.Contains(joined, tt.wantDetail) {
					t.Errorf("details %v never name %q", c.Details, tt.wantDetail)
				}
			}
		})
	}
}

// TestAbsentWorkflowsDirSplitsOnRootProvenance pins the t-wzsw decision for
// all three local caller-side checks at once: under a git-named root an
// absent .github/workflows is an observed absence (pass — a fresh repository
// can reach doctor exit 0 before wiring CI), while under a bare "." it stays
// unknown, because a wrong working directory reads exactly the same.
func TestAbsentWorkflowsDirSplitsOnRootProvenance(t *testing.T) {
	checks := map[string]func(string, bool) Check{
		"workflow-glyph-pins":         checkWorkflowPins,
		"workflow-caller-permissions": checkCallerPermissions,
		"workflow-caller-inputs":      checkCallerInputs,
	}
	for name, fn := range checks {
		t.Run(name, func(t *testing.T) {
			if c := fn(t.TempDir(), true); c.Status != StatusPass {
				t.Errorf("verified root, absent dir: status = %s, want %s (observed %q)", c.Status, StatusPass, c.Observed)
			}
			if c := fn(t.TempDir(), false); c.Status != StatusUnknown {
				t.Errorf("unverified root, absent dir: status = %s, want %s (observed %q)", c.Status, StatusUnknown, c.Observed)
			}
		})
	}
}
