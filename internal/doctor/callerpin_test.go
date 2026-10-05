package doctor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
)

// TestCallerChecksJudgeTheCallerAtItsPin pins the D2a ruling for both caller
// checks: GitHub starts a caller against the reusable at the caller's @ref, so
// a caller is judged only where this binary can speak for that release —
// above the row's After, and for a stamped build not above its own release —
// and every other caller is unknown, never a pass and never a fail. The first
// two rows are the two wrong verdicts measured at adfc5e1: lint.yml@v1.0.0
// granting exactly what v1.0.0 declares FAILED, and release.yml@v0.4.0
// omitting the `app` v0.4.0 required PASSED.
func TestCallerChecksJudgeTheCallerAtItsPin(t *testing.T) {
	const granted = "permissions:\n  contents: read\n  pull-requests: read"
	lintAt := func(ref, perms string) string {
		b := "name: c\non:\n  pull_request:\n"
		if perms != "" {
			b += perms + "\n"
		}
		return b + "jobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@" + ref + "\n"
	}
	tests := []struct {
		name               string
		files              map[string]string
		glyph              string
		perms, inputs      Status
		wantDetail         string
		permsFix, inputFix string
	}{
		{
			name:       "a lint caller granting exactly what v1.0.0 declares is not judged by v2.0.0's grants",
			files:      map[string]string{"c.yml": lintAt("v1.0.0", "permissions:\n  contents: read")},
			glyph:      "dev",
			perms:      StatusUnknown,
			inputs:     StatusPass, // lint's inputs bound is v0.1.0, and it requires none
			wantDetail: "v1.0.0 is at or below v1.0.0",
			permsFix:   "move the pin to a release after v1.0.0",
		},
		{
			name: "a release caller at v0.4.0 omitting the app it then required is not blessed",
			files: map[string]string{"r.yml": "name: r\non:\n  push:\npermissions:\n  contents: write\njobs:\n  r:\n" +
				"    uses: akira-toriyama/glyph/.github/workflows/release.yml@v0.4.0\n    with:\n      install-notes: x\n"},
			glyph:      "dev",
			perms:      StatusPass, // release's grants bound is v0.2.0
			inputs:     StatusUnknown,
			wantDetail: "v0.4.0 is at or below v0.7.0",
			inputFix:   "move the pin to a release after v0.7.0",
		},
		{
			name:   "a pin above every bound is judged, and short is still a fail",
			files:  map[string]string{"c.yml": lintAt("v4.2.0", "permissions:\n  contents: read")},
			glyph:  "dev",
			perms:  StatusFail,
			inputs: StatusPass,
		},
		{
			name:       "a branch pin resolves to whatever the branch holds, which doctor cannot know offline",
			files:      map[string]string{"c.yml": lintAt("main", granted)},
			glyph:      "dev",
			perms:      StatusUnknown,
			inputs:     StatusUnknown,
			wantDetail: "@main is not a release tag",
		},
		{
			name:       "a pin newer than a stamped glyph is beyond what that glyph knows",
			files:      map[string]string{"c.yml": lintAt("v4.3.0", granted)},
			glyph:      "4.2.0",
			perms:      StatusUnknown,
			inputs:     StatusUnknown,
			wantDetail: "newer than this glyph (4.2.0)",
			permsFix:   "run doctor with glyph v4.3.0 or later",
		},
		{
			// dev, a pseudo-version and a git-describe stamp have no upper end.
			name:   "the same pin under an unreleased build is judged",
			files:  map[string]string{"c.yml": lintAt("v4.3.0", granted)},
			glyph:  "v4.2.1-0.20260929061233-adfc5e185dd1",
			perms:  StatusPass,
			inputs: StatusPass,
		},
		{
			name: "a defect in a judged caller fails beside a caller nobody can judge",
			files: map[string]string{
				"old.yml": lintAt("v1.0.0", "permissions:\n  contents: read"),
				"new.yml": lintAt("v4.2.0", "permissions:\n  contents: read"),
			},
			glyph:      "dev",
			perms:      StatusFail,
			inputs:     StatusPass,
			wantDetail: "not judged",
		},
		{
			// The hole the D2a review measured: a reusable this binary has
			// no row for was skipped, and the check passed as "no workflow
			// calls a glyph reusable … observed — not assumed".
			name: "a reusable this glyph does not ship is unknown, never skipped",
			files: map[string]string{"n.yml": "name: n\non:\n  push:\npermissions:\n  contents: write\njobs:\n  n:\n" +
				"    uses: akira-toriyama/glyph/.github/workflows/notes.yml@v5.0.0\n"},
			glyph:      "4.2.0",
			perms:      StatusUnknown,
			inputs:     StatusUnknown,
			wantDetail: "ships no reusable named notes.yml",
		},
		{
			// The no-permissions-block exemption runs BEFORE the pin rule:
			// the repository default decides such a caller at any pin.
			name:   "a caller with no permissions block at an old pin is the exemption's pass",
			files:  map[string]string{"c.yml": lintAt("v1.0.0", "")},
			glyph:  "dev",
			perms:  StatusPass,
			inputs: StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := checkoutWith(t, tt.files)
			for _, run := range []struct {
				check Check
				want  Status
				fix   string
			}{
				{checkCallerPermissions(root, true, tt.glyph), tt.perms, tt.permsFix},
				{checkCallerInputs(root, true, tt.glyph), tt.inputs, tt.inputFix},
			} {
				c := run.check
				if c.Status != run.want {
					t.Errorf("%s: status = %s, want %s\nobserved: %s\ndetails: %v", c.ID, c.Status, run.want, c.Observed, c.Details)
					continue
				}
				if run.want == StatusUnknown && tt.wantDetail != "" && !strings.Contains(strings.Join(c.Details, "\n"), tt.wantDetail) {
					t.Errorf("%s: details %v do not say %q", c.ID, c.Details, tt.wantDetail)
				}
				if run.fix != "" && !strings.Contains(c.Fix, run.fix) {
					t.Errorf("%s: fix %q does not say %q", c.ID, c.Fix, run.fix)
				}
			}
		})
	}
}

