package doctor

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/github"
)

// yes/no build the *bool the merge settings decode into — a pointer precisely
// so "GitHub did not say" stays distinguishable from "the setting is off".
func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// healthyInput is the repository a fleet repo is supposed to be, in a checkout
// whose caller stub is correctly pinned.
func healthyInput(t *testing.T) Input {
	t.Helper()
	return Input{
		Repo:            "akira-toriyama/glyph",
		Root:            checkoutWith(t, map[string]string{"commit-lint.yml": commentedStub}),
		ConfigPath:      configPathWith(t, minimalConfig),
		TokenConfigured: true,
		RepoObject: github.Repo{
			FullName: "akira-toriyama/glyph", Visibility: "public",
			AllowSquashMerge: yes(), AllowMergeCommit: no(), AllowRebaseMerge: no(),
			SquashMergeCommitTitle:   wantSquashTitle,
			SquashMergeCommitMessage: wantSquashMessage,
			Permissions:              &github.RepoPermissions{Admin: true, Push: true, Pull: true},
		},
	}
}

// find returns one check by id, failing the test when it is absent — the ids
// are the report's API.
func find(t *testing.T, r *Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in the report", id)
	return Check{}
}

// TestRunHealthyRepositoryPassesEverything is the baseline the other cases move
// one field away from.
func TestRunHealthyRepositoryPassesEverything(t *testing.T) {
	r := Run(healthyInput(t))
	if !r.OK || r.Counts.Pass != len(r.Checks) {
		t.Fatalf("counts = %+v ok=%t, want every check passing; report %+v", r.Counts, r.OK, r.Checks)
	}
}

// TestUnansweredRepoReadIsCouldNotRunNotAFailure pins the line between "the API
// answered and the answer is no" and "the API never answered". A dead socket, a
// rate limit or a 5xx that outlived the retry schedule observed NOTHING about
// the repository, so the token check may not report a finding — it reports
// could-not-run, which the CLI exits 4 on. Reporting it as a failed check made
// glyph exit 3 ("this repository does not satisfy what glyph assumes about it")
// on evidence that was never collected, and 3 is what the fleet's CI wrappers
// hard-fail on.
func TestUnansweredRepoReadIsCouldNotRunNotAFailure(t *testing.T) {
	in := healthyInput(t)
	in.RepoObject = github.Repo{}
	in.RepoErr = errors.New("github: GET /repos/akira-toriyama/glyph: 403 API rate limit exceeded")

	r := Run(in)
	token := find(t, r, IDTokenAccess)
	if token.Status != StatusUnknown {
		t.Fatalf("%s = %s, want %s — nothing about the repository was observed", IDTokenAccess, token.Status, StatusUnknown)
	}
	if !strings.Contains(token.Observed, "no answer") {
		t.Errorf("the observation must say the read produced no answer, got %q", token.Observed)
	}
	if r.Counts.Fail != 0 {
		t.Errorf("counts = %+v, want 0 fail — a report cannot exit on a failure it does not count", r.Counts)
	}
	// Independence still holds: the settings degrade with it, the local check
	// does not.
	if got := find(t, r, IDSquashEnabled).Status; got != StatusUnknown {
		t.Errorf("%s = %s, want unknown", IDSquashEnabled, got)
	}
	if got := find(t, r, IDWorkflowPinned).Status; got != StatusPass {
		t.Errorf("%s = %s, want pass — the local check needs no credential", IDWorkflowPinned, got)
	}
	if r.OK {
		t.Error("ok must be false: unverified is not verified-good")
	}
}

// TestTokenWriteIsAdviceNeverAFailure pins the two decisions the write check
// is made of. First, its three verdicts: push reported → pass; a block without
// push, or no block at all → ADVICE, because a read-only credential is the
// correct configuration for a repository that never runs `glyph release`, and
// doctor must not red the fleet's read-side wiring over a command it does not
// use. Second, that advice really is advice: it may not flip Report.OK or
// count as a failure — the 403 it predicts costs a release run, not a lint
// gate, and a red doctor on every read-only repo would train the fleet to
// stop reading it.
func TestTokenWriteIsAdviceNeverAFailure(t *testing.T) {
	tests := []struct {
		name string
		perm *github.RepoPermissions
		want Status
	}{
		{"push reported", &github.RepoPermissions{Push: true, Pull: true}, StatusPass},
		{"read-only block", &github.RepoPermissions{Pull: true}, StatusAdvice},
		{"no block (anonymous read)", nil, StatusAdvice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := healthyInput(t)
			in.RepoObject.Permissions = tt.perm
			r := Run(in)
			c := find(t, r, IDTokenWrite)
			if c.Status != tt.want {
				t.Fatalf("%s = %s, want %s (observed: %s)", IDTokenWrite, c.Status, tt.want, c.Observed)
			}
			if tt.want == StatusAdvice {
				if !r.OK || r.Counts.Fail != 0 {
					t.Errorf("advice flipped the verdict: ok=%v counts=%+v — a read-only credential on a "+
						"non-release repository is correct configuration", r.OK, r.Counts)
				}
				if !strings.Contains(c.Message, "release") {
					t.Errorf("the advice must name the release-only consequence, got %q", c.Message)
				}
			}
		})
	}
}

