package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/draftplan"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/notes"
	"github.com/spf13/cobra"
)

// releaseResult is the machine verdict: {current, level, tag, target, body,
// action, url, commits, pulls, reason} — plus packages when the repository
// declares [[packages]]. tag, target and body are present exactly when the
// run upserts a draft (a dry run: would upsert one) — a release verdict, or
// the Unreleased placeholder draft_on_none maintains — and omitted otherwise:
// there is no release to act on. url is present only when a write actually
// happened (never on a dry run). pulls is the walk's expansion provenance —
// which merged pulls it resolved and how many of each one's listed commits
// the walk took in — which is what a human or a CI step reads to audit how a
// verdict was assembled.
//
// With packages declared, current / level / action are EMPTY, tag / body /
// url are absent, and packages carries one verdict and one draft per line
// (DESIGN §4.1): a caller written for the single line — release.yml's scalar
// outputs, a tag step — then fails safe on "" or an absent key, which its
// contract already tells it to. target is shared — one checkout, one HEAD, every line's draft
// points at it — under the same rule: omitted when no line upserts a draft
// (every selected line none, draft_on_none off). commits then lists every
// participating commit (bumpResult). newReleaseResult is the one producer of
// both shapes.
type releaseResult struct {
	Current  string           `json:"current"`
	Level    string           `json:"level"`
	Tag      string           `json:"tag,omitempty"`
	Target   string           `json:"target,omitempty"`
	Body     string           `json:"body,omitempty"`
	Action   string           `json:"action"`
	URL      string           `json:"url,omitempty"`
	Commits  []bump.Row       `json:"commits"`
	Packages []packageRelease `json:"packages,omitempty"`
	Pulls    []pullExpansion  `json:"pulls"`
	Reason   string           `json:"reason"`
}

// packageRelease is one line's verdict: what bump's packageVerdict carries,
// plus the draft this line converges to — its tag, its body, the convergence
// performed and, after a real write, its URL. Under [[packages]] it is one
// entry of releaseResult.packages; on the bare line it fills the scalars.
type packageRelease struct {
	Path    string     `json:"path"`
	Current string     `json:"current"`
	Level   string     `json:"level"`
	Next    string     `json:"next,omitempty"`
	Tag     string     `json:"tag,omitempty"`
	Body    string     `json:"body,omitempty"`
	Action  string     `json:"action"`
	URL     string     `json:"url,omitempty"`
	Commits []bump.Row `json:"commits"`
	Reason  string     `json:"reason"`
}

// lineDraft is one line's planned write: which verdict it belongs to (an
// index into the run's verdicts, where its URL lands once written) and the
// draft it converges to (nil plan Keep means create).
type lineDraft struct {
	at     int
	plan   draftplan.Plan
	params github.ReleaseParams
}

// releaseOptions is what one release run was asked, snapshotted at RunE.
// Nothing below RunE reads a flag — releaseRun is a function of these alone,
// so the run drives with no command built
// (TestReleaseRunsFromOptionsWithoutCobra). tag is sinceTagAuto when
// --since-tag was not given: release has exactly one input source, so a bare
// flag is not required.
type releaseOptions struct {
	tag        string
	repo       string
	current    string
	target     string
	footerFile string
	dryRun     bool
	json       bool
}

