package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/akira-toriyama/glyph/internal/parser"
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
		Short: "Lint commit messages against the gitmoji convention",
		Long: "lint checks commit messages against the gitmoji convention\n" +
			"(`<:code:>[(scope)][!] <subject>`; the retired Conventional token after the\n" +
			"gitmoji is a hard error — the violation carries the canonical rewrite).\n" +
			"--range lints every commit on its way into main (bots, merges, autosquash\n" +
			"artifacts and raw git reverts are skipped; :construction: is a violation\n" +
			"there). --pr lints a pull request's TITLE over the API, as the merge\n" +
			"candidate it is: a squash merge records that title as the landed commit's\n" +
			"subject (ratified in CONTRIBUTING — a pull-request title is a commit\n" +
			"subject), and it is the one such subject the --range walk can never see.\n" +
			"--message and --stdin lint one message at authoring time — the\n" +
			"commit-msg hook path, where :construction: stays legal. Violations exit 3\n" +
			"with a structured stderr envelope; a clean run is silent, EXCEPT that a\n" +
			"--range which judged no commit at all says so and still exits 0 — `0` means\n" +
			"\"everything I checked conforms\", which is vacuous when nothing was checked.\n\n" +
			"One rule asks for something no shape check can supply, and it fires HERE at\n" +
			"authoring time, not only in CI: a :fire:, :coffin: or :truck: commit must say\n" +
			"whether it breaks anyone — add ! (or a BREAKING CHANGE: footer) if it takes\n" +
			"public API away, else a NON-BREAKING: <why> footer. Beyond what the error\n" +
			"itself says: that footer is case-SENSITIVE, its reason is mandatory (a bare\n" +
			"NON-BREAKING: still fails), and it counts only where a trailer can sit — after\n" +
			"a blank line, as the first body line, or stacked under another trailer. What\n" +
			"counts as public: https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkNamingFlags(cmd, [][3]string{
				{"repo", "repository", repoHint},
			}); err != nil {
				return err
			}
			table, err := loadRules()
			if err != nil {
				return err
			}
			known := func(code string) bool { _, ok := table.Lookup(code); return ok }
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
			switch {
			case cmd.Flags().Changed("range"):
				return lintRangeRun(cmd.Context(), lintRange, known)
			case cmd.Flags().Changed("pr"):
				return lintPRRun(cmd.Context(), lintPR, lintRepo, known)
			case cmd.Flags().Changed("stdin"):
				if !lintStdin {
					return core.Usagef("--stdin=false selects no input mode — --stdin IS the mode, so drop it and give --range or --message instead")
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
				warnIfHookStale(cmd.Context(), hook.Kinds[0])
				return lintOne(parser.Cleanup(string(b), hookCleanupMode(cmd.Context())), known)
			case cmd.Flags().Changed("message"):
				// An empty --message is the caller naming no message, which is
				// usage — not a message that violates the convention. The old
				// fall-through could not tell the two apart and called both 3.
				if err := checkGivenEmpty(cmd, "message", "message",
					"name the message to lint (--message='<:code:> subject'), or read one from the commit-msg hook with --stdin"); err != nil {
					return err
				}
				return lintOne(lintMessage, known)
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
func hookCleanupMode(ctx context.Context) parser.CleanupMode {
	// An error is treated as unset on purpose: this is an advisory hook, and a
	// git that cannot answer a config question is not a reason to refuse a lint.
	configured, _, _ := gitsource.ConfigGet(ctx, ".", "commit.cleanup")
	mode, known := parser.ResolveCleanupMode(configured, os.Getenv("GIT_EDITOR") != ":")
	if !known {
		// Warn, never fail. The installed hook forwards ONLY the lint gate code
		// and waves every other non-zero through, so exiting here would trade a
		// typo in commit.cleanup for a repository whose commits are not linted
		// at all — maximum strictness buying zero enforcement.
		warnf("commit.cleanup=%q is not a mode git knows; linting this message as if it were 'default'", configured)
	}
	return mode
}

// lintOne lints a single message at authoring time (no merge-candidate rules).
//
// Subjects git writes itself are skipped, exactly as the --range walk skips
// them (bump.ExcludedFromClassification — the message question, the only one a
// hook can ask: there is no commit to resolve yet). Authoring time is where
// that tolerance matters MOST: the range walk only ever sees these after the
// fact, whereas the commit-msg hook stands between the developer and
// `git merge` / `git commit --fixup`. Judging them here rejected messages CI
// accepts — an author cannot rewrite a subject git generated, so the only
// escape was --no-verify, which turns the whole gate off. The retired shell
// hook exempted the same four prefixes.
//
// UnknownParents is what makes the hook the ONE place the word "Merge" still
// buys tolerance: there is no commit yet, so a merge in progress has no parent
// count to be recognised by. Every parents-aware caller passes the real count
// and gets the structural judgement instead (t-fs5y).
func lintOne(message string, known func(string) bool) error {
	if _, excluded := bump.ExcludedFromClassification("", firstLine(message), bump.UnknownParents); excluded {
		return nil
	}
	vs := parser.Lint(message, parser.LintOptions{Known: known})
	if len(vs) == 0 {
		return nil
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     fmt.Sprintf("%d commit-convention violation(s)", len(vs)),
		Details: vs,
	}
}

// lintPRRun lints the one line of a pull request a squash merge writes into
// main's history: its title. CONTRIBUTING ratifies that line as a commit
// subject, and it is the only merge-candidate subject the --range walk can
// never see — the range holds the pre-squash commits, while the title exists
// nowhere as a commit until the squash mints it. Measured across the fleet
// before this existed, 158 landed subjects failed glyph's own grammar this way,
// each one degrading the release walk's fallback to a silent none.
//
// The judgement is exactly lintRaws' judgement of the commit this title is
// about to become: parents=1 (a squash lands single-parent — never
// UnknownParents, so a "Merge …" title is an author's sentence and faces the
// lint), the PR's author stands in for the commit author (a squash attributes
// the landed commit to the pull's author, so dependabot's titles are excluded
// exactly as its commits are), and the merge-candidate rules apply because
// main is where this subject is headed (:construction: is a violation).
func lintPRRun(ctx context.Context, number int, repoFlag string, known func(string) bool) error {
	if err := checkPRFlag(number); err != nil {
		return err
	}
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return err
	}
	pull, err := newGitHub().PullRequest(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if _, excluded := bump.ExcludedFromClassification(pull.Author, pull.Title, 1); excluded {
		return nil
	}
	vs := parser.Lint(pull.Title, parser.LintOptions{Known: known, MergeCandidate: true})
	if len(vs) == 0 {
		return nil
	}
	// One annotation per finding, written by the binary that computed it —
	// the same producer contract lintRangeRun holds (t-sws7).
	for _, v := range vs {
		errorf("%s/%s#%d title %s: %s", owner, repo, number, v.Rule, v.Detail)
	}
	return &core.Error{
		Code: core.CodeLint,
		Msg: fmt.Sprintf("%d commit-convention violation(s) in the title of %s/%s#%d — a squash merge records this title as the landed commit's subject",
			len(vs), owner, repo, number),
		Details: vs,
	}
}

// lintRaws lints raw commits as merge candidates, returning every finding and
// how many commits were actually judged (excluded ones are skipped, never
// failed). Both callers — the `--range` gate CI runs and the pre-push hook —
// go through here, because a hook and CI that reach different verdicts on one
// commit is glyph lying in one of two directions, and DESIGN §2.1 says which
// of the two costs the developer the push.
func lintRaws(raws []gitsource.RawCommit, known func(string) bool) (findings []rangeViolation, checked int) {
	for _, raw := range raws {
		subject := firstLine(raw.Message)
		if _, excluded := bump.ExcludedFromClassification(raw.Author, subject, raw.Parents); excluded {
			continue
		}
		checked++
		for _, v := range parser.Lint(raw.Message, parser.LintOptions{Known: known, MergeCandidate: true}) {
			findings = append(findings, rangeViolation{SHA: raw.SHA, Subject: subject, Rule: v.Rule, Detail: v.Detail})
		}
	}
	return findings, checked
}

// rangeViolation is one finding over a range, anchored to its commit.
type rangeViolation struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Rule    string `json:"rule"`
	Detail  string `json:"detail"`
}

// lintRangeRun lints every participating commit in revRange as a merge
// candidate. Excluded commits (bots, merges, autosquash artifacts, raw git
// reverts) are skipped, never failed — the same tolerance the retired shell
// lint gave history.
func lintRangeRun(ctx context.Context, revRange string, known func(string) bool) error {
	if err := checkRangeFlag(revRange); err != nil {
		return err
	}
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return err
	}
	all, checked := lintRaws(raws, known)
	if checked == 0 {
		warnNothingLinted(revRange, len(raws))
	}
	if len(all) == 0 {
		return nil
	}
	// One annotation per finding, written by the binary that computed it. The
	// envelope below still carries the machine answer; this is the human half,
	// and it is here rather than in a caller's jq because that reconstruction
	// is where a whole run's annotations went missing in silence (t-sws7).
	for _, v := range all {
		errorf("%.7s %s: %s", v.SHA, v.Rule, v.Detail)
	}
	return &core.Error{
		Code:    core.CodeLint,
		Msg:     fmt.Sprintf("%d commit-convention violation(s) across %d linted commit(s) in %s", len(all), checked, revRange),
		Details: all,
	}
}

