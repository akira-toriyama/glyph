// Package doctor verifies that a repository actually SATISFIES the assumptions
// glyph's release machinery makes about it — the configuration glyph depends on
// but cannot see from the inside.
//
// It exists because that configuration drifts silently and nothing notices. The
// fleet measurement that motivated it (2026-07-21) found 31 of 34 non-archived
// repositories allowing merge commits and rebase merges, and glyph-test drifted
// to squash_merge_commit_title=PR_TITLE / squash_merge_commit_message=PR_BODY —
// neither fact detected by anything, in either direction, until a human ran
// `gh api` by hand. Every workflow stayed green throughout; the release verdict
// was simply computed over a repository that no longer matched the model.
//
// Two properties are load-bearing and hold for every check here:
//
//   - READ-ONLY. doctor never writes a repository, a checkout or a workflow
//     file. It reports and hands over the command to run; a diagnostic that
//     mutates cannot be run casually, and this one is meant to be run casually.
//   - INDEPENDENT. One unreadable input (a repository object a token cannot
//     fetch, a checkout with no .github/workflows) degrades THAT check to
//     StatusUnknown and no other. A report that aborts halfway is a report that
//     hides the finding you needed.
//
// The severities are the other deliberate part, argued at each check. An
// advisory about a house convention is a weaker claim than a defect, and it is
// kept weaker on purpose: a report that fails over settings glyph itself handles
// correctly teaches the fleet to ignore the report, which is the one failure
// mode a voluntary check cannot survive.
package doctor

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/internal/github"
)

// Status is a check's verdict. Four values, and the distinction between the
// last two is the point: a check that FAILED observed a real defect, a check
// that could not RUN observed nothing and proves nothing — collapsing them
// would let an unreadable repository read as a clean bill of health.
type Status string

const (
	// StatusPass — the assumption holds, observed.
	StatusPass Status = "pass"
	// StatusFail — the assumption is violated and something concrete breaks.
	// Drives the non-zero exit.
	StatusFail Status = "fail"
	// StatusAdvice — a divergence from a house convention that costs glyph
	// nothing mechanically. Worth seeing, never worth failing a job over.
	StatusAdvice Status = "advice"
	// StatusUnknown — the check could not run: the input it needed was
	// unreadable or the API declined to report it. Not a pass.
	StatusUnknown Status = "unknown"
)

