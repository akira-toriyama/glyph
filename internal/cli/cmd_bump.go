package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v3/internal/bump"
	"github.com/akira-toriyama/glyph/v3/internal/config"
	"github.com/akira-toriyama/glyph/v3/internal/core"
	"github.com/akira-toriyama/glyph/v3/internal/gitsource"
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

// bumpResult is the machine verdict: {current, level, next, commits, reason}
// — plus packages when the repository declares [[packages]].
// next is omitted on a none verdict — there is no next version to act on.
// The commit rows are bump.SigilVerdict: {sha, subject, sigil, level} — the
// v1 "code" and "breaking" keys died with the embedded table; the sigil IS
// the classification input now.
//
// With packages declared the scalars current / level / next are EMPTY and
// packages carries one verdict per line (DESIGN §4.1): a repository with
// packages has no one line for the scalars to describe, and a consumer that
// reads only the scalars is exactly the consumer that must not act on them
// (mutation row packages-scalar-verdict-describes-one-line). commits then
// lists every commit that participates on any line.
type bumpResult struct {
	Current  string              `json:"current"`
	Level    string              `json:"level"`
	Next     string              `json:"next,omitempty"`
	Commits  []bump.SigilVerdict `json:"commits"`
	Packages []packageVerdict    `json:"packages,omitempty"`
	Reason   string              `json:"reason"`
}

// packageVerdict is one line's verdict inside bumpResult.packages: the
// package's path (the line's name), what the line steps from, how far, to
// what, the commits that participate on the line, and why.
type packageVerdict struct {
	Path    string              `json:"path"`
	Current string              `json:"current"`
	Level   string              `json:"level"`
	Next    string              `json:"next,omitempty"`
	Commits []bump.SigilVerdict `json:"commits"`
	Reason  string              `json:"reason"`
}

func newBumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bump",
		Short: "Compute the next version from a range of commits",
		Long: "bump reads each commit's version sigil under the repository's glyph.toml\n" +
			"(= none / ~ patch / ^ minor / ! major / % promote — captured by your\n" +
			"patterns, or supplied by a pattern's fixed semver_sigil), folds the levels\n" +
			"with max — so order can never change the verdict — and steps the current\n" +
			"version. While the major is 0 a ! steps the minor, so 1.0.0 is reached only\n" +
			"by a % commit saying so; from 1.x on, % is a plain major.\n" +
			"exclude_authors stay out of the fold; a skip-pattern commit stays out of\n" +
			"everything; a commit NO pattern claims refuses the whole range (exit 3) —\n" +
			"an unmatched commit folded as none would be a silent hole.\n" +
			"There are three input sources, exactly one of which is required.\n" +
			"--range reads a local git revision range; --pr reads a pull request's\n" +
			"INDIVIDUAL commits over the API, which is what makes the verdict\n" +
			"squash-safe (a squash-merge rewrites the subject to the PR title and would\n" +
			"otherwise erase every per-commit sigil); --since-tag walks main's merge\n" +
			"points since a tag and expands each back into the pull it merged — the\n" +
			"release-time source, and what glyph's own release job uses.\n" +
			"stdout is the bare next version\n" +
			"(pipe it into a tag step); --json emits {current,level,next,commits,reason}.\n" +
			"A none verdict prints no version and exits 1 (soft no-release).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return bumpRun(cmd)
		},
	}
	cmd.Flags().StringVar(&bumpRange, "range", "", "fold every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&bumpPR, "pr", 0, "fold a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &bumpSinceTag, "fold")
	cmd.Flags().StringVar(&bumpRepo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().StringVar(&bumpCurrent, "current", "", currentFlagUsage)
	cmd.Flags().BoolVar(&bumpJSON, "json", false, "emit the machine verdict {current,level,next,commits,reason}")
	markInputSourceFlags(cmd)
	return cmd
}

