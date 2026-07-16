package cli

import (
	"context"
	"fmt"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

var (
	bumpRange    string
	bumpPR       int
	bumpSinceTag string
	bumpRepo     string
	bumpCurrent  string
	bumpJSON     bool
)

// bumpCommit is one classified commit in the machine verdict. The gitmoji code
// serializes as "code" — the same key the rules table (rules.json,
// `glyph rules --json`) and notes use for the identical token, so every glyph
// JSON surface names it one way.
type bumpCommit struct {
	SHA      string `json:"sha"`
	Code     string `json:"code"`
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
		Long: "bump classifies every participating commit (bots, merges, autosquash\n" +
			"artifacts and raw git reverts are excluded), folds the levels with max — so\n" +
			"order can never change the verdict — and steps the current version.\n" +
			"--range reads a local git revision range; --pr reads a pull request's\n" +
			"INDIVIDUAL commits over the API, which is what makes the verdict\n" +
			"squash-safe: a squash-merge rewrites the subject to the PR title and would\n" +
			"otherwise erase every per-commit type. stdout is the bare next version\n" +
			"(pipe it into a tag step); --json emits {current,level,next,commits,reason}.\n" +
			"A none verdict prints no version and exits 1 (soft no-release).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return bumpRun(cmd)
		},
	}
	cmd.Flags().StringVar(&bumpRange, "range", "", "classify every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&bumpPR, "pr", 0, "classify a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &bumpSinceTag, "classify")
	cmd.Flags().StringVar(&bumpRepo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&bumpCurrent, "current", "", "the version to step from (default: highest parseable v* tag, else v0.0.0)")
	cmd.Flags().BoolVar(&bumpJSON, "json", false, "emit the machine verdict {current,level,next,commits,reason}")
	markInputSourceFlags(cmd)
	return cmd
}

func bumpRun(cmd *cobra.Command) error {
	ctx := cmd.Context()
	table, err := loadRules()
	if err != nil {
		return err
	}
	parsed, source, base, perr := bumpInput(cmd, table)
	if perr != nil {
		return perr
	}

	commits := []bumpCommit{} // non-nil: serializes as [] so consumers can index
	levels := make([]gitmoji.Bump, 0, len(parsed))
	for _, c := range parsed {
		level, cerr := bump.Classify(c, table)
		if cerr != nil {
			return cerr
		}
		levels = append(levels, level)
		commits = append(commits, bumpCommit{
			SHA:      c.SHA,
			Code:     c.Gitmoji,
			Level:    string(level),
			Breaking: c.Breaking,
			Subject:  c.Subject,
		})
	}

	level := bump.Reduce(levels)
	current, verr := currentVersion(ctx, bumpCurrent, base)
	if verr != nil {
		return verr
	}

	if level == gitmoji.BumpNone {
		reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), source)
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

// bumpInput reads the commits the verdict is computed from, names the source
// for the reason line — a local revision range, a pull request's individual
// (pre-squash) commits over the API, or the release walk since a tag — and,
// when the source itself names a version (--since-tag), the base the bump
// steps from. It dispatches on whether a flag was set, not on its value — so
// an explicit --pr 0 (what a workflow yields from a null PR number) reaches
// the --pr guard and is diagnosed as a bad --pr, not misrouted into a --range
// complaint.
func bumpInput(cmd *cobra.Command, table *gitmoji.Table) ([]parser.Commit, string, *bump.Version, error) {
	ctx := cmd.Context()
	if cmd.Flags().Changed("pr") {
		commits, source, err := pullInput(ctx, bumpPR, bumpRepo)
		return commits, source, nil, err
	}
	if cmd.Flags().Changed("since-tag") {
		return sinceTagInput(ctx, table, bumpSinceTag, bumpRepo)
	}
	if err := checkRangeFlag(bumpRange); err != nil {
		return nil, "", nil, err
	}
	commits, err := participatingCommits(ctx, bumpRange)
	return commits, bumpRange, nil, err
}

// currentVersion resolves the version to step from: an explicit --current
// (malformed ⇒ usage — it is the caller's input) wins; else the base the input
// source itself named (--since-tag's tag — the walk base and the step base
// must be the SAME tag); else the highest parseable v* tag, which is v0.0.0
// for a repo before its first release.
func currentVersion(ctx context.Context, flag string, base *bump.Version) (bump.Version, error) {
	if flag != "" {
		v, err := bump.ParseVersion(flag)
		if err != nil {
			return bump.Version{}, core.Usagef("--current: %v", err)
		}
		return v, nil
	}
	if base != nil {
		return *base, nil
	}
	_, v, err := latestVersionTag(ctx)
	return v, err
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
			return fmt.Sprintf("%.7s %s %q%s → %s", c.SHA, c.Code, c.Subject, breaking, level)
		}
	}
	return fmt.Sprintf("level %s", level)
}
