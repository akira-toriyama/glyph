package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/attribution"
	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/github"
	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
)

// This file is the packages layer over the walk (DESIGN §4.1): which version
// lines a --since-tag names, the ONE walk over the union of their ranges, and
// the partition that says which line each participating commit moves. A
// repository that declares no [[packages]] passes through it untouched — one
// line, no files fetched, no git asked — so every single-line verdict stays
// byte-identical (mutation row packages-absent-changes-the-single-line).
//
// Layer contract: nothing here judges a message (config.Match does), folds
// (bump.FoldSigils does) or decides which package a diff belongs to
// (attribution.Attribute does). It resolves lines, fetches the files the
// attribution needs from the cheapest source that has them, and intersects
// two answers — which lines a commit is UNRELEASED on and which lines its
// diff MOVES — into the per-line commit lists the folds read.

// line is one version line the walk answers for. Package is the [[packages]]
// entry that owns it — the zero value (Path "") only on the bare line of a
// repository that declares none. Line is its tag line — the prefix and the
// majors it holds (config.Config.LineOf). Base is what the bump steps from
// when the walk base names a version, nil when it does not (the line's
// highest tag is then read). Source names the line's own range in messages;
// Range is the revision range whose commits are unreleased on this line, ""
// when every walked commit is (the whole history, or a --range fold). Bound
// is the below: bound as typed when that form resolved the line, "" for the
// other forms: with Range "" it tells the two whole-history causes apart —
// no tag UNDER the bound is not no tag at all, and a line's diagnosis must
// not be a sentence `git tag -l` refutes (t-gt9n) — which is also why each
// cause is stated of HEAD's history, the set the base is read from.
type line struct {
	Package config.Package
	Line    config.Line
	Base    *bump.Version
	Source  string
	Range   string
	Bound   string
}

// lineWalk is one line's slice of the walk: the commits that participate in
// it, in walk order.
type lineWalk struct {
	line
	Commits []walked
}

// sinceTagWalk is what sinceTagInput hands back: every commit that
// participates on at least one line (All, walk order — the single line's fold
// input), the walk's own facts, the revision range walked, the single line's
// step base (nil when packages are declared: each line carries its own), and
// the per-line partition — exactly one entry, holding All, when no packages
// are declared.
type sinceTagWalk struct {
	All    []walked
	Facts  walkFacts
	Source string
	Base   *bump.Version
	Lines  []lineWalk
}

