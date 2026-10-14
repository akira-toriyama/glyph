package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/notes"
	"github.com/akira-toriyama/glyph/v5/internal/preview"
	"github.com/spf13/cobra"
)

// previewResult is the machine verdict:
// {current, untagged, level, next, pr, pending, body} — plus packages when
// the repository declares [[packages]]. level/next are the FOLDED
// answer — what the version becomes if this PR merges — so a caller never
// re-derives it from the two sides; pr and pending are the two sides it folded,
// reported separately so a comment can show the working. With packages
// declared every scalar carries its zero value — the strings empty (next,
// omitempty, absent), untagged false — and packages carries one folded verdict per touched line (DESIGN
// §4.1): pr-verdict.yml's level output is then "", which its callers already
// treat as not computed, and its breaking output follows level into "". A
// pr or pending of none there would claim the pull moves nothing beside a
// packages[] whose lines move (t-xbk0).
type previewResult struct {
	Current  string           `json:"current"`
	Untagged bool             `json:"untagged"`
	Level    string           `json:"level"`
	Next     string           `json:"next,omitempty"`
	PR       string           `json:"pr"`
	Pending  string           `json:"pending"`
	Body     string           `json:"body"`
	Packages []packagePreview `json:"packages,omitempty"`
}

// packagePreview is one line's verdict — bump's vocabulary (bare versions,
// the folded level, the two sides) per line. Under [[packages]] it is one
// entry of previewResult.packages; on the bare line it fills the scalars.
type packagePreview struct {
	Path     string `json:"path"`
	Current  string `json:"current"`
	Untagged bool   `json:"untagged"`
	Level    string `json:"level"`
	Next     string `json:"next,omitempty"`
	PR       string `json:"pr"`
	Pending  string `json:"pending"`
}

// previewOptions is what one preview run was asked, snapshotted at RunE.
// Nothing below RunE reads a flag — previewRun is a function of these alone,
// so the preview runs with no command built
// (TestPreviewRunsFromOptionsWithoutCobra). preview has one input source, the
// pull request --pr names, so the options hold its number and --repo as
// fields rather than the source type bump and notes dispatch on: routing
// preview through readSource would let it preview a local range or a walk,
// which nothing asked for.
type previewOptions struct {
	pr    int
	repo  string
	notes bool
	json  bool
}

// previewWalkEscape is the remedy preview can actually offer: it has no
// --since-tag flag, so the refusal names only the tag the message already
// told the operator to cut.
const previewWalkEscape = "; cut the tag named above and re-run — preview reads the pull request, and has no --since-tag flag to name a base with"

func newPreviewCmd() *cobra.Command {
	var o previewOptions
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Render the merge preview for a pull request as one Markdown comment",
		Long: "preview answers, BEFORE the merge, what merging a pull request does to\n" +
			"the version — and renders it as the whole sticky-comment body, marker\n" +
			"included. It folds two verdicts: the PR's own INDIVIDUAL (pre-squash)\n" +
			"commits read over the API, and what is already merged but unreleased on\n" +
			"the checked-out branch. Both step from the same tag, so the answer is the\n" +
			"`next` of whichever folds higher — no caller ever does version arithmetic.\n\n" +
			"A base branch whose history holds no v* tag skips the pending walk: it\n" +
			"would resolve the whole history at one API round-trip per commit to\n" +
			"answer a question that cannot matter (nothing is unreleased when nothing\n" +
			"was released), so the PR's own verdict stands alone and the body says so.\n\n" +
			"The body never names a rolling draft — the arithmetic is the same whether\n" +
			"the next release is a draft or a tag cut by hand, and most repos have no\n" +
			"draft to name. stdout is the Markdown body (post it with `gh pr comment\n" +
			"--body-file -`); --json emits {current,untagged,level,next,pr,pending,body}.\n" +
			"Unlike bump and notes, a none verdict is a normal answer here and exits 0:\n" +
			"'this PR moves nothing' is exactly what a reviewer asked.\n\n" +
			"On a repository declaring [[packages]] the body carries one headline per\n" +
			"line the pull touches (versions spelled as tags: haiku/v0.1.0 →\n" +
			"haiku/v0.2.0) and one commit table per line; a line the pull does not\n" +
			"touch is not mentioned. --json carries packages:\n" +
			"[{path,current,untagged,level,next,pr,pending}] with the scalars\n" +
			"current/level/pr/pending EMPTY, untagged false and next absent. A commit\n" +
			"no line can carry (a version sigil on shared files with no package scope)\n" +
			"is refused at exit 3 here, while the branch can still be fixed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
				return err
			}
			return previewRun(cmd.Context(), o)
		},
	}
	cmd.Flags().IntVar(&o.pr, "pr", 0, "the pull request to preview, read over the API (required)")
	cmd.Flags().StringVar(&o.repo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().BoolVar(&o.notes, "notes", false, "fold the release-notes preview for this PR into the body")
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the machine verdict {current,untagged,level,next,pr,pending,body}; with [[packages]] declared current/level/pr/pending are empty, untagged false, next absent, and packages[] carries one verdict per line")
	_ = cmd.MarkFlagRequired("pr")
	return cmd
}

