package gitsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestHead returns the checkout's HEAD sha — what the rolling draft's
// target_commitish records so the eventual Publish tags the commit the
// verdict was computed at, not whatever main has moved to since.
func TestHead(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	want := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	got, err := Head(context.Background(), dir)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got != want {
		t.Fatalf("Head = %q, want %q", got, want)
	}
}

// TestHeadErrorsAreAPI: a directory that is not a repository fails as an
// ordinary git/IO error (4) carrying git's own fatal line.
func TestHeadErrorsAreAPI(t *testing.T) {
	gitOrSkip(t)
	_, err := Head(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error outside a repository")
	}
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("error = %v, want CodeAPI", err)
	}
}

// TestHave: the repository's own commits come back, and everything else — a
// well-formed sha nobody has, a tree that is not a commit, outright garbage —
// is simply absent rather than an error. The listing Have is asked about is
// GitHub's, so it routinely names commits that exist in no clone.
func TestHave(t *testing.T) {
	dir := newRepo(t)
	root := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	head := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	tree := git(t, dir, "akira-toriyama", "rev-parse", "HEAD^{tree}")
	absent := "0123456789abcdef0123456789abcdef01234567"

	got, err := Have(context.Background(), dir, []string{root, head, tree, absent, "not-a-sha"})
	if err != nil {
		t.Fatalf("Have: %v", err)
	}
	for _, sha := range []string{root, head} {
		if !got[sha] {
			t.Fatalf("Have does not report %.7s, which this repository holds", sha)
		}
	}
	for name, sha := range map[string]string{"a tree": tree, "an absent commit": absent, "garbage": "not-a-sha"} {
		if got[sha] {
			t.Fatalf("Have reports %s (%q) as a commit this repository holds", name, sha)
		}
	}
}

// TestHaveEmpty: no shas is a successful empty answer and must not shell out —
// the release walk calls this per pull request, and a squash-merged one whose
// listing the API truncated to nothing would otherwise pay for a git process.
func TestHaveEmpty(t *testing.T) {
	got, err := Have(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Have(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Have(nil) = %v, want empty", got)
	}
}

// TestIsAncestor: a YES is a real ancestor or the rev itself; a NO covers both
// spellings git gives it — an honest exit 1 for a commit on another line of
// history, and exit 128 for an object this repository does not hold. The walk
// reads both as "did not land here", so neither may surface as an error.
func TestIsAncestor(t *testing.T) {
	dir := newRepo(t)
	root := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	head := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
	commit(t, dir, "akira-toriyama", ":memo: a commit off the released branch")
	offBranch := git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	git(t, dir, "akira-toriyama", "switch", "-q", "main")

	for name, tc := range map[string]struct {
		sha  string
		want bool
	}{
		"an ancestor":               {root, true},
		"the rev itself":            {head, true},
		"another line of history":   {offBranch, false},
		"an object we do not hold":  {"0123456789abcdef0123456789abcdef01234567", false},
		"a name that is not an sha": {"not-a-sha", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := IsAncestor(context.Background(), dir, tc.sha, "HEAD")
			if err != nil {
				t.Fatalf("IsAncestor(%q): unexpected error: %v", tc.sha, err)
			}
			if got != tc.want {
				t.Fatalf("IsAncestor(%q) = %v, want %v", tc.sha, got, tc.want)
			}
		})
	}
}

// TestIsAncestorCancelIsInterrupted: the abort must not be readable as an
// answer about the repository.
//
// This is a REGRESSION test with a specific shape, and the shape is the whole
// point. A context cancelled BEFORE the child starts makes os/exec return
// ctx.Err() and never produces an *exec.ExitError, so it exercises none of the
// ordering: the bug needed the child to be RUNNING when the cancel lands, at
// which point exec.CommandContext kills it and the abort comes back wearing an
// ExitError — indistinguishable, to a status-first reading, from git's own
// "no, not an ancestor". Reading it that way returned (false, nil): a wrong
// answer about which commits a release counts, with a nil error so no caller
// could tell.
//
// Reproducing that needs the child alive at a moment the test controls, which
// real `git merge-base` (microseconds) cannot offer. So PATH is pointed at a
// git that announces itself and then blocks: the test waits for the
// announcement — proof the process is running — and only then cancels. No
// sleeps, no timing assumptions.
func TestIsAncestorCancelIsInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub git is a shell script")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	started := filepath.Join(dir, "started")
	stub := "#!/bin/sh\ntouch " + started + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	_, err := IsAncestor(ctx, dir, "any-sha", "HEAD")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("IsAncestor cancelled mid-run = %v, want CodeInterrupted (%d) — a killed child arrives as an *exec.ExitError, so reading the exit status before the context turns the abort into a silent \"did not land\"", err, core.CodeInterrupted)
	}
}