// resolveLines turns the --since-tag value into the lines the walk answers
// for and the ONE revision range the walk runs over.
//
// With no packages declared the single line resolves exactly as it always
// has (sinceTagRange). With packages, a tag names a line (DESIGN §4.1): a
// bare --since-tag walks every declared line from its own highest tag;
// --since-tag=below:<prefix>vX.Y.Z and --since-tag=<prefix>vX.Y.Z select
// that line alone, and a prefix no [[packages]] entry declares is usage —
// asking about a line that does not exist must not silently walk another. A
// tag that is not version-shaped on any line names no line: every line
// walks from it and steps from its own highest tag, as the single line does.
//
// Version-SHAPED is bump.ParseBaseVersionOn's question, the one below: has
// always asked of its bound: a release candidate or a build-metadata tag on
// a declared line names that line, and the line steps from its highest PLAIN
// tag — a candidate is a question, never an answer — as the single line does
// with a tag that names no base. The first cut asked ParseVersionOn's
// question of the plain form, so haiku/v3.0.0-rc.1 was "not a version on any
// line" and every declared line walked from haiku's candidate, stepping past
// its own highest tag with nothing on stderr; and fish/v1.0.0-rc.1 died in
// git at 4 instead of at the usage guard (t-gt9n; mutation row
// packages-candidate-tag-walks-every-line).
//
// The union is the range from the bases' common ancestor to HEAD, which
// contains every line's range; a line with no tag of its own makes it the
// whole history, under the same cap the single line has, with the remedy
// §4.1 names (cut <path>/v0.0.0 at the commit before the line's first
// change).
// A nil scope resolves every declared line; a scope narrows the UNION to its
// lines. Every declared line is still resolved and returned, because
// partitionLines attributes over the whole set; only the range the walk runs
// over is decided by the subset.
func resolveLinesScoped(ctx context.Context, cfg *config.Config, tagFlag string, scope *walkScope) ([]line, string, error) {
	if len(cfg.Packages) == 0 {
		revRange, base, err := sinceTagRange(ctx, cfg, tagFlag)
		if err != nil {
			return nil, "", err
		}
		return []line{{Base: base, Source: revRange, Range: revRange}}, revRange, nil
	}
	tag := strings.TrimSpace(tagFlag)
	var lines []line
	switch {
	case tag == sinceTagAuto:
		for _, p := range cfg.Packages {
			l, err := lineFromLatest(ctx, cfg, p, nil)
			if err != nil {
				return nil, "", err
			}
			lines = append(lines, l)
		}
	case strings.HasPrefix(tag, sinceTagBelow):
		rest := strings.TrimSpace(strings.TrimPrefix(tag, sinceTagBelow))
		prefix, _ := bump.SplitTag(rest)
		// checkSinceTagFlag guaranteed the bound parses on its line.
		bound, perr := bump.ParseBaseVersionOn(prefix, rest)
		if perr != nil {
			return nil, "", core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
		}
		p, ok := packageOnLine(cfg, prefix, bound.Major)
		if !ok {
			return nil, "", core.Usagef("--since-tag=below:%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", rest, config.Line{Prefix: prefix}.Label(), declaredLines(cfg))
		}
		l, err := lineFromLatest(ctx, cfg, p, &bound)
		if err != nil {
			return nil, "", err
		}
		l.Bound = rest
		lines = []line{l}
	default:
		prefix, _ := bump.SplitTag(tag)
		if shape, perr := bump.ParseBaseVersionOn(prefix, tag); perr == nil {
			p, ok := packageOnLine(cfg, prefix, shape.Major)
			if !ok {
				return nil, "", core.Usagef("--since-tag=%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", tag, config.Line{Prefix: prefix}.Label(), declaredLines(cfg))
			}
			l := line{Package: p, Line: cfg.LineOf(p), Source: tag + "..HEAD", Range: tag + "..HEAD"}
			if v, verr := bump.ParseVersionOn(prefix, tag); verr == nil {
				// A plain version is the step base too — the walk base and the
				// step base are ONE tag (sinceTagRange). A candidate is not, and
				// the line's highest plain tag answers (currentVersion).
				l.Base = &v
			}
			lines = []line{l}
			break
		}
		for _, p := range cfg.Packages {
			lines = append(lines, line{Package: p, Line: cfg.LineOf(p), Source: tag + "..HEAD", Range: tag + "..HEAD"})
		}
	}
	union, err := unionRange(ctx, cfg, scopedLines(lines, scope), scopeEscape(scope))
	if err != nil {
		return nil, "", err
	}
	return lines, union, nil
}

