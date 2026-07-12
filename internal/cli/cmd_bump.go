package cli

import (
	"context"
	"fmt"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

var (
	bumpRange   string
	bumpCurrent string
	bumpJSON    bool
)

// bumpCommit is one classified commit in the machine verdict.
type bumpCommit struct {
	SHA      string `json:"sha"`
	Gitmoji  string `json:"gitmoji"`
	Level    string `json:"level"`
	Breaking bool   `json:"breaking"`
	Subject  string `json:"subject"`
}

// bumpResult is the machine verdict: {current, level, next, commits, reason}.
// next is omitted on a none verdict — there is no next version to act on.
type bumpResult struct {
	Current string       `json:"current"`
	Level   string       `json:"level"`
	Next    string       `json:"next,omitempty"`
	Commits []bumpCommit `json:"commits"`
	Reason  string       `json:"reason"`
}

func newBumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bump",
		Short: "Compute the next version from a range of commits",
		Long: "bump classifies every participating commit in a range (bots, merges,\n" +
			"autosquash artifacts and raw git reverts are excluded), folds the levels\n" +
			"with max — so order can never change the verdict — and steps the current\n" +
			"version. stdout is the bare next version (pipe it into a tag step);\n" +
			"--json emits {current,level,next,commits,reason}. A none verdict prints\n" +
			"no version and exits 1 (soft no-release). The squash-safe inputs\n" +
			"(--pr, --since-tag) arrive with the GitHub plumbing phase.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return bumpRun(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&bumpRange, "range", "", "classify every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().StringVar(&bumpCurrent, "current", "", "the version to step from (default: highest parseable v* tag, else v0.0.0)")
	cmd.Flags().BoolVar(&bumpJSON, "json", false, "emit the machine verdict {current,level,next,commits,reason}")
	cmd.MarkFlagsOneRequired("range")
	return cmd
}

func bumpRun(ctx context.Context) error {
	if err := checkRangeFlag(bumpRange); err != nil {
		return err
	}
	table, err := loadRules()
	if err != nil {
		return err
	}
	raws, gerr := gitsource.Log(ctx, ".", bumpRange)
	if gerr != nil {
		return gerr
	}

	commits := []bumpCommit{} // non-nil: serializes as [] so consumers can index
	levels := make([]gitmoji.Bump, 0, len(raws))
	for _, raw := range raws {
		if _, excluded := bump.Excluded(raw.Author, firstLine(raw.Message), raw.Parents); excluded {
			continue
		}
		c, perr := parser.Parse(raw.Message)
		if perr != nil {
			return core.Lintf("commit %.7s: %v", raw.SHA, perr)
		}
		c.SHA, c.Author = raw.SHA, raw.Author
		level, cerr := bump.Classify(c, table)
		if cerr != nil {
			return cerr
		}
		levels = append(levels, level)
		commits = append(commits, bumpCommit{
			SHA:      raw.SHA,
			Gitmoji:  c.Gitmoji,
			Level:    string(level),
			Breaking: c.Breaking,
			Subject:  c.Subject,
		})
	}

	level := bump.Reduce(levels)
	current, verr := currentVersion(ctx, bumpCurrent)
	if verr != nil {
		return verr
	}

	if level == gitmoji.BumpNone {
		reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), bumpRange)
		if bumpJSON {
			printCompact(bumpResult{Current: current.String(), Level: string(level), Commits: commits, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}

	next := current.Next(level)
	reason := decidingReason(commits, level)
	if bumpJSON {
		printCompact(bumpResult{
			Current: current.String(),
			Level:   string(level),
			Next:    next.String(),
			Commits: commits,
			Reason:  reason,
		})
		return nil
	}
	fmt.Fprintln(out, next.String())
	return nil
}

// currentVersion resolves the version to step from: an explicit --current
// (malformed ⇒ usage — it is the caller's input), else the highest parseable
// v* tag, else v0.0.0 for a repo before its first release.
func currentVersion(ctx context.Context, flag string) (bump.Version, error) {
	if flag != "" {
		v, err := bump.ParseVersion(flag)
		if err != nil {
			return bump.Version{}, core.Usagef("--current: %v", err)
		}
		return v, nil
	}
	tags, err := gitsource.Tags(ctx, ".")
	if err != nil {
		return bump.Version{}, err
	}
	for _, t := range tags {
		if v, perr := bump.ParseVersion(t); perr == nil {
			return v, nil
		}
	}
	return bump.Version{}, nil
}

// decidingReason names the oldest commit that reaches the folded level — the
// one-line answer to "why this bump".
func decidingReason(commits []bumpCommit, level gitmoji.Bump) string {
	for _, c := range commits {
		if c.Level == string(level) {
			breaking := ""
			if c.Breaking {
				breaking = " (breaking)"
			}
			return fmt.Sprintf("%.7s %s %q%s → %s", c.SHA, c.Gitmoji, c.Subject, breaking, level)
		}
	}
	return fmt.Sprintf("level %s", level)
}