// TestCallerFixNeverSendsAReaderToADoctorThatCannotJudge pins the D2a review's
// correction: "run the doctor of the release you pin" is a remedy only from the
// release the check shipped in. Below it the pinned glyph has no such check —
// v0.4.0 has no doctor at all, v1.0.0's has no caller checks — so the offer
// appears only for a pin at or above the floor.
func TestCallerFixNeverSendsAReaderToADoctorThatCannotJudge(t *testing.T) {
	below := judgeAt("v1.0.0", "v1.0.0", "dev", permsFloor)
	if below == nil || strings.Contains(below.Remedy, "run doctor with glyph") {
		t.Errorf("a pin below %s: remedy %+v must not offer the pinned release's doctor", permsFloor, below)
	}
	above := judgeAt("v4.3.0", "v4.3.0", "dev", permsFloor)
	if above == nil || !strings.Contains(above.Remedy, "run doctor with glyph v4.3.0") {
		t.Errorf("a pin at or below its bound and above %s: remedy %+v must offer the pinned release's doctor", permsFloor, above)
	}
}

// TestCallerDeclarationBoundsMatchReleasedTags holds every row's After to
// glyph's own release tags: for each reusable, the newest plain vX.Y.Z tag
// whose reusable declares otherwise than this tree's — absent, not a
// reusable (no workflow_call), or different grants / required inputs, read
// with the checks' own parsers. A commit that changes a reusable's
// permissions or required inputs fails here until it moves After to the
// newest release tag; the lockstep tests fail first and say so.
//
// It needs glyph's tags. A depth-1 checkout has none (go-ci's), and the
// mutation ledger snapshots the tree without .git, so it skips there — and
// fails under GLYPH_RELEASE_HISTORY=required, which build.yml's extras job
// and scripts/check.sh's release-history gate set on a full clone. Run it with
// -count=1: Go's test cache does not track the tags it reads.
func TestCallerDeclarationBoundsMatchReleasedTags(t *testing.T) {
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("GLYPH_RELEASE_HISTORY") == "required" {
			t.Fatalf("GLYPH_RELEASE_HISTORY=required, and glyph's release history is unavailable: "+format, args...)
		}
		t.Skipf("glyph's release history is unavailable (set GLYPH_RELEASE_HISTORY=required to fail instead): "+format, args...)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	top, err := gitRead(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		unavailable("%s is not a git checkout: %v", repo, err)
	}
	if resolved, rerr := filepath.EvalSymlinks(repo); rerr == nil {
		repo = resolved
	}
	if resolvedTop, rerr := filepath.EvalSymlinks(top); rerr != nil || resolvedTop != repo {
		unavailable("the git checkout around this package is %s, not glyph's tree %s", top, repo)
	}
	if shallow, serr := gitRead(repo, "rev-parse", "--is-shallow-repository"); serr != nil || shallow != "false" {
		unavailable("the checkout is shallow (%q, %v)", shallow, serr)
	}
	list, err := gitRead(repo, "tag", "--list", "v*")
	if err != nil {
		unavailable("git tag --list: %v", err)
	}
	type release struct {
		tag string
		v   bump.Version
	}
	var releases []release
	for tag := range strings.SplitSeq(list, "\n") {
		if isReleaseTag(tag) {
			v, _ := bump.ParseVersion(tag)
			releases = append(releases, release{tag, v})
		}
	}
	if len(releases) == 0 {
		unavailable("no vX.Y.Z tag in %s", repo)
	}
	slices.SortFunc(releases, func(a, b release) int { return a.v.Compare(b.v) })

	// newestDiffering is the bound one row's declaration implies: the newest
	// release at which declares(body) — nil for an absent file or a workflow
	// that is not a reusable — differs from want.
	newestDiffering := func(file, want string, declares func(string) string) string {
		after := ""
		for _, r := range releases {
			body, serr := gitRead(repo, "show", r.tag+":.github/workflows/"+file)
			got := ""
			if serr == nil && isReusable(body) {
				got = declares(body)
			}
			if got != want {
				after = r.tag
			}
		}
		return after
	}
	grantsOf := func(body string) string {
		declared, _ := callerGrants(body)
		return fmt.Sprint(declared)
	}
	requiredOf := func(body string) string {
		names := []string{}
		for name := range shippedRequiredInputs(t, body) {
			names = append(names, name)
		}
		slices.Sort(names)
		return fmt.Sprint(names)
	}
	for file, row := range reusableNeeds {
		if got := newestDiffering(file, fmt.Sprint(needsOf(row)), grantsOf); got != row.After {
			t.Errorf("reusableNeeds[%q].After = %q, but the newest release whose %s declares other grants is %q — "+
				"set After to it, or a caller pinned at a release this tree does not describe is judged by its grants",
				file, row.After, file, got)
		}
	}
	for file, row := range reusableRequiredInputs {
		want := slices.Clone(row.Required)
		if want == nil {
			want = []string{}
		}
		slices.Sort(want)
		if got := newestDiffering(file, fmt.Sprint(want), requiredOf); got != row.After {
			t.Errorf("reusableRequiredInputs[%q].After = %q, but the newest release whose %s requires other inputs is %q — "+
				"set After to it, or a caller pinned at a release this tree does not describe is judged by its inputs",
				file, row.After, file, got)
		}
	}
}

// isReusable reports whether a workflow body declares a workflow_call
// trigger — a file that does not is no reusable a caller could start.
func isReusable(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "workflow_call:") {
			return true
		}
	}
	return false
}

// gitRead runs one read-only git command in dir and returns its trimmed
// stdout. Test-only: internal/doctor's shipped code runs no git.
func gitRead(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- fixed binary, the test's own arguments
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