// scopedLines keeps the lines the scope asked about, in the resolved order. A
// nil scope, an empty Only, or a subset that matches nothing keeps every line:
// the union must never be computed from NO line, which would silently widen
// to the merge base of an empty set.
func scopedLines(lines []line, scope *walkScope) []line {
	if scope == nil || len(scope.Only) == 0 {
		return lines
	}
	want := make(map[string]bool, len(scope.Only))
	for _, p := range scope.Only {
		want[p.Path] = true
	}
	var kept []line
	for _, l := range lines {
		if want[l.Package.Path] {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return lines
	}
	return kept
}

func scopeEscape(scope *walkScope) string {
	if scope == nil {
		return ""
	}
	return scope.Escape
}

// lineFromLatest resolves one package's line from its own highest tag HEAD
// contains — strictly below the bound when one is given — else the whole
// history.
func lineFromLatest(ctx context.Context, cfg *config.Config, p config.Package, below *bump.Version) (line, error) {
	tl := cfg.LineOf(p)
	latest, v, err := latestVersionTag(ctx, tl, below)
	if err != nil {
		return line{}, err
	}
	if latest == "" {
		return line{Package: p, Line: tl, Base: &bump.Version{}, Source: "HEAD"}, nil
	}
	return line{Package: p, Line: tl, Base: &v, Source: latest + "..HEAD", Range: latest + "..HEAD"}, nil
}

// unionRange is the one range the walk runs over: a single line's own range;
// the common ancestor of several lines' bases to HEAD; the whole history —
// capped and warned exactly as the single line's — as soon as any line has
// no tag to start from. The diagnosis tells a line with no tag at all from
// one with none under its below: bound: the remedy is the same tag, the
// sentence is not, and past the cap it is the refusal body an operator reads
// beside `git tag -l` (t-gt9n).
func unionRange(ctx context.Context, cfg *config.Config, lines []line, escape string) (string, error) {
	var bases, untagged, unbounded, bounded []string
	for _, l := range lines {
		if l.Range == "" {
			untagged = append(untagged, firstTagOn(l.Line))
			if l.Bound != "" {
				bounded = append(bounded, fmt.Sprintf("no version tag below %s on the %s line in HEAD's history", l.Bound, l.Line.Label()))
			} else {
				unbounded = append(unbounded, l.Line.Label())
			}
			continue
		}
		base := strings.TrimSuffix(l.Range, "..HEAD")
		if !slices.Contains(bases, base) {
			bases = append(bases, base)
		}
	}
	if len(untagged) > 0 {
		var causes []string
		if len(unbounded) > 0 {
			causes = append(causes, fmt.Sprintf("no version tag on the %s line(s) in HEAD's history", strings.Join(unbounded, ", ")))
		}
		causes = append(causes, bounded...)
		revRange, _, err := wholeHistory(ctx, cfg, fmt.Sprintf("%s — cut %s at the commit before that line's first change to say nothing of it was released before there", strings.Join(causes, "; "), strings.Join(untagged, " / ")), escape)
		return revRange, err
	}
	mb, err := gitsource.MergeBase(ctx, ".", bases)
	if err != nil {
		return "", err
	}
	return mb + "..HEAD", nil
}

// firstTagOn is the tag that says "nothing of this line was released before
// here": the null version on a free line, and on a locked line — which has
// no version below its major — the major's own floor.
func firstTagOn(l config.Line) string {
	return bump.Version{Major: l.Major}.TagOn(l.Prefix)
}

// packageOnLine finds the declared package whose line holds a version of
// major on prefix — "" selects the bare line (the root package, or a
// root-level vN). Two packages can share a prefix (pubsub and pubsub/v2);
// the major is what tells their lines apart.
func packageOnLine(cfg *config.Config, prefix string, major int) (config.Package, bool) {
	for _, p := range cfg.Packages {
		if l := cfg.LineOf(p); l.Prefix == prefix && l.Holds(major) {
			return p, true
		}
	}
	return config.Package{}, false
}

// declaredLines lists every declared line for a message, the root package
// as bare v*.
func declaredLines(cfg *config.Config) string {
	out := make([]string, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		out = append(out, cfg.LineOf(p).Label())
	}
	return strings.Join(out, ", ")
}

// governing is the on-branch commit the walk range judges a walked commit by:
// its own sha when it landed on the released branch, else the merge point of
// the pull it was expanded from (DESIGN §4.1: a listed commit participates in
// a line when its governing commit is unreleased there).
func (w walked) governing() string {
	if w.Landed || w.MergePoint == "" {
		return w.Raw.SHA
	}
	return w.MergePoint
}

// placement is how partitionLines and previewLines place one commit on the
// declared lines.
type placement int

const (
	// placedByFiles: the commit's own diff decides — with its scope and sigil
	// when the fold reads the message, by files alone when it does not.
	placedByFiles placement = iota
	// placedEverywhere: a message no pattern claims joins every line it is
	// unreleased on, so the fold refuses it there (§3); it is not attributed.
	placedEverywhere
	// placedNowhere: a skip-pattern commit is in no fold and no section, so
	// no line holds it and its files are never asked for.
	placedNowhere
)

// reading is what attribution may read of one commit's message: the scope
// and sigil the claiming pattern handed on, and which pattern that was —
// which decides no placement and only words a refusal (attribute). unjudged
// is the reading of a message it may read nothing of: no scope, the none
// sigil, no pattern, so files alone place the commit and nothing refuses it.
type reading struct {
	scope   string
	sigil   config.Sigil
	pattern int // position in cfg.Patterns; -1 when the message is not read
}

var unjudged = reading{sigil: config.SigilNone, pattern: -1}

func readingOf(m config.Match) reading {
	return reading{scope: m.Groups[config.ScopeGroup], sigil: m.Sigil, pattern: m.PatternIndex}
}

// placeOf reads what attribution may read of a commit's message: its scope
// and sigil when the fold reads the message, nothing when it does not. An
// exclude_authors commit is placed by its files alone — the message is
// exactly what glyph declared it would not judge, so neither its scope nor
// its sigil may carry it or refuse it (attribution.Attribute with no scope
// and the none sigil: files decide, and under no package it is placed
// nowhere, the shape rule 3 gives a shared-only `=`).
func placeOf(cfg *config.Config, raw gitsource.RawCommit) (placement, reading) {
	if slices.Contains(cfg.ExcludeAuthors, raw.Author) {
		return placedByFiles, unjudged
	}
	m, err := cfg.Match(raw.Message)
	if err != nil || !m.Matched {
		return placedEverywhere, unjudged
	}
	if m.Skip {
		return placedNowhere, unjudged
	}
	return placedByFiles, readingOf(m)
}

// attribute is the one door the walk, lint --range and preview ask
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
// returned.
//
// One helper, because the alternative is three call sites each setting three
// fields by hand: a site that forgets one moves no verdict, so every verdict
// test stays green while its refusal goes back to the first cut's words —
// which told a raw revert's author to write a scope and a = its pattern never
// captures (t-mfny (A); DESIGN §4.1). Each arm has a test that reads its
// refusal's own words, and a mutation row that hands this door no commit
// (<arm>-refuses-a-merge-commit-as-touching-no-file).
func attribute(cfg *config.Config, raw gitsource.RawCommit, said reading, files []string, whole bool) ([]config.Package, error) {
	moved, err := attribution.Attribute(files, said.scope, said.sigil, cfg.Packages)
	var r *attribution.Refusal
	if errors.As(err, &r) {
		r.Merge = raw.Parents >= 2
		r.Unread = !whole
		if say, ok := cfg.Sayable(said.pattern, raw.Message); ok {
			r.Pattern = &say
		}
	}
	return moved, err
}

// partitionLines splits the walk's commits over the lines. With no packages
// declared it is the identity: one line holding every commit, nothing asked
// of git or the API.
//
// With packages, a commit participates in line p when it is UNRELEASED on p
// (its governing commit is in p's range) AND its own diff PLACES it on p
// (attribution). The first question is git's, answered per line from the
// line's own range; the second is asked of every commit's files (placeOf):
// with its scope and sigil when the fold reads the message, by files alone
// when it does not — an exclude_authors commit moves no version and appears
// in the notes of the lines its files touch, on no line when they touch
// none, because its message is exactly what glyph declared it would not
// judge. A skip-pattern commit appears nowhere, so it is placed nowhere and
// its files are never asked for; a message no pattern claims joins every
// line it is unreleased on, so the fold refuses it there (§3). Files come
// from local git for a landed identity and from the API for a squash-merged
// pull's inner commit, the one shape no branch holds; a merge commit's diff
// is never asked for.
//
// The first cut placed every commit the fold would not read on every line:
// a dependabot bump touching only haiku/poem.go rendered under all five line
// headings of the live-fire harness, lines with no commit of their own grew
// a section for it, and release wrote it into every draft — while preview
// dropped the same commit from every line (t-sr1c, measured 2026-09-11;
// mutation row packages-excluded-author-placed-on-every-line).
//
// A commit inside the union walk that is released on every line — possible
// on a history where the bases' common ancestor sits before both — is
// dropped with a notice; it was walked because the union had to contain it,
// and it belongs to no line's verdict.
//
// A refusal attribution hands down over a diff the walk did not read whole
// (diffGap) — a listing GitHub TRUNCATED at its cap or cut short with a 422
// (listFiles), or a shallow clone's boundary, whose diff local git cannot
// compute at all — is not a finding and never wedges: "no carrier" and "the
// scope names a package owning none of the files" are both claims about files
// the walk could not read (the package past the cap may be exactly the one
// named). The commit is carried nowhere and the walk's own shortfall answers
// — FilesCapped, FilesUnknown or Shallow, on which a writing command refuses
// at 4 and a reporting one warns — never the gate code, which would tell an
// operator to cut a tag past a commit whose true attribution was hidden
// (t-c6r5, measured: exit 3 with the wedge remedy). A non-refusal answer
// stands over such a diff, so a scope naming a package still carries the
// commit there (rule 2), and so do the files a listing did hold, whatever cut
// it short (t-esm5). One arm withholds for all three so they cannot come to
// place a commit two ways again: a boundary was once carried on no line
// whatever its scope, beside a 422 the same walk placed by scope (DESIGN
// §4.1). The shortfall each records stays its own — a boundary is no
// unreadListing, whose remedy is a tag past the commit; a boundary's is the
// full history.
func partitionLines(ctx context.Context, gh *github.Client, cfg *config.Config, owner, repo string, commits []walked, facts *walkFacts, lines []line) ([]lineWalk, []walked, error) {
	if len(cfg.Packages) == 0 {
		return []lineWalk{{line: lines[0], Commits: commits}}, commits, nil
	}
	unreleased := make([]map[string]bool, len(lines))
	for i, l := range lines {
		if l.Range == "" {
			continue
		}
		raws, err := gitsource.Log(ctx, ".", l.Range)
		if err != nil {
			return nil, nil, err
		}
		set := make(map[string]bool, len(raws))
		for _, r := range raws {
			set[r.SHA] = true
		}
		unreleased[i] = set
	}
	lws := make([]lineWalk, len(lines))
	for i := range lines {
		lws[i] = lineWalk{line: lines[i], Commits: []walked{}}
	}
	var all []walked
	for _, c := range commits {
		gov := c.governing()
		var reach []int
		for i := range lines {
			if unreleased[i] == nil || unreleased[i][gov] {
				reach = append(reach, i)
			}
		}
		if len(reach) == 0 {
			noticef("commit %.7s is inside the union walk but already released on every line — not counted", c.Raw.SHA)
			continue
		}
		all = append(all, c)
		var carriers []int
		switch place, said := placeOf(cfg, c.Raw); place {
		case placedEverywhere:
			carriers = reach
		case placedNowhere:
		case placedByFiles:
			files, gap, ferr := walkedFiles(ctx, gh, owner, repo, c, facts, reachedLines(lines, reach))
			if ferr != nil {
				return nil, nil, ferr
			}
			moved, aerr := attribute(cfg, c.Raw, said, files, gap == diffWhole)
			switch {
			case aerr != nil && gap != diffWhole:
				carriers = nil
			case aerr != nil:
				return nil, nil, attributionWedge(aerr, c, owner, repo, reachedLines(lines, reach))
			default:
				for _, i := range reach {
					if slices.ContainsFunc(moved, func(p config.Package) bool { return p.Path == lines[i].Package.Path }) {
						carriers = append(carriers, i)
					}
				}
			}
			switch {
			case gap == diffBoundary:
				warnBoundary(c.Raw.SHA, reachedLines(lines, carriers), aerr)
			case aerr != nil && gap == diffUnlisted:
				warnf("commit %.7s: over the files GitHub listed, attribution would refuse it (%v) — but GitHub did not list the commit's whole diff, so that is not a verdict: the commit is carried nowhere, and the walk is incomplete", c.Raw.SHA, aerr)
			}
		}
		for _, i := range carriers {
			lws[i].Commits = append(lws[i].Commits, c)
		}
	}
	return lws, all, nil
}

// walkedFiles fetches the paths a walked commit's own diff touches from the
// cheapest source that has them: nothing for a merge commit (attributed to
// nothing, its diff never asked for), local git for a landed identity, the
// API for a squash-merged pull's inner commit. An API listing GitHub did not
// give whole — one that reached CommitFilesCap, or one a 422 cut short
// (listFiles) — is recorded on the facts with the tags that take the
// commit out of the lines reached (the shortfall's remedy), and returned as
// diffUnlisted: the files it did not list are unreachable, not absent, and a
// package they touch would be missing from the verdict — an incomplete walk
// in §4's sense, and a listing the caller must not let attribution refuse
// over.
//
// A shallow clone's boundary commit comes back with no file and diffBoundary:
// git reads it as a root, and the diff it would give is the whole tree, which
// once moved every line the tree touched (t-esm5). It is recorded on no fact
// here and warned about by the caller, once it is placed: a since-tag walk
// over a shallow checkout already records walkFacts.Shallow, whose remedy —
// the full history — is the boundary's, where an unreadListing would offer a
// tag past it.
func walkedFiles(ctx context.Context, gh *github.Client, owner, repo string, c walked, facts *walkFacts, reached []line) (files []string, gap diffGap, err error) {
	if c.Raw.Parents >= 2 {
		return nil, diffWhole, nil
	}
	if c.Landed {
		files, err = gitsource.DiffTreeFiles(ctx, ".", c.Raw.SHA)
		if gitsource.IsShallowBoundary(err) {
			return nil, diffBoundary, nil
		}
		return files, diffWhole, err
	}
	l, err := listFiles(ctx, gh, owner, repo, c.Raw.SHA)
	if err != nil {
		return nil, diffWhole, err
	}
	unread := unreadListing{SHA: fmt.Sprintf("%.7s", c.Raw.SHA), Pull: c.Pull, Escapes: lineEscapes(c, reached)}
	switch {
	case l.Unknown:
		facts.FilesUnknown = append(facts.FilesUnknown, unread)
		warnf("commit %.7s in pull request #%d: GitHub answered 422 for its file listing, so a line it touches in files GitHub did not list may be missing from this verdict", c.Raw.SHA, c.Pull)
	case l.Capped:
		facts.FilesCapped = append(facts.FilesCapped, unread)
		warnf("commit %.7s in pull request #%d touches at least %d files, and GitHub lists no more than that — the files past the cap could not be read, so a package they touch is missing from this verdict", c.Raw.SHA, c.Pull, github.CommitFilesCap)
	}
	if l.incomplete() {
		return l.Files, diffUnlisted, nil
	}
	return l.Files, diffWhole, nil
}

// diffGap is how much of a commit's own diff the walk read — what decides
// whether a refusal over its files is a verdict (partitionLines).
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
func warnBoundary(sha string, carried []line, withheld error) {
	const head = "commit %.7s is this shallow clone's boundary: its parents are not here, so its own diff cannot be read — "
	const remedy = " (fetch the full history: fetch-depth: 0)"
	switch {
	case withheld != nil:
		warnf(head+"over no file, attribution would refuse it (%v), which is not a verdict: the commit is carried nowhere, and a package it touched is missing from this verdict"+remedy, sha, withheld)
	case len(carried) > 0:
		labels := make([]string, 0, len(carried))
		for _, l := range carried {
			labels = append(labels, l.Line.Label())
		}
		warnf(head+"it is carried on %s by its scope alone, with no file read to confirm that is the line it moves"+remedy, sha, strings.Join(labels, ", "))
	default:
		warnf(head+"it is carried on no line, and a package it touched is missing from this verdict"+remedy, sha)
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
// classifies the answer — the one place both callers, the walk
// (walkedFiles) and preview's PR side, read CommitFiles through, so the two
// cannot take the same listing two ways. A 422 (github.IsCommitUnknown) is
// a capped listing holding what the pages before it listed — nothing when
// the first page answered it (DESIGN §4.1): the sha came from GitHub's own
// pull listing, its message was read, and the files are the one input
// attribution has no weaker source for, so the listing is unread — never
// §4's lag fallback. Handed back raw, it died at exit 4 on release, bump,
// notes and preview alike (t-esm5; mutation row
// unlisted-commit-files-die-as-a-raw-api-error); emptied, a 422 on page 2
// threw away the files page 1 listed (mutation row
// later-page-422-discards-the-listed-files).
func listFiles(ctx context.Context, gh *github.Client, owner, repo, sha string) (fileListing, error) {
	files, capped, err := gh.CommitFiles(ctx, owner, repo, sha)
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
func reachedLines(lines []line, idx []int) []line {
	out := make([]line, 0, len(idx))
	for _, i := range idx {
		out = append(out, lines[i])
	}
	return out
}

// lineEscapes names, per line whose range holds c, the tag that takes c out
// of that line's walk: a tag on the line at or past c's governing identity.
// attributionWedge and the unread-listing shortfall share it, so the escape
// reads the same wherever the walk hands it down.
func lineEscapes(c walked, reached []line) []string {
	escapes := make([]string, 0, len(reached))
	for _, l := range reached {
		escapes = append(escapes, fmt.Sprintf("a %s tag at or past %.7s", l.Line.Label(), c.governing()))
	}
	return escapes
}

// attributionWedge is wedgeHint's sibling for the two refusals attribution
// hands down (DESIGN §4.1, rule 3 and the contradiction): lint class, like
// every message the walk refuses, and decorated with where the commit sits
// and the escape PER LINE — a refused commit wedges every line whose range
// holds it, and each of those walks starts past it only when that line
// gains a tag at or past the commit's governing identity.
func attributionWedge(err error, c walked, owner, repo string, reached []line) error {
	where := fmt.Sprintf("a commit on the released branch (%.7s)", c.Raw.SHA)
	if c.Pull > 0 {
		where = fmt.Sprintf("inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s", owner, repo, c.Pull, c.MergePoint)
	}
	return &core.Error{Code: core.CodeLint, Details: []rangeViolation{{SHA: c.Raw.SHA, Subject: bump.FirstLine(c.Raw.Message), Detail: err.Error()}}, Msg: fmt.Sprintf(
		"commit %.7s: %v — %s; the commit is already on a published branch and cannot be rewritten, so every release of a line whose range holds it wedges here until that line's walk starts past it: cut %s by hand, or name such a tag with --since-tag=<line>vX.Y.Z (DESIGN §4.1)",
		c.Raw.SHA, err, where, strings.Join(lineEscapes(c, reached), " / "))}
}

// rangeLines is the --range twin of sinceTagInput for a repository with
// packages: every commit in the local range is a landed identity, unreleased
// on every line (a --range fold names no release base — the caller chose the
// range), and attributed from local git. The walk facts are empty: nothing
// was resolved over the API.
func rangeLines(ctx context.Context, cfg *config.Config, revRange string) (sinceTagWalk, error) {
	raws, err := logRange(ctx, revRange)
	if err != nil {
		return sinceTagWalk{}, err
	}
	commits := make([]walked, 0, len(raws))
	for _, r := range raws {
		commits = append(commits, walked{Raw: r, Landed: true})
	}
	lines := make([]line, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		lines = append(lines, line{Package: p, Line: cfg.LineOf(p), Source: revRange})
	}
	facts := walkFacts{Pulls: []pullExpansion{}}
	lws, all, perr := partitionLines(ctx, nil, cfg, "", "", commits, &facts, lines)
	if perr != nil {
		return sinceTagWalk{}, perr
	}
	return sinceTagWalk{All: all, Facts: facts, Source: revRange, Lines: lws}, nil
}

// checkLineSelection refuses --current unless exactly one line is selected:
// --current names one version, and a walk over two lines has two verdicts
// for it to describe (DESIGN §4.1).
func checkLineSelection(current string, lines []lineWalk) error {
	if current == "" || len(lines) == 1 {
		return nil
	}
	names := make([]string, 0, len(lines))
	for _, l := range lines {
		names = append(names, l.Line.Label())
	}
	return core.Usagef("--current names one version, and this walk answers for %d lines (%s) — select one line with --since-tag=<line>vX.Y.Z (or below:), or drop --current", len(lines), strings.Join(names, ", "))
}

// refusePullSource is the packages-mode answer to --pr on bump and notes: a
// pull's listing carries messages and no files, so nothing can say which
// line each commit moves. preview owns that question (DESIGN §4.1); until it
// does, the sources that have files are the ones that answer.
func refusePullSource(cfg *config.Config) error {
	if len(cfg.Packages) == 0 {
		return nil
	}
	return core.Usagef("this repository declares [[packages]], and --pr reads a pull request's messages alone, so its commits cannot be attributed to a line — use --since-tag (the release walk) or --range (local git), which have the files")
}
