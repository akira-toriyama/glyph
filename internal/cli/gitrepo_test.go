package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestMain keeps every test off the real API. GITHUB_REPOSITORY and
// GITHUB_API_URL are ambient on an Actions runner, GITHUB_TOKEN and GH_TOKEN
// wherever a workflow or a shell exports them: the repository and both tokens
// are blanked, and GITHUB_API_URL points at a closed loopback port rather than
// going blank — blank means api.github.com, and a blank GITHUB_REPOSITORY
// falls back to the clone's origin, which in glyph's own checkout is on
// github.com. So a test that forgot usePR is refused at usage before any
// request (the origin is on github.com, the API host 127.0.0.1 — originRepo's
// host gate), and one that names the repository but forgets the server fails
// at a refused connection, after the client's retries (about 21s) — never at
// api.github.com (both measured). That host gate, and "--repo owner/name is
// required" in a fixture with no origin, answer 2 as well, so a usage test
// whose run would go on to resolve a repository were its refusal to regress
// asserts the refusal's text, never the code alone. Tests that need the
// variables set them per-test with t.Setenv, `""` included for the
// public-host default.
// GIT_EDITOR joins them for the same reason from the other side: `lint --stdin`
// reads it to tell an edited message from a `-m` one, and a developer whose
// shell exports GIT_EDITOR would otherwise run a different code path than CI.
// The personal git config is held out for the same reason: `lint --stdin` runs
// `git config` IN this process for commit.cleanup and commit.verbose, and a
// ~/.gitconfig commit.cleanup=strip failed TestHookVerdictMatchesWhatGitRecords
// (measured) — its commits pinned the config, its in-process hook side did not.
// The run-context pair (actionsEnv: GITHUB_REF, GITHUB_EVENT_PATH) is the
// sharpest case of all, and it is ranged over rather than re-typed here: glyph
// runs its own `go test` on an Actions runner, where GITHUB_REF is
// refs/pull/N/merge and GITHUB_EVENT_PATH points at a real payload, so an
// unblanked one arms checkReleaseRef and every writing release test judges
// differently in CI than on a laptop. Sharing the slice means a third
// run-context variable joins this list by construction.
func TestMain(m *testing.M) {
	for _, k := range append([]string{"GITHUB_REPOSITORY", "GITHUB_TOKEN", "GH_TOKEN", "GIT_EDITOR"}, actionsEnv...) {
		os.Unsetenv(k)
	}
	os.Setenv("GITHUB_API_URL", "http://127.0.0.1:1")
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		os.Setenv(k, os.DevNull)
	}
	code := m.Run()
	removeGlyphBinary()
	os.Exit(code)
}

// errEnvelope is the decoded machine error envelope glyph prints to stderr on
// a non-zero exit: {"error":{"code","message"[,"details"]}}. Details stays raw
// so each caller decodes its own detail shape.
type errEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

// decodeErrorEnvelope decodes the stderr envelope — the machine API scripts
// and agents branch on — so tests pin its keys and numeric code instead of
// grepping substrings (a renamed key would pass every substring assertion).
// It expects stderr to be exactly the envelope (no ::warning:: lines before).
func decodeErrorEnvelope(t *testing.T, stderr string) errEnvelope {
	t.Helper()
	var env struct {
		Error *errEnvelope `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &env); err != nil || env.Error == nil {
		t.Fatalf("stderr is not the {\"error\":{...}} envelope (decode: %v):\n%s", err, stderr)
	}
	return *env.Error
}

// setStdin points the lint --stdin input stream at s for one test.
func setStdin(t *testing.T, s string) {
	t.Helper()
	old := in
	in = strings.NewReader(s)
	t.Cleanup(func() { in = old })
}

// runGlyph executes the root command with args, returning the exit code and
// what was written to the payload and diagnostic streams.
func runGlyph(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return runGlyphCtx(t, context.Background(), args...)
}

// runGlyphCtx is runGlyph with a caller-owned context, for the tests that
// cancel mid-run — the wiring a live SIGINT/SIGTERM uses. cobra's Execute is
// ExecuteContext over context.Background, so runGlyph is this with no
// context of its own.
func runGlyphCtx(t *testing.T, ctx context.Context, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	outBuf, errBuf, restore := captureOut()
	defer restore()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(errBuf) // cobra's own usage/help output is diagnostics here
	root.SetErr(errBuf)
	code = finish(root.ExecuteContext(ctx))
	return code, outBuf.String(), errBuf.String()
}
