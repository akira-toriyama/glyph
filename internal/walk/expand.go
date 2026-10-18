package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
)

// walkSince walks the range's commits oldest first and folds every merged PR's
// individual commits into one walked list, recording per-pull expansion
// provenance alongside. Author-excluded commits are skipped before any API call
// — the routine fleet-sync direct push never costs a request. Everything else
// is asked about: a commit's own shape cannot tell a pull request's merge point
// from a local merge, so resolution answers that, and the unresolved ones fall
// through to the inline fallback, which applies the message rules. A last pass
// reconciles the pulls commits stood aside for against the pulls actually
// expanded, so a merged pull request whose canonical commit never resolved is
// named in a warning instead of silently lost.
func (w Walker) walkSince(ctx context.Context, cfg *config.Config, owner, repo, revRange string) ([]Commit, Facts, error) {
	raws, err := w.Git.Log(ctx, revRange)
	if err != nil {
		return nil, Facts{}, err
	}
	// Asked once per WALK, before anything else, and not inside the expansion.
	//
	// It used to be asked lazily on the first pull that actually expanded, to
	// save one `git rev-parse` on a release that folds nothing. That saving cost
	// the fact: a walk where no pull resolves — the API lagging, an automation
	// authoring the merge commits, a range of direct pushes — never asked at all,
	// so the shallowest reading of the shallowest checkout was the one that went
	// unreported. The question is about the CHECKOUT, not about any pull, so it
	// belongs where the checkout is first read.
	shallow, serr := w.Git.IsShallow(ctx)
	if serr != nil {
		return nil, Facts{}, serr
	}
	if shallow {
		w.Report.Warnf("this is a SHALLOW checkout: the walk cannot tell a commit that never landed on the released branch from one git does not have, so a merged pull request's commits can be counted again even though an earlier tag shipped them. Check out with full history (actions/checkout with fetch-depth: 0) before releasing")
	}
	var commits []Commit
	// Normalized to [] up front so the JSON surface never emits null: .pulls is
	// indexable on every verdict, the none verdict included, with no null-check
	// at each consumer.
	facts := Facts{Pulls: []PullExpansion{}, Shallow: shallow}
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
	// wedges is every commit the walk refused, from either arm. The refusal
	// goes out after the WHOLE range is walked, as the fold's does
	// (bump.Refusal): stopping at the first answered a cleanup of several
	// commits with one red run per commit.
	var wedges []wedge
	// wedged is each refused sha's index in wedges, for a pull that lists a
	// commit another pull already had refused.
	wedged := map[string]int{}
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
	// filtered BEFORE anything judges it (see the two filters below), and the
	// pull's contribution is recorded. Only a RESOLVED canonical commit may
	// expand a pull — the reconciliation at the end of the walk deliberately
	// does not call this (see there), because a listing fetched without a
	// canonical commit in range says nothing about which of its commits belong
	// to this walk.
	//
	// What the listing DOES say is filtered twice: by where each commit landed
	// on the released branch (mainFootprint — the range governs anything git
	// can place, which is what stops a pull from folding back work an earlier
	// tag shipped), and by seen (an already-represented commit must never be
	// able to fail the release — its message may be a squash subject, which
	// need not match any pattern).
	//
	// What remains is PRE-FLIGHTED against the pattern file: a non-excluded
	// inner commit no pattern claims would refuse the fold downstream anyway
	// (same Match, same config — it cannot disagree), but down there the PR
	// association is gone, and a bare per-commit refusal is uniquely unhelpful
	// HERE — see wedge.
	foldPull := func(number int, canonical string) error {
		listing, perr := w.PullRawCommits(ctx, owner, repo, number)
		if perr != nil {
			return perr
		}
		// Asked HERE and not inside pullRawCommits, which the --pr path shares and
		// which carries no facts: only a walk has a verdict that can be acted on
		// irreversibly, so only a walk needs the fact rather than the warning.
		// pullRawCommits still emits that warning for both callers.
		if len(listing) >= github.PullCommitsCap {
			facts.Truncated = append(facts.Truncated, number)
		}
		// Where did each listed commit LAND on the released branch? That is the
		// only question that lets the walk RANGE — a git fact — govern a listing
		// that knows nothing about it. A commit with no landing site is governed
		// by the pull alone and folds exactly as it always did; one that landed
		// is folded only if its landing site is in range.
		landed, ferr := w.mainFootprint(ctx, canonical, listing)
		if ferr != nil {
			return ferr
		}
		contributed := 0
		for i, r := range listing {
			if on := landed[i]; on != "" {
				if !inRange[on] {
					w.Report.Noticef("commit %.7s in pull request #%d landed on the released branch as %.7s, which is outside %s — it shipped under an earlier tag and is not counted again", r.SHA, number, on, revRange)
					continue
				}
				// The landing site is the identity that matters downstream: the
				// notes must cite a commit the repository actually has, and a
				// rebase-merge's listing reports pre-rebase shas that exist on
				// no branch. Substituting here also makes the walk-wide dedup
				// below compare like with like.
				r.SHA = on
			}
			if i, refused := wedged[r.SHA]; refused {
				// A refused commit a second pull lists (a stacked pull carries
				// its base pull's commits) is one row with one more escape:
				// this pull's merge point wedges too, and a base that clears
				// the first pull's alone leaves the next run red on this one.
				wedges[i].escapes = append(wedges[i].escapes, placeEscape(owner, repo, number, canonical, onMain(r.SHA, inRange)))
				continue
			}
			if r.SHA != "" && seen[r.SHA] {
				w.Report.Noticef("commit %.7s in pull request #%d is already represented in this walk (a stacked branch carries its base PR's pre-squash commits; a merge-merged PR's commits sit on main under these same SHAs; a sub-PR's squash commit rides inside the PR that landed its branch) — counted once", r.SHA, number)
				continue
			}
			if !slices.Contains(cfg.ExcludeAuthors, r.Author) {
				if row, ok := unclaimed(cfg, r); ok {
					// Represented like any other listed commit, and remembered
					// as refused: one refused commit is one row however many
					// pulls list it.
					if r.SHA != "" {
						seen[r.SHA] = true
						wedged[r.SHA] = len(wedges)
					}
					wedges = append(wedges, newWedge(row, owner, repo, number, canonical, onMain(r.SHA, inRange)))
					continue
				}
			}
			if r.SHA != "" {
				seen[r.SHA] = true
			}
			commits = append(commits, Commit{Raw: r, Pull: number, Landed: landed[i] != "", MergePoint: canonical})
			contributed++
		}
		facts.Pulls = append(facts.Pulls, PullExpansion{Number: number, Commits: contributed})
		expanded[number] = true
		return nil
	}
	visited, unknown := 0, 0
	for _, raw := range raws {
		// A merge point here is a POINTER to a pull request, not a message
		// being judged, so only its AUTHOR excludes it before resolution — and
		// the authors are the config's exclude_authors, whose commits can never
		// move the version, so resolving one would buy nothing and must not
		// cost an API round-trip (the routine bot push runs on every
		// repository, every day). The commit itself still joins the walk:
		// whether it appears in the notes is note.sections' decision, not the
		// resolution gate's. Neither its subject nor its shape may exclude it:
		// a two-parent commit is what the "Create a merge commit" button
		// writes — skipping it for that dropped the entire pull request out of
		// the release, silently (t-7zt7). Message rules apply where a message
		// IS judged: in the fold, the lint and the notes downstream.
		if slices.Contains(cfg.ExcludeAuthors, raw.Author) {
			if raw.SHA != "" && seen[raw.SHA] {
				continue
			}
			if raw.SHA != "" {
				seen[raw.SHA] = true
			}
			commits = append(commits, Commit{Raw: raw, Landed: true})
			continue
		}
		visited++
		number, found, covering, rerr := w.mergedPullFor(ctx, owner, repo, raw.SHA, inRange)
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
		lag := false
		switch {
		case github.IsCommitUnknown(rerr):
			// GitHub does not know the SHA yet (422) — the walk outran the API
			// right after a push. DESIGN §4's API lag: fall back, never fail.
			unknown++
			lag = true
		case rerr != nil:
			return nil, Facts{}, w.refusedFirst(wedges, rerr)
		case found:
			if ferr := foldPull(number, raw.SHA); ferr != nil {
				return nil, Facts{}, w.refusedFirst(wedges, ferr)
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
		}
		// The fallback: no merged pull request explains this commit (a direct
		// push, or the API lagging), so its own message is the evidence — the
		// same evidence the fold and the notes read for every commit. One case
		// deserves more than the default: a merge commit GitHub has not
		// indexed yet is a merge POINT the walk could not resolve, and its own
		// message never says what it merged (the pattern file will skip it as
		// a merge), so the pull behind it is lost to this run. That is a fact
		// a release decision needs, not only a warning (t-7zt7's silence, in
		// miniature).
		if lag && raw.Parents >= 2 {
			w.Report.Warnf("merge commit %.7s is not known to GitHub yet (API lag), so the pull request it may have merged could not be resolved — nothing was counted from it; if the release looks short, re-run it once GitHub has indexed the commit", raw.SHA)
			facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
			continue
		}
		if i, refused := wedged[raw.SHA]; refused {
			// Refused inside a pull the walk expanded earlier, and now met on
			// the released branch itself: one row, one more escape — the
			// commit is its own base here — and no "counted once" for a
			// commit that was not.
			wedges[i].escapes = append(wedges[i].escapes, placeEscape(owner, repo, 0, "", raw.SHA))
			continue
		}
		if raw.SHA != "" && seen[raw.SHA] {
			w.Report.Noticef("commit %.7s was already folded in by a merged pull request — counted once", raw.SHA)
			continue
		}
		if lag {
			// The lag fallback stays SOFT on an unmatched message, unlike the
			// fold (Q2): this commit's true content is the pull request the
			// API has not indexed yet — a squash subject is not a commit
			// message anyone wrote — so refusing the range here would red
			// every release for the minutes GitHub lags behind a merge.
			// DESIGN §4 blesses the weaker source and records the shortfall:
			// the commit is dropped from the fold, warned about, and carried
			// in facts.Dropped so release refuses to ACT on the incomplete
			// walk while bump and notes merely report it.
			if row, ok := unclaimed(cfg, raw); ok {
				w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) and its own message cannot be folded (%s) — nothing was counted from it; re-run once GitHub has indexed the commit", raw.SHA, row.Detail)
				facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
				continue
			}
			w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) — reading its own message", raw.SHA)
		} else {
			// Judged HERE, like a pull's inner commit, and for the same reason:
			// bump's fold downstream would refuse the same message with the
			// same Match (notes has no fold: it left the commit out of every
			// section, or rendered its raw subject under a section that
			// claimed its author), but the fold's remedy is authoring's (fix
			// the message, add a pattern — right for --range and --pr), and
			// this commit is on the released branch, where the only way out
			// is a base at or past it. The refusal is the wedge's, with the
			// escape at the commit itself.
			if row, ok := unclaimed(cfg, raw); ok {
				if raw.SHA != "" {
					seen[raw.SHA] = true
					wedged[raw.SHA] = len(wedges)
				}
				wedges = append(wedges, newWedge(row, owner, repo, 0, "", raw.SHA))
				continue
			}
			if m, merr := cfg.Match(raw.Message); merr != nil || !m.Matched || !m.Skip {
				// A skip-pattern commit (a local git merge under the presets)
				// is passed over in silence; every other fallback announces
				// that a message, not a pull request, is the evidence.
				w.Report.Warnf("commit %.7s has no merged pull request (a direct push, or the API lagging) — reading its own message", raw.SHA)
			}
		}
		if raw.SHA != "" {
			seen[raw.SHA] = true
		}
		// Landed by construction: the fallback reads the walked commit
		// itself, an identity the released branch holds.
		commits = append(commits, Commit{Raw: raw, Landed: true})
	}
	// Reconcile the ledger: a pull the walk only ever saw from the INSIDE. Its
	// commits stood aside for a canonical commit that never resolved — GitHub
	// not knowing the merge commit yet (a release job runs seconds after the
	// merge, and the merge commit is indexed after the commits it merges), or
	// an exclude_authors author having authored it (the author gate skips it
	// before the API).
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
	// silence surviving on the new path) into a visible one.
	for _, number := range coveredOrder {
		if expanded[number] {
			continue
		}
		facts.LostPulls = append(facts.LostPulls, number)
		w.Report.Warnf("pull request #%d has commits in %s but nothing in the range resolved to it — GitHub does not associate its merge commit with the pull yet, or an exclude_authors author merged it with a real merge commit (which the walk skips before resolution); those commits stood aside for that merge point and are NOT counted, so this release is short by whatever #%d changed. The pull's own commit listing is its whole history and cannot say which commits belong to %s, so the walk will not guess — re-run the release once GitHub has caught up with the merge point", number, revRange, number, revRange)
	}
	if visited > 0 && unknown == visited {
		facts.AllUnknown = true
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		w.Report.Warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", visited, revRange, owner, repo)
	}
	if len(wedges) > 0 {
		return nil, Facts{}, walkRefusal(wedges)
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
// Two questions, asked of LOCAL GIT alone — GitHub reports no merge method, and
// asking for one would be a round-trip per pull to learn something git already
// knows. They are asked in this order because the first is a git FACT and the
// second is an inference the first narrows:
//
//  1. Did this listed commit land under its OWN sha? The merge button leaves the
//     pull's commits on the released branch verbatim, so a listed sha that this
//     repository holds AND that is an ancestor of HEAD landed as itself. Asked of
//     HEAD rather than of the merge commit on purpose: a commit that reached the
//     branch by another route is just as released, and the range is what decides
//     whether it belongs here. That holds even when this pull's rebase ALSO wrote
//     a copy of the change — the double landing keeps git's answer, ratified
//     (t-nsww, DESIGN §4).
//  2. Whatever is LEFT, does it align against the run a rebase would have
//     written? A rebase rewrites the commits it replays but preserves their
//     messages and their order, and GitHub names the LAST of the run as
//     merge_commit_sha — so the N first-parent commits ending there align
//     positionally with the N unplaced entries, in listing order. The alignment
//     is VERIFIED message by message and abandoned whole unless every one
//     matches: a guess about which commits belong to a release is precisely what
//     this function exists to remove, and half a mapping is worse than none.
//     The window can also reach PAST the run: a replay the rebase dropped as
//     already upstream aligns, message-verified, to the older commit that made
//     it redundant — the other half of the ratified double-landing answer
//     (t-nsww, DESIGN §4).
//
// Step 2 used to run only when step 1 placed NOTHING, on the reading that a
// listing is either all-verbatim or all-rewritten. It is neither, and both ways
// out of that assumption were measured against the real API on
// akira-toriyama/glyph-test (drill for t-7h15):
//
//   - A listing can be MIXED. A stacked pull request carries its base pull's
//     commits, and GitHub computes the listing against a STORED base sha rather
//     than re-deriving it, so those commits stay listed after the base pull lands
//     them on main under their own shas (measured: pull #28 still listed the
//     merge-button-landed ed1bd9e after #27 merged). Rebase-merge the stacked
//     pull and step 1 places that one entry, step 2 never ran, and every REWRITTEN
//     entry kept an empty landing site — which foldPull reads as "the pull alone
//     governs" and folds regardless of the range. That is t-8xsb resurrected
//     through the front door: measured minor where patch was correct, exit 0, no
//     warning, and the notes citing pre-rebase shas main does not contain.
//   - A rebase does NOT replay everything it is given. It drops the base-merge
//     commit that "merge main into the branch" leaves — an entirely ordinary
//     branch, and GitHub permits rebase-merge over it (measured: pull #26 listed
//     three commits, two landed). That entry has no landing site under any shape,
//     so leaving it in the window made the count wrong and the alignment was
//     abandoned WHOLE, taking the two commits it could have placed with it. It is
//     excluded on its parent count — the same RawCommit.Parents fact the walk
//     already reads everywhere it handles a merge commit, so nothing
//     downstream needs it placed.
//
// A squash leaves no footprint at all and reaches step 2 with the whole listing,
// where it fails to align: the pull alone governs it, exactly as before.
//
// The unalignable case therefore still degrades to the old behaviour rather than
// to an error or a warning. A squash and a rebase whose window does not align are
// not distinguishable without the merge method — including when step 1 placed
// some of the listing, since a squash-merged STACKED pull is that shape too — and
// squash is the fleet's norm, so a diagnostic here would fire on nearly every
// release and teach people to ignore it. What it costs is bounded: exactly the
// expansion that ran before any of this existed.
func (w Walker) mainFootprint(ctx context.Context, canonical string, listing []gitsource.RawCommit) ([]string, error) {
	landed := make([]string, len(listing))
	shas := make([]string, 0, len(listing))
	for _, r := range listing {
		if r.SHA != "" {
			shas = append(shas, r.SHA)
		}
	}
	// One call to drop everything this repository does not hold — which is most
	// of the listing for the squash and rebase shapes, so the per-sha ancestry
	// test below runs only where it can answer yes.
	have, err := w.Git.Have(ctx, shas)
	if err != nil {
		return nil, err
	}
	// window is the listing positions a rebase could still account for, in listing
	// order: everything step 1 did not place, minus the entries a rebase never
	// replays. Its LENGTH is what makes the alignment checkable, so an entry that
	// cannot have a landing site has to leave — one that stays shifts every
	// position after it and the whole mapping is thrown away.
	window := make([]int, 0, len(listing))
	for i, r := range listing {
		if r.SHA != "" && have[r.SHA] {
			on, aerr := w.Git.IsAncestor(ctx, r.SHA, "HEAD")
			if aerr != nil {
				return nil, aerr
			}
			if on {
				landed[i] = r.SHA
				continue
			}
		}
		// A merge commit is never replayed by a rebase (measured — see above), so
		// it has no landing site to find and no position to hold.
		if r.Parents >= 2 {
			continue
		}
		window = append(window, i)
	}
	// Nothing left to place: the whole merge-button shape, and also the pull that
	// listed no commits at all. One guard for both because it is one question: is
	// there anything an alignment could answer about? The walk hands in a
	// `canonical` from its own local log, so for the walk falling through only
	// spends a `git log -n 0` that answers nothing and lands on the same return;
	// TestMainFootprintEmptyListingAsksGitNothing holds the guard with a canonical
	// this checkout lacks, where falling through is an exit-4 git error.
	if len(window) == 0 {
		return landed, nil
	}
	mains, err := w.Git.FirstParentLog(ctx, canonical, len(window))
	if err != nil {
		return nil, err
	}
	if len(mains) != len(window) {
		return landed, nil
	}
	for k, i := range window {
		if strings.TrimSpace(mains[k].Message) != strings.TrimSpace(listing[i].Message) {
			return landed, nil
		}
	}
	for k, i := range window {
		landed[i] = mains[k].SHA
	}
	return landed, nil
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
func (w Walker) mergedPullFor(ctx context.Context, owner, repo, sha string, inRange map[string]bool) (number int, found bool, covering []int, err error) {
	pulls, err := w.Pulls.CommitPulls(ctx, owner, repo, sha)
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

// pullRawCommits is the read half of pullInput: the listing as GitHub
// returns it, converted to the local raw shape and with the truncation warning
// already emitted, but NOT yet parsed.
//
// The split exists for the release walk, which must filter this listing against
// its walk-wide SHA set BEFORE anything parses it. A commit the walk already
// folded in is already represented in the verdict, so re-reading its message
// can only do harm: a pull request squash-merged into a topic branch leaves its
// own squash subject (`Add a menu (#6)`, which no pattern claims) inside the
// listing of the pull that later landed that branch, and parsing it there
// wedged the release permanently (t-7zt7). Parse only what the walk has not
// already accounted for.
func (w Walker) PullRawCommits(ctx context.Context, owner, repo string, number int) ([]gitsource.RawCommit, error) {
	raws, err := w.Pulls.PullCommits(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	if len(raws) >= github.PullCommitsCap {
		// No silent caps: the verdict may be computed from a truncated PR, and
		// a missing commit could carry the deciding sigil.
		w.Report.Warnf("pull request #%d returned %d commits — GitHub truncates this listing at %d, so some commits (and their sigils) may be missing from the verdict", number, len(raws), github.PullCommitsCap)
	}
	local := make([]gitsource.RawCommit, len(raws))
	for i, r := range raws {
		local[i] = gitsource.RawCommit(r)
	}
	return local, nil
}
