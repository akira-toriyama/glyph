// Package doctor verifies that a repository actually SATISFIES the assumptions
// glyph's release machinery makes about it — the configuration glyph depends on
// but cannot see from the inside. Why that configuration needs a check, and the
// argument behind each check's severity, are DESIGN §7's (§4.1's for the two
// package checks).
//
// Two properties are load-bearing and hold for every check here:
//
//   - READ-ONLY. doctor never writes a repository, a checkout or a workflow
//     file. It reports and hands over the command to run; a diagnostic that
//     mutates cannot be run casually, and this one is meant to be run casually.
//   - INDEPENDENT. One unreadable input (a repository object a token cannot
//     fetch, a workflow file the filesystem refuses) degrades THAT check to
//     StatusUnknown and no other. A report that aborts halfway is a report that
//     hides the finding you needed.
package doctor

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/github"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
	"github.com/akira-toriyama/glyph/v6/internal/hook"
)

// Status is a check's verdict. Four values, and the distinction between fail
// and unknown is the point: a check that FAILED observed a real defect, a check
// that could not RUN observed nothing and proves nothing — collapsing them
// would let an unreadable repository read as a clean bill of health.
type Status string

const (
	// StatusPass — the assumption holds, observed.
	StatusPass Status = "pass"
	// StatusFail — the assumption is violated and something concrete breaks.
	// Drives the non-zero exit.
	StatusFail Status = "fail"
	// StatusAdvice — worth seeing, never worth failing a job over. Each
	// advising check's case is DESIGN §7's (§4.1's for root-line-tags).
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
// check" is not "it is fine". Advice never touches it.
type Report struct {
	Repo   string  `json:"repo"`
	Checks []Check `json:"checks"`
	Counts Counts  `json:"counts"`
	OK     bool    `json:"ok"`
}

// Input is everything the checks read. The repository object is fetched by the
// caller (internal/cli makes every request on doctor's behalf — doctor itself
// speaks to no network) and handed over WITH its error: RepoErr non-nil is not a fatal
// condition here, it is the input to the token check and the reason the
// settings checks report could-not-run.
type Input struct {
	// Repo is owner/name, for the report header.
	Repo string
	// Root is the local checkout whose .github/workflows are scanned and
	// whose glyph.toml Config was loaded from: git's own top level (rev-parse
	// --show-toplevel), or a bare "." — with nothing loaded — when git could
	// not name one. RootErr is that failure, verbatim — resolved by the
	// caller for the same reason HooksDir is: naming the top level takes a
	// subprocess, and internal/doctor runs none. It decides how the local
	// scans read an absent .github/workflows: under a git-named root that
	// absence is an observed fact about the repository (pass — nothing to
	// scan), while under a bare "." it could equally be a wrong working
	// directory, which must stay unknown; and it is the config check's
	// could-not-run.
	Root    string
	RootErr error
	// Config / ConfigErr are the checkout's glyph.toml, loaded ONCE by the
	// caller at Root, and config.LoadFile's failure handed over VERBATIM like
	// RepoErr: the config check classifies it (absent, not a regular file,
	// unreadable, invalid) and must see the loader's own error. Neither is
	// set when RootErr is. Every check that reads the file reads Config —
	// nothing here opens it.
	Config    *config.Config
	ConfigErr error
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
	// HooksDir / HooksErr are git's answer to where this checkout's hooks live
	// (core.hooksPath relocates them, so it must be ASKED, not assumed) and the
	// failure to get one. Resolved by the caller for the same reason RepoObject
	// is: the caller runs doctor's subprocesses, as it makes its requests. A nil
	// error with an empty directory cannot happen — git names one or fails.
	HooksDir string
	HooksErr error
	// CommitMsgProbe is the outcome of the caller FIRING the installed
	// commit-msg hook with a violating message — executed by internal/cli for
	// the same reason HooksDir is resolved there, and only when the installed
	// bytes are exactly this binary's hook (see probeCommitMsgHook). nil means
	// nothing was fired.
	CommitMsgProbe *HookProbe
	// Tags / TagsErr are git's tag list for the checkout and the failure to
	// get one — resolved by the caller like HooksDir, read by the root-line
	// check alone.
	Tags    []string
	TagsErr error
	// HeadTrees / HeadTreesErr are every directory HEAD records
	// (gitsource.HeadTrees) and the failure to list them — resolved by the
	// caller like HooksDir, read by the package-paths check alone, which
	// judges a declared path the way attribution reads it: as git's path.
	HeadTrees    []string
	HeadTreesErr error
	// ActionFiles / ActionFilesErr are every action.yml / action.yaml git
	// counts as part of the checkout (gitsource.ActionFiles, relative to Root)
	// and the failure to list them — resolved by the caller like HooksDir,
	// read by the pin check, which never walks the filesystem for them.
	ActionFiles    []gitsource.ActionFile
	ActionFilesErr error
	// GlyphVersion is the running binary's version as version.Resolve reports
	// it. The two caller checks judge a caller at the release it pins, and a
	// stamped build cannot speak for a release newer than its own (judgeAt);
	// `dev`, a pseudo-version or a git-describe stamp has no upper end.
	GlyphVersion string
	// RemoteHeads / RemoteHeadsErr are, for each configured remote, the
	// default branch this clone records (refs/remotes/<remote>/HEAD, "" when
	// none — gitsource.DefaultBranch, a local ref, never the network) and the
	// failure to read them. The caller reads them only when the installed
	// pre-push hook is byte-identical, the one arm whose pass they qualify;
	// nil with a nil error means not read.
	RemoteHeads    map[string]string
	RemoteHeadsErr error
	// RemoteRefless marks, among the remotes RemoteHeads reports unrecorded,
	// those whose name git cannot place in a ref
	// (gitsource.RemoteCanRecordHead): set-head is no remedy for them.
	RemoteRefless map[string]bool
}

// HookProbe is what came back from firing a hook. Fired with Exit is a real
// answer; Err is the probe itself failing to run — which is unknown, never a
// verdict about the hook. Claimed reports whether the repository's own
// glyph.toml classifies the probe message: lint has no taste, so a config is
// free to claim anything — and against one that does, exit 0 is the chain
// working, not the gate failing.
type HookProbe struct {
	Fired   bool
	Exit    int
	Claimed bool
	Err     error
}

// The stable check ids. The strings are the report's API — change one and
// every CI gate branching on it silently stops matching, so treat them as the
// versioned surface they are; the Go names are nobody's.
const (
	idConfigLoads    = "glyph-toml-loads"
	idPackagePaths   = "package-paths-exist"
	idRootLineTags   = "root-line-tags"
	idTokenAccess    = "token-repo-read"
	idTokenWrite     = "token-repo-write"
	idSquashEnabled  = "squash-merge-enabled"
	idMergeCommit    = "merge-commit-enabled"
	idRebaseMerge    = "rebase-merge-enabled"
	idSquashTitle    = "squash-commit-title"
	idSquashMessage  = "squash-commit-message"
	idWorkflowPinned = "workflow-glyph-pins"
	idCallerPerms    = "workflow-caller-permissions"
	idCallerInputs   = "workflow-caller-inputs"
	idCommitMsgHook  = "commit-msg-hook"
	idCommitMsgFires = "commit-msg-hook-fires"
	idPrePushHook    = "pre-push-hook"
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
// Check order is the report order and is chosen for reading: glyph.toml
// first, because it is the precondition every verdict command reads before
// anything else — with it down nothing else glyph does can run, however
// healthy the rest of the report is — and the two checks that read the loaded
// file, the package paths it declares and the root line's tags, right behind
// it. Then the token checks, because the read explains every settings
// check's could-not-run below it (and the write advisory rides on the
// same response), then the three merge methods, then the squash policy they
// enable, then the remaining LOCAL checks — the workflow pins, the two caller
// checks and the three hook checks — which, like the config, need no network
// at all, so they still answer when the API side is entirely dark.
func Run(in Input) *Report {
	// The fired hook's lint reads the same glyph.toml, so the config verdict is
	// an input to the live-fire check — see checkHookFires.
	config := checkConfig(in.Root, in.RootErr, in.Config, in.ConfigErr)
	rootVerified := in.RootErr == nil
	r := &Report{Repo: in.Repo, Checks: []Check{
		config,
		checkPackagePaths(in.Root, in.Config, in.HeadTrees, in.HeadTreesErr),
		checkRootLineTags(in.Config, in.Tags, in.TagsErr),
		checkTokenAccess(in),
		checkTokenWrite(in),
		checkSquashEnabled(in),
		checkMergeCommit(in),
		checkRebaseMerge(in),
		checkSquashTitle(in),
		checkSquashMessage(in),
		checkWorkflowPins(in.Root, rootVerified, in.ActionFiles, in.ActionFilesErr),
		checkCallerPermissions(in.Root, rootVerified, in.GlyphVersion),
		checkCallerInputs(in.Root, rootVerified, in.GlyphVersion),
		checkHook(hook.Kinds()[0], idCommitMsgHook, in.HooksDir, in.HooksErr),
		checkHookFires(in.CommitMsgProbe, in.HooksErr, config.Status == StatusPass),
		checkPrePushHook(in.HooksDir, in.HooksErr, in.CommitMsgProbe, in.RemoteHeads, in.RemoteRefless, in.RemoteHeadsErr),
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
// A read failure splits on what GitHub answered (github.IsRepoUnknown): a 404
// is a finding (StatusFail, exit 3), and anything else observed nothing about
// the repository (StatusUnknown, exit 4). The split, and the outage that
// collapsing it misreported to the fleet's CI wrappers: DESIGN §7.
func checkTokenAccess(in Input) Check {
	c := Check{ID: idTokenAccess, Expected: "the credential can read " + in.Repo}
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

// checkTokenWrite asks whether the credential could WRITE the rolling draft —
// the one thing `glyph release` does that every read command does not. A push
// grant passes and anything less is ADVICE, never a failure (DESIGN §7); a
// repository object that was never read degrades to unknown exactly like
// every other check fed by that read. The verdict branches only on what the
// API SAID (the same reported block permissionSummary narrates): glyph never
// infers a scope the API did not report — see github.Repo.Permissions.
func checkTokenWrite(in Input) Check {
	c := Check{ID: idTokenWrite, Expected: "the credential can write releases in " + in.Repo + " (needed only by `glyph release`)"}
	if in.RepoErr != nil {
		c.Status = StatusUnknown
		c.Observed = "the repository object was never read (see " + idTokenAccess + "), so the API reported nothing about write access"
		c.Message = "unverified, not a verdict — this check reads the same response as " + idTokenAccess + " and answers with it"
		c.Fix = "resolve " + idTokenAccess + " and re-run"
		return c
	}
	c.Observed = permissionSummary(in.RepoObject.Permissions)
	if p := in.RepoObject.Permissions; p != nil && p.Push {
		c.Status = StatusPass
		c.Message = "the draft upsert `glyph release` performs would be authorised"
		return c
	}
	c.Status = StatusAdvice
	c.Message = "every read command is unaffected — which is why this is advice, not a failure — but `glyph release` " +
		"writes the rolling draft over the API, and this credential's write would be refused (403) only after the " +
		"walk has already spent its API budget. On a repository that never runs release there is nothing to do"
	c.Fix = "for a release repository: run with a credential that can write — in Actions, `permissions: contents: write` " +
		"on the release caller (the reusable's stub carries it); locally, a token with repo write access"
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

// checkSquashEnabled fails when squash merging is off, because squash is the
// one landing style with no partial state: a squash-merged pull puts exactly
// ONE commit on main and that commit IS its merge_commit_sha, so the walk
// either expands the whole pull from the API or falls back on that one
// message. Every multi-commit landing splits those two states. Why that makes
// this a failure while allowing another method is advice (checkMergeCommit,
// checkRebaseMerge), and what squash itself still costs with the API dark:
// DESIGN §7. With squash off, the two squash policy checks describe a commit
// this repository can no longer produce.
func checkSquashEnabled(in Input) Check {
	c := Check{ID: idSquashEnabled, Expected: "allow_squash_merge=true"}
	if u, ok := available(in, c, "allow_squash_merge", in.RepoObject.AllowSquashMerge); !ok {
		return u
	}
	allowed := *in.RepoObject.AllowSquashMerge
	c.Observed = fmt.Sprintf("allow_squash_merge=%t", allowed)
	if allowed {
		c.Status = StatusPass
		c.Message = "PRs can land as one squash commit: the pull request's merge_commit_sha and the ONLY commit it puts on " +
			"main are the same object, so a squash-merged pull is never half-resolved — the walk either expands it from the " +
			"API or falls back on that one message, and no part of it can stand aside for a merge point that never resolves. " +
			"That is the guarantee; identical verdicts are NOT. Under COMMIT_OR_PR_TITLE a MULTI-commit squash carries the PR " +
			"title, whose sigil is its own, so an API-dark run classifies that one subject instead of the pull's commits: a " +
			"^+~ pull titled ~ reads minor with the API and patch without, and a title no pattern claims is dropped from the " +
			"fold with a warning, so the pull counts none. Squash bounds an API-dark window to one wrong level on one pull; " +
			"a multi-commit landing can lose a whole one"
		return c
	}
	c.Status = StatusFail
	c.Message = "squash merging is OFF, so every PR must land as a merge commit or a rebase merge — landings that put MANY " +
		"commits on main for one pull request, which is where the walk acquires a window it otherwise does not have. While " +
		"GitHub answers, all three styles resolve alike: merge_commit_sha names the squash commit, a rebase's last replayed " +
		"commit, or the merge commit, and the walk expands the pull request from there. A TOTAL blackout is not the problem " +
		"either — when GitHub answers 422 for every sha the walk classifies each commit from its own message (DESIGN §4), and " +
		"a merge- or rebase-landed pull comes out at the SAME level, because its commits are on main with their sigils " +
		"intact; a multi-commit squash is the style that loses there. What squash removes is the PARTIAL window, which only a " +
		"multi-commit landing has. GitHub indexes a merge commit AFTER the commits it merges, so a release run seconds behind " +
		"the button sees the branch commits stand aside for a merge point it cannot resolve yet and the whole pull request " +
		"counts none. The same shape is PERMANENT when an automation authors the merge commit: the author gate skips it " +
		"before the API, so nothing ever resolves the pull. A rebase writes new shas that appear in no pull request's commit " +
		"listing, so it depends on the walk aligning that listing against the run of commits ending at the canonical one; " +
		"where the alignment cannot be verified — a rebase that dropped a commit it was asked to replay, one already " +
		"upstream or one that rebased empty — a replayed commit classified during the lag is folded in a second time and " +
		"the same change renders twice, with nothing that says so. " +
		"A squash-merged pull has exactly one commit on main and it IS its merge point, so none of those states can exist. " +
		"Allowing another method is a convention; leaving squash off makes every pull take a landing that has them"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_squash_merge=true", in.Repo)
	return c
}

// checkMergeCommit reports allow_merge_commit at ADVICE: the release walk's
// fix for t-7zt7 resolves and expands a merge commit exactly like a squash
// commit, so what the setting still costs is the squash-only house convention
// plus the partial window, where an unresolved merge point loses its whole
// pull out loud (DESIGN §7).
//
// The coupling runs the other way too: this severity is downstream of that fix.
// If the merge-commit walk is ever reverted or regresses, this check must move
// back to StatusFail with it — they are one decision, not two.
//
// There is a SECOND coupling, and it is the quieter one: advice is only defensible
// because the partial window is announced. The reconciliation warning at the end
// of the walk (internal/walk, walk.reconcile) is what turns "this release is
// short by a whole pull request" into something a human sees, and it fires on EVERY release
// of a repository whose merge button an automation presses — exactly the kind of
// recurring noise somebody eventually silences. Quiet that warning and this
// setting becomes a silent total loss; the severity must move to StatusFail with
// it, for the same reason as the coupling above.
func checkMergeCommit(in Input) Check {
	c := Check{ID: idMergeCommit, Expected: "allow_merge_commit=false (house convention: squash-only)"}
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
	c.Message = "merge commits are allowed. The unconditional data loss is gone — the release walk resolves and expands a " +
		"merge commit like a squash commit, and even with the API fully dark the merged branch's commits are in the range and " +
		"carry their own gitmoji, so the verdict comes out the same. What is left is a window and a convention. The window: a " +
		"merge-merged pull's branch commits stand aside for its merge point, so if that merge point alone is unresolved — " +
		"GitHub indexes it after the commits it merges, or an automation authored it and the author gate skipped it before " +
		"the API — the whole pull counts none. The walk names the lost pull, and the release refuses the incomplete walk " +
		"at exit 4, so this is a re-run (or a " +
		"human merge) rather than a silent wrong version; for an automation-driven merge button it repeats every release. The " +
		"convention: squash-only keeps main at one commit per PR and the walk at one API round-trip per PR"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_merge_commit=false (optional — this is a convention, not a defect)", in.Repo)
	return c
}

// checkRebaseMerge reports allow_rebase_merge at ADVICE, the merge-commit
// convention's weight: the LAST replayed commit expands the whole pull request
// exactly as strictly as a squash commit (a subject no pattern claims fails the
// release through the walk's refusal, internal/walk), so rebase merging costs one API round-trip
// per replayed commit and a narrow lag window, not strictness (DESIGN §7).
func checkRebaseMerge(in Input) Check {
	c := Check{ID: idRebaseMerge, Expected: "allow_rebase_merge=false (house convention: squash-only)"}
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
		"commit resolves and expands the whole pull request through the API exactly as a squash commit does — a subject " +
		"inside it that no pattern claims fails the release, it is NOT downgraded to a warning — and the earlier replayed commits resolve as " +
		"covered by the same pull request and are skipped, so nothing is counted twice. What it costs is one API round-trip " +
		"per replayed commit instead of one per pull request. The dedup key used to cost more: a rebase always writes new " +
		"shas, which appear in no pull request's commit listing, so inside the API-lag window a replayed commit was folded " +
		"in twice. The walk now aligns the listing against the run of commits ending at the canonical one and counts it once " +
		"(t-8xsb); what remains is a rebase that cannot be aligned — one that dropped a commit it was asked to replay, " +
		"already upstream or rebased empty — where the double fold survives"
	c.Fix = fmt.Sprintf("gh api -X PATCH repos/%s -F allow_rebase_merge=false (optional — this is a convention, not a defect)", in.Repo)
	return c
}

// checkSquashTitle fails unless squash_merge_commit_title is
// COMMIT_OR_PR_TITLE: the setting decides whether the commit that lands on main
// carries the subject its author wrote, sigil included, and only
// COMMIT_OR_PR_TITLE keeps a single-commit PR's own subject. Why PR_TITLE is a
// failure: DESIGN §7.
func checkSquashTitle(in Input) Check {
	return checkSquashEnum(in, idSquashTitle, "squash_merge_commit_title",
		in.RepoObject.SquashMergeCommitTitle, wantSquashTitle,
		"a single-commit PR keeps its own classifiable subject on main; only a multi-commit squash borrows the PR title",
		"every squash commit takes the PR TITLE as its subject, including single-commit PRs — so a commit's own sigil "+
			"never lands on main. The verdict holds while the API answers, but the walk's fallback "+
			"(the API lag right after a push) classifies the squash commit's own message: the title's sigil "+
			"alone decides, and a title no pattern claims is dropped from the fold with a warning — bump says no release "+
			"and release refuses the incomplete walk at exit 4")
}

// checkSquashMessage is the body half of the same policy: COMMIT_MESSAGES
// keeps the per-commit message list in the squash body, the only offline
// record of a PR's pre-squash types. glyph does not depend on that body
// (DESIGN §4 keeps it only as a drift alarm); why PR_BODY is a failure all the
// same: DESIGN §7.
func checkSquashMessage(in Input) Check {
	return checkSquashEnum(in, idSquashMessage, "squash_merge_commit_message",
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
	c.Message = "this setting is unverified, not verified-good: " + field + " was never observed. See the " + idTokenAccess + " check"
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
