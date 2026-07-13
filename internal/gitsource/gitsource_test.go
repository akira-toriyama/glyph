package gitsource

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/internal/core"
)

// gitOrSkip skips the test when git is absent (never on CI, where git is a
// given — this guards exotic local environments only).
func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitEnv pins a hermetic git environment: fixed identity, and the user's real
// global/system config held out (a global commit.gpgsign would otherwise break
// the test commits).
func gitEnv(author string) []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=committer",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	)
}

// git runs one git command in dir as author and fails the test on error.
func git(t *testing.T, dir, author string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv(author)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo creates a temp repository with one root commit and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	gitOrSkip(t)
	dir := t.TempDir()
	git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", ":tada: begin the project")
	return dir
}

// commit adds one empty commit authored by author with the given message.
func commit(t *testing.T, dir, author, message string) {
	t.Helper()
	git(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
}

// TestLogRange: Log walks BASE..HEAD oldest→newest carrying SHA, author name,
// parent count, and the verbatim message (subject + body, CJK intact).
func TestLogRange(t *testing.T) {
	dir := newRepo(t)
	base := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	commit(t, dir, "dependabot[bot]", ":arrow_up: bump a dep")
	commit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu\n\nBody line.\n\n---（和訳）\nメニュー追加。")

	got, err := Log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Log returned %d commits, want 3: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Message, ":bug: fix a crash") {
		t.Fatalf("commits are not oldest-first: first is %q", got[0].Message)
	}
	for i, c := range got {
		if len(c.SHA) != 40 {
			t.Fatalf("commit %d has a malformed SHA %q", i, c.SHA)
		}
		if c.Parents != 1 {
			t.Fatalf("commit %d has %d parents, want 1", i, c.Parents)
		}
	}
	if got[1].Author != "dependabot[bot]" {
		t.Fatalf("author = %q, want dependabot[bot]", got[1].Author)
	}
	last := got[2].Message
	if !strings.Contains(last, "---（和訳）\nメニュー追加。") {
		t.Fatalf("message body was not carried verbatim: %q", last)
	}
	if !strings.HasPrefix(last, ":sparkles:(ui) add a menu\n\nBody line.") {
		t.Fatalf("subject/body shape lost: %q", last)
	}
}

// TestLogMergeParents: a merge commit reports its true parent count.
func TestLogMergeParents(t *testing.T) {
	dir := newRepo(t)
	base := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	git(t, dir, "akira-toriyama", "switch", "-q", "-c", "feat")
	commit(t, dir, "akira-toriyama", ":bug: fix on a branch")
	git(t, dir, "akira-toriyama", "switch", "-q", "main")
	commit(t, dir, "akira-toriyama", ":memo: docs on main")
	git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'feat'", "feat")

	got, err := Log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	var mergeParents int
	for _, c := range got {
		if strings.HasPrefix(c.Message, "Merge ") {
			mergeParents = c.Parents
		}
	}
	if mergeParents != 2 {
		t.Fatalf("merge commit parent count = %d, want 2", mergeParents)
	}
}

// TestLogEmptyRange: an empty range is a successful empty result, not an error.
func TestLogEmptyRange(t *testing.T) {
	dir := newRepo(t)
	got, err := Log(context.Background(), dir, "HEAD..HEAD")
	if err != nil {
		t.Fatalf("Log(HEAD..HEAD): unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Log(HEAD..HEAD) = %+v, want empty", got)
	}
}

// TestLogErrorsAreAPI: a bad revision and a non-repo directory are classified
// as API/git failures (exit 4), and the error carries git's diagnostic.
func TestLogErrorsAreAPI(t *testing.T) {
	dir := newRepo(t)
	for name, tc := range map[string]struct{ dir, rng string }{
		"bad revision":                         {dir, "no-such-ref..HEAD"},
		"not a repo":                           {t.TempDir(), "HEAD~1..HEAD"},
		"option-shaped range stays a revision": {dir, "--all"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Log(context.Background(), tc.dir, tc.rng)
			if err == nil {
				t.Fatalf("Log(%q, %q) should fail", tc.dir, tc.rng)
			}
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("Log(%q, %q) error = %v, want a *core.Error with the API code", tc.dir, tc.rng, err)
			}
		})
	}
}

// TestLogCancelIsInterrupted: a canceled context is the user's own Ctrl-C — the
// one thing CodeInterrupted means (SIGINT/SIGTERM, per the core contract).
func TestLogCancelIsInterrupted(t *testing.T) {
	dir := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Log(ctx, dir, "HEAD~0..HEAD")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("Log with a canceled context = %v, want CodeInterrupted (%d)", err, core.CodeInterrupted)
	}
}

// TestLogDeadlineIsAPI: a deadline is NOT an interrupt. Only the user aborting
// the run is CodeInterrupted; a caller-imposed timeout firing against a wedged
// git is an ordinary I/O failure and must exit CodeAPI — exiting 130 would tell
// a release job "the operator stopped me" when in truth git hung.
func TestLogDeadlineIsAPI(t *testing.T) {
	dir := newRepo(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := Log(ctx, dir, "HEAD~0..HEAD")
	ce := core.AsError(err)
	if ce == nil {
		t.Fatalf("Log past its deadline = %v, want a *core.Error", err)
	}
	if ce.Code == core.CodeInterrupted {
		t.Fatalf("a deadline was classified as CodeInterrupted (%d) — that code means the user aborted; want CodeAPI (%d)", core.CodeInterrupted, core.CodeAPI)
	}
	if ce.Code != core.CodeAPI {
		t.Fatalf("Log past its deadline error code = %d, want CodeAPI (%d)", ce.Code, core.CodeAPI)
	}
}

// TestTags: version-sorted descending, all tags included (the caller picks the
// first it can parse); a repo without tags yields an empty list.
func TestTags(t *testing.T) {
	dir := newRepo(t)

	got, err := Tags(context.Background(), dir)
	if err != nil {
		t.Fatalf("Tags on an untagged repo: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Tags on an untagged repo = %v, want empty", got)
	}

	for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.1.1", "not-a-version"} {
		git(t, dir, "akira-toriyama", "tag", tag)
	}
	got, err = Tags(context.Background(), dir)
	if err != nil {
		t.Fatalf("Tags: unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Tags = %v, want 4 entries", got)
	}
	if got[0] != "v0.2.0" {
		t.Fatalf("Tags[0] = %q, want v0.2.0 (version-sorted descending)", got[0])
	}
}
