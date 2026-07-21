package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

// This file is the release-time walk — the third input source beside --range
// (pure git) and --pr (pure API), combining both: it walks main's merge points
// since a tag out of LOCAL git — a squash commit, a rebase-merge's last commit,
// or the merge commit the merge button writes, whichever GitHub named
// merge_commit_sha — resolves each to the pull request that merged it over the
// API, and expands that PR into its individual (pre-squash) commits. Nothing is
// persisted — the walk recomputes from git every run, so a release is
// idempotent and self-healing (DESIGN §4).

// sinceTagAuto is what a bare --since-tag parses to (the flag's NoOptDefVal):
// resolve the walk base from the repository itself — the highest parseable v*
// tag — so a release job never duplicates glyph's version-tag policy in shell.
const sinceTagAuto = "auto"

// addSinceTagFlag wires --since-tag onto a command. The value is optional
// (pflag's NoOptDefVal grammar): a bare --since-tag resolves the tag itself; a
// named tag must be attached with = (--since-tag=v1.2.3).
func addSinceTagFlag(cmd *cobra.Command, target *string, verb string) {
	cmd.Flags().StringVar(target, "since-tag", "",
		verb+" every merged PR's individual (pre-squash) commits on main since a tag (bare --since-tag: the highest v* tag; use --since-tag=TAG to name one)")
	cmd.Flags().Lookup("since-tag").NoOptDefVal = sinceTagAuto
}

// markInputSourceFlags is the source-selection grammar bump and notes share:
// exactly one of --range / --pr / --since-tag, and --repo only with the
// API-backed sources — combined with the purely local --range it would be
// silently ignored, and glyph does not ignore input silently.
func markInputSourceFlags(cmd *cobra.Command) {
	cmd.MarkFlagsOneRequired("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "repo")
}

// sinceTagArgs is the Args guard for the commands carrying --since-tag: they
// take no positionals, and the one stray positional users actually produce is
// the space form of the flag's optional value (`--since-tag v1.2.3`), which
// pflag parses as a bare --since-tag plus a leftover. Walking the WRONG range
// silently is the worst outcome, so that shape gets a usage error spelling out
// the = form instead of cobra's generic unknown-command complaint.
func sinceTagArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && cmd.Flags().Changed("since-tag") {
		if tag, _ := cmd.Flags().GetString("since-tag"); tag == sinceTagAuto {
			return core.Usagef("--since-tag takes its tag attached with '=': --since-tag=%s", args[0])
		}
	}
	return cobra.NoArgs(cmd, args)
}

// checkSinceTagFlag rejects a malformed explicit tag before anything runs —
// caller input, so usage (2), mirroring checkRangeFlag: the same mistake must
// not surface as a retryable-looking git failure (4). The auto sentinel (a
// bare --since-tag) is not a name and skips the checks.
func checkSinceTagFlag(tag string) error {
	if tag == sinceTagAuto {
		return nil
	}
	if strings.TrimSpace(tag) == "" {
		// A workflow templating an unset variable produces exactly this; auto
		// is spelled by OMITTING the value, never by an empty one.
		return core.Usagef("--since-tag= names an empty tag — use a bare --since-tag for the highest v* tag, or name one with --since-tag=TAG")
	}
	if strings.HasPrefix(tag, "-") {
		return core.Usagef("--since-tag %q looks like an option, not a tag", tag)
	}
	if strings.Contains(tag, "..") {
		return core.Usagef("--since-tag %q looks like a revision range — name the tag alone; the walk appends ..HEAD itself", tag)
	}
	return nil
}

// sinceTagInput resolves the repository, the walk range and the version base,
// then walks. bump, notes and release share it; the returned source names the
// range for the reason line, base (when the tag names a version) is what the
// bump steps from, and pulls is the walk's expansion provenance (release
// reports it, the others discard it).
func sinceTagInput(ctx context.Context, table *gitmoji.Table, tagFlag, repoFlag string) ([]parser.Commit, []pullExpansion, string, *bump.Version, error) {
	if err := checkSinceTagFlag(tagFlag); err != nil {
		return nil, nil, "", nil, err
	}
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return nil, nil, "", nil, err
	}
	revRange, base, err := sinceTagRange(ctx, tagFlag)
	if err != nil {
		return nil, nil, "", nil, err
	}
	commits, pulls, err := walkSince(ctx, newGitHub(), table, owner, repo, revRange)
	return commits, pulls, revRange, base, err
}

