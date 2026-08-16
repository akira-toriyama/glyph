package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/hook"
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
// makes it the working directory — doctor reads the LOCAL tree for both of its
// offline checks.
//
// It is a REAL git repository, because doctor asks git where hooks live rather
// than assuming .git/hooks (core.hooksPath relocates them). A bare directory
// would make the hook check could-not-run in every test here, which is a true
// answer to a question none of them are asking. No commit and no hook is
// installed, so these tests also hold the line that an ABSENT hook passes: a
// fresh clone has none, an Actions checkout cannot have one, and a check that
// spoke up about it would speak up on every run in every repository.
func useDoctorCheckout(t *testing.T, workflow string) {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "akira-toriyama", "init", "-q", "-b", "main")
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
	return code, decodeDoctorJSON(t, stdout), stderr
}

// decodeDoctorJSON decodes a doctor --json payload whose fixture the caller has
// already arranged — the firing tests need to install a hook and stub PATH
// between the checkout and the run, which runDoctorJSON's one-call shape
// cannot express.
func decodeDoctorJSON(t *testing.T, stdout string) doctorReport {
	t.Helper()
	var rep doctorReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, stdout)
	}
	return rep
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
	if !strings.Contains(stdout, "12 checks: 12 pass, 0 fail, 0 advice, 0 could not run") {
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
		"token-repo-write",
		"squash-merge-enabled",
		"merge-commit-enabled",
		"rebase-merge-enabled",
		"squash-commit-title",
		"squash-commit-message",
		"workflow-glyph-pins",
		"workflow-caller-permissions",
		"commit-msg-hook",
		"commit-msg-hook-fires",
		"pre-push-hook",
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
	if !rep.OK || rep.Counts.Pass != 12 {
		t.Errorf("counts = %+v ok=%t, want 12 pass and ok", rep.Counts, rep.OK)
	}
}

// installCurrentHook writes THIS binary's commit-msg hook into the checkout's
// hooks directory, exactly as `glyph hook install` would — the byte-identical
// arm, the only one the probe fires.
func installCurrentHook(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(".git/hooks", 0o750); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	if err := os.WriteFile(".git/hooks/commit-msg", []byte(hook.Kinds("")[0].Script), 0o700); err != nil { // #nosec G306 -- a hook must be executable
		t.Fatalf("install hook: %v", err)
	}
}

