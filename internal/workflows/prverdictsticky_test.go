package workflows

// pr-verdict.yml's sticky-comment upsert, executed. The step runs under the
// unspecified-shell invocation with real jq and a gh stub that answers the comment
// lookup and records each write and the body it was handed. The step's env: block —
// which contexts feed COMPOSE_OUTCOME, HEAD_SHA and the rest — is outside the
// harness: it supplies each by name, and TestPRVerdictUpsertRunsAfterAFailedCompose
// holds the wiring.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/preview"
)

const (
	prVerdictWorkflow = ".github/workflows/pr-verdict.yml"
	composeStep       = "Compose the verdict comment"
	upsertStep        = "Upsert the sticky comment"
)

// ghComment is a row of GET /repos/{owner}/{repo}/issues/{n}/comments, as much of
// it as the upsert reads.
type ghComment struct {
	ID   int64  `json:"id"`
	User ghUser `json:"user"`
	Body string `json:"body"`
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

var (
	actionsBot = ghUser{Login: "github-actions[bot]", Type: "Bot"}
	aPerson    = ghUser{Login: "akira-toriyama", Type: "User"}
)

// commentPages is the comment listing as `gh api --paginate --slurp` prints it: one
// array of the pages. A pull with no comment is one empty page, `[[]]`.
func commentPages(t *testing.T, pages ...[]ghComment) string {
	t.Helper()
	listing, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	return string(listing)
}

// writeUpsertGH stubs gh for the upsert. The lookup prints listing and exits
// lookupExit, and a write copies the body it is handed to sent. Each behaviour is
// the real one's, measured with gh 2.96.0 on glyph-test-2 #5 and #11 (2026-11-04):
// --paginate --slurp prints one array of the pages, --paginate --jq runs the filter
// on each page alone, --slurp beside --jq is refused, and an argument holding a
// newline never reaches the wire.
func writeUpsertGH(t *testing.T, stubs, log, sent, listing string, lookupExit int) {
	t.Helper()
	listingFile := filepath.Join(filepath.Dir(stubs), "comment-listing.json")
	if err := os.WriteFile(listingFile, []byte(listing), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStub(t, stubs, "gh", `newline='
'
case "$*" in
  *"$newline"*) echo 'net/url: invalid control character in URL' >&2; exit 1 ;;
esac
`+recordArgv(log)+`case "$*" in
  *--method*)
    for a do
      case $a in body=@*) cat "${a#body=@}" > `+shq(sent)+` || exit 1 ;; esac
    done ;;
  *--slurp*--jq* | *--jq*--slurp*)
    echo 'the --slurp option is not supported with --jq or --template' >&2
    exit 1 ;;
  *--slurp*)
    cat `+shq(listingFile)+`
    exit `+strconv.Itoa(lookupExit)+` ;;
  *--jq*)
    while [ "$1" != --jq ]; do shift; done
    jq -c '.[]' `+shq(listingFile)+` | while IFS= read -r page; do
      printf '%s' "$page" | jq -r "$2"
    done ;;
  *) echo "gh stub: the lookup asks for neither --slurp nor --jq: $*" >&2; exit 1 ;;
esac
`)
}

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
	requireTool(t, "jq")
	script := extractRun(t, repoFile(t, filepath.FromSlash(prVerdictWorkflow)), upsertStep)

	const (
		slug      = "akira-toriyama/glyph-test"
		headSHA   = "800189d4c0ffee00000000000000000000000001"
		runID     = "37306205001"
		composed  = preview.Marker + "\nthe verdict glyph composed at this head\n"
		commentID = 42
	)
	runURL := "https://github.com/" + slug + "/actions/runs/" + runID

	cases := []struct {
		name     string
		outcome  string // steps.verdict.outcome
		bodyFile string // what the compose step left in body.md; "" leaves none
		existing int64  // the sticky comment's id; 0 when the pull has none yet
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
			page := []ghComment{}
			if c.existing != 0 {
				page = append(page, ghComment{c.existing, actionsBot, preview.Marker + "\nan earlier verdict"})
			}
			writeUpsertGH(t, stubs, ghLog, sent, commentPages(t, page), 0)

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
			if c.existing != 0 {
				want = "api --method PATCH repos/" + slug + "/issues/comments/" + strconv.FormatInt(c.existing, 10)
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

// TestPRVerdictStickyLookupAnswersOneBotComment runs the upsert over comment
// listings and reads which comment it writes. The lookup hands one id to one
// write: the first comment a bot opened with the marker, across every page. A
// person's comment opening with the marker is left alone — this job's token edits
// it and the person's text is gone (glyph-test-2 run 41816989920, on #12). Markers
// on two pages answered a --paginate --jq filter twice, and the two-line id made a
// PATCH URL gh refuses (`net/url: invalid control character in URL`, rc 1, run
// 41817273095, on #11 at 104 comments: --paginate asks for 100 a page). A lookup
// that fails stops the step: read as "no comment yet", it posts a second sticky
// comment. gh's two ways of failing print on stdout too, measured 2026-11-04: `[]`
// on a refused connection, which jq answers empty and exit 0 so only pipefail holds
// it, and the error body on a 404, which jq itself refuses.
func TestPRVerdictStickyLookupAnswersOneBotComment(t *testing.T) {
	requireTool(t, "jq")
	script := extractRun(t, repoFile(t, filepath.FromSlash(prVerdictWorkflow)), upsertStep)

	const (
		slug   = "akira-toriyama/glyph-test-2"
		pull   = "5"
		sticky = preview.Marker + "\nthe sticky verdict"
	)
	post := "api --method POST repos/" + slug + "/issues/" + pull + "/comments"
	patch := func(id int) string {
		return "api --method PATCH repos/" + slug + "/issues/comments/" + strconv.Itoa(id)
	}
	otherBot := ghUser{Login: "another-app[bot]", Type: "Bot"}

	// run executes the upsert with gh's lookup printing listing and exiting lookupExit.
	run := func(t *testing.T, listing string, lookupExit int) (stepRun, [][]string) {
		t.Helper()
		dir := t.TempDir()
		stubs := filepath.Join(dir, "stubs")
		if err := os.Mkdir(stubs, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte(sticky), 0o600); err != nil {
			t.Fatal(err)
		}
		ghLog := filepath.Join(dir, "gh-calls")
		writeUpsertGH(t, stubs, ghLog, filepath.Join(dir, "sent-body"), listing, lookupExit)
		r := runStep(t, script, unspecifiedShell, dir, []string{
			"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"),
			"HOME=" + dir,
			"RUNNER_TEMP=" + dir,
			"GH_TOKEN=stub-token",
			"GH_REPO_SLUG=" + slug,
			"PR_NUMBER=" + pull,
			"COMPOSE_OUTCOME=success",
		})
		return r, recordedCalls(t, ghLog)
	}

	cases := []struct {
		name  string
		pages [][]ghComment
		write string
		why   string
	}{
		{
			name: "a pull with no comment opens the sticky", write: post,
			pages: [][]ghComment{{}},
			why:   "the positive control for the empty answer",
		},
		{
			name: "the bot's sticky is edited in place", write: patch(7),
			pages: [][]ghComment{{{7, actionsBot, sticky}}},
			why:   "the positive control for a found comment",
		},
		{
			name: "a sticky on a later page is found", write: patch(9),
			pages: [][]ghComment{{{8, aPerson, "first"}}, {{9, actionsBot, sticky}}},
			why:   "the lookup reads every page, or a busy pull grows a second sticky",
		},
		{
			name: "a person's marker ahead of the sticky is passed over", write: patch(11),
			pages: [][]ghComment{{{10, aPerson, sticky}, {11, actionsBot, sticky}}},
			why:   "the job's token could edit the person's comment, and it is not the job's to overwrite",
		},
		{
			name: "a person's marker alone opens the sticky", write: post,
			pages: [][]ghComment{{{12, aPerson, sticky}}},
			why:   "no bot wrote it, so no sticky exists yet",
		},
		{
			name: "markers on two pages answer one id", write: patch(13),
			pages: [][]ghComment{{{13, actionsBot, sticky}}, {{14, aPerson, sticky}}},
			why:   "a per-page filter answers twice, and gh refuses the PATCH URL two lines make",
		},
		{
			name: "two bot markers on two pages answer the first", write: patch(15),
			pages: [][]ghComment{{{15, actionsBot, sticky}}, {{16, actionsBot, sticky}}},
			why:   "two runs racing to open the sticky can leave two, and every later push still needs one id",
		},
		{
			name: "two bot markers on one page answer the first", write: patch(17),
			pages: [][]ghComment{{{17, actionsBot, sticky}, {18, actionsBot, sticky}}},
			why:   "the same race on one page",
		},
		{
			name: "any bot's marker is the sticky", write: patch(19),
			pages: [][]ghComment{{{19, otherBot, sticky}}},
			why:   "a bot's name is its token's, so the filter reads the type and not the login",
		},
		{
			name: "a bot comment without the marker is another workflow's", write: post,
			pages: [][]ghComment{{{20, actionsBot, "a coverage report"}}},
			why:   "the marker is what makes a comment the sticky",
		},
		{
			name: "a marker past the start is a quotation", write: post,
			pages: [][]ghComment{{{21, actionsBot, "> " + preview.Marker}}},
			why:   "the marker leads the body it marks",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, calls := run(t, commentPages(t, c.pages...), 0)
			if r.exit != 0 {
				t.Fatalf("the upsert exited %d, want 0 — %s\n%s", r.exit, c.why, r)
			}
			if len(calls) != 2 {
				t.Fatalf("the upsert called gh %d times, want 2 (find the comment, then write it) — %s\n%v\n%s",
					len(calls), c.why, calls, r)
			}
			if write := strings.Join(calls[1], " "); !strings.HasPrefix(write, c.write+" ") {
				t.Errorf("the upsert ran `gh %s`, want `gh %s …` — %s", write, c.write, c.why)
			}
		})
	}

	for _, c := range []struct {
		name, listing string
		exit          int
	}{
		{"a refused connection writes nothing", "[]", 1},
		{"an HTTP error writes nothing", `[{"message":"Not Found","status":"404"}]`, 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, calls := run(t, c.listing, 1)
			if r.exit != c.exit {
				t.Errorf("the upsert exited %d after a failed lookup, want %d\n%s", r.exit, c.exit, r)
			}
			if len(calls) != 1 {
				t.Errorf("the upsert went on past a failed lookup (%v) — read as \"no comment yet\", the POST opens a "+
					"second sticky comment\n%s", calls, r)
			}
		})
	}
}

// TestPRVerdictJobRunsOnlyOnASameRepoPull pins the job's condition, the one thing
// keeping this job off a fork's read-only token and off an event with no pull: a
// push-triggered caller's job reports skipped with no step run (glyph-test-2 run
// 41809789871). That skip is why the compose step carries no guard of its own: a
// check inside the job for an empty PR number cannot run on the only events that
// lack one, and it reads as a loud refusal nobody would ever get.
func TestPRVerdictJobRunsOnlyOnASameRepoPull(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(prVerdictWorkflow))

	const condition = "    if: github.event.pull_request.head.repo.full_name == github.repository\n"
	if !strings.Contains(code(raw), condition) {
		t.Errorf("the verdict job's condition is no longer %q — a fork pull's read-only token 403s on the comment "+
			"POST and a push-triggered caller's job runs with no pull to preview", strings.TrimSpace(condition))
	}
	script := extractRun(t, raw, composeStep)
	if !strings.Contains(script, `glyph preview --pr "$PR_NUMBER"`) {
		t.Fatalf("canary: the compose step does not run glyph preview, so the absence below is over the wrong script:\n%s", script)
	}
	if strings.Contains(script, "pull_request context") {
		t.Errorf("the compose step refuses a missing pull_request context again — no event without a pull reaches a " +
			"step of this job, so the refusal is dead code reading as a live check")
	}
}