// previewRun is preview below its flags (DESIGN §4.1, "Preview"): the pull's
// commits placed on the lines they touch (attributePull), the whole listing
// folded once, each touched line's own fold (foldTouched), the pending side
// from one walk (previewPending), one verdict per touched line
// (lineVerdict), and the render. A repository without [[packages]] answers
// for one bare line holding the whole pull, so the per-line loop is the only
// loop and the render alone tells the shapes apart. The bare line's verdict
// is the scalars and preview.Render's scalar body, the pre-packages surface
// byte for byte (TestPreviewBareLineRendersTheScalarBody). Under packages
// every scalar keeps its zero value — pr and pending included: none is a
// level word, and a scalar saying the pull moves nothing beside a
// packages[] whose lines move is the #219 class (t-xbk0) — packages[]
// carries one verdict per touched line, and the body one headline per line,
// or preview.RenderNoLine's sentence when the pull touches none.
func previewRun(ctx context.Context, o previewOptions) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	raws, name, err := pullInput(ctx, o.pr, o.repo)
	if err != nil {
		return err
	}
	owner, repo, err := resolveRepo(ctx, o.repo)
	if err != nil {
		return err
	}
	touched, prShort, err := attributePull(ctx, newGitHub(), cfg, owner, repo, o.pr, raws, name)
	if err != nil {
		return err
	}
	// The whole listing is folded once, before any line: a message no pattern
	// claims refuses the preview here on either shape, which is why
	// attributePull places no such commit. Its rows are the pull's
	// participating commits (DESIGN §4.1), the one count every body states —
	// the first cut of the packages bodies counted len(raws), bots and merge
	// commits included, where no line was touched, and what the tables showed
	// where one was (t-rrw0) — and each row's pattern warning is said here,
	// once: said per touched line, a warned commit on no line was silent while
	// bump said it, and one on two lines was said twice.
	rows, _, err := bump.FoldSigils(sigilCommits(raws), cfg)
	if err != nil {
		return err
	}
	warnSigilVerdicts(rows)
	if err := foldTouched(ctx, cfg, touched); err != nil {
		return err
	}
	pending, pendingShort, pendingWalked, err := previewPending(ctx, cfg, o.repo, owner, repo, touched)
	if err != nil {
		return err
	}

	lines := make([]preview.Package, 0, len(touched))
	verdicts := make([]packagePreview, 0, len(touched))
	lineNotes := make([]string, 0, len(touched))
	for _, tl := range touched {
		p, pv, err := lineVerdict(tl, pending[tl.Package.Path], pendingWalked)
		if err != nil {
			return err
		}
		n := ""
		if o.notes {
			sections, err := notes.GroupSigils(noteCommits(tl.commits, o.pr), cfg)
			if err != nil {
				return err
			}
			if len(sections) > 0 {
				n = notes.RenderSigils(sections)
			}
		}
		lines, verdicts, lineNotes = append(lines, p), append(verdicts, pv), append(lineNotes, n)
	}

	in := preview.Input{PendingShort: pendingShort, PRShort: prShort}
	res := previewResult{Packages: verdicts}
	render := preview.Render
	if len(cfg.Packages) == 0 {
		one, v := lines[0], verdicts[0]
		in.Current, in.Untagged, in.PR, in.Pending, in.Notes = one.Current, one.Untagged, one.PR, one.Pending, lineNotes[0]
		res = previewResult{Current: v.Current, Untagged: v.Untagged, Level: v.Level, Next: v.Next, PR: v.PR, Pending: v.Pending}
	} else {
		in.Participating, in.OffLine, in.Packages = len(rows), offLine(rows, touched), lines
		var headed []string
		for i, n := range lineNotes {
			if n != "" {
				headed = append(headed, "# "+lines[i].Path+"\n\n"+n)
			}
		}
		in.Notes = strings.Join(headed, "\n")
		if len(lines) == 0 {
			render = preview.RenderNoLine
		}
	}
	// Truncated (never refused) before EITHER output path: the workflow posts
	// the --json payload's body, so an oversized comment has to be cut in the
	// one place both surfaces share.
	res.Body = truncateComment(render(in))
	if o.json {
		printCompact(res)
		return nil
	}
	fmt.Fprint(out, res.Body)
	return nil
}

