package walk

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/attribution"
	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/github"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// Place is how partitionLines and preview's pull side place one commit on
// the declared lines.
type Place int

const (
	// PlacedByFiles: the commit's own diff decides — with its scope and sigil
	// when the fold reads the message, by files alone when it does not.
	PlacedByFiles Place = iota
	// PlacedEverywhere: a message no pattern claims joins every line it is
	// unreleased on, so the fold refuses it there (§3); it is not attributed.
	PlacedEverywhere
	// PlacedNowhere: a skip-pattern commit is in no fold and no section, so
	// no line holds it and its files are never asked for.
	PlacedNowhere
)

// Reading is what attribution may read of one commit's message: the scope
// and sigil the claiming pattern handed on, and which pattern that was —
// which decides no placement and only words a refusal (Attribute). unjudged
// is the Reading of a message it may read nothing of: no scope, the none
// sigil, no pattern, so files alone place the commit and nothing refuses it.
type Reading struct {
	scope   string
	sigil   config.Sigil
	pattern int // position in cfg.Patterns; -1 when the message is not read
}

var unjudged = Reading{sigil: config.SigilNone, pattern: -1}

// ReadingOf is the Reading of a message the fold reads, from its match.
func ReadingOf(m config.Match) Reading {
	return Reading{scope: m.Groups[config.ScopeGroup], sigil: m.Sigil, pattern: m.PatternIndex}
}

// PlaceOf reads what attribution may read of a commit's message: its scope
// and sigil when the fold reads the message, nothing when it does not. An
// exclude_authors commit is placed by its files alone — the message is
// exactly what glyph declared it would not judge, so neither its scope nor
// its sigil may carry it or refuse it (attribution.Attribute with no scope
// and the none sigil: files decide, and under no package it is placed
// nowhere, the shape rule 3 gives a shared-only `=`).
func PlaceOf(cfg *config.Config, raw gitsource.RawCommit) (Place, Reading) {
	if slices.Contains(cfg.ExcludeAuthors, raw.Author) {
		return PlacedByFiles, unjudged
	}
	m, err := cfg.Match(raw.Message)
	if err != nil || !m.Matched {
		return PlacedEverywhere, unjudged
	}
	if m.Skip {
		return PlacedNowhere, unjudged
	}
	return PlacedByFiles, ReadingOf(m)
}

// Attribute is the one door the walk, lint --range and preview ask
// attribution through. The verdict is attribution.Attribute's, over the four
// inputs it has always read. What this adds is the half of a refusal's
// sentence only a caller knows: that the commit is a merge commit (its diff
// is never read, so files is nil whatever it touched), that the diff was not
// read whole (whole false: a listing GitHub capped or cut short, a shallow
// boundary — the refusal a caller then withholds), and what this message can
// write under the pattern that claimed it (config.Sayable, which is handed
// the message because an escape that takes something out of it — the scope,
// the sigil — is proven by matching the rewritten message, not read off the
// pattern). None of it can move the answer: it is set on a refusal already
// returned. One door, because a call site that forgets one field moves no
// verdict and every test stays green while its refusal words a pattern
// nobody described (t-mfny; DESIGN §4.1; mutation rows
// <arm>-refuses-a-merge-commit-as-touching-no-file).
func Attribute(cfg *config.Config, raw gitsource.RawCommit, said Reading, files []string, whole bool) ([]config.Package, error) {
	moved, err := attribution.Attribute(files, said.scope, said.sigil, cfg.Packages)
	if r, ok := errors.AsType[*attribution.Refusal](err); ok {
		r.Merge = raw.Parents >= 2
		r.Unread = !whole
		r.Sayable, _ = cfg.Sayable(said.pattern, raw.Message)
	}
	return moved, err
}

// lineRanges is the lines a walk answers for and, under [[packages]], the
// shas each line's own range holds — the commits unreleased on it — with a
// nil set for a line every walked commit is unreleased on (no range: the
// whole history, a --range fold, a pull's listing). With no packages
// declared holds is nil: the one line's range is the walk's own, and no
// commit is asked about per line.
type lineRanges struct {
	lines []Line
	holds []map[string]bool
}