func bumpRun(cmd *cobra.Command) error {
	if err := checkNamingFlags(cmd, [][3]string{
		{"current", "version", currentHint},
		{"repo", "repository", repoHint},
	}); err != nil {
		return err
	}
	ctx := cmd.Context()
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	if len(cfg.Packages) > 0 {
		return bumpLines(cmd, cfg)
	}
	raws, source, base, perr := bumpInput(cmd, cfg)
	if perr != nil {
		return perr
	}

	commits, dec, cerr := bump.FoldSigils(raws, cfg)
	if cerr != nil {
		return cerr
	}
	warnSigilVerdicts(commits)
	current, verr := currentVersion(ctx, bumpCurrent, base, "")
	if verr != nil {
		return verr
	}

	if dec.Level == bump.LevelNone {
		reason := fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(commits), source)
		if bumpJSON {
			printCompact(bumpResult{Current: current.String(), Level: string(dec.Level), Commits: commits, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}

	next := current.Next(dec)
	reason := decidingReason(commits, dec)
	if bumpJSON {
		printCompact(bumpResult{
			Current: current.String(),
			Level:   string(dec.Level),
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
func bumpInput(cmd *cobra.Command, cfg *config.Config) ([]bump.SigilCommit, string, *bump.Version, error) {
	ctx := cmd.Context()
	if cmd.Flags().Changed("pr") {
		raws, source, err := pullInput(ctx, bumpPR, bumpRepo)
		return sigilCommits(raws), source, nil, err
	}
	if cmd.Flags().Changed("since-tag") {
		// Both halves of the walk's facts are discarded here, and for the same
		// reason: bump REPORTS, it does not act. The expansion provenance is
		// release's audit trail, and an incomplete walk is already a ::warning::
		// per cause on stderr — the channel the operator and the Actions
		// annotation layer both read. What made discarding it wrong for release
		// is that release acts on its verdict irreversibly, which is why it
		// fails loud (4) on an incomplete walk; bump writes nothing back, so
		// the warning is the whole remedy. (preview is the exception among the
		// readers: its answer is pasted into a pull request and read later by
		// someone who never opens the log, so it carries the shortfall in the
		// body itself.)
		w, err := sinceTagInput(ctx, cfg, bumpSinceTag, bumpRepo)
		return walkedSigilCommits(w.All), w.Source, w.Base, err
	}
	if err := checkRangeFlag(bumpRange); err != nil {
		return nil, "", nil, err
	}
	raws, err := gitsource.Log(ctx, ".", bumpRange)
	return sigilCommits(raws), bumpRange, nil, err
}

// bumpLines is bump for a repository that declares [[packages]] (DESIGN
// §4.1): one verdict per line, each folded over the commits that participate
// on that line and stepped from that line's own base. --pr is refused — a
// pull's listing has no files to attribute — and --current is accepted only
// when one line is selected. stdout is the next TAG of every line that
// moves, one per line (haiku/v0.2.0 — the prefix is what a tag step needs);
// exit 1 only when every line folds to none.
func bumpLines(cmd *cobra.Command, cfg *config.Config) error {
	ctx := cmd.Context()
	var w sinceTagWalk
	var err error
	switch {
	case cmd.Flags().Changed("pr"):
		return refusePullSource(cfg)
	case cmd.Flags().Changed("since-tag"):
		w, err = sinceTagInput(ctx, cfg, bumpSinceTag, bumpRepo)
	default:
		if err = checkRangeFlag(bumpRange); err != nil {
			return err
		}
		w, err = rangeLines(ctx, cfg, bumpRange)
	}
	if err != nil {
		return err
	}
	if err := checkLineSelection(bumpCurrent, w.Lines); err != nil {
		return err
	}
	// The union rows first: every participating commit is matched here, so a
	// message no pattern claims refuses the walk BEFORE any line is folded
	// (a wrong grammar cannot hide behind a wrong tree), and a shared-only =
	// commit — on no line — still appears in what the walk read.
	rows, _, cerr := bump.FoldSigils(walkedSigilCommits(w.All), cfg)
	if cerr != nil {
		return cerr
	}
	warnSigilVerdicts(rows)
	var pkgs []packageVerdict
	var reasons, tags []string
	for _, lw := range w.Lines {
		verdicts, dec, ferr := bump.FoldSigils(walkedSigilCommits(lw.Commits), cfg)
		if ferr != nil {
			return ferr
		}
		current, verr := currentVersion(ctx, bumpCurrent, lw.Base, lw.Prefix)
		if verr != nil {
			return verr
		}
		pv := packageVerdict{Path: lw.Package.Path, Current: current.String(), Level: string(dec.Level), Commits: verdicts}
		if dec.Level == bump.LevelNone {
			pv.Reason = fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", len(verdicts), lw.Source)
		} else {
			next := current.Next(dec)
			pv.Next = next.String()
			pv.Reason = decidingReason(verdicts, dec)
			tags = append(tags, next.TagOn(lw.Prefix))
		}
		reasons = append(reasons, lw.Package.Path+": "+pv.Reason)
		pkgs = append(pkgs, pv)
	}
	reason := strings.Join(reasons, "; ")
	if len(tags) == 0 {
		reason = "no release: every line folds to none (" + reason + ")"
		if bumpJSON {
			printCompact(bumpResult{Commits: rows, Packages: pkgs, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}
	if bumpJSON {
		printCompact(bumpResult{Commits: rows, Packages: pkgs, Reason: reason})
		return nil
	}
	for _, tag := range tags {
		fmt.Fprintln(out, tag)
	}
	return nil
}

// currentVersion resolves the version to step from ON ONE LINE: an explicit
// --current (malformed ⇒ usage — it is the caller's input) wins; else the
// base the input source itself named (--since-tag's tag — the walk base and
// the step base must be the SAME tag); else the highest parseable tag on the
// line (prefix "" is the bare line), which is v0.0.0 for a line before its
// first release.
func currentVersion(ctx context.Context, flag string, base *bump.Version, prefix string) (bump.Version, error) {
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
	_, v, err := latestVersionTag(ctx, prefix, nil)
	return v, err
}

// warnSigilVerdicts surfaces each folded commit's pattern warning
// (config.Pattern.Warn). Every command that folds a range calls this right
// after FoldSigils — bump, release, and both of preview's folds — because a
// warned pattern loud in one command and silent in another teaches the reader
// that the loud one is noise, which is how a warning dies.
func warnSigilVerdicts(commits []bump.SigilVerdict) {
	for _, c := range commits {
		if c.Warn != "" {
			warnf("commit %.7s: %s", c.SHA, c.Warn)
		}
	}
}

// decidingReason names the oldest commit that reaches the folded level — the
// one-line answer to "why this bump".
//
// A promoted range names its oldest '%' commit instead, even though promote
// and major share a level. Otherwise the reason for landing on 1.0.0 would
// point at whichever breaking commit came first, and the one commit that
// actually chose the version would not appear in the answer at all.
func decidingReason(commits []bump.SigilVerdict, dec bump.Decision) string {
	if dec.Promote {
		for _, c := range commits {
			if c.Sigil == config.SigilPromote.String() {
				return fmt.Sprintf("%.7s %s %q → promote", c.SHA, c.Sigil, c.Subject)
			}
		}
	}
	for _, c := range commits {
		if c.Level == string(dec.Level) {
			return fmt.Sprintf("%.7s %s %q → %s", c.SHA, c.Sigil, c.Subject, dec.Level)
		}
	}
	return fmt.Sprintf("level %s", dec.Level)
}
