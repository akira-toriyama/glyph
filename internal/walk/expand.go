package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/github"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// walk is one walk over a range in progress. Its contract is an order: newWalk
// opens it over the range's log, visit is called once per commit of that log,
// oldest first, and reconcile closes it once after the last visit; the verdict
// — the walked list with its facts, or the refusal of every wedge — is read
// only then. The field docs say what each ledger keeps and why the order
// matters.
type walk struct {
	Walker
	cfg                   *config.Config
	owner, repo, revRange string
	// ranges is the lines the walk answers for, with each line's own range
	// under [[packages]]: what a refused commit's escape is named per line
	// from, and what tells a commit released on every line, which the walk
	// does not judge (judges).
	ranges lineRanges

	// commits is the walked list, oldest first, every commit represented once.
	commits []Commit
	// facts is normalized up front so the JSON surface never emits null:
	// .pulls is indexable on every verdict, the none verdict included, with
	// no null-check at each consumer.
	facts Facts
	// seen holds every SHA already REPRESENTED in the fold — a commit two
	// merged pulls list (a stacked branch carries its base pull's), a
	// merge-merged pull's commits met on main and again inside its listing —
	// and the CANONICAL commit of every resolved pull, which comes back around
	// too (a pull squash-merged into a topic branch leaves its squash commit
	// inside the listing of the pull that landed the branch). The walk runs
	// oldest first, so a shared commit is attributed to whatever first landed
	// it, and a merge point is always walked AFTER the commits it merges
	// (DESIGN §4).
	seen map[string]bool
	// wedges is every commit the walk refused, from either arm. The refusal
	// goes out after the WHOLE range is walked, as the fold's does
	// (bump.Refusal): stopping at the first answered a cleanup of several
	// commits with one red run per commit.
	wedges []wedge
	// wedged is each refused sha's index in wedges, for a pull that lists a
	// commit another pull already had refused.
	wedged map[string]int
	// unjudged is every listed commit represented without a judgment of its
	// message: its first sighting sat at an identity every line had released
	// (judges). seen alone cannot tell it from a commit judged and accepted,
	// or from a resolved pull's canonical commit, whose squash subject must
	// never be judged (t-7zt7) — and a later pull pending on some line judges
	// exactly these (t-wjbs).
	unjudged map[string]bool
	// landings is where each listed sha landed, as far as this walk has read:
	// the commit it landed as, under the first pull that landed it (land) — a
	// footprint's answer for a pull this walk expanded, and for a pull behind
	// the base the footprint's answer against that pull's merge point, else the
	// merge point itself (readBackPull). seen cannot answer for these: a
	// pre-rebase sha is folded under its landing site, and a pull behind the
	// base is never folded at all (placeStacked).
	landings map[string]string
	// readBackDone is every commit readBack has asked GitHub about: pulls that
	// forked behind the base read overlapping windows, and each costs a request.
	readBackDone map[string]bool
	// readBackTop is where the read-back's window ends: the base of a tagged
	// range (what the range holds, the walk expands itself), HEAD for an open
	// pull's listing (OpenPull), "" for the whole-history arm, which has no
	// released branch to read behind.
	readBackTop string
	// inRange is every SHA this walk will visit — what makes "covered"
	// checkable: a commit stands aside only for a pull whose canonical commit
	// is IN HERE, never for one merged into another base branch (DESIGN §4,
	// "Covered is deliberately gated").
	inRange map[string]bool
	// The covered ledger, in walk order, and the pulls actually expanded;
	// reconcile compares the two, because a covered pull nothing expanded is
	// a merged pull every commit of which stood aside (t-7zt7).
	coveredPulls map[int]bool
	coveredOrder []int
	expanded     map[int]bool
	// visited counts the commits the walk asked GitHub about, unknown the ones
	// GitHub did not know; reconcile reads the all-unknown shape off them.
	visited, unknown int
}