// warnNothingLinted reports the one verdict the exit-code contract cannot
// express: `0` says "every commit I checked conforms", and with nothing checked
// that sentence is vacuously true. The gate then reads as a pass over work it
// never looked at.
//
// lint.yml already refuses one shape of this — outside a pull_request context
// its base/head SHAs are empty, `..` collapses to an empty range and "every
// commit 'passes' silently" — but it refuses it in YAML, on the caller's side of
// the pin, and only for that one cause. The invocation CLAUDE.md tells authors
// and agents to run before pushing, `glyph lint --range origin/main..HEAD`,
// reaches the same silence with no guard at all: a stale base, a base ahead of
// head, or an unfetched ref all read as an empty range.
//
// It stays exit 0, and that is the argued part. `2` is frozen as "the arguments
// are wrong" and these arguments are fine; more concretely, a range holding
// nothing but bot commits is a DAILY event fleet-wide (dependabot, fleet-sync),
// and lint.yml forwards glyph's code verbatim (`exit "$status"`), so any
// non-zero here would red every repository's gate on a healthy push. The verdict
// belongs on the diagnostic stream, which is where warnf puts it — one
// `::warning::` annotation in Actions, one plain line at a terminal.
//
// The two causes get different sentences because they call for different fixes:
// an empty range is the caller's range to correct, while an all-excluded range
// is glyph working exactly as designed and worth knowing anyway.
func warnNothingLinted(revRange string, seen int) {
	if seen == 0 {
		warnf("nothing linted: %s holds no commits — a base ahead of head, a stale or unfetched base ref, and a genuinely empty range all read alike here", revRange)
		return
	}
	warnf("nothing linted: all %d commit(s) in %s are excluded from the convention (bots, merge commits, autosquash artifacts, raw git reverts)", seen, revRange)
}
