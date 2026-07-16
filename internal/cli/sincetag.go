package cli

import (
	"context"
	"fmt"
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

// markInputSourceFlags is the source-selection grammar bump and notes share:
// exactly one of --range / --pr / --since-tag, and --repo only with the
// API-backed sources — combined with the purely local --range it would be
// silently ignored, and glyph does not ignore input silently.
func markInputSourceFlags(cmd *cobra.Command) {
	cmd.MarkFlagsOneRequired("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "repo")
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

// checkSinceTagFlag rejects a malformed explicit tag before anything runs —
// caller input, so usage (2), mirroring checkRangeFlag: the same mistake must
// not surface as a retryable-looking git failure (4). The auto sentinel (a
// bare --since-tag) is not a name and skips the checks.
func checkSinceTagFlag(tag string) error {
	if tag == sinceTagAuto {
		return nil
	}
	if strings.TrimSpace(tag) == "" {
		// A workflow templating an unset variable produces exactly this; auto
		// is spelled by OMITTING the value, never by an empty one.
		return core.Usagef("--since-tag= names an empty tag — use a bare --since-tag for the highest v* tag, or name one with --since-tag=TAG")
	}
	if strings.HasPrefix(tag, "-") {
		return core.Usagef("--since-tag %q looks like an option, not a tag", tag)
	}
	if strings.Contains(tag, "..") {
		return core.Usagef("--since-tag %q looks like a revision range — name the tag alone; the walk appends ..HEAD itself", tag)
	}
	return nil
}

// sinceTagInput resolves the repository, the walk range and the version base,
// then walks. bump, notes and release share it; the returned source names the
// range for the reason line, base (when the tag names a version) is what the
// bump steps from, and pulls is the walk's expansion provenance (release
// reports it, the others discard it).
func sinceTagInput(ctx context.Context, table *gitmoji.Table, tagFlag, repoFlag string) ([]parser.Commit, []pullExpansion, string, *bump.Version, error) {
	if err := checkSinceTagFlag(tagFlag); err != nil {
		return nil, nil, "", nil, err
	}
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return nil, nil, "", nil, err
	}
	revRange, base, err := sinceTagRange(ctx, tagFlag)
	if err != nil {
		return nil, nil, "", nil, err
	}
	commits, pulls, err := walkSince(ctx, newGitHub(), table, owner, repo, revRange)
	return commits, pulls, revRange, base, err
}

// sinceTagRange turns the --since-tag value into a git revision range and, when
// the tag names a version, the version the bump steps from. Both come from ONE
// resolution, so the walk base and the step base are the same tag by
// construction — naming a tag names the release being redone; stepping from a
// different (higher) tag would version a verdict computed over another range.
// Auto resolves the highest parseable v* tag; a repository before its first
// release walks the whole history and steps from v0.0.0. An explicit tag that
// is not a version still walks, but names no base (nil — the bump falls back
// to the highest v* tag).
func sinceTagRange(ctx context.Context, tagFlag string) (revRange string, base *bump.Version, err error) {
	tag := strings.TrimSpace(tagFlag)
	if tag == sinceTagAuto {
		latest, v, lerr := latestVersionTag(ctx)
		if lerr != nil {
			return "", nil, lerr
		}
		if latest == "" {
			// One API round-trip per commit over everything — a cost the
			// caller should see named (house rule: no silent unbounded work).
			warnf("no version tag found — walking the repository's whole history")
			return "HEAD", &bump.Version{}, nil
		}
		return latest + "..HEAD", &v, nil
	}
	if v, perr := bump.ParseVersion(tag); perr == nil {
		return tag + "..HEAD", &v, nil
	}
	return tag + "..HEAD", nil, nil
}

// latestVersionTag returns the highest parseable version tag and its parsed
// version; tag is empty for a repository before its first release. The one
// resolver behind both the walk base and the bump base.
func latestVersionTag(ctx context.Context) (tag string, v bump.Version, err error) {
	tags, terr := gitsource.Tags(ctx, ".")
	if terr != nil {
		return "", bump.Version{}, terr
	}
	for _, t := range tags {
		if pv, perr := bump.ParseVersion(t); perr == nil {
			return t, pv, nil
		}
	}
	return "", bump.Version{}, nil
}

// pullExpansion records one merged pull request the walk resolved and how many
// participating commits it contributed. This is the walk reporting its own
// expansion facts: git-cliff (and any main-history reader) sees only the squash
// subject, so a verdict divergence is EXPECTED exactly when some pull
// contributed 2+ commits (COMMIT_OR_PR_TITLE replaced its squash subject with
// the PR title) — the shadow comparison (ratified Q6) branches on this instead
// of re-implementing the walk's exclusion rules in shell.
type pullExpansion struct {
	Number  int `json:"number"`
	Commits int `json:"commits"`
}

// walkSince walks the range's commits oldest first and folds every merged PR's
// individual commits into one participating list, recording per-pull expansion
// provenance alongside. Excluded commits are skipped before any API call — the
// routine fleet-sync direct push never costs a request.
func walkSince(ctx context.Context, c *github.Client, table *gitmoji.Table, owner, repo, revRange string) ([]parser.Commit, []pullExpansion, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, nil, err
	}
	var commits []parser.Commit
	// Normalized to [] up front so the JSON surface never emits null — a
	// shadow script indexes .pulls unconditionally.
	pulls := []pullExpansion{}
	walked, unknown := 0, 0
	for _, raw := range raws {
		// A squash subject here is a POINTER to a pull request, not a message
		// being classified — so only the author (bots) and the structure
		// (merge commits) exclude before resolution: a `Revert "..."` squash
		// subject is a real revert PR that must resolve and expand. Subject
		// rules apply where a message IS classified — on the PR's inner
		// commits (participating) and on the fallback path (fallbackCommit).
		if _, excluded := bump.Excluded(raw.Author, "", raw.Parents); excluded {
			continue
		}
		walked++
		number, found, covered, rerr := mergedPullFor(ctx, c, owner, repo, raw.SHA)
		var why string
		switch {
		case github.IsCommitUnknown(rerr):
			// GitHub does not know the SHA yet (422) — the walk outran the API
			// right after a push. DESIGN §4's API lag: fall back, never fail.
			unknown++
			why = "is not known to GitHub yet (API lag)"
		case rerr != nil:
			return nil, nil, rerr
		case found:
			inner, perr := participatingPull(ctx, c, owner, repo, number)
			if perr != nil {
				return nil, nil, wedgeHint(perr, owner, repo, number)
			}
			// Pre-flight the membership check that classifyVerdict would run
			// later anyway (same Classify, same table — it cannot disagree):
			// down there the PR association is gone, and a bare per-commit
			// lint line is uniquely unhelpful HERE — see wedgeHint.
			for _, ic := range inner {
				if _, cerr := bump.Classify(ic, table); cerr != nil {
					return nil, nil, wedgeHint(cerr, owner, repo, number)
				}
			}
			commits = append(commits, inner...)
			pulls = append(pulls, pullExpansion{Number: number, Commits: len(inner)})
			continue
		case covered:
			// Part of a merged PR whose canonical (merge_commit_sha) commit
			// represents it — a rebase-merge's non-final commits. That commit
			// expands the whole PR, so counting this one too would double it.
			continue
		default:
			why = "has no merged pull request (a direct push, or the API lagging)"
		}
		if fc, ok := fallbackCommit(table, raw, why); ok {
			commits = append(commits, fc)
		}
	}
	if walked > 0 && unknown == walked {
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", walked, revRange, owner, repo)
	}
	return commits, pulls, nil
}

// gitmojiBoom is the canonical breaking gitmoji — what an unknown-code
// breaking fallback is normalized to so the shared verdict and notes
// plumbing (which hard-errors on unknown codes, by design) can carry it.
const gitmojiBoom = ":boom:"

// fallbackCommit handles a walked commit that cannot be resolved to a merged
// pull request — no association (a direct push to main), or GitHub not knowing
// the SHA yet. Fallbacks never hard-fail a release (DESIGN §4), so every
// outcome is at most a ::warning:: carrying why (the caller's headline) plus
// the softest sound decision: an excluded message (a raw git revert, an
// autosquash artifact) is skipped silently, exactly as the --range walk skips
// it; a message that parses to a KNOWN gitmoji is classified as itself; a
// message that does not parse, or whose gitmoji is unknown (the ratified
// t-kbqx policy — this assembly layer downgrades what the lint gate keeps as a
// hard error, so internal/bump stays pure), counts none by being left out of
// the fold. One exception, ratified Q10: a breaking marker is NEVER
// suppressed — an unknown code carrying one (`!` or a BREAKING CHANGE footer,
// already parsed onto c.Breaking) counts major, normalized to :boom: so it
// folds and hoists into Breaking Changes downstream. The asymmetry is
// deliberate: a typo can over-bump a version, but a breaking change must
// never be silently dropped from one.
func fallbackCommit(table *gitmoji.Table, raw gitsource.RawCommit, why string) (parser.Commit, bool) {
	if _, excluded := bump.Excluded(raw.Author, firstLine(raw.Message), raw.Parents); excluded {
		return parser.Commit{}, false // skipped, never a violation — and never a warning
	}
	c, perr := parseRaw(raw)
	if perr != nil {
		warnf("commit %.7s %s and its own message does not parse (%v) — counted as none", raw.SHA, why, perr)
		return parser.Commit{}, false
	}
	if _, cerr := bump.Classify(c, table); cerr != nil {
		if c.Breaking {
			warnf("commit %.7s %s and its gitmoji %s is not in the rules table, but it carries a breaking marker — counted as a breaking change (%s, major)", raw.SHA, why, c.Gitmoji, gitmojiBoom)
			c.Gitmoji = gitmojiBoom
			return c, true
		}
		warnf("commit %.7s %s and its gitmoji %s is not in the rules table — counted as none", raw.SHA, why, c.Gitmoji)
		return parser.Commit{}, false
	}
	warnf("commit %.7s %s — classifying its own message", raw.SHA, why)
	return c, true
}

// wedgeHint decorates a lint failure surfaced while expanding a merged PR on
// the release walk (ratified Q1/t-2nzf: such failures stay hard — never a
// silent patch). A bare per-commit lint line is uniquely unhelpful here: the
// caller never named a PR (the walk resolved it), and the squash history is
// immutable, so the SAME failure — a commit that bypassed the lint gate —
// wedges every future release until the walk range moves past it. The message
// therefore names the pull request and both escapes. Non-lint failures pass
// through untouched.
func wedgeHint(err error, owner, repo string, number int) error {
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeLint {
		return err
	}
	return &core.Error{Code: core.CodeLint, Details: ce.Details, Msg: fmt.Sprintf(
		"%s — inside merged pull request %s/%s#%d, which the release walk resolved; squash history is immutable, so every release wedges here until the walk moves past this commit: cut a release tag past it by hand, or name a later base with an explicit --since-tag=TAG (DESIGN §4)",
		ce.Msg, owner, repo, number)}
}

// mergedPullFor resolves a squash commit to the pull request that merged it: a
// commit can be ASSOCIATED with many PRs (a revert, a mention), so the match is
// MergeCommitSHA == sha and actually merged — never list order. covered
// reports the rebase-merge shape instead: the commit belongs to a merged PR
// whose canonical commit is a DIFFERENT sha, so this one is that PR's to
// represent, not the fallback's.
func mergedPullFor(ctx context.Context, c *github.Client, owner, repo, sha string) (number int, found, covered bool, err error) {
	pulls, err := c.CommitPulls(ctx, owner, repo, sha)
	if err != nil {
		return 0, false, false, err
	}
	for _, p := range pulls {
		if p.MergedAt == "" {
			continue
		}
		if p.MergeCommitSHA == sha {
			return p.Number, true, false, nil
		}
		covered = true
	}
	return 0, false, covered, nil
}
