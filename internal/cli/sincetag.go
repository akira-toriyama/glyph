package cli

import (
	"context"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
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

// sinceTagArgs is the Args guard for the commands carrying --since-tag: they
// take no positionals, and the one stray positional users actually produce is
// the space form of the flag's optional value (`--since-tag v1.2.3`), which
// pflag parses as a bare --since-tag plus a leftover. Walking the WRONG range
// silently is the worst outcome, so that shape gets a usage error spelling out
// the = form instead of cobra's generic unknown-command complaint.
func sinceTagArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && cmd.Flags().Changed("since-tag") {
		if tag, _ := cmd.Flags().GetString("since-tag"); tag == sinceTagAuto {
			return core.Usagef("--since-tag takes its tag attached with '=': --since-tag=%s", args[0])
		}
	}
	return cobra.NoArgs(cmd, args)
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
		if github.IsCommitUnknown(rerr) {
			// GitHub does not know the SHA yet (422) — the walk outran the API
			// right after a push. DESIGN §4's API lag, so fall back, not fail.
			if fc, ok := fallbackCommit(table, raw, "is not known to GitHub yet (API lag)"); ok {
				commits = append(commits, fc)
			}
			continue
		}
		if rerr != nil {
			return nil, rerr
		}
		if !found {
			if fc, ok := fallbackCommit(table, raw, "has no merged pull request (a direct push, or the API lagging)"); ok {
				commits = append(commits, fc)
			}
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

// fallbackCommit handles a walked commit that cannot be resolved to a merged
// pull request — no association (a direct push to main), or GitHub not knowing
// the SHA yet. Fallbacks never hard-fail a release (DESIGN §4), so every
// outcome is a ::warning:: carrying why (the caller's headline) plus the
// softest sound decision: a message that parses to a KNOWN gitmoji is
// classified as itself; a message that does not parse, or whose gitmoji is
// unknown (the ratified t-kbqx policy — this assembly layer downgrades what
// the lint gate keeps as a hard error, so internal/bump stays pure), counts
// none by being left out of the fold.
func fallbackCommit(table *gitmoji.Table, raw gitsource.RawCommit, why string) (parser.Commit, bool) {
	c, perr := parser.Parse(raw.Message)
	if perr != nil {
		warnf("commit %.7s %s and its own message does not parse (%v) — counted as none", raw.SHA, why, perr)
		return parser.Commit{}, false
	}
	c.SHA, c.Author = raw.SHA, raw.Author
	if _, cerr := bump.Classify(c, table); cerr != nil {
		warnf("commit %.7s %s and its gitmoji %s is not in the rules table — counted as none", raw.SHA, why, c.Gitmoji)
		return parser.Commit{}, false
	}
	warnf("commit %.7s %s — classifying its own message", raw.SHA, why)
	return c, true
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