func newReleaseCmd() *cobra.Command {
	var o releaseOptions
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Upsert the rolling DRAFT release from one composed verdict",
		Long: "release runs the squash-safe walk ONCE and derives both the next version\n" +
			"and the release-notes body from that single commit set — calling bump and\n" +
			"notes separately walks twice, and a merge landing between the walks could\n" +
			"version one range and describe another. The walk defaults to the highest\n" +
			"v* tag HEAD contains (release has exactly one input source, so no bare\n" +
			"--since-tag is required). Bare release upserts the rolling DRAFT\n" +
			"release: the one glyph-managed draft is created or updated (tag, notes\n" +
			"body, target sha) FIRST, and stale drafts are then removed by release\n" +
			"id; one that will not go leaves a warning rather than failing the\n" +
			"release, because the notes have already landed and the next run\n" +
			"converges it — no tag is created; GitHub tags the target commit when a\n" +
			"human publishes.\n" +
			"On a none verdict any\n" +
			"residual glyph-managed draft is deleted (the draft state converges to\n" +
			"the verdict) and the run exits 1 (soft no-release). A walk that could\n" +
			"NOT read its range (a wrong --repo, a merged pull whose merge point\n" +
			"nothing resolved, a commit GitHub has not indexed, a truncated pull\n" +
			"listing, a shallow checkout) exits 4 before touching anything — an\n" +
			"empty fold from a walk that could not look is not evidence that\n" +
			"nothing shipped, so no verdict is handed down on it at all.\n" +
			"The body's notes close with the range's compare link\n" +
			"(compare/<base tag>...<target>; none when the walk has no tag base, the\n" +
			"notes are empty, or --target is not a full sha), before the\n" +
			"--footer-file block.\n" +
			"A real run prints the draft's URL; --dry-run computes everything\n" +
			"including that action and writes nothing, printing the tag line, a\n" +
			"blank line, then the Markdown body. --json emits\n" +
			"{current,level,tag,target,body,action,url,commits,pulls,reason} — pulls is the\n" +
			"walk's expansion provenance (each resolved pull and how many of its listed\n" +
			"commits the walk took in), which is how a verdict can be audited after the\n" +
			"fact.\n\n" +
			"On a repository declaring [[packages]] release converges ONE rolling draft\n" +
			"per line (haiku/v0.2.0 beside curry/v0.1.1), every line's upsert written\n" +
			"before any stray is deleted, each draft's compare link from its own\n" +
			"line's base tag, and --footer-file appended to each; exit 1\n" +
			"means every line folded to none. --dry-run prints one block per line (tag\n" +
			"line, blank line, body); --json carries packages:\n" +
			"[{path,current,level,next,tag,body,action,url,commits,reason}] with the\n" +
			"scalar current/level/action EMPTY, tag/body/url absent, and target shared\n" +
			"(omitted, as on the single line, when no line has a draft to write).\n" +
			"--current is refused (exit 2) unless the walk selects one line\n" +
			"(--since-tag=<prefix>vX.Y.Z or below:, <prefix> being the line's tag\n" +
			"prefix: haiku/, or nothing for the root package).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// release is where a silently-ignored empty flag costs most:
			// --current decides the tag, --target decides the commit that tag
			// will point at, and both defaults look plausible enough to publish.
			if err := checkNamingFlags(cmd, [][3]string{
				{"current", "version", currentHint},
				{"repo", "repository", repoHint},
				{"target", "commit", "omit --target to point the draft at this checkout's HEAD, or name a commit with --target=SHA"},
				{"footer-file", "path", "omit --footer-file to publish the notes alone, or name the file with --footer-file=PATH"},
			}); err != nil {
				return err
			}
			if !cmd.Flags().Changed("since-tag") {
				o.tag = sinceTagAuto
			}
			return releaseRun(cmd.Context(), o)
		},
	}
	addSinceTagFlag(cmd, &o.tag, "compose the release from")
	cmd.Flags().StringVar(&o.repo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().StringVar(&o.current, "current", "", currentFlagUsage)
	cmd.Flags().StringVar(&o.target, "target", "", "the commit sha the draft's eventual tag points at (default: the checkout's HEAD)")
	cmd.Flags().StringVar(&o.footerFile, "footer-file", "", "a Markdown file appended verbatim after the notes, separated by one --- line (the per-repo install block)")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "compute the full verdict and the draft action but write nothing to GitHub")
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the machine verdict {current,level,tag,target,body,action,url,commits,pulls,reason}; with [[packages]] declared current/level/action are empty, tag/body/url absent (target too when no line has a draft to write), and packages[] carries one verdict per line")
	return cmd
}