// readLineRanges reads each line's own range from local git, once, before
// the walk: the walk asks it of every commit it would refuse, and the
// partition of every commit it places.
func (w Walker) readLineRanges(ctx context.Context, cfg *config.Config, lines []Line) (lineRanges, error) {
	r := lineRanges{lines: lines}
	if len(cfg.Packages) == 0 {
		return r, nil
	}
	r.holds = make([]map[string]bool, len(lines))
	for i, l := range lines {
		if l.Range == "" {
			continue
		}
		raws, err := w.Git.Log(ctx, l.Range)
		if err != nil {
			return lineRanges{}, err
		}
		set := make(map[string]bool, len(raws))
		for _, raw := range raws {
			set[raw.SHA] = true
		}
		r.holds[i] = set
	}
	return r, nil
}

// reach is the lines whose range holds gov, a commit's governing identity —
// the lines it is unreleased on, by index in declared order; on the single
// line, that line.
func (r lineRanges) reach(gov string) []int {
	var out []int
	for i := range r.lines {
		if r.holds == nil || r.holds[i] == nil || r.holds[i][gov] {
			out = append(out, i)
		}
	}
	return out
}

// releasedEverywhere reports an identity the union walk holds that no line's
// range does: every line has released it, which a union starting at the
// bases' common ancestor walks wherever that ancestor sits behind them all
// (a criss-cross; DESIGN §4.1, "The walk is one walk"). The walk judges no
// sighting at such an identity (judges), and the partition drops a commit
// it governs with a notice.
func (r lineRanges) releasedEverywhere(gov string) bool {
	return r.holds != nil && len(r.reach(gov)) == 0
}

// partitionLines splits the walk's commits over the lines. With no packages
// declared it is the identity: one line holding every commit, nothing asked
// of git or the API.
//
// With packages, a commit JOINS line p when it is UNRELEASED on p (its
// governing commit is in p's range, lineRanges) AND its own diff PLACES it
// on p (DESIGN §4.1, "The walk is one walk"). The second question is asked
// of every commit's files (PlaceOf): with its scope and sigil when the fold
// reads the message, by files alone for an exclude_authors commit — on no
// line when its files touch none (t-sr1c; mutation row
// packages-excluded-author-placed-on-every-line) — nowhere for a skip, and
// on every line it is unreleased on for a message no pattern claims, so the
// fold refuses it there (§3). A commit the union holds that is released on
// every line is dropped with a notice: it was walked because the union had
// to contain it. PlaceByFiles withholds a refusal over a diff the walk did
// not read whole; every one over a diff read whole is collected with the
// lines it wedges and handed down once the whole range is placed, for the
// caller to word (attributionWedge, rangeRefusal) — one red run for every
// refused commit, as lint --range over the same range answers (DESIGN §4.1,
// rule 3). A refusal already collected outranks a later failure to place
// the rest (Outranks).
func (w Walker) partitionLines(ctx context.Context, cfg *config.Config, owner, repo string, commits []Commit, facts *Facts, ranges lineRanges) ([]LineWalk, []Commit, []placementRefusal, error) {
	lines := ranges.lines
	if len(cfg.Packages) == 0 {
		return []LineWalk{{Line: lines[0], Commits: commits}}, commits, nil, nil
	}
	lws := make([]LineWalk, len(lines))
	for i := range lines {
		lws[i] = LineWalk{Line: lines[i], Commits: []Commit{}}
	}
	var all []Commit
	var refused []placementRefusal
	for _, c := range commits {
		reach := ranges.reach(c.Governing())
		if len(reach) == 0 {
			w.Report.Noticef("commit %.7s is inside the union walk but already released on every line — not counted", c.Raw.SHA)
			continue
		}
		all = append(all, c)
		var carriers []int
		switch place, said := PlaceOf(cfg, c.Raw); place {
		case PlacedEverywhere:
			carriers = reach
		case PlacedNowhere:
		case PlacedByFiles:
			placed, ferr := w.PlaceByFiles(ctx, cfg, owner, repo, c, said, facts, reachedLines(lines, reach))
			if ferr != nil {
				if w.Outranks(len(refused) > 0, ferr) {
					return nil, nil, refused, nil
				}
				return nil, nil, nil, ferr
			}
			if placed.Refused != nil {
				refused = append(refused, placementRefusal{err: placed.Refused, c: c, reach: reach})
				continue
			}
			for _, i := range reach {
				if slices.ContainsFunc(placed.Moved, func(p config.Package) bool { return p.Path == lines[i].Package.Path }) {
					carriers = append(carriers, i)
				}
			}
			if placed.gap == diffBoundary {
				w.warnBoundary(c.Raw.SHA, reachedLines(lines, carriers), placed.withheld)
			}
		}
		for _, i := range carriers {
			lws[i].Commits = append(lws[i].Commits, c)
		}
	}
	return lws, all, refused, nil
}

