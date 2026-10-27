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
	b := "name: caller\non:\n  pull_request:\njobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/" + reusable + "@v4.2.0\n"
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
	for reusable, row := range reusableRequiredInputs {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", reusable))
		if err != nil {
			t.Fatalf("reading the shipped reusable: %v", err)
		}
		got := shippedRequiredInputs(t, string(body))
		wantSet := map[string]bool{}
		for _, w := range row.Required {
			wantSet[w] = true
		}
		if len(got) != len(wantSet) {
			t.Fatalf("%s requires %v but reusableRequiredInputs says %v — update the row, and move its After to the newest "+
				"release tag: every release so far required otherwise", reusable, got, row.Required)
		}
		for name := range got {
			if !wantSet[name] {
				t.Errorf("%s marks %q required but the table does not list it — the check now blesses callers GitHub kills. "+
					"Add it, and move the row's After to the newest release tag", reusable, name)
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
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "")},
			want:  StatusFail, wantDetail: "install-notes",
		},
		{
			name: "a with: block that names other keys but not the required one",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      dry-run: true\n")},
			want: StatusFail, wantDetail: "install-notes",
		},
		{
			name:  "install-notes passed as a block scalar",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", blockWith)},
			want:  StatusPass,
		},
		{
			name: "with: written ABOVE its uses: — YAML orders siblings freely",
			files: map[string]string{".github/workflows/release.yml": "name: caller\non:\n  push:\njobs:\n  j:\n" +
				blockWith + "    uses: akira-toriyama/glyph/.github/workflows/release.yml@v4.2.0\n"},
			want: StatusPass,
		},
		{
			name: "flow-form with: { install-notes: x }",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: { install-notes: nothing, dry-run: false }")},
			want: StatusPass,
		},
		{
			name:  "a lint caller needs no inputs",
			files: map[string]string{".github/workflows/commit-lint.yml": callerWith("lint.yml", "")},
			want:  StatusPass,
		},
		{
			name:  "no glyph caller at all",
			files: map[string]string{".github/workflows/ci.yml": "name: ci\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"},
			want:  StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkCallerInputs(checkoutAt(t, tt.files), true, "dev")
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
		"workflow-glyph-pins":         func(r string, v bool) Check { return checkWorkflowPins(r, v, nil, nil) },
		"workflow-caller-permissions": func(r string, v bool) Check { return checkCallerPermissions(r, v, "dev") },
		"workflow-caller-inputs":      func(r string, v bool) Check { return checkCallerInputs(r, v, "dev") },
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

// TestUnlistableWorkflowsDirRemedyNamesTheCause holds the could-not-run arms
// of a workflows listing to the remedy of their cause. Without a git-named top
// level the remedy is a checkout — and only that: outside one doctor does not
// start without --repo, so the flag is no advice. Under one, doctor already
// reads that root from any subdirectory, so "the repository root" names a
// cause that is not there: a listing refused for permissions is the
// directory's permissions, and anything else at the path (a regular file
// here) must become a listable directory — the split checkConfig draws for
// glyph.toml.
func TestUnlistableWorkflowsDirRemedyNamesTheCause(t *testing.T) {
	const outside = "re-run from inside a git checkout of the repository being diagnosed"
	const refused = "fix the directory's permissions and re-run"
	checks := map[string]func(string, bool) Check{
		idWorkflowPinned: func(r string, v bool) Check { return checkWorkflowPins(r, v, nil, nil) },
		idCallerPerms:    func(r string, v bool) Check { return checkCallerPermissions(r, v, "dev") },
		idCallerInputs:   func(r string, v bool) Check { return checkCallerInputs(r, v, "dev") },
	}
	for id, fn := range checks {
		t.Run(id, func(t *testing.T) {
			check := func(arm string, c Check, fix string) {
				t.Helper()
				if c.Status != StatusUnknown || c.Fix != fix {
					t.Errorf("%s: status %s, fix %q — want %s and %q", arm, c.Status, c.Fix, StatusUnknown, fix)
				}
				if s := c.Message + " " + c.Fix; strings.Contains(s, "repository root") {
					t.Errorf("%s: %q sends the user to the repository root, which doctor finds from any subdirectory", arm, s)
				}
			}

			check("no git-named root", fn(t.TempDir(), false), outside)

			root := checkoutAt(t, map[string]string{".github/workflows": "not a directory\n"})
			check("a regular file at the workflows path", fn(root, true),
				"make "+filepath.Join(root, ".github", "workflows")+" a directory that can be listed, then re-run")

			root = checkoutAt(t, map[string]string{".github/workflows/": ""})
			lockDir(t, filepath.Join(root, ".github", "workflows"))
			check("an unlistable directory under a git-named root", fn(root, true), refused)
		})
	}
}

// lockDir makes dir unlistable, skipping the test where mode 000 does not stop
// a listing (root, or a filesystem that ignores modes).
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this filesystem (or a root-equivalent uid) ignores mode 000, so an unlistable directory cannot be staged")
	}
}