// stubGlyphOnPATH prepends a directory holding a fake `glyph` that exits with
// code, standing in for whatever the hook's `command -v glyph` resolves to on a
// developer machine — a healthy binary (the gate code) or a wrapper whose
// checkout is broken (anything else).
func stubGlyphOnPATH(t *testing.T, code int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nexit %d\n", code)
	if err := os.WriteFile(filepath.Join(dir, "glyph"), []byte(script), 0o700); err != nil { // #nosec G306 -- PATH stubs must be executable
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestDoctorFiresTheCurrentHook is the live half of the hook diagnosis: doctor
// must EXECUTE the byte-identical commit-msg hook, not just compare its bytes.
// The bytes prove the script is current; they prove nothing about the glyph the
// script resolves on PATH at run time — and the script waves every non-gate
// failure through by design, so a wrapper building a different checkout (the
// documented worktree trap) or a tree that does not compile is a local gate
// that answers 0 to everything while its bytes compare clean. That silent
// no-op is exactly what the byte-compare check reported as healthy.
func TestDoctorFiresTheCurrentHook(t *testing.T) {
	t.Run("a working glyph blocks the probe", func(t *testing.T) {
		usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
		useDoctorCheckout(t, pinnedCaller)
		installCurrentHook(t)
		stubGlyphOnPATH(t, 3)

		code, stdout, stderr := runGlyph(t, "doctor", "--json")
		rep := decodeDoctorJSON(t, stdout)
		if code != 0 {
			t.Fatalf("doctor exited %d, want 0\nstderr: %s", code, stderr)
		}
		c := checkByID(t, rep, "commit-msg-hook-fires")
		if c.Status != "pass" || !strings.Contains(c.Observed, "blocked") {
			t.Errorf("a hook that blocks the violating probe must pass with the block observed, got %s: %s",
				c.Status, c.Observed)
		}
	})

	t.Run("a broken glyph is a silent no-op, and that is a FAIL", func(t *testing.T) {
		usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
		useDoctorCheckout(t, pinnedCaller)
		installCurrentHook(t)
		// 127: what the wrapper yields when its source clone is gone or the
		// build fails. The hook forwards only the gate code and exits 0 — the
		// quiet direction.
		stubGlyphOnPATH(t, 127)

		code, stdout, stderr := runGlyph(t, "doctor", "--json")
		rep := decodeDoctorJSON(t, stdout)
		if code != 3 {
			t.Fatalf("a silently dead local gate must fail doctor (exit 3), got %d\nstderr: %s", code, stderr)
		}
		c := checkByID(t, rep, "commit-msg-hook-fires")
		if c.Status != "fail" {
			t.Errorf("the probe passing through must FAIL the check, got %s: %s", c.Status, c.Observed)
		}
		if !strings.Contains(stderr, "commit-msg-hook-fires") || !strings.Contains(stderr, "::warning::") {
			t.Errorf("the silent no-op must annotate:\n%s", stderr)
		}
		// The sibling byte-compare check must still PASS here — the split is
		// the whole finding: current bytes, dead gate.
		if s := checkByID(t, rep, "commit-msg-hook"); s.Status != "pass" {
			t.Errorf("the byte-compare must still pass (the bytes ARE current), got %s", s.Status)
		}
	})

	t.Run("a foreign hook is not fired", func(t *testing.T) {
		usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
		useDoctorCheckout(t, pinnedCaller)
		if err := os.MkdirAll(".git/hooks", 0o750); err != nil {
			t.Fatalf("mkdir hooks: %v", err)
		}
		if err := os.WriteFile(".git/hooks/commit-msg", []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { // #nosec G306
			t.Fatalf("install foreign hook: %v", err)
		}
		// A PATH stub that would make any firing VISIBLE as a wrong verdict:
		// were the foreign hook executed, it would exit 1 and the check could
		// not report "nothing to fire".
		stubGlyphOnPATH(t, 3)

		_, stdout, _ := runGlyph(t, "doctor", "--json")
		rep := decodeDoctorJSON(t, stdout)
		c := checkByID(t, rep, "commit-msg-hook-fires")
		if c.Status != "pass" || !strings.Contains(c.Observed, "nothing to fire") {
			t.Errorf("someone else's hook is theirs to run — the probe must not fire it, got %s: %s",
				c.Status, c.Observed)
		}
	})
}

// checkByID returns the one check with the given id, failing the test if the
// report does not carry it.
func checkByID(t *testing.T, rep doctorReport, id string) struct {
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Observed string   `json:"observed"`
	Expected string   `json:"expected"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix"`
	Details  []string `json:"details"`
} {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report carries no %q check", id)
	panic("unreachable")
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
	if !strings.Contains(stdout, "10 pass, 0 fail, 2 advice") {
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
		// Retry-After: 0 is a timing knob, not a claim about GitHub's wire
		// shape: a real primary-rate-limit 403 (this body) carries no such
		// header, and github.go reserves a header-bearing 403 for the
		// secondary-limit path. Without it the retryable statuses here fall
		// back to the 1s/4s/16s default schedule, twice per doctor run — 42 of
		// this package's 69 test seconds were that sleep. Header honouring is
		// itself pinned by internal/github/retry_test.go.
		w.Header().Set("Retry-After", "0")
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
	if rep.Counts.Unknown != 7 {
		t.Errorf("counts = %+v, want the 7 API-backed checks unknown (the local pin check still ran)", rep.Counts)
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

// runGlyphCtx is runGlyph with a caller-owned context, for the one family of
// tests that needs to cancel mid-run — the wiring a live SIGINT/SIGTERM uses.
func runGlyphCtx(t *testing.T, ctx context.Context, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	oldOut, oldErr := out, errOut
	out, errOut = &outBuf, &errBuf
	defer func() { out, errOut = oldOut, oldErr }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&errBuf)
	root.SetErr(&errBuf)
	code = finish(root.ExecuteContext(ctx))
	return code, outBuf.String(), errBuf.String()
}

// TestFirstInterruptChecksEveryRead pins the predicate doctorRun guards its two
// independent reads with. The order matters only for which error carries out;
// what must never regress is that the SECOND position is consulted at all —
// see TestDoctorInterruptDuringHooksReadCarriesOut for why cancelling before
// the run cannot cover that.
func TestFirstInterruptChecksEveryRead(t *testing.T) {
	interrupt := &core.Error{Code: core.CodeInterrupted, Msg: "interrupted", Silent: true}
	plain := core.APIf("git: boom")

	if got := firstInterrupt(nil, nil); got != nil {
		t.Errorf("firstInterrupt(nil, nil) = %v, want nil", got)
	}
	if got := firstInterrupt(plain, nil); got != nil {
		t.Errorf("a plain failure is not an abort; got %v, want nil", got)
	}
	if got := firstInterrupt(nil, interrupt); got == nil {
		t.Error("an interrupt in the SECOND read was not seen — that is the laundering defect")
	}
	if got := firstInterrupt(interrupt, plain); !errors.Is(got, error(interrupt)) {
		t.Errorf("the first interrupt should carry out verbatim; got %v", got)
	}
}

// TestDoctorInterruptDuringHooksReadCarriesOut pins the guard over doctor's
// SECOND read. Both reads run before the guard, and the signal lands in
// whichever is in flight; measured before the fix, a SIGTERM landing in the
// hooks read exited 4 — stdout carried a report with commit-msg-hook rendered
// as "could not run: … interrupted" — and 4 is the one code the fleet's
// wrappers read as retryable infra, so CI would retry a run its operator
// stopped. The same signal landing in the API read already exited 130 with
// nothing written: same command, same signal, different answer by landing
// site.
//
// The choreography exists because the trivial arrangement proves nothing:
// cancelling the context BEFORE the run makes the API read interrupted and the
// rerr half of the guard fires first, green with or without the herr half. So
// the API read completes against a healthy fake, and only when the fake git
// signals that the hooks read is in flight is the context cancelled.
func TestDoctorInterruptDuringHooksReadCarriesOut(t *testing.T) {
	srv := doctorServer(t, apiRepoObject(healthySettings))
	usePR(t, srv)
	useDoctorCheckout(t, pinnedCaller)

	// A fake git that reports being asked, then blocks. `exec` hands the pipe
	// fds to a /dev/null-redirected sleep so the harness's output copy sees
	// EOF at once and the kill on cancel is all Wait waits for.
	bin := t.TempDir()
	asked := filepath.Join(bin, "asked")
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\ntouch " + asked + "\nexec sleep 30 </dev/null >/dev/null 2>&1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for range 400 {
			if _, err := os.Stat(asked); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel() // safety net: never leave the run behind the fake's 30s block
	}()

	code, stdout, _ := runGlyphCtx(t, ctx, "doctor", "--json")
	if code != 130 {
		t.Fatalf("doctor exited %d, want 130 — an interrupt in the hooks read is the user's own abort, not a check result", code)
	}
	if stdout != "" {
		t.Errorf("doctor wrote a report over an interrupted run (the abort was laundered into a finding):\n%s", stdout)
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

// TestDoctorFindsAStaleInstalledHook is the end-to-end half of the hook check:
// a real checkout, a real hook file on disk, and git asked where hooks live.
//
// The stale hook is the failure internal/hook's code interpolation prevents
// when glyph WRITES the script and cannot prevent afterwards — hooks are
// untracked, so nothing refreshes one and the copy on disk keeps comparing
// against whatever number it was born with. It exits 0 on a real violation,
// which is indistinguishable from a clean message, which is why doctor has to
// be the thing that notices.
func TestDoctorFindsAStaleInstalledHook(t *testing.T) {
	usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
	useDoctorCheckout(t, pinnedCaller)
	stale := strings.Replace(hook.Kinds("")[0].Script, `-eq 3 `, `-eq 9 `, 1)
	if stale == hook.Kinds("")[0].Script {
		t.Fatal("the stale-hook fixture no longer differs from the current commit-msg script — re-derive it")
	}
	writeHook(t, filepath.Join(".git", "hooks"), stale)

	code, stdout, stderr := runGlyph(t, "doctor")
	if code != 3 {
		t.Fatalf("a stale glyph-written hook exited %d, want 3 (the convention-violation code)\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "fail     commit-msg-hook") {
		t.Errorf("the stale hook must be the failing check:\n%s", stdout)
	}
	if !strings.Contains(stderr, "::warning::glyph: doctor commit-msg-hook") {
		t.Errorf("a failing check must annotate as a warning:\n%s", stderr)
	}
}

// TestDoctorHonoursCoreHooksPath is why the hooks directory is git's answer and
// not filepath.Join(root, ".git", "hooks"). The family's older repos set
// core.hooksPath to a tracked directory; a doctor that assumed .git/hooks would
// read a file git never runs and report a clean bill of health over a stale hook
// sitting in the directory git actually uses.
func TestDoctorHonoursCoreHooksPath(t *testing.T) {
	usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
	useDoctorCheckout(t, pinnedCaller)
	testGit(t, ".", "akira-toriyama", "config", "core.hooksPath", "scripts/hooks")
	// The decoy sits where the naive implementation would look. It is CURRENT,
	// so a doctor reading it reports a pass — the exact wrong answer.
	writeHook(t, filepath.Join(".git", "hooks"), hook.Kinds("")[0].Script)
	writeHook(t, filepath.Join("scripts", "hooks"), strings.Replace(hook.Kinds("")[0].Script, `-eq 3 `, `-eq 9 `, 1))

	code, stdout, _ := runGlyph(t, "doctor")
	if code != 3 {
		t.Fatalf("the hook in core.hooksPath is the one git runs; doctor exited %d, want 3\n%s", code, stdout)
	}
	if !strings.Contains(stdout, filepath.Join("scripts", "hooks", "commit-msg")) {
		t.Errorf("the report must name the directory git reported, not .git/hooks:\n%s", stdout)
	}
}

// writeHook installs body as the commit-msg hook under dir, creating it.
func writeHook(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commit-msg"), []byte(body), 0o600); err != nil {
		t.Fatalf("write hook: %v", err)
	}
}
