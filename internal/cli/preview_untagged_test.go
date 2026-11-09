package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins preview's prose about a line with no release tag (DESIGN
// §4.1, "Preview"): what the refusal past the cap tells the operator to do,
// what an untagged headline says its absence of, and what a shallow
// checkout is allowed to claim.

// rootLinesRepo is NewTwoLineRepo folded into ONE root commit: haiku/ and
// curry/ are both added by the root (glyph-monorepo-test-2's shape), so no
// commit precedes either line's first change and no baseline can be named.
func rootLinesRepo(t *testing.T) (dir, root string) {
	t.Helper()
	dir = testutil.NewTwoLineRepo(t)
	first := testutil.Git(t, dir, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "reset", "-q", "--soft", first)
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":tada:= begin with two lines")
	return dir, testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// pullsRoutes answers every commit of dir's history as merged by no pull.
func pullsRoutes(t *testing.T, dir string, routes map[string]string) map[string]string {
	t.Helper()
	for sha := range strings.FieldsSeq(testutil.Git(t, dir, "akira-toriyama", "rev-list", "HEAD")) {
		routes[commitPullsPath(sha)] = `[]`
	}
	return routes
}

// TestPreviewPackagesCapRefusalNamesAnEscapeForEveryUntaggedLine: past the
// walk cap, preview's refusal names a way out that exists for each untagged
// line the pull touches — the baseline where one can be cut, and the line's
// own first release cut by hand where none can, because its first change is
// the root commit. "cut the tag named above" over a refusal that named no
// tag dangled, and on a pull touching a baselined line beside a
// root-declared one, cutting the baseline as told cleared nothing: the
// re-run refused again, for the root line, naming nothing (review of #361,
// measured 2026-11-08). A locked line's own first release is its major's
// floor: named pubsub/vX.Y.Z, the operator's pubsub/v1.5.0 lands on the free
// pubsub line and the re-run refuses again (measured 2026-11-09). Two
// baselines and two hand releases are each said in the plural. Every shape
// is run end to end: the refusal, the advice followed, the re-run
// answering. Mutation rows preview-cap-escape-names-a-tag-nobody-named,
// preview-cap-escape-sends-a-locked-line-to-the-free-line and
// preview-cap-escape-disagrees-in-number.
func TestPreviewPackagesCapRefusalNamesAnEscapeForEveryUntaggedLine(t *testing.T) {
	const noFlag = "re-run — preview reads the pull request, and has no --since-tag flag to name a base with"
	t.Run("a baselined line beside a root-declared one", func(t *testing.T) {
		dir, root := rootLinesRepo(t)
		testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0") // curry never released
		appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \"fish\"\n")
		testutil.WriteFile(t, dir, "fish/fish.go", "package fish\n")
		testutil.Git(t, dir, "akira-toriyama", "add", ".")
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:(fish)^ declare a third line")
		for i := range 200 {
			testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
		}
		routes := map[string]string{
			pullCommitsPath(9): `[` +
				apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
				apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `,` +
				apiCommit("f1", "akira-toriyama", ":bug:~ feed the fish") + `]`,
			commitFilesPath("h1"): apiFiles("haiku/season.go"),
			commitFilesPath("c1"): apiFiles("curry/curry.go"),
			commitFilesPath("f1"): apiFiles("fish/fish.go"),
		}
		usePR(t, walkServer(t, pullsRoutes(t, dir, routes)))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 4 {
			t.Fatalf("preview exited %d, want 4 — 202 commits are past the cap\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		out := stdout + stderr
		for _, want := range []string{
			"; to start from a tag instead, cut fish/v0.0.0 at the commit before that line's first change",
			"; cut the tag named above, and release the line(s) by hand (cut curry/vX.Y.Z at the commit that release covers — the one floor a line with no baseline can take), then " + noFlag,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal must carry\n  %q\ngot:\n%s", want, out)
			}
		}

		// Follow both halves of the advice: fish's baseline at the commit
		// before its first change (the root), curry's first release by hand
		// at the root, the commit it covers. The walk is then bounded by
		// every touched line's tag and the pull previews.
		testutil.Git(t, dir, "akira-toriyama", "tag", "fish/v0.0.0", root)
		testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0", root)
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "9")
		if code != 0 {
			t.Fatalf("after following the refusal's advice preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		for _, want := range []string{"**haiku/v0.1.0 → haiku/v0.2.0**", "**curry/v0.1.0 → curry/v0.1.1**", "**fish** — 🔧 Merging this PR adds **patch**-level changes — the next release stays **fish/v0.1.0** (a **minor** bump is already pending)."} {
			if !strings.Contains(stdout, want) {
				t.Errorf("the re-run must preview every line, with\n  %q\ngot:\n%s", want, stdout)
			}
		}
	})

	t.Run("root-declared lines alone", func(t *testing.T) {
		dir, root := rootLinesRepo(t)
		testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0") // curry never released
		for i := range 201 {
			testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
		}
		usePR(t, walkServer(t, pullsRoutes(t, dir, crossLinePull(9))))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 4 {
			t.Fatalf("preview exited %d, want 4\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		out := stdout + stderr
		if !strings.Contains(out, "refusing the unbounded walk; release the line(s) by hand (cut curry/vX.Y.Z at the commit that release covers — the one floor a line with no baseline can take) and "+noFlag) {
			t.Errorf("with no baseline to name, the refusal names the line's first release as the way out:\n%s", out)
		}
		if strings.Contains(out, "named above") || strings.Contains(out, "curry/v0.0.0") {
			t.Errorf("the refusal points at a tag it never named:\n%s", out)
		}

		testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0", root)
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "9")
		if code != 0 || !strings.Contains(stdout, "**curry/v0.1.0 → curry/v0.1.1**") {
			t.Fatalf("after cutting curry's first release preview exited %d, want 0 with curry previewed\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("a root-declared locked line", func(t *testing.T) {
		dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"pubsub\"\n\n[[packages]]\npath = \"pubsub/v2\"\nname = \"pubsub-v2\"\n",
			map[string]string{"pubsub/p.go": "package pubsub\n", "pubsub/v2/p.go": "package pubsub\n"})
		first := testutil.Git(t, dir, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
		testutil.Git(t, dir, "akira-toriyama", "reset", "-q", "--soft", first)
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":tada:= begin with both majors")
		root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Git(t, dir, "akira-toriyama", "tag", "pubsub/v1.4.0") // the v2 line never released
		for i := range 201 {
			testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
		}
		usePR(t, walkServer(t, pullsRoutes(t, dir, map[string]string{
			pullCommitsPath(5): `[` + apiCommit("p1", "akira-toriyama", ":bug:~ fix the queue") + `,` +
				apiCommit("v1", "akira-toriyama", ":sparkles:(pubsub-v2)^ grow v2") + `]`,
			commitFilesPath("p1"): apiFiles("pubsub/p.go"),
			commitFilesPath("v1"): apiFiles("pubsub/v2/p.go"),
		})))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "5", "--json")
		if code != 4 {
			t.Fatalf("preview exited %d, want 4\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		out := stdout + stderr
		if !strings.Contains(out, "refusing the unbounded walk; release the line(s) by hand (cut pubsub/v2.0.0 at the commit that release covers — the one floor a line with no baseline can take) and "+noFlag) {
			t.Errorf("a locked line's first release is its major's floor, never a pubsub/vX.Y.Z the free pubsub line takes:\n%s", out)
		}

		testutil.Git(t, dir, "akira-toriyama", "tag", "pubsub/v2.0.0", root)
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "5")
		if code != 0 || !strings.Contains(stdout, "**pubsub/v2.0.0 → pubsub/v2.1.0**") {
			t.Fatalf("after cutting pubsub/v2.0.0 preview exited %d, want 0 with the v2 line previewed\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})

	t.Run("two baselined lines beside two root-declared ones", func(t *testing.T) {
		dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \"curry\"\n\n[[packages]]\npath = \"fish\"\n",
			map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n", "fish/fish.go": "package fish\n"})
		first := testutil.Git(t, dir, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
		testutil.Git(t, dir, "akira-toriyama", "reset", "-q", "--soft", first)
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":tada:= begin with three lines")
		root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0") // curry and fish never released
		before := map[string]string{}
		for _, l := range []string{"eel", "gar"} {
			before[l] = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \""+l+"\"\n")
			testutil.WriteFile(t, dir, l+"/"+l+".go", "package "+l+"\n")
			testutil.Git(t, dir, "akira-toriyama", "add", ".")
			testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:("+l+")^ declare "+l)
		}
		for i := range 200 {
			testutil.Commit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
		}
		routes := map[string]string{
			pullCommitsPath(9): `[` +
				apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
				apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `,` +
				apiCommit("f1", "akira-toriyama", ":bug:~ feed the fish") + `,` +
				apiCommit("e1", "akira-toriyama", ":bug:~ feed the eel") + `,` +
				apiCommit("g1", "akira-toriyama", ":bug:~ feed the gar") + `]`,
			commitFilesPath("h1"): apiFiles("haiku/season.go"),
			commitFilesPath("c1"): apiFiles("curry/curry.go"),
			commitFilesPath("f1"): apiFiles("fish/fish.go"),
			commitFilesPath("e1"): apiFiles("eel/eel.go"),
			commitFilesPath("g1"): apiFiles("gar/gar.go"),
		}
		usePR(t, walkServer(t, pullsRoutes(t, dir, routes)))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 4 {
			t.Fatalf("preview exited %d, want 4 — 203 commits are past the cap\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		out := stdout + stderr
		for _, want := range []string{
			"; to start from a tag instead, cut eel/v0.0.0 / gar/v0.0.0, each at the commit before its line's first change",
			"; cut the tags named above, and release the line(s) by hand (cut curry/vX.Y.Z / fish/vX.Y.Z, each at the commit its release covers — the one floor a line with no baseline can take), then " + noFlag,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal must carry\n  %q\ngot:\n%s", want, out)
			}
		}

		testutil.Git(t, dir, "akira-toriyama", "tag", "eel/v0.0.0", before["eel"])
		testutil.Git(t, dir, "akira-toriyama", "tag", "gar/v0.0.0", before["gar"])
		testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0", root)
		testutil.Git(t, dir, "akira-toriyama", "tag", "fish/v0.1.0", root)
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "9")
		if code != 0 {
			t.Fatalf("after following the refusal's advice preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		for _, want := range []string{"**haiku/v0.1.0 → haiku/v0.2.0**", "**curry/v0.1.0 → curry/v0.1.1**", "**fish/v0.1.0 → fish/v0.1.1**", "the next release stays **eel/v0.1.0** (a **minor** bump is already pending)", "the next release stays **gar/v0.1.0** (a **minor** bump is already pending)"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("the re-run must preview every line, with\n  %q\ngot:\n%s", want, stdout)
			}
		}
	})
}

// TestPreviewWalkEscapeAgreesInNumber: each half of preview's escape agrees
// in number with the lines it names, as the walk's remedy before it does.
// The singular said of two read "cut the tag named above" right after "cut
// curry/v0.0.0 / fish/v0.0.0, each at the commit before its line's first
// change", and "cut curry/vX.Y.Z / fish/vX.Y.Z at the commit that release
// covers", one commit for two releases (measured 2026-11-09). Mutation row
// preview-cap-escape-disagrees-in-number.
func TestPreviewWalkEscapeAgreesInNumber(t *testing.T) {
	const tail = "re-run — preview reads the pull request, and has no --since-tag flag to name a base with"
	const floor = " — the one floor a line with no baseline can take)"
	for _, tc := range []struct {
		baselines, unbaselined []string
		want                   string
	}{
		{[]string{"eel/v0.0.0"}, nil, "; cut the tag named above and " + tail},
		{[]string{"eel/v0.0.0", "gar/v0.0.0"}, nil, "; cut the tags named above and " + tail},
		{nil, []string{"curry/vX.Y.Z"}, "; release the line(s) by hand (cut curry/vX.Y.Z at the commit that release covers" + floor + " and " + tail},
		{nil, []string{"curry/vX.Y.Z", "fish/vX.Y.Z"}, "; release the line(s) by hand (cut curry/vX.Y.Z / fish/vX.Y.Z, each at the commit its release covers" + floor + " and " + tail},
		{[]string{"eel/v0.0.0"}, []string{"curry/vX.Y.Z"}, "; cut the tag named above, and release the line(s) by hand (cut curry/vX.Y.Z at the commit that release covers" + floor + ", then " + tail},
		{[]string{"eel/v0.0.0", "gar/v0.0.0"}, []string{"curry/vX.Y.Z", "fish/vX.Y.Z"}, "; cut the tags named above, and release the line(s) by hand (cut curry/vX.Y.Z / fish/vX.Y.Z, each at the commit its release covers" + floor + ", then " + tail},
	} {
		if got := previewWalkEscape(tc.baselines, tc.unbaselined); got != tc.want {
			t.Errorf("previewWalkEscape(%q, %q)\n got: %s\nwant: %s", tc.baselines, tc.unbaselined, got, tc.want)
		}
	}
}

// TestPreviewPackagesUntaggedLineSpeaksOfTheLine: a walked line with no tag
// whose two sides both fold to none says the LINE has no release tag yet.
// It said "the base branch holds no release tag yet" — in the comment whose
// footer names haiku/v0.1.0 on that same base branch two paragraphs below
// (review of #361, measured 2026-11-08); the bare line keeps the base-branch
// sentence, which is the fact there (TestHeadline). Mutation row
// preview-untagged-line-denies-the-base-branch-a-tag.
func TestPreviewPackagesUntaggedLineSpeaksOfTheLine(t *testing.T) {
	dir := testutil.NewTwoLineRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0") // curry never released
	routes := map[string]string{
		pullCommitsPath(9): `[` + apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
			apiCommit("c1", "akira-toriyama", ":memo:(curry)= document the curry") + `]`,
		commitFilesPath("h1"): apiFiles("haiku/season.go"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
	}
	usePR(t, walkServer(t, pullsRoutes(t, dir, routes)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"\n**curry** — ⏸️ Merging this PR moves nothing — and this line has no release tag yet, so there is no version to move.\n",
		"since **haiku/v0.1.0** (haiku). curry has no release tag on the base branch yet, so everything merged so far is folded in for it. Pushing",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the body must carry\n  %q\ngot:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "holds no release tag") {
		t.Errorf("a line's headline asserts of the base branch what the footer refutes:\n%s", stdout)
	}
}

// shallowTwoLineClone is NewTwoLineRepo with curry/v0.1.0 on the declaring
// commit, eight notes, haiku/v0.1.0, one more note, cloned at --depth 3: the
// clone holds haiku's tag and not curry's (asserted), so curry has a release
// tag the clone cannot see.
func shallowTwoLineClone(t *testing.T) (src, clone string) {
	t.Helper()
	src = testutil.NewTwoLineRepo(t)
	testutil.Git(t, src, "akira-toriyama", "tag", "curry/v0.1.0")
	for i := range 8 {
		testutil.Commit(t, src, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
	}
	testutil.Git(t, src, "akira-toriyama", "tag", "haiku/v0.1.0")
	testutil.Commit(t, src, "akira-toriyama", ":memo:= note 9")
	clone = shallowClone(t, src, 3)
	if tags := testutil.Git(t, clone, "akira-toriyama", "tag", "-l"); tags != "haiku/v0.1.0" {
		t.Fatalf("control: the depth-3 clone must hold haiku/v0.1.0 alone, got %q", tags)
	}
	return src, clone
}

// TestPreviewOnAShallowCheckoutSpeaksPerLine: on a shallow checkout a line
// with no release tag within the clone's reach is said to be exactly that —
// of the LINE, never of the clone as a whole, which holds a sibling's tag —
// and names no version, since curry/v0.1.0 exists past the reach and every
// version the headline could name steps from the null floor that tag
// refutes. Both arms: the floor guard skipping the walk (the pull touches
// curry alone) said "no release tag is within this shallow checkout's
// reach" of a clone holding haiku/v0.1.0, and the walk running (haiku
// touched too, its tag within reach) drew "the first release here would be
// **curry/v0.0.1**" and "curry has no release tag on the base branch yet"
// (review of #361, measured 2026-11-08; the t-txed class). The machine
// verdict is unchanged: untagged reports what the checkout holds, next what
// the PR side stepped from the null floor. Mutation rows
// preview-shallow-line-names-a-first-release (the headline) and
// preview-walked-shallow-line-is-called-untagged (the walked arm's shallow
// read).
func TestPreviewOnAShallowCheckoutSpeaksPerLine(t *testing.T) {
	const lineReach = "no release tag on this line is within this shallow checkout's reach, so the release it lands in cannot be named here."
	const caveat = "may be missing from what each headline says, and so may the release tag a line with none within reach would step from, so treat each as a floor rather than the answer."
	t.Run("walk skipped: the one touched line has no tag within reach", func(t *testing.T) {
		_, clone := shallowTwoLineClone(t)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("c1", "akira-toriyama", ":memo:(curry)= document the curry") + `]`,
			commitFilesPath("c1"): apiFiles("curry/curry.go"),
		}))
		t.Chdir(clone)
		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "::warning::glyph: no release tag within this shallow checkout's reach on any line this PR touches — previewing the PR's own verdict per line (the pending walk needs a release floor)\n") {
			t.Errorf("the floor guard must say the absence is of the clone's reach:\n%s", stderr)
		}
		for _, want := range []string{
			"\n**curry** — ⏸️ Merging this PR moves nothing — and " + lineReach + "\n",
			caveat,
			"erase them. curry has no release tag within this shallow checkout's reach, so nothing merged earlier is folded in for it. Pushing",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("the body must carry\n  %q\ngot:\n%s", want, stdout)
			}
		}
		if strings.Contains(stdout, "figure") || strings.Contains(stdout, "no release tag is within") || strings.Contains(stdout, "base branch") {
			t.Errorf("the body qualifies a figure it does not carry, or speaks of the clone or the base branch instead of the line:\n%s", stdout)
		}
	})

	t.Run("walk run: a touched sibling's tag is within reach", func(t *testing.T) {
		_, clone := shallowTwoLineClone(t)
		usePR(t, walkServer(t, pullsRoutes(t, clone, crossLinePull(9))))
		t.Chdir(clone)
		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		res := decodeJSON[previewVerdictLines](t, stdout)
		var curry struct {
			untagged bool
			next     string
		}
		for _, p := range res.Packages {
			if p.Path == "curry" {
				curry.untagged, curry.next = p.Untagged, p.Next
			}
		}
		if !curry.untagged || curry.next != "v0.0.1" {
			t.Fatalf("the machine verdict must still report what the checkout holds and the PR side's step from it: %+v", curry)
		}
		for _, want := range []string{
			"\n**haiku** — 🔼 Merging this PR raises **minor** — the next release becomes **haiku/v0.1.0 → haiku/v0.2.0**.\n",
			"\n**curry** — 🔧 Merging this PR raises **patch** — " + lineReach + "\n",
			"> The pending side of this fold is INCOMPLETE: this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0). Anything already merged but unreleased " + caveat,
			"since **haiku/v0.1.0** (haiku). 1 of them sits on no line. curry has no release tag within this shallow checkout's reach, so everything merged within that reach is folded in for it. Pushing",
		} {
			if !strings.Contains(res.Body, want) {
				t.Errorf("the body must carry\n  %q\ngot:\n%s", want, res.Body)
			}
		}
		if strings.Contains(res.Body, "first release here would be") || strings.Contains(res.Body, "curry/v0.0.1") || strings.Contains(res.Body, "on the base branch yet") {
			t.Errorf("the body names a first release off a floor the clone never confirmed, or calls curry untagged on the base branch:\n%s", res.Body)
		}
	})
}

// TestPreviewOnAShallowCheckoutNamesNoFigure: on the floor guard's shallow
// path a pull that moves nothing got an admonition about "the figure above"
// under a headline that named none (review of #361, measured 2026-11-08).
// The caveat qualifies what the headline says, and the headline says why no
// release can be named. Mutation row
// preview-shallow-caveat-qualifies-a-figure-nobody-named.
func TestPreviewOnAShallowCheckoutNamesNoFigure(t *testing.T) {
	usePR(t, prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":memo:= tidy")+`]`))
	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= note the palette")
	t.Chdir(shallowClone(t, dir, 1))
	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"\n⏸️ Merging this PR moves nothing — and no release tag is within this shallow checkout's reach, so the release it lands in cannot be named here.\n",
		"Anything already merged but unreleased may be missing from what the headline says, and so may the release tag it would step from, so treat it as a floor rather than the answer.\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the body must carry\n  %q\ngot:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "figure") || strings.Contains(stdout, "no version to move") {
		t.Errorf("the caveat qualifies a figure the headline does not carry:\n%s", stdout)
	}
}