// placementRefusal is one commit attribution refused over a diff read whole,
// with the lines whose range holds it (by index) — the lines it wedges.
type placementRefusal struct {
	err   error
	c     Commit
	reach []int
}

func (r placementRefusal) row() bump.Refusal {
	return bump.Refusal{SHA: r.c.Raw.SHA, Subject: bump.FirstLine(r.c.Raw.Message), Detail: r.err.Error()}
}

// FilePlacement is where a commit's own diff places it (PlaceByFiles): the
// packages its files move, how much of the diff was read, and attribution's
// refusal — Refused over a diff read whole, which each caller words for
// where the commit sits, or withheld over one that was not, which is no
// verdict and leaves the commit on no line.
type FilePlacement struct {
	Moved    []config.Package
	gap      diffGap
	Refused  error
	withheld error
}

// PlaceByFiles places one commit by its own diff — the one place the walk
// (partitionLines) and preview's pull side do it, so the two cannot read a
// listing, record it unread or withhold a refusal two ways. walkedFiles reads
// the files and records a listing GitHub did not give whole on facts, with
// the escapes of the lines reached — none on preview's pull side, which has
// no merge point a tag could be cut at.
//
// A refusal over a diff not read whole (diffGap: a capped or 422-cut
// listing, a shallow clone's boundary) is not a finding and never wedges —
// "no carrier" is a claim about files nobody read, and the package past the
// cap may be the one named — so the commit is carried nowhere and the
// caller's shortfall answers, never the gate code (t-c6r5). A non-refusal
// answer stands over such a diff: a scope still carries the commit (rule 2),
// and so do the files a listing did hold (t-esm5). One arm withholds for all
// three so they cannot come to place a commit two ways again (DESIGN §4.1,
// "Where the files come from"); each shortfall stays its own — a boundary is
// no UnreadListing — and so does its warning (warnBoundary).
func (w Walker) PlaceByFiles(ctx context.Context, cfg *config.Config, owner, repo string, c Commit, said Reading, facts *Facts, reached []Line) (FilePlacement, error) {
	files, gap, err := w.walkedFiles(ctx, owner, repo, c, facts, reached)
	if err != nil {
		return FilePlacement{}, err
	}
	moved, aerr := Attribute(cfg, c.Raw, said, files, gap == diffWhole)
	switch {
	case aerr != nil && gap != diffWhole:
		if gap == diffUnlisted {
			w.Report.Warnf("commit %.7s: over the files GitHub listed, attribution would refuse it (%v) — but GitHub did not list the commit's whole diff, so that is not a verdict: the commit is attributed to no line", c.Raw.SHA, aerr)
		}
		return FilePlacement{gap: gap, withheld: aerr}, nil
	case aerr != nil:
		return FilePlacement{gap: gap, Refused: aerr}, nil
	}
	return FilePlacement{Moved: moved, gap: gap}, nil
}

// walkedFiles fetches the paths a walked commit's own diff touches from the
// cheapest source that has them: nothing for a merge commit (its diff is
// never asked for), local git for a landed identity, the API for a commit
// this checkout may not hold — a squash-merged pull's inner commit, or one
// of the pull preview reads (DESIGN §4.1, "Where the files come from"). A
// listing GitHub did not give whole — capped, or cut short by a 422
// (listFiles) — is recorded on the facts with the escapes of the lines
// reached and returned as diffUnlisted: the files it did not list are
// unreachable, not absent. A shallow clone's boundary comes back with no
// file and diffBoundary — git reads it as a root, and its whole tree is not
// its diff (t-esm5) — recorded on no fact here, since Facts.Shallow already
// carries the checkout's remedy, and warned about by the caller once placed.
func (w Walker) walkedFiles(ctx context.Context, owner, repo string, c Commit, facts *Facts, reached []Line) (files []string, gap diffGap, err error) {
	if c.Raw.Parents >= 2 {
		return nil, diffWhole, nil
	}
	if c.Landed {
		files, err = w.Git.DiffTreeFiles(ctx, c.Raw.SHA)
		if gitsource.IsShallowBoundary(err) {
			return nil, diffBoundary, nil
		}
		return files, diffWhole, err
	}
	l, err := w.listFiles(ctx, owner, repo, c.Raw.SHA)
	if err != nil {
		return nil, diffWhole, err
	}
	unread := UnreadListing{SHA: fmt.Sprintf("%.7s", c.Raw.SHA), Pull: c.Pull, Escapes: lineEscapes(c, reached)}
	switch {
	case l.Unknown:
		facts.FilesUnknown = append(facts.FilesUnknown, unread)
		w.Report.Warnf("commit %.7s in pull request #%d: GitHub answered 422 for its file listing, so a line it touches in files GitHub did not list may be missing from this verdict", c.Raw.SHA, c.Pull)
	case l.Capped:
		facts.FilesCapped = append(facts.FilesCapped, unread)
		w.Report.Warnf("commit %.7s in pull request #%d touches at least %d files, and GitHub lists no more than that — the files past the cap could not be read, so a package they touch is missing from this verdict", c.Raw.SHA, c.Pull, github.CommitFilesCap)
	}
	if l.incomplete() {
		return l.Files, diffUnlisted, nil
	}
	return l.Files, diffWhole, nil
}

