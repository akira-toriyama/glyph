package workflows

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseRefGuardWritesFromTheDefaultBranchOnly runs the guard's own shell
// instead of reading it, in the shape TestArtifactArityAcceptsNeither argues
// for: "default branch only", "default branch unless dry-run" and "any ref"
// differ by one condition each and read almost identically in a diff, so the
// only honest check is to feed the script the ref combinations and look at
// what it does.
//
// What the guard is for. A release run WRITES — it upserts the one
// glyph-managed rolling draft and re-points its target — so the ref it runs
// from decides what the repository ships. Off the default branch every one of
// those writes is wrong and nothing downstream notices: the walk's range is
// <tag>..HEAD over that ref, the branch's unmerged commits enter the notes,
// each resolves to no merged pull request and is classified from its own
// subject on the direct-push arm, and walkFacts stays complete (only an API
// lag is recorded as dropped) so the exit-4 refusal for an unreadable walk
// never fires. The run is green, the draft holds unmerged work, and Publish
// puts the tag on the branch tip. A caller cannot close this itself: GitHub
// cannot restrict which branch a workflow_dispatch runs from.
//
// The exact-match case is not padding. `refs/heads/mainline` against a default
// of `main` is what a prefix test or a `case` glob would wave through, and the
// wave-through is a write.
func TestReleaseRefGuardWritesFromTheDefaultBranchOnly(t *testing.T) {
	script := extractRun(t, repoFile(t, filepath.Join(".github", "workflows", "release.yml")),
		"Refuse a release run from a non-default ref")

	cases := []struct {
		name       string
		ref        string
		defBranch  string
		dryRun     string
		wantExit   int
		wantOutput string
	}{
		{"a push to the default branch is the release path", "refs/heads/main", "main", "false", 0, ""},
		{"a topic branch is refused", "refs/heads/topic", "main", "false", 1, "::error::glyph-release writes the rolling draft from the default branch only"},
		{"a tag ref is refused", "refs/tags/v1.2.3", "main", "false", 1, "::error::glyph-release writes the rolling draft"},
		{"a branch the default branch is a prefix of is refused", "refs/heads/mainline", "main", "false", 1, "::error::glyph-release writes the rolling draft"},
		{"a non-main default branch is honoured", "refs/heads/trunk", "trunk", "false", 0, ""},
		{"a dry run from a topic branch is the sanctioned preview", "refs/heads/topic", "main", "true", 0, "::notice::dry run"},
		{"a dry run from the default branch is fine too", "refs/heads/main", "main", "true", 0, "::notice::dry run"},
		{"an unreadable boundary fails closed", "refs/heads/main", "", "false", 1, "::error::glyph-release cannot read this repository's default branch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(),
				"RELEASE_REF="+c.ref, "DEFAULT_BRANCH="+c.defBranch, "DRY_RUN="+c.dryRun)
			out, err := cmd.CombinedOutput()
			exit := 0
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				exit = ee.ExitCode()
			case err != nil:
				t.Fatalf("running the ref guard: %v", err)
			}
			if exit != c.wantExit {
				t.Errorf("ref=%q default_branch=%q dry_run=%s exited %d, want %d\n%s",
					c.ref, c.defBranch, c.dryRun, exit, c.wantExit, out)
			}
			if c.wantOutput != "" && !strings.Contains(string(out), c.wantOutput) {
				t.Errorf("ref=%q default_branch=%q dry_run=%s printed no %q — a refusal nobody can "+
					"diagnose gets worked around instead of fixed:\n%s",
					c.ref, c.defBranch, c.dryRun, c.wantOutput, out)
			}
		})
	}
}

// TestReleaseRefGuardReadsTheEventItIsGiven pins the two expressions the guard
// compares, because the shell above is only as good as what the runner puts in
// its environment — a step whose env block drifted would pass every case above
// with both variables empty.
//
// `github.event.repository.default_branch` is the same expression lint.yml's
// push arm compares against, and it was measured on 2026-08-11 (glyph-test's
// ref-probe, dispatched from main and from a topic branch) to survive a
// workflow_dispatch payload, not just the push payload lint.yml sees. That
// measurement is why the guard reads the event instead of paying an API
// round-trip for the repository object: the alternative failure mode is a
// fail-closed guard that refuses every manual run in the fleet.
func TestReleaseRefGuardReadsTheEventItIsGiven(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))
	i := strings.Index(body, "name: Refuse a release run from a non-default ref")
	if i < 0 {
		t.Fatal("release.yml has no non-default-ref guard step — the whole file is now a write path " +
			"any caller can point at any branch, and the test above is asserting nothing")
	}
	env, _, ok := strings.Cut(body[i:], "run: |")
	if !ok {
		t.Fatal("the ref guard step has no `run: |` block — it stopped being a step that runs anything")
	}
	for _, want := range []string{
		"RELEASE_REF: ${{ github.ref }}",
		"DEFAULT_BRANCH: ${{ github.event.repository.default_branch }}",
		"DRY_RUN: ${{ inputs.dry-run }}",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the ref guard's env block no longer sets %q — the comparison still runs, over an "+
				"empty variable, and `set -u` does not save a step that assigns \"\" from a stale "+
				"expression", want)
		}
	}
}

// TestReleaseRefGuardRunsBeforeTheWrite pins the ordering the guard's whole
// value rests on. Every case above stays green with the step moved below the
// verdict, and by then the draft has already been retagged and rewritten at
// the branch tip — the guard would be refusing a run that had done the damage.
func TestReleaseRefGuardRunsBeforeTheWrite(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "release.yml")))
	guard := strings.Index(body, "name: Refuse a release run from a non-default ref")
	write := strings.Index(body, "name: Compose the verdict and upsert the rolling DRAFT")
	if guard < 0 || write < 0 {
		t.Fatalf("could not find both steps in release.yml (guard=%d, write=%d) — a rename moved one "+
			"of them and this ordering is no longer being checked", guard, write)
	}
	if guard > write {
		t.Error("the non-default-ref guard now runs AFTER the verdict step that upserts the draft, so " +
			"the write it exists to prevent has already happened when it fires")
	}
}
