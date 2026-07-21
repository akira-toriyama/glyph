package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commentedStub is THE trap, reproduced verbatim in shape: every glyph reusable
// workflow ships a permanently-stale caller stub in its header comment, and the
// repository's real caller sits below it pinned to a later tag. A scan that
// ignores the comment marker reports glyph itself as drifted forever; a scan
// that stops at the first match reads v0.9.0 and reports the wrong version for
// every repository in the fleet. Both directions are pinned below.
const commentedStub = `name: commit-lint

# Reusable caller stub — copy this into a family repo:
#
#   jobs:
#     lint:
#       uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.9.0  # pin a release tag
#
on:
  pull_request:
permissions:
  contents: read
jobs:
  lint:
    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1  # pin a release tag
`

// TestScanUsesReadsTheRealLineNotTheCommentedStub pins both halves of the trap
// at once: the stale commented v0.9.0 must not appear as a reference at all,
// and the one reference found must be the executable line's v0.10.1.
func TestScanUsesReadsTheRealLineNotTheCommentedStub(t *testing.T) {
	refs := scanUses("commit-lint.yml", commentedStub)
	if len(refs) != 1 {
		t.Fatalf("scanUses found %d reference(s), want exactly 1 (the commented stub is documentation): %+v", len(refs), refs)
	}
	if refs[0].Ref != "v0.10.1" {
		t.Errorf("read ref %q, want v0.10.1 — the commented stub's v0.9.0 must never be read as the pin", refs[0].Ref)
	}
	if refs[0].Line != 15 {
		t.Errorf("reference reported at line %d, want 15 (the executable uses:)", refs[0].Line)
	}
	if refs[0].Problem != "" {
		t.Errorf("a concrete tag must be clean, got problem %q", refs[0].Problem)
	}
}

// fleetSyncCaller is the trap one shape further out than the commented stub,
// reproduced from what akira-toriyama/.github actually ships: a step whose
// `run: |` block WRITES a caller stub into another repository. The heredoc's
// text is a `uses:` line on a moving ref, and it is data this workflow emits —
// not a workflow this repository executes. Reading it as a pin failed doctor
// (exit 3) on a repository whose every executing `uses:` was correctly pinned.
const fleetSyncCaller = `name: fleet-sync
on:
  workflow_dispatch:
jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - name: write the caller stub
        run: |
          cat > .github/workflows/commit-lint.yml <<'YAML'
          jobs:
            lint:
              uses: akira-toriyama/glyph/.github/workflows/lint.yml@main
          YAML
      - uses: akira-toriyama/glyph/.github/actions/install@v0.10.1
`

// TestScanUsesSkipsBlockScalars pins the block-scalar handling in both
// directions: nothing inside a scalar is a reference, and the scan resumes the
// moment the content dedents back to the key's column.
func TestScanUsesSkipsBlockScalars(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string // the refs expected, in order
	}{
		{
			name: "a heredoc that writes a caller stub is data, not a pin",
			body: fleetSyncCaller,
			want: []string{"v0.10.1"},
		},
		{
			name: "a dashed block key (`- run: |`) hides its content too",
			body: "jobs:\n  s:\n    steps:\n      - run: |\n          uses: akira-toriyama/glyph/.github/workflows/lint.yml@main\n",
			want: nil,
		},
		{
			name: "a folded scalar (>) is a scalar as much as a literal one",
			body: "jobs:\n  s:\n    steps:\n      - name: note\n        with: >\n          uses: akira-toriyama/glyph/.github/workflows/lint.yml@v1\n",
			want: nil,
		},
		{
			name: "chomping and indentation indicators still open a block",
			body: "run: |-\n  uses: akira-toriyama/glyph/.github/workflows/lint.yml@main\nuses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1\n",
			want: []string{"v0.10.1"},
		},
		{
			name: "a plain value that merely starts with > is not a block",
			body: "jobs:\n  s:\n    if: >0\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1\n",
			want: []string{"v0.10.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := scanUses("w.yml", tt.body)
			if len(refs) != len(tt.want) {
				t.Fatalf("scanUses found %d reference(s), want %d: %+v", len(refs), len(tt.want), refs)
			}
			for i, want := range tt.want {
				if refs[i].Ref != want {
					t.Errorf("reference %d = %q, want %q", i, refs[i].Ref, want)
				}
				if refs[i].Problem != "" {
					t.Errorf("reference %d is a release tag but reports %q", i, refs[i].Problem)
				}
			}
		})
	}
}