// releaseRun is release below its flags: one walk, one verdict and one
// rolling draft per line, converged by draftplan on that line's own tag
// prefix so the founding invariants — never a second draft, retagged in
// place, nothing glyph did not manage touched — hold per line. A repository
// without [[packages]] walks as one bare line (sinceTagInput), so the
// per-line loop is the only loop; newReleaseResult tells the shapes apart at
// the render, and the residue guard below the loop is the one other place
// that asks. The decisions it carries, each ratified in §4 and §4.1:
//
//   - every line's verdict, body and plan are computed BEFORE any write, so a
//     line that refuses (the published floor, an oversized body) stops the
//     run with nothing written;
//   - the write order is write-first: every line's upsert lands before any
//     delete, and a write that fails on the second line leaves the first
//     line's notes standing and exits 4 — the next run heals it;
//   - a line that folds to none (draft_on_none off) has its residual drafts'
//     deletes as its whole action, so one that will not go exits 4 whatever
//     its siblings wrote; the strays of the lines that write a draft keep
//     convergeStray's leniency. Deletes run after the upserts, the residuals
//     first, and a residual that will not go is answered only once every
//     other delete was tried;
//   - a tag that selects one line converges that line ALONE: the other
//     lines' drafts are not this run's to touch;
//   - the bare vX.Y.Z draft of a repository that declares packages but no
//     root package is the single line's residue and is deleted on the first
//     packages run — a hand region it carried goes with it, which a dry run's
//     notice says before anything is written and a real run's once the
//     DELETE went;
//   - --footer-file appends to every draft; make_latest is never set;
//   - exit 1 is answered only when every selected line folds to none.
func releaseRun(ctx context.Context, o releaseOptions) error {
	// Caller-input problems surface before any network or git work — and
	// before the environment is judged: the footer file is read up front, and
	// the repository must resolve for the releases listing (sinceTagInput
	// re-resolves it for the walk — same answer, one source of truth in
	// resolveRepo).
	footer, ferr := readFooter(o.footerFile)
	if ferr != nil {
		return ferr
	}
	owner, repoName, oerr := resolveRepo(ctx, o.repo)
	if oerr != nil {
		return oerr
	}
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	// Before the walk, not merely before the write: everything above this line
	// is local and free, and sinceTagInput is where the money goes (at least
	// one API round-trip per commit it visits). A run on the wrong ref is not
	// going to be allowed to write, so paying for its verdict is waste — and
	// the walk is also the half that produces the wrong ANSWER, not only the
	// wrong write, which is why this sits above it rather than beside the
	// upsert.
	if rerr := checkReleaseRef(ctx, owner, repoName, o.dryRun); rerr != nil {
		return rerr
	}
	w, perr := sinceTagInput(ctx, cfg, o.tag, o.repo)
	if perr != nil {
		return perr
	}
	// A verdict is a claim about the range only when the walk READ the range,
	// and release is the command that acts on its verdict irreversibly. So a
	// walk that came back short — every commit unknown to the queried
	// repository, a merged pull whose merge point nothing resolved, a commit
	// GitHub had not indexed, a truncated pull listing, a shallow checkout —
	// stops here, before the releases listing is even fetched: fail loud (4),
	// like every other place glyph refuses to judge what it could not read
	// (the wedge, the covered pull, the published floor). Ratified t-pysg,
	// replacing #66's warn-and-refuse-to-destroy: that compromise existed for
	// the repository whose merge button an automation presses, which then
	// warned on every release structurally — but merge points resolve now
	// (t-7zt7), so what remains of that shape is a bot-authored merge commit,
	// and a walk blind to a whole pull is exactly what this exit is for. The
	// escape is the wedge escape: cut a tag at or past what the walk cannot
	// read, or fix the checkout (fetch-depth: 0) when the shortfall names it.
	if !w.Facts.complete() {
		return core.APIf("this walk did not read %s (%s) — refusing to hand down a verdict computed from a range it could not read; re-run the release once the walk can read the range",
			w.Source, w.Facts.shortfall(owner, repoName))
	}
	if err := checkLineSelection(o.current, w.Lines); err != nil {
		return err
	}
	// The union rows first: a message no pattern claims refuses the walk
	// before any line is folded, and the rows are what the verdict reports as
	// the commits it read. On the bare line the union is the line.
	rows, _, cerr := bump.Fold(walkedBumpCommits(w.All), cfg)
	if cerr != nil {
		return cerr
	}
	warnBumpRows(rows)
	// --current is the caller's input, so its shape is refused here, after
	// the fold's warnings and before the releases listing is paid for: a
	// malformed flag beside a listing that will not answer is usage (2), not
	// the listing's 4.
	if _, err := parseCurrent(o.current); err != nil {
		return err
	}

	// The releases listing feeds both halves of the convergence decision: the
	// glyph-managed drafts to upsert or clear, and the published floor the
	// next version must clear. The dry run performs this read too: only the
	// writes are skipped.
	gh := newGitHub()
	releases, lerr := gh.Releases(ctx, owner, repoName)
	if lerr != nil {
		return lerr
	}
	drafted := planInput(releases)

	// The target resolves BEFORE the dry-run fork (only the writes are
	// skipped), once for every draft — one checkout, one HEAD — and above the
	// loop, because each line's compare link ends at it and checkReleaseBody
	// sizes the final body; so every run without --target reads HEAD once,
	// a none verdict and a refusal below included. It used to sit below the
	// fork, which made `--dry-run --target=X` byte-identical to `--dry-run`
	// for every X: the flag naming which commit the eventual tag points at
	// was the one flag the preview silently ignored, so a typo surfaced only
	// on the real run (t-nfz3).
	target := o.target
	if target == "" {
		var herr error
		if target, herr = gitsource.Head(ctx, "."); herr != nil {
			return herr
		}
	}

	var verdicts []packageRelease
	var drafts []lineDraft
	// residual: the drafts whose delete is a none line's whole action, loud.
	// stale: the strays beside a draft a line writes, lenient once it landed.
	var residual, stale []github.Release
	moving := 0
	for _, lw := range w.Lines {
		lineRows, dec, ferr := bump.Fold(walkedBumpCommits(lw.Commits), cfg)
		if ferr != nil {
			return ferr
		}
		current, verr := currentVersion(ctx, o.current, lw.Base, lw.Line)
		if verr != nil {
			return verr
		}
		pv := packageRelease{Path: lw.Package.Path, Current: current.String(), Level: string(dec.Level), Commits: lineRows, Reason: noneReason(len(lineRows), lw.Source)}
		if dec.Level == bump.LevelNone && !cfg.Note.DraftOnNone {
			plan := draftplan.PlanDraft(lw.Line, dec.Level, "", false, drafted)
			pv.Action = string(plan.Action)
			residual = append(residual, staleReleases(plan.Stale)...)
			verdicts = append(verdicts, pv)
			continue
		}
		// tagName is what the kept (or created) draft converges to: the next
		// version on a release verdict, the Unreleased placeholder on a
		// maintained none (draft_on_none — the flag's whole point is that a
		// quiet merge keeps the door open instead of deleting the draft).
		tagName := draftplan.PlaceholderTagOn(lw.Line)
		if dec.Level != bump.LevelNone {
			tag, nerr := bump.NextOn(lw.Line, current, dec)
			if nerr != nil {
				return nerr
			}
			if gerr := draftplan.CheckPublishedFloor(lw.Line, tag, drafted); gerr != nil {
				return gerr
			}
			tagName = tag.TagOn(lw.Line.Prefix)
			pv.Next = tag.String()
			pv.Reason = bump.DecidingReason(lineRows, dec)
			moving++
		}
		sections, gerr := notes.Group(walkedNoteCommits(lw.Commits), cfg)
		if gerr != nil {
			return gerr
		}
		// The link closes the notes, BEFORE the footer: the machine region is
		// then notes' output for the same walk followed by the caller's block.
		// Each line's link starts at that line's own base tag — never the
		// union's merge base, never a sibling's tag (§4.1).
		body := compareLink(notes.Render(sections), owner, repoName, lw.BaseTag, target)
		if footer != "" {
			// One --- line between the notes and the caller's install block
			// (Q11) — composed here so a dry run previews the EXACT published
			// body and no caller ever concatenates markdown in shell.
			body = body + "\n---\n\n" + footer
		}
		// The plan is computed BEFORE the body is final, because the body
		// depends on it: an update must re-read the kept draft's current body
		// and carry the hand region across (see draftplan.ComposeBody) — the
		// rolling draft is the one place a human can write release prose, and
		// rewriting it blind is how that prose was silently destroyed on the
		// next push (t-qgps).
		plan := draftplan.PlanDraft(lw.Line, dec.Level, tagName, cfg.Note.DraftOnNone, drafted)
		body = draftplan.ComposeBody(plan.Keep, body)
		// Sized BEFORE the dry-run fork: a dry run previews the real run, and
		// a body the real run refuses must fail the preview identically. Sized
		// over the FINAL body, hand region and compare link included — a human
		// can write the draft over the limit, and the refusal must name it
		// before the API does.
		if serr := checkReleaseBody(body); serr != nil {
			return serr
		}
		pv.Tag, pv.Body, pv.Action = tagName, body, string(plan.Action)
		stale = append(stale, staleReleases(plan.Stale)...)
		drafts = append(drafts, lineDraft{at: len(verdicts), plan: plan, params: github.ReleaseParams{TagName: tagName, Target: target, Name: tagName, Body: body, Draft: true}})
		verdicts = append(verdicts, pv)
	}

	// The bare line's residue: with packages declared and no root package,
	// a bare vX.Y.Z (or Unreleased) draft is what the single line left behind
	// and nothing will ever converge it again. Claimed on the precedent of the
	// placeholder being claimed with the flag off. A tag that selected one
	// line still clears it: the residue belongs to no line, so no line's
	// selection protects it.
	// The residue line is the bare FREE line: a declared root-level vN
	// holds its own major's bare drafts, and they are that line's, not
	// residue (config.Config.LineOf on an undeclared root computes exactly
	// what is left over). Without packages there is no residue: the bare
	// line is the walk's one line, and its drafts were planned above.
	var residue []github.Release
	if len(cfg.Packages) > 0 && !slices.ContainsFunc(cfg.Packages, func(p config.Package) bool { return p.Path == "." }) {
		residue = staleReleases(draftplan.PlanDraft(cfg.LineOf(config.Package{Path: "."}), bump.LevelNone, "", false, drafted).Stale)
	}
	// Spoken once the residue's DELETE went, never at plan time: printed there
	// it told a dry run, and a run that died at an upsert with the residue
	// untouched, that the draft "is deleted" (t-xz1z).
	residueGone := func(s github.Release) {
		if slices.ContainsFunc(residue, func(r github.Release) bool { return r.ID == s.ID }) {
			noticef("the bare draft %s (release id %d) is the single line's residue — this repository declares packages and no root package, so no line would converge it again; a hand region it carried is gone with it", s.TagName, s.ID)
		}
	}
	// The residue is no line's, so its delete is the run's: a stray of the
	// upserts when any line writes a draft, the whole action when none does.
	if len(drafts) == 0 {
		residual = append(residual, residue...)
	} else {
		stale = append(stale, residue...)
	}

	result := newReleaseResult(cfg, w, rows, verdicts, target, len(drafts) > 0, moving)

	if o.dryRun {
		for _, d := range drafts {
			noticef("dry run: the upsert would %s the rolling draft %s at %s", d.plan.Action, d.params.TagName, target)
		}
		if len(residual) > 0 {
			noticef("dry run: %d residual draft(s) to delete (a delete that will not go fails the run)", len(residual))
		}
		if len(stale) > 0 {
			noticef("dry run: %d stale draft(s) to delete after the upserts", len(stale))
		}
		for _, r := range residue {
			noticef("dry run: the bare draft %s (release id %d) is the single line's residue — this repository declares packages and no root package, so no line will converge it again; it would be deleted, and a hand region it carried would go with it (move that prose into the line's own draft, above the marker, before a real run)", r.TagName, r.ID)
		}
		if o.json {
			printCompact(result)
			return releaseExit(moving, result.Reason, true)
		}
		if len(drafts) == 0 {
			// Nothing to print, and no zero-byte write: on macOS an empty
			// write to a pipe whose reader has gone is still a SIGPIPE.
			return releaseExit(moving, result.Reason, false)
		}
		blocks := make([]string, 0, len(drafts))
		for _, d := range drafts {
			blocks = append(blocks, d.params.TagName+"\n\n"+d.params.Body)
		}
		fmt.Fprint(out, strings.Join(blocks, "\n"))
		return releaseExit(moving, result.Reason, true)
	}

	// Every line's upsert lands before any draft is deleted: a failure here
	// leaves the lines already written standing and exits 4 — nothing was
	// destroyed, and the next run heals the rest.
	urls := make([]string, 0, len(drafts))
	for i, d := range drafts {
		var rel github.Release
		var werr error
		if d.plan.Keep != nil {
			rel, werr = gh.UpdateRelease(ctx, owner, repoName, d.plan.Keep.ID, d.params)
		} else {
			rel, werr = gh.CreateRelease(ctx, owner, repoName, d.params)
		}
		if werr != nil {
			if i > 0 {
				warnf("%d line(s) were written before this failure and stand; the remaining lines converge on the next run", i)
			}
			return werr
		}
		noticef("draft release %s %sd at %s (unpublished — the tag is created at that commit when a human publishes): %s", d.params.TagName, d.plan.Action, d.params.Target, rel.URL)
		urls = append(urls, rel.URL)
		verdicts[d.at].URL = rel.URL
	}
	// A none line's residual deletes are loud: the delete is that line's
	// entire action, and absorbing its failure would mean the line did
	// nothing and the run reported fine — a sibling's landed draft is no
	// write of this line's to be lenient about. Routed through convergeStray
	// whenever a sibling had written, the same line, verdict and failing
	// DELETE exited 0 or 4 on the siblings alone (t-xz1z). The failure is
	// answered only after every other delete was tried: one line's failed
	// action is no reason to leave another line's undone, nor the strays
	// standing unwarned, and returning on the spot stranded both (DESIGN
	// §4.1, the bare line included). The residuals still go first: they are
	// the verdict of the lines that fold to none and the strays are
	// bookkeeping, so a run an interrupt (never absorbed) cuts short has
	// spent itself on the verdict first.
	var residualErr error
	for _, s := range residual {
		gone, derr := gh.DeleteRelease(ctx, owner, repoName, s.ID)
		switch {
		case derr == nil:
			noticef("no release is due — %s the residual draft %s (release id %d)", discardedOrGone(gone), s.TagName, s.ID)
			residueGone(s)
		case core.IsInterrupted(derr):
			return derr
		case residualErr == nil:
			residualErr = derr
		default:
			warnf("the residual draft %s (release id %d) would not go either: %v — the run fails on the first that would not, and the next run tries every one again", s.TagName, s.ID, derr)
		}
	}
	for _, s := range stale {
		went, cerr := convergeStray(ctx, gh, owner, repoName, s)
		if cerr != nil {
			return cerr
		}
		if went {
			residueGone(s)
		}
	}
	if residualErr != nil {
		return residualErr
	}

	result = newReleaseResult(cfg, w, rows, verdicts, target, len(drafts) > 0, moving)
	if o.json {
		printCompact(result)
		return releaseExit(moving, result.Reason, true)
	}
	for _, u := range urls {
		fmt.Fprintln(out, u)
	}
	return releaseExit(moving, result.Reason, len(drafts) > 0)
}