// sinceTagRange turns the --since-tag value into a git revision range and, when
// the tag names a version, the version the bump steps from. Both come from ONE
// resolution, so the walk base and the step base are the same tag by
// construction — naming a tag names the release being redone; stepping from a
// different (higher) tag would version a verdict computed over another range.
// Auto resolves the highest parseable v* tag; a repository before its first
// release walks the whole history and steps from v0.0.0. An explicit tag that
// is not a version still walks, but names no base (nil — the bump falls back
// to the highest v* tag).
func sinceTagRange(ctx context.Context, tagFlag string) (revRange string, base *bump.Version, err error) {
	tag := strings.TrimSpace(tagFlag)
	if tag == sinceTagAuto {
		latest, v, lerr := latestVersionTag(ctx)
		if lerr != nil {
			return "", nil, lerr
		}
		if latest == "" {
			// One API round-trip per commit over everything — a cost the
			// caller should see named (house rule: no silent unbounded work).
			warnf("no version tag found — walking the repository's whole history")
			return "HEAD", &bump.Version{}, nil
		}
		return latest + "..HEAD", &v, nil
	}
	if v, perr := bump.ParseVersion(tag); perr == nil {
		return tag + "..HEAD", &v, nil
	}
	return tag + "..HEAD", nil, nil
}

// latestVersionTag returns the highest parseable version tag and its parsed
// version; tag is empty for a repository before its first release. The one
// resolver behind both the walk base and the bump base.
func latestVersionTag(ctx context.Context) (tag string, v bump.Version, err error) {
	tags, terr := gitsource.Tags(ctx, ".")
	if terr != nil {
		return "", bump.Version{}, terr
	}
	for _, t := range tags {
		if pv, perr := bump.ParseVersion(t); perr == nil {
			return t, pv, nil
		}
	}
	return "", bump.Version{}, nil
}

// pullExpansion records one merged pull request the walk expanded — resolved
// from its canonical commit — and how many participating commits it
// contributed, after the walk-wide SHA dedup: a stacked PR whose commits all
// rode in with its base PR reports 0, and so does a merge-merged PR whose
// commits the walk already folded in on the fallback path. This is the walk
// reporting its own expansion facts, and the shadow comparison (ratified Q6)
// branches on them instead of re-implementing the walk's exclusion rules in
// shell.
//
// What 2+ means depends on the merge style, so the count is an UPPER BOUND on
// divergence rather than a predicate for it. For a SQUASH-merged pull,
// git-cliff (and any main-history reader) sees only the squash subject, so 2+
// contributed commits means a divergence is expected — COMMIT_OR_PR_TITLE
// replaced that subject with the PR title, and the per-commit types exist
// nowhere on main. For a MERGE-COMMIT-merged pull the same commits ARE on main,
// messages intact, so a text reader reads exactly what the expansion did and
// agrees. Nothing here distinguishes the two shapes, so a comparison branching
// on 2+ can raise a false alarm on a merge-merged pull — never miss a real
// divergence, which is the direction that matters.
type pullExpansion struct {
	Number  int `json:"number"`
	Commits int `json:"commits"`
}

