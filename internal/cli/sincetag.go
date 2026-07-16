package cli

import (
	"context"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/parser"
	"github.com/spf13/cobra"
)

// This file is the release-time walk — the third input source beside --range
// (pure git) and --pr (pure API), combining both: it walks main's squash
// commits since a tag out of LOCAL git, resolves each to the pull request that
// merged it over the API, and expands that PR into its individual (pre-squash)
// commits. Nothing is persisted — the walk recomputes from git every run, so a
// release is idempotent and self-healing (DESIGN §4).

// sinceTagAuto is what a bare --since-tag parses to (the flag's NoOptDefVal):
// resolve the walk base from the repository itself — the highest parseable v*
// tag — so a release job never duplicates glyph's version-tag policy in shell.
const sinceTagAuto = "auto"

// addSinceTagFlag wires --since-tag onto a command. The value is optional
// (pflag's NoOptDefVal grammar): a bare --since-tag resolves the tag itself; a
// named tag must be attached with = (--since-tag=v1.2.3).
func addSinceTagFlag(cmd *cobra.Command, target *string, verb string) {
	cmd.Flags().StringVar(target, "since-tag", "",
		verb+" every merged PR's individual (pre-squash) commits on main since a tag (bare --since-tag: the highest v* tag; use --since-tag=TAG to name one)")
	cmd.Flags().Lookup("since-tag").NoOptDefVal = sinceTagAuto
}

// sinceTagInput resolves the repository and the walk range, then walks. bump
// and notes share it; the returned source names the range for the reason line.
func sinceTagInput(ctx context.Context, table *gitmoji.Table, tagFlag, repoFlag string) ([]parser.Commit, string, error) {
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return nil, "", err
	}
	revRange, err := sinceTagRange(ctx, tagFlag)
	if err != nil {
		return nil, "", err
	}
	commits, err := walkSince(ctx, newGitHub(), table, owner, repo, revRange)
	return commits, revRange, err
}

// sinceTagRange turns the --since-tag value into a git revision range: an
// explicit tag walks tag..HEAD; auto finds the highest parseable v* tag — the
// last published version, the same tag currentVersion steps from — and a
// repository before its first release (no version tag yet) walks the whole
// history.
func sinceTagRange(ctx context.Context, tagFlag string) (string, error) {
	if tag := strings.TrimSpace(tagFlag); tag != "" && tag != sinceTagAuto {
		return tag + "..HEAD", nil
	}
	tag, _, ok, err := latestVersionTag(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "HEAD", nil
	}
	return tag + "..HEAD", nil
}

// latestVersionTag returns the highest parseable version tag and its parsed
// version, or ok=false for a repository before its first release. One resolver
// serves the walk base and the bump base (currentVersion), so the two can
// never disagree on what "the last published version" means.
func latestVersionTag(ctx context.Context) (tag string, v bump.Version, ok bool, err error) {
	tags, terr := gitsource.Tags(ctx, ".")
	if terr != nil {
		return "", bump.Version{}, false, terr
	}
	for _, t := range tags {
		if pv, perr := bump.ParseVersion(t); perr == nil {
			return t, pv, true, nil
		}
	}
	return "", bump.Version{}, false, nil
}

// walkSince walks the range's commits oldest first and folds every merged PR's
// individual commits into one participating list. Excluded commits (bots,
// merges, generated subjects) are skipped before any API call — the routine
// fleet-sync direct push never costs a request.
func walkSince(ctx context.Context, c *github.Client, table *gitmoji.Table, owner, repo, revRange string) ([]parser.Commit, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, err
	}
	var commits []parser.Commit
	for _, raw := range raws {
		if _, excluded := bump.Excluded(raw.Author, firstLine(raw.Message), raw.Parents); excluded {
			continue
		}
		number, found, rerr := mergedPullFor(ctx, c, owner, repo, raw.SHA)
		if rerr != nil {
			return nil, rerr
		}
		if !found {
			continue
		}
		inner, perr := participatingPull(ctx, c, owner, repo, number)
		if perr != nil {
			return nil, perr
		}
		commits = append(commits, inner...)
	}
	return commits, nil
}

// mergedPullFor resolves a squash commit to the pull request that merged it: a
// commit can be ASSOCIATED with many PRs (a revert, a mention), so the match is
// MergeCommitSHA == sha and actually merged — never list order.
func mergedPullFor(ctx context.Context, c *github.Client, owner, repo, sha string) (number int, found bool, err error) {
	pulls, err := c.CommitPulls(ctx, owner, repo, sha)
	if err != nil {
		return 0, false, err
	}
	for _, p := range pulls {
		if p.MergeCommitSHA == sha && p.MergedAt != "" {
			return p.Number, true, nil
		}
	}
	return 0, false, nil
}