// newReleaseResult is the one producer of the machine verdict, and where the
// render tells the bare line from the lines. Without [[packages]] the bare
// line's verdict fills the scalars — its reason unprefixed, its tag bare; a
// maintained none's reason is the none reason, as bump's and every packages
// line's is. With packages declared the scalars stay empty, packages[]
// carries one verdict per line, and the reason joins the lines', wrapped by
// everyLineNone when none moves. target is reported exactly when a draft is
// upserted (drafted), single line and packages alike: the gate is the draft
// count, never moving, because a draft_on_none placeholder is a draft that
// points at target too.
func newReleaseResult(cfg *config.Config, w sinceTagWalk, rows []bump.Row, verdicts []packageRelease, target string, drafted bool, moving int) releaseResult {
	res := releaseResult{Commits: rows, Pulls: w.Facts.Pulls}
	if drafted {
		res.Target = target
	}
	if len(cfg.Packages) == 0 {
		one := verdicts[0]
		res.Current, res.Level, res.Tag, res.Body, res.Action, res.URL, res.Reason = one.Current, one.Level, one.Tag, one.Body, one.Action, one.URL, one.Reason
		return res
	}
	reasons := make([]string, 0, len(verdicts))
	for _, v := range verdicts {
		reasons = append(reasons, v.Path+": "+v.Reason)
	}
	res.Packages = verdicts
	res.Reason = strings.Join(reasons, "; ")
	if moving == 0 {
		res.Reason = everyLineNone(res.Reason)
	}
	return res
}