// diffGap is how much of a commit's own diff the walk read — what decides
// whether a refusal over its files is a verdict (PlaceByFiles).
type diffGap int

const (
	// diffWhole: every file the diff touches (none, for a merge commit, whose
	// diff is never asked for).
	diffWhole diffGap = iota
	// diffUnlisted: GitHub's listing stopped short — capped, or cut by a 422.
	diffUnlisted
	// diffBoundary: nothing — a shallow clone's boundary commit.
	diffBoundary
)

// warnBoundary is the one warning a shallow clone's boundary commit gets,
// worded by where rules 2–3 over no file left it: on the lines its scope
// names (carried), refused and so nowhere (withheld), or on no line. Each
// says that no file was read, because the line a scope names may be the wrong
// one — the full clone refuses a scope its files contradict.
func (w Walker) warnBoundary(sha string, carried []Line, withheld error) {
	const head = "commit %.7s is this shallow clone's boundary: its parents are not here, so its own diff cannot be read — "
	const remedy = " (fetch the full history: fetch-depth: 0)"
	switch {
	case withheld != nil:
		w.Report.Warnf(head+"over no file, attribution would refuse it (%v), which is not a verdict: the commit is carried nowhere, and a package it touched is missing from this verdict"+remedy, sha, withheld)
	case len(carried) > 0:
		labels := make([]string, 0, len(carried))
		for _, l := range carried {
			labels = append(labels, l.TagLine.Label())
		}
		w.Report.Warnf(head+"it is carried on %s by its scope alone, with no file read to confirm that is the line it moves"+remedy, sha, strings.Join(labels, ", "))
	default:
		w.Report.Warnf(head+"it is carried on no line, and a package it touched is missing from this verdict"+remedy, sha)
	}
}

// fileListing is GitHub's answer for one commit's files, classified: the
// paths it listed, Capped when the listing stopped at github.CommitFilesCap,
// Unknown when GitHub answered 422 — Files then holds what the pages before
// the 422 listed, none when the first page answered it.
type fileListing struct {
	Files   []string
	Capped  bool
	Unknown bool
}

// incomplete reports that the listing is not the commit's whole diff — the
// listing attribution must not refuse over.
func (l fileListing) incomplete() bool { return l.Capped || l.Unknown }

// listFiles asks GitHub for the files of a commit no branch holds and
// classifies the answer — the one place CommitFiles is read, so the walk and
// preview's pull side cannot take one listing two ways. A 422
// (github.IsCommitUnknown) is a capped listing holding what the pages before
// it listed: the sha came from GitHub's own pull listing and the files are
// the one input attribution has no weaker source for, so the listing is
// unread, never §4's lag fallback (DESIGN §4.1; t-esm5; mutation rows
// unlisted-commit-files-die-as-a-raw-api-error,
// later-page-422-discards-the-listed-files).
func (w Walker) listFiles(ctx context.Context, owner, repo, sha string) (fileListing, error) {
	files, capped, err := w.Pulls.CommitFiles(ctx, owner, repo, sha)
	switch {
	case github.IsCommitUnknown(err):
		return fileListing{Files: files, Unknown: true}, nil
	case err != nil:
		return fileListing{}, err
	}
	return fileListing{Files: files, Capped: capped}, nil
}

// reachedLines names the lines whose range holds a commit — the lines a
// refused commit wedges.
func reachedLines(lines []Line, idx []int) []Line {
	out := make([]Line, 0, len(idx))
	for _, i := range idx {
		out = append(out, lines[i])
	}
	return out
}