// walkSince is the walk over revRange: the log, the shallow question asked
// once, one visit per commit, reconcile, and the verdict — the walked list
// with its facts, or the refusal naming every wedge. An error out of a visit is
// the API's or git's as received; Outranks decides whether the wedges
// already found are the verdict over it.
func (w Walker) walkSince(ctx context.Context, cfg *config.Config, owner, repo, revRange string, ranges lineRanges) ([]Commit, Facts, error) {
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
	wk := newWalk(w, cfg, owner, repo, revRange, raws, shallow, ranges)
	for _, raw := range raws {
		if err := wk.visit(ctx, raw); err != nil {
			if w.Outranks("the rest of the range", len(wk.wedges) > 0, err) {
				return nil, Facts{}, wk.walkRefusal()
			}
			return nil, Facts{}, err
		}
	}
	wk.reconcile()
	if len(wk.wedges) > 0 {
		return nil, Facts{}, wk.walkRefusal()
	}
	return wk.commits, wk.facts, nil
}

// newWalk opens a walk over raws, the range's log oldest first: every ledger
// empty, every sha of the range indexed, and the checkout's shallowness
// recorded as a fact.
func newWalk(w Walker, cfg *config.Config, owner, repo, revRange string, raws []gitsource.RawCommit, shallow bool, ranges lineRanges) *walk {
	inRange := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if raw.SHA != "" {
			inRange[raw.SHA] = true
		}
	}
	top, tagged := strings.CutSuffix(revRange, "..HEAD")
	if !tagged {
		top = ""
	}
	return &walk{
		Walker:       w,
		cfg:          cfg,
		owner:        owner,
		repo:         repo,
		revRange:     revRange,
		ranges:       ranges,
		facts:        Facts{Pulls: []PullExpansion{}, shallow: shallow},
		seen:         map[string]bool{},
		wedged:       map[string]int{},
		unjudged:     map[string]bool{},
		landings:     map[string]string{},
		readBackDone: map[string]bool{},
		readBackTop:  top,
		inRange:      inRange,
		coveredPulls: map[int]bool{},
		expanded:     map[int]bool{},
	}
}

