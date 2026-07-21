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

// releaseResult is the machine verdict: {current, level, tag, body, action,
// url, commits, pulls, reason}. tag and body are omitted on a none verdict —
// there is no release to act on; url is present only when a write actually
// happened (never on a dry run). pulls is the walk's expansion provenance —
// which merged pulls it resolved and how many participating commits each
// contributed — what the shadow comparison (Q6) branches on.
type releaseResult struct {
	Current string          `json:"current"`
	Level   string          `json:"level"`
	Tag     string          `json:"tag,omitempty"`
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
			"and stale drafts are removed by release id — no tag is created; GitHub\n" +
			"tags the target commit when a human publishes. On a none verdict any\n" +
			"residual glyph-managed draft is deleted (the draft state converges to\n" +
			"the verdict) and the run exits 1 (soft no-release). --dry-run computes\n" +
			"everything including that action and writes nothing: stdout is the tag\n" +
			"line, a blank line, then the Markdown body; --json emits\n" +
			"{current,level,tag,body,action,url,commits,pulls,reason} — pulls is the\n" +
			"walk's expansion provenance (each resolved pull and its participating\n" +
			"commit count), what a shadow comparison against a squash-subject reader\n" +
			"branches on.",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return releaseRun(cmd)
		},
	}
	addSinceTagFlag(cmd, &releaseSinceTag, "compose the release from")
	cmd.Flags().StringVar(&releaseRepo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&releaseCurrent, "current", "", "the version to step from (default: the walked tag, else the highest parseable v* tag)")
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
	parsed, pulls, source, base, perr := sinceTagInput(ctx, table, tagFlag, releaseRepo)
	if perr != nil {
		return perr
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
		return releaseNone(ctx, gh, owner, repoName, current, commits, pulls, source, drafts)
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

	keep, stale := planDrafts(drafts, tag.String())
	action := actionCreate
	if keep != nil {
		action = actionUpdate
	}
	result := releaseResult{
		Current: current.String(),
		Level:   string(level),
		Tag:     tag.String(),
		Body:    body,
		Action:  action,
		Commits: commits,
		Pulls:   pulls,
		Reason:  decidingReason(commits, level),
	}

	if releaseDryRun {
		noticef("dry run: the upsert would %s the rolling draft %s (%d stale draft(s) to delete)", action, tag, len(stale))
		if releaseJSON {
			printCompact(result)
			return nil
		}
		fmt.Fprintf(out, "%s\n\n%s", tag, body)
		return nil
	}

	target := releaseTarget
	if target == "" {
		var herr error
		if target, herr = gitsource.Head(ctx, "."); herr != nil {
			return herr
		}
	}
	for _, s := range stale {
		if derr := gh.DeleteRelease(ctx, owner, repoName, s.ID); derr != nil {
			return derr
		}
		noticef("discarded the stale draft %s (release id %d)", s.TagName, s.ID)
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

	result.URL = rel.URL
	if releaseJSON {
		printCompact(result)
		return nil
	}
	fmt.Fprintln(out, rel.URL)
	return nil
}

// releaseNone finishes a none verdict: the draft state converges to "no
// release should exist" (Q3 — residual glyph-managed drafts are deleted, by
// id), and the exit stays the uniform soft no-release (1).
func releaseNone(ctx context.Context, gh *github.Client, owner, repo string, current bump.Version, commits []bumpCommit, pulls []pullExpansion, source string, drafts []github.Release) error {
	action := actionNone
	if len(drafts) > 0 {
		action = actionDelete
	}
	if !releaseDryRun {
		for _, d := range drafts {
			if derr := gh.DeleteRelease(ctx, owner, repo, d.ID); derr != nil {
				return derr
			}
			noticef("no release is due — deleted the residual draft %s (release id %d)", d.TagName, d.ID)
		}
	} else if len(drafts) > 0 {
		noticef("dry run: no release is due — the upsert would delete %d residual draft(s)", len(drafts))
	}
	reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), source)
	if releaseJSON {
		printCompact(releaseResult{Current: current.String(), Level: string(gitmoji.BumpNone), Action: action, Commits: commits, Pulls: pulls, Reason: reason})
		return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
	}
	return core.NoReleasef("%s", reason)
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
