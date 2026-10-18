package walk

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// The walk's own git calls run through gitsource.Checkout, so the same pin
// internal/cli's and internal/gitsource's TestMain carry holds here: a
// personal git config must not move an answer (DESIGN §5, "Anything that
// models an external system").
func TestMain(m *testing.M) {
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		os.Setenv(k, os.DevNull)
	}
	os.Exit(m.Run())
}

// recorder is the Reporter the tests read the walk's warnings and notices
// back from, each line prefixed by its kind.
type recorder struct {
	lines []string
}

func (r *recorder) Warnf(format string, a ...any) {
	r.lines = append(r.lines, "warning: "+fmt.Sprintf(format, a...))
}

func (r *recorder) Noticef(format string, a ...any) {
	r.lines = append(r.lines, "notice: "+fmt.Sprintf(format, a...))
}

// at is the walk over the fixture at dir, with no API behind it: the tests
// never change directory, so a git call that reached past the Git port
// would run in the package directory and fail.
func at(dir string) Walker {
	return Walker{Git: gitsource.Checkout{Dir: dir}, Report: &recorder{}}
}

// testRepo is testutil's hermetic fixture plus a v0.1.0 tag on the root
// commit, the shape the walk-base tests start from.
func testRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = testutil.NewRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "v0.1.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

func testGit(t *testing.T, dir, author string, args ...string) string {
	t.Helper()
	return testutil.Git(t, dir, author, args...)
}

func testCommit(t *testing.T, dir, author, message string) {
	t.Helper()
	testutil.Commit(t, dir, author, message)
}

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	return testutil.Config(t)
}

// fakePulls is the Pulls port answered from memory: the merged pull each
// merge point resolves to and each pull's listing. A sha it was not told
// about resolves to no pull, as GitHub answers for a direct push.
type fakePulls struct {
	pulls    map[string][]github.PullRef
	listings map[int][]github.Commit
	asked    []string
}

func (p *fakePulls) CommitPulls(_ context.Context, _, _, sha string) ([]github.PullRef, error) {
	p.asked = append(p.asked, "pulls:"+sha)
	return p.pulls[sha], nil
}

func (p *fakePulls) PullCommits(_ context.Context, _, _ string, number int) ([]github.Commit, error) {
	p.asked = append(p.asked, fmt.Sprintf("commits:#%d", number))
	return p.listings[number], nil
}

func (p *fakePulls) CommitFiles(_ context.Context, _, _, sha string) ([]string, bool, error) {
	p.asked = append(p.asked, "files:"+sha)
	return nil, false, nil
}

// TestSinceReadsThroughItsPorts drives the whole walk — base, expansion,
// partition — with every outside read answered by the ports and the fixture
// named by path alone: a squash-merged pull's inner commits fold in, its
// squash subject never reaches the fold, the bot push costs no request, and
// the whole-history warning lands on the Reporter rather than on a stream.
// Nothing here changes directory, so a git call made past the Git port, or a
// warning written past the Reporter, fails this test.
func TestSinceReadsThroughItsPorts(t *testing.T) {
	dir := testutil.NewRepo(t)
	testCommit(t, dir, "akira-toriyama", "Add a menu (#7)")
	squash := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testCommit(t, dir, "dependabot[bot]", ":robot:= chore(fleet): sync")
	pulls := &fakePulls{
		pulls: map[string][]github.PullRef{
			squash: {{Number: 7, MergedAt: "2026-10-01T00:00:00Z", MergeCommitSHA: squash}},
		},
		listings: map[int][]github.Commit{
			7: {{SHA: "a1a1a1a", Author: "akira-toriyama", Message: ":sparkles:(ui)^ add a menu"},
				{SHA: "b2b2b2b", Author: "akira-toriyama", Message: ":bug:(ui)~ fix the menu"}},
		},
	}
	rep := &recorder{}
	w := Walker{Git: gitsource.Checkout{Dir: dir}, Pulls: pulls, Report: rep}

	res, err := w.Since(t.Context(), testCfg(t), Auto, "akira-toriyama", "glyph", nil)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if res.Source != "HEAD" || res.Base == nil || *res.Base != (bump.Version{}) {
		t.Fatalf("source/base = %q/%v, want the whole history from v0.0.0 (no tag in HEAD's history)", res.Source, res.Base)
	}
	var subjects []string
	for _, c := range res.All {
		subjects = append(subjects, c.Raw.Message)
	}
	want := []string{":tada:= begin the project", ":sparkles:(ui)^ add a menu", ":bug:(ui)~ fix the menu", ":robot:= chore(fleet): sync"}
	if strings.Join(subjects, "\n") != strings.Join(want, "\n") {
		t.Errorf("the fold reads:\n%s\nwant:\n%s", strings.Join(subjects, "\n"), strings.Join(want, "\n"))
	}
	if len(res.Facts.Pulls) != 1 || res.Facts.Pulls[0] != (PullExpansion{Number: 7, Commits: 2}) || !res.Facts.Complete() {
		t.Errorf("facts = %+v, want pull #7 expanded into 2 commits and a complete walk", res.Facts)
	}
	for _, asked := range pulls.asked {
		if strings.Contains(asked, "files:") {
			t.Errorf("no file was to be asked for on the bare line, got %q", asked)
		}
	}
	if len(pulls.asked) != 3 {
		t.Errorf("requests = %q, want the root's and the squash's resolution and one listing (the bot push costs none)", pulls.asked)
	}
	if len(rep.lines) != 2 || !strings.HasPrefix(rep.lines[0], "warning: no version tag in HEAD's history — walking the repository's whole history") ||
		!strings.HasSuffix(rep.lines[1], "has no merged pull request (a direct push, or the API lagging) — reading its own message") {
		t.Errorf("reporter heard %q, want the whole-history warning and the root commit read as a direct push", rep.lines)
	}
}
