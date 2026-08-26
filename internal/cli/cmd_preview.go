package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/akira-toriyama/glyph/internal/preview"
	"github.com/spf13/cobra"
)

var (
	previewPR    int
	previewRepo  string
	previewNotes bool
	previewJSON  bool
)

// previewResult is the machine verdict:
// {current, untagged, level, next, pr, pending, body}. level/next are the FOLDED
// answer — what the version becomes if this PR merges — so a caller never
// re-derives it from the two sides; pr and pending are the two sides it folded,
// reported separately so a comment can show the working.
type previewResult struct {
	Current  string `json:"current"`
	Untagged bool   `json:"untagged"`
	Level    string `json:"level"`
	Next     string `json:"next,omitempty"`
	PR       string `json:"pr"`
	Pending  string `json:"pending"`
	Body     string `json:"body"`
}

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Render the merge preview for a pull request as one Markdown comment",
		Long: "preview answers, BEFORE the merge, what merging a pull request does to\n" +
			"the version — and renders it as the whole sticky-comment body, marker\n" +
			"included. It folds two verdicts: the PR's own INDIVIDUAL (pre-squash)\n" +
			"commits read over the API, and what is already merged but unreleased on\n" +
			"the checked-out branch. Both step from the same tag, so the answer is the\n" +
			"`next` of whichever folds higher — no caller ever does version arithmetic.\n\n" +
			"A repository with no v* tag skips the pending walk: it would resolve the\n" +
			"whole history at one API round-trip per commit to answer a question that\n" +
			"cannot matter (nothing is unreleased when nothing was released), so the\n" +
			"PR's own verdict stands alone and the body says so.\n\n" +
			"The body never names a rolling draft — the arithmetic is the same whether\n" +
			"the next release is a draft or a tag cut by hand, and most repos have no\n" +
			"draft to name. stdout is the Markdown body (post it with `gh pr comment\n" +
			"--body-file -`); --json emits {current,untagged,level,next,pr,pending,body}.\n" +
			"Unlike bump and notes, a none verdict is a normal answer here and exits 0:\n" +
			"'this PR moves nothing' is exactly what a reviewer asked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return previewRun(cmd)
		},
	}
	cmd.Flags().IntVar(&previewPR, "pr", 0, "the pull request to preview, read over the API (required)")
	cmd.Flags().StringVar(&previewRepo, "repo", "", "owner/name to query (default: $GITHUB_REPOSITORY)")
	cmd.Flags().BoolVar(&previewNotes, "notes", false, "fold the release-notes preview for this PR into the body")
	cmd.Flags().BoolVar(&previewJSON, "json", false, "emit the machine verdict {current,untagged,level,next,pr,pending,body}")
	_ = cmd.MarkFlagRequired("pr")
	return cmd
}

