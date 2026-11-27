package walk

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/github"
)

// PullExpansion records one merged pull the walk expanded and how many of
// its listed commits it took in, after the footprint filter and the walk-wide
// dedup — the `pulls` provenance a verdict reports (DESIGN §4). It records
// what the walk DID, never why: 0 has only innocent causes (a stacked pull
// whose commits rode in with its base, a merge-merged pull the fallback
// already folded, a pull whose every commit landed under an earlier tag), and
// an excluded author's and a skipped merge commit are taken in with the rest,
// so Commits is no count of participating commits (§4.1).
type PullExpansion struct {
	Number  int `json:"number"`
	Commits int `json:"commits"`
}

// Facts is what the walk observed about ITSELF, beside the commits it
// folded: the expansion provenance the verdict reports, and whether the walk
// came back knowing it had NOT read the whole range. Downstream a range that
// holds nothing and a range the walk could not read arrive as the same empty
// fold, and only these facts tell them apart (DESIGN §4, "fails loud (4)");
// the walk warns about every cause, and this is the warning's fact in a form
// a decision gates on.
type Facts struct {
	// Pulls is the per-pull expansion provenance (see PullExpansion).
	Pulls []PullExpansion
	// Unknown are the commits GitHub had not indexed (API lag) that the
	// fallback met, in walk order: exactly the commits its lag ::warning::
	// lines name, so a --json reader sees what a stderr reader is told
	// (bump, notes, release and preview carry it as unknown). Never a gate:
	// the arms that leave the fold short also record in dropped, which
	// Complete reads.
	Unknown []UnknownCommit
	// allUnknown: every commit the walk asked about was unknown to the queried
	// repository. One unknown SHA is API lag; ALL of them is what a wrong --repo
	// (or an inherited $GITHUB_REPOSITORY in a fork or a reusable-workflow
	// context) looks like from inside the walk.
	allUnknown bool
	// lostPulls are merged pulls whose commits stood aside for a merge point
	// nothing resolved, in walk order — the fold is short by whatever they
	// changed. DESIGN §4 refuses to guess at their listings; this records that
	// the refusal happened.
	lostPulls []int
	// dropped names the commits the walk could not read AT ALL because the API
	// had not indexed them: a merge commit unknown to GitHub is a merge POINT
	// the walk could not resolve, whose own message never says what it merged.
	// Deliberately NOT every fallback — a commit the fold reads from its own
	// message was READ, just from a weaker source, and DESIGN §4 blesses that
	// path. The line is "something is missing from the fold", not "the fold
	// used second-best evidence".
	dropped []string
	// truncated are pulls whose commit listing came back at GitHub's hard cap
	// (github.PullCommitsCap), in walk order: the API stops at that many
	// however far the pagination follows, so the commits past it are
	// unreachable, not absent, and any one could carry the deciding sigil.
	truncated []int
	// truncatedTags is, under [[packages]], the tag that takes the truncated
	// pulls out of each line whose range holds a merge point of theirs, as
	// lineTags words a wedge's: a commit past the cap may touch any line, and
	// a bare tag is no line's. "" on the single line, whose one tag needs no
	// naming.
	truncatedTags string
	// shallow: this checkout is a --depth clone, so git cannot answer the
	// footprint question at all — a commit it does not HAVE is
	// indistinguishable from one that never landed on the released branch.
	// The one member that is a property of the CHECKOUT rather than of what
	// the API returned, and the likeliest: actions/checkout defaults to
	// fetch-depth: 1 (DESIGN §4).
	shallow bool
	// filesCapped are the squash-arm inner commits whose file listing came
	// back at GitHub's hard cap (github.CommitFilesCap), in walk order — the
	// packages walk's own truncation. A package touched only past the cap is
	// unreachable, not absent, and the verdict that leaves it out is a
	// verdict computed over a range the walk could not read whole (DESIGN
	// §4.1). Empty whenever no packages are declared: no file is ever asked
	// for then.
	filesCapped []unreadListing
	// filesUnknown are the squash-arm inner commits whose file listing GitHub
	// answered 422 for (listFiles), in walk order — a capped listing holding
	// what the pages before the 422 listed, nothing when the first page
	// answered it, so a package touched in the files it did not list is
	// unreachable, not absent. Not §4's lag fallback: the sha came from GitHub's own pull
	// listing and its message was read, and the files are the one input
	// attribution has no weaker source for (DESIGN §4.1). Empty whenever no
	// packages are declared.
	filesUnknown []unreadListing
}

// UnknownCommit is one commit GitHub had not indexed, by its full sha, and
// the arm of the fallback it took: ArmMessage, ArmUnclaimed, ArmMerge or
// ArmExcluded.
type UnknownCommit struct {
	SHA string `json:"sha"`
	Arm string `json:"arm"`
}

// The lag arms an UnknownCommit names. Only ArmMessage counted anything; the
// other three are each one of fallback's "nothing was counted from it"
// warnings, and ArmMerge and ArmUnclaimed are dropped as well.
const (
	ArmMessage   = "message"   // read from its own message, which a pattern claims
	ArmUnclaimed = "unclaimed" // no pattern claims its own message
	ArmMerge     = "merge"     // a merge commit: the pull it may have merged could not be resolved
	ArmExcluded  = "excluded"  // an exclude_authors author's: a pull it may have merged could not be resolved
)

