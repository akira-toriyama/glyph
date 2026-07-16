package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/spf13/cobra"
)

var (
	releaseSinceTag string
	releaseRepo     string
	releaseCurrent  string
	releaseDryRun   bool
	releaseJSON     bool
)

// releaseResult is the machine verdict: {current, level, tag, body, commits,
// reason}. tag and body are omitted on a none verdict — there is no release to
// act on.
type releaseResult struct {
	Current string       `json:"current"`
	Level   string       `json:"level"`
	Tag     string       `json:"tag,omitempty"`
	Body    string       `json:"body,omitempty"`
	Commits []bumpCommit `json:"commits"`
	Reason  string       `json:"reason"`
}

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Compose the next version and its release notes in one verdict",
		Long: "release runs the squash-safe walk ONCE and derives both the next version\n" +
			"and the release-notes body from that single commit set — calling bump and\n" +
			"notes separately walks twice, and a merge landing between the walks could\n" +
			"version one range and describe another. The walk defaults to the highest\n" +
			"v* tag (release has exactly one input source, so no bare --since-tag is\n" +
			"required). Bare release will upsert the rolling DRAFT release (no tag —\n" +
			"GitHub tags on manual Publish); until that write path lands it fails\n" +
			"loud. --dry-run computes everything and writes nothing: stdout is the\n" +
			"tag line, a blank line, then the Markdown body; --json emits\n" +
			"{current,level,tag,body,commits,reason}. A none verdict prints no\n" +
			"release and exits 1 (soft no-release).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return releaseRun(cmd)
		},
	}
	addSinceTagFlag(cmd, &releaseSinceTag, "compose the release from")
	cmd.Flags().StringVar(&releaseRepo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&releaseCurrent, "current", "", "the version to step from (default: the walked tag, else the highest parseable v* tag)")
	cmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "compute the full verdict but write nothing to GitHub")
	cmd.Flags().BoolVar(&releaseJSON, "json", false, "emit the machine verdict {current,level,tag,body,commits,reason}")
	return cmd
}

func releaseRun(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if !releaseDryRun {
		// The ratified surface: bare release upserts the rolling draft. Failing
		// loud here (instead of shipping bare as a read-only preview) keeps the
		// bare form from silently changing sides when the write path lands.
		return core.Usagef("the rolling-draft upsert is not implemented yet — run `glyph release --dry-run` for the computed verdict")
	}
	table, err := loadRules()
	if err != nil {
		return err
	}
	tagFlag := releaseSinceTag
	if !cmd.Flags().Changed("since-tag") {
		tagFlag = sinceTagAuto
	}
	parsed, source, base, perr := sinceTagInput(ctx, table, tagFlag, releaseRepo)
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

	if level == gitmoji.BumpNone {
		reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), source)
		if releaseJSON {
			printCompact(releaseResult{Current: current.String(), Level: string(level), Commits: commits, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}

	sections, gerr := notes.Group(parsed, table)
	if gerr != nil {
		return gerr
	}
	body, rerr := notes.Render(sections)
	if rerr != nil {
		return rerr
	}

	tag := current.Next(level).String()
	if releaseJSON {
		printCompact(releaseResult{
			Current: current.String(),
			Level:   string(level),
			Tag:     tag,
			Body:    body,
			Commits: commits,
			Reason:  decidingReason(commits, level),
		})
		return nil
	}
	fmt.Fprintf(out, "%s\n\n%s", tag, body)
	return nil
}
