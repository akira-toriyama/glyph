package walk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// TestLatestVersionTagComparesVersionsNotRefnames: the walk base and the
// version base both come from this resolver, and it must answer with the
// highest VERSION rather than with whatever tag git listed first.
//
// git's --sort=-v:refname is a refname sort. It compares digit runs
// numerically, which is why it looks like a version sort, but it still starts
// on the leading byte — so every v-prefixed tag lands above every bare one
// ('v' > '1'), and ParseVersion accepts both spellings (^v?…). On the tags
// below git reports v0.0.2 first while the highest version is 100.0.0; taking
// git's first parseable entry moved the release two tags backwards in silence.
func TestLatestVersionTagComparesVersionsNotRefnames(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	for _, tag := range []string{"v0.0.1", "v0.0.2", "9.9.9", "100.0.0"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}

	// The premise: git really does report a lower tag first here. Without this
	// the assertion below could pass on a git whose sort changed, proving
	// nothing.
	if first := testGit(t, dir, "akira-toriyama", "tag", "--list", "--sort=-v:refname"); !strings.HasPrefix(first, "v0.1.0\n") {
		t.Fatalf("premise gone: git --sort=-v:refname now leads with %q, so this test no longer exercises the refname/version gap", strings.SplitN(first, "\n", 2)[0])
	}

	tag, v, err := at(dir).LatestVersionTag(t.Context(), config.Line{}, nil)
	if err != nil {
		t.Fatalf("LatestVersionTag: %v", err)
	}
	if tag != "100.0.0" {
		t.Errorf("LatestVersionTag tag = %q, want %q (the highest version, not git's first entry)", tag, "100.0.0")
	}
	if want := (bump.Version{Major: 100}); v != want {
		t.Errorf("LatestVersionTag version = %v, want %v", v, want)
	}
}

// TestLatestVersionTagBreaksATieOnGitsOrder: one version spelled twice is not
// a version question, so the answer is whatever git listed first — pinned so
// the resolver stays deterministic rather than depending on map or loop order.
//
// This one passes against the pre-change tree by design: the tie-break is the
// half of the old behaviour the fix deliberately KEEPS, and a test that pins it
// is what makes a later "just sort the tags ourselves" visible.
//
// bite-exempt: pins the behaviour the fix preserves, not the behaviour it changes
func TestLatestVersionTagBreaksATieOnGitsOrder(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	for _, tag := range []string{"1.2.3", "v1.2.3"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}

	tag, _, err := at(dir).LatestVersionTag(t.Context(), config.Line{}, nil)
	if err != nil {
		t.Fatalf("LatestVersionTag: %v", err)
	}
	if tag != "v1.2.3" {
		t.Errorf("LatestVersionTag tag = %q, want %q (git lists it first among the two spellings)", tag, "v1.2.3")
	}
}

// TestLatestVersionTagIsPerLine pins the line boundary of the walk base
// (DESIGN §4.1): asked on a line, the resolver answers with that line's
// highest tag and no other's — the bare line ignores every <path>/ tag, a
// package ignores bare tags and its siblings', and a parent ignores its
// nested package's. A tag two lines over that baselined a walk would re-fold
// released commits or skip unreleased ones with nothing on stderr.
func TestLatestVersionTagIsPerLine(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	for _, tag := range []string{"haiku/v2.0.0", "haiku/v1.9.0", "curry/v0.3.0", "haiku/sub/v9.0.0", "v0.0.9"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}

	cases := map[string]string{
		"":           "v0.1.0",
		"haiku/":     "haiku/v2.0.0",
		"curry/":     "curry/v0.3.0",
		"haiku/sub/": "haiku/sub/v9.0.0",
		"other/":     "",
	}
	for prefix, want := range cases {
		tag, _, err := at(dir).LatestVersionTag(t.Context(), config.Line{Prefix: prefix}, nil)
		if err != nil {
			t.Fatalf("LatestVersionTag(%q): %v", prefix, err)
		}
		if tag != want {
			t.Errorf("LatestVersionTag(%q) = %q, want %q", prefix, tag, want)
		}
	}
	// below: on a line is the predecessor ON THAT LINE.
	below := bump.Version{Major: 2}
	tag, v, err := at(dir).LatestVersionTag(t.Context(), config.Line{Prefix: "haiku/"}, &below)
	if err != nil || tag != "haiku/v1.9.0" || v != (bump.Version{Major: 1, Minor: 9}) {
		t.Errorf("LatestVersionTag(haiku/, below v2.0.0) = %q, %v, %v; want haiku/v1.9.0", tag, v, err)
	}
}