// releaseExit is the exit once stdout is written: nil when a line moves, and
// otherwise the uniform soft no-release (1) — a maintained placeholder
// (draft_on_none) keeps a door open, it does not release — quiet when stdout
// carries the verdict (the JSON, or at least one draft's block or URL) and
// the envelope otherwise.
func releaseExit(moving int, reason string, verdictOnStdout bool) error {
	if moving > 0 {
		return nil
	}
	return core.NoReleasef("%s", reason).Quiet(verdictOnStdout)
}

// planInput adapts the releases listing for draftplan, which reads all of it:
// the drafts for what is glyph-managed (house-shaped drafts and the
// Unreleased placeholder) and the kept draft's body, the published releases
// for the floor.
func planInput(releases []github.Release) []draftplan.Draft {
	out := make([]draftplan.Draft, 0, len(releases))
	for _, r := range releases {
		out = append(out, draftplan.Draft{ID: r.ID, TagName: r.TagName, Draft: r.Draft, Body: r.Body})
	}
	return out
}

// staleReleases converts a plan's stale set back to the shape the delete
// calls and their notices read.
func staleReleases(ds []draftplan.Draft) []github.Release {
	out := make([]github.Release, 0, len(ds))
	for _, d := range ds {
		out = append(out, github.Release{ID: d.ID, TagName: d.TagName, Draft: d.Draft})
	}
	return out
}

