package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

var (
	notesRange    string
	notesPR       int
	notesSinceTag string
	notesRepo     string
	notesJSON     bool
)

// notesResult is the machine verdict: {sections, reason}. reason appears only
// on an empty verdict — it explains why nothing rendered.
type notesResult struct {
	Sections []notes.Section `json:"sections"`
	Reason   string          `json:"reason,omitempty"`
}

func newNotesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "Render section-grouped release notes from a range of commits",
		Long: "notes renders the release-notes body: every participating commit (bots,\n" +
			"merges, autosquash artifacts and raw git reverts are excluded) groups under\n" +
			"its gitmoji's section, breaking commits hoist into Breaking Changes, and\n" +
			"sectionless commits stay out — though a none-bump removal (:fire:/:coffin:/\n" +
			":truck:) still surfaces under Removals. --range reads a local git revision\n" +
			"range; --pr reads a pull request's INDIVIDUAL commits over the API, so a\n" +
			"squash-merge cannot collapse them into one line. stdout is the Markdown body\n" +
			"(pipe it into a release step); --json emits {sections,reason}. Nothing\n" +
			"release-worthy prints no body and exits 1 (soft no-release).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notesRun(cmd)
		},
	}
	cmd.Flags().StringVar(&notesRange, "range", "", "render notes for every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&notesPR, "pr", 0, "render notes from a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &notesSinceTag, "render notes from")
	cmd.Flags().StringVar(&notesRepo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY)")
	cmd.Flags().BoolVar(&notesJSON, "json", false, "emit the machine verdict {sections,reason}")
	markInputSourceFlags(cmd)
	return cmd
}

// notesInput reads the commits the notes are rendered from and names the source
// for the reason line — the notes twin of bumpInput, dispatching on whether a
// flag was set rather than on its value.
func notesInput(cmd *cobra.Command, table *gitmoji.Table) ([]parser.Commit, string, error) {
	ctx := cmd.Context()
	if cmd.Flags().Changed("pr") {
		return pullInput(ctx, notesPR, notesRepo)
	}
	if cmd.Flags().Changed("since-tag") {
		// The version base and the pull-expansion provenance the walk also
		// resolves are bump's and release's concerns, not notes'.
		commits, _, source, _, err := sinceTagInput(ctx, table, notesSinceTag, notesRepo)
		return commits, source, err
	}
	if err := checkRangeFlag(notesRange); err != nil {
		return nil, "", err
	}
	commits, err := participatingCommits(ctx, notesRange)
	return commits, notesRange, err
}

func notesRun(cmd *cobra.Command) error {
	if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
		return err
	}
	table, err := loadRules()
	if err != nil {
		return err
	}
	commits, source, perr := notesInput(cmd, table)
	if perr != nil {
		return perr
	}

	sections, nerr := notes.Group(commits, table)
	if nerr != nil {
		return nerr
	}

	if len(sections) == 0 {
		reason := fmt.Sprintf("no release notes: %d commit(s) participate in %s and none is release-worthy", len(commits), source)
		if notesJSON {
			printCompact(notesResult{Sections: []notes.Section{}, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}

	if notesJSON {
		printCompact(notesResult{Sections: sections})
		return nil
	}
	md, rerr := notes.Render(sections)
	if rerr != nil {
		return rerr
	}
	fmt.Fprint(out, md)
	return nil
}
