package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/spf13/cobra"
)

var (
	releaseSinceTag   string
	releaseRepo       string
	releaseCurrent    string
	releaseTarget     string
	releaseFooterFile string
	releaseDryRun     bool
	releaseJSON       bool
)

// releaseResult is the machine verdict: {current, level, tag, target, body,
// action, url, commits, pulls, reason}. tag, target and body are omitted on a
// none verdict — there is no release to act on; url is present only when a
// write actually happened (never on a dry run). pulls is the walk's expansion
// provenance — which merged pulls it resolved and how many participating
// commits each contributed — which is what a human or a CI step reads to
// audit how a verdict was assembled.
type releaseResult struct {
	Current string          `json:"current"`
	Level   string          `json:"level"`
	Tag     string          `json:"tag,omitempty"`
	Target  string          `json:"target,omitempty"`
	Body    string          `json:"body,omitempty"`
	Action  string          `json:"action"`
	URL     string          `json:"url,omitempty"`
	Commits []bumpCommit    `json:"commits"`
	Pulls   []pullExpansion `json:"pulls"`
	Reason  string          `json:"reason"`
}

// The draft-convergence actions a release run can take (Q4): what the rolling
// draft needs to match the verdict — create it, update (grow/retag) it,
// delete residual drafts on a none verdict, or nothing at all.
const (
	actionCreate = "create"
	actionUpdate = "update"
	actionDelete = "delete"
	actionNone   = "none"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Upsert the rolling DRAFT release from one composed verdict",
		Long: "release runs the squash-safe walk ONCE and derives both the next version\n" +
			"and the release-notes body from that single commit set — calling bump and\n" +
			"notes separately walks twice, and a merge landing between the walks could\n" +
			"version one range and describe another. The walk defaults to the highest\n" +
			"v* tag (release has exactly one input source, so no bare --since-tag is\n" +
			"required). Bare release upserts the rolling DRAFT release: the one\n" +
			"glyph-managed draft is created or updated (tag, notes body, target sha)\n" +
			"FIRST, and stale drafts are then removed by release id; one that will\n" +
			"not go leaves a warning rather than failing the release, because the\n" +
			"notes have already landed and the next run converges it — no tag is\n" +
			"created; GitHub\n" +
			"tags the target commit when a human publishes. On a none verdict any\n" +
			"residual glyph-managed draft is deleted (the draft state converges to\n" +
			"the verdict) and the run exits 1 (soft no-release). A walk that could\n" +
			"NOT read its range (a wrong --repo, a merged pull whose merge point\n" +
			"nothing resolved, a commit GitHub has not indexed, a truncated pull\n" +
			"listing, a shallow checkout) exits 4 before touching anything — an\n" +
			"empty fold from a walk that could not look is not evidence that\n" +
			"nothing shipped, so no verdict is handed down on it at all.\n" +
			"A real run prints the draft's URL; --dry-run computes everything\n" +
			"including that action and writes nothing, printing the tag line, a\n" +
			"blank line, then the Markdown body. --json emits\n" +
			"{current,level,tag,body,action,url,commits,pulls,reason} — pulls is the\n" +
			"walk's expansion provenance (each resolved pull and its participating\n" +
			"commit count), which is how a verdict can be audited after the fact.",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return releaseRun(cmd)
		},
	}
	addSinceTagFlag(cmd, &releaseSinceTag, "compose the release from")
	cmd.Flags().StringVar(&releaseRepo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&releaseCurrent, "current", "", currentFlagUsage)
	cmd.Flags().StringVar(&releaseTarget, "target", "", "the commit sha the draft's eventual tag points at (default: the checkout's HEAD)")
	cmd.Flags().StringVar(&releaseFooterFile, "footer-file", "", "a Markdown file appended verbatim after the notes, separated by one --- line (the per-repo install block)")
	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "compute the full verdict and the draft action but write nothing to GitHub")
	cmd.Flags().BoolVar(&releaseJSON, "json", false, "emit the machine verdict {current,level,tag,body,action,url,commits,pulls,reason}")
	return cmd
}

