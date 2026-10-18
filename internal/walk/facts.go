package walk

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/github"
)

// PullExpansion records one merged pull request the walk expanded — resolved
// from its canonical commit — and how many of its listed commits the walk
// took in, after the footprint filter and the walk-wide SHA dedup: a stacked
// PR whose commits all rode in with its base PR reports 0, and so does a
// merge-merged PR whose commits the walk already folded in on the fallback
// path. An excluded author's and a skipped merge commit are taken in with the
// rest, so Commits is no count of participating commits (DESIGN §4.1). This
// is the walk reporting its own expansion facts: how a verdict was assembled,
// in a form a human or a CI step can read back afterwards without re-deriving
// the walk's exclusion rules somewhere else.
//
// It records what the walk DID, and never why a number is what it is. A count
// of 0 is the case that invites a wrong reading — the dedup cases above report
// it, and so does a pull whose every listed commit landed under an earlier tag
// (the footprint filter) — so nothing may treat 0 as "this pull changed
// nothing".
type PullExpansion struct {
	Number  int `json:"number"`
	Commits int `json:"commits"`
}

// Facts is what the walk observed about ITSELF, beside the commits it
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
type Facts struct {
	// Pulls is the per-pull expansion provenance (see PullExpansion).
	Pulls []PullExpansion
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
	// had not indexed them: a merge commit unknown to GitHub is a merge POINT
	// the walk could not resolve, whose own message never says what it merged.
	// Deliberately NOT every fallback — a commit the fold reads from its own
	// message was READ, just from a weaker source, and DESIGN §4 blesses that
	// path. The line is "something is missing from the fold", not "the fold
	// used second-best evidence".
	Dropped []string
	// Truncated are pulls whose commit listing came back at GitHub's hard cap
	// (github.PullCommitsCap), in walk order. The API stops at that many however
	// far the pagination follows, so a listing of exactly the cap is one glyph
	// cannot claim to have read whole — the commits past it are unreachable, not
	// absent, and any one of them could carry the deciding sigil.
	//
	// It belongs here rather than in the warning alone for the same reason every
	// other shortfall member does: the walk already SAID it could not read the
	// range, and then deleted a draft and lowered a version on the strength of
	// the fold anyway.
	// Measured before this arm existed: a pull returning 250 :memo: commits, with
	// a v0.2.0 draft present, exited 1 with action "delete" and the write set was
	// exactly one DELETE — in the same run whose stderr carried the truncation
	// warning.
	Truncated []int
	// Shallow: this checkout is a --depth clone, so git cannot answer the
	// footprint question at all — a commit it does not HAVE is indistinguishable
	// from one that never landed on the released branch.
	//
	// It is the only member of this struct that is a property of the CHECKOUT
	// rather than of what the API returned, and it is the one most likely to
	// appear: actions/checkout defaults to fetch-depth: 1, so a single line
	// removed from a release workflow produces it. Measured on a real
	// `git clone --depth 1`: the walk folds commits an earlier tag already
	// shipped and answers minor where the full-history control on the same
	// repository answers patch — loud (two warnings) and green.
	Shallow bool
	// FilesCapped are the squash-arm inner commits whose file listing came
	// back at GitHub's hard cap (github.CommitFilesCap), in walk order — the
	// packages walk's own truncation. A package touched only past the cap is
	// unreachable, not absent, and the verdict that leaves it out is a
	// verdict computed over a range the walk could not read whole (DESIGN
	// §4.1). Empty whenever no packages are declared: no file is ever asked
	// for then.
	FilesCapped []UnreadListing
	// FilesUnknown are the squash-arm inner commits whose file listing GitHub
	// answered 422 for (listFiles), in walk order — a capped listing holding
	// what the pages before the 422 listed, nothing when the first page
	// answered it, so a package touched in the files it did not list is
	// unreachable, not absent. Not §4's lag fallback: the sha came from GitHub's own pull
	// listing and its message was read, and the files are the one input
	// attribution has no weaker source for (DESIGN §4.1). Empty whenever no
	// packages are declared.
	FilesUnknown []UnreadListing
}

// UnreadListing is one commit whose file listing GitHub did not give whole,
// as the shortfall names it: its short sha, the pull it was expanded from,
// and the tags that take it out of each line whose range holds it
// (lineEscapes). Neither cause clears on the re-run the release refusal asks
// for — a capped listing is capped again, and a 422 clears only once GitHub
// lists the commit — so the escapes are the clause's own remedy (DESIGN
// §4.1). On preview's PR side Pull is the pull under review and there are
// no escapes: that pull has no merge point to cut a tag at.
type UnreadListing struct {
	SHA     string
	Pull    int
	Escapes []string
}

// unreadClause names the unread listings for a shortfall clause, each with
// remedy — what this cause leaves a re-run — ahead of its escapes, or bare
// when it has none. Bare shas join with commas, as the PR side's always
// have; a remedy carries commas of its own, so entries that have one join
// with semicolons.
func unreadClause(ls []UnreadListing, remedy string) string {
	parts := make([]string, 0, len(ls))
	sep := ", "
	for _, l := range ls {
		if len(l.Escapes) == 0 {
			parts = append(parts, l.SHA)
			continue
		}
		sep = "; "
		parts = append(parts, fmt.Sprintf("%s in pull request #%d — %s cut %s", l.SHA, l.Pull, remedy, strings.Join(l.Escapes, " / ")))
	}
	return strings.Join(parts, sep)
}

// complete reports that the walk read the range it was asked about. Everything
// glyph does that it cannot take back — deleting a draft, lowering the version a
// human is about to publish — is gated on it.
func (f Facts) Complete() bool {
	return !f.AllUnknown && !f.Shallow && len(f.LostPulls) == 0 && len(f.Dropped) == 0 && len(f.Truncated) == 0 &&
		len(f.FilesCapped) == 0 && len(f.FilesUnknown) == 0
}

// shortfall says, in one clause, what the walk could not read — for release's
// exit-4 refusal and for the preview comment's PendingShort / PRShort lines.
func (f Facts) Shortfall(owner, repo string) string {
	var parts []string
	if f.AllUnknown {
		parts = append(parts, fmt.Sprintf("every commit in it was unknown to %s/%s (check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to)", owner, repo))
	}
	if f.Shallow {
		parts = append(parts, "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)")
	}
	for _, n := range f.LostPulls {
		parts = append(parts, fmt.Sprintf("merged pull request #%d has commits here that stood aside for a merge point nothing resolved", n))
	}
	if len(f.Dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) GitHub had not indexed could not be read (%s)", len(f.Dropped), strings.Join(f.Dropped, ", ")))
	}
	for _, n := range f.Truncated {
		parts = append(parts, fmt.Sprintf("merged pull request #%d returned the maximum %d commits, so GitHub truncated its listing and the rest could not be read", n, github.PullCommitsCap))
	}
	if len(f.FilesCapped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) returned the maximum %d files, so GitHub truncated the listing and a package touched past it could not be read (%s)", len(f.FilesCapped), github.CommitFilesCap, unreadClause(f.FilesCapped, "no re-run lists past the cap:")))
	}
	if len(f.FilesUnknown) > 0 {
		parts = append(parts, fmt.Sprintf("GitHub answered 422 for the file listing of %d commit(s), so a line they touch in files it did not list could not be read (%s)", len(f.FilesUnknown), unreadClause(f.FilesUnknown, "re-run once GitHub lists its files, else")))
	}
	return strings.Join(parts, "; ")
}