// touchedLine is one line the pull touches and what its verdict is folded
// from: the pull's commits placed on it, in listing order, their fold, and
// the line's highest tag HEAD holds — "" when it holds none — with the
// version that tag parses to, v0.0.0 without one.
type touchedLine struct {
	line
	commits []gitsource.RawCommit
	rows    []bump.SigilVerdict
	pr      bump.Decision
	tag     string
	current bump.Version
}

// attributePull places the pull's commits on the lines and returns the lines
// they touch, in config order, with the PR side's shortfall — the listings
// GitHub did not give whole, for the body's PR-side caveat
// (preview.Input.PRShort). Without [[packages]] the bare line holds the whole
// pull and no file is asked for: partitionLines' identity arm. With packages
// each commit is placed as the walk places a squash-merged pull's inner
// commit (placeByFiles), its files read over the API, one request per commit
// — a pull's commits exist on its branch only, and this checkout may not hold
// them — by its files, with its scope and sigil when the fold reads the
// message and by files alone for an exclude_authors commit; or not at all: a
// skip appears nowhere, and a message no pattern claims refuses the
// whole-listing fold (previewRun), so it is not attributed first. The first
// cut dropped excluded authors here while the walk placed them on every line
// — two answers in one run (t-sr1c, measured 2026-09-11; mutation row
// preview-packages-excluded-author-dropped). A line no commit was placed on
// is not touched, and a pull whose commits carry nothing touches none.
//
// A refusal over a diff read whole is the lint-class refusal the walk and
// lint --range hand down — preview says what CI will say — worded for where
// the commit sits: on a branch that can still be fixed.
func attributePull(ctx context.Context, gh *github.Client, cfg *config.Config, owner, repo string, number int, raws []gitsource.RawCommit, name string) ([]touchedLine, string, error) {
	lines := pendingLines(cfg, name)
	if len(cfg.Packages) == 0 {
		return []touchedLine{{line: lines[0], commits: raws}}, "", nil
	}
	perLine := make([][]gitsource.RawCommit, len(lines))
	var facts walkFacts
	for _, r := range raws {
		place, said := placeOf(cfg, r)
		if place != placedByFiles {
			continue
		}
		placed, err := placeByFiles(ctx, gh, cfg, owner, repo, walked{Raw: r, Pull: number}, said, &facts, nil)
		if err != nil {
			return nil, "", err
		}
		if placed.refused != nil {
			return nil, "", core.Lintf("commit %.7s in pull request %s/%s#%d: %v — the release walk will refuse this commit the same way once it is merged, and a merged commit cannot be rewritten; fix it on the branch", r.SHA, owner, repo, number, placed.refused).
				WithDetails([]bump.Refusal{{SHA: r.SHA, Subject: bump.FirstLine(r.Message), Detail: placed.refused.Error()}})
		}
		for i, l := range lines {
			if slices.ContainsFunc(placed.moved, func(p config.Package) bool { return p.Path == l.Package.Path }) {
				perLine[i] = append(perLine[i], r)
			}
		}
	}
	var touched []touchedLine
	for i, l := range lines {
		if len(perLine[i]) == 0 {
			continue
		}
		touched = append(touched, touchedLine{line: l, commits: perLine[i]})
	}
	short := ""
	if !facts.complete() {
		short = facts.shortfall(owner, repo)
	}
	return touched, short, nil
}

