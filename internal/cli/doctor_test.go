package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorRepoPath is the one endpoint doctor reads: the repository object for
// the repository these tests name.
const doctorRepoPath = "/repos/akira-toriyama/glyph"

// healthySettings is the repository configuration a fleet repo is supposed to
// have — squash-only, with the squash subject and body policy that keeps a
// classifiable gitmoji on main.
const healthySettings = `"allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":false,` +
	`"squash_merge_commit_title":"COMMIT_OR_PR_TITLE","squash_merge_commit_message":"COMMIT_MESSAGES"`

// apiRepoObject renders the repository object GET /repos/{owner}/{repo}
// returns. settings is spliced in verbatim so a test can omit fields entirely —
// which is not the same as setting them false, and is exactly the case the
// *bool decoding exists for.
func apiRepoObject(settings string) string {
	body := `{"full_name":"akira-toriyama/glyph","private":false,"visibility":"public",` +
		`"permissions":{"admin":true,"maintain":true,"push":true,"triage":true,"pull":true}`
	if settings != "" {
		body += "," + settings
	}
	return body + "}"
}

// doctorServer stands in for api.github.com. It answers the repository object
// and fails the test on anything else — including any method but GET, which is
// how "doctor never mutates anything" is enforced by the harness rather than by
// review: a future check that PATCHes a setting to fix it fails here.
func doctorServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("doctor sent %s %s — doctor is read-only and must never write", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != doctorRepoPath {
			t.Errorf("unexpected request %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pinnedCaller is a caller stub as fleet-sync distributes it: a permanently
// stale COMMENTED example in the header (frozen at v0.9.0), a commented
// counter-example on a moving ref, and then the real, correctly pinned `uses:`.
// Every doctor test runs in a checkout carrying it, so a scan that reads
// comments as pins fails most of this file rather than one case — and the
// counter-example line is what makes that failure show up here at all: a
// comment holding a stale but valid TAG would still satisfy this check, and the
// naive scan would slip through end to end.
const pinnedCaller = `name: commit-lint

# Copy this stub into a family repo:
#
#   jobs:
#     lint:
#       uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.9.0  # pin a release tag
#
# Never this — a moving ref changes the workflow AND the binary under you:
#       uses: akira-toriyama/glyph/.github/workflows/lint.yml@main
on:
  pull_request:
jobs:
  lint:
    uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1
`

// driftedCaller is the same stub with the real reference on a moving ref, which
// is the drift the pin check exists to catch.
const driftedCaller = `name: commit-lint
#   uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.1
jobs:
  lint:
    uses: akira-toriyama/glyph/.github/workflows/lint.yml@main
`

// useDoctorCheckout builds a throwaway checkout holding one workflow file and
// makes it the working directory — doctor reads the LOCAL tree for the pin
// check. No git repository is needed: doctor runs no git command.
func useDoctorCheckout(t *testing.T, workflow string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if workflow != "" {
		if err := os.WriteFile(filepath.Join(dir, "commit-lint.yml"), []byte(workflow), 0o600); err != nil {
			t.Fatalf("write workflow: %v", err)
		}
	}
	t.Chdir(root)
}

// doctorReport is the decoded --json surface. Declaring it here pins the keys:
// a renamed field breaks the decode, where a substring assertion would not.
type doctorReport struct {
	Repo   string `json:"repo"`
	Checks []struct {
		ID       string   `json:"id"`
		Status   string   `json:"status"`
		Observed string   `json:"observed"`
		Expected string   `json:"expected"`
		Message  string   `json:"message"`
		Fix      string   `json:"fix"`
		Details  []string `json:"details"`
	} `json:"checks"`
	Counts struct {
		Pass    int `json:"pass"`
		Fail    int `json:"fail"`
		Advice  int `json:"advice"`
		Unknown int `json:"unknown"`
	} `json:"counts"`
	OK bool `json:"ok"`
}

// runDoctorJSON runs doctor against a repository object and decodes the report.
func runDoctorJSON(t *testing.T, settings, workflow string) (int, doctorReport, string) {
	t.Helper()
	usePR(t, doctorServer(t, apiRepoObject(settings)))
	useDoctorCheckout(t, workflow)
	code, stdout, stderr := runGlyph(t, "doctor", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, stdout)
	}
	return code, rep, stderr
}

// status returns one check's status by id, failing the test when the id is
// absent — the ids are the report's API, so a missing one is a contract break.
func status(t *testing.T, rep doctorReport, id string) string {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c.Status
		}
	}
	t.Fatalf("no check %q in the report (ids are the stable machine surface)", id)
	return ""
}