// walkSince walks the range's commits oldest first and folds every merged PR's
// individual commits into one participating list, recording per-pull expansion
// provenance alongside. Author-excluded commits are skipped before any API call
// — the routine fleet-sync direct push never costs a request. Everything else
// is asked about: a commit's own shape cannot tell a pull request's merge point
// from a local merge, so resolution answers that, and the unresolved ones fall
// through to fallbackCommit, which applies the message rules. A last pass
// reconciles the pulls commits stood aside for against the pulls actually
// expanded, so a merged pull request whose canonical commit never resolved is
// named in a warning instead of silently lost.
func walkSince(ctx context.Context, c *github.Client, table *gitmoji.Table, owner, repo, revRange string) ([]parser.Commit, []pullExpansion, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, nil, err
	}
	var commits []parser.Commit
	// Normalized to [] up front so the JSON surface never emits null — a
	// shadow script indexes .pulls unconditionally.
	pulls := []pullExpansion{}
	// seen holds every SHA already REPRESENTED in the fold. One commit can be
	// reachable from TWO merged PRs — a stacked branch carries its base PR's
	// pre-squash commits, so after both squash-merge, both listings contain
	// them — and without this set each occurrence would land in the notes and
	// the fold once per PR. A merge-merged PR is the other producer: its commits
	// sit on main under the SHAs the PR listing reports, so the walk meets each
	// of them twice (once as a walked commit, once inside the expansion). The
	// walk runs oldest first, so a shared commit is attributed to whatever first
	// landed it on main — and git's revision walk reaches a parent only through
	// its child, so a merge point is always walked AFTER the commits it merges,
	// whatever the commit clocks say.
	//
	// "Represented" is the whole membership rule, so it holds the CANONICAL
	// commit of every resolved pull too, not only the inner commits that pull
	// expanded into (see the found arm). Both are equally already-counted, and
	// both come back around: a pull squash-merged into a topic branch leaves its
	// squash commit inside the listing of the pull that later landed that
	// branch.
	seen := map[string]bool{}
	// inRange is every SHA this walk will visit. It is what makes "covered"
	// mean something checkable: a commit stands aside only for a pull whose
	// canonical commit is IN HERE, so a pull left unexpanded at the end is a
	// resolution failure and never a pull that lives in some other history. A
	// pull merged into a DIFFERENT base branch (a backport line, a fork) is
	// associated with commits that reached main another way while its own merge
	// point is nowhere on main — deferring to it would drop those commits, and
	// expanding it would fold a branch main never took.
	inRange := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if raw.SHA != "" {
			inRange[raw.SHA] = true
		}
	}
	// The covered ledger, in walk order, and the pulls actually expanded. A
	// commit that reports itself covered by pull #N stands aside for #N's own
	// canonical commit — and if that commit never resolves, #N is a merged pull
	// request nothing in the walk represents. Every commit of it would then skip
	// itself and the release would lose it in silence, which is exactly the
	// t-7zt7 failure the fix exists to end (found by review, on this path). The
	// end of the walk reconciles the two sets.
	coveredPulls, coveredOrder, expanded := map[int]bool{}, []int{}, map[int]bool{}
	// foldPull expands one merged pull request into the walk, canonical being the
	// walked commit that resolved to it (its merge point): the listing is
	// filtered against seen BEFORE it is parsed (an already-represented commit
	// must never be able to fail the release — its message may be a squash
	// subject, which never parses), what remains is classified, and the pull's
	// contribution is recorded. Only a RESOLVED canonical commit may expand a
	// pull — the reconciliation at the end of the walk deliberately does not call
	// this (see there), because a listing fetched without a canonical commit in
	// range says nothing about which of its commits belong to this walk.
	foldPull := func(number int, canonical string) error {
		listing, perr := pullRawCommits(ctx, c, owner, repo, number)
		if perr != nil {
			return wedgeHint(perr, owner, repo, number, canonical)
		}
		fresh := make([]gitsource.RawCommit, 0, len(listing))
		for _, r := range listing {
			if r.SHA != "" && seen[r.SHA] {
				noticef("commit %.7s in pull request #%d is already represented in this walk (a stacked branch carries its base PR's pre-squash commits; a merge-merged PR's commits sit on main under these same SHAs; a sub-PR's squash commit rides inside the PR that landed its branch) — counted once", r.SHA, number)
				continue
			}
			fresh = append(fresh, r)
		}
		inner, perr := participating(fresh)
		if perr != nil {
			return wedgeHint(perr, owner, repo, number, canonical)
		}
		contributed := 0
		for _, ic := range inner {
			// Pre-flight the membership check that classifyVerdict would run
			// later anyway (same Classify, same table — it cannot disagree):
			// down there the PR association is gone, and a bare per-commit lint
			// line is uniquely unhelpful HERE — see wedgeHint.
			if _, cerr := bump.Classify(ic, table); cerr != nil {
				return wedgeHint(cerr, owner, repo, number, canonical)
			}
			if ic.SHA != "" {
				seen[ic.SHA] = true
			}
			commits = append(commits, ic)
			contributed++
		}
		pulls = append(pulls, pullExpansion{Number: number, Commits: contributed})
		expanded[number] = true
		return nil
	}
	walked, unknown := 0, 0
	for _, raw := range raws {
		// A merge point here is a POINTER to a pull request, not a message
		// being classified, so only its AUTHOR excludes it before resolution
		// (bump.ExcludedFromResolution spells out why that is the whole
		// evidence). Neither its subject nor its shape may: a `Revert "..."`
		// squash subject is a real revert PR that must resolve and expand, and
		// a two-parent commit is what the "Create a merge commit" button
		// writes — skipping it for that dropped the entire pull request out of
		// the release, silently (t-7zt7). Message rules apply where a message
		// IS classified: on the PR's inner commits (participating) and on the
		// fallback path (fallbackCommit), which still skips an unresolved
		// merge commit — a local `git merge` — exactly as before.
		if _, excluded := bump.ExcludedFromResolution(raw.Author); excluded {
			continue
		}
		walked++
		number, found, covering, rerr := mergedPullFor(ctx, c, owner, repo, raw.SHA, inRange)
		// Ledger first, whatever this commit resolves to: a commit can name the
		// pull that merged it AND a pull that merely carries it (a sub-PR's
		// squash commit is inside the branch of the pull that landed it), and
		// the reconciliation below needs both sightings.
		for _, n := range covering {
			if !coveredPulls[n] {
				coveredPulls[n] = true
				coveredOrder = append(coveredOrder, n)
			}
		}
		var reason fallbackReason
		switch {
		case github.IsCommitUnknown(rerr):
			// GitHub does not know the SHA yet (422) — the walk outran the API
			// right after a push. DESIGN §4's API lag: fall back, never fail.
			unknown++
			reason = fallbackReason{why: "is not known to GitHub yet (API lag)", lag: true}
		case rerr != nil:
			return nil, nil, rerr
		case found:
			if ferr := foldPull(number, raw.SHA); ferr != nil {
				return nil, nil, ferr
			}
			// The canonical commit is now represented BY that expansion, so it
			// joins the same set — recorded after the expansion, never before,
			// because a rebase-merge's merge_commit_sha is the last rebased
			// commit and appears in the listing it must still be folded from.
			if raw.SHA != "" {
				seen[raw.SHA] = true
			}
			continue
		case len(covering) > 0:
			// Part of a merged PR whose canonical (merge_commit_sha) commit is
			// in this very range and represents it: a rebase-merge's non-final
			// commits, and — now that a merge commit resolves — every commit a
			// merge-merged PR put on main beside its merge point (gitsource.Log
			// runs without --first-parent, so the walk sees both). That
			// canonical commit expands the whole PR, so counting this one too
			// would double it. If it never does, the ledger above names the pull
			// at the end — standing aside is only safe because something in the
			// walk was supposed to stand in, and when nothing did, a human has to
			// hear about it.
			continue
		default:
			reason = fallbackReason{why: "has no merged pull request (a direct push, or the API lagging)"}
		}
		if fc, ok := fallbackCommit(table, raw, reason); ok {
			// The same dedup as the PR path: a fast-forwarded branch puts a
			// PR's pre-squash commits on main under their original SHAs, so a
			// fallback can name a commit an expanded PR already contributed.
			if fc.SHA != "" && seen[fc.SHA] {
				noticef("commit %.7s was already folded in by a merged pull request — counted once", fc.SHA)
				continue
			}
			if fc.SHA != "" {
				seen[fc.SHA] = true
			}
			commits = append(commits, fc)
		}
	}
	// Reconcile the ledger: a pull the walk only ever saw from the INSIDE. Its
	// commits stood aside for a canonical commit that never resolved — GitHub
	// not knowing the merge commit yet (a release job runs seconds after the
	// merge, and the merge commit is indexed after the commits it merges), or an
	// automation having authored it (the author gate skips it before the API).
	//
	// WARN, and deliberately do NOT expand. An earlier round of t-7zt7 folded the
	// pull's whole listing in here, and it was unsound for one reason: a pull's
	// API listing is the pull's ENTIRE history and carries nothing about this
	// walk's range, which is a git fact the listing never saw. Expanding it is
	// therefore guessing which of the pull's commits belong to THIS release, and
	// review broke that guess twice —
	//
	//   - double counting: a rebase-merged pull lists its ORIGINAL pre-rebase
	//     SHAs, which can never equal the main-branch SHAs seen holds, so the
	//     filter above passes them and the same change renders twice;
	//   - resurrecting released work: a pull whose earlier commits shipped under
	//     the previous tag has them folded straight back in — a minor bump
	//     manufactured out of a commit released one tag ago, announced by nothing.
	//
	// Both are the silent-wrong-verdict class this file exists to kill, so the
	// honest answer is to say what was lost and refuse to guess: a named warning
	// turns a silent loss (exit 1, `no release`, not one diagnostic — the t-7zt7
	// silence surviving on the new path) into a visible one. A warning on every
	// normal release would be worse than none, and this one cannot fire on a
	// healthy repository: standing aside requires the pull's canonical commit to
	// be IN this range, and a canonical commit in range that resolves is expanded
	// right there.
	for _, number := range coveredOrder {
		if expanded[number] {
			continue
		}
		warnf("pull request #%d has commits in %s but nothing in the range resolved to it — GitHub does not associate its merge commit with the pull yet, or an automation authored that merge; those commits stood aside for that merge point and are NOT counted, so this release is short by whatever #%d changed. The pull's own commit listing is its whole history and cannot say which commits belong to %s, so the walk will not guess — re-run the release once GitHub has caught up with the merge point", number, revRange, number, revRange)
	}
	if walked > 0 && unknown == walked {
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", walked, revRange, owner, repo)
	}
	return commits, pulls, nil
}

