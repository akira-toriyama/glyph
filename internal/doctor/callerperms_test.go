package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caller renders a minimal workflow that calls one glyph reusable with the
// given permissions block spliced in verbatim ("" omits the block entirely —
// which is not the same as an empty one, and the check treats them
// differently on purpose).
func caller(reusable, permissions string) string {
	b := "name: caller\non:\n  pull_request:\n"
	if permissions != "" {
		b += permissions + "\n"
	}
	return b + "jobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/" + reusable + "@v1.2.3\n"
}

// TestReusableNeedsMatchTheShippedWorkflows holds the requirements table to
// the reusables it describes. The table says what a CALLER must grant; the
// authority on that is what each shipped workflow actually declares (workflow
// level plus job-level elevation — GitHub's startup gate enforces the union).
// A grant added to a reusable without a row here would ship a check that
// blesses callers the new grant breaks; this test turns that into a red diff
// instead. callerGrants is deliberately the reader on both sides, so the
// comparison cannot drift from the parser the check itself uses.
func TestReusableNeedsMatchTheShippedWorkflows(t *testing.T) {
	for reusable, needs := range reusableNeeds {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", reusable))
		if err != nil {
			t.Fatalf("reading the shipped reusable: %v", err)
		}
		declared, seen := callerGrants(string(body))
		if !seen {
			t.Fatalf("%s declares no permissions at all — the table row is fiction", reusable)
		}
		want := map[string]string{}
		for _, n := range needs {
			want[n.Scope] = n.Level
		}
		if fmt.Sprint(declared) != fmt.Sprint(want) {
			t.Errorf("%s declares %v but reusableNeeds says %v — the check now blesses callers GitHub refuses (or reds ones it accepts)",
				reusable, declared, want)
		}
	}
}

// TestCallerPermissionsOutcomes walks the verdicts over real files: the
// measured startup_failure shape (a lint caller granting only contents:
// read, .github#186), each reusable's own requirement, the deliberate
// not-judged case, and the grant spellings GitHub accepts.
func TestCallerPermissionsOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		want       Status
		wantDetail string // substring of one Details row when failing
	}{
		{
			name:  "the measured incident: a lint caller granting only contents: read",
			files: map[string]string{"commit-lint.yml": caller("lint.yml", "permissions:\n  contents: read")},
			want:  StatusFail, wantDetail: "pull-requests: read",
		},
		{
			name:  "the distributed lint stub passes",
			files: map[string]string{"commit-lint.yml": caller("lint.yml", "permissions:\n  contents: read\n  pull-requests: read")},
			want:  StatusPass,
		},
		{
			name:  "a release caller must grant contents: write, read is the same death",
			files: map[string]string{"release.yml": caller("release.yml", "permissions:\n  contents: read")},
			want:  StatusFail, wantDetail: "contents: write",
		},
		{
			name:  "a pr-verdict caller granting pull-requests: read is short of the comment write",
			files: map[string]string{"version-preview.yml": caller("pr-verdict.yml", "permissions:\n  contents: read\n  pull-requests: read")},
			want:  StatusFail, wantDetail: "pull-requests: write",
		},
		{
			name:  "no permissions block is not judged — the repository default decides, and this file cannot see it",
			files: map[string]string{"commit-lint.yml": caller("lint.yml", "")},
			want:  StatusPass,
		},
		{
			name:  "write covers read, and a flow mapping is a block",
			files: map[string]string{"release.yml": caller("release.yml", "permissions: { contents: write, pull-requests: read }")},
			want:  StatusPass,
		},
		{
			name:  "read-all covers every read but no write",
			files: map[string]string{"release.yml": caller("release.yml", "permissions: read-all")},
			want:  StatusFail, wantDetail: "contents: write",
		},
		{
			name:  "a job-level grant counts — GitHub accepts it, so redding it would cry wolf",
			files: map[string]string{"commit-lint.yml": "name: c\non:\n  pull_request:\njobs:\n  j:\n    permissions:\n      contents: read\n      pull-requests: read\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v1.2.3\n"},
			want:  StatusPass,
		},
		{
			name:  "a heredoc that WRITES a permissions block grants nothing — the fleet-sync trap, again",
			files: map[string]string{"fleet-sync.yml": "name: f\non:\n  pull_request:\npermissions:\n  contents: read\njobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v1.2.3\n  w:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          cat > stub.yml <<'YAML'\n          permissions:\n            pull-requests: read\n          YAML\n"},
			want:  StatusFail, wantDetail: "pull-requests: read",
		},
		{
			name:  "a checkout with no glyph reusable caller has nothing to judge",
			files: map[string]string{"ci.yml": "name: ci\non:\n  pull_request:\npermissions:\n  contents: read\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: \"true\"\n"},
			want:  StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkCallerPermissions(checkoutWith(t, tt.files))
			if c.Status != tt.want {
				t.Fatalf("status = %s, want %s\nobserved: %s\ndetails: %v", c.Status, tt.want, c.Observed, c.Details)
			}
			if tt.wantDetail != "" {
				found := false
				for _, d := range c.Details {
					if strings.Contains(d, tt.wantDetail) {
						found = true
					}
				}
				if !found {
					t.Errorf("no detail names the missing grant %q: %v", tt.wantDetail, c.Details)
				}
			}
			if tt.want == StatusFail && !strings.Contains(c.Message, "startup_failure") {
				t.Errorf("the failure must name the startup death the caller will hit, got %q", c.Message)
			}
		})
	}
}

// TestCallerPermissionsWithoutADirectoryIsUnknown mirrors the pin check's
// contract: an unlistable workflows directory observed nothing, and "we could
// not check" is not "it is fine".
func TestCallerPermissionsWithoutADirectoryIsUnknown(t *testing.T) {
	c := checkCallerPermissions(t.TempDir())
	if c.Status != StatusUnknown {
		t.Fatalf("status = %s, want %s", c.Status, StatusUnknown)
	}
}