// TestTokenWriteDegradesWithTheUnreadRepo holds the write check to the same
// independence rule as everything else fed by the repository read: no response
// means UNKNOWN — "we could not check" is not "it is fine" — never a verdict
// invented over evidence that was never collected.
func TestTokenWriteDegradesWithTheUnreadRepo(t *testing.T) {
	in := healthyInput(t)
	in.RepoObject = github.Repo{}
	in.RepoErr = errors.New("github: GET /repos/akira-toriyama/glyph: 403 API rate limit exceeded")
	c := find(t, Run(in), IDTokenWrite)
	if c.Status != StatusUnknown {
		t.Fatalf("%s = %s, want %s — nothing about write access was observed", IDTokenWrite, c.Status, StatusUnknown)
	}
}

// TestRemainingChecksDescribeMeasuredBehaviour extends the prose guard below
// to the four checks its merge-method table does not reach (t-rmng):
// token-repo-read, the two squash policy checks, and workflow-glyph-pins.
// Mention-only, deliberately: a forbid entry is a phrase that SHIPPED and was
// false, and none of these four has shipped one yet — what is pinned here is
// the load-bearing clause of each message, so a reworded claim becomes a
// review question instead of a silent drift. The one arm not covered is
// token-repo-read's 404 finding: its error shape (github's statusError) is
// unexported by design, so that message is exercised end-to-end from
// internal/cli instead.
func TestRemainingChecksDescribeMeasuredBehaviour(t *testing.T) {
	unanswered := healthyInput(t)
	unanswered.RepoObject = github.Repo{}
	unanswered.RepoErr = errors.New("github: GET /repos/akira-toriyama/glyph: 502 bad gateway")
	titleOff := healthyInput(t)
	titleOff.RepoObject.SquashMergeCommitTitle = "PR_TITLE"
	bodyOff := healthyInput(t)
	bodyOff.RepoObject.SquashMergeCommitMessage = "PR_BODY"
	movingComposite := healthyInput(t)
	movingComposite.Root = checkoutAt(t, map[string]string{
		".github/workflows/w.yml":          "jobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: \"true\"\n",
		".github/actions/setup/action.yml": "runs:\n  using: composite\n  steps:\n    - uses: akira-toriyama/glyph/.github/actions/install@main\n",
	})

	tests := []struct {
		name    string
		check   Check
		want    Status
		mention []string
	}{
		{
			name:  "an unanswered read is unverified, never a verdict",
			check: find(t, Run(unanswered), IDTokenAccess),
			want:  StatusUnknown,
			mention: []string{
				"nothing was observed about the repository", // the split the check exists for
				"NOT a verdict", // 3-vs-4 is what the fleet wrappers branch on
			},
		},
		{
			name:  "PR_TITLE is argued from the fallback the walk actually runs",
			check: find(t, Run(titleOff), IDSquashTitle),
			want:  StatusFail,
			mention: []string{
				// The dark-window mechanism, pinned live by
				// TestSinceTagNonGitmojiPRTitleCountsNoneWhenDark: the fallback
				// classifies the commit's own message, and a sigil-less subject
				// a window pattern claims counts none.
				"classifies the squash commit's own message",
				"counts none",
			},
		},
		{
			name:  "PR_BODY is argued from the offline record, not from glyph's own needs",
			check: find(t, Run(bodyOff), IDSquashMessage),
			want:  StatusFail,
			mention: []string{
				// DESIGN §4 rejected the squash body as a signal, so the cost
				// this check may claim is the record and the drift alarm — not
				// a verdict.
				"nothing in the repository itself records",
			},
		},
		{
			name:  "a moving pin is argued from the tag-derived version, not from taste",
			check: find(t, Run(movingComposite), IDWorkflowPinned),
			want:  StatusFail,
			mention: []string{
				"ONE repository at ONE tag", // the lockstep the pin preserves
				"job.workflow_ref",          // the mechanism that derives the binary version
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.check.Status != tt.want {
				t.Fatalf("status = %s, want %s (observed %q)", tt.check.Status, tt.want, tt.check.Observed)
			}
			for _, m := range tt.mention {
				if !strings.Contains(tt.check.Message, m) {
					t.Errorf("the message does not say %q — it must describe the behaviour that was measured:\n%s", m, tt.check.Message)
				}
			}
		})
	}
}

