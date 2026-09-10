package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/internal/attribution"
	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitsource"
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
// repository that declares none. Base is what the bump steps from when the
// walk base names a version, nil when it does not (the line's highest tag is
// then read). Source names the line's own range in messages; Range is the
// revision range whose commits are unreleased on this line, "" when every
// walked commit is (the whole history, or a --range fold).
type line struct {
	Package config.Package
	Prefix  string
	Base    *bump.Version
	Source  string
	Range   string
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
// tag that is not a version on any line names no line: every line walks
// from it and steps from its own highest tag, as the single line does.
//
// The union is the range from the bases' common ancestor to HEAD, which
// contains every line's range; a line with no tag of its own makes it the
// whole history, under the same cap the single line has, with the remedy
// §4.1 names (cut <path>/v0.0.0 at the commit before the line's first
// change).
func resolveLines(ctx context.Context, cfg *config.Config, tagFlag string) ([]line, string, error) {
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
			l, err := lineFromLatest(ctx, p, nil)
			if err != nil {
				return nil, "", err
			}
			lines = append(lines, l)
		}
	case strings.HasPrefix(tag, sinceTagBelow):
		rest := strings.TrimSpace(strings.TrimPrefix(tag, sinceTagBelow))
		prefix, _ := bump.SplitTag(rest)
		p, ok := packageOnLine(cfg, prefix)
		if !ok {
			return nil, "", core.Usagef("--since-tag=below:%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", rest, lineLabel(prefix), declaredLines(cfg))
		}
		// checkSinceTagFlag guaranteed the bound parses on its line.
		bound, perr := bump.ParseBaseVersionOn(prefix, rest)
		if perr != nil {
			return nil, "", core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
		}
		l, err := lineFromLatest(ctx, p, &bound)
		if err != nil {
			return nil, "", err
		}
		lines = []line{l}
	default:
		prefix, _ := bump.SplitTag(tag)
		if v, perr := bump.ParseVersionOn(prefix, tag); perr == nil {
			p, ok := packageOnLine(cfg, prefix)
			if !ok {
				return nil, "", core.Usagef("--since-tag=%s names the %s line, which no [[packages]] entry declares (declared lines: %s)", tag, lineLabel(prefix), declaredLines(cfg))
			}
			lines = []line{{Package: p, Prefix: prefix, Base: &v, Source: tag + "..HEAD", Range: tag + "..HEAD"}}
			break
		}
		for _, p := range cfg.Packages {
			lines = append(lines, line{Package: p, Prefix: p.TagPrefix(), Source: tag + "..HEAD", Range: tag + "..HEAD"})
		}
	}
	union, err := unionRange(ctx, cfg, lines)
	if err != nil {
		return nil, "", err
	}
	return lines, union, nil
}

// lineFromLatest resolves one package's line from its own highest tag —
// strictly below the bound when one is given — else the whole history.
func lineFromLatest(ctx context.Context, p config.Package, below *bump.Version) (line, error) {
	latest, v, err := latestVersionTag(ctx, p.TagPrefix(), below)
	if err != nil {
		return line{}, err
	}
	if latest == "" {
		return line{Package: p, Prefix: p.TagPrefix(), Base: &bump.Version{}, Source: "HEAD"}, nil
	}
	return line{Package: p, Prefix: p.TagPrefix(), Base: &v, Source: latest + "..HEAD", Range: latest + "..HEAD"}, nil
}

// unionRange is the one range the walk runs over: a single line's own range;
// the common ancestor of several lines' bases to HEAD; the whole history —
// capped and warned exactly as the single line's — as soon as any line has
// no tag to start from.
func unionRange(ctx context.Context, cfg *config.Config, lines []line) (string, error) {
	var bases, untagged []string
	for _, l := range lines {
		if l.Range == "" {
			untagged = append(untagged, l.Prefix+"v0.0.0")
			continue
		}
		base := strings.TrimSuffix(l.Range, "..HEAD")
		if !slices.Contains(bases, base) {
			bases = append(bases, base)
		}
	}
	if len(untagged) > 0 {
		revRange, _, err := wholeHistory(ctx, cfg, fmt.Sprintf("no version tag on the %s line(s) — cut %s at the commit before that line's first change to say nothing of it was released before there", strings.Join(untaggedPrefixes(untagged), ", "), strings.Join(untagged, " / ")))
		return revRange, err
	}
	mb, err := gitsource.MergeBase(ctx, ".", bases)
	if err != nil {
		return "", err
	}
	return mb + "..HEAD", nil
}

func untaggedPrefixes(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, lineLabel(strings.TrimSuffix(t, "v0.0.0")))
	}
	return out
}

// packageOnLine finds the declared package whose tag prefix is prefix — ""
// selects the root package (path "."), the one line whose tags are bare.
func packageOnLine(cfg *config.Config, prefix string) (config.Package, bool) {
	for _, p := range cfg.Packages {
		if p.TagPrefix() == prefix {
			return p, true
		}
	}
	return config.Package{}, false
}

// lineLabel names a line in a message: its prefix, or the bare line.
func lineLabel(prefix string) string {
	if prefix == "" {
		return "bare v*"
	}
	return prefix
}

