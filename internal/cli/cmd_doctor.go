package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/doctor"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/spf13/cobra"
)

var (
	doctorRepo string
	doctorJSON bool
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that this repository satisfies the assumptions glyph's release machinery makes",
		Long: "doctor answers the question every other glyph command silently assumes: does\n" +
			"this repository still match the model glyph computes against. The settings it\n" +
			"reads are invisible from inside a release run, so when one drifts the workflows\n" +
			"stay green and the verdict is simply wrong — glyph-test ran for weeks with the\n" +
			"squash subject switched to the PR title and nothing said a word.\n\n" +
			"It is READ-ONLY: nothing about the repository, the checkout or the workflows is\n" +
			"ever modified. Each failing check prints the command that fixes it and leaves\n" +
			"running it to you.\n\n" +
			"The checks, each independent — one it cannot run never stops the others:\n\n" +
			"  - the credential can read the repository at all, and what the API itself says\n" +
			"    about its permissions (never a guess at scopes the API did not report)\n" +
			"  - squash merging is ENABLED: a squash-merged pull puts exactly ONE commit on\n" +
			"    main and that commit IS its merge_commit_sha, so it is never half-resolved —\n" +
			"    no part of it can stand aside for a merge point the walk cannot resolve. The\n" +
			"    multi-commit landings all have that window. (A dark API is still not free:\n" +
			"    a MULTI-commit squash carries the PR title — linted by `lint --pr` at the\n" +
			"    gate, but the fallback still classifies that one subject instead of the\n" +
			"    pull's commits.)\n" +
			"  - merge commits / rebase merges are off — advice, not a defect: the walk\n" +
			"    resolves and expands both (a merge commit since t-7zt7, a rebase from its\n" +
			"    last replayed commit, which is what GitHub names merge_commit_sha). What is\n" +
			"    left is a house convention plus one named, LOUD window per style — a merge\n" +
			"    point GitHub has not indexed yet (or an automation authored) drops its pull\n" +
			"    with two warnings (release refuses the incomplete walk at exit 4;\n" +
			"    bump/notes hand down their verdict without it); a rebase whose listing the\n" +
			"    walk cannot align — one that dropped a commit it was asked to replay,\n" +
			"    already upstream or rebased empty — can still fold a replayed commit in\n" +
			"    twice during API lag.\n" +
			"    Neither is the silent wrong verdict a failure is reserved for\n" +
			"  - squash_merge_commit_title=COMMIT_OR_PR_TITLE and\n" +
			"    squash_merge_commit_message=COMMIT_MESSAGES: these decide whether the commit\n" +
			"    that lands on main carries a classifiable gitmoji subject at all\n" +
			"  - every `uses: akira-toriyama/glyph/…` in the LOCAL .github/workflows pins a\n" +
			"    concrete @vX.Y.Z tag (whether the pin is the LATEST release is deliberately\n" +
			"    NOT checked — glyph-pin-audit.yml in akira-toriyama/.github already owns\n" +
			"    that question fleet-wide, and two answers to it would be one too many)\n" +
			"  - no STALE glyph-written commit-msg hook is installed. Hooks are untracked, so\n" +
			"    nothing refreshes one: whatever glyph was on PATH the day it was installed\n" +
			"    froze the exit code it stops a commit on and the arguments it hands lint, and\n" +
			"    a stale hook still exits 0 — indistinguishable from a clean message. NO hook\n" +
			"    passes (it is opt-in, and an Actions checkout cannot have one); a hook glyph\n" +
			"    did not write is advice, because glyph will not overwrite it unasked\n\n" +
			"--repo moves only the API side. The workflow-pin check always reads the LOCAL\n" +
			"checkout, because a pin is a fact about the tree in front of you — pointing\n" +
			"--repo elsewhere diagnoses that repository's settings and THIS checkout's pins.\n\n" +
			"Every check carries a stable machine id — branch on that, never on the prose.\n" +
			"Exit: 0 = every check passed / 2 = the invocation itself was wrong (a\n" +
			"malformed --repo, rejected before any request goes out) / 3 = a check\n" +
			"failed (the repository violates a convention glyph enforces, the same class\n" +
			"as a lint violation) / 4 = every runnable check passed but one could not\n" +
			"run, which is unverified, not fine / 130 = interrupted.\n" +
			"An API failure that produced no answer at all — a rate limit, an outage that\n" +
			"outlived the retries, an unreachable host — is a could-not-run (4) and never a\n" +
			"violation (3): glyph observed nothing, so it says nothing about the repository.\n" +
			"Advice never affects the exit. --json emits {repo,checks,counts,ok} on one line.",
		Example: "  glyph doctor                          # diagnose $GITHUB_REPOSITORY from this checkout\n" +
			"  glyph doctor --repo akira-toriyama/sill\n" +
			"  glyph doctor --json | jq -e .ok       # CI pre-flight",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doctorRun(cmd)
		},
	}
	cmd.Flags().StringVar(&doctorRepo, "repo", "", "owner/name to diagnose (default: $GITHUB_REPOSITORY)")
	cmd.Flags().BoolVar(&doctorJSON, "json", false, "emit the machine report {repo,checks,counts,ok}")
	return cmd
}