// lineTag is the one wording of a line's escape: a tag on the line at or past
// the base a refused commit is governed by, or at or past all of several.
// lineEscapes, attributionWedge and the walk's own refusal all word it here,
// so the escape reads the same wherever the walk hands it down.
func lineTag(l Line, bases []string) string {
	short := make([]string, len(bases))
	for i, b := range bases {
		short[i] = fmt.Sprintf("%.7s", b)
	}
	if len(short) == 1 {
		return fmt.Sprintf("a %s tag at or past %s", l.TagLine.Label(), short[0])
	}
	return fmt.Sprintf("a %s tag at or past all of %s", l.TagLine.Label(), strings.Join(short, ", "))
}

// lineBases is, per line by index, the bases a tag on that line must sit at
// or past to clear every refused commit its range holds — each once, in walk
// order.
type lineBases [][]string

func (b lineBases) add(base string, reach []int) {
	for _, i := range reach {
		if !slices.Contains(b[i], base) {
			b[i] = append(b[i], base)
		}
	}
}

// lineTags names the escape of every line a refusal wedges, in declared
// order: one tag per line, and only a tag on every one of them clears the
// range — a bare tag is no line's (t-wjbs, measured 2026-10-26).
func lineTags(lines []Line, held lineBases) string {
	var tags []string
	for i, bases := range held {
		if len(bases) > 0 {
			tags = append(tags, lineTag(lines[i], bases))
		}
	}
	return strings.Join(tags, " / ")
}

// lineEscapes names, per line whose range holds c, the tag that takes c out
// of that line's walk: a tag on the line at or past c's governing identity —
// the unread-listing shortfall's per-commit escapes.
func lineEscapes(c Commit, reached []Line) []string {
	escapes := make([]string, 0, len(reached))
	for _, l := range reached {
		escapes = append(escapes, lineTag(l, []string{c.Governing()}))
	}
	return escapes
}

// attributionWedge is the walk's refusal for what attribution refused
// (DESIGN §4.1, rule 3 and the contradiction): lint class, like every
// message the walk refuses, worded as the walk's own refusal of a message no
// pattern claims — the first refused commit's sentence and where it sits,
// the count of the rest, every refused commit in details — and the escape
// PER LINE: a refused commit wedges every line whose range holds it, and
// each of those walks starts past it only when that line gains a tag at or
// past the commit's governing identity.
func attributionWedge(refused []placementRefusal, owner, repo string, lines []Line) error {
	first := refused[0].c
	where := fmt.Sprintf("a commit on the released branch (%.7s)", first.Raw.SHA)
	if first.Pull > 0 {
		where = fmt.Sprintf("inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s", owner, repo, first.Pull, first.MergePoint)
	}
	more := ""
	if n := len(refused) - 1; n > 0 {
		more = fmt.Sprintf(" (+%d more refused commit(s), all in details)", n)
	}
	held := make(lineBases, len(lines))
	rows := make([]bump.Refusal, 0, len(refused))
	for _, r := range refused {
		held.add(r.c.Governing(), r.reach)
		rows = append(rows, r.row())
	}
	return core.Lintf(
		"commit %.7s: %v%s — %s; the commit is already on a published branch and cannot be rewritten, so every release of a line whose range holds it wedges here until that line's walk starts past it: cut %s by hand, or name such a tag with --since-tag=<prefix>vX.Y.Z (DESIGN §4.1)",
		first.Raw.SHA, refused[0].err, more, where, lineTags(lines, held)).WithDetails(rows)
}

// rangeRefusal is attribution's refusal over a --range fold, whose commits
// the caller may still rewrite: the sentence attribution wrote, which names
// the escapes the commit can take, in the shape of bump.Fold's refusal of
// the same range — never the walk's published branch or a tag to cut (DESIGN
// §4: the authoring remedy is kept for --range; t-wjbs, measured 2026-10-26:
// an unpushed commit was told to cut a tag on every line). Keep the two
// shapes in step: a --range caller reads one refusal from either.
func rangeRefusal(refused []placementRefusal) error {
	first := refused[0]
	rows := make([]bump.Refusal, 0, len(refused))
	for _, r := range refused {
		rows = append(rows, r.row())
	}
	msg := fmt.Sprintf("commit %.7s: %v; refusing to version the range", first.c.Raw.SHA, first.err)
	if n := len(refused) - 1; n > 0 {
		msg = fmt.Sprintf("commit %.7s: %v (+%d more violation(s), all in details); refusing to version the range", first.c.Raw.SHA, first.err, n)
	}
	return core.Lintf("%s", msg).WithDetails(rows)
}