// TestDoctorHealthyRepositoryPasses: the configuration the fleet is supposed to
// have, in a checkout whose caller stub is pinned — every check passes and the
// run exits 0, with no annotation on the diagnostic stream.
func TestDoctorHealthyRepositoryPasses(t *testing.T) {
	usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
	useDoctorCheckout(t, pinnedCaller)

	code, stdout, stderr := runGlyph(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor on a healthy repository exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "7 checks: 7 pass, 0 fail, 0 advice, 0 could not run") {
		t.Errorf("summary line missing or wrong:\n%s", stdout)
	}
	if !strings.Contains(stdout, "read-only") {
		t.Errorf("the human report must state that doctor changed nothing:\n%s", stdout)
	}
	if strings.Contains(stderr, "::warning::") || strings.Contains(stderr, "::notice::") {
		t.Errorf("a clean repository must produce no annotation, got:\n%s", stderr)
	}
}

// TestDoctorJSONShape pins the machine surface a CI job consumes: the stable
// ids, in a stable order, each carrying the observed/expected pair and a
// message, plus the tally and the single boolean a gate branches on.
func TestDoctorJSONShape(t *testing.T) {
	code, rep, _ := runDoctorJSON(t, healthySettings, pinnedCaller)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	want := []string{
		"token-repo-read",
		"squash-merge-enabled",
		"merge-commit-enabled",
		"rebase-merge-enabled",
		"squash-commit-title",
		"squash-commit-message",
		"workflow-glyph-pins",
	}
	if len(rep.Checks) != len(want) {
		t.Fatalf("report carries %d checks, want %d", len(rep.Checks), len(want))
	}
	for i, id := range want {
		if rep.Checks[i].ID != id {
			t.Errorf("check %d is %q, want %q — the order is part of the contract", i, rep.Checks[i].ID, id)
		}
		for field, value := range map[string]string{
			"status":   rep.Checks[i].Status,
			"observed": rep.Checks[i].Observed,
			"expected": rep.Checks[i].Expected,
			"message":  rep.Checks[i].Message,
		} {
			if value == "" {
				t.Errorf("check %q has an empty %s; every check must report all four", id, field)
			}
		}
	}
	if rep.Repo != "akira-toriyama/glyph" {
		t.Errorf("repo = %q, want the diagnosed repository", rep.Repo)
	}
	if !rep.OK || rep.Counts.Pass != 7 {
		t.Errorf("counts = %+v ok=%t, want 7 pass and ok", rep.Counts, rep.OK)
	}
}

// TestDoctorSquashPolicyDriftFails is the drift that motivated the command:
// glyph-test sat on PR_TITLE / PR_BODY for weeks with every workflow green.
// Both halves must fail (exit 3), name the consequence, and hand over the exact
// command that fixes them.
func TestDoctorSquashPolicyDriftFails(t *testing.T) {
	drifted := `"allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":false,` +
		`"squash_merge_commit_title":"PR_TITLE","squash_merge_commit_message":"PR_BODY"`
	usePR(t, doctorServer(t, apiRepoObject(drifted)))
	useDoctorCheckout(t, pinnedCaller)

	code, stdout, stderr := runGlyph(t, "doctor")
	if code != 3 {
		t.Fatalf("a drifted squash policy exited %d, want 3 (the repository violates what glyph enforces)", code)
	}
	for _, want := range []string{
		"squash_merge_commit_title=PR_TITLE",
		"gh api -X PATCH repos/akira-toriyama/glyph -f squash_merge_commit_title=COMMIT_OR_PR_TITLE",
		"gh api -X PATCH repos/akira-toriyama/glyph -f squash_merge_commit_message=COMMIT_MESSAGES",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not say what to DO — missing %q:\n%s", want, stdout)
		}
	}
	// The findings must also reach an Actions log as annotations...
	if strings.Count(stderr, "::warning::") != 2 {
		t.Errorf("want one ::warning:: annotation per failing check:\n%s", stderr)
	}
	// ...and the envelope must carry the failing ids as machine details.
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if env.Code != 3 {
		t.Fatalf("envelope code = %d, want 3", env.Code)
	}
	var ids []string
	if err := json.Unmarshal(env.Details, &ids); err != nil {
		t.Fatalf("details are not a list of ids: %v", err)
	}
	if len(ids) != 2 || ids[0] != "squash-commit-title" || ids[1] != "squash-commit-message" {
		t.Errorf("details = %v, want both squash policy ids", ids)
	}
}