// convergeStray deletes one stale glyph-managed draft AFTER the rolling draft
// has been written, and treats one that will not go as a WARNING rather than
// a failure: went reports that the DELETE went and its notice was printed,
// and the only error is an interrupt.
//
// Both halves are the same argument. The upsert used to delete first, so a
// delete that would not go spent the run's ONLY chance to land the notes:
// measured against a fake API answering every DELETE with 503, the run burned
// the whole 1s→4s→16s schedule, exited 4, and sent the PATCH zero times — the
// rolling draft never received the release it was computed for. t-yq7m absorbed
// one shape of this (a resent DELETE whose lost answer returns 404); the harm
// it named is general, and the order is the general fix. It also removes the
// mirror-image loss: with the delete first, a write that failed left the strays
// already gone, so the run destroyed state and landed nothing.
//
// The leniency is bounded by what already succeeded. The exit code of `release`
// answers one question — did the verdict land? — and after the write it did.
// What remains is convergence bookkeeping over a DRAFT: no tag exists, nothing
// is published, and no new draft is created while one exists, so the stray set
// is self-limiting and the same failure simply repeats next run (DESIGN §4:
// nothing is persisted, the upsert converges, and even a duplicate self-heals).
// Failing here instead would red the release job at release.yml's `status -ne 0`
// gate, before it reads the verdict — so a stray GitHub will not delete would
// stop the repository shipping artefacts while its notes were correct.
//
// An interrupt is never absorbed: internal/github classifies a canceled context
// as CodeInterrupted, so a Ctrl-C during convergence still leaves the process
// exiting 130 rather than 0 with a warning.
//
// A residual draft — one a none verdict leaves — deliberately does NOT take
// this path: there the delete is the line's entire action, so absorbing its
// failure would mean the run did nothing and reported fine, whatever that
// line's siblings wrote.
func convergeStray(ctx context.Context, gh *github.Client, owner, repo string, s github.Release) (went bool, err error) {
	gone, derr := gh.DeleteRelease(ctx, owner, repo, s.ID)
	if derr != nil {
		if core.IsInterrupted(derr) {
			return false, derr
		}
		warnf("the notes landed, but the stale draft %s (release id %d) would not go: %v — "+
			"it converges on the next release; do NOT publish it, a stale tag at a stale "+
			"target wedges the next release at the published floor", s.TagName, s.ID, derr)
		return false, nil
	}
	noticef("%s the stale draft %s (release id %d)", discardedOrGone(gone), s.TagName, s.ID)
	return true, nil
}

// discardedOrGone words a delete's notice for what actually happened. A delete
// whose retry found the release already absent reached its goal without glyph
// ever seeing its own request succeed, and 404 is also how GitHub answers for a
// repository the credential can no longer see — so on that path the notice says
// what was OBSERVED (it is not there) instead of claiming a deletion. It matters
// most on the none verdict, where no later write exists to contradict it.
func discardedOrGone(alreadyGone bool) string {
	if alreadyGone {
		return "found already gone (the retried delete was answered 404, so this is unconfirmed):"
	}
	return "discarded"
}

// readFooter reads the --footer-file when one is named. The path is the
// caller's input, so an unreadable file is usage (2), caught before any
// request goes out.
func readFooter(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path) // #nosec G304 -- the caller names their own footer file
	if err != nil {
		return "", core.Usagef("--footer-file %q cannot be read: %v", path, err)
	}
	return string(content), nil
}
