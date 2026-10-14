package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/spf13/cobra"
)

// bumpResult is the machine verdict: {current, level, next, commits, reason}
// — plus packages when the repository declares [[packages]].
// next is omitted on a none verdict — there is no next version to act on.
//
// With packages declared current / level are EMPTY, next is absent, and
// packages carries one verdict per line (DESIGN §4.1): a repository with
// packages has no one line for the scalars to describe, and a consumer that
// reads only the scalars is exactly the consumer that must not act on them
// (mutation row packages-scalar-verdict-describes-one-line). commits then
// lists every participating commit — the fold's rows over the whole walk, a
// shared-only = among them, in no line (DESIGN §4.1).
type bumpResult struct {
	Current  string           `json:"current"`
	Level    string           `json:"level"`
	Next     string           `json:"next,omitempty"`
	Commits  []bump.Row       `json:"commits"`
	Packages []packageVerdict `json:"packages,omitempty"`
	Reason   string           `json:"reason"`
}

// packageVerdict is one line's verdict inside bumpResult.packages: the
// package's path (the line's name), what the line steps from, how far, to
// what, the commits that participate in the line, and why.
type packageVerdict struct {
	Path    string     `json:"path"`
	Current string     `json:"current"`
	Level   string     `json:"level"`
	Next    string     `json:"next,omitempty"`
	Commits []bump.Row `json:"commits"`
	Reason  string     `json:"reason"`
}

// bumpOptions is what one bump run was asked, snapshotted at RunE: the input
// source, --current and --json. Nothing below RunE reads a flag — bumpRun is
// a function of these alone, so the verdict runs with no command built and
// no flag parsed (TestBumpRunsFromOptionsWithoutCobra).
type bumpOptions struct {
	source
	current string
	json    bool
}

func newBumpCmd() *cobra.Command {
	var f sourceFlags
	var current string
	var asJSON bool
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
			"an unmatched commit folded as none would be a silent hole — and so does\n" +
			"one an unlandable pattern claims, with that pattern's reason.\n" +
			"There are three input sources, exactly one of which is required.\n" +
			"--range reads a local git revision range; --pr reads a pull request's\n" +
			"INDIVIDUAL commits over the API, which is what makes the verdict\n" +
			"squash-safe (a squash-merge rewrites the subject to the PR title and would\n" +
			"otherwise erase every per-commit sigil); --since-tag walks main's merge\n" +
			"points since a tag and expands each back into the pull it merged — the\n" +
			"release-time source, and what glyph's own release job uses.\n" +
			"stdout is the bare next version\n" +
			"(pipe it into a tag step); --json emits {current,level,next,commits,reason}.\n" +
			"A none verdict prints no version and exits 1 (soft no-release).\n\n" +
			"On a repository declaring [[packages]] the verdict is per line: stdout is\n" +
			"the next TAG of every line that moves, one per line (haiku/v0.2.0), and\n" +
			"exit 1 means every line folded to none; --json carries packages:\n" +
			"[{path,current,level,next,commits,reason}] with the scalar current/level\n" +
			"EMPTY and next absent — there is no one line for them to describe, so read\n" +
			"the array. --pr is refused there (exit 2: a pull's listing carries\n" +
			"messages and no files, so nothing can be attributed to a line), and\n" +
			"--current is refused (exit 2) unless the walk selects one line\n" +
			"(--since-tag=<prefix>vX.Y.Z or below:, <prefix> being the line's tag\n" +
			"prefix: haiku/, or nothing for the root package).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkNamingFlags(cmd, [][3]string{
				{"current", "version", currentHint},
				{"repo", "repository", repoHint},
			}); err != nil {
				return err
			}
			return bumpRun(cmd.Context(), bumpOptions{source: sourceOf(cmd, f), current: current, json: asJSON})
		},
	}
	cmd.Flags().StringVar(&f.rng, "range", "", "fold every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&f.pull, "pr", 0, "fold a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &f.tag, "fold")
	cmd.Flags().StringVar(&f.repo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().StringVar(&current, "current", "", currentFlagUsage)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine verdict {current,level,next,commits,reason}; with [[packages]] declared current/level are empty, next is absent, and packages[] carries one verdict per line")
	markInputSourceFlags(cmd)
	return cmd
}