// TestMergeMethodChecksDescribeTheWalkThatExists is a regression test on the
// PROSE, and it has now earned its place twice. The first draft told the fleet
// that a rebase-merged PR's non-final commits are classified on "the walk's
// fallback path", where an unknown :code: degrades to a warning counting none;
// the walk does the opposite, and that arm is pinned below still.
//
// The second round was the same defect in the squash argument, every clause of it
// re-measured against this walk (t-nnb7): a squash-merged pull does NOT reach the
// same verdict dark as live (a multi-commit squash carries the unlinted PR title
// — minor live, patch dark), a merge-merged pull does NOT count none at full
// darkness (gitsource.Log runs without --first-parent, so its branch commits are
// in the range and classify themselves — minor either way), and an outage does
// NOT reach the fallback at all (only a 422 does; a 403/404/5xx exits 4). What
// squash actually buys is that a pull request resolves all-or-nothing.
//
// So the forbid lists are the load-bearing half: every entry is a phrase that
// SHIPPED and was false, and the walk behind each correction is pinned by a test
// in internal/cli/sincetag_test.go. The mention lists stay short on purpose —
// this is a guard against resurrecting a lie, not a golden file, so a rewording
// stays a review question instead of becoming a test failure.
func TestMergeMethodChecksDescribeTheWalkThatExists(t *testing.T) {
	permissive := healthyInput(t)
	permissive.RepoObject.AllowRebaseMerge = yes()
	squashOff := healthyInput(t)
	squashOff.RepoObject.AllowSquashMerge = no()
	mergeCommitOn := healthyInput(t)
	mergeCommitOn.RepoObject.AllowMergeCommit = yes()

	tests := []struct {
		name    string
		check   Check
		want    Status
		mention []string
		forbid  []string
	}{
		{
			name:  "rebase merging is strict, not lenient",
			check: find(t, Run(permissive), IDRebaseMerge),
			want:  StatusAdvice,
			mention: []string{
				"expands the whole pull request", // it resolves exactly, like a squash
				"covered",                        // ...and the earlier commits are skipped
			},
			forbid: []string{
				"fallback path",  // there is no fallback for a resolved PR
				"degrades to a ", // nothing degrades
			},
		},
		{
			name:  "squash on promises all-or-nothing, never an identical verdict",
			check: find(t, Run(healthyInput(t)), IDSquashEnabled),
			want:  StatusPass,
			mention: []string{
				"never half-resolved", // the guarantee squash actually gives
			},
			forbid: []string{
				// The sentence this whole round existed to delete: measured
				// minor -> patch, and minor -> none for an unlinted PR title.
				"whether or not the",
			},
		},
		{
			name:  "squash off is argued from the partial window, not from a blackout",
			check: find(t, Run(squashOff), IDSquashEnabled),
			want:  StatusFail,
			mention: []string{
				"own message",    // §4's fallback classifies the commit itself
				"PARTIAL window", // ...and THAT window is the one squash removes
			},
			forbid: []string{
				"lenient",                 // the rebase path is not lenient
				"only the last",           // and its earlier commits are not classified at all
				"neither survives",        // both survive a TOTAL blackout — measured, minor either way
				"the only one that holds", // so squash is not the only style that holds when dark
			},
		},
		{
			name:  "allowing merge commits leaves a window, so it cannot claim nothing breaks",
			check: find(t, Run(mergeCommitOn), IDMergeCommit),
			want:  StatusAdvice,
			mention: []string{
				"counts none", // the partial window the setting still costs
			},
			forbid: []string{
				// An unresolved merge point drops the whole pull request. Loud
				// (two warnings; release refuses the incomplete walk at 4) —
				// which is why this is advice — but not nothing.
				"so nothing breaks",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.check.Status != tt.want {
				t.Fatalf("status = %s, want %s", tt.check.Status, tt.want)
			}
			for _, m := range tt.mention {
				if !strings.Contains(tt.check.Message, m) {
					t.Errorf("the message does not say %q — it must describe the walk that exists:\n%s", m, tt.check.Message)
				}
			}
			for _, f := range tt.forbid {
				if strings.Contains(tt.check.Message, f) {
					t.Errorf("the message still claims %q, which the walk does not do:\n%s", f, tt.check.Message)
				}
			}
		})
	}
}

// TestUnreportedSettingIsUnknownNotFalse: the *bool encoding exists so a field
// GitHub omitted never reads as a setting that is off. A confidently wrong
// answer is the one thing a diagnostic must not produce.
func TestUnreportedSettingIsUnknownNotFalse(t *testing.T) {
	in := healthyInput(t)
	in.RepoObject.AllowSquashMerge = nil

	c := find(t, Run(in), IDSquashEnabled)
	if c.Status != StatusUnknown {
		t.Fatalf("status = %s, want %s", c.Status, StatusUnknown)
	}
	if strings.Contains(c.Observed, "false") {
		t.Errorf("an omitted field must never be described as off: %q", c.Observed)
	}
}