// gitmojiBoom is the canonical breaking gitmoji — what an unknown-code
// breaking fallback is normalized to so the shared verdict and notes
// plumbing (which hard-errors on unknown codes, by design) can carry it.
const gitmojiBoom = ":boom:"

// fallbackReason is why a walked commit reached the fallback path: the headline
// every fallback warning splices in, plus whether the API simply did not know
// the commit YET (422). The lag flag is not decoration — it decides whether an
// EXCLUDED commit still gets a diagnostic (see fallbackCommit), and a plain
// string could not carry that: the walk computed "is not known to GitHub yet"
// and then threw it away for the one commit shape where it mattered most.
type fallbackReason struct {
	why string
	lag bool
}

// fallbackCommit handles a walked commit that cannot be resolved to a merged
// pull request — no association (a direct push to main, or a local `git merge`
// of a topic branch), or GitHub not knowing the SHA yet. It is also where the
// message rules the resolution question deliberately skipped are applied, so an
// unresolved merge commit is excluded HERE, on its parent count, instead of
// before the lookup. Fallbacks never hard-fail a release (DESIGN §4), so every
// outcome is at most a ::warning:: carrying the reason's why (the caller's
// headline) plus the softest sound decision: an excluded message (a merge commit
// no PR explains, a raw git revert, an autosquash artifact) is skipped as the
// --range walk skips it — silently, unless it is a merge commit excluded while
// the API did not know it, which is a lost merge point and says so; a message
// that parses to a KNOWN gitmoji is classified as itself; a message that does
// not parse, or whose gitmoji is unknown (the ratified t-kbqx policy — this
// assembly layer downgrades what the lint gate keeps as a hard error, so
// internal/bump stays pure), counts none by being left out of the fold. One
// exception, ratified Q10: a breaking marker is NEVER
// suppressed — an unknown code carrying one (`!` or a BREAKING CHANGE footer,
// already parsed onto c.Breaking) counts major, normalized to :boom: so it
// folds and hoists into Breaking Changes downstream. The asymmetry is
// deliberate: a typo can over-bump a version, but a breaking change must
// never be silently dropped from one.
func fallbackCommit(table *gitmoji.Table, raw gitsource.RawCommit, reason fallbackReason) (parser.Commit, bool) {
	if _, excluded := bump.ExcludedFromClassification(raw.Author, firstLine(raw.Message), raw.Parents); excluded {
		if reason.lag && raw.Parents >= 2 {
			// The one exclusion that can cost a whole pull request, so it is
			// the one that must not be silent: a merge commit GitHub has not
			// indexed yet is a merge point the walk could not resolve, and its
			// own message never says what it merged. The lag was computed one
			// step up and then discarded here — the t-7zt7 silence in
			// miniature. (An unresolved merge commit whose API answer was a
			// definite "no pull request" is a local `git merge` and stays
			// silent, exactly as the --range walk skips it.)
			warnf("merge commit %.7s %s, so the pull request it may have merged could not be resolved — its own message is not classifiable, so nothing was counted from it; if the release looks short, re-run it once GitHub has indexed the commit", raw.SHA, reason.why)
		}
		return parser.Commit{}, false // skipped, never a violation
	}
	c, perr := parseRaw(raw)
	if perr != nil {
		warnf("commit %.7s %s and its own message does not parse (%v) — counted as none", raw.SHA, reason.why, perr)
		return parser.Commit{}, false
	}
	if _, cerr := bump.Classify(c, table); cerr != nil {
		if c.Breaking {
			warnf("commit %.7s %s and its gitmoji %s is not in the rules table, but it carries a breaking marker — counted as a breaking change (%s, major)", raw.SHA, reason.why, c.Gitmoji, gitmojiBoom)
			c.Gitmoji = gitmojiBoom
			return c, true
		}
		warnf("commit %.7s %s and its gitmoji %s is not in the rules table — counted as none", raw.SHA, reason.why, c.Gitmoji)
		return parser.Commit{}, false
	}
	warnf("commit %.7s %s — classifying its own message", raw.SHA, reason.why)
	return c, true
}

