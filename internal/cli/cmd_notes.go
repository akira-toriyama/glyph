package cli

import (
	"context"
	"fmt"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/spf13/cobra"
)

var (
	notesRange string
	notesJSON  bool
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
		Long: "notes renders the release-notes body for a range: every participating\n" +
			"commit (bots, merges, autosquash artifacts and raw git reverts are\n" +
			"excluded) groups under its gitmoji's section, breaking commits hoist\n" +
			"into Breaking Changes, and none-bump commits stay out. stdout is the\n" +
			"Markdown body (pipe it into a release step); --json emits\n" +
			"{sections,reason}. A range with nothing release-worthy prints no body\n" +
			"and exits 1 (soft no-release). The squash-safe inputs (--pr,\n" +
			"--since-tag) arrive with the GitHub plumbing phase.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notesRun(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&notesRange, "range", "", "render notes for every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().BoolVar(&notesJSON, "json", false, "emit the machine verdict {sections,reason}")
	cmd.MarkFlagsOneRequired("range")
	return cmd
}

func notesRun(ctx context.Context) error {
	if err := checkRangeFlag(notesRange); err != nil {
		return err
	}
	table, err := loadRules()
	if err != nil {
		return err
	}
	commits, perr := participatingCommits(ctx, notesRange)
	if perr != nil {
		return perr
	}

	sections, nerr := notes.Group(commits, table)
	if nerr != nil {
		return nerr
	}

	if len(sections) == 0 {
		reason := fmt.Sprintf("no release notes: %d commit(s) participate in %s and none is release-worthy", len(commits), notesRange)
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
