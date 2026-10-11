package workflows

// pr-verdict.yml's sticky-comment upsert, executed. The step runs under the
// unspecified-shell invocation with a gh stub that answers the comment lookup
// and records each write and the body it was handed. The step's env: block —
// which contexts feed COMPOSE_OUTCOME, HEAD_SHA and the rest — is outside the
// harness: it supplies each by name, and TestPRVerdictUpsertRunsAfterAFailedCompose
// holds the wiring.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/preview"
)

const (
	prVerdictWorkflow = ".github/workflows/pr-verdict.yml"
	composeStep       = "Compose the verdict comment"
	upsertStep        = "Upsert the sticky comment"
)

// TestPRVerdictCommentNeverOutlivesARedPush runs the upsert over each way the
// compose step can end and reads what reaches the sticky comment. The comment
// is refreshed on every push (DESIGN §6): a push whose verdict did not compute
// replaces the previous verdict with the workflow's own short body, because a
// skipped upsert left the verdict an earlier green push posted beside a red
// head — measured on glyph-test run 37306205001 (2026-10-05: compose failure,
// upsert skipped). The computed verdicts are the positive control: they prove
// the stub's lookup and writes are the step's, so a fallback elsewhere is the
// step's choice and not the harness's.
func TestPRVerdictCommentNeverOutlivesARedPush(t *testing.T) {
	script := extractRun(t, repoFile(t, filepath.FromSlash(prVerdictWorkflow)), upsertStep)

	const (
		slug      = "akira-toriyama/glyph-test"
		headSHA   = "800189d4c0ffee00000000000000000000000001"
		runID     = "37306205001"
		composed  = preview.Marker + "\nthe verdict glyph composed at this head\n"
		commentID = "42"
	)
	runURL := "https://github.com/" + slug + "/actions/runs/" + runID

	cases := []struct {
		name     string
		outcome  string // steps.verdict.outcome
		bodyFile string // what the compose step left in body.md; "" leaves none
		existing string // the sticky comment's id; "" when the pull has none yet
		posted   bool   // the write carries the composed body, not the fallback
		why      string
	}{
		{
			name: "a computed verdict replaces the sticky comment", outcome: "success",
			bodyFile: composed, existing: commentID, posted: true,
			why: "the positive control: a verdict that computed is posted as glyph rendered it",
		},
		{
			name: "a computed verdict opens the sticky comment", outcome: "success",
			bodyFile: composed, posted: true,
			why: "the positive control for a pull with no comment yet",
		},
		{
			name: "a failed compose replaces the previous verdict", outcome: "failure", existing: commentID,
			why: "a rejected commit or an incomplete walk (preview exit 3 or 4) leaves no body.md — skipped, the " +
				"upsert left the previous push's verdict standing beside a red head",
		},
		{
			name: "a body the compose step wrote before failing is not posted", outcome: "failure",
			bodyFile: composed, existing: commentID,
			why: "the compose step writes body.md before it reads the verdict fields, so a step that failed after " +
				"writing it leaves a body for a verdict the job did not hand out",
		},
		{
			name: "a compose skipped behind a failed install replaces the previous verdict", outcome: "skipped",
			existing: commentID,
			why:      "a push whose install failed computed no verdict either",
		},
		{
			name: "a failed compose on a pull with no comment yet opens one", outcome: "failure",
			why: "the first push of a pull can be the red one",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stubs := filepath.Join(dir, "stubs")
			if err := os.Mkdir(stubs, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.bodyFile != "" {
				if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte(c.bodyFile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ghLog := filepath.Join(dir, "gh-calls")
			sent := filepath.Join(dir, "sent-body")
			writeStub(t, stubs, "gh", recordArgv(ghLog)+`case "$*" in
  *--method*)
    for a do
      case $a in body=@*) cat "${a#body=@}" > `+shq(sent)+` || exit 1 ;; esac
    done ;;
  *) printf '%s\n' `+shq(c.existing)+` ;;
esac
`)

			run := runStep(t, script, unspecifiedShell, dir, []string{
				"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
				"HOME=" + dir,
				"RUNNER_TEMP=" + dir,
				"GH_TOKEN=stub-token",
				"GH_REPO_SLUG=" + slug,
				"PR_NUMBER=7",
				"COMPOSE_OUTCOME=" + c.outcome,
				"HEAD_SHA=" + headSHA,
				"GITHUB_SERVER_URL=https://github.com",
				"GITHUB_RUN_ID=" + runID,
			})
			if run.exit != 0 {
				t.Fatalf("the upsert exited %d, want 0 — %s\n%s", run.exit, c.why, run)
			}

			calls := recordedCalls(t, ghLog)
			if len(calls) != 2 {
				t.Fatalf("the upsert called gh %d times, want 2 (find the comment, then write it) — %s\n%v\n%s",
					len(calls), c.why, calls, run)
			}
			write := strings.Join(calls[1], " ")
			want := "api --method POST repos/" + slug + "/issues/7/comments"
			if c.existing != "" {
				want = "api --method PATCH repos/" + slug + "/issues/comments/" + c.existing
			}
			if !strings.HasPrefix(write, want+" ") {
				t.Errorf("the upsert ran `gh %s`, want `gh %s …` — %s", write, want, c.why)
			}

			body := readIfAny(t, sent)
			if c.posted {
				if body != composed {
					t.Errorf("the upsert sent %q, want the body glyph composed (%q) — %s", body, composed, c.why)
				}
				return
			}
			for _, part := range []string{
				preview.Marker + "\n",
				"could not compute a release verdict at `" + headSHA[:7] + "`",
				"commit-lint", runURL,
			} {
				if !strings.Contains(body, part) {
					t.Errorf("the fallback body lacks %q — it must carry the marker the next push finds it by, "+
						"say no verdict was computed at the head it names, and point at commit-lint and this "+
						"run's log — %s\nsent: %q", part, c.why, body)
				}
			}
			// The same body opens the comment on a pull whose first push is red,
			// where no earlier verdict exists to speak of.
			if strings.Contains(body, "earlier") {
				t.Errorf("the fallback body refers to an earlier verdict, which a first red push never had\nsent: %q", body)
			}
			if !strings.HasPrefix(body, preview.Marker) {
				t.Errorf("the fallback body does not open with %q, so the next push's lookup (startswith) "+
					"cannot find it and posts a second comment\nsent: %q", preview.Marker, body)
			}
			if strings.Contains(body, "the verdict glyph composed") {
				t.Errorf("the upsert posted the composed body after a %s compose — %s\nsent: %q", c.outcome, c.why, body)
			}
		})
	}
}

// TestPRVerdictUpsertRunsAfterAFailedCompose pins what the harness above cannot
// see: that the runner reaches the upsert at all when the compose step failed,
// and hands it the compose step's outcome and the pull's head. A step with no
// `if:` runs only while every step before it succeeded — the skip that kept a
// stale verdict on glyph-test run 37306205001 — and `!cancelled()` rather than
// `always()` keeps a cancelled run (a newer push superseding this one under the
// caller's concurrency group) from writing over the newer push's comment.
func TestPRVerdictUpsertRunsAfterAFailedCompose(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(prVerdictWorkflow))

	if got := readStepShape(t, raw, upsertStep).keys["if"]; got != "${{ !cancelled() }}" {
		t.Errorf("the upsert step's condition is %q, want ${{ !cancelled() }} — without it a failed compose "+
			"skips the upsert and the previous push's verdict stays on the pull", got)
	}
	if got := readStepShape(t, raw, composeStep).keys["id"]; got != "verdict" {
		t.Fatalf("the compose step's id is %q, want verdict — the upsert reads steps.verdict.outcome", got)
	}

	i := strings.Index(raw, "- name: "+upsertStep+"\n")
	if i < 0 {
		t.Fatalf("no step is named %q", upsertStep)
	}
	header, _, ok := strings.Cut(raw[i:], "run: |")
	if !ok {
		t.Fatalf("the %q step has no run: block", upsertStep)
	}
	for _, line := range []string{
		"COMPOSE_OUTCOME: ${{ steps.verdict.outcome }}",
		"HEAD_SHA: ${{ github.event.pull_request.head.sha }}",
	} {
		if !strings.Contains(header, line) {
			t.Errorf("the upsert step's env lacks %q — the harness supplies it by name, so without the "+
				"wiring the fallback the harness proves never sees what it decides on", line)
		}
	}
}
