package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// The two variables that make a run's identity readable to a PROCESS rather
// than to the template engine. GITHUB_REF is the ref the run was started from;
// GITHUB_EVENT_PATH is the file the `github.event.*` expressions are rendered
// out of, which is where the repository's default branch can be read for free.
//
// Measured 2026-08-11 in glyph-test (PR #48, dispatched from main and from a
// topic branch): the file is present, readable, ~7.4 kB, and carries
// `.repository.default_branch` on a workflow_dispatch payload. That the
// expression resolves — what #156 measured — is evidence about the runner's
// template engine and says nothing about a process's filesystem, which is what
// the measurement above reads.
const (
	envActionsRef   = "GITHUB_REF"
	envActionsEvent = "GITHUB_EVENT_PATH"
)

// actionsEnv is the run-context set as one value so `internal/cli`'s TestMain
// can blank all of it by ranging rather than by re-typing the names. glyph's
// own tests run ON an Actions runner, where these are ambient and would arm the
// guard mid-test; a hand-maintained list is the convention TestMain's comment
// disowns, and a third variable added here later joins the blank list by
// construction.
var actionsEnv = []string{envActionsRef, envActionsEvent}

// checkReleaseRef refuses a release WRITE started from a ref that is not the
// repository's default branch. The harm, and why no caller can close it, are
// DESIGN §4's ("the only release-time refusal a dry run does not reproduce").
//
// This is the same boundary `.github/workflows/release.yml` draws in its
// "Refuse a release run from a non-default ref" step, and the two must never
// drift on polarity — same exact-match predicate, same dry-run exemption. Why
// both layers are kept: DESIGN §4, "the cheaper half".
//
// Arming is by EVIDENCE, not by provider detection: `GITHUB_ACTIONS == "true"`
// would argue against output.go's position that glyph's annotations are written
// unconditionally because "outside Actions the line is an ordinary
// human-readable warning". Either variable present means something handed this
// process a run identity: an empty GITHUB_REF beside a payload is then a refusal
// rather than a shrug, and a ref without a payload is judged against the
// repository object — a step that sets `env: GITHUB_REF:` to empty, or a runner
// that renames one of them, must not be able to disarm the fleet's only
// write-side ref boundary with a green run. Both absent is a laptop, where there
// is no ref to judge; the run proceeds and says so.
func checkReleaseRef(ctx context.Context, owner, repo string, dryRun bool) error {
	ref := strings.TrimSpace(os.Getenv(envActionsRef))
	event := strings.TrimSpace(os.Getenv(envActionsEvent))

	// A dry run writes nothing, so it has no authority in question. This is
	// the one release-time refusal a preview deliberately does NOT reproduce
	// (DESIGN §4, "the argued exception"). Exempting it cannot make the preview more
	// permissive than the run it previews, because the real run from the same
	// ref refuses unconditionally — and judging it would red the sanctioned
	// preview path every consumer exposes as a dispatch input, on the day the
	// pin moves. Short-circuits before the boundary is resolved so a preview
	// never pays for the fallback request.
	if dryRun {
		if ref != "" {
			noticef("dry run from %s: nothing is written, so the ref this release would write from was not judged", ref)
		}
		return nil
	}

	if ref == "" && event == "" {
		noticef("no GitHub Actions run context (%s and %s are both unset), so the ref this release writes from was not judged", envActionsRef, envActionsEvent)
		return nil
	}
	if ref == "" {
		return core.APIf("glyph was handed a GitHub Actions run context (%s is set) but no ref (%s is empty), so the branch this release would write from cannot be judged; refusing rather than upserting the rolling draft from an unnamed ref", envActionsEvent, envActionsRef)
	}

	def, source, derr := releaseDefaultBranch(ctx, owner, repo)
	if derr != nil {
		return derr
	}
	if want := "refs/heads/" + def; ref != want {
		return core.APIf("glyph release writes the rolling draft from the default branch only: this run is on %s, not %s (%s). A release from another ref folds that ref's unmerged commits into the notes and re-points the draft's target at its HEAD. Re-run from the default branch, or pass --dry-run to preview from here", ref, want, source)
	}
	return nil
}

// releaseDefaultBranch resolves the branch a release may be written from, and
// names where the answer came from so a refusal can be argued with.
//
// Two sources, cheapest first: the event payload, which costs nothing, then
// the repository object, one request (why two: DESIGN §4, "eleven pin reverts").
//
// gitsource.DefaultBranch is deliberately not a third source: it reads
// refs/remotes/<remote>/HEAD, which actions/checkout never writes, so in CI it
// is permanently unresolved — and its contract is that unresolved is not an
// error, which is the opposite polarity from a write guard.
func releaseDefaultBranch(ctx context.Context, owner, repo string) (name, source string, err error) {
	if path := strings.TrimSpace(os.Getenv(envActionsEvent)); path != "" {
		// #nosec G304 -- the path is the runner's own $GITHUB_EVENT_PATH, the
		// same file every `github.event.*` expression is rendered from; a
		// process that can set it can already set anything else glyph reads.
		if b, rerr := os.ReadFile(path); rerr == nil {
			var payload struct {
				Repository struct {
					DefaultBranch string `json:"default_branch"`
				} `json:"repository"`
			}
			if json.Unmarshal(b, &payload) == nil {
				if got := strings.TrimSpace(payload.Repository.DefaultBranch); got != "" {
					return got, "from the event payload", nil
				}
			}
		}
		warnf("the event payload at %s does not name this repository's default branch; falling back to the repository object over the API", path)
	}

	r, aerr := newGitHub().Repository(ctx, owner, repo)
	if aerr != nil {
		// Never the ref-refusal wording: this run may well be on the default
		// branch, and saying otherwise would send a human to fix a ref that
		// was never wrong.
		return "", "", core.APIf("glyph could not read this repository's default branch — not from the event payload, and not from %s/%s over the API (%v) — so the ref this release writes from cannot be judged; refusing rather than upserting the rolling draft against an unverified boundary", owner, repo, aerr)
	}
	if got := strings.TrimSpace(r.DefaultBranch); got != "" {
		return got, "from the repository object", nil
	}
	return "", "", core.APIf("neither the event payload nor the repository object %s/%s names a default branch, so the ref this release writes from cannot be judged; refusing rather than upserting the rolling draft against an unverified boundary", owner, repo)
}
