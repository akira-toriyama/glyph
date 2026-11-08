package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestPreviewOnAShallowCheckoutDoesNotAssertNoTag: preview's floor guard
// reads whether the checkout is shallow before it concludes that the base
// branch holds no release tag. A --depth 1 clone of a tagged repository
// holds no tag (the control below measures it), and the guard then said "no
// release tag in HEAD's history" on stderr and "The base branch holds no v*
// release tag yet" in the body — of a repository that has v0.1.0 (t-txed,
// measured 2026-11-04 at 2f9428a). Now it says what the walk says of a
// shallow checkout, in the walk's words, on stderr and in the body; the
// verdict stays the PR side's and the exit code stays 0. The full untagged
// clone is the other control: no shallow sentence, the no-tag sentence.
// Mutation row preview-floor-guard-asserts-no-tag-on-a-shallow-checkout.
func TestPreviewOnAShallowCheckoutDoesNotAssertNoTag(t *testing.T) {
	const shallowWarning = "::warning::glyph: this is a SHALLOW checkout: the walk cannot tell a commit that never landed on the released branch from one git does not have, so a merged pull request's commits can be counted again even though an earlier tag shipped them. Check out with full history (actions/checkout with fetch-depth: 0) before releasing\n"
	const shallowClause = "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)"
	usePR(t, prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":sparkles:^ add the palette")+`]`))

	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= note the palette")
	clone := shallowClone(t, dir, 1)
	if tags := testutil.Git(t, clone, "akira-toriyama", "tag", "-l"); tags != "" {
		t.Fatalf("control: the --depth 1 clone must hold no tag, got %q", tags)
	}
	t.Chdir(clone)
	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0 — the verdict stays the PR side's\nstderr: %s", code, stderr)
	}
	res := decodeJSON[struct {
		Level, Next, Body string
		Untagged          bool
	}](t, stdout)
	if res.Level != "minor" || res.Next != "v0.1.0" || !res.Untagged {
		t.Fatalf("the PR side's verdict must stand: %+v", res)
	}
	if !strings.Contains(stderr, shallowWarning) || !strings.Contains(stderr, "::warning::glyph: no release tag within this shallow checkout's reach — previewing the PR's own verdict (the pending walk needs a release floor)\n") {
		t.Errorf("stderr must say the checkout is shallow in the walk's words, and skip the walk for that reason:\n%s", stderr)
	}
	if strings.Contains(stderr, "no release tag in HEAD's history") {
		t.Errorf("stderr asserts of HEAD's history what a shallow clone cannot know:\n%s", stderr)
	}
	for _, want := range []string{
		"the first release here would be **v0.1.0**, if no release tag lies past this shallow checkout's reach.",
		"> The pending side of this fold is INCOMPLETE: " + shallowClause + ".",
		"No v* release tag is within this shallow checkout's reach, so nothing merged earlier is folded in.",
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("the body must carry\n  %q\ngot:\n%s", want, res.Body)
		}
	}
	if strings.Contains(res.Body, "holds no v* release tag") {
		t.Errorf("the body asserts the base branch holds no tag, which the repository refutes:\n%s", res.Body)
	}

	// Control: a full clone with no tag keeps the no-tag sentences and says
	// nothing about shallowness.
	t.Chdir(testutil.NewRepo(t))
	code, stdout, stderr = runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, floorWarningBare) || strings.Contains(stderr, "SHALLOW") || strings.Contains(stdout, "shallow") || !strings.Contains(stdout, "The base branch holds no v* release tag yet") {
		t.Errorf("the full untagged clone must keep the no-tag sentences and name no shallowness:\nstderr: %s\nbody: %s", stderr, stdout)
	}
}
