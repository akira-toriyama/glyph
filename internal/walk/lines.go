package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// Line is one version line the walk answers for. Package is the [[packages]]
// entry that owns it — the zero value (Path "") only on the bare line of a
// repository that declares none. TagLine is its tag line — the prefix and the
// majors it holds (config.Config.LineOf). Base is what the bump steps from:
// the walk base's version when the walk base is a version tag, v0.0.0 on the
// whole-history arm, and nil — the line's highest tag is then read (cli's
// currentVersion) — for a typed tag that names no plain version, for a
// --range fold and for a pull's listing. Source names the line's own range in
// messages; Range is the revision range whose commits are unreleased on this
// line, "" when every walked commit is (the whole history, a --range fold, a
// pull's listing). Bound is the below: bound as typed when that form resolved
// the line, "" for the other forms: with Range "" it tells the two
// whole-history causes apart, and a line's diagnosis must not be a sentence
// `git tag -l` refutes (t-gt9n).
//
// BaseTag is the tag the line's range starts from, in its own spelling — the
// compare link's left side — and "" when the walk has no tag base: the whole
// history, a --range fold, a pull's listing, or a typed value that is no tag
// (tagBase). A field rather than cut out of Range, because Range cannot say
// it: the single line's whole-history Range is "HEAD", a packages line's is
// "", a typed branch looks like any tag, and Base is the parsed version,
// whose String() re-adds the v a bare `1.0.0` tag does not have.
type Line struct {
	Package config.Package
	TagLine config.Line
	Base    *bump.Version
	BaseTag string
	Source  string
	Range   string
	Bound   string
}

// LineWalk is one line's slice of the walk: the commits that JOIN it — placed
// on it and unreleased on it (DESIGN §4.1) — in walk order. That is the
// line's fold input and its notes' input, not its participating commits: an
// exclude_authors commit joins the lines its files touch and the fold drops
// it, and a message no pattern claims joins every line it is unreleased on so
// the fold refuses it there.
type LineWalk struct {
	Line
	Commits []Commit
}

// resolveLinesScoped turns the --since-tag value into the lines the walk answers
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
// tag — a candidate is a question, never an answer (t-gt9n; mutation row
// packages-candidate-tag-walks-every-line).
//
// The union is the range from the bases' common ancestor to HEAD, which
// contains every line's range; a line with no tag of its own makes it the
// whole history, under the single line's cap and with the remedy §4.1 names.
// A nil scope resolves every declared line; a scope narrows the UNION to its
// lines. Every declared line is still resolved and returned, because
// partitionLines attributes over the whole set; only the range the walk runs
// over is decided by the subset.
func (w Walker) resolveLinesScoped(ctx context.Context, cfg *config.Config, tagFlag string, scope *Scope) ([]Line, string, error) {
	if len(cfg.Packages) == 0 {
		revRange, baseTag, base, err := w.sinceTagRange(ctx, cfg, tagFlag)
		if err != nil {
			return nil, "", err
		}
		return []Line{{Base: base, BaseTag: baseTag, Source: revRange, Range: revRange}}, revRange, nil
	}
	tag := strings.TrimSpace(tagFlag)
	var lines []Line
	switch {
	case tag == Auto:
		for _, p := range cfg.Packages {
			l, err := w.lineFromLatest(ctx, cfg, p, nil)
			if err != nil {
				return nil, "", err
			}
			lines = append(lines, l)
		}
	case strings.HasPrefix(tag, below):
		rest, prefix, bound, perr := belowBound(strings.TrimPrefix(tag, below))
		if perr != nil {
			return nil, "", perr
		}
		p, ok := packageOnLine(cfg, prefix, bound.Major)
		if !ok {
			return nil, "", core.Usagef("--since-tag=below:%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", rest, config.Line{Prefix: prefix}.Label(), declaredLines(cfg))
		}
		l, err := w.lineFromLatest(ctx, cfg, p, &bound)
		if err != nil {
			return nil, "", err
		}
		l.Bound = rest
		lines = []Line{l}
	default:
		baseTag, terr := w.tagBase(ctx, tag)
		if terr != nil {
			return nil, "", terr
		}
		prefix := bump.LinePrefix(tag)
		if shape, perr := bump.ParseBaseVersionOn(prefix, tag); perr == nil {
			p, ok := packageOnLine(cfg, prefix, shape.Major)
			if !ok {
				return nil, "", core.Usagef("--since-tag=%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", tag, config.Line{Prefix: prefix}.Label(), declaredLines(cfg))
			}
			l := Line{Package: p, TagLine: cfg.LineOf(p), BaseTag: baseTag, Source: tag + "..HEAD", Range: tag + "..HEAD"}
			if v, verr := bump.ParseVersionOn(prefix, tag); verr == nil {
				// A plain version is the step base too — the walk base and the
				// step base are ONE tag (sinceTagRange). A candidate is not, and
				// the line's highest plain tag answers (currentVersion).
				l.Base = &v
			}
			lines = []Line{l}
			break
		}
		for _, p := range cfg.Packages {
			lines = append(lines, Line{Package: p, TagLine: cfg.LineOf(p), BaseTag: baseTag, Source: tag + "..HEAD", Range: tag + "..HEAD"})
		}
	}
	union, err := w.unionRange(ctx, cfg, scopedLines(lines, scope), scopeEscape(scope))
	if err != nil {
		return nil, "", err
	}
	return lines, union, nil
}

