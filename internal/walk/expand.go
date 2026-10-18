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
	// Asked once per WALK, before any expansion, never lazily on the first
	// pull that expands: a walk where no pull resolves is still a walk over a
	// truncated history, and it was the one that never asked (DESIGN §4).
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
	// seen holds every SHA already REPRESENTED in the fold — a commit two
	// merged pulls list (a stacked branch carries its base pull's), a
	// merge-merged pull's commits met on main and again inside its listing —
	// and the CANONICAL commit of every resolved pull, which comes back around
	// too (a pull squash-merged into a topic branch leaves its squash commit
	// inside the listing of the pull that landed the branch). The walk runs
	// oldest first, so a shared commit is attributed to whatever first landed
	// it, and a merge point is always walked AFTER the commits it merges
	// (DESIGN §4).
	seen := map[string]bool{}
	// wedges is every commit the walk refused, from either arm. The refusal
	// goes out after the WHOLE range is walked, as the fold's does
	// (bump.Refusal): stopping at the first answered a cleanup of several
	// commits with one red run per commit.
	var wedges []wedge
	// wedged is each refused sha's index in wedges, for a pull that lists a
	// commit another pull already had refused.
	wedged := map[string]int{}
	// inRange is every SHA this walk will visit — what makes "covered"
	// checkable: a commit stands aside only for a pull whose canonical commit
	// is IN HERE, never for one merged into another base branch (DESIGN §4,
	// "Covered is deliberately gated").
	inRange := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if raw.SHA != "" {
			inRange[raw.SHA] = true
		}
	}
	// The covered ledger, in walk order, and the pulls actually expanded; the
	// end of the walk reconciles the two, because a covered pull nothing
	// expanded is a merged pull every commit of which stood aside (t-7zt7).
	coveredPulls, coveredOrder, expanded := map[int]bool{}, []int{}, map[int]bool{}
	// foldPull expands one RESOLVED merged pull into the walk, canonical being
	// the merge point that resolved to it; the reconciliation at the end never
	// calls it, because a listing fetched without a canonical commit in range
	// says nothing about which of its commits belong to this walk (DESIGN §4,
	// "warns and does not expand"). The listing is filtered BEFORE anything
	// judges it — by where each commit landed (mainFootprint: the range governs
	// whatever git can place) and by seen (an already-represented commit must
	// never fail the release; its message may be a squash subject) — and what
	// remains is pre-flighted against the pattern file HERE, where the pull
	// association still exists to word the refusal (wedge).
	foldPull := func(number int, canonical string) error {
		listing, perr := w.PullRawCommits(ctx, owner, repo, number)
		if perr != nil {
			return perr
		}
		// Recorded HERE, not in PullRawCommits, which --pr shares and which
		// carries no facts: only a walk acts on its verdict irreversibly.
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
		// A merge point is a POINTER to a pull request, not a message being
		// judged, so only its AUTHOR excludes it before resolution: an
		// exclude_authors commit can never move the version, and the fleet's
		// daily bot push must not cost a round-trip. Neither its subject nor
		// its parent count may — judging the shape dropped a whole pull out of
		// a release in silence (t-7zt7; DESIGN §4). The commit still joins the
		// walk; whether the notes show it is their decision.
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
			// Covered: part of a merged pull whose canonical commit is in this
			// range and expands it — a rebase-merge's earlier commits, a
			// merge-merged pull's branch commits (the log runs without
			// --first-parent). Counting this one too would double it; if the
			// canonical commit never resolves, the ledger names the pull at the
			// end.
			continue
		}
		// The fallback: no merged pull explains this commit (a direct push, or
		// the API lagging), so its own message is the evidence. A merge commit
		// GitHub has not indexed yet is a merge POINT the walk could not
		// resolve, and its message never says what it merged — a fact a
		// release decision needs (Facts.Dropped), not only a warning.
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
			// The lag arm stays SOFT on an unmatched message, unlike the fold:
			// this commit's true content is a pull the API has not indexed, so
			// it is dropped from the fold, warned about and carried in
			// Facts.Dropped — release refuses to ACT on it, bump and notes
			// report it (DESIGN §4).
			if row, ok := unclaimed(cfg, raw); ok {
				w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) and its own message cannot be folded (%s) — nothing was counted from it; re-run once GitHub has indexed the commit", raw.SHA, row.Detail)
				facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
				continue
			}
			w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) — reading its own message", raw.SHA)
		} else {
			// A direct push GitHub knows is judged HERE, like a pull's inner
			// commit: the fold's remedy (fix the message, add a pattern) is
			// authoring's, and this commit is on the released branch, where
			// the only way out is a base at or past it — the wedge's refusal,
			// with the escape at the commit itself (DESIGN §4).
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
	// Reconcile the ledger: a pull the walk saw only from the INSIDE, its
	// commits standing aside for a canonical commit that never resolved (API
	// lag, or an exclude_authors author merged it with a real merge commit).
	// WARN, and deliberately do NOT expand: a listing carries nothing about
	// this range, and expanding it was unsound both ways — a rebase-merge's
	// pre-rebase SHAs double-count, released work comes back (DESIGN §4, "The
	// walk warns and does not expand"). Naming the loss is what turns
	// t-7zt7's silence into a visible one.
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

// mainFootprint answers, for each commit in a merged pull's API listing,
// which commit on the RELEASED branch it landed as — "" when it landed under
// no identity of its own. It is what lets the walk's range govern a listing
// that knows nothing about it (DESIGN §4, "A pull's listing is governed by the
// walk's range, not by the pull"; t-8xsb). Two questions, of local git alone,
// in this order because the first is a fact and the second an inference the
// first narrows:
//
//  1. Did the listed commit land under its OWN sha — held by this repository
//     and an ancestor of HEAD? Asked of HEAD, not of the merge commit: a
//     commit that reached the branch by another route is just as released,
//     and a change that landed twice keeps the landing git states (t-nsww).
//  2. Does whatever is LEFT align against the first-parent run ending at the
//     merge point, the run a rebase would have written? Verified message by
//     message and abandoned WHOLE unless every entry matches — half a mapping
//     is worse than none — and reaching past the run to the commit a dropped
//     replay was redundant with.
//
// Step 2 runs over what step 1 did not place, not only when it placed
// nothing (a listing can be MIXED), and a merge commit leaves the window on
// its parent count (a rebase never replays it) — both measured against the
// real API (DESIGN §4, "The order of those two questions matters"; t-7h15). A
// squash fails to align and the pull alone governs it, quietly: without the
// merge method a squash and an unaligned rebase are one shape, and squash is
// the fleet's norm.
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
	// Nothing left to place — the whole merge-button shape, or an empty listing
	// — is one question, so one guard. The walk's canonical comes from its own
	// log, where falling through would only spend a `git log -n 0`;
	// TestMainFootprintEmptyListingAsksGitNothing hands in a canonical the
	// checkout lacks, where it is an exit-4 git error.
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

// mergedPullFor resolves a merge point to the pull that merged it: a commit
// can be ASSOCIATED with many pulls (a revert, a mention), so the match is
// MergeCommitSHA == sha and actually merged — never list order — and one
// equality serves every merge style (DESIGN §4). covering is the other side
// of that equality, by NUMBER: every merged pull that carries this commit
// while some OTHER sha in range represents it, so the walk can tell at the
// end whether that sha ever resolved (a bare bool lost whole pulls, t-7zt7).
// Both can be non-empty at once. inRange gates covering: a pull merged into
// another base branch is no claim about this release (DESIGN §4, "Covered is
// deliberately gated").
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

// PullRawCommits is a pull's listing as GitHub returns it, converted to the
// raw shape and with the truncation warning said, NOT yet parsed: the walk
// filters it against its walk-wide SHA set before anything parses it (a pull
// squash-merged into a topic branch leaves its own squash subject inside the
// listing of the pull that landed the branch, and parsing it there wedged
// the release — t-7zt7). bump, notes and preview read a pull through it too
// (cli's pullInput).
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
