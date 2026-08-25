package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/akira-toriyama/glyph/internal/cleanup"
	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
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
			"skipped, and so is anything a skip pattern claims — merge commits and\n" +
			"autosquash artifacts under the shipped presets). --pr lints a pull\n" +
			"request's TITLE over the API, as the merge candidate it is: a squash merge\n" +
			"records that title as the landed commit's subject. --message and --stdin\n" +
			"lint one message at authoring time — the commit-msg hook path. Violations\n" +
			"exit 3 with a structured stderr envelope; a clean run is silent, EXCEPT\n" +
			"that a --range which judged no commit at all says so and still exits 0 —\n" +
			"`0` means \"everything I checked conforms\", which is vacuous when nothing\n" +
			"was checked.",
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
					return core.Usagef("--stdin=false selects no input mode — --stdin IS the mode, so drop it and give --range or --message instead")
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
				// status block and (under commit.verbose) the diff. Reduce it to
				// the message git will record before judging it.
				//
				// The hook that called this is also the one artefact nothing
				// refreshes, so its own run is where a drifted copy is reported.
				warnIfHookStale(cmd.Context(), hook.Kinds()[0])
				return lintOne(cleanup.Apply(string(b), hookCleanupMode(cmd.Context())), cfg)
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
				return core.Usagef("lint needs one of --range, --message or --stdin")
			}
		},
	}
	cmd.Flags().StringVar(&lintRange, "range", "", "lint every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&lintPR, "pr", 0, "lint a pull request's title — the subject a squash merge lands — read over the API")
	cmd.Flags().StringVar(&lintRepo, "repo", "", "owner/name to query for --pr (default: $GITHUB_REPOSITORY)")
	cmd.Flags().StringVar(&lintMessage, "message", "", "lint one message given inline")
	cmd.Flags().BoolVar(&lintStdin, "stdin", false, "lint one message read from stdin (commit-msg hook)")
	cmd.MarkFlagsMutuallyExclusive("range", "pr", "message", "stdin")
	cmd.MarkFlagsOneRequired("range", "pr", "message", "stdin")
	return cmd
}

// hookCleanupMode reads the two signals a commit-msg hook has about what git is
// about to do to the message it was handed: `commit.cleanup`, and GIT_EDITOR.
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
// Neither signal is required. Outside a repository, or with git unable to answer,
// the config read fails and this proceeds as if unset — a developer piping a file
// into `glyph lint --stdin` by hand gets git's default-with-an-editor reading,
// which is what that file looks like.
func hookCleanupMode(ctx context.Context) cleanup.Mode {
	// An error is treated as unset on purpose: this is an advisory hook, and a
	// git that cannot answer a config question is not a reason to refuse a lint.
	configured, _, _ := gitsource.ConfigGet(ctx, ".", "commit.cleanup")
	mode, known := cleanup.ResolveMode(configured, os.Getenv("GIT_EDITOR") != ":")
	if !known {
		// Warn, never fail. The installed hook forwards ONLY the lint gate code
		// and waves every other non-zero through, so exiting here would trade a
		// typo in commit.cleanup for a repository whose commits are not linted
		// at all — maximum strictness buying zero enforcement.
		warnf("commit.cleanup=%q is not a mode git knows; linting this message as if it were 'default'", configured)
	}
	return mode
}

// lintOne lints a single message at authoring time. The author is unknown —
// no commit exists yet — so it stands as the empty string. That the human
// authoring path is always judged is enforced in config.Load, which refuses
// an empty exclude_authors entry: slices.Contains matched one happily, so an
// entry of the empty string excused every message this function was ever
// handed — the installed hook turned off by a stray comma, at exit 0. Whatever
// tolerance authoring needs (a merge in progress, an autosquash artifact) is
// the pattern file's to grant through skip patterns, and the shipped presets
// grant exactly those two.
func lintOne(message string, cfg *config.Config) error {
	v := cfg.Lint(message, "")
	if v.OK || v.Excluded {
		if v.Warn != "" {
			warnf("%s", v.Warn)
		}
		return nil
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     "1 commit-convention violation(s)",
		Details: []rangeViolation{{Subject: firstLine(message), Detail: v.Reason}},
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
	owner, repo, err := resolveRepo(repoFlag)
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
		Msg: fmt.Sprintf("1 commit-convention violation(s) in the title of %s/%s#%d — a squash merge records this title as the landed commit's subject",
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
func lintRaws(raws []gitsource.RawCommit, cfg *config.Config) (findings, warned []rangeViolation, checked int) {
	for _, raw := range raws {
		v := cfg.Lint(raw.Message, raw.Author)
		if v.Excluded {
			continue
		}
		checked++
		if !v.OK {
			findings = append(findings, rangeViolation{SHA: raw.SHA, Subject: firstLine(raw.Message), Detail: v.Reason})
			continue
		}
		if v.Warn != "" {
			warned = append(warned, rangeViolation{SHA: raw.SHA, Subject: firstLine(raw.Message), Detail: v.Warn})
		}
	}
	return findings, warned, checked
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
// never failed — the bots exclude_authors names lint nowhere.
func lintRangeRun(ctx context.Context, revRange string) error {
	if err := checkRangeFlag(revRange); err != nil {
		return err
	}
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	raws, lerr := gitsource.Log(ctx, ".", revRange)
	if lerr != nil {
		return lerr
	}
	findings, warned, checked := lintRaws(raws, cfg)
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