// probeCommitMsgHook FIRES the installed commit-msg hook against a message
// that violates the convention, and reports what came back. It lives here
// because internal/doctor runs no subprocess, exactly as it makes no request —
// the same division that puts HooksDir here.
//
// It fires ONLY when the installed bytes are exactly this binary's hook — the
// arm the byte-compare check passes. Someone else's hook is theirs to run and
// a stale glyph hook already fails the drift check, so executing either would
// have a read-only diagnosis run code it does not vouch for. nil means nothing
// was fired, and internal/doctor renders why from its own checks.
//
// What the probe buys over the byte-compare: the script blocks only on the
// lint gate code and waves every other failure through by design, so current
// bytes over a glyph that cannot answer — a PATH wrapper building a different
// checkout (the documented worktree trap), a tree that does not compile — are
// a silent no-op the compare calls healthy. Only firing the hook asks the
// question end to end.
//
// commit-msg is fired and pre-push is NOT, and the asymmetry is argued, not
// an accident. What a live fire proves is a property of the MACHINE — that
// the glyph the hook resolves on PATH can actually lint — and both hooks
// resolve the same PATH, so one answer covers both for the default `glyph
// hook install`, which writes the pair. Firing commit-msg costs a temp file;
// firing pre-push would cost a fabricated push — a scratch repository with a
// remote, a violating commit and a default-branch head — which is subprocess
// choreography a read-only diagnosis should not carry for a question already
// answered. The residual blind spot is a machine where ONLY pre-push was
// installed by name: real, narrow, and accepted (t-2etd holds the design if
// it is ever worth closing).
func probeCommitMsgHook(ctx context.Context, dir string, dirErr error) *doctor.HookProbe {
	if dirErr != nil {
		return nil
	}
	k := hook.Kinds()[0]
	path := filepath.Join(dir, k.Name)
	body, err := os.ReadFile(path) // #nosec G304 -- the path git itself reported for this checkout
	if err != nil || string(body) != k.Script {
		return nil
	}
	msg, err := os.CreateTemp("", "glyph-doctor-probe-*.txt")
	if err != nil {
		return &doctor.HookProbe{Err: err}
	}
	defer os.Remove(msg.Name()) //nolint:errcheck // best-effort scratch cleanup
	// A subject no cleanup mode can rescue and NO profile grammar accepts —
	// no leading token in either vocabulary — so the gate code is the only
	// healthy answer whichever profile the installed hook lints under.
	if _, werr := msg.WriteString("this doctor probe matches no commit grammar\n"); werr != nil {
		_ = msg.Close() // the write error is the one worth reporting
		return &doctor.HookProbe{Err: werr}
	}
	if cerr := msg.Close(); cerr != nil {
		return &doctor.HookProbe{Err: cerr}
	}
	// Exactly git's invocation: the hook file itself, argv[1] the message file.
	// Output is discarded — the hook's stderr talks to a committing developer,
	// and the check renders its own sentence from the exit code alone.
	probe := exec.CommandContext(ctx, path, msg.Name()) // #nosec G204 -- firing the byte-verified hook glyph itself wrote
	err = probe.Run()
	if err == nil {
		return &doctor.HookProbe{Fired: true, Exit: 0}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return &doctor.HookProbe{Fired: true, Exit: exit.ExitCode()}
	}
	return &doctor.HookProbe{Err: err}
}