// Check is one independent finding. The id is the STABLE machine key a CI job
// branches on — never the message, which is prose and will be reworded.
// Observed and Expected are the two halves a human needs to act, and Fix is the
// concrete command or edit; Details carries the per-item breakdown for a check
// that inspects many things at once (the workflow pins).
type Check struct {
	ID       string   `json:"id"`
	Status   Status   `json:"status"`
	Observed string   `json:"observed"`
	Expected string   `json:"expected"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Details  []string `json:"details,omitempty"`
}

// Counts is the report's tally by status — what a dashboard reads without
// walking the check array.
type Counts struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Advice  int `json:"advice"`
	Unknown int `json:"unknown"`
}

// Report is the whole machine surface: the repository asked about, every check
// in a STABLE order (source order — a consumer may index it), the tally, and
// the single boolean a CI gate wants.
//
// OK is deliberately false for an unknown as well as a fail: "we could not
// check" is not "it is fine". Advice never touches it — house conventions are
// not gates.
type Report struct {
	Repo   string  `json:"repo"`
	Checks []Check `json:"checks"`
	Counts Counts  `json:"counts"`
	OK     bool    `json:"ok"`
}

// Input is everything the checks read. The repository object is fetched by the
// caller (internal/cli owns every network decision, as it does for every other
// command) and handed over WITH its error: RepoErr non-nil is not a fatal
// condition here, it is the input to the token check and the reason the
// settings checks report could-not-run.
type Input struct {
	// Repo is owner/name, for the report header.
	Repo string
	// Root is the local checkout whose .github/workflows are scanned.
	Root string
	// RepoObject / RepoErr are the outcome of GET /repos/{owner}/{repo}.
	// RepoErr must be handed over VERBATIM, exactly as internal/github
	// returned it: the token check asks github.IsRepoUnknown about it, and
	// re-wrapping or re-classifying it on the way here would flatten the one
	// distinction that decides between exit 3 and exit 4.
	RepoObject github.Repo
	RepoErr    error
	// TokenConfigured records whether a credential was configured at all, so
	// the token check can tell "anonymous" from "rejected".
	TokenConfigured bool
}

// The stable check ids. They are the report's API — rename one and every CI
// gate branching on it silently stops matching, so treat this block as the
// versioned surface it is.
const (
	IDTokenAccess    = "token-repo-read"
	IDSquashEnabled  = "squash-merge-enabled"
	IDMergeCommit    = "merge-commit-enabled"
	IDRebaseMerge    = "rebase-merge-enabled"
	IDSquashTitle    = "squash-commit-title"
	IDSquashMessage  = "squash-commit-message"
	IDWorkflowPinned = "workflow-glyph-pins"
)

// Expected values for the two squash enums, as GitHub spells them.
const (
	wantSquashTitle   = "COMMIT_OR_PR_TITLE"
	wantSquashMessage = "COMMIT_MESSAGES"
)

// Run executes every check and assembles the report. It never returns an error:
// an unhealthy — or unreadable — repository IS the report, not a failure to
// produce one. The caller maps the report to an exit code.
//
// Check order is the report order and is chosen for reading: the token check
// first because it explains every could-not-run below it, then the three merge
// methods, then the squash policy they enable, then the local workflow pins
// (the one check that needs no network at all, so it still answers when the API
// side is entirely dark).
func Run(in Input) *Report {
	r := &Report{Repo: in.Repo, Checks: []Check{
		checkTokenAccess(in),
		checkSquashEnabled(in),
		checkMergeCommit(in),
		checkRebaseMerge(in),
		checkSquashTitle(in),
		checkSquashMessage(in),
		checkWorkflowPins(in.Root),
	}}
	r.OK = true
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			r.Counts.Pass++
		case StatusFail:
			r.Counts.Fail++
			r.OK = false
		case StatusAdvice:
			r.Counts.Advice++
		case StatusUnknown:
			r.Counts.Unknown++
			r.OK = false
		}
	}
	return r
}

// IDs returns the ids of every check with the given status, in report order —
// what the CLI puts in the error envelope's details so a machine gets the
// failing keys without re-parsing the human output.
func (r *Report) IDs(s Status) []string {
	var ids []string
	for _, c := range r.Checks {
		if c.Status == s {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// checkTokenAccess reports whether the configured credential can actually read
// the repository, and surfaces what the API SAID about its access — never what
// it might have. GitHub does not report a token's scopes in the repository
// object, and guessing them ("this looks like a repo-scoped PAT") would be a
// diagnostic inventing evidence, so the check reports the permissions block or
// says plainly that there was none.
//
// A read failure splits in two, and the split is the whole point of the check:
//
//   - GitHub ANSWERED 404 — there is no such repository for this credential.
//     That is a finding: the answer is no, and it will still be no on a retry.
//     Fail, which the CLI exits 3 on.
//   - Anything else — a 403 rate limit, a 5xx that outlived the retry schedule,
//     a dead socket, a body that did not parse. Nothing about the repository was
//     observed, so nothing may be concluded about it. Could-not-run, exit 4.
//
// Collapsing the two (any error is a failed check) is the bug this shape exists
// to prevent: it made a transient GitHub outage exit 3, which .github's fleet
// wrappers read as "this repository is misconfigured — hard-fail, do not retry",
// while every other glyph command classifies the same outage as 4 and gets
// retried. Both directions were wrong at once.
func checkTokenAccess(in Input) Check {
	c := Check{ID: IDTokenAccess, Expected: "the credential can read " + in.Repo}
	credential := "no credential (GITHUB_TOKEN/GH_TOKEN unset — an anonymous read)"
	if in.TokenConfigured {
		credential = "a token from GITHUB_TOKEN/GH_TOKEN"
	}
	if in.RepoErr != nil {
		if !github.IsRepoUnknown(in.RepoErr) {
			c.Status = StatusUnknown
			c.Observed = fmt.Sprintf("the read with %s produced no answer: %v", credential, in.RepoErr)
			c.Message = "GitHub did not answer this read at all — a rate limit, an outage that outlived glyph's retry " +
				"schedule, or an unreachable host — so nothing was observed about the repository and nothing below could " +
				"run. This is unverified, NOT a verdict on the repository: the same failure would abort a release run, and " +
				"it is the class a CI wrapper retries"
			c.Fix = "re-run once the API answers again; if it persists, check GITHUB_API_URL, the rate limit (gh api rate_limit) and network egress"
			return c
		}
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("the repository object could not be read with %s: %v", credential, in.RepoErr)
		// The 404 nuance is the one that wastes the most time: GitHub hides a
		// private repository from a credential that cannot see it rather than
		// admitting it exists, so "Not Found" here almost never means the name
		// is wrong. internal/github's IsCommitUnknown carries the same warning
		// for the same reason.
		c.Message = "every setting below is unverifiable without this read, and a release run would fail the same way. " +
			"GitHub answers 404 (not 403) for a private repository a credential cannot see, so a Not Found here usually " +
			"names the token rather than the repository"
		c.Fix = "set GITHUB_TOKEN (or GH_TOKEN) to a credential with read access — in Actions, pass token: ${{ github.token }}"
		return c
	}
	c.Status = StatusPass
	c.Observed = fmt.Sprintf("read %s with %s; %s", visibility(in.RepoObject), credential, permissionSummary(in.RepoObject.Permissions))
	c.Message = "the read glyph's verdict commands depend on works"
	return c
}

// visibility renders how GitHub described the repository, falling back to the
// boolean when the newer visibility field is absent.
func visibility(r github.Repo) string {
	if r.Visibility != "" {
		return r.Visibility + " repository"
	}
	if r.Private {
		return "private repository"
	}
	return "public repository"
}

// permissionSummary renders the API's permissions block. Absent is a fact worth
// stating in those words: an anonymous read of a public repository reports
// nothing, and so does a credential GitHub chooses not to describe — doctor
// says which levels were reported, and nothing about the ones that were not.
func permissionSummary(p *github.RepoPermissions) string {
	if p == nil {
		return "the API reported no permissions block for this credential (an anonymous or otherwise undescribed read)"
	}
	granted := []string{}
	for _, l := range []struct {
		name string
		on   bool
	}{{"admin", p.Admin}, {"maintain", p.Maintain}, {"push", p.Push}, {"triage", p.Triage}, {"pull", p.Pull}} {
		if l.on {
			granted = append(granted, l.name)
		}
	}
	if len(granted) == 0 {
		return "the API reported the permissions block with no level granted"
	}
	return "the API reports permissions: " + join(granted)
}

// checkSquashEnabled is the one merge-method setting that is a hard failure,
// and the reason is mechanical rather than stylistic — but it is NOT the reason
// the first draft of this file gave. That draft argued from the exact path ("only
// a squash commit carries merge_commit_sha") and from a lenient fallback for a
// rebase; both are false, as checkRebaseMerge now spells out. The engine resolves
// EVERY merge style on the exact path: GitHub points merge_commit_sha at whichever
// commit represents the merge — the squash commit, a rebase's last replayed
// commit, or the merge commit itself — and the walk expands the pull request from
// there (internal/cli/sincetag.go, t-7zt7). While the API answers, squash being
// off costs nothing at all.
//
// What squash being off removes is the path for when the API does NOT answer.
// DESIGN §4's fallback classifies a walked commit from its OWN message, and a
// squash commit is the only landing style where that message is the pull
// request's gitmoji subject AND the commit's sha is the pull request's key — the
// exact path and the fallback agree by construction. Land the same PR as a merge
// commit and the message on main is "Merge pull request #12 from …": no :code: at
// all, and the fallback skips it on its parent count, so the whole PR counts none.
// Land it as a rebase and the messages survive but the shas do not — GitHub always
// writes new commits, none of which appear in the PR's own commit listing, so the
// walk's dedup cannot recognise them and a lagging read can fold the same change
// in twice.
//
// That window is not hypothetical: the walk runs seconds after a push, where
// GitHub answers 422 for a sha it does not know yet, and the t-bjrv outage
// answered 503 for tens of seconds across the fleet. A repository with squash off
// is one such window away from a wrong verdict, and nothing in the run says so.
//
// Hence fail rather than advice, and hence the asymmetry with the two checks
// below: ALLOWING another merge method leaves squash there for the traffic that
// matters, while turning squash off removes the only landing style that survives
// an API-dark window. Everything downstream is drawn on it too — the two squash
// policy checks describe a commit this repository can no longer produce, and the
// notes grouping, the PR preview and the release reusable are all calibrated on
// one commit per PR.
func checkSquashEnabled(in Input) Check {
	c := Check{ID: IDSquashEnabled, Expected: "allow_squash_merge=true"}
	if u, ok := available(in, c, "allow_squash_merge", in.RepoObject.AllowSquashMerge); !ok {
		return u
	}
	allowed := *in.RepoObject.AllowSquashMerge
	c.Observed = fmt.Sprintf("allow_squash_merge=%t", allowed)
	if allowed {
		c.Status = StatusPass
		c.Message = "PRs can land as one squash commit, the only landing style that is BOTH the pull request's " +
			"merge_commit_sha and a classifiable gitmoji subject — so the walk reaches the same verdict whether or not the " +
			"API answers"
		return c
	}
	c.Status = StatusFail
	c.Message = "squash merging is OFF, so every PR must land as a merge commit or a rebase merge — and neither survives the " +
		"walk's fallback path. While GitHub answers, all three styles resolve alike: merge_commit_sha names the squash commit, " +
		"a rebase's last replayed commit, or the merge commit, and the walk expands the pull request from there. When GitHub " +
		"does NOT answer — the seconds after a push, where it does not know the sha yet, or an outage outliving the retry " +
		"schedule — the walk classifies the commit on main from its own message (DESIGN §4). Only a squash commit is both the " +
		"pull request's key and a classifiable subject: a merge commit's message is \"Merge pull request #N from …\", which the " +
		"fallback skips on its parent count so the whole PR counts none, and a rebase's replayed commits carry new shas that " +
		"appear in no pull request's commit listing, so the same change can be folded in twice. Allowing another method is a " +
		"convention; leaving squash off removes the only one that holds when the API is dark"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_squash_merge=true", in.Repo)
	return c
}

// checkMergeCommit reports allow_merge_commit, and its severity is the one
// deliberate judgement call in this file.
//
// A merge commit USED to be data loss: bump.Excluded drops any commit with 2+
// parents, so a PR landed with the merge button's "Create a merge commit"
// vanished from the release walk entirely and its gitmoji never reached the
// verdict — a whole PR's worth of change shipping as no release at all (t-7zt7).
// If that were still true, this would be a hard failure and the honest one.
//
// t-7zt7 fixes the walk to resolve and expand a merge commit exactly like a
// squash commit, so the setting costs no bump any more. What remains is a house
// convention — the fleet is squash-only so that main carries one commit per PR
// and the walk is one round-trip per PR — and doctor reports it at that weight.
// Failing over a setting glyph now handles correctly would be crying wolf, and a
// diagnostic nobody is obliged to run cannot afford to be ignored.
//
// The coupling runs the other way too: this severity is downstream of that fix.
// If the merge-commit walk is ever reverted or regresses, this check must move
// back to StatusFail with it — they are one decision, not two.
func checkMergeCommit(in Input) Check {
	c := Check{ID: IDMergeCommit, Expected: "allow_merge_commit=false (house convention: squash-only)"}
	if u, ok := available(in, c, "allow_merge_commit", in.RepoObject.AllowMergeCommit); !ok {
		return u
	}
	allowed := *in.RepoObject.AllowMergeCommit
	c.Observed = fmt.Sprintf("allow_merge_commit=%t", allowed)
	if !allowed {
		c.Status = StatusPass
		c.Message = "the merge button cannot create a merge commit, so main stays one commit per PR"
		return c
	}
	c.Status = StatusAdvice
	c.Message = "merge commits are allowed. This is no longer data loss — the release walk resolves and expands a merge " +
		"commit like a squash commit — so nothing breaks; it is a divergence from the squash-only house convention that " +
		"keeps main at one commit per PR and the walk at one API round-trip per PR"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_merge_commit=false (optional — this is a convention, not a defect)", in.Repo)
	return c
}

// checkRebaseMerge reports allow_rebase_merge. Advice — and the reason had to be
// corrected, because the first draft of this check described a walk that does not
// exist. It told the fleet that a rebase-merged PR's non-final commits are
// "classified from their own preserved messages on the walk's fallback path",
// where an unknown :code: only warns and counts none.
//
// The walk does no such thing. GitHub points merge_commit_sha at the LAST replayed
// commit, so that commit resolves and expands the whole pull request through the
// API — an unknown :code: anywhere inside it hard-fails the release through
// wedgeHint, exactly as it would for a squash — and the earlier replayed commits
// resolve as COVERED by that same pull request and are skipped outright, so
// nothing is classified twice and nothing is classified leniently. Rebase merging
// is handled exactly as strictly as squash, the opposite of what this check used
// to say. A diagnostic that lies about the engine is worse than no diagnostic.
//
// What it does cost is one API round-trip per replayed commit instead of one per
// pull request, and the walk's dedup key: GitHub always writes NEW shas when it
// rebases, and none of them appear in the pull request's own commit listing, so
// inside the API-lag window (DESIGN §4) a replayed commit classified from its own
// message can be folded in a second time when the last one expands the PR. A cost
// and a narrow window, not a defect — hence advice, at the same weight as the
// merge-commit convention rather than as a claim about correctness.
func checkRebaseMerge(in Input) Check {
	c := Check{ID: IDRebaseMerge, Expected: "allow_rebase_merge=false (house convention: squash-only)"}
	if u, ok := available(in, c, "allow_rebase_merge", in.RepoObject.AllowRebaseMerge); !ok {
		return u
	}
	allowed := *in.RepoObject.AllowRebaseMerge
	c.Observed = fmt.Sprintf("allow_rebase_merge=%t", allowed)
	if !allowed {
		c.Status = StatusPass
		c.Message = "the merge button cannot rebase-merge, so every PR lands as one commit on main and the walk stays one " +
			"API round-trip per pull request"
		return c
	}
	c.Status = StatusAdvice
	c.Message = "rebase merging is allowed. It costs no strictness: merge_commit_sha names the LAST replayed commit, so that " +
		"commit resolves and expands the whole pull request through the API exactly as a squash commit does — an unknown " +
		":code: inside it fails the release, it is NOT downgraded to a warning — and the earlier replayed commits resolve as " +
		"covered by the same pull request and are skipped, so nothing is counted twice. What it costs is one API round-trip " +
		"per replayed commit instead of one per pull request, plus the walk's dedup key: a rebase always writes new shas, " +
		"which appear in no pull request's commit listing, so inside the API-lag window a replayed commit can be folded in " +
		"twice"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_rebase_merge=false (optional — this is a convention, not a defect)", in.Repo)
	return c
}

// checkSquashTitle is a hard failure, and unlike the merge methods it is not
// about conventions at all: squash_merge_commit_title decides whether the commit
// that lands on main carries a classifiable gitmoji subject.
//
// COMMIT_OR_PR_TITLE keeps a single-commit PR's own subject — gitmoji and all —
// on main, and only borrows the PR title when a PR has several commits (the
// erasure glyph's second hop exists to undo). PR_TITLE hands the PR title to
// EVERY squash, including the single-commit PRs that are most of a fleet repo's
// traffic, so main fills with subjects that no gitmoji reader can classify. The
// verdict survives while the API answers — glyph re-reads the PR — but the
// walk's documented fallback (a direct push, or the API lag right after a push,
// DESIGN §4) classifies the squash commit's OWN message, and that message is now
// unclassifiable: the release counts none and the bump is silently lost.
// glyph-test drifted to exactly this and nothing noticed.
func checkSquashTitle(in Input) Check {
	return checkSquashEnum(in, IDSquashTitle, "squash_merge_commit_title",
		in.RepoObject.SquashMergeCommitTitle, wantSquashTitle,
		"a single-commit PR keeps its own gitmoji subject on main; only a multi-commit squash borrows the PR title",
		"every squash commit takes the PR TITLE as its subject, including single-commit PRs — so main fills with subjects "+
			"no gitmoji reader can classify. The verdict holds while the API answers, but the walk's fallback (a direct push, "+
			"or API lag right after a push) classifies the squash commit's own message, which now carries no :code: at all: "+
			"the release counts none and the bump is lost silently")
}

// checkSquashMessage is the body half of the same policy. COMMIT_MESSAGES keeps
// the per-commit message list in the squash body — the human-readable record of
// what was actually in the PR, on main, forever. PR_BODY replaces it with the
// pull request description, so once GitHub's API is unavailable or the PR is
// gone the pre-squash types exist nowhere in the repository. glyph deliberately
// does not depend on that body (DESIGN §4 rejected owning it as the primary
// signal — the fragility is exactly why glyph reads the API instead), but it is
// the only offline record of a PR's contents, and the drift alarm that made
// glyph-test's drift discoverable at all.
func checkSquashMessage(in Input) Check {
	return checkSquashEnum(in, IDSquashMessage, "squash_merge_commit_message",
		in.RepoObject.SquashMergeCommitMessage, wantSquashMessage,
		"the squash body keeps the per-commit messages, the only offline record of a PR's pre-squash types",
		"the squash body is the PR DESCRIPTION instead of the per-commit messages, so the pre-squash commit types survive "+
			"only in GitHub's API — nothing in the repository itself records what a PR contained, and the drift alarm that "+
			"makes a wrong verdict noticeable is gone")
}

// checkSquashEnum is the shared body of the two policy checks: same absence
// handling, same fail shape, different consequence text.
func checkSquashEnum(in Input, id, field, observed, want, passMsg, failMsg string) Check {
	c := Check{ID: id, Expected: field + "=" + want}
	if in.RepoErr != nil {
		return unreadableRepo(c, field)
	}
	if observed == "" {
		return unreportedField(c, field)
	}
	c.Observed = field + "=" + observed
	if observed == want {
		c.Status = StatusPass
		c.Message = passMsg
		return c
	}
	c.Status = StatusFail
	c.Message = failMsg
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -f %s=%s", in.Repo, field, want)
	return c
}

// available is the shared could-not-run guard for the three *bool merge
// settings. ok false means the caller must return the Check it got back (an
// Unknown, already worded); ok true means value is safe to dereference.
func available(in Input, c Check, field string, value *bool) (Check, bool) {
	switch {
	case in.RepoErr != nil:
		return unreadableRepo(c, field), false
	case value == nil:
		return unreportedField(c, field), false
	}
	return c, true
}

// unreadableRepo marks a check that never ran because the repository object
// itself could not be fetched. It points at the token check rather than
// repeating the API error on every line.
func unreadableRepo(c Check, field string) Check {
	c.Status = StatusUnknown
	c.Observed = "not checked — the repository object could not be read"
	c.Message = "this setting is unverified, not verified-good: " + field + " was never observed. See the " + IDTokenAccess + " check"
	c.Fix = "fix the credential and re-run"
	return c
}

// unreportedField marks a check whose field the API simply did not include.
// GitHub omits a repository's merge settings from the response it gives a
// credential that may not see them, and the *bool/"" encoding in
// internal/github keeps that absence distinguishable from a false — which is
// the whole reason this state exists rather than a confident wrong answer.
func unreportedField(c Check, field string) Check {
	c.Status = StatusUnknown
	c.Observed = "not reported — the API response carried no " + field + " field"
	c.Message = "GitHub omits a repository's merge settings from the response given to a credential that cannot see them, " +
		"so this is unverified rather than off. Re-run with a credential that has admin access to the repository's settings"
	c.Fix = "re-run with a token that can read the repository's settings (admin), e.g. `gh auth token`"
	return c
}

// join renders a short list as "a, b and c" — the report is read by humans as
// well as jq.
func join(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