// foldTouched folds the pull's commits on each touched line and reads the
// line's highest tag HEAD holds, in config order. The tag NAME is kept, not
// only the version it parses to: whether a line has released is a question
// about the existence of a tag, and the version answers only what to step
// FROM — v0.0.0 for a line with no tag, which is also a perfectly ordinary
// tag to have cut. Comparing the version against the zero value called a
// repository tagged v0.0.0 untagged, skipped its pending walk and printed
// "this repository has no v* release tag yet" — while `bump
// --since-tag=auto` on the SAME checkout walked v0.0.0..HEAD and answered
// minor.
func foldTouched(ctx context.Context, cfg *config.Config, touched []touchedLine) error {
	for i := range touched {
		tl := &touched[i]
		rows, dec, err := bump.FoldSigils(sigilCommits(tl.commits), cfg)
		if err != nil {
			return err
		}
		tag, current, err := latestVersionTag(ctx, tl.Line, nil)
		if err != nil {
			return err
		}
		tl.rows, tl.pr, tl.tag, tl.current = rows, dec, tag, current
	}
	return nil
}

// previewPending folds what is already merged but unreleased, per touched
// line, from ONE walk, and says whether it walked. The walk runs only when a
// touched line has released at all — the release-floor guard. It is the one
// unbounded input: with no tag in HEAD's history it resolves the whole
// history at one API round-trip per commit (sinceTagRange names the cost
// rather than hiding it), which distributed fleet-wide lands on repositories
// with a thousand commits and no release, and buys nothing: nothing is
// unreleased on a line that never released. The PR's own verdict is then
// the whole answer, and the body says why.
//
// The walk's RANGE comes from the touched lines alone. Resolved over every
// declared line it used to take one untouched line with no tag to the whole
// history: past the cap that refused the whole command (exit 4) for a pull
// that touches only released lines, and under it that bought one API
// round-trip per commit of the history for a line nobody asked about (t-60dc
// symptoms A and B, measured 2026-09-15 on a 211-commit fixture: exit 4 for
// a haiku-only pull, and 9 round-trips where the touched line's own range
// held 1).
//
// The pending side is the one side computed from a walk, so it is the one
// side that can come back short. release refuses to act on that; there is
// nothing to refuse here, so the shortfall is returned for the body instead
// — see preview.Input.PendingShort for why the walk's own ::warning:: is not
// enough on this path.
func previewPending(ctx context.Context, cfg *config.Config, repoFlag, owner, repo string, touched []touchedLine) (map[string]bump.Decision, string, bool, error) {
	if !slices.ContainsFunc(touched, func(tl touchedLine) bool { return tl.tag != "" }) {
		if len(touched) > 0 {
			warnf("%s", skippedWalkWarning(cfg))
		}
		return nil, "", false, nil
	}
	only := make([]config.Package, 0, len(touched))
	for _, tl := range touched {
		only = append(only, tl.Package)
	}
	w, err := sinceTagInputScoped(ctx, cfg, sinceTagAuto, repoFlag, &walkScope{Only: only, Escape: previewWalkEscape})
	if err != nil {
		return nil, "", false, err
	}
	short := ""
	if !w.Facts.complete() {
		short = w.Facts.shortfall(owner, repo)
	}
	// The union once, as bump folds it, and for the pull's reason (previewRun):
	// warned per walked line, a merged warned commit on no line was silent and
	// one on two lines was said twice.
	rows, _, err := bump.FoldSigils(walkedSigilCommits(w.All), cfg)
	if err != nil {
		return nil, "", false, err
	}
	warnSigilVerdicts(rows)
	pending := make(map[string]bump.Decision, len(w.Lines))
	for _, lw := range w.Lines {
		_, dec, err := bump.FoldSigils(walkedSigilCommits(lw.Commits), cfg)
		if err != nil {
			return nil, "", false, err
		}
		pending[lw.Package.Path] = dec
	}
	return pending, short, true, nil
}

// skippedWalkWarning names the pending walk the release-floor guard skipped,
// one sentence for both shapes: "on any line this PR touches" and "per line"
// are said exactly when there are lines to say them of (noNotesReason's rule;
// mutation row preview-bare-line-floor-warning-says-on-any-line).
func skippedWalkWarning(cfg *config.Config) string {
	where, per := "", ""
	if len(cfg.Packages) > 0 {
		where, per = " on any line this PR touches", " per line"
	}
	return "no release tag in HEAD's history" + where + " — previewing the PR's own verdict" + per + " (the pending walk needs a release floor)"
}