// wedgeHint decorates a lint failure surfaced while expanding a merged PR on
// the release walk (ratified Q1/t-2nzf: such failures stay hard — never a
// silent patch). A bare per-commit lint line is uniquely unhelpful here: the
// caller never named a PR (the walk resolved it), and published history is
// immutable, so the SAME failure — a commit that bypassed the lint gate —
// wedges every future release until the walk range moves past it. The message
// therefore names the pull request and the escape. Non-lint failures pass
// through untouched.
//
// "Published history", not "squash history": since a merge commit resolves
// (t-7zt7), the offending commit is just as often one a merge-merged PR put on
// main under its own SHA. Neither shape is rewritable on a branch other repos
// have pulled — but a message that named only squashes read as inapplicable to
// exactly the repositories where this newly happens, which is the worst moment
// to sound like someone else's problem.
//
// The escape is stated against the pull's MERGE POINT, never against the
// offending commit, and since the other shape became reachable that is the whole
// difference between an open door and a closed one: foldPull re-fetches and
// re-lints the pull's ENTIRE listing whenever its merge point is in range, so
// for a merge-merged pull a base cut past the offending commit alone — what this
// hint used to advise — wedges again (verified: tag AT the offending commit and
// tag strictly past it both still exit 3; only a base at or past the merge
// commit exits 0). A squash-merged pull has exactly ONE commit on main and it IS
// its merge point, so the two readings coincide there — which is why the old
// wording was right until this walk started resolving merge commits.
func wedgeHint(err error, owner, repo string, number int, mergePoint string) error {
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeLint {
		return err
	}
	return &core.Error{Code: core.CodeLint, Details: ce.Details, Msg: fmt.Sprintf(
		"%s — inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s; the commit is already on a published branch and cannot be rewritten, so every release wedges here until the walk no longer reaches that merge point. Expanding a pull re-reads its WHOLE commit listing, so a base inside the pull does not help even when it sits past the offending commit: the walk must start AT OR PAST %.7s — cut a release tag there by hand, or name such a tag with an explicit --since-tag=TAG (DESIGN §4)",
		ce.Msg, owner, repo, number, mergePoint, mergePoint)}
}