// TestDoctorMergeMethodsAreAdviceNotFailure pins the deliberate severity call.
// A merge commit used to vanish from the release walk entirely (t-7zt7); with
// that fixed, the setting costs no bump and reporting it as a failure would be
// crying wolf — so it is advice, it annotates as a ::notice::, and it must NOT
// move the exit code.
func TestDoctorMergeMethodsAreAdviceNotFailure(t *testing.T) {
	permissive := `"allow_squash_merge":true,"allow_merge_commit":true,"allow_rebase_merge":true,` +
		`"squash_merge_commit_title":"COMMIT_OR_PR_TITLE","squash_merge_commit_message":"COMMIT_MESSAGES"`
	usePR(t, doctorServer(t, apiRepoObject(permissive)))
	useDoctorCheckout(t, pinnedCaller)

	code, stdout, stderr := runGlyph(t, "doctor")
	if code != 0 {
		t.Fatalf("permissive merge methods exited %d, want 0 — a house convention is not a gate", code)
	}
	if !strings.Contains(stdout, "5 pass, 0 fail, 2 advice") {
		t.Errorf("merge and rebase must report as advice:\n%s", stdout)
	}
	if strings.Count(stderr, "::notice::") != 2 {
		t.Errorf("advice must annotate as a notice, never a warning:\n%s", stderr)
	}
	if strings.Contains(stderr, "::warning::") {
		t.Errorf("no warning may be raised for a setting glyph handles correctly:\n%s", stderr)
	}
}

// TestDoctorSquashDisabledFails: squash being ENABLED is the load-bearing one —
// not because it is the only style the walk resolves (every style is; GitHub
// names whichever commit represents the merge), but because it is the only one
// that survives the API being dark. DESIGN §4's fallback classifies a commit
// from its own message, and only a squash commit is both the pull request's key
// and a classifiable gitmoji subject.
func TestDoctorSquashDisabledFails(t *testing.T) {
	off := `"allow_squash_merge":false,"allow_merge_commit":true,"allow_rebase_merge":false,` +
		`"squash_merge_commit_title":"COMMIT_OR_PR_TITLE","squash_merge_commit_message":"COMMIT_MESSAGES"`
	code, rep, _ := runDoctorJSON(t, off, pinnedCaller)
	if code != 3 {
		t.Fatalf("squash merging off exited %d, want 3", code)
	}
	if got := status(t, rep, "squash-merge-enabled"); got != "fail" {
		t.Errorf("squash-merge-enabled = %s, want fail", got)
	}
	if got := status(t, rep, "merge-commit-enabled"); got != "advice" {
		t.Errorf("merge-commit-enabled = %s, want advice even here — the severities are independent", got)
	}
	if rep.OK {
		t.Error("ok must be false when a check failed")
	}
}

// TestDoctorUnreportedSettingsAreUnknownNotFalse is the reason the settings
// decode into *bool. GitHub omits a repository's merge settings from the
// response given to a credential that may not see them; decoding that silence
// into false would report "squash merging is disabled" about a repository that
// has it on — a confident wrong answer from the tool whose whole job is
// telling the truth about configuration.
func TestDoctorUnreportedSettingsAreUnknownNotFalse(t *testing.T) {
	code, rep, stderr := runDoctorJSON(t, "", pinnedCaller)
	if code != 4 {
		t.Fatalf("a report with unreadable settings exited %d, want 4 (unverified, not verified-good)", code)
	}
	for _, id := range []string{"squash-merge-enabled", "merge-commit-enabled", "rebase-merge-enabled",
		"squash-commit-title", "squash-commit-message"} {
		if got := status(t, rep, id); got != "unknown" {
			t.Errorf("%s = %s, want unknown", id, got)
		}
	}
	// The two checks that did not need the settings still ran — independence.
	if got := status(t, rep, "token-repo-read"); got != "pass" {
		t.Errorf("token-repo-read = %s, want pass (the read itself worked)", got)
	}
	if got := status(t, rep, "workflow-glyph-pins"); got != "pass" {
		t.Errorf("workflow-glyph-pins = %s, want pass (it needs no API at all)", got)
	}
	if rep.OK {
		t.Error("ok must be false when a check could not run — nothing was proven")
	}
	if strings.Contains(stderr, "disabled") || strings.Contains(stderr, "=false") {
		t.Errorf("an unreported setting must never be described as off:\n%s", stderr)
	}
}