// TestLatestVersionTagOnAShallowCheckoutReadsEveryTag: a --depth clone that
// fetched its tags holds v0.2.0 as an object but not the history joining it
// to HEAD, so --merged answers nothing and the base would fall to v0.0.0. A
// shallow checkout cannot say what its history holds; it reads every tag, as
// before, and the walk records the checkout as unread.
//
// bite-exempt: pins the behaviour the change preserves on the one input where --merged cannot answer
func TestLatestVersionTagOnAShallowCheckoutReadsEveryTag(t *testing.T) {
	src, _ := testRepo(t) // tags v0.1.0
	testCommit(t, src, "akira-toriyama", ":sparkles:^ second")
	testGit(t, src, "akira-toriyama", "tag", "v0.2.0")
	testCommit(t, src, "akira-toriyama", ":bug:~ third")
	dir := t.TempDir()
	testGit(t, dir, "akira-toriyama", "clone", "-q", "--depth", "1", "file://"+src, ".")
	testGit(t, dir, "akira-toriyama", "fetch", "-q", "--depth", "1", "origin", "refs/tags/*:refs/tags/*")
	if merged := testGit(t, dir, "akira-toriyama", "tag", "--merged", "HEAD"); merged != "" {
		t.Fatalf("premise gone: the shallow clone's HEAD now holds %q", merged)
	}
	if all := testGit(t, dir, "akira-toriyama", "tag", "--list", "v0.2.0"); all != "v0.2.0" {
		t.Fatalf("premise gone: the shallow clone did not fetch v0.2.0 (tag --list = %q)", all)
	}

	tag, _, err := at(dir).LatestVersionTag(t.Context(), config.Line{}, nil)
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("LatestVersionTag on a shallow checkout = %q, %v; want v0.2.0 (every tag, the walk says the checkout is shallow)", tag, err)
	}
}

// TestSinceTagAutoWalksFromTheHighestVersion carries the same defect up to the
// command surface, where it is a wrong RELEASE and not merely a wrong string:
// a bare --since-tag resolves the base, and a base two tags behind re-folds
// commits an earlier tag already shipped.
func TestSinceTagAutoWalksFromTheHighestVersion(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ ship the released feature")
	testGit(t, dir, "akira-toriyama", "tag", "100.0.0")
	testCommit(t, dir, "akira-toriyama", ":bug:(ui)~ fix the unreleased crash")

	revRange, _, base, err := at(dir).sinceTagRange(t.Context(), testCfg(t), Auto)
	if err != nil {
		t.Fatalf("sinceTagRange: %v", err)
	}
	if revRange != "100.0.0..HEAD" {
		t.Errorf("walk range = %q, want %q", revRange, "100.0.0..HEAD")
	}
	if base == nil || *base != (bump.Version{Major: 100}) {
		t.Errorf("version base = %v, want v100.0.0", base)
	}
}

// TestSinceTagBaseIsAReleaseHEADContains: a v9.0.0 cut on a side branch that
// forked before v0.2.0 is another history's release. Read as the base it
// walks v9.0.0..HEAD — which re-admits the commit v0.2.0 already shipped —
// and steps main to v9.x (measured at adfc5e1: v9.1.0 over two commits).
func TestSinceTagBaseIsAReleaseHEADContains(t *testing.T) {
	dir, _ := testRepo(t) // tags v0.1.0
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "-b", "side")
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ side work")
	testGit(t, dir, "akira-toriyama", "tag", "v9.0.0")
	testGit(t, dir, "akira-toriyama", "checkout", "-q", "main")
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ second")
	testGit(t, dir, "akira-toriyama", "tag", "v0.2.0")
	testCommit(t, dir, "akira-toriyama", ":bug:~ third")

	revRange, _, base, err := at(dir).sinceTagRange(t.Context(), testCfg(t), Auto)
	if err != nil {
		t.Fatalf("sinceTagRange: %v", err)
	}
	if revRange != "v0.2.0..HEAD" || base == nil || *base != (bump.Version{Minor: 2}) {
		t.Fatalf("range/base = %q/%v, want v0.2.0..HEAD / v0.2.0 (the highest release HEAD contains, not the side branch's v9.0.0)", revRange, base)
	}
}

// TestWholeHistoryAtTheCapStillWalks is the untagged arm's half of the cap
// decision that cli's TestSinceTagUnboundedWalkRefused cannot pin: a history
// of exactly Cap walk-visible commits still walks (a skip would fold zero
// commits and verdict the first release out of existence), and the count
// excludes the bot authors the walk itself never asks about. The refusal one
// commit past the cap, and its exit 4 and escape, are that cli test's.
func TestWholeHistoryAtTheCapStillWalks(t *testing.T) {
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "v0.1.0")
	// 199 human commits (plus the fixture's root) and 10 bot commits: 200
	// walk-visible, exactly AT the cap. The bot commits prove the guard counts
	// what the walk VISITS, not raw history length.
	for i := range 199 {
		testCommit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= human commit %d", i))
	}
	for i := range 10 {
		testCommit(t, dir, "dependabot[bot]", fmt.Sprintf(":robot:= chore(fleet): sync %d", i))
	}

	revRange, _, base, err := at(dir).sinceTagRange(t.Context(), testCfg(t), Auto)
	if err != nil {
		t.Fatalf("200 walk-visible commits are AT the cap and must still walk (a skip would "+
			"verdict the first release out of existence), got %v", err)
	}
	if revRange != "HEAD" || base == nil || *base != (bump.Version{}) {
		t.Fatalf("range/base = %q/%v, want HEAD / v0.0.0", revRange, base)
	}
}