func releaseRun(cmd *cobra.Command) error {
	// release is where a silently-ignored empty flag costs most: --current
	// decides the tag, --target decides the commit that tag will point at, and
	// both defaults look plausible enough to publish.
	if err := checkNamingFlags(cmd, [][3]string{
		{"current", "version", currentHint},
		{"repo", "repository", repoHint},
		{"target", "commit", "omit --target to point the draft at this checkout's HEAD, or name a commit with --target=SHA"},
		{"footer-file", "path", "omit --footer-file to publish the notes alone, or name the file with --footer-file=PATH"},
	}); err != nil {
		return err
	}
	ctx := cmd.Context()
	table, err := loadRules()
	if err != nil {
		return err
	}
	// Caller-input problems surface before any network or git work: the footer
	// file is read up front, and the repository must resolve for the releases
	// listing (sinceTagInput re-resolves it for the walk — same answer, one
	// source of truth in resolveRepo).
	footer, ferr := readFooter(releaseFooterFile)
	if ferr != nil {
		return ferr
	}
	owner, repoName, oerr := resolveRepo(releaseRepo)
	if oerr != nil {
		return oerr
	}

	tagFlag := releaseSinceTag
	if !cmd.Flags().Changed("since-tag") {
		tagFlag = sinceTagAuto
	}
	parsed, facts, source, base, perr := sinceTagInput(ctx, table, tagFlag, releaseRepo)
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
	// (the wedge, the covered pull, checkPublishedFloor). Ratified t-pysg,
	// replacing #66's warn-and-refuse-to-destroy: that compromise existed for
	// the repository whose merge button an automation presses, which then
	// warned on every release structurally — but merge points resolve now
	// (t-7zt7), so what remains of that shape is a bot-authored merge commit,
	// and a walk blind to a whole pull is exactly what this exit is for. The
	// escape is the wedge escape: cut a tag at or past what the walk cannot
	// read, or fix the checkout (fetch-depth: 0) when the shortfall names it.
	if !facts.complete() {
		return core.APIf("this walk did not read %s (%s) — refusing to hand down a verdict computed from a range it could not read; re-run the release once the walk can read the range",
			source, facts.shortfall(owner, repoName))
	}

	commits, level, cerr := classifyVerdict(parsed, table)
	if cerr != nil {
		return cerr
	}
	current, verr := currentVersion(ctx, releaseCurrent, base)
	if verr != nil {
		return verr
	}

	// The releases listing feeds both halves of the convergence decision: the
	// glyph-managed drafts to upsert or clear, and the published floor the
	// next version must clear. The dry run performs this read too — Q4: only
	// the writes are skipped.
	gh := newGitHub()
	releases, lerr := gh.Releases(ctx, owner, repoName)
	if lerr != nil {
		return lerr
	}
	drafts := glyphDrafts(releases)

	if level == gitmoji.BumpNone {
		return releaseNone(ctx, gh, owner, repoName, current, commits, facts, source, drafts)
	}

	tag := current.Next(level)
	if gerr := checkPublishedFloor(tag, releases); gerr != nil {
		return gerr
	}

	sections, gerr := notes.Group(parsed, table)
	if gerr != nil {
		return gerr
	}
	body, rerr := notes.Render(sections)
	if rerr != nil {
		return rerr
	}
	if footer != "" {
		// One --- line between the notes and the caller's install block (Q11)
		// — composed here so a dry run previews the EXACT published body and
		// no caller ever concatenates markdown in shell.
		body = body + "\n---\n\n" + footer
	}

	// The target resolves BEFORE the dry-run fork — Q4 again: only the writes
	// are skipped. This used to sit below it, which made `--dry-run --target=X`
	// byte-identical to `--dry-run` for every X: the flag naming which commit
	// the eventual tag points at was the one flag the preview silently ignored,
	// so a typo surfaced only on the real run (t-nfz3).
	target := releaseTarget
	if target == "" {
		var herr error
		if target, herr = gitsource.Head(ctx, "."); herr != nil {
			return herr
		}
	}

	keep, stale := planDrafts(drafts, tag.String())
	action := actionCreate
	if keep != nil {
		action = actionUpdate
	}
	result := releaseResult{
		Current: current.String(),
		Level:   string(level),
		Tag:     tag.String(),
		Target:  target,
		Body:    body,
		Action:  action,
		Commits: commits,
		Pulls:   facts.Pulls,
		Reason:  decidingReason(commits, level),
	}

	if releaseDryRun {
		noticef("dry run: the upsert would %s the rolling draft %s at %s (%d stale draft(s) to delete)", action, tag, target, len(stale))
		if releaseJSON {
			printCompact(result)
			return nil
		}
		fmt.Fprintf(out, "%s\n\n%s", tag, body)
		return nil
	}
	params := github.ReleaseParams{
		TagName: tag.String(),
		Target:  target,
		Name:    tag.String(),
		Body:    body,
		Draft:   true,
	}
	var rel github.Release
	var werr error
	if keep != nil {
		rel, werr = gh.UpdateRelease(ctx, owner, repoName, keep.ID, params)
	} else {
		rel, werr = gh.CreateRelease(ctx, owner, repoName, params)
	}
	if werr != nil {
		return werr
	}
	noticef("draft release %s %sd (unpublished — the tag is created when a human publishes): %s", tag, action, rel.URL)

	if cerr := convergeStrays(ctx, gh, owner, repoName, stale); cerr != nil {
		return cerr
	}

	result.URL = rel.URL
	if releaseJSON {
		printCompact(result)
		return nil
	}
	fmt.Fprintln(out, rel.URL)
	return nil
}