// TestDoctorTokenFailureDoesNotAbortTheReport: one failing API call must not
// take the rest of the report with it. The token check fails (that IS its
// question), the settings checks report could-not-run pointing at it, and the
// purely local pin check still answers.
func TestDoctorTokenFailureDoesNotAbortTheReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	useDoctorCheckout(t, pinnedCaller)

	code, stdout, _ := runGlyph(t, "doctor", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("the report must still be emitted when the API read fails: %v\n%s", err, stdout)
	}
	if code != 3 {
		t.Fatalf("exit %d, want 3 — a failed check outranks the ones it made unrunnable", code)
	}
	if got := status(t, rep, "token-repo-read"); got != "fail" {
		t.Errorf("token-repo-read = %s, want fail (a failed read answers this check's question)", got)
	}
	if got := status(t, rep, "squash-commit-title"); got != "unknown" {
		t.Errorf("squash-commit-title = %s, want unknown (it never observed anything)", got)
	}
	if got := status(t, rep, "workflow-glyph-pins"); got != "pass" {
		t.Errorf("workflow-glyph-pins = %s, want pass — the local check needs no credential", got)
	}
	for _, c := range rep.Checks {
		if c.ID == "token-repo-read" && !strings.Contains(c.Message, "404") {
			t.Errorf("the token failure must explain GitHub's 404-for-private answer: %q", c.Message)
		}
	}
}

// failingAPI points doctor at a server that answers every request with one
// status and body — the shapes where GitHub gives glyph no usable answer.
func failingAPI(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	usePR(t, srv)
	useDoctorCheckout(t, pinnedCaller)
}

// TestDoctorUnansweredAPIIsCouldNotRunNotAViolation is the exit-code contract
// the fleet already codifies, pinned end to end. A 403 rate limit tells glyph
// NOTHING about the repository, so calling it a failed check — and exiting 3,
// "this repository does not satisfy what glyph assumes about it" — is a verdict
// on evidence that was never collected. lint.yml (shipped from this repo)
// branches on `.error.code == 3` to hard-fail and treats everything else as a
// retryable infra failure, so a transient GitHub answer classified as 3 would
// hard-fail a healthy repository, un-retryably, while every other glyph command
// classifies the same answer as 4.
func TestDoctorUnansweredAPIIsCouldNotRunNotAViolation(t *testing.T) {
	failingAPI(t, http.StatusForbidden, `{"message":"API rate limit exceeded for 203.0.113.7"}`)

	code, stdout, _ := runGlyph(t, "doctor", "--json")
	var rep doctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("the report must still be emitted: %v\n%s", err, stdout)
	}
	if code != 4 {
		t.Fatalf("a rate-limited read exited %d, want 4 — nothing about the repository was observed", code)
	}
	if got := status(t, rep, "token-repo-read"); got != "unknown" {
		t.Errorf("token-repo-read = %s, want unknown: an answer glyph never got is not a finding", got)
	}
	if rep.Counts.Fail != 0 {
		t.Errorf("counts = %+v, want 0 fail — a report that says '0 fail' must not exit on a failure", rep.Counts)
	}
	if rep.Counts.Unknown != 6 {
		t.Errorf("counts = %+v, want the 6 API-backed checks unknown (the local pin check still ran)", rep.Counts)
	}
	if got := status(t, rep, "workflow-glyph-pins"); got != "pass" {
		t.Errorf("workflow-glyph-pins = %s, want pass — it needs no API at all", got)
	}

	// The same contract on the machine envelope a CI wrapper actually parses:
	// the code a jq branch reads must be 4, never the gate code. (--json makes
	// the error silent — the payload carries every field — so the envelope only
	// exists on the human run.)
	code, _, stderr := runGlyph(t, "doctor")
	if code != 4 {
		t.Fatalf("the human run exited %d, want 4", code)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if env.Code != 4 {
		t.Fatalf("envelope code = %d, want 4 — `.error.code == 3` means a convention violation to the fleet's wrappers", env.Code)
	}
}