// probeErr unwraps the probe's own failure for the interrupt guard below — a
// Ctrl-C that lands mid-probe is the user's abort, never a check result.
func probeErr(p *doctor.HookProbe) error {
	if p == nil {
		return nil
	}
	return p.Err
}

// firstInterrupt returns the first of errs carrying the user's interrupt, or
// nil. Split out of doctorRun so the guard over its two independent reads (the
// API, then git) is testable without arranging a live signal — cancelling the
// context before doctorRun makes the FIRST read interrupted and the guard
// passes for the wrong reason, hiding a dropped herr entirely.
func firstInterrupt(errs ...error) error {
	for _, err := range errs {
		if core.IsInterrupted(err) {
			return err
		}
	}
	return nil
}

func doctorRun(cmd *cobra.Command) error {
	if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
		return err
	}
	owner, name, err := resolveRepo(doctorRepo)
	if err != nil {
		return err
	}
	repoObject, rerr := newGitHub().Repository(cmd.Context(), owner, name)
	// git names the hooks directory (core.hooksPath relocates it), and this is
	// the only place allowed to ask: internal/doctor runs no subprocess, exactly
	// as it makes no request. A failure here is handed over verbatim and becomes
	// that one check's could-not-run — never an aborted report.
	hooksDir, herr := gitsource.HooksDir(cmd.Context(), ".")
	probe := probeCommitMsgHook(cmd.Context(), hooksDir, herr)
	// An interrupt is the user's own abort and must never be laundered into a
	// check result: reporting "the token cannot read the repository" — or "git
	// could not report where hooks live" — because somebody pressed Ctrl-C
	// would be a diagnosis of the wrong thing entirely. Both reads run before
	// this guard, and the signal lands in whichever is in flight, so both are
	// asked: guarding rerr alone turned a mid-run SIGTERM into exit 4 with the
	// abort rendered as the hook check's could-not-run — the one code the
	// fleet's wrappers read as retryable infra, on a run the operator stopped.
	if err := firstInterrupt(rerr, herr, probeErr(probe)); err != nil {
		return err
	}
	report := doctor.Run(doctor.Input{
		Repo: owner + "/" + name,
		// The local checkout is cwd, the same assumption every verdict command
		// makes (DESIGN §4: glyph runs inside a checkout of the repository
		// being released). The workflow-pin check names the directory it read
		// when it is not there, so a wrong cwd diagnoses itself.
		Root:            ".",
		RepoObject:      repoObject,
		RepoErr:         rerr,
		TokenConfigured: githubToken() != "",
		HooksDir:        hooksDir,
		HooksErr:        herr,
		CommitMsgProbe:  probe,
	})

	// Annotations go out in BOTH modes, before the payload. On an Actions
	// runner they are the finding a human sees in the job summary, and making
	// them conditional on --json would mean the machine mode is the only one a
	// human can read — exactly backwards.
	for _, c := range report.Checks {
		switch c.Status {
		case doctor.StatusFail:
			warnf("doctor %s: %s (expected %s) — %s", c.ID, c.Observed, c.Expected, c.Fix)
		case doctor.StatusUnknown:
			warnf("doctor %s could not run: %s", c.ID, c.Observed)
		case doctor.StatusAdvice:
			noticef("doctor %s: %s (house convention: %s)", c.ID, c.Observed, c.Expected)
		case doctor.StatusPass:
		}
	}

	if doctorJSON {
		printCompact(report)
	} else {
		printDoctorHuman(report)
	}
	return doctorVerdict(report)
}

