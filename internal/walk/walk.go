// Package walk is the release-time walk of DESIGN §4 and §4.1: the range
// past a tag, resolved out of the tags HEAD's history holds; the expansion
// of every merge point in it into the pull request's own commits, governed
// by where each listed commit landed on the released branch; the facts the
// walk records about what it could not read; and, under [[packages]], which
// lines each walked commit joins.
//
// It reaches outside itself through the three ports it declares — Git,
// Pulls, Reporter — and names no directory, flag or stream of its own: the
// --since-tag value arrives as the string CheckTag accepted, and every
// warning and notice goes to the Reporter, so DESIGN §5's stream order is
// the caller's to keep. It holds no copy of what the packages below decide:
// a message's meaning (internal/config's pattern match), the lattice and the
// fold (internal/bump), which package a diff moves (internal/attribution).
// The data it hands around is theirs too — gitsource.RawCommit for a commit
// as git or GitHub reports it, github's PullRef and Commit on the Pulls port.
package walk

import (
	"context"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/notes"
)

// Git is the checkout the walk reads: the ten questions it asks local git,
// each the gitsource function of the same name bound to one directory
// (gitsource.Checkout). Every verdict command runs inside a checkout of the
// repository being released (DESIGN §4), and this is that checkout.
type Git interface {
	Log(ctx context.Context, revRange string) ([]gitsource.RawCommit, error)
	IsShallow(ctx context.Context) (bool, error)
	Tags(ctx context.Context) ([]string, error)
	MergedTags(ctx context.Context, rev string) ([]string, error)
	IsTag(ctx context.Context, name string) (bool, error)
	Have(ctx context.Context, shas []string) (map[string]bool, error)
	IsAncestor(ctx context.Context, sha, rev string) (bool, error)
	FirstParentLog(ctx context.Context, rev string, n int) ([]gitsource.RawCommit, error)
	DiffTreeFiles(ctx context.Context, sha string) ([]string, error)
	MergeBase(ctx context.Context, revs []string) (string, error)
}

// Pulls is GitHub as the walk asks it: which merged pull a merge point
// belongs to, a pull's own commits, and the files of a commit no branch
// holds. *github.Client is the one implementation that ships.
type Pulls interface {
	CommitPulls(ctx context.Context, owner, repo, sha string) ([]github.PullRef, error)
	PullCommits(ctx context.Context, owner, repo string, number int) ([]github.Commit, error)
	CommitFiles(ctx context.Context, owner, repo, sha string) (files []string, capped bool, err error)
}

// Reporter is where the walk says what it saw and could not read, in the
// order it saw it: Warnf is a ::warning::, Noticef a ::notice:: (DESIGN §5's
// stream contract, kept by the caller).
type Reporter interface {
	Warnf(format string, a ...any)
	Noticef(format string, a ...any)
}

// Walker is the walk's reader: what it reads git and GitHub through, and
// where it reports. Pulls is nil for a walk that resolves nothing over the
// API (Pending), which never reaches it.
type Walker struct {
	Git    Git
	Pulls  Pulls
	Report Reporter
}

// Result is the shape bump, notes, release and preview's pending side
// read their commits in — from sinceTagInput for the walk, and from
// rangeLines and pullLines for the sources that are no walk (readSource):
// every walked commit that is unreleased on at least one line (All, walk
// order — the union fold's input, and the single line's), whatever line it
// joins or none: partitionLines appends to it before it places anything.
// Then the walk's own facts, the
// revision range walked, the single line's step base (nil when packages are
// declared: each line carries its own), and the per-line partition — exactly
// one entry, holding All, when no packages are declared.
type Result struct {
	All    []Commit
	Facts  Facts
	Source string
	Base   *bump.Version
	Lines  []LineWalk
}

// Scope narrows the walk to the lines a caller actually asked about, and
// names the escape hatch that caller can offer. Attribution still runs over
// EVERY declared line — a nested package must keep taking its files out of
// its parent — so the scope decides the RANGE only.
//
// preview is its one user: its pending side is asked per touched line, and a
// declared line the pull does not touch used to drag the union to the whole
// history and refuse the command at the cap (t-60dc symptom A), or pay one
// API round-trip per commit of the whole history under it (symptom B).
type Scope struct {
	Only   []config.Package // nil: every declared line
	Escape string           // "": the --since-tag sentence
}