// lineVerdict is one touched line's verdict in the two shapes the render
// reads: the body's, its versions spelled as the line's tags, and the
// machine verdict's, its versions bare. Each side steps from the line's own
// current version on its own line (nextOn), and the answer is the higher of
// the two (foldDecision).
func lineVerdict(tl touchedLine, pending bump.Decision, pendingWalked bool) (preview.Package, packagePreview, error) {
	prefix := tl.Line.Prefix
	untagged := tl.tag == ""
	p := preview.Package{Path: tl.Package.Path, Current: tl.current.TagOn(prefix), Untagged: untagged,
		// PendingWalked is the half preview.Input.Untagged used to carry on
		// its own: "the pending side is UNCOMPUTED". A line with no tag still
		// gets its pending walked whenever a tagged sibling in the same pull
		// pulls the walk to the whole history, and reusing one flag for both
		// meanings made the same run answer twice — body "the first release
		// here would be curry/v0.0.1", machine verdict next=v0.1.0 (t-60dc
		// symptom C, measured 2026-09-15).
		PendingWalked: pendingWalked,
		PR:            preview.Verdict{Level: tl.pr.Level, Commits: previewCommits(tl.rows)}}
	pv := packagePreview{Path: tl.Package.Path, Current: tl.current.String(), Untagged: untagged, PR: string(tl.pr.Level), Pending: string(orNone(pending.Level))}
	if tl.pr.Level != bump.LevelNone {
		next, err := nextOn(tl.Line, tl.current, tl.pr)
		if err != nil {
			return preview.Package{}, packagePreview{}, err
		}
		p.PR.Next = next.TagOn(prefix)
	}
	p.Pending.Level = orNone(pending.Level)
	if pending.Level != bump.LevelNone && pending.Level != "" {
		next, err := nextOn(tl.Line, tl.current, pending)
		if err != nil {
			return preview.Package{}, packagePreview{}, err
		}
		p.Pending.Next = next.TagOn(prefix)
	}
	d := foldDecision(tl.pr, pending)
	pv.Level = string(d.Level)
	if d.Level != bump.LevelNone {
		next, err := nextOn(tl.Line, tl.current, d)
		if err != nil {
			return preview.Package{}, packagePreview{}, err
		}
		pv.Next = next.String()
	}
	return p, pv, nil
}

// offLine counts the participating commits no touched line holds — a
// shared-only =, or a commit whose refusal was withheld — which no table
// shows, so the packages footer says how many there are.
func offLine(rows []bump.SigilVerdict, touched []touchedLine) int {
	onLine := map[string]bool{}
	for _, tl := range touched {
		for _, r := range tl.commits {
			onLine[r.SHA] = true
		}
	}
	n := 0
	for _, row := range rows {
		if !onLine[row.SHA] {
			n++
		}
	}
	return n
}

// foldDecision is the whole point of the command: the version this PR lands on
// is the HIGHER of its own decision and what is already pending, because both
// step from the same tag. bump.Decision.Merge owns the ordering — the same
// max-fold every other verdict in glyph goes through, so preview cannot grow a
// second opinion about which level wins, and a '%' on either side promotes the
// preview exactly as it will promote the release.
func foldDecision(a, b bump.Decision) bump.Decision {
	a.Level, b.Level = orNone(a.Level), orNone(b.Level)
	return a.Merge(b)
}

// orNone normalizes an uncomputed level. bump.Level's zero value is "" rather
// than "none" (it is a string type), and a skipped pending walk leaves exactly that
// — which must read as "nothing pending", never as an unknown that leaks into
// the JSON surface.
func orNone(b bump.Level) bump.Level {
	if b == "" {
		return bump.LevelNone
	}
	return b
}

// previewCommits maps the shared verdict rows onto the render's own type. The
// two are deliberately separate: bump.SigilVerdict is a JSON surface other
// commands publish, and preview must be free to show a column it does not.
func previewCommits(cs []bump.SigilVerdict) []preview.Commit {
	out := make([]preview.Commit, 0, len(cs))
	for _, c := range cs {
		out = append(out, preview.Commit{
			Sigil:   c.Sigil,
			Level:   bump.Level(c.Level),
			Subject: c.Subject,
		})
	}
	return out
}
