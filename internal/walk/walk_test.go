package walk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/github"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// The walk's git calls run through gitsource.Checkout, so the same pin
// internal/cli's and internal/gitsource's TestMain carry holds here: a
// personal git config must not move an answer (DESIGN §5, "Anything that
// models an external system"). Every test then runs from a directory that
// is no repository: the package directory sits inside glyph's own clone,
// where a git call made past the Git port would still be answered, and
// from here it fails — measured by swapping one w.Git call for the
// gitsource function at "." (the walk tests go red).
func TestMain(m *testing.M) {
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		os.Setenv(k, os.DevNull)
	}
	nowhere, err := os.MkdirTemp("", "glyph-walk-nowhere")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(nowhere); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(nowhere)
	os.Exit(code)
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

// at is the walk over the fixture at dir, with no API behind it. The fixture
// is named by path alone (TestMain runs every test outside any repository).
func at(dir string) Walker {
	return Walker{Git: gitsource.Checkout{Dir: dir}, Report: &recorder{}}
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
// A warning written past the Reporter is never heard, and a git call made
// past the Git port runs where TestMain put the test — outside any
// repository — and fails.
func TestSinceReadsThroughItsPorts(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", "Add a menu (#7)")
	squash := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "dependabot[bot]", ":robot:= chore(fleet): sync")
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

	res, err := w.Since(t.Context(), testutil.Config(t), Auto, "akira-toriyama", "glyph", nil)
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

// TestSinceReadsThroughItsPortsUnderPackages reaches the port calls the bare
// line never makes — the merge base of two lines' bases, each line's own
// range, a landed commit's files from local git, and a typed tag's
// existence — so that, with TestMain running the test outside any
// repository, bypassing any one of them fails here (the bare-line test
// covers the other nine; measured one call at a time).
func TestSinceReadsThroughItsPortsUnderPackages(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \"curry\"\n",
		map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n"})
	declared, err := os.ReadFile(filepath.Join(dir, "glyph.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(declared)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	write("curry/curry.go", "package curry // spiced\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:(curry)^ spice the curry")
	spice := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0")
	write("haiku/haiku.go", "package haiku // fixed\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":bug:(haiku)~ fix the haiku")
	fix := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	pulls := &fakePulls{}
	w := Walker{Git: gitsource.Checkout{Dir: dir}, Pulls: pulls, Report: &recorder{}}
	res, err := w.Since(t.Context(), cfg, Auto, "akira-toriyama", "glyph", nil)
	if err != nil {
		t.Fatalf("Since over two lines: %v", err)
	}
	// The union starts at the bases' merge base — haiku's tag — so the spice
	// commit is walked, unreleased on haiku's line and placed on curry's, and
	// joins neither; the fix joins haiku alone, its files read from git.
	if len(res.Lines) != 2 || len(res.Lines[0].Commits) != 1 || res.Lines[0].Commits[0].Raw.SHA != fix || len(res.Lines[1].Commits) != 0 {
		t.Fatalf("lines = %+v, want the fix on haiku and nothing on curry", res.Lines)
	}
	if len(res.All) != 2 || res.All[0].Raw.SHA != spice || !res.Facts.Complete() {
		t.Errorf("all = %d commit(s), facts %+v; want the spice and the fix, complete", len(res.All), res.Facts)
	}
	if want := []string{"pulls:" + spice, "pulls:" + fix}; strings.Join(pulls.asked, " ") != strings.Join(want, " ") {
		t.Errorf("requests = %q, want each direct push resolved once and no file asked (both are landed)", pulls.asked)
	}

	one, err := w.Since(t.Context(), cfg, "haiku/v0.1.0", "akira-toriyama", "glyph", nil)
	if err != nil {
		t.Fatalf("Since from a typed tag: %v", err)
	}
	if len(one.Lines) != 1 || one.Lines[0].Package.Path != "haiku" || one.Lines[0].BaseTag != "haiku/v0.1.0" || len(one.Lines[0].Commits) != 1 {
		t.Errorf("a typed tag names its line alone with the tag as its base: got %+v", one.Lines)
	}
}
