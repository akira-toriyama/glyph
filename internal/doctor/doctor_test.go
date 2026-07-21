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
		TokenConfigured: true,
		RepoObject: github.Repo{
			FullName: "akira-toriyama/glyph", Visibility: "public",
			AllowSquashMerge: yes(), AllowMergeCommit: no(), AllowRebaseMerge: no(),
			SquashMergeCommitTitle:   wantSquashTitle,
			SquashMergeCommitMessage: wantSquashMessage,
			Permissions:              &github.RepoPermissions{Admin: true, Pull: true},
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

// TestMergeMethodChecksDescribeTheWalkThatExists is a regression test on the
// PROSE, and it earns its place: an earlier draft told the fleet that a
// rebase-merged PR's non-final commits are classified on "the walk's fallback
// path", where an unknown :code: degrades to a warning counting none. The walk
// does the opposite — GitHub names the LAST replayed commit as
// merge_commit_sha, that commit expands the whole pull request through the API,
// and an unknown :code: inside it hard-fails the release, while the earlier
// replayed commits resolve as covered and are skipped. A diagnostic that lies
// about the engine is worse than no diagnostic, and the same sentence was the
// stated justification for the squash hard fail, so both are pinned here.
func TestMergeMethodChecksDescribeTheWalkThatExists(t *testing.T) {
	permissive := healthyInput(t)
	permissive.RepoObject.AllowRebaseMerge = yes()
	squashOff := healthyInput(t)
	squashOff.RepoObject.AllowSquashMerge = no()

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
			name:  "squash off is argued from the fallback, not from resolution",
			check: find(t, Run(squashOff), IDSquashEnabled),
			want:  StatusFail,
			mention: []string{
				"own message",                // §4's fallback classifies the commit itself
				"Merge pull request #N from", // which a merge commit's message is
			},
			forbid: []string{
				"lenient",       // the rebase path is not lenient
				"only the last", // and its earlier commits are not classified at all
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
