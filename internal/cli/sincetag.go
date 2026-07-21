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
// bump steps from, and the walk's own facts come back with the commits — its
// expansion provenance AND whether it could read the range at all (release
// reports them, the others discard them).
func sinceTagInput(ctx context.Context, table *gitmoji.Table, tagFlag, repoFlag string) ([]parser.Commit, walkFacts, string, *bump.Version, error) {
	if err := checkSinceTagFlag(tagFlag); err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	revRange, base, err := sinceTagRange(ctx, tagFlag)
	if err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	commits, facts, err := walkSince(ctx, newGitHub(), table, owner, repo, revRange)
	return commits, facts, revRange, base, err
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
//
// EVERY tag is parsed and compared as a VERSION. The order git reports is not
// one, and this used to take git's first parseable entry: `--sort=-v:refname`
// is a REFNAME sort that happens to compare digit runs numerically, so it
// orders on the leading byte first and every `refs/tags/v…` lands above every
// `refs/tags/1…` ('v' > '1'). ParseVersion accepts a tag WITHOUT the v (its
// pattern is ^v?…), so both spellings reach this loop, and measured on tags
// {v0.0.1, v0.0.2, 9.9.9, 100.0.0} git reports v0.0.2 first — the resolver
// answered v0.0.2 where the highest version is 100.0.0, moving the walk base
// and the version base together to a tag two releases behind. A wrong base is
// the silent-wrong-verdict class: the walk re-folds released commits and the
// bump steps from the wrong number, with nothing on stderr.
//
// A tie — the same version spelled twice, v1.2.3 beside 1.2.3 — keeps the
// FIRST in git's order, so the answer stays deterministic without inventing a
// preference between two tags git considers equally valid.
func latestVersionTag(ctx context.Context) (tag string, v bump.Version, err error) {
	tags, terr := gitsource.Tags(ctx, ".")
	if terr != nil {
		return "", bump.Version{}, terr
	}
	for _, t := range tags {
		pv, perr := bump.ParseVersion(t)
		if perr != nil {
			continue
		}
		if tag == "" || pv.Compare(v) > 0 {
			tag, v = t, pv
		}
	}
	return tag, v, nil
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

// walkFacts is what the walk observed about ITSELF, beside the commits it
// folded: the expansion provenance the verdict reports, and whether the walk
// came back knowing it had NOT read the whole range.
//
// That second half exists because downstream the two are indistinguishable — a
// range that genuinely holds nothing and a range the walk could not read both
// arrive as an empty fold — and glyph acted on the difference: it deleted the
// rolling draft on a verdict it had just told the operator to re-run, and it
// wrote a draft whose version was computed without the pull it had just
// reported missing. The walk already warns about every one of these; this is
// the same fact in a form a decision can be taken on.
type walkFacts struct {
	// Pulls is the per-pull expansion provenance (see pullExpansion).
	Pulls []pullExpansion
	// AllUnknown: every commit the walk asked about was unknown to the queried
	// repository. One unknown SHA is API lag; ALL of them is what a wrong --repo
	// (or an inherited $GITHUB_REPOSITORY in a fork or a reusable-workflow
	// context) looks like from inside the walk.
	AllUnknown bool
	// LostPulls are merged pulls whose commits stood aside for a merge point
	// nothing resolved, in walk order — the fold is short by whatever they
	// changed. DESIGN §4 refuses to guess at their listings; this records that
	// the refusal happened.
	LostPulls []int
	// Dropped names the commits the walk could not read AT ALL because the API
	// had not indexed them: an unresolved merge commit, a message that does not
	// parse, an unknown gitmoji. Deliberately NOT every fallback — a commit the
	// fallback classified from its own message was READ, just from a weaker
	// source, and DESIGN §4 blesses that path. The line is "something is missing
	// from the fold", not "the fold used second-best evidence".
	Dropped []string
}

// complete reports that the walk read the range it was asked about. Everything
// glyph does that it cannot take back — deleting a draft, lowering the version a
// human is about to publish — is gated on it.
func (f walkFacts) complete() bool {
	return !f.AllUnknown && len(f.LostPulls) == 0 && len(f.Dropped) == 0
}

// shortfall says, in one clause, what the walk could not read — for the warning
// that explains why glyph declined to act and for the line the draft carries to
// the human who will press Publish.
func (f walkFacts) shortfall(owner, repo string) string {
	var parts []string
	if f.AllUnknown {
		parts = append(parts, fmt.Sprintf("every commit in it was unknown to %s/%s (check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to)", owner, repo))
	}
	for _, n := range f.LostPulls {
		parts = append(parts, fmt.Sprintf("merged pull request #%d has commits here that stood aside for a merge point nothing resolved", n))
	}
	if len(f.Dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) GitHub had not indexed could not be read (%s)", len(f.Dropped), strings.Join(f.Dropped, ", ")))
	}
	return strings.Join(parts, "; ")
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
func walkSince(ctx context.Context, c *github.Client, table *gitmoji.Table, owner, repo, revRange string) ([]parser.Commit, walkFacts, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, walkFacts{}, err
	}
	var commits []parser.Commit
	// Normalized to [] up front so the JSON surface never emits null — a
	// shadow script indexes .pulls unconditionally.
	facts := walkFacts{Pulls: []pullExpansion{}}
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
	//
	// What the listing DOES say is filtered twice before anything is parsed: by
	// where each commit landed on the released branch (mainFootprint — the range
	// governs anything git can place, which is what stops a pull from folding
	// back work an earlier tag shipped), and by seen (an already-represented
	// commit must never be able to fail the release — its message may be a squash
	// subject, which never parses).
	//
	// A shallow checkout cannot answer the footprint question: a commit git does
	// not HAVE is indistinguishable from one that never landed, so every listed
	// commit reads as footprint-less and the walk quietly returns to expanding
	// whole listings — the t-8xsb behaviour, wearing a clone option. Asked once,
	// and only if some pull actually expands, so the ordinary full-history
	// release pays one `git rev-parse` and a shallow one is told.
	shallowChecked := false
	foldPull := func(number int, canonical string) error {
		if !shallowChecked {
			shallowChecked = true
			shallow, serr := gitsource.IsShallow(ctx, ".")
			if serr != nil {
				return serr
			}
			if shallow {
				warnf("this is a SHALLOW checkout: the walk cannot tell a commit that never landed on the released branch from one git does not have, so a merged pull request's commits can be counted again even though an earlier tag shipped them. Check out with full history (actions/checkout with fetch-depth: 0) before releasing")
			}
		}
		listing, perr := pullRawCommits(ctx, c, owner, repo, number)
		if perr != nil {
			return wedgeHint(perr, owner, repo, number, canonical, "")
		}
		// Where did each listed commit LAND on the released branch? That is the
		// only question that lets the walk RANGE — a git fact — govern a listing
		// that knows nothing about it. A commit with no landing site is governed
		// by the pull alone and folds exactly as it always did; one that landed
		// is folded only if its landing site is in range.
		landed, ferr := mainFootprint(ctx, canonical, listing)
		if ferr != nil {
			return ferr
		}
		fresh := make([]gitsource.RawCommit, 0, len(listing))
		for i, r := range listing {
			if on := landed[i]; on != "" {
				if !inRange[on] {
					noticef("commit %.7s in pull request #%d landed on the released branch as %.7s, which is outside %s — it shipped under an earlier tag and is not counted again", r.SHA, number, on, revRange)
					continue
				}
				// The landing site is the identity that matters downstream: the
				// notes must cite a commit the repository actually has, and a
				// rebase-merge's listing reports pre-rebase shas that exist on
				// no branch. Substituting here also makes the walk-wide dedup
				// below compare like with like.
				r.SHA = on
			}
			if r.SHA != "" && seen[r.SHA] {
				noticef("commit %.7s in pull request #%d is already represented in this walk (a stacked branch carries its base PR's pre-squash commits; a merge-merged PR's commits sit on main under these same SHAs; a sub-PR's squash commit rides inside the PR that landed its branch) — counted once", r.SHA, number)
				continue
			}
			fresh = append(fresh, r)
		}
		// Parsed one at a time so a failure knows WHICH commit it was. The escape
		// the wedge hint offers is a tag cut at that commit, and only a commit
		// this walk can point at on the released branch can carry one — see
		// wedgeHint. participating holds no cross-commit state, so a run of
		// singletons folds to exactly the same list as one call.
		inner := make([]parser.Commit, 0, len(fresh))
		for _, r := range fresh {
			one, oerr := participating([]gitsource.RawCommit{r})
			if oerr != nil {
				return wedgeHint(oerr, owner, repo, number, canonical, onMain(r.SHA, inRange))
			}
			inner = append(inner, one...)
		}
		contributed := 0
		for _, ic := range inner {
			// Pre-flight the membership check that classifyVerdict would run
			// later anyway (same Classify, same table — it cannot disagree):
			// down there the PR association is gone, and a bare per-commit lint
			// line is uniquely unhelpful HERE — see wedgeHint.
			if _, cerr := bump.Classify(ic, table); cerr != nil {
				return wedgeHint(cerr, owner, repo, number, canonical, onMain(ic.SHA, inRange))
			}
			if ic.SHA != "" {
				seen[ic.SHA] = true
			}
			commits = append(commits, ic)
			contributed++
		}
		facts.Pulls = append(facts.Pulls, pullExpansion{Number: number, Commits: contributed})
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
			return nil, walkFacts{}, rerr
		case found:
			if ferr := foldPull(number, raw.SHA); ferr != nil {
				return nil, walkFacts{}, ferr
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
		if fc, ok := fallbackCommit(table, raw, reason, &facts); ok {
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
	// repository whose merge points the walk can RESOLVE: standing aside
	// requires the pull's canonical commit to be IN this range, and a canonical
	// commit in range that resolves is expanded right there. Note what that
	// does — and does not — promise. API lag clears itself, so the warning is a
	// one-run event. But a repository whose merge button is pressed by an
	// AUTOMATION is perfectly healthy and warns on every release: the author
	// gate skips a bot-authored merge commit before the API, so nothing is ever
	// left to resolve the pull. That is not a regression (before t-7zt7 the same
	// pull was lost in silence) and the loudness is the point, but "healthy
	// repositories stay quiet" would be a promise this mechanism does not keep.
	for _, number := range coveredOrder {
		if expanded[number] {
			continue
		}
		facts.LostPulls = append(facts.LostPulls, number)
		warnf("pull request #%d has commits in %s but nothing in the range resolved to it — GitHub does not associate its merge commit with the pull yet, or an automation authored that merge; those commits stood aside for that merge point and are NOT counted, so this release is short by whatever #%d changed. The pull's own commit listing is its whole history and cannot say which commits belong to %s, so the walk will not guess — re-run the release once GitHub has caught up with the merge point", number, revRange, number, revRange)
	}
	if walked > 0 && unknown == walked {
		facts.AllUnknown = true
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", walked, revRange, owner, repo)
	}
	return commits, facts, nil
}

// mainFootprint answers, for each commit in a merged pull request's API listing,
// which commit on the RELEASED branch it landed as — the empty string when it
// landed under no identity of its own.
//
// This is the missing half of the expansion. A pull's listing is the pull's
// ENTIRE history and carries nothing about the walk's range, which is a git
// fact; DESIGN §4 says so, and says it as the reason the walk refuses to expand
// an UNRESOLVED pull. The resolved arm expanded the listing whole anyway, and
// the gap between the two is a fabricated release: with a version tag cut inside
// a merge-merged pull's topic branch, commits that shipped under that tag were
// folded back into the next one (t-8xsb — exit 0, empty stderr, a minor invented
// out of released work). Once each listed commit is mapped to where it landed,
// the range can govern it, and both arms follow the same principle.
//
// Three shapes, decided from LOCAL GIT alone — GitHub reports no merge method,
// and asking for one would be a round-trip per pull to learn something git
// already knows:
//
//   - the merge button: the pull's commits sit on the released branch verbatim,
//     so a listed sha that this repository holds AND that is an ancestor of HEAD
//     landed as itself. This is asked of HEAD rather than of the merge commit on
//     purpose: a commit that reached the branch by some earlier route is just as
//     released, and the range is what decides whether it belongs here.
//   - rebase and merge: git rewrote every commit, so no listed sha is on the
//     branch — but it preserved their messages and their order, and the canonical
//     commit GitHub names is the LAST of the run. So the N first-parent commits
//     ending there align positionally. The alignment is VERIFIED message by
//     message and abandoned whole unless every one matches: a guess about which
//     commits belong to a release is precisely what this function exists to
//     remove, and half a mapping is worse than none.
//   - squash and merge: the listing collapsed into the one canonical commit and
//     has no footprint at all. Nothing changes for it — the pull alone governs,
//     as it always has.
//
// The unverifiable case degrades to the old behaviour rather than to an error:
// a squash-merged pull and a rebase whose alignment does not hold are not
// distinguishable without the merge method, and the squash shape is the fleet's
// norm, so a warning here would fire on nearly every release and teach people to
// ignore it. What that costs is bounded — it is exactly today's expansion.
func mainFootprint(ctx context.Context, canonical string, listing []gitsource.RawCommit) ([]string, error) {
	landed := make([]string, len(listing))
	if len(listing) == 0 {
		return landed, nil
	}
	shas := make([]string, 0, len(listing))
	for _, r := range listing {
		if r.SHA != "" {
			shas = append(shas, r.SHA)
		}
	}
	// One call to drop everything this repository does not hold — which is the
	// WHOLE listing for the squash and rebase shapes, so the per-sha ancestry
	// test below runs only where it can answer yes.
	have, err := gitsource.Have(ctx, ".", shas)
	if err != nil {
		return nil, err
	}
	anyLanded := false
	for i, r := range listing {
		if r.SHA == "" || !have[r.SHA] {
			continue
		}
		on, aerr := gitsource.IsAncestor(ctx, ".", r.SHA, "HEAD")
		if aerr != nil {
			return nil, aerr
		}
		if on {
			landed[i], anyLanded = r.SHA, true
		}
	}
	if anyLanded {
		return landed, nil
	}
	// Nothing landed under its own sha: either a rebase rewrote them all, or the
	// pull was squashed. Only the rebase shape leaves a run this listing can be
	// aligned against, and only an exact, complete match is accepted as one.
	mains, err := gitsource.FirstParentLog(ctx, ".", canonical, len(listing))
	if err != nil {
		return nil, err
	}
	if len(mains) != len(listing) {
		return landed, nil
	}
	for i := range listing {
		if strings.TrimSpace(mains[i].Message) != strings.TrimSpace(listing[i].Message) {
			return landed, nil
		}
	}
	for i := range listing {
		landed[i] = mains[i].SHA
	}
	return landed, nil
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
func fallbackCommit(table *gitmoji.Table, raw gitsource.RawCommit, reason fallbackReason, facts *walkFacts) (parser.Commit, bool) {
	// A commit the walk could not READ is recorded, not only warned about: the
	// warning reaches a log, and what glyph must not do on an incomplete fold —
	// delete the rolling draft, lower the version a human is about to publish —
	// is a decision, which needs the fact itself. Only the lag arms count: a
	// direct-push commit that does not parse is a message glyph read and judged,
	// not evidence it could not obtain.
	dropped := func() {
		if reason.lag {
			facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
		}
	}
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
			dropped()
		}
		return parser.Commit{}, false // skipped, never a violation
	}
	c, perr := parseRaw(raw)
	if perr != nil {
		warnf("commit %.7s %s and its own message does not parse (%v) — counted as none", raw.SHA, reason.why, perr)
		dropped()
		return parser.Commit{}, false
	}
	if _, cerr := bump.Classify(c, table); cerr != nil {
		if c.Breaking {
			warnf("commit %.7s %s and its gitmoji %s is not in the rules table, but it carries a breaking marker — counted as a breaking change (%s, major)", raw.SHA, reason.why, c.Gitmoji, gitmojiBoom)
			c.Gitmoji = gitmojiBoom
			return c, true
		}
		warnf("commit %.7s %s and its gitmoji %s is not in the rules table — counted as none", raw.SHA, reason.why, c.Gitmoji)
		dropped()
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
// WHERE THE ESCAPE POINTS depends on whether the offending commit is somewhere a
// tag can be cut, and onMain is what answers that. A merge- or rebase-merged
// pull put its commits on the released branch, so the walk can now drop the ones
// an earlier tag already shipped (mainFootprint) — which means a base past the
// offending commit clears the wedge, the intuitive escape, restored. A
// SQUASH-merged pull has no such commit: its listing exists only over the API,
// its one commit on main IS its merge point, and nothing short of a base at or
// past that merge point stops the walk from re-fetching the whole listing.
//
// That distinction used to be invisible, and the cost was a hint that lied by
// omission. Before mainFootprint, expanding a pull re-read its entire listing
// whenever its merge point was in range, so for a merge-merged pull a base past
// the offending commit wedged again and the hint had to send everyone to the
// merge point (verified then: tag AT the offending commit and tag strictly past
// it both still exited 3). Now both subtests exit 0, so pointing a merge-merged
// pull at its merge point would throw away every commit between — a short
// release recommended by glyph itself.
func wedgeHint(err error, owner, repo string, number int, mergePoint, offending string) error {
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeLint {
		return err
	}
	base, why := mergePoint, "the pull request was squash-merged, so its commits exist only over the API and re-reading the listing is the only way the walk sees them at all; its ONE commit on the released branch is that merge point"
	if offending != "" {
		base, why = offending, "the offending commit is itself on the released branch (the pull was merge- or rebase-merged), and the walk no longer folds back a listed commit that landed outside the range"
	}
	return &core.Error{Code: core.CodeLint, Details: ce.Details, Msg: fmt.Sprintf(
		"%s — inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s; the commit is already on a published branch and cannot be rewritten, so every release wedges here until the walk starts past it. The walk must start AT OR PAST %.7s — %s. Cut a release tag there by hand, or name such a tag with an explicit --since-tag=TAG (DESIGN §4)",
		ce.Msg, owner, repo, number, mergePoint, base, why)}
}

// onMain reports the sha a wedge escape can be cut at: the commit itself when
// the walk can see it on the released branch, else "" for a commit that lives
// only inside a pull request's API listing. By the time foldPull asks, a listed
// commit that landed outside the range has already been dropped, so being in
// range is exactly the same question as having a landing site at all.
func onMain(sha string, inRange map[string]bool) string {
	if sha != "" && inRange[sha] {
		return sha
	}
	return ""
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
