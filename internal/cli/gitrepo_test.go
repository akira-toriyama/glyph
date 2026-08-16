package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/testutil"
)

// TestMain blanks the GitHub environment before any test runs: on an Actions
// runner GITHUB_REPOSITORY / GITHUB_TOKEN / GITHUB_API_URL are ambient, and a
// future API-touching test that forgot usePR would silently read them and
// call the real api.github.com. Blanking here makes that leak structurally
// impossible instead of merely avoided by convention; tests that need the
// variables set them per-test with t.Setenv.
// GIT_EDITOR joins them for the same reason from the other side: `lint --stdin`
// reads it to tell an edited message from a `-m` one, and a developer whose
// shell exports GIT_EDITOR would otherwise run a different code path than CI.
// The run-context pair (actionsEnv: GITHUB_REF, GITHUB_EVENT_PATH) is the
// sharpest case of all, and it is ranged over rather than re-typed here: glyph
// runs its own `go test` on an Actions runner, where GITHUB_REF is
// refs/pull/N/merge and GITHUB_EVENT_PATH points at a real payload, so an
// unblanked one arms checkReleaseRef and every writing release test judges
// differently in CI than on a laptop. Sharing the slice means a third
// run-context variable joins this list by construction.
func TestMain(m *testing.M) {
	for _, k := range append([]string{"GITHUB_API_URL", "GITHUB_REPOSITORY", "GITHUB_TOKEN", "GH_TOKEN", "GIT_EDITOR"}, actionsEnv...) {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
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

// testCfg loads the gemoji preset — the config every fixture repo carries —
// for tests that call the plumbing directly rather than through runGlyph.
func testCfg(t *testing.T) *config.Config {
	t.Helper()
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("gemoji preset missing")
	}
	cfg, err := config.Load(preset)
	if err != nil {
		t.Fatalf("load gemoji preset: %v", err)
	}
	return cfg
}

// setStdin points the lint --stdin input stream at s for one test.
func setStdin(t *testing.T, s string) {
	t.Helper()
	old := in
	in = strings.NewReader(s)
	t.Cleanup(func() { in = old })
}

// testRepo builds a hermetic throwaway repository for the --range commands —
// testutil's fixture (pinned identity, real git config held out, maintenance
// off; the incidents live on testutil.GitEnv) plus a v0.1.0 tag on the base so
// bump has a current version to step from.
func testRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = testutil.NewRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// testGit runs one git command in dir as author and fails the test on error.
func testGit(t *testing.T, dir, author string, args ...string) string {
	t.Helper()
	return testutil.Git(t, dir, author, args...)
}

// testCommit adds one empty commit authored by author with the given message.
func testCommit(t *testing.T, dir, author, message string) {
	t.Helper()
	testutil.Commit(t, dir, author, message)
}

// runGlyph executes the root command with args, returning the exit code and
// what was written to the payload and diagnostic streams.
func runGlyph(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	oldOut, oldErr := out, errOut
	out, errOut = &outBuf, &errBuf
	defer func() { out, errOut = oldOut, oldErr }()

	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&errBuf) // cobra's own usage/help output is diagnostics here
	root.SetErr(&errBuf)
	code = finish(root.Execute())
	return code, outBuf.String(), errBuf.String()
}