// TestFirstParentLog: the n commits ending at rev along first parents, oldest
// first — the shape a rebase-merge leaves behind. A merged side branch must not
// appear: --first-parent is what makes the run alignable.
func TestFirstParentLog(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "akira-toriyama", ":bug: first")
	git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	commit(t, dir, "akira-toriyama", ":memo: on the side branch")
	git(t, dir, "akira-toriyama", "switch", "-q", "main")
	commit(t, dir, "akira-toriyama", ":sparkles: second")
	git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'side'", "side")

	got, err := FirstParentLog(context.Background(), dir, "HEAD", 3)
	if err != nil {
		t.Fatalf("FirstParentLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FirstParentLog(3) returned %d commits, want 3: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Message, ":bug: first") {
		t.Fatalf("commits are not oldest-first: first is %q", got[0].Message)
	}
	if !strings.HasPrefix(got[2].Message, "Merge branch 'side'") {
		t.Fatalf("last commit = %q, want the merge commit (the run ends AT rev)", got[2].Message)
	}
	for _, c := range got {
		if strings.Contains(c.Message, "on the side branch") {
			t.Fatalf("a merged side-branch commit appeared in a --first-parent run: %+v", got)
		}
	}
}

// TestFirstParentLogShortNearTheRoot: asking for more commits than the history
// holds is a SHORT answer, not an error — the contract mainFootprint's length
// guard depends on. Without that guard a young repository indexes a listing-long
// loop into a shorter slice and panics.
func TestFirstParentLogShortNearTheRoot(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "akira-toriyama", ":bug: the only other commit")

	got, err := FirstParentLog(context.Background(), dir, "HEAD", 10)
	if err != nil {
		t.Fatalf("FirstParentLog past the root: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("FirstParentLog(10) on a 2-commit history = %d commits, want 2", len(got))
	}
}

// TestIsShallow: a full clone says no, a --depth 1 clone says yes. The release
// walk branches on this because a truncated history cannot tell a commit that
// never landed from one git simply does not have.
func TestIsShallow(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "akira-toriyama", ":bug: fix a crash")

	full, err := IsShallow(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsShallow(full): %v", err)
	}
	if full {
		t.Fatal("IsShallow reports a full checkout as shallow")
	}

	clone := filepath.Join(t.TempDir(), "shallow")
	git(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", "1", "file://"+dir, clone)
	shallow, err := IsShallow(context.Background(), clone)
	if err != nil {
		t.Fatalf("IsShallow(shallow): %v", err)
	}
	if !shallow {
		t.Fatal("IsShallow reports a --depth 1 clone as full — the walk would grade itself on a partial repository")
	}
}

// TestHooksDir asks git rather than assuming .git/hooks, because core.hooksPath
// relocates them — the family's older repos set it, and writing to .git/hooks
// there installs a hook git never runs.
func TestHooksDir(t *testing.T) {
	dir := newRepo(t)

	got, err := HooksDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("HooksDir: %v", err)
	}
	if filepath.Base(got) != "hooks" {
		t.Fatalf("HooksDir = %q, want a path ending in hooks", got)
	}

	git(t, dir, "akira-toriyama", "config", "core.hooksPath", "scripts/hooks")
	got, err = HooksDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("HooksDir with core.hooksPath: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "scripts/hooks") {
		t.Fatalf("HooksDir with core.hooksPath = %q, want it to honour the relocation", got)
	}
}

// TestHooksDirErrorsAreAPI: outside a repository there is no answer to give,
// and a git/IO failure is exit 4 by contract.
func TestHooksDirErrorsAreAPI(t *testing.T) {
	gitOrSkip(t)
	_, err := HooksDir(context.Background(), t.TempDir())
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("HooksDir outside a repository = %v, want CodeAPI", err)
	}
}