// TestPreviewOnAShallowCheckoutPRCaveatNamesNoFigure: the PR side's caveat
// under a headline that names no figure — curry has no release tag within
// the --depth 3 clone's reach — qualifies what each headline says, as the
// pending caveat beside it does. It said "may be missing from the figures
// above" there, figures no headline carried (measured 2026-11-09: a pull
// whose second commit's file listing GitHub answered 422 for). Mutation row
// preview-pr-caveat-qualifies-a-figure-nobody-named.
func TestPreviewOnAShallowCheckoutPRCaveatNamesNoFigure(t *testing.T) {
	_, clone := shallowTwoLineClone(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9): `[` + apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `,` +
			apiCommit("u1", "akira-toriyama", ":bug:~ fix what GitHub would not list") + `]`,
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
		commitFilesPath("u1"): apiUnknownSHA,
	}))
	t.Chdir(clone)
	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"\n**curry** — 🔧 Merging this PR raises **patch** — no release tag on this line is within this shallow checkout's reach, so the release it lands in cannot be named here.\n",
		"(u1). A line one of those commits touches in files GitHub did not list may be missing from what each headline says, so treat each as a floor rather than the answer.\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the body must carry\n  %q\ngot:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "figure") {
		t.Errorf("a caveat qualifies a figure no headline carries:\n%s", stdout)
	}
}