func previewRun(cmd *cobra.Command) error {
	if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
		return err
	}
	ctx := cmd.Context()
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := checkPRFlag(previewPR); err != nil {
		return err
	}

	// The PR side: pure API, bounded by the PR's own commits.
	raws, _, perr := pullInput(ctx, previewPR, previewRepo)
	if perr != nil {
		return perr
	}
	prCommits, prDec, cerr := bump.FoldSigils(sigilCommits(raws), cfg)
	if cerr != nil {
		return cerr
	}
	warnSigilVerdicts(prCommits)
	// The tag NAME, not just the version it parses to. Whether this repository
	// has ever released is a question about the existence of a tag, and
	// currentVersion answers only what to step FROM — for an untagged repository
	// that is v0.0.0, which is also a perfectly ordinary tag to have cut.
	// Comparing the version against the zero value therefore called a repository
	// tagged v0.0.0 untagged, skipped its pending walk, and printed "this
	// repository has no v* release tag yet" — while `bump --since-tag=auto` on
	// the SAME checkout walked v0.0.0..HEAD and answered minor. Two commands,
	// one repository, contradictory answers.
	latestTag, current, verr := latestVersionTag(ctx, nil)
	if verr != nil {
		return verr
	}

	// RELEASE-FLOOR guard. The pending walk is the only unbounded input: with no
	// v* tag, --since-tag resolves the entire history and pays one API
	// round-trip per commit (sinceTagRange names the cost rather than hiding
	// it). Distributed fleet-wide that lands on repos with a thousand commits
	// and no release — and buys nothing, because a repo with no release has
	// nothing merged-but-unreleased to fold in. Skip the walk; the PR's own
	// verdict is the whole answer, and the body says why.
	untagged := latestTag == ""
	var pendingDec bump.Decision
	var pendingNext string
	var pendingShort string
	if !untagged {
		pparsed, facts, _, _, serr := sinceTagInput(ctx, cfg, sinceTagAuto, previewRepo)
		if serr != nil {
			return serr
		}
		// The pending fold is the one side of this comment computed from a walk,
		// so it is the one side that can come back short. release refuses to act
		// on that; there is nothing to refuse here, so it is reported instead —
		// see preview.Input.PendingShort for why the walk's own ::warning:: is
		// not enough on this path.
		if !facts.complete() {
			owner, repo, rerr := resolveRepo(previewRepo)
			if rerr != nil {
				return rerr
			}
			pendingShort = facts.shortfall(owner, repo)
		}
		var pendingCommits []bump.SigilVerdict
		pendingCommits, pendingDec, cerr = bump.FoldSigils(walkedSigilCommits(pparsed), cfg)
		if cerr != nil {
			return cerr
		}
		warnSigilVerdicts(pendingCommits)
		if pendingDec.Level != bump.LevelNone {
			pendingNext = current.Next(pendingDec).String()
		}
	} else {
		warnf("no v* release tag here — previewing this PR's own verdict only (the pending walk needs a release floor)")
	}

	in := preview.Input{
		Current:  current.String(),
		Untagged: untagged,
		PR:       preview.Verdict{Level: prDec.Level, Commits: previewCommits(prCommits)},
		Pending:  preview.Verdict{Level: pendingDec.Level, Next: pendingNext},

		PendingShort: pendingShort,
	}
	if prDec.Level != bump.LevelNone {
		in.PR.Next = current.Next(prDec).String()
	}
	if previewNotes {
		sections, gerr := notes.GroupSigils(noteCommits(raws, previewPR), cfg)
		if gerr != nil {
			return gerr
		}
		if len(sections) > 0 {
			in.Notes = notes.RenderSigils(sections)
		}
	}

	// Truncated (never refused) before EITHER output path: the workflow posts
	// the --json payload's body, so an oversized comment has to be cut in the
	// one place both surfaces share.
	body := truncateComment(preview.Render(in))
	if previewJSON {
		d := foldDecision(prDec, pendingDec)
		res := previewResult{
			Current:  in.Current,
			Untagged: untagged,
			Level:    string(d.Level),
			PR:       string(prDec.Level),
			Pending:  string(orNone(pendingDec.Level)),
			Body:     body,
		}
		if d.Level != bump.LevelNone {
			res.Next = current.Next(d).String()
		}
		printCompact(res)
		return nil
	}
	fmt.Fprint(out, body)
	return nil
}

// foldDecision is the whole point of the command: the version this PR lands on
// is the HIGHER of its own decision and what is already pending, because both
// step from the same tag. bump.Decision.Merge owns the ordering — the same
// max-fold every other verdict in glyph goes through, so preview cannot grow a
// second opinion about which level wins, and a '%' on either side promotes the
// preview exactly as it will promote the release.
func foldDecision(a, b bump.Decision) bump.Decision {
	a.Level, b.Level = orNone(a.Level), orNone(b.Level)
	return a.Merge(b)
}

// orNone normalizes an uncomputed level. Bump's zero value is "" rather than
// "none" (it is a string type), and a skipped pending walk leaves exactly that
// — which must read as "nothing pending", never as an unknown that leaks into
// the JSON surface.
func orNone(b bump.Level) bump.Level {
	if b == "" {
		return bump.LevelNone
	}
	return b
}

// previewCommits maps the shared verdict rows onto the render's own type. The
// two are deliberately separate: bump.SigilVerdict is a JSON surface other
// commands publish, and preview must be free to show a column it does not.
func previewCommits(cs []bump.SigilVerdict) []preview.Commit {
	out := make([]preview.Commit, 0, len(cs))
	for _, c := range cs {
		out = append(out, preview.Commit{
			Sigil:   c.Sigil,
			Level:   bump.Level(c.Level),
			Subject: c.Subject,
		})
	}
	return out
}