// declaredLines lists every declared line's prefix for a message, the root
// package as bare v*.
func declaredLines(cfg *config.Config) string {
	out := make([]string, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		out = append(out, lineLabel(p.TagPrefix()))
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

// partitionLines splits the walk's commits over the lines. With no packages
// declared it is the identity: one line holding every commit, nothing asked
// of git or the API.
//
// With packages, a commit participates in line p when it is UNRELEASED on p
// (its governing commit is in p's range) AND its diff MOVES p (attribution).
// The first question is git's, answered per line from the line's own range;
// the second is asked only of a commit the fold would read — not an
// exclude_authors author, not a skip-pattern match, not a message no pattern
// claims (those three join every line they are unreleased on: the fold and
// the notes already decide what to do with each, and an unmatched commit
// must still refuse the range it is in). Files come from local git for a
// landed identity and from the API for a squash-merged pull's inner commit,
// the one shape no branch holds; a merge commit's diff is never asked for.
//
// A commit inside the union walk that is released on every line — possible
// on a history where the bases' common ancestor sits before both — is
// dropped with a notice; it was walked because the union had to contain it,
// and it belongs to no line's verdict.
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
		carriers := reach
		if !slices.Contains(cfg.ExcludeAuthors, c.Raw.Author) {
			if m, merr := cfg.Match(c.Raw.Message); merr == nil && m.Matched && !m.Skip {
				files, ferr := walkedFiles(ctx, gh, owner, repo, c, facts)
				if ferr != nil {
					return nil, nil, ferr
				}
				moved, aerr := attribution.Attribute(files, m.Groups[config.ScopeGroup], m.Sigil, cfg.Packages)
				if aerr != nil {
					return nil, nil, attributionWedge(aerr, c, owner, repo, reachedLines(lines, reach))
				}
				carriers = carriers[:0:0]
				for _, i := range reach {
					if slices.ContainsFunc(moved, func(p config.Package) bool { return p.Path == lines[i].Package.Path }) {
						carriers = append(carriers, i)
					}
				}
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
// API for a squash-merged pull's inner commit. An API listing that reached
// CommitFilesCap is recorded on the facts: the files past it are unreachable,
// not absent, and a package they touch would be missing from the verdict —
// an incomplete walk in §4's sense.
func walkedFiles(ctx context.Context, gh *github.Client, owner, repo string, c walked, facts *walkFacts) ([]string, error) {
	if c.Raw.Parents >= 2 {
		return nil, nil
	}
	if c.Landed {
		return gitsource.DiffTreeFiles(ctx, ".", c.Raw.SHA)
	}
	files, capped, err := gh.CommitFiles(ctx, owner, repo, c.Raw.SHA)
	if err != nil {
		return nil, err
	}
	if capped {
		facts.FilesCapped = append(facts.FilesCapped, fmt.Sprintf("%.7s", c.Raw.SHA))
		warnf("commit %.7s in pull request #%d touches at least %d files, and GitHub lists no more than that — the files past the cap could not be read, so a package they touch is missing from this verdict", c.Raw.SHA, c.Pull, github.CommitFilesCap)
	}
	return files, nil
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
	escapes := make([]string, 0, len(reached))
	for _, l := range reached {
		escapes = append(escapes, fmt.Sprintf("a %s tag at or past %.7s", lineLabel(l.Prefix), c.governing()))
	}
	return &core.Error{Code: core.CodeLint, Details: []rangeViolation{{SHA: c.Raw.SHA, Subject: bump.FirstLine(c.Raw.Message), Detail: err.Error()}}, Msg: fmt.Sprintf(
		"commit %.7s: %v — %s; the commit is already on a published branch and cannot be rewritten, so every release of a line whose range holds it wedges here until that line's walk starts past it: cut %s by hand, or name such a tag with --since-tag=<line>vX.Y.Z (DESIGN §4.1)",
		c.Raw.SHA, err, where, strings.Join(escapes, " / "))}
}

// rangeLines is the --range twin of sinceTagInput for a repository with
// packages: every commit in the local range is a landed identity, unreleased
// on every line (a --range fold names no release base — the caller chose the
// range), and attributed from local git. The walk facts are empty: nothing
// was resolved over the API.
func rangeLines(ctx context.Context, cfg *config.Config, revRange string) (sinceTagWalk, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return sinceTagWalk{}, err
	}
	commits := make([]walked, 0, len(raws))
	for _, r := range raws {
		commits = append(commits, walked{Raw: r, Landed: true})
	}
	lines := make([]line, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		lines = append(lines, line{Package: p, Prefix: p.TagPrefix(), Source: revRange})
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
		names = append(names, lineLabel(l.Prefix))
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

// refusePackages is the answer of a command that has no per-line form yet:
// release converges one draft, preview renders one verdict, and running
// either over a repository whose verdict has N lines would act on the wrong
// answer. The remaining e-7hat tasks lift it (DESIGN §4.1 status).
func refusePackages(cfg *config.Config, command string) error {
	if len(cfg.Packages) == 0 {
		return nil
	}
	return core.Usagef("this repository declares [[packages]], and `glyph %s` does not answer per line yet — bump --since-tag and notes --since-tag do (DESIGN §4.1)", command)
}