// sinceTagInputScoped is sinceTagInput with the range narrowed to scope.
func (w Walker) Since(ctx context.Context, cfg *config.Config, tagFlag, owner, repo string, scope *Scope) (Result, error) {
	lines, revRange, err := w.resolveLinesScoped(ctx, cfg, tagFlag, scope)
	if err != nil {
		return Result{}, err
	}
	commits, facts, err := w.walkSince(ctx, cfg, owner, repo, revRange)
	if err != nil {
		return Result{}, err
	}
	lws, all, err := w.partitionLines(ctx, cfg, owner, repo, commits, &facts, lines)
	if err != nil {
		return Result{}, err
	}
	var base *bump.Version
	if len(cfg.Packages) == 0 {
		base = lines[0].Base
	}
	return Result{All: all, Facts: facts, Source: revRange, Base: base, Lines: lws}, nil
}

// pendingWalk partitions commits read outside the walk over pendingLines.
// The walk facts are empty: nothing was resolved over the API. The API
// client is nil, so attribution reads local git alone — which is why the
// commits must be landed identities the checkout holds (walkedFiles), as a
// local range's are, and why a pull's listing reaches it only on the bare
// line, where partitionLines is the identity and no file is read.
func (w Walker) Pending(ctx context.Context, cfg *config.Config, commits []Commit, source string) (Result, error) {
	facts := Facts{Pulls: []PullExpansion{}}
	lws, all, err := w.partitionLines(ctx, cfg, "", "", commits, &facts, PendingLines(cfg, source))
	if err != nil {
		return Result{}, err
	}
	return Result{All: all, Facts: facts, Source: source, Lines: lws}, nil
}

// walked is one folded commit plus the provenance only the walk knows: the
// merged pull request it was expanded from (0 on the fallback path) and
// whether its SHA is a *landed* identity (glossary) — a commit the released
// branch actually holds. The two downstream consumers read different halves:
// classification reads the bare gitsource.RawCommit (walkedBumpCommits), the
// notes read the citation (walkedNoteCommits) — the pull beside the sha, and
// the pull ALONE for a footprint-less commit, whose listed sha exists on no
// branch and used to be published anyway (t-xxhj: a body citing shas
// `git branch -r --contains` answers nothing for).
type Commit struct {
	Raw    gitsource.RawCommit
	Pull   int
	Landed bool
	// MergePoint is the on-branch commit the pull was resolved from — GitHub's
	// merge_commit_sha — "" on the fallback path. With packages declared it
	// is the identity the lines' ranges judge a footprint-less commit by
	// (walked.governing): a squash-merged pull's inner commit is unreleased
	// on a line exactly when its merge point is.
	MergePoint string
}

// governing is the on-branch commit the walk range judges a walked commit by:
// its own sha when it landed on the released branch, else the merge point of
// the pull it was expanded from (DESIGN §4.1: a listed commit joins a line
// only when its governing commit is unreleased there).
func (c Commit) Governing() string {
	if c.Landed || c.MergePoint == "" {
		return c.Raw.SHA
	}
	return c.MergePoint
}

// walkedBumpCommits strips the provenance for the fold: bump.Fold reads
// commits, not citations.
func BumpCommits(ws []Commit) []bump.Commit {
	out := make([]bump.Commit, 0, len(ws))
	for _, w := range ws {
		out = append(out, bump.Commit{SHA: w.Raw.SHA, Author: w.Raw.Author, Message: w.Raw.Message})
	}
	return out
}

// walkedNoteCommits hands the fold to the notes with its citation: the pull
// number beside every expanded commit, and the SHA blanked for a commit that
// landed under no identity of its own — the squash arm, the one arm whose
// listed shas no branch holds. Blanked HERE, where DESIGN §4's arms are a
// known fact, because internal/notes renders what it is given and holds no
// arm knowledge.
func NoteCommits(ws []Commit) []notes.Commit {
	out := make([]notes.Commit, 0, len(ws))
	for _, w := range ws {
		sha := w.Raw.SHA
		if w.Pull > 0 && !w.Landed {
			sha = ""
		}
		out = append(out, notes.Commit{SHA: sha, Pull: w.Pull, Author: w.Raw.Author, Login: identity(w.Raw), Message: w.Raw.Message})
	}
	return out
}

// identity is the GitHub login the notes may credit a commit's author BY:
// the login GitHub itself mapped the commit to when the API described it,
// else the login a noreply author address carries, else "" — and "" is an
// answer, never the author name: a name is free text and @<name> pages
// whoever happens to own it (t-39fy).
func identity(r gitsource.RawCommit) string {
	if r.Login != "" {
		return r.Login
	}
	return github.LoginFromNoreply(r.Email)
}