// convergeStrays deletes the stale glyph-managed drafts AFTER the rolling draft
// has been written, and treats one that will not go as a WARNING rather than a
// failure.
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
// releaseNone deliberately does NOT use this: on a none verdict the delete is
// the entire action, so absorbing its failure would mean the run did nothing
// and reported fine.
func convergeStrays(ctx context.Context, gh *github.Client, owner, repo string, stale []github.Release) error {
	for _, s := range stale {
		gone, derr := gh.DeleteRelease(ctx, owner, repo, s.ID)
		if derr != nil {
			if core.IsInterrupted(derr) {
				return derr
			}
			warnf("the notes landed, but the stale draft %s (release id %d) would not go: %v — "+
				"it converges on the next release; do NOT publish it, a stale tag at a stale "+
				"target wedges the next release at the published floor", s.TagName, s.ID, derr)
			continue
		}
		noticef("%s the stale draft %s (release id %d)", discardedOrGone(gone), s.TagName, s.ID)
	}
	return nil
}

// releaseNone finishes a none verdict: the draft state converges to "no
// release should exist" (Q3 — residual glyph-managed drafts are deleted, by
// id), and the exit stays the uniform soft no-release (1).
//
// Convergence is on the VERDICT, and by the time this runs the verdict is one
// the walk is entitled to: releaseRun already failed loud (4) on any walk that
// did not read its range, so an empty fold here is a range that genuinely
// holds nothing — never a reading glyph distrusts.
func releaseNone(ctx context.Context, gh *github.Client, owner, repo string, current bump.Version, commits []bumpCommit, facts walkFacts, source string, drafts []github.Release) error {
	action := actionNone
	if len(drafts) > 0 {
		action = actionDelete
	}
	switch {
	case !releaseDryRun:
		for _, d := range drafts {
			gone, derr := gh.DeleteRelease(ctx, owner, repo, d.ID)
			if derr != nil {
				return derr
			}
			noticef("no release is due — %s the residual draft %s (release id %d)", discardedOrGone(gone), d.TagName, d.ID)
		}
	case len(drafts) > 0:
		noticef("dry run: no release is due — the upsert would delete %d residual draft(s)", len(drafts))
	}
	reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), source)
	if releaseJSON {
		printCompact(releaseResult{Current: current.String(), Level: string(gitmoji.BumpNone), Action: action, Commits: commits, Pulls: facts.Pulls, Reason: reason})
		return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
	}
	return core.NoReleasef("%s", reason)
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

// glyphDrafts filters the releases down to the ones glyph manages: unpublished
// drafts whose intended tag is the house version shape (vX.Y.Z, with the v).
// Everything else — published releases, and a human's hand-made drafts under
// other names — is not glyph's to touch, ever.
func glyphDrafts(releases []github.Release) []github.Release {
	var drafts []github.Release
	for _, r := range releases {
		if !r.Draft || !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		if _, err := bump.ParseVersion(r.TagName); err != nil {
			continue
		}
		drafts = append(drafts, r)
	}
	return drafts
}

// planDrafts converges the glyph-managed drafts on exactly one: keep the
// draft already carrying the intended tag when there is one (else the first
// listed — GitHub lists newest first), and mark every other one stale. The
// kept draft is UPDATED to the next tag rather than replaced — ratified:
// never a second draft.
func planDrafts(drafts []github.Release, tag string) (keep *github.Release, stale []github.Release) {
	for i := range drafts {
		if keep == nil && drafts[i].TagName == tag {
			keep = &drafts[i]
		}
	}
	if keep == nil && len(drafts) > 0 {
		keep = &drafts[0]
	}
	for i := range drafts {
		if keep == nil || drafts[i].ID != keep.ID {
			stale = append(stale, drafts[i])
		}
	}
	return keep, stale
}

// checkPublishedFloor is the deadlock guard (immutable releases): the next
// version must be STRICTLY greater than the latest published release, or the
// draft could never be published — its tag is taken, or permanently burned if
// a published release was deleted. The repository's state is off, so this
// fails loud (4) instead of creating an unpublishable draft.
func checkPublishedFloor(next bump.Version, releases []github.Release) error {
	floor, ok := highestPublished(releases)
	if !ok || next.Compare(floor) > 0 {
		return nil
	}
	return core.APIf(
		"computed version %s is not greater than the latest published release %s — refusing the draft (it would collide with or regress below a published release; if a published release was deleted its tag is permanently burned — bump past it)",
		next, floor)
}

// highestPublished returns the highest published (non-draft) house-shaped
// version among the releases; ok is false when there is none — a repository
// before its first publish has no floor.
func highestPublished(releases []github.Release) (bump.Version, bool) {
	var floor bump.Version
	found := false
	for _, r := range releases {
		if r.Draft || !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		v, err := bump.ParseVersion(r.TagName)
		if err != nil {
			continue
		}
		if !found || v.Compare(floor) > 0 {
			floor, found = v, true
		}
	}
	return floor, found
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
