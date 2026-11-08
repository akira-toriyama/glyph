package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// Line is one version line the walk answers for. Package is the [[packages]]
// entry that owns it — the zero value (Path "") only on the bare line of a
// repository that declares none. TagLine is its tag line — the prefix and the
// majors it holds (config.Config.LineOf). Base is what the bump steps from:
// the walk base's version when the walk base is a version tag, v0.0.0 on the
// whole-history arm, and nil — the line's highest tag is then read (cli's
// currentVersion) — for a typed tag that names no plain version, for a
// --range fold and for a pull's listing. Source names the line's own range in
// messages; revRange is the revision range whose commits are unreleased on this
// line, "" when every walked commit is — under [[packages]] the whole history,
// and on every line a --range fold or a pull's listing (the single line's
// whole-history revRange is "HEAD", below). bound is the below: bound as typed
// when that form resolved the line, "" for the other forms: with revRange "" it
// tells the two whole-history causes apart, and a line's diagnosis must not be
// a sentence `git tag -l` refutes (t-gt9n).
//
// BaseTag is the tag the line's range starts from, in its own spelling — the
// compare link's left side — and "" when the walk has no tag base: the whole
// history, a --range fold, a pull's listing, or a typed value that is no tag
// (tagBase). A field rather than cut out of revRange, because revRange cannot say
// it: the single line's whole-history revRange is "HEAD", a packages line's is
// "", a typed branch looks like any tag, and Base is the parsed version,
// whose String() re-adds the v a bare `1.0.0` tag does not have.
type Line struct {
	Package  config.Package
	TagLine  config.Line
	Base     *bump.Version
	BaseTag  string
	Source   string
	revRange string
	bound    string
}

// LineWalk is one line's slice of the walk: the commits that JOIN it — placed
// on it and unreleased on it (DESIGN §4.1) — in walk order. That is the
// line's fold input and its notes' input, not its participating commits: an
// exclude_authors commit joins the lines its files touch and the fold drops
// it. A judged message no pattern claims joins no line: the walk refused it
// at its sighting, and partitionLines for the sources that are no walk.
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
// Version-shaped is bump.ParseBaseTag's question (DESIGN §4.1,
// "Version-shaped"; t-gt9n; mutation row
// packages-candidate-tag-walks-every-line): a candidate names its line and the
// line steps from its highest plain tag.
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
		return []Line{{Base: base, BaseTag: baseTag, Source: revRange, revRange: revRange}}, revRange, nil
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
			return nil, "", undeclaredLine(below+rest, prefix, cfg)
		}
		l, err := w.lineFromLatest(ctx, cfg, p, &bound)
		if err != nil {
			return nil, "", err
		}
		l.bound = rest
		lines = []Line{l}
	default:
		baseTag, terr := w.tagBase(ctx, tag)
		if terr != nil {
			return nil, "", terr
		}
		if prefix, shape, perr := bump.ParseBaseTag(tag); perr == nil {
			p, ok := packageOnLine(cfg, prefix, shape.Major)
			if !ok {
				return nil, "", undeclaredLine(tag, prefix, cfg)
			}
			l := Line{Package: p, TagLine: cfg.LineOf(p), BaseTag: baseTag, Source: tag + "..HEAD", revRange: tag + "..HEAD"}
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
			lines = append(lines, Line{Package: p, TagLine: cfg.LineOf(p), BaseTag: baseTag, Source: tag + "..HEAD", revRange: tag + "..HEAD"})
		}
	}
	union, err := w.unionRange(ctx, cfg, scopedLines(lines, scope), scopeEscape(scope))
	if err != nil {
		return nil, "", err
	}
	return lines, union, nil
}