// OpenPull is a pull's listing as --pr reads it: what GitHub lists, less every
// commit another pull already landed on the released branch, which is not
// this pull's own work. GitHub goes on listing a base pull's commits in the
// pull stacked on it after the base merges (DESIGN §4, "A stacked pull lists
// what its base landed"), and read whole the listing promised a level the
// release walk then refused to give (t-vc8c). The question is the walk's
// third, asked with the walk's read-back over the one window an open pull
// has: from its fork to HEAD, which holds what the base tag released and what
// merged since. Nothing is judged here; a dropped commit is said with a
// notice, as the walk says its own drops.
func (w Walker) OpenPull(ctx context.Context, cfg *config.Config, owner, repo string, number int) ([]gitsource.RawCommit, error) {
	listing, err := w.PullRawCommits(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	wk := newWalk(w, cfg, owner, repo, "", nil, false, lineRanges{})
	wk.readBackTop = "HEAD"
	if err := wk.readBack(ctx, listing); err != nil {
		return nil, err
	}
	own := make([]gitsource.RawCommit, 0, len(listing))
	for _, r := range listing {
		if on := wk.landings[r.SHA]; on != "" {
			w.Report.Noticef("commit %.7s in pull request #%d already landed on the released branch as %.7s through another pull request — it is not this pull's own work and is not counted", r.SHA, number, on)
			continue
		}
		own = append(own, r)
	}
	return own, nil
}

// visit folds one commit of the range in by what resolves it: an excluded
// author's commit joins before any request, a merge point expands the pull it
// resolves to, a covered commit stands aside for its pull's merge point, and
// everything else falls back to its own message. The error is the API's or
// git's as received; walkSince decides what it means for the verdict.
func (w *walk) visit(ctx context.Context, raw gitsource.RawCommit) error {
	// A merge point is a POINTER to a pull request, not a message being
	// judged, so only its AUTHOR excludes it before resolution: an
	// exclude_authors commit can never move the version, and the fleet's
	// daily bot push must not cost a round-trip. Neither its subject nor
	// its parent count may — judging the shape dropped a whole pull out of
	// a release in silence (t-7zt7; DESIGN §4). The commit still joins the
	// walk; whether the notes show it is their decision.
	if slices.Contains(w.cfg.ExcludeAuthors, raw.Author) {
		if raw.SHA != "" && w.seen[raw.SHA] {
			return nil
		}
		if raw.SHA != "" {
			w.seen[raw.SHA] = true
		}
		w.commits = append(w.commits, Commit{Raw: raw, Landed: true})
		return nil
	}
	w.visited++
	number, found, covering, rerr := w.mergedPullFor(ctx, w.owner, w.repo, raw.SHA, w.inRange)
	// Ledger first, whatever this commit resolves to: a commit can name the
	// pull that merged it AND a pull that merely carries it (a sub-PR's
	// squash commit is inside the branch of the pull that landed it), and
	// reconcile needs both sightings.
	for _, n := range covering {
		if !w.coveredPulls[n] {
			w.coveredPulls[n] = true
			w.coveredOrder = append(w.coveredOrder, n)
		}
	}
	lag := false
	switch {
	case github.IsCommitUnknown(rerr):
		// GitHub does not know the SHA yet (422) — the walk outran the API
		// right after a push. DESIGN §4's API lag: fall back, never fail.
		w.unknown++
		lag = true
	case rerr != nil:
		return rerr
	case found:
		if err := w.expand(ctx, number, raw.SHA); err != nil {
			return err
		}
		// The canonical commit is now represented BY that expansion, so it
		// joins the same set — recorded after the expansion, never before,
		// because a rebase-merge's merge_commit_sha is the last rebased
		// commit and appears in the listing it must still be folded from.
		if raw.SHA != "" {
			w.seen[raw.SHA] = true
		}
		return nil
	case len(covering) > 0:
		// Covered: part of a merged pull whose canonical commit is in this
		// range and expands it — a rebase-merge's earlier commits, a
		// merge-merged pull's branch commits (the log runs without
		// --first-parent). Counting this one too would double it; if the
		// canonical commit never resolves, reconcile names the pull.
		return nil
	}
	w.fallback(raw, lag)
	return nil
}

// expand folds one RESOLVED merged pull into the walk, canonical being the
// merge point that resolved to it; reconcile never calls it, because a
// listing fetched without a canonical commit in range says nothing about
// which of its commits belong to this walk (DESIGN §4, "warns and does not
// expand"). The listing is filtered BEFORE anything judges it — by where
// each commit landed (mainFootprint: the range governs whatever git can
// place; placeStacked: and whatever another pull landed) and by seen (an
// already-represented commit must never fail the release; its message may be
// a squash subject) — and what remains is pre-flighted against the pattern
// file HERE, where the pull association still exists to word the refusal
// (wedge).
func (w *walk) expand(ctx context.Context, number int, canonical string) error {
	listing, err := w.PullRawCommits(ctx, w.owner, w.repo, number)
	if err != nil {
		return err
	}
	// Recorded HERE, not in PullRawCommits, which --pr shares and which
	// carries no facts: only a walk acts on its verdict irreversibly.
	if len(listing) >= github.PullCommitsCap {
		w.facts.truncated = append(w.facts.truncated, number)
	}
	// Where did each listed commit LAND on the released branch? That is the
	// only question that lets the walk RANGE — a git fact — govern a listing
	// that knows nothing about it. A commit with no landing site is governed
	// by the pull alone and folds exactly as it always did; one that landed
	// is folded only if its landing site is in range.
	landed, err := w.mainFootprint(ctx, canonical, listing, "HEAD")
	if err != nil {
		return err
	}
	if err := w.placeStacked(ctx, canonical, listing, landed); err != nil {
		return err
	}
	contributed := 0
	for i, r := range listing {
		if on := landed[i]; on != "" {
			w.land(r.SHA, on)
			if !w.inRange[on] {
				w.Report.Noticef("commit %.7s in pull request #%d landed on the released branch as %.7s, which is outside %s — it shipped under an earlier tag and is not counted again", r.SHA, number, on, w.revRange)
				continue
			}
			// The landing site is the identity that matters downstream: the
			// notes must cite a commit the repository actually has, and a
			// rebase-merge's listing reports pre-rebase shas that exist on
			// no branch. Substituting here also makes the walk-wide dedup
			// below compare like with like.
			r.SHA = on
		}
		c := Commit{Raw: r, Pull: number, Landed: landed[i] != "", mergePoint: canonical}
		judged := w.judges(c.governing())
		if i, refused := w.wedged[r.SHA]; refused {
			// A refused commit a second pull lists (a stacked pull carries
			// its base pull's commits) is one row, and one more escape only
			// when the escapes named so far do not clear this sighting: a
			// base at an escape the read-back reaches from this listing's
			// fork drops the commit here before anything judges it, and
			// naming this pull's merge point too would send the operator
			// past this pull's own work (DESIGN §4, "Which base clears it").
			if judged {
				clears, err := w.readsBackTo(ctx, listing, w.wedges[i])
				if err != nil {
					return err
				}
				if !clears {
					w.wedges[i].escapes = append(w.wedges[i].escapes, placeEscape(w.owner, w.repo, number, canonical, onMain(r.SHA, w.inRange)))
				}
			}
			continue
		}
		judgeMessage := judged && !slices.Contains(w.cfg.ExcludeAuthors, r.Author)
		if r.SHA != "" && w.seen[r.SHA] {
			// An earlier pull listed it at an identity every line had
			// released, so it went in unjudged; this sighting's identity is
			// pending on some line, so its message is judged now, and the
			// refusal names this sighting's escape — never the other's,
			// which every line already passed.
			if judgeMessage && w.unjudged[r.SHA] {
				if row, ok := unclaimed(w.cfg, r); ok {
					w.refuse(row, number, canonical, r.SHA)
					continue
				}
			}
			w.Report.Noticef("commit %.7s in pull request #%d is already represented in this walk (a stacked branch carries its base PR's pre-squash commits; a merge-merged PR's commits sit on main under these same SHAs; a sub-PR's squash commit rides inside the PR that landed its branch) — counted once", r.SHA, number)
			continue
		}
		if judgeMessage {
			if row, ok := unclaimed(w.cfg, r); ok {
				w.refuse(row, number, canonical, r.SHA)
				continue
			}
		}
		if r.SHA != "" {
			w.seen[r.SHA] = true
			if !judged {
				w.unjudged[r.SHA] = true
			}
		}
		w.commits = append(w.commits, c)
		contributed++
	}
	w.facts.Pulls = append(w.facts.Pulls, PullExpansion{Number: number, Commits: contributed})
	w.expanded[number] = true
	return nil
}

// refuse records a refused commit from either arm: represented like any other
// commit, and remembered as refused, so one refused commit is one row however
// many pulls list it and however often the released branch meets it. number
// and mergePoint are the pull the walk resolved it from (0, "" on the
// fallback arm); sha is the commit's own identity, and onMain places the
// escape at it exactly when the range holds it — always for the fallback
// arm's commit, which is the log's own (newWalk indexes every sha visit is
// handed), and only where it landed in range for a listed one.
func (w *walk) refuse(row bump.Refusal, number int, mergePoint, sha string) {
	if sha != "" {
		w.seen[sha] = true
		w.wedged[sha] = len(w.wedges)
	}
	w.wedges = append(w.wedges, newWedge(row, w.owner, w.repo, number, mergePoint, onMain(sha, w.inRange)))
}

// judges reports whether the walk judges one sighting of a commit by its
// message — refuses it, or names one more escape for it — given gov, THIS
// sighting's governing identity: always on the single line, and under
// [[packages]] unless every line has already released that identity. A
// sighting at such an identity is walked only because the union had to
// contain it, and refused there it would name an escape every line has
// already taken; this refusal runs ahead of the partition's drop, so the
// drop is decided here (t-wjbs; DESIGN §4.1). A commit two pulls list is
// judged under each sighting's governing identity — a squash-merged pull's
// inner commit under that pull's merge point, a landed commit under its
// landing site whichever pull lists it — and one sighting judged is enough to
// refuse it: the refusal names the escape of every judged sighting, never one
// at an identity every line released (expand's seen arm: a stacked pull's
// later sighting skipped answers 0 where the line's own walk answers 3).
func (w *walk) judges(gov string) bool {
	return !w.ranges.releasedEverywhere(gov)
}

// fallback reads a commit no merged pull explains — a direct push, or the API
// lagging — from its own message, the evidence that is left. A merge commit
// GitHub has not indexed yet is a merge POINT the walk could not resolve, and
// its message never says what it merged — a fact a release decision needs
// (Facts.dropped), not only a warning.
func (w *walk) fallback(raw gitsource.RawCommit, lag bool) {
	if lag && len(raw.Parents) >= 2 {
		w.Report.Warnf("merge commit %.7s is not known to GitHub yet (API lag), so the pull request it may have merged could not be resolved — nothing was counted from it; if the release looks short, re-run it once GitHub has indexed the commit", raw.SHA)
		w.facts.dropped = append(w.facts.dropped, fmt.Sprintf("%.7s", raw.SHA))
		return
	}
	if i, refused := w.wedged[raw.SHA]; refused {
		// Refused inside a pull the walk expanded earlier, and now met on
		// the released branch itself: one row, one more escape — the
		// commit is its own base here — and no "counted once" for a
		// commit that was not. No judges gate: a refusal inside a pull is
		// recorded under this sha only as the listed commit's landing site
		// in range (mainFootprint), its governing identity there, so it was
		// judged under this very identity and the gate could never be false.
		w.wedges[i].escapes = append(w.wedges[i].escapes, placeEscape(w.owner, w.repo, 0, "", raw.SHA))
		return
	}
	if raw.SHA != "" && w.seen[raw.SHA] {
		w.Report.Noticef("commit %.7s was already folded in by a merged pull request — counted once", raw.SHA)
		return
	}
	if lag {
		// The lag arm stays SOFT on an unmatched message, unlike the fold:
		// this commit's true content is a pull the API has not indexed, so
		// it is dropped from the fold, warned about and carried in
		// Facts.dropped — release refuses to ACT on it, bump and notes
		// report it (DESIGN §4).
		if row, ok := unclaimed(w.cfg, raw); ok {
			w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) and its own message cannot be folded (%s) — nothing was counted from it; re-run once GitHub has indexed the commit", raw.SHA, row.Detail)
			w.facts.dropped = append(w.facts.dropped, fmt.Sprintf("%.7s", raw.SHA))
			return
		}
		w.Report.Warnf("commit %.7s is not known to GitHub yet (API lag) — reading its own message", raw.SHA)
	} else {
		// A direct push GitHub knows is judged HERE, like a pull's inner
		// commit: the fold's remedy (fix the message, add a pattern) is
		// authoring's, and this commit is on the released branch, where
		// the only way out is a base at or past it — the wedge's refusal,
		// with the escape at the commit itself (DESIGN §4).
		if row, ok := unclaimed(w.cfg, raw); ok && w.judges(raw.SHA) {
			w.refuse(row, 0, "", raw.SHA)
			return
		}
		if m, merr := w.cfg.Match(raw.Message); merr != nil || !m.Matched || !m.Skip {
			// A skip-pattern commit (a local git merge under the presets)
			// is passed over in silence; every other fallback announces
			// that a message, not a pull request, is the evidence.
			w.Report.Warnf("commit %.7s has no merged pull request (a direct push, or the API lagging) — reading its own message", raw.SHA)
		}
	}
	if raw.SHA != "" {
		w.seen[raw.SHA] = true
	}
	// Landed by construction: the fallback reads the walked commit
	// itself, an identity the released branch holds.
	w.commits = append(w.commits, Commit{Raw: raw, Landed: true})
}

