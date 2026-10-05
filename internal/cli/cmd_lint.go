package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/akira-toriyama/glyph/v4/internal/bump"
	"github.com/akira-toriyama/glyph/v4/internal/cleanup"
	"github.com/akira-toriyama/glyph/v4/internal/config"
	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/gitsource"
	"github.com/akira-toriyama/glyph/v4/internal/hook"
	"github.com/spf13/cobra"
)

// The four lint input modes; exactly one is required (cobra-enforced).
// lintRepo rides alongside --pr the way bumpRepo rides alongside bump's.
var (
	lintRange   string
	lintMessage string
	lintStdin   bool
	lintPR      int
	lintRepo    string
)

func newLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint commit messages against the repository's glyph.toml patterns",
		Long: "lint checks commit messages against the repository's own glyph.toml: a\n" +
			"message must match one of the file's patterns and yield a version sigil\n" +
			"(= none / ~ patch / ^ minor / ! major / % promote) — or be claimed by a skip\n" +
			"pattern.\n" +
			"That is the whole check: which prefixes exist, where the sigil sits and\n" +
			"what a subject looks like are the pattern file's decisions, and glyph has\n" +
			"no opinion on combinations (a docs commit carrying ! is the author's call).\n" +
			"--range lints every commit on its way into main (exclude_authors are\n" +
			"skipped, and so is anything a skip pattern claims — merge commits under the\n" +
			"shipped presets). --pr lints a pull request's TITLE over the API, as the\n" +
			"merge candidate it is: a squash merge records that title as the landed\n" +
			"commit's subject. --message and --stdin lint one message at authoring\n" +
			"time — the commit-msg hook path — where a message an unlandable pattern\n" +
			"claims passes with its reason as a warning (the shipped presets claim git's\n" +
			"fixup! and squash! subjects, and an amend! whose body their grammar reads);\n" +
			"every other mode refuses it, because it may be written but must not land.\n" +
			"Violations exit 3 with a structured stderr envelope; a clean run is silent,\n" +
			"EXCEPT where it warns and still exits 0: a --range which judged\n" +
			"no commit at all says so (`0` means \"everything I checked conforms\",\n" +
			"which is vacuous when nothing was checked), a --range in a shallow clone\n" +
			"says it could judge only the commits the clone holds (and, with packages\n" +
			"declared, names the commit whose attribution it could not check), a\n" +
			"pattern carrying a warn annotates every commit it claims, an unlandable\n" +
			"message at authoring time says the later gates will refuse it, and\n" +
			"--stdin — the commit-msg hook's mode — says so when commit.cleanup names\n" +
			"a mode git does not know or the installed hook was written by an older\n" +
			"glyph.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkNamingFlags(cmd, [][3]string{
				{"repo", "repository", repoHint},
			}); err != nil {
				return err
			}
			// EVERY arm asks whether the flag was GIVEN, never what its value
			// is — the same question MarkFlagsOneRequired answers, so the group
			// check and the dispatch cannot disagree about which mode was
			// selected. Dispatching --stdin on its VALUE meant an explicit
			// --stdin=false satisfied the group (cobra asks pflag's Changed) and
			// then matched no arm, so the run fell through to an empty --message
			// and answered a bad INVOCATION with 3 — the gate code the fleet's
			// commit-lint job hard-fails on, for a commit nobody submitted. It
			// swallowed a good message too: a valid subject on stdin was reported
			// as "empty commit message".
			// The invocation is judged before the environment: every arm
			// validates its own input shape FIRST and loads glyph.toml after,
			// so `--stdin=false` in a directory with no config is still
			// answered as the usage error it is, never as "not initialized".
			switch {
			case cmd.Flags().Changed("range"):
				return lintRangeRun(cmd.Context(), lintRange)
			case cmd.Flags().Changed("pr"):
				return lintPRRun(cmd.Context(), lintPR, lintRepo)
			case cmd.Flags().Changed("stdin"):
				if !lintStdin {
					return core.Usagef("--stdin=false selects no input mode — --stdin IS the mode, so drop it and give --range, --pr or --message instead")
				}
				cfg, err := loadConfig(cmd.Context())
				if err != nil {
					return err
				}
				b, rerr := io.ReadAll(in)
				if rerr != nil {
					return core.APIf("reading stdin: %v", rerr)
				}
				// --stdin is the commit-msg hook, which git invokes BEFORE its
				// own cleanup: the file still carries the editor template, the
				// status block and (under -v or commit.verbose, with an editor)
				// the diff. Reduce it to the message git will record before
				// judging it; `in` is the file the hook redirected, whose
				// identity tells a git merge from a git commit.
				//
				// The hook that called this is also the one artefact nothing
				// refreshes, so its own run is where a drifted copy is reported.
				warnIfHookStale(cmd.Context(), hook.Kinds()[0])
				return lintOne(cleanup.Apply(string(b), hookCleanupMode(cmd.Context(), in)), cfg)
			case cmd.Flags().Changed("message"):
				// An empty --message is the caller naming no message, which is
				// usage — not a message that violates the convention. The old
				// fall-through could not tell the two apart and called both 3.
				if err := checkGivenEmpty(cmd, "message", "message",
					"name the message to lint (--message='<subject>'), or read one from the commit-msg hook with --stdin"); err != nil {
					return err
				}
				cfg, err := loadConfig(cmd.Context())
				if err != nil {
					return err
				}
				return lintOne(lintMessage, cfg)
			default:
				// Unreachable while MarkFlagsOneRequired holds. Kept as usage
				// rather than a panic so a fourth mode added without its arm is
				// diagnosed as a bad invocation instead of crashing a CI gate.
				return core.Usagef("lint needs one of --range, --pr, --message or --stdin")
			}
		},
	}
	cmd.Flags().StringVar(&lintRange, "range", "", "lint every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&lintPR, "pr", 0, "lint a pull request's title — the subject a squash merge lands — read over the API")
	cmd.Flags().StringVar(&lintRepo, "repo", "", "owner/name to query for --pr (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().StringVar(&lintMessage, "message", "", "lint one message given inline")
	cmd.Flags().BoolVar(&lintStdin, "stdin", false, "lint one message read from stdin (commit-msg hook)")
	cmd.MarkFlagsMutuallyExclusive("range", "pr", "message", "stdin")
	cmd.MarkFlagsOneRequired("range", "pr", "message", "stdin")
	// --repo feeds the one API-backed mode. Combined with any local mode it
	// would be silently ignored, and glyph does not ignore input silently —
	// the same grammar markInputSourceFlags gives bump and notes.
	cmd.MarkFlagsMutuallyExclusive("repo", "range")
	cmd.MarkFlagsMutuallyExclusive("repo", "message")
	cmd.MarkFlagsMutuallyExclusive("repo", "stdin")
	return cmd
}

// hookCleanupMode reads the four signals a commit-msg hook has about what git
// is about to do to the message it was handed: `commit.cleanup`,
// `commit.verbose`, GIT_EDITOR, and whether the message is git merge's own
// MERGE_MSG (DESIGN §2.1). message is what the hook redirected onto stdin, nil
// when there is no such file to ask about.
//
// Asking git HERE rather than having the hook script pass a `--cleanup` flag is
// the decision worth knowing, and it is a rollout one. The hook is a file
// installed once into ~34 repositories; a script that had to compute the mode
// would leave every already-installed copy computing nothing, so the fix would
// reach a repo only when someone re-ran `glyph hook install` there. Deriving it
// inside the binary means the hook script does not change at all and every
// installed copy is fixed the moment the binary is. It also keeps the hook's
// founding property intact: the hook holds no knowledge, it asks glyph.
//
// No signal is required. Outside a repository, or with git unable to answer,
// the config reads fail and this proceeds as if unset — a developer piping a
// file into `glyph lint --stdin` by hand gets git's default-with-an-editor
// reading, which is what that file looks like.
func hookCleanupMode(ctx context.Context, message io.Reader) cleanup.Mode {
	// An error is treated as unset on purpose: this is an advisory hook, and a
	// git that cannot answer a config question is not a reason to refuse a lint.
	configured, _, _ := gitsource.ConfigGet(ctx, ".", "commit.cleanup")
	mode, known := cleanup.ResolveMode(configured, os.Getenv("GIT_EDITOR") != ":", hookVerbose(ctx, message))
	if !known {
		// Warn, never fail. git refuses an unknown mode for a plain `git commit`,
		// `cherry-pick -e` and a rebase reword before any hook runs, but a
		// `--cleanup=` override on the command line gets past it and the hook
		// runs (measured) — and the installed hook forwards ONLY the lint gate
		// code, so exiting here would leave exactly those commits unlinted.
		warnf("commit.cleanup=%q is not a mode git knows; linting this message as if it were 'default'", configured)
	}
	return mode
}

// hookVerbose is what the hook can know of the verbose setting git will clean
// the message under. `git merge` hands the hook its MERGE_MSG and cleans with
// verbose 0 whatever commit.verbose says; `git commit` reads commit.verbose,
// and `git -c commit.verbose=…` reaches this read too (GIT_CONFIG_PARAMETERS,
// measured). An error is unset, as commit.cleanup's is: git commit dies on a
// value it cannot parse before any hook runs, so a failing read comes from
// outside git commit — a hand-run lint, doctor's probe, or a git merge whose
// message was piped rather than redirected.
func hookVerbose(ctx context.Context, message io.Reader) cleanup.Verbose {
	if isGitMergeMessage(ctx, message) {
		return cleanup.VerboseNever
	}
	if n, _, _ := gitsource.ConfigBoolOrInt(ctx, ".", "commit.verbose"); n > 0 {
		return cleanup.VerboseOn
	}
	return cleanup.VerboseUnseen
}

// isGitMergeMessage reports whether message is the very file git merge writes
// its message to. Only `git merge` (and `git pull`) hands the commit-msg hook
// MERGE_MSG; concluding a conflicted merge with `git commit` hands it
// COMMIT_EDITMSG (both measured). The installed hook redirects the file onto
// stdin (`<"$1"`), so its identity survives; a hook that pipes the message
// instead gets git commit's reading.
func isGitMergeMessage(ctx context.Context, message io.Reader) bool {
	f, ok := message.(*os.File)
	if !ok || f == nil {
		return false
	}
	got, err := f.Stat()
	if err != nil || !got.Mode().IsRegular() {
		return false
	}
	path, err := gitsource.GitPath(ctx, ".", "MERGE_MSG")
	if err != nil {
		return false
	}
	want, err := os.Stat(path)
	return err == nil && os.SameFile(got, want)
}

// lintOne lints a single message at authoring time. The author is unknown —
// no commit exists yet — so it stands as the empty string. That the human
// authoring path is always judged is enforced in config.Load, which refuses
// an empty exclude_authors entry: slices.Contains matched one happily, so an
// entry of the empty string excused every message this function was ever
// handed — the installed hook turned off by a stray comma, at exit 0. Whatever
// tolerance authoring needs is the pattern file's to grant: skip patterns
// (the presets skip a merge in progress) and unlandable ones (the presets
// claim git's fixup! and squash! subjects, and an amend! whose body their
// grammar reads), which pass here with a warning and nowhere else
// (config.LintAuthoring).
func lintOne(message string, cfg *config.Config) error {
	v := cfg.LintAuthoring(message)
	if v.OK || v.Excluded {
		if v.Warn != "" {
			warnf("%s", v.Warn)
		}
		return nil
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     "1 commit-convention violation",
		Details: []rangeViolation{{Subject: bump.FirstLine(message), Detail: v.Reason}},
	}
}

// lintPRRun lints the one line of a pull request a squash merge writes into
// main's history: its title. CONTRIBUTING ratifies that line as a commit
// subject, and it is the only merge-candidate subject the --range walk can
// never see — the range holds the pre-squash commits, while the title exists
// nowhere as a commit until the squash mints it.
//
// The PR's author stands in for the commit author (a squash attributes the
// landed commit to the pull's author), so an exclude_authors title —
// dependabot's — passes exactly as its commits do.
func lintPRRun(ctx context.Context, number int, repoFlag string) error {
	if err := checkPRFlag(number); err != nil {
		return err
	}
	owner, repo, err := resolveRepo(ctx, repoFlag)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	pull, err := newGitHub().PullRequest(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	v := cfg.Lint(pull.Title, pull.Author)
	if v.OK || v.Excluded {
		if v.Warn != "" {
			warnf("%s/%s#%d title: %s", owner, repo, number, v.Warn)
		}
		return nil
	}
	// One annotation for the finding, written by the binary that computed it —
	// the same producer contract lintRangeRun holds (t-sws7).
	errorf("%s/%s#%d title: %s", owner, repo, number, v.Reason)
	return &core.Error{
		Code: core.CodeLint,
		Msg: fmt.Sprintf("1 commit-convention violation in the title of %s/%s#%d — a squash merge records this title as the landed commit's subject",
			owner, repo, number),
		Details: []rangeViolation{{Subject: pull.Title, Detail: v.Reason}},
	}
}

// lintRaws lints raw commits, returning every finding, every warned-but-clean
// commit, and how many commits were actually judged (excluded authors are
// skipped, never failed; a skip-pattern match is judged and clean). Both
// callers — the `--range` gate CI runs and the pre-push hook — go through
// here, because a hook and CI that reach different verdicts on one commit is
// glyph lying in one of two directions, and DESIGN §2.1 says which of the two
// costs the developer the push. warned rides beside findings for the same
// reason: a warned pattern must be loud at both gates or the quiet one
// teaches the developer the warning is noise.
//
// With [[packages]] declared, a clean message is judged once more against
// the commit's own diff (DESIGN §4.1): a commit under no package whose sigil
// claims a version impact, and a scope naming a package the diff does not
// touch, are findings here — the pre-push hook is where a shared-only ^ is
// caught before it is pushed, and the release walk would refuse it later
// with no way to rewrite it. --message and --stdin never reach this: a
// message alone has no diff. A shallow clone's boundary commit has no diff
// this checkout can read, so its attribution is not asked and the commit is
// warned about instead — at both gates, like a warned pattern. The error is
// git failing to read a diff (API).
func lintRaws(ctx context.Context, raws []gitsource.RawCommit, cfg *config.Config) (findings, warned []rangeViolation, checked int, err error) {
	for _, raw := range raws {
		v := cfg.Lint(raw.Message, raw.Author)
		if v.Excluded {
			continue
		}
		checked++
		if !v.OK {
			findings = append(findings, rangeViolation{SHA: raw.SHA, Subject: bump.FirstLine(raw.Message), Detail: v.Reason})
			continue
		}
		if v.Warn != "" {
			warned = append(warned, rangeViolation{SHA: raw.SHA, Subject: bump.FirstLine(raw.Message), Detail: v.Warn})
		}
		if len(cfg.Packages) == 0 {
			continue
		}
		reason, aerr := lintAttribution(ctx, raw, cfg)
		switch {
		case gitsource.IsShallowBoundary(aerr):
			warned = append(warned, rangeViolation{SHA: raw.SHA, Subject: bump.FirstLine(raw.Message),
				Detail: "its parents are not in this shallow clone, so its own diff cannot be read and the [[packages]] attribution was not checked — fetch the full history (fetch-depth: 0) to judge it"})
		case aerr != nil:
			return nil, nil, 0, aerr
		case reason != "":
			findings = append(findings, rangeViolation{SHA: raw.SHA, Subject: bump.FirstLine(raw.Message), Detail: reason})
		}
	}
	return findings, warned, checked, nil
}

// lintAttribution asks attribution's question of one clean, matched commit
// under local git, returning the refusal's sentence or "". A skip-pattern
// match has no sigil to carry; a merge commit's diff is never asked for
// (attributed to nothing, the same as in the walk). A shallow boundary's
// unreadable diff comes back as DiffTreeFiles' error, for the caller to warn.
func lintAttribution(ctx context.Context, raw gitsource.RawCommit, cfg *config.Config) (string, error) {
	m, merr := cfg.Match(raw.Message)
	if merr != nil || !m.Matched || m.Skip {
		return "", nil
	}
	var files []string
	if raw.Parents < 2 {
		var err error
		if files, err = gitsource.DiffTreeFiles(ctx, ".", raw.SHA); err != nil {
			return "", err
		}
	}
	if _, aerr := attribute(cfg, raw, readingOf(m), files, true); aerr != nil {
		return aerr.Error(), nil
	}
	return "", nil
}

// rangeViolation is one finding, anchored to its commit where one exists.
// The v1 rule-id vocabulary is gone with the embedded grammar: a v2 finding
// is the config's own sentence about why the message means nothing under it.
type rangeViolation struct {
	SHA     string `json:"sha,omitempty"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// lintRangeRun lints every commit in revRange. Excluded authors are skipped,
// never failed — the bots exclude_authors names lint nowhere. A shallow
// checkout is warned about, never refused (logRange): the verdict is about the
// commits the clone holds.
func lintRangeRun(ctx context.Context, revRange string) error {
	if err := checkRangeFlag(revRange); err != nil {
		return err
	}
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	raws, lerr := logRange(ctx, revRange)
	if lerr != nil {
		return lerr
	}
	findings, warned, checked, aerr := lintRaws(ctx, raws, cfg)
	if aerr != nil {
		return aerr
	}
	// Warnings go out even when the run fails: the warned commits are real
	// whichever way the verdict lands, and a developer fixing the violation
	// should not discover the warning only on the green re-run.
	for _, w := range warned {
		warnf("commit %.7s: %s", w.SHA, w.Detail)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			errorf("commit %.7s: %s", f.SHA, f.Detail)
		}
		return &core.Error{
			Code:    core.CodeLint,
			Msg:     fmt.Sprintf("%d commit-convention violation(s)", len(findings)),
			Details: findings,
		}
	}
	if checked == 0 {
		// The two causes need different sentences: an empty walk means the
		// range holds no commits (the caller's range is what needs fixing),
		// while a non-empty one means every commit was excluded.
		if len(raws) == 0 {
			warnf("nothing linted: %s holds no commits — nothing to lint is not a pass on anything", revRange)
		} else {
			warnf("nothing linted: all %d commit(s) in %s are excluded from the convention (exclude_authors) — nothing to lint is not a pass on anything", len(raws), revRange)
		}
	}
	return nil
}