// bumpRun is bump below its flags: the one walk result whatever the source
// (readSource), the union fold, one verdict per line, and the render. A
// repository without [[packages]] walks as one bare line (pendingLines,
// resolveLinesScoped), so the per-line loop is the only loop and the render
// alone tells the shapes apart. The bare line's verdict IS the scalars —
// its reason unprefixed, its next version bare on stdout — the pre-packages
// surface byte for byte (mutation row packages-absent-changes-the-single-line).
// With packages declared (DESIGN §4.1) the scalars stay empty, packages[]
// carries one verdict per line, each folded over the commits that joined
// that line and stepped from that line's own base, --current is accepted
// only when one line is selected, and stdout is the next TAG of every line
// that moves, one per line (haiku/v0.2.0 — the prefix is what a tag step
// needs); exit 1 only when every line folds to none.
func bumpRun(ctx context.Context, o bumpOptions) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	w, err := readSource(ctx, cfg, o.source)
	if err != nil {
		return err
	}
	if err := checkLineSelection(o.current, w.Lines); err != nil {
		return err
	}
	// The union rows first: every walked commit is matched here, so a
	// message no pattern claims refuses the walk BEFORE any line is folded
	// (a wrong grammar cannot hide behind a wrong tree), and a shared-only =
	// commit — on no line — still appears in what the walk read. On the bare
	// line the union is the line, and these rows are its rows.
	rows, _, cerr := bump.Fold(walkedBumpCommits(w.All), cfg)
	if cerr != nil {
		return cerr
	}
	warnBumpRows(rows)
	var pkgs []packageVerdict
	var reasons, tags []string
	for _, lw := range w.Lines {
		lineRows, dec, ferr := bump.Fold(walkedBumpCommits(lw.Commits), cfg)
		if ferr != nil {
			return ferr
		}
		current, verr := currentVersion(ctx, o.current, lw.Base, lw.Line)
		if verr != nil {
			return verr
		}
		pv := packageVerdict{Path: lw.Package.Path, Current: current.String(), Level: string(dec.Level), Commits: lineRows}
		if dec.Level == bump.LevelNone {
			pv.Reason = noneReason(len(lineRows), lw.Source)
		} else {
			next, nerr := bump.NextOn(lw.Line, current, dec)
			if nerr != nil {
				return nerr
			}
			pv.Next = next.String()
			pv.Reason = bump.DecidingReason(lineRows, dec)
			tags = append(tags, next.TagOn(lw.Line.Prefix))
		}
		reasons = append(reasons, lw.Package.Path+": "+pv.Reason)
		pkgs = append(pkgs, pv)
	}
	res := bumpResult{Commits: rows, Packages: pkgs, Reason: strings.Join(reasons, "; ")}
	switch {
	case len(cfg.Packages) == 0:
		one := pkgs[0]
		res = bumpResult{Current: one.Current, Level: one.Level, Next: one.Next, Commits: rows, Reason: one.Reason}
	case len(tags) == 0:
		res.Reason = everyLineNone(res.Reason)
	}
	if len(tags) == 0 {
		if o.json {
			printCompact(res)
		}
		return core.NoReleasef("%s", res.Reason).Quiet(o.json)
	}
	if o.json {
		printCompact(res)
		return nil
	}
	for _, tag := range tags {
		fmt.Fprintln(out, tag)
	}
	return nil
}

// currentVersion resolves the version to step from ON ONE LINE: an explicit
// --current (malformed, or of a major the line does not hold ⇒ usage — it is
// the caller's input) wins; else the
// base the input source itself named (--since-tag's tag — the walk base and
// the step base must be the SAME tag); else the highest parseable tag on the
// line (prefix "" is the bare line) that HEAD contains, which is v0.0.0 for a
// line whose history holds no release yet. A topic branch forked before the
// base branch's latest release therefore steps from its fork point's release
// (DESIGN §4).
func currentVersion(ctx context.Context, flag string, base *bump.Version, l config.Line) (bump.Version, error) {
	v, err := parseCurrent(flag)
	if err != nil {
		return bump.Version{}, err
	}
	if v != nil {
		if !l.Holds(v.Major) {
			return bump.Version{}, core.Usagef("--current %s is not on the %s line, which holds no v%d.* version — name a version the line can step from", v, l.Label(), v.Major)
		}
		return *v, nil
	}
	if base != nil {
		return *base, nil
	}
	_, latest, err := latestVersionTag(ctx, l, nil)
	return latest, err
}

// parseCurrent reads --current: nil when the flag is absent, the version it
// names, or usage (2) — the caller's input. release asks before the releases
// listing is fetched, so a malformed flag is answered 2 even when that
// listing would fail at 4.
func parseCurrent(flag string) (*bump.Version, error) {
	if flag == "" {
		return nil, nil
	}
	v, err := bump.ParseVersion(flag)
	if err != nil {
		return nil, core.Usagef("--current: %v", err)
	}
	return &v, nil
}

// warnBumpRows surfaces each folded commit's pattern warning
// (config.Pattern.Warn). Every command that folds a range calls this right
// after bump.Fold — bump, release, and both of preview's folds — because a
// warned pattern loud in one command and silent in another teaches the reader
// that the loud one is noise, which is how a warning dies.
func warnBumpRows(commits []bump.Row) {
	for _, c := range commits {
		if c.Warn != "" {
			warnf("commit %.7s: %s", c.SHA, c.Warn)
		}
	}
}

// noneReason is the none verdict's reason line — the single line's and each
// packages line's, one spelling. release.yml and operators read it, and
// DESIGN §4.1 makes "participate" one set, so the sentence is a contract.
func noneReason(participating int, source string) string {
	return fmt.Sprintf("no release: %d commit(s) participate in %s and every level is none", participating, source)
}

func everyLineNone(reasons string) string {
	return "no release: every line folds to none (" + reasons + ")"
}