// mergedPullFor resolves a merge point on main to the pull request that merged
// it: a commit can be ASSOCIATED with many PRs (a revert, a mention), so the
// match is MergeCommitSHA == sha and actually merged — never list order. One
// equality serves every merge style, because GitHub points merge_commit_sha at
// whichever commit represents the merge: the squash commit, the last rebased
// commit, or — for the merge button — the merge commit itself.
//
// covering reports the other side of that equality, by NUMBER and not merely as
// a flag: every merged pull that carries this commit while some OTHER sha
// represents it (a rebase-merge's earlier commits, a merge-merged PR's branch
// commits, a sub-PR's squash commit riding inside the branch of the pull that
// landed it). Such a commit stands aside for that pull's canonical commit — so
// the walk must be able to tell, at the end, whether that canonical commit ever
// turned up. The numbers are what make that reconciliation possible; a bare
// bool let a merged pull request disappear whenever its merge point failed to
// resolve (t-7zt7). Both results can be non-empty at once, and both are used.
//
// inRange gates covering on the canonical commit being one this walk visits.
// Without that gate "covered" is not a claim about this release at all: a pull
// merged into ANOTHER base branch is associated with commits that reached main
// by a different route, and its merge point is on a history main never took —
// standing aside for it would drop a commit from the release, and rescuing it
// would fold in a branch that was never merged here.
func mergedPullFor(ctx context.Context, c *github.Client, owner, repo, sha string, inRange map[string]bool) (number int, found bool, covering []int, err error) {
	pulls, err := c.CommitPulls(ctx, owner, repo, sha)
	if err != nil {
		return 0, false, nil, err
	}
	for _, p := range pulls {
		if p.MergedAt == "" {
			continue
		}
		if p.MergeCommitSHA == sha {
			if !found {
				// First match wins, as before this returned early: list order is
				// GitHub's and the verdict must not start depending on it.
				number, found = p.Number, true
			}
			continue
		}
		if inRange[p.MergeCommitSHA] {
			covering = append(covering, p.Number)
		}
	}
	return number, found, covering, nil
}