// undeclaredLine is the usage refusal for a --since-tag value whose line no
// [[packages]] entry declares: flagValue is the value as given (below:<bound>
// or the tag), prefix the line it names.
func undeclaredLine(flagValue, prefix string, cfg *config.Config) error {
	return core.Usagef("--since-tag=%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", flagValue, config.Line{Prefix: prefix}.Label(), declaredLines(cfg))
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

func scopeEscape(scope *Scope) Escape {
	if scope == nil {
		return nil
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
	return Line{Package: p, TagLine: tl, Base: &v, BaseTag: latest, Source: latest + "..HEAD", revRange: latest + "..HEAD"}, nil
}

// unionRange is the one range the walk runs over: a single line's own range;
// the common ancestor of several lines' bases to HEAD; the whole history —
// capped and warned exactly as the single line's — as soon as any line has
// no tag to start from. The diagnosis tells a line with no tag at all from
// one with none under its below: bound: the remedy is the same tag, the
// sentence is not, and past the cap it is the refusal body an operator reads
// beside `git tag -l` (t-gt9n). The remedy — a baseline tag — is named only
// for a line that can take one (baselineOn), after the walk has said it
// proceeds without it: a line whose first change is the root commit gets
// the single line's sentence, since "the commit before" does not exist
// (t-vc5w, measured 2026-11-04 on glyph-monorepo-test-2, both lines added by
// the root commit), and a line whose first change lies at or past a shallow
// clone's boundary is told so, since the commit before a change the clone
// cannot read is not in it either. Past the cap the caller's escape is built
// from the same two lists (Escape): every line the refusal fires for has a
// way out named, in the case it fires in.
func (w Walker) unionRange(ctx context.Context, cfg *config.Config, lines []Line, escape Escape) (string, error) {
	var bases, baselines, unbaselined, pastBoundary, unbounded, bounded []string
	untagged := false
	for _, l := range lines {
		if l.revRange == "" {
			untagged = true
			if l.bound != "" {
				bounded = append(bounded, fmt.Sprintf("no version tag below %s on the %s line in HEAD's history", l.bound, l.TagLine.Label()))
			} else {
				unbounded = append(unbounded, l.TagLine.Label())
			}
			tag, boundary, err := w.baselineOn(ctx, cfg, l)
			if err != nil {
				return "", err
			}
			if tag != "" {
				baselines = append(baselines, tag)
				continue
			}
			if boundary {
				pastBoundary = append(pastBoundary, l.TagLine.Label())
			}
			unbaselined = append(unbaselined, firstReleaseOn(l.TagLine))
			continue
		}
		base := strings.TrimSuffix(l.revRange, "..HEAD")
		if !slices.Contains(bases, base) {
			bases = append(bases, base)
		}
	}
	if untagged {
		var causes []string
		if len(unbounded) > 0 {
			causes = append(causes, fmt.Sprintf("no version tag on the %s line(s) in HEAD's history", strings.Join(unbounded, ", ")))
		}
		causes = append(causes, bounded...)
		remedy := ""
		if len(baselines) > 0 {
			remedy = fmt.Sprintf("; to start from a tag instead, cut %s at the commit before that line's first change — a baseline says nothing of the line was released before there, and it takes its number: the line's first release then steps from it", strings.Join(baselines, " / "))
		}
		if len(pastBoundary) > 0 {
			remedy += fmt.Sprintf("; no baseline can be named for the %s line(s): within this shallow clone the oldest commit touching it is the boundary, whose own diff cannot be read, so the line's first change lies at or past it (check out with fetch-depth: 0)", strings.Join(pastBoundary, ", "))
		}
		out := ""
		if escape != nil {
			out = escape(baselines, unbaselined)
		}
		revRange, _, err := w.wholeHistory(ctx, cfg, strings.Join(causes, "; "), remedy, out)
		return revRange, err
	}
	mb, err := w.Git.MergeBase(ctx, bases)
	if err != nil {
		return "", err
	}
	return mb + "..HEAD", nil
}

// baselineOn is the baseline tag an untagged line can take (firstTagOn), ""
// when it can take none: a baseline is cut at the commit BEFORE the line's
// first change, so a line whose first change is a root commit — or whose
// subtree no commit of HEAD's history touches — has nowhere to put it. The
// first change is asked of git over the line's own files: its path, minus
// the nested packages attribution takes out of it (DESIGN §4.1, rule 1), so
// a parent's first change is never its child's. boundary says the first
// change could not be read: within a shallow clone the oldest commit touching
// the line is the boundary, whose own diff the clone cannot compute
// (gitsource.IsShallowBoundary; DESIGN §4.1, "Where the files come from"), so
// the real first change lies at or past it and a baseline "before" it would
// name a commit the clone does not hold. Not an error: the walk over a
// shallow checkout proceeds and warns (§4), and so does its remedy.
func (w Walker) baselineOn(ctx context.Context, cfg *config.Config, l Line) (tag string, boundary bool, err error) {
	sha, parents, err := w.Git.FirstChange(ctx, "HEAD", linePathspec(cfg, l.Package))
	if gitsource.IsShallowBoundary(err) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if sha == "" || parents == 0 {
		return "", false, nil
	}
	return firstTagOn(l.TagLine), false, nil
}

// firstReleaseOn is the shape of the first release an operator can cut on a
// line by hand, for an escape that has no flag to name a base with: a locked
// line's is its major's floor whatever the level (firstTagOn), a free line's
// takes whatever number the operator gives it.
func firstReleaseOn(l config.Line) string {
	if l.Locked() {
		return firstTagOn(l)
	}
	return l.Prefix + "vX.Y.Z"
}

// linePathspec spells a package's own files as a git pathspec: its path from
// the top level (`:(top)`, so the working directory does not move it) and
// every package nested under it excluded. The root package is the whole tree
// minus every other package.
func linePathspec(cfg *config.Config, p config.Package) []string {
	own, under := ":(top)", ""
	if p.Path != "." {
		own, under = ":(top)"+p.Path, p.Path+"/"
	}
	spec := []string{own}
	for _, q := range cfg.Packages {
		if q.Path != p.Path && (under == "" || strings.HasPrefix(q.Path, under)) {
			spec = append(spec, ":(top,exclude)"+q.Path)
		}
	}
	return spec
}

// firstTagOn is the tag that says "nothing of this line was released before
// here": the null version on a free line, and on a locked line — which has
// no version below its major — the major's own floor. Cut as a baseline it
// is a release like any other and takes its number: the line's first release
// steps FROM it (pubsub/v2.1.0 after a pubsub/v2.0.0 baseline and a ^), the
// price DESIGN §4.1 names for a tag that is an answer rather than a question
// (t-ngac).
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
// definition. Source names the input in messages; revRange is "" (every commit
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