// TestScanUsesShapes is the grammar table: which lines are references at all,
// and which refs count as pinned.
func TestScanUsesShapes(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantRef string // "" = not a reference at all
		wantBad bool
	}{
		{"tag pin", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v1.2.3", "v1.2.3", false},
		{"list item", "  - uses: akira-toriyama/glyph/.github/actions/install@v0.10.1", "v0.10.1", false},
		{"quoted", `    uses: "akira-toriyama/glyph/.github/workflows/release.yml@v0.10.1"`, "v0.10.1", false},
		{"trailing comment", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1 # pinned", "v0.10.1", false},
		{"moving branch", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@main", "main", true},
		{"major alias", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v1", "v1", true},
		{"minor alias", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10", "v0.10", true},
		{"sha pin", "    uses: akira-toriyama/glyph/.github/actions/install@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", true},
		// A v-less tag is not a pin because it does not exist: GoReleaser cuts
		// glyph's tag space as vX.Y.Z only.
		{"v-less tag", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml@0.10.1", "0.10.1", true},
		{"no ref", "    uses: akira-toriyama/glyph/.github/workflows/lint.yml", "", true},
		// GitHub resolves owner/repo case-insensitively, so this line EXECUTES
		// the glyph reusable off a moving ref. A case-sensitive scan saw no
		// glyph reference at all and reported the repository clean (exit 0).
		{"owner cased as a human writes it", "    uses: Akira-Toriyama/Glyph/.github/workflows/lint.yml@main", "main", true},
		// Everything below is NOT a glyph reference and must be invisible.
		{"whole-line comment", "#   uses: akira-toriyama/glyph/.github/workflows/lint.yml@main", "", false},
		{"indented comment", "      # uses: akira-toriyama/glyph/.github/workflows/lint.yml@main", "", false},
		{"inline mention", "    with: # uses: akira-toriyama/glyph/.github/workflows/lint.yml@main", "", false},
		// A sibling repository whose NAME merely starts with the glyph prefix.
		// A prefix match would sweep glyph-test in and diagnose the wrong repo.
		{"sibling repo", "    uses: akira-toriyama/glyph-test/.github/workflows/lint.yml@main", "", false},
		{"other owner", "    uses: akira-toriyama/.github/.github/workflows/go-ci.yml@v2", "", false},
		{"local action", "    uses: ./.glyph-action/.github/actions/install", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := scanUses("w.yml", tt.line)
			if tt.wantRef == "" && !tt.wantBad {
				if len(refs) != 0 {
					t.Fatalf("line %q produced %+v, want no reference", tt.line, refs)
				}
				return
			}
			if len(refs) != 1 {
				t.Fatalf("line %q produced %d reference(s), want 1", tt.line, len(refs))
			}
			if refs[0].Ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", refs[0].Ref, tt.wantRef)
			}
			if bad := refs[0].Problem != ""; bad != tt.wantBad {
				t.Errorf("problem = %q, want bad=%t", refs[0].Problem, tt.wantBad)
			}
		})
	}
}

// TestPinProblemNamesTheShape: the two failing shapes need different fixes, so
// the report must not blur them into one sentence. A SHA is immutable and only
// breaks the version derivation; a moving ref changes under the caller.
func TestPinProblemNamesTheShape(t *testing.T) {
	sha := pinProblem("9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", true)
	if !strings.Contains(sha, "commit SHA") || !strings.Contains(sha, "job.workflow_ref") {
		t.Errorf("a SHA pin must be explained as a version-derivation failure, got %q", sha)
	}
	moving := pinProblem("main", true)
	if !strings.Contains(moving, "moving ref") {
		t.Errorf("a branch must be explained as a moving ref, got %q", moving)
	}
}

// checkoutWith writes workflow files into a throwaway checkout and returns its
// root — the local tree the pin check reads.
func checkoutWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// TestCheckWorkflowPinsOutcomes walks the check's four outcomes over a real
// directory: clean, drifted, nothing to check, and no directory at all.
func TestCheckWorkflowPinsOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		want       Status
		wantDetail string
	}{
		{
			name:  "a real pin under a stale commented stub",
			files: map[string]string{"commit-lint.yml": commentedStub},
			want:  StatusPass,
		},
		{
			// The other direction of the trap: the COMMENT is well-pinned and
			// the executable line is not. A scan reading the comment would call
			// this repository healthy.
			name: "a good comment over a moving real ref",
			files: map[string]string{"commit-lint.yml": "# uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1\n" +
				"jobs:\n  lint:\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@main\n"},
			want:       StatusFail,
			wantDetail: ".github/workflows/commit-lint.yml:4",
		},
		{
			// End to end: the check must not fail a repository over a `uses:`
			// its fleet-sync step writes into someone else's checkout.
			name:  "a fleet-sync step that writes a caller stub is not drift",
			files: map[string]string{"fleet-sync.yml": fleetSyncCaller},
			want:  StatusPass,
		},
		{
			name:  "the .yaml spelling is scanned too",
			files: map[string]string{"verdict.yaml": "jobs:\n  v:\n    uses: akira-toriyama/glyph/.github/workflows/pr-verdict.yml@v1\n"},
			want:  StatusFail,
		},
		{
			name:  "a repo that consumes nothing from glyph",
			files: map[string]string{"ci.yml": "jobs:\n  t:\n    uses: akira-toriyama/.github/.github/workflows/go-ci.yml@v2\n"},
			want:  StatusPass,
		},
		{
			name:  "no workflows at all",
			files: map[string]string{},
			want:  StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkWorkflowPins(checkoutWith(t, tt.files))
			if c.Status != tt.want {
				t.Fatalf("status = %s, want %s (observed %q, details %v)", c.Status, tt.want, c.Observed, c.Details)
			}
			if tt.wantDetail != "" && !strings.Contains(strings.Join(c.Details, "\n"), tt.wantDetail) {
				t.Errorf("details %v do not name %q — a finding must point at the line to edit", c.Details, tt.wantDetail)
			}
		})
	}
}

// TestCheckWorkflowPinsWithoutADirectoryIsUnknown: "no .github/workflows here"
// is ambiguous — a repository with no workflows, or doctor run from the wrong
// directory — so it must report could-not-run rather than a pass it did not
// earn.
func TestCheckWorkflowPinsWithoutADirectoryIsUnknown(t *testing.T) {
	c := checkWorkflowPins(t.TempDir())
	if c.Status != StatusUnknown {
		t.Fatalf("status = %s, want %s", c.Status, StatusUnknown)
	}
	if !strings.Contains(c.Message, "LOCAL checkout") {
		t.Errorf("the message must say doctor reads the local checkout: %q", c.Message)
	}
}