// scopedLines keeps the lines the scope asked about, in the resolved order. A
// nil scope, an empty Only, or a subset that matches nothing keeps every line:
// the union must never be computed from NO line, which would silently widen
// to the merge base of an empty set.
func scopedLines(lines []Line, scope *Scope) []Line {
	if scope == nil || len(scope.Only) == 0 {
		return lines
	}
	want := make(map[string]bool, len(scope.Only))
	for _, p := range scope.Only {
		want[p.Path] = true
	}
	var kept []Line
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

func scopeEscape(scope *Scope) string {
	if scope == nil {
		return ""
	}
	return scope.Escape
}

// lineFromLatest resolves one package's line from its own highest tag HEAD
// contains — strictly below the bound when one is given — else the whole
// history.
func (w Walker) lineFromLatest(ctx context.Context, cfg *config.Config, p config.Package, below *bump.Version) (Line, error) {
	tl := cfg.LineOf(p)
	latest, v, err := w.LatestVersionTag(ctx, tl, below)
	if err != nil {
		return Line{}, err
	}
	if latest == "" {
		return Line{Package: p, TagLine: tl, Base: &bump.Version{}, Source: "HEAD"}, nil
	}
	return Line{Package: p, TagLine: tl, Base: &v, BaseTag: latest, Source: latest + "..HEAD", Range: latest + "..HEAD"}, nil
}

// unionRange is the one range the walk runs over: a single line's own range;
// the common ancestor of several lines' bases to HEAD; the whole history —
// capped and warned exactly as the single line's — as soon as any line has
// no tag to start from. The diagnosis tells a line with no tag at all from
// one with none under its below: bound: the remedy is the same tag, the
// sentence is not, and past the cap it is the refusal body an operator reads
// beside `git tag -l` (t-gt9n).
func (w Walker) unionRange(ctx context.Context, cfg *config.Config, lines []Line, escape string) (string, error) {
	var bases, untagged, unbounded, bounded []string
	for _, l := range lines {
		if l.Range == "" {
			untagged = append(untagged, firstTagOn(l.TagLine))
			if l.Bound != "" {
				bounded = append(bounded, fmt.Sprintf("no version tag below %s on the %s line in HEAD's history", l.Bound, l.TagLine.Label()))
			} else {
				unbounded = append(unbounded, l.TagLine.Label())
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
		revRange, _, err := w.wholeHistory(ctx, cfg, fmt.Sprintf("%s — cut %s at the commit before that line's first change to say nothing of it was released before there", strings.Join(causes, "; "), strings.Join(untagged, " / ")), escape)
		return revRange, err
	}
	mb, err := w.Git.MergeBase(ctx, bases)
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

// PendingLines are the lines a source that names no release base answers
// for — every declared line, or the bare line of a repository that declares
// none — with every commit pending on each: a --range fold names no base
// (the caller chose the range) and a pull's listing is unreleased by
// definition. Source names the input in messages; Range is "" (every commit
// is unreleased), Base nil (the line's highest tag answers) and BaseTag ""
// (no compare link: a local range names no repository and a pull no release
// base — DESIGN §4, the citation each source can attest).
func PendingLines(cfg *config.Config, source string) []Line {
	if len(cfg.Packages) == 0 {
		return []Line{{Source: source}}
	}
	lines := make([]Line, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		lines = append(lines, Line{Package: p, TagLine: cfg.LineOf(p), Source: source})
	}
	return lines
}