// reconcile closes the walk: the covered ledger against the pulls expanded,
// and the resolution counts against each other. A pull the walk saw only from
// the INSIDE — its commits standing aside for a canonical commit that never
// resolved (API lag, or an exclude_authors author merged it with a real merge
// commit) — is WARNED about and deliberately NOT expanded: a listing carries
// nothing about this range, and expanding it was unsound both ways — a
// rebase-merge's pre-rebase SHAs double-count, released work comes back
// (DESIGN §4, "The walk warns and does not expand"). Naming the loss is what
// turns t-7zt7's silence into a visible one.
func (w *walk) reconcile() {
	for _, number := range w.coveredOrder {
		if w.expanded[number] {
			continue
		}
		w.facts.lostPulls = append(w.facts.lostPulls, number)
		w.Report.Warnf("pull request #%d has commits in %s but nothing in the range resolved to it — GitHub does not associate its merge commit with the pull yet, or an exclude_authors author merged it with a real merge commit (which the walk skips before resolution); those commits stood aside for that merge point and are NOT counted, so this release is short by whatever #%d changed. The pull's own commit listing is its whole history and cannot say which commits belong to %s, so the walk will not guess — re-run the release once GitHub has caught up with the merge point", number, w.revRange, number, w.revRange)
	}
	if w.visited > 0 && w.unknown == w.visited {
		w.facts.allUnknown = true
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		w.Report.Warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", w.visited, w.revRange, w.owner, w.repo)
	}
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
//     and an ancestor of tip? The walk asks of HEAD, not of the merge commit:
//     a commit that reached the branch by another route is just as released,
//     and a change that landed twice keeps the landing git states (t-nsww).
//     The read-back asks of the released pull's own merge point: what that
//     pull landed is what sits at or under it, and a sha the range holds
//     through a later merge is not its landing (readBackPull).
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
// the fleet's norm — unless another pull landed the commit (placeStacked).
func (w Walker) mainFootprint(ctx context.Context, canonical string, listing []gitsource.RawCommit, tip string) ([]string, error) {
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
	for i, r := range listing {
		if r.SHA != "" && have[r.SHA] {
			on, aerr := w.Git.IsAncestor(ctx, r.SHA, tip)
			if aerr != nil {
				return nil, aerr
			}
			if on {
				landed[i] = r.SHA
			}
		}
	}
	if err := w.alignRewritten(ctx, canonical, listing, landed); err != nil {
		return nil, err
	}
	return landed, nil
}

// replayable reports whether a listing entry placed at on is a position a
// rebase could still account for: not placed, and not a merge commit, which a
// rebase never replays (measured — alignRewritten). The alignment's window is
// exactly these, and so is what the read-back can be asked for on a squash's
// behalf (placeStacked); one predicate, so the two cannot drift apart.
func replayable(r gitsource.RawCommit, on string) bool {
	return on == "" && len(r.Parents) < 2
}

// alignRewritten is mainFootprint's second question, asked of whatever landed
// does not place yet, and it fills landed in only when every entry aligns.
func (w Walker) alignRewritten(ctx context.Context, canonical string, listing []gitsource.RawCommit, landed []string) error {
	// window is the listing positions a rebase could still account for, in listing
	// order: everything not placed, minus the entries a rebase never replays. Its
	// LENGTH is what makes the alignment checkable, so an entry that cannot have a
	// landing site has to leave — one that stays shifts every position after it and
	// the whole mapping is thrown away. A merge commit is never replayed by a rebase
	// (measured — see above), so it has no landing site to find and no position to
	// hold.
	window := make([]int, 0, len(listing))
	for i, r := range listing {
		if replayable(r, landed[i]) {
			window = append(window, i)
		}
	}
	// Nothing left to place — the whole merge-button shape, or an empty listing
	// — is one question, so one guard. The walk's canonical comes from its own
	// log, where falling through would only spend a `git log -n 0`;
	// TestMainFootprintEmptyListingAsksGitNothing hands in a canonical the
	// checkout lacks, where it is an exit-4 git error.
	if len(window) == 0 {
		return nil
	}
	mains, err := w.Git.FirstParentLog(ctx, canonical, len(window))
	if err != nil {
		return err
	}
	if len(mains) != len(window) {
		return nil
	}
	for k, i := range window {
		if strings.TrimSpace(mains[k].Message) != strings.TrimSpace(listing[i].Message) {
			return nil
		}
	}
	for k, i := range window {
		landed[i] = mains[k].SHA
	}
	return nil
}

// placeStacked places what another pull landed: a stacked pull lists its base
// pull's commits, and GitHub goes on listing them after the base merges and
// the stacked pull is retargeted, so without this a commit the base released
// folds again as the pull's own (t-ypxb; DESIGN §4, "A stacked pull lists
// what its base landed"). It is asked for the entries another pull's landing
// can still account for (open): what the footprint left — a squash put it on
// no branch — and what sits in the range under its own sha, which a merge
// commit's second parent brings in whatever an earlier squash or rebase of
// the same commit released. A commit a rebase replayed is the stacked pull's
// own, a rebase drops what its base put upstream, and a merge commit left
// over is no question; a listing with no open entry asks nothing.
//
// The identity is the listed sha, the one seen already judges a stacked
// duplicate by. A message would be a second rule, and it cannot hold: a
// multi-commit squash's subject is the pull's title, its body the text GitHub
// drops unless squash_merge_commit_message says otherwise (DESIGN §1), and a
// new commit repeating a released message would be dropped with it. What is
// left aligns once what another pull landed is out of the way: a stacked rebase
// replays only its own commits, so they are the run ending at the merge point
// even when another pull landed under them.
func (w *walk) placeStacked(ctx context.Context, canonical string, listing []gitsource.RawCommit, landed []string) error {
	open := false
	for i, r := range listing {
		open = open || w.open(r, landed[i])
	}
	if !open {
		return nil
	}
	if err := w.readBack(ctx, listing); err != nil {
		return err
	}
	placed := false
	for i, r := range listing {
		if on := w.landings[r.SHA]; w.open(r, landed[i]) && on != "" && on != landed[i] {
			landed[i], placed = on, true
		}
	}
	if !placed {
		return nil
	}
	return w.alignRewritten(ctx, canonical, listing, landed)
}

// open reports whether another pull's landing can still account for a listing
// entry the footprint placed at on: the entry is replayable, or it sits in the
// range under its own sha. A rebase's replay and a landing outside the range
// are the footprint's whole answer.
func (w *walk) open(r gitsource.RawCommit, on string) bool {
	return replayable(r, on) || (on == r.SHA && w.inRange[on])
}

// land records where a listed sha landed, under the first pull that landed it:
// the walk runs oldest first and the read-back reads its window oldest first,
// so the first answer is the pull that put the content on the released branch,
// and a later pull listing the same sha changes nothing.
func (w *walk) land(sha, on string) {
	if _, landed := w.landings[sha]; !landed && sha != "" {
		w.landings[sha] = on
	}
}

// readBack records in landings what the pulls between this listing's fork and
// readBackTop list: for the walk the base, because the pulls the range holds
// are expanded by the walk itself and a commit descending the base cannot be in
// the listing, so a fork in range costs nothing, a fork at the base two git
// calls and no request, and the whole-history arm has no base to read behind;
// for an open pull HEAD, the one window it has. The listing's own shas are
// never asked about: a checkout standing on the pull's branch holds them in the
// window, and an open pull's commit names no merged pull.
//
// Asked of the two questions every walk rests on, a merge point's merged pull
// and that pull's listing. Never of GET commits/{listed sha}/pulls, the one
// request that names the pull directly: for a sha no default branch holds it
// answers an open pull that carries the sha INSTEAD of the merged one that
// landed it (measured 2026-11-04, glyph-test-2#6 merged, #7 open).
func (w *walk) readBack(ctx context.Context, listing []gitsource.RawCommit) error {
	if w.readBackTop == "" {
		return nil
	}
	forks := forkPoints(listing, w.inRange)
	have, err := w.Git.Have(ctx, forks)
	if err != nil {
		return err
	}
	listed := make(map[string]bool, len(listing))
	for _, r := range listing {
		listed[r.SHA] = true
	}
	for _, fork := range forks {
		if !have[fork] {
			continue
		}
		missed, err := w.Git.Log(ctx, fork+".."+w.readBackTop)
		if err != nil {
			return err
		}
		for _, raw := range missed {
			if listed[raw.SHA] {
				continue
			}
			if err := w.readBackPull(ctx, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// readBackPull records what the pull raw is the merge point of landed, under
// visit's author gate: each listed sha as the commit the footprint places it
// at against that merge point — its own sha, or a rebase's replay — and, for
// what the footprint leaves, the merge point itself, a squash's one commit on
// the branch. A sha GitHub does not know names no pull, and is no shortfall:
// what it could have released is at worst counted again, the answer a listing
// with no landing site always gets. A listing GitHub capped is read as far as
// the cap, and the warning names what the cap risks here: a sha past it is
// matched by nothing and may count again, where the walk's own cap warning
// speaks of a verdict this pull is not in.
func (w *walk) readBackPull(ctx context.Context, raw gitsource.RawCommit) error {
	if w.readBackDone[raw.SHA] || slices.Contains(w.cfg.ExcludeAuthors, raw.Author) {
		return nil
	}
	w.readBackDone[raw.SHA] = true
	number, found, _, err := w.mergedPullFor(ctx, w.owner, w.repo, raw.SHA, nil)
	switch {
	case github.IsCommitUnknown(err):
		return nil
	case err != nil:
		return err
	case !found:
		return nil
	}
	released, capped, err := w.pullListing(ctx, w.owner, w.repo, number)
	if err != nil {
		return err
	}
	if capped {
		w.Report.Warnf("pull request #%d, read back for what it landed on the released branch, returned %d commits — GitHub truncates this listing at %d, so a commit past the cap cannot be matched and a later pull listing it may count it again", number, len(released), github.PullCommitsCap)
	}
	landed, err := w.mainFootprint(ctx, raw.SHA, released, raw.SHA)
	if err != nil {
		return err
	}
	for i, r := range released {
		if landed[i] == "" {
			landed[i] = raw.SHA
		}
		w.land(r.SHA, landed[i])
	}
	return nil
}

// readsBackTo reports whether a base at one of wd's escapes clears this
// listing's sighting of the refused commit on its own: the read-back of the
// walk that starts there reaches that escape from one of the listing's forks,
// so the pull behind the base lands the commit and nothing judges it here.
// Every parent no listed commit accounts for is a fork, in range or not: a
// tag at the escape puts it behind the base.
func (w *walk) readsBackTo(ctx context.Context, listing []gitsource.RawCommit, wd wedge) (bool, error) {
	forks := forkPoints(listing, nil)
	have, err := w.Git.Have(ctx, forks)
	if err != nil {
		return false, err
	}
	for _, fork := range forks {
		if !have[fork] {
			continue
		}
		for _, e := range wd.escapes {
			if e.base == fork {
				continue
			}
			below, err := w.Git.IsAncestor(ctx, fork, e.base)
			if err != nil {
				return false, err
			}
			if below {
				return true, nil
			}
		}
	}
	return false, nil
}

// forkPoints is where a listing left the branch it forked from: the parents no
// listed commit accounts for, less those in range (every one, over nil).
func forkPoints(listing []gitsource.RawCommit, inRange map[string]bool) []string {
	listed := make(map[string]bool, len(listing))
	for _, r := range listing {
		listed[r.SHA] = true
	}
	var forks []string
	for _, r := range listing {
		for _, p := range r.Parents {
			if !listed[p] && !inRange[p] && !slices.Contains(forks, p) {
				forks = append(forks, p)
			}
		}
	}
	return forks
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
				// First match wins: list order is GitHub's, and the loop running
				// on for covering pulls must not change which pull claims sha.
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
	local, capped, err := w.pullListing(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	if capped {
		// No silent caps: the verdict may be computed from a truncated PR, and
		// a missing commit could carry the deciding sigil.
		w.Report.Warnf("pull request #%d returned %d commits — GitHub truncates this listing at %d, so some commits (and their sigils) may be missing from the verdict", number, len(local), github.PullCommitsCap)
	}
	return local, nil
}

// pullListing is PullRawCommits without the warning, and whether the listing
// sits at GitHub's cap: the read-back reads a pull that is in no verdict, so
// it words the cap's risk itself (readBackPull).
func (w Walker) pullListing(ctx context.Context, owner, repo string, number int) ([]gitsource.RawCommit, bool, error) {
	raws, err := w.Pulls.PullCommits(ctx, owner, repo, number)
	if err != nil {
		return nil, false, err
	}
	local := make([]gitsource.RawCommit, len(raws))
	for i, r := range raws {
		local[i] = gitsource.RawCommit(r)
	}
	return local, len(raws) >= github.PullCommitsCap, nil
}