// TestDoctorAnnotationsStayOnOneLine: a workflow command is one physical line —
// the runner parses `::warning::` up to the first newline and drops the rest —
// and the text here is not glyph's own. A proxy or gateway behind an enterprise
// GITHUB_API_URL answers with an HTML page, which interpolated raw would push
// the expected/fix text the annotation exists to deliver onto lines the job
// summary never shows.
func TestDoctorAnnotationsStayOnOneLine(t *testing.T) {
	failingAPI(t, http.StatusNotFound, "<html>\r\n<head><title>404 Not Found</title></head>\n<body>\n<center>nginx</center>\n</body>\n</html>")

	code, _, stderr := runGlyph(t, "doctor", "--json")
	if code != 3 {
		t.Fatalf("exit %d, want 3 — a 404 is GitHub answering about the repository", code)
	}
	for line := range strings.SplitSeq(strings.TrimRight(stderr, "\n"), "\n") {
		if !strings.HasPrefix(line, "::") {
			t.Fatalf("a remote error body broke the annotation onto its own line %q; whole stream:\n%s", line, stderr)
		}
	}
	if !strings.Contains(stderr, "</html>") || !strings.Contains(stderr, "fix:") && !strings.Contains(stderr, "GITHUB_TOKEN") {
		t.Errorf("the folded annotation must still carry the whole error and the fix:\n%s", stderr)
	}
}

// TestDoctorReadsTheRealUsesNotTheCommentedStub is the trap end to end. The API
// side is perfect, so the exit code is entirely the pin check's verdict: a
// stale COMMENTED stub above a good pin must pass, and a good comment above a
// moving real ref must fail and point at the line to edit.
func TestDoctorReadsTheRealUsesNotTheCommentedStub(t *testing.T) {
	t.Run("stale comment over a real pin", func(t *testing.T) {
		code, rep, _ := runDoctorJSON(t, healthySettings, pinnedCaller)
		if code != 0 {
			t.Fatalf("exit %d, want 0 — the permanently stale caller stub in a comment is documentation, not a pin", code)
		}
		if got := status(t, rep, "workflow-glyph-pins"); got != "pass" {
			t.Errorf("workflow-glyph-pins = %s, want pass", got)
		}
	})
	t.Run("good comment over a moving real ref", func(t *testing.T) {
		code, rep, _ := runDoctorJSON(t, healthySettings, driftedCaller)
		if code != 3 {
			t.Fatalf("exit %d, want 3 — the executable line is on @main", code)
		}
		var details []string
		for _, c := range rep.Checks {
			if c.ID == "workflow-glyph-pins" {
				details = c.Details
			}
		}
		joined := strings.Join(details, "\n")
		if !strings.Contains(joined, ".github/workflows/commit-lint.yml:5") {
			t.Errorf("the finding must point at the executable line (5), got:\n%s", joined)
		}
		if !strings.Contains(joined, "@main") {
			t.Errorf("the finding must name the moving ref it read, got:\n%s", joined)
		}
	})
}

// TestDoctorReportsTheConfiguredCredential: doctor states which credential it
// used and what the API said about it — never a guess at scopes the API did not
// report.
func TestDoctorReportsTheConfiguredCredential(t *testing.T) {
	usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
	t.Setenv("GITHUB_TOKEN", "a-token")
	useDoctorCheckout(t, pinnedCaller)

	code, stdout, _ := runGlyph(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "a token from GITHUB_TOKEN/GH_TOKEN") {
		t.Errorf("the report must name the credential source:\n%s", stdout)
	}
	if !strings.Contains(stdout, "permissions: admin, maintain, push, triage and pull") {
		t.Errorf("the report must surface what the API said about the credential:\n%s", stdout)
	}
}

// TestDoctorWithoutARepositoryIsUsage: with neither --repo nor
// GITHUB_REPOSITORY there is nothing to diagnose, and nothing has been sent —
// so it is the caller's input (2), the same answer bump and notes give.
func TestDoctorWithoutARepositoryIsUsage(t *testing.T) {
	useDoctorCheckout(t, pinnedCaller)
	code, _, stderr := runGlyph(t, "doctor")
	if code != 2 {
		t.Fatalf("doctor with no repository exited %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "repo") {
		t.Fatalf("the error should name the missing repository input:\n%s", stderr)
	}
}

// TestDoctorTakesNoPositionalArgs: a stray argument is a typo'd flag more often
// than not, and silently diagnosing $GITHUB_REPOSITORY anyway would hide it.
func TestDoctorTakesNoPositionalArgs(t *testing.T) {
	useDoctorCheckout(t, pinnedCaller)
	if code, _, _ := runGlyph(t, "doctor", "akira-toriyama/glyph"); code != 2 {
		t.Fatalf("a positional argument exited %d, want 2 (usage)", code)
	}
}