// TestPreviewPRCaveatOnAFullCheckoutNamesNoFigure is the command half of
// TestRenderPackagesPRShortNamesNoFigureNoHeadlineCarries. On a full
// checkout a pull with a commit whose file listing GitHub answered 422 for
// posted "may be missing from the figures above" under headlines that carry
// none. That held in every shape measured (2026-11-09): the walk skipped or
// run, untagged curry moving nothing alone or beside haiku, and a tagged
// haiku alone. The caveat makes a floor of "moves nothing" when no
// line moves, and qualifies what each headline says beside curry's headline,
// which names no version. Mutation rows
// preview-pr-caveat-points-at-figures-no-line-moves and
// preview-pr-caveat-points-at-a-version-an-untagged-line-lacks.
func TestPreviewPRCaveatOnAFullCheckoutNamesNoFigure(t *testing.T) {
	const curry = "\n**curry** — ⏸️ Merging this PR moves nothing — and this line has no release tag yet, so there is no version to move.\n"
	const haiku = "\n**haiku** — ⏸️ Merging this PR moves nothing — and nothing release-worthy is pending since **haiku/v0.1.0**.\n"
	const floor = "(u1). A line one of those commits touches in files GitHub did not list may move all the same, so treat \"moves nothing\" as a floor rather than the answer.\n"
	const each = "(u1). A line one of those commits touches in files GitHub did not list may be missing from what each headline says, so treat each as a floor rather than the answer.\n"
	tidyCurry := apiCommit("c1", "akira-toriyama", ":memo:= document the curry")
	tidyHaiku := apiCommit("h1", "akira-toriyama", ":memo:= tidy the season")
	for _, tc := range []struct {
		name string
		tags []string
		pull []string
		want []string
	}{
		{"the walk skipped: an untagged line alone moves nothing", []string{"haiku/v0.1.0"}, []string{tidyCurry}, []string{curry, floor}},
		{"the walk run: a tagged and an untagged line move nothing", []string{"haiku/v0.1.0"}, []string{tidyHaiku, tidyCurry}, []string{haiku, curry, floor}},
		{"a tagged line alone moves nothing", []string{"haiku/v0.1.0", "curry/v0.1.0"}, []string{tidyHaiku}, []string{haiku, floor}},
		{"a line moves beside an untagged one that moves nothing", []string{"haiku/v0.1.0"}, []string{apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season"), tidyCurry}, []string{"**haiku/v0.1.0 → haiku/v0.2.0**", curry, each}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := testutil.NewTwoLineRepo(t)
			for _, tag := range tc.tags {
				testutil.Git(t, dir, "akira-toriyama", "tag", tag)
			}
			usePR(t, walkServer(t, pullsRoutes(t, dir, map[string]string{
				pullCommitsPath(9):    "[" + strings.Join(tc.pull, ",") + "," + apiCommit("u1", "akira-toriyama", ":bug:~ fix what GitHub would not list") + "]",
				commitFilesPath("h1"): apiFiles("haiku/season.go"),
				commitFilesPath("c1"): apiFiles("curry/curry.go"),
				commitFilesPath("u1"): apiUnknownSHA,
			})))
			t.Chdir(dir)
			code, stdout, stderr := runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("the body must carry\n  %q\ngot:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "figure") {
				t.Errorf("a caveat qualifies a figure no headline carries:\n%s", stdout)
			}
		})
	}
}