// unreadListing is one commit whose file listing GitHub did not give whole,
// as the shortfall names it: its short sha, the pull it was expanded from,
// and the tags that take it out of each line whose range holds it
// (lineEscapes). Neither cause clears on the re-run the release refusal asks
// for — a capped listing is capped again, and a 422 clears only once GitHub
// lists the commit — so the escapes are the clause's own remedy (DESIGN
// §4.1). On preview's PR side pull is the pull under review and there are
// no escapes: that pull has no merge point to cut a tag at.
type unreadListing struct {
	sha     string
	pull    int
	escapes []string
}

// unreadClause names the unread listings for a shortfall clause, each with
// remedy — what this cause leaves a re-run — ahead of its escapes, or bare
// when it has none. Bare shas join with commas, as the PR side's always
// have; a remedy carries commas of its own, so entries that have one join
// with semicolons.
func unreadClause(ls []unreadListing, remedy string) string {
	parts := make([]string, 0, len(ls))
	sep := ", "
	for _, l := range ls {
		if len(l.escapes) == 0 {
			parts = append(parts, l.sha)
			continue
		}
		sep = "; "
		parts = append(parts, fmt.Sprintf("%s in pull request #%d — %s cut %s", l.sha, l.pull, remedy, strings.Join(l.escapes, " / ")))
	}
	return strings.Join(parts, sep)
}

// Complete reports that the walk read the range it was asked about.
// Everything glyph does that it cannot take back — deleting a draft, lowering
// the version a human is about to publish — is gated on it.
func (f Facts) Complete() bool {
	return !f.allUnknown && !f.shallow && len(f.lostPulls) == 0 && len(f.dropped) == 0 && len(f.truncated) == 0 &&
		len(f.filesCapped) == 0 && len(f.filesUnknown) == 0
}

// ShallowShortfall is Shortfall's clause for a shallow checkout, exported for
// preview's floor guard: on a shallow checkout with no tag within reach the
// walk is skipped, so no Facts carry it, and the comment must still quote the
// one clause a walked shallow checkout gets (t-txed).
const ShallowShortfall = "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)"

// Shortfall says, in one clause, what the walk could not read — for release's
// exit-4 refusal and for the preview comment's PendingShort / PRShort lines.
func (f Facts) Shortfall(owner, repo string) string {
	var parts []string
	if f.allUnknown {
		parts = append(parts, fmt.Sprintf("every commit in it was unknown to %s/%s (check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to)", owner, repo))
	}
	if f.shallow {
		parts = append(parts, ShallowShortfall)
	}
	for _, n := range f.lostPulls {
		parts = append(parts, fmt.Sprintf("merged pull request #%d has commits here that stood aside for a merge point nothing resolved", n))
	}
	if len(f.dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) GitHub had not indexed could not be read (%s)", len(f.dropped), strings.Join(f.dropped, ", ")))
	}
	for _, n := range f.truncated {
		parts = append(parts, fmt.Sprintf("merged pull request #%d returned the maximum %d commits, so GitHub truncated its listing and the rest could not be read", n, github.PullCommitsCap))
	}
	if len(f.filesCapped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) returned the maximum %d files, so GitHub truncated the listing and a package touched past it could not be read (%s)", len(f.filesCapped), github.CommitFilesCap, unreadClause(f.filesCapped, "no re-run lists past the cap:")))
	}
	if len(f.filesUnknown) > 0 {
		parts = append(parts, fmt.Sprintf("GitHub answered 422 for the file listing of %d commit(s), so a line they touch in files it did not list could not be read (%s)", len(f.filesUnknown), unreadClause(f.filesUnknown, "re-run once GitHub lists its files, else")))
	}
	return strings.Join(parts, "; ")
}

// Remedy is release's next step for what Shortfall names, chosen from the same
// facts and never from its text: first a tag cut past what no re-run can read
// (a capped listing is capped again on every run — DESIGN §4's wedge escape;
// the capped file listings' clause names its tags itself; under [[packages]]
// the truncated pulls' step names its tag per line, truncatedTags), then the
// re-run that clears the rest — API lag, and the --repo and checkout fixes
// whose clauses carry their own remedy. Each step once.
func (f Facts) Remedy() string {
	var steps []string
	if len(f.truncated) > 0 {
		at := make([]string, len(f.truncated))
		for i, n := range f.truncated {
			at[i] = fmt.Sprintf("merged pull request #%d's merge point", n)
		}
		step := "cut a tag at or past what the walk cannot read, " + strings.Join(at, " and ")
		if f.truncatedTags != "" {
			step = "cut a tag on every line whose range holds what the walk cannot read, " + strings.Join(at, " and ") + " — " + f.truncatedTags
		}
		steps = append(steps, step)
	}
	if len(f.filesCapped) > 0 {
		steps = append(steps, "cut the tags the capped file listings name")
	}
	if f.allUnknown || f.shallow || len(f.lostPulls) > 0 || len(f.dropped) > 0 || len(f.filesUnknown) > 0 {
		steps = append(steps, "re-run the release once the walk can read the range")
	}
	return strings.Join(steps, "; then ")
}