// doctorVerdict maps the report to glyph's exit-code contract. No new integers:
//
//   - A FAILED check is a convention violation in the subject glyph was asked to
//     judge — a repository's configuration here, a commit message under `lint` —
//     so it takes the same code 3 the lint gate uses. It is emphatically not
//     usage (2: the invocation was fine) and not an API failure (4: glyph did its
//     job and the answer is no).
//   - A check that could NOT RUN is glyph failing to do its job — the API never
//     answered, the file was unreadable — which is exactly what 4 means. Nothing
//     was proven, so it cannot be 0. This is where a rate limit or a 5xx that
//     outlived the retry schedule lands (see checkTokenAccess): the fleet's CI
//     wrappers branch on `.error.code == 3` to hard-fail and treat everything
//     else as a retryable infra failure, so classifying an outage as 3 would
//     hard-fail a healthy repository as "misconfigured" and never retry it.
//   - Failure outranks could-not-run: a definite defect is the more actionable
//     verdict and must not be masked by an unrelated blind spot.
//
// The report is already on stdout, so in --json mode the error is Silent (the
// payload carries every field the envelope would) and otherwise renders the
// envelope with the failing ids as machine-actionable details — the same split
// bump and release make on a none verdict.
func doctorVerdict(r *doctor.Report) error {
	if failed := r.IDs(doctor.StatusFail); len(failed) > 0 {
		return &core.Error{
			Code:    core.CodeLint,
			Msg:     fmt.Sprintf("doctor: %d check(s) failed — this repository does not satisfy what glyph assumes about it", len(failed)),
			Details: failed,
			Silent:  doctorJSON,
		}
	}
	if unknown := r.IDs(doctor.StatusUnknown); len(unknown) > 0 {
		return &core.Error{
			Code:    core.CodeAPI,
			Msg:     fmt.Sprintf("doctor: %d check(s) could not run — the repository is unverified, not verified-good", len(unknown)),
			Details: unknown,
			Silent:  doctorJSON,
		}
	}
	return nil
}

// printDoctorHuman renders the report as one scannable line per check —
// status, machine id, what was observed — with the expectation, the consequence
// and the command to run indented under anything that is not a pass. A passing
// check needs no advice, so it stays one line and the failures are what the eye
// lands on.
func printDoctorHuman(r *doctor.Report) {
	fmt.Fprintf(out, "glyph doctor — %s (read-only: nothing was changed)\n\n", r.Repo)
	// spaced tracks whether the last block already ended in a blank line, so the
	// summary is separated from the checks exactly once however the report ends.
	spaced := false
	for _, c := range r.Checks {
		fmt.Fprintf(out, "%-8s %-22s %s\n", c.Status, c.ID, c.Observed)
		if c.Status == doctor.StatusPass {
			spaced = false
			continue
		}
		fmt.Fprintf(out, "%s expected: %s\n", doctorIndent, c.Expected)
		fmt.Fprintf(out, "%s why:      %s\n", doctorIndent, c.Message)
		for _, d := range c.Details {
			fmt.Fprintf(out, "%s   - %s\n", doctorIndent, d)
		}
		if c.Fix != "" {
			fmt.Fprintf(out, "%s fix:      %s\n", doctorIndent, c.Fix)
		}
		fmt.Fprintln(out)
		spaced = true
	}
	if !spaced {
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%d checks: %d pass, %d fail, %d advice, %d could not run\n",
		len(r.Checks), r.Counts.Pass, r.Counts.Fail, r.Counts.Advice, r.Counts.Unknown)
}

// doctorIndent aligns a finding's detail lines under the id column.
const doctorIndent = "         "
