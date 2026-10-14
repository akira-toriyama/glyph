package cli

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// The skipped pending walk is named in one sentence (skippedWalkWarning),
// spelled for the bare line and, with "on any line this PR touches" and
// "per line", for the lines.
const (
	floorWarningBare  = "::warning::glyph: no release tag in HEAD's history — previewing the PR's own verdict (the pending walk needs a release floor)\n"
	floorWarningLines = "::warning::glyph: no release tag in HEAD's history on any line this PR touches — previewing the PR's own verdict per line (the pending walk needs a release floor)\n"
)

// testRepoUntagged is testRepo without its v0.1.0 tag — a repository before its
// first release, which is the only state the release-floor guard is about. It
// lives here rather than in gitrepo_test.go because it is the negative case of
// that helper's central assumption, and reading the two side by side is the
// point.
func testRepoUntagged(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	testGit(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
	preset, _ := config.Preset("gemoji")
	if err := os.WriteFile(filepath.Join(dir, "glyph.toml"), preset, 0o644); err != nil {
		t.Fatalf("write glyph.toml: %v", err)
	}
	testGit(t, dir, "akira-toriyama", "add", "glyph.toml")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= begin the project")
	return dir
}

// TestPreviewUntaggedSkipsTheWalk is the release-floor guard, and it is a COST
// test as much as a correctness one. walkServer fails the test on any path it
// was not given, so serving ONLY the PR's commits proves the pending walk never
// ran: on a repo with no v* tag that walk would resolve the entire history at
// one API round-trip per commit — the thing that makes fleet-wide distribution
// unsafe — for an answer that cannot matter, since nothing is unreleased when
// nothing was ever released.
func TestPreviewUntaggedSkipsTheWalk(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+
		apiCommit("aaa1111", "akira-toriyama", ":sparkles:^ add the palette")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "the first release here would be **v0.1.0**") {
		t.Errorf("untagged headline missing:\n%s", stdout)
	}
	// Both sentences state the fact of the history the base is read from, not
	// of the repository: a v* tag on another branch is no release of this one,
	// and "this repository has no v* release tag" beside a `git tag -l` that
	// lists one is the sentence t-gt9n forbids (DESIGN §4).
	if !strings.Contains(stdout, "The base branch holds no v* release tag yet") {
		t.Errorf("body does not explain the skipped fold:\n%s", stdout)
	}
	// The skipped walk must be named, not silent — the house rule is that an
	// input glyph declines to read is a fact the log states.
	if !strings.Contains(stderr, floorWarningBare) {
		t.Errorf("skipped walk not warned about: %s", stderr)
	}
}

// TestPreviewNoneExitsZero: a none verdict is a real answer here, unlike bump
// and notes where it is the soft no-release signal. "This PR moves nothing" is
// exactly what the reviewer asked, and a caller that must post the comment
// cannot have the renderer exit non-zero on the commonest outcome.
func TestPreviewNoneExitsZero(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":memo:= document it")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("a none preview must exit 0, got %d: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "<!-- glyph-pr-verdict -->") {
		t.Errorf("body must still render for a none verdict:\n%s", stdout)
	}
	if !strings.Contains(stdout, "moves nothing") {
		t.Errorf("none headline missing:\n%s", stdout)
	}
}

// TestPreviewJSONFoldsTheLevels pins the machine surface: level/next are the
// FOLDED answer, so a caller never re-derives the winner from the two sides —
// which is the shell arithmetic this command exists to absorb.
func TestPreviewJSONFoldsTheLevels(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	srv := prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":bug:~ fix a crash")+`]`)
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview --json exited %d: %s", code, stderr)
	}
	var got struct {
		Current  string `json:"current"`
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Level != "patch" || got.Next != "v0.0.1" {
		t.Errorf("folded verdict = %s/%s, want patch/v0.0.1", got.Level, got.Next)
	}
	if !got.Untagged {
		t.Error("untagged not reported for a repo with no v* tag")
	}
	// pending is uncomputed here (the walk was skipped) and must serialize as
	// the level "none", never as the empty zero value of the Bump type.
	if got.Pending != "none" {
		t.Errorf("pending = %q, want %q", got.Pending, "none")
	}
	if got.PR != "patch" {
		t.Errorf("pr = %q, want patch", got.PR)
	}
	if !strings.HasPrefix(got.Body, "<!-- glyph-pr-verdict -->") {
		t.Errorf("body missing its marker: %q", got.Body)
	}
}

// TestPreviewNotes: --notes folds the rendered release notes into the comment.
// Off by default — the headline is the message and the notes are for whoever
// asks.
func TestPreviewNotes(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	body := `[` + apiCommit("aaa1111", "akira-toriyama", ":sparkles:(ui)^ add the palette") + `]`
	usePR(t, prServer(t, 7, body))

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--notes")
	if code != 0 {
		t.Fatalf("preview --notes exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "<summary>Release notes preview</summary>") {
		t.Errorf("notes not folded in:\n%s", stdout)
	}
	if !strings.Contains(stdout, "add the palette") {
		t.Errorf("notes body missing:\n%s", stdout)
	}
}

// TestPreviewNotesOffByDefault guards the default: the same input without
// --notes must carry no disclosure block.
func TestPreviewNotesOffByDefault(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	usePR(t, prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":sparkles:^ add it")+`]`))

	code, stdout, _ := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d", code)
	}
	if strings.Contains(stdout, "<details>") {
		t.Errorf("notes rendered without --notes:\n%s", stdout)
	}
}

// TestPreviewFoldsPendingOverPR is the command's reason to exist, on the path
// that actually walks: a tagged repo with a minor already merged-but-unreleased
// and a PR that only fixes a bug. The PR's own level is patch, but the version
// it lands on is the pending minor — the fold no caller should be doing in
// shell. The walk resolves the squash commit to PR 3 and re-expands it, which
// is also what makes the pending side squash-safe.
func TestPreviewFoldsPendingOverPR(t *testing.T) {
	dir, _ := testRepo(t) // tagged v0.1.0
	testCommit(t, dir, "akira-toriyama", ":sparkles:^ land a feature (#3)")
	t.Chdir(dir)
	head := strings.TrimSpace(testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD"))

	srv := walkServer(t, map[string]string{
		// The PR under preview: a lone bug fix.
		pullCommitsPath(7): `[` + apiCommit("bbb2222", "akira-toriyama", ":bug:~ fix a crash") + `]`,
		// The walk: main's squash commit resolves to merged PR 3 …
		commitPullsPath(head): `[{"number":3,"merged_at":"2026-07-17T00:00:00Z","merge_commit_sha":"` + head + `"}]`,
		// … which re-expands into the feature that makes the pending level minor.
		pullCommitsPath(3): `[` + apiCommit("ccc3333", "akira-toriyama", ":sparkles:^ land a feature") + `]`,
	})
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	var got struct {
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
		Body     string `json:"body"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Untagged {
		t.Error("a tagged repo must not report untagged")
	}
	if got.PR != "patch" || got.Pending != "minor" {
		t.Errorf("sides = pr:%s pending:%s, want pr:patch pending:minor", got.PR, got.Pending)
	}
	// The fold: pending (minor) outranks the PR (patch), so the version lands on
	// the minor and the PR does not move it.
	if got.Level != "minor" || got.Next != "v0.2.0" {
		t.Errorf("fold = %s/%s, want minor/v0.2.0", got.Level, got.Next)
	}
	if !strings.Contains(got.Body, "the next release stays **v0.2.0** (a **minor** bump is already pending)") {
		t.Errorf("headline does not report the pending winner:\n%s", got.Body)
	}
}

// TestPreviewPRMultiCommitListing: the PR side of the preview folds over the
// pull's WHOLE commit listing, max-fold like everywhere else. Every other
// preview test feeds a one-commit listing (t-njmw found the gap: 9 tests,
// none with two commits in one pull), so a preview that read only the first
// listing entry would have stayed green everywhere while under-reporting any
// multi-commit PR whose stronger sigil came second — which is exactly the
// order this test uses.
func TestPreviewPRMultiCommitListing(t *testing.T) {
	dir, _ := testRepo(t) // tagged v0.1.0, HEAD on the tag: the pending side is empty
	t.Chdir(dir)

	srv := walkServer(t, map[string]string{
		pullCommitsPath(7): `[` +
			apiCommit("aaa1111", "akira-toriyama", ":memo:= note the thing") + `,` +
			apiCommit("bbb2222", "akira-toriyama", ":sparkles:^ add the thing") + `]`,
	})
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	var got struct {
		Level string `json:"level"`
		Next  string `json:"next"`
		PR    string `json:"pr"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.PR != "minor" {
		t.Errorf("pr side = %s, want minor (the fold must read the whole listing, not its first entry)", got.PR)
	}
	if got.Level != "minor" || got.Next != "v0.2.0" {
		t.Errorf("fold = %s/%s, want minor/v0.2.0", got.Level, got.Next)
	}
}

// TestPreviewRequiresPR: without --pr there is no pull request to preview.
// Caller input, so usage (2) — never a walk over an empty input.
func TestPreviewRequiresPR(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	code, _, stderr := runGlyph(t, "preview")
	if code != 2 {
		t.Fatalf("missing --pr exited %d, want 2 (usage): %s", code, stderr)
	}
}

// TestPreviewZeroVersionTagIsTagged: a repository tagged v0.0.0 HAS released,
// and the pending walk must run.
//
// untagged used to be decided by comparing the parsed version against the zero
// value, which is the same answer for "no tag anywhere" and "the tag is
// v0.0.0". The second is an ordinary thing to have cut — it is what a repo that
// wants a floor before its first real release does — and glyph then skipped its
// pending walk and told the reviewer, in a durable PR comment, that the
// repository had no release tag. Measured on the same checkout before this
// change: `preview --pr 7 --json` reported {"untagged":true,"pending":"none"}
// while `bump --since-tag=auto --json` walked v0.0.0..HEAD and answered
// {"level":"minor"}. The existence question is about the TAG, so it is now
// asked of the tag name.
func TestPreviewZeroVersionTagIsTagged(t *testing.T) {
	dir := testRepoUntagged(t)
	testGit(t, dir, "akira-toriyama", "tag", "v0.0.0")
	pending := squashCommit(t, dir, "Add a menu", 9)
	t.Chdir(dir)

	srv := walkServer(t, map[string]string{
		pullCommitsPath(7):       `[` + apiCommit("aaa1111", "akira-toriyama", ":bug:~ fix a crash") + `]`,
		commitPullsPath(pending): `[` + apiPullRef(9, "2026-07-24T00:00:00Z", pending) + `]`,
		pullCommitsPath(9):       `[` + apiCommit("bbb2222", "akira-toriyama", ":sparkles:^ add a menu") + `]`,
	})
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"untagged":false`) {
		t.Errorf("a v0.0.0-tagged repository must not read as untagged:\n%s", stdout)
	}
	// The walk ran AND its answer reached the fold: walkServer fails on any
	// route it was not given, so serving the pending routes proves the request
	// happened, and the level proves the result was used.
	if !strings.Contains(stdout, `"pending":"minor"`) {
		t.Errorf("the pending walk must run and fold in the merged :sparkles:; got:\n%s", stdout)
	}
	if strings.Contains(stderr, "no release tag in HEAD's history") {
		t.Errorf("a tagged repository must not be told it has no release tag: %s", stderr)
	}
}

// TestPreviewIncompletePendingWalkSaysSoInTheBody: the comment states a
// conclusion about what is pending, and an incomplete walk produces the same
// empty fold as a base branch that genuinely holds nothing.
//
// release refuses to ACT on that ambiguity; preview has nothing to refuse, so
// it has to say it — and say it in the BODY, not only in the ::warning:: the
// walk already emits. This body is pasted into a pull request by
// pr-verdict.yml and read later by a reviewer who never opens the workflow log:
// the whole reason the prose lives in a tested package is that it makes claims
// which must stay true wherever it is distributed.
func TestPreviewIncompletePendingWalkSaysSoInTheBody(t *testing.T) {
	dir, _ := testRepo(t)
	// A merge point GitHub has not indexed yet: the walk knows a pull is there,
	// cannot resolve it, and comes back short by whatever it changed.
	pending := squashCommit(t, dir, "Add a menu", 9)
	t.Chdir(dir)

	srv := walkServer(t, map[string]string{
		pullCommitsPath(7):       `[` + apiCommit("aaa1111", "akira-toriyama", ":bug:~ fix a crash") + `]`,
		commitPullsPath(pending): apiUnknownSHA,
	})
	usePR(t, srv)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
	if code != 0 {
		t.Fatalf("preview exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "[!WARNING]") || !strings.Contains(stdout, "INCOMPLETE") {
		t.Errorf("the body must qualify its pending claim when the walk came back short:\n%s", stdout)
	}
	if !strings.Contains(stdout, "as the walk could read") {
		t.Errorf("the footer must stop claiming a complete fold:\n%s", stdout)
	}
}

// TestPreviewRunsFromOptionsWithoutCobra: the run below RunE is a function of
// its options — no command built, no flag parsed — so the pull, --notes and
// --json reach it as fields, not as package state a cobra run has to set
// first.
func TestPreviewRunsFromOptionsWithoutCobra(t *testing.T) {
	dir := testRepoUntagged(t)
	t.Chdir(dir)
	usePR(t, prServer(t, 7, `[`+apiCommit("aaa1111", "akira-toriyama", ":sparkles:(ui)^ add the palette")+`]`))
	stdout, stderr, restore := captureOut()
	defer restore()

	err := previewRun(context.Background(), previewOptions{pr: 7, notes: true, json: true})
	const verdict = `{"current":"v0.0.0","untagged":true,"level":"minor","next":"v0.1.0","pr":"minor","pending":"none","body":"<!-- glyph-pr-verdict -->`
	if err != nil || !strings.HasPrefix(stdout.String(), verdict) || !strings.Contains(stdout.String(), "Release notes preview") {
		t.Fatalf("previewRun from options: %v, stdout %q, want the JSON verdict with the notes folded in\nstderr: %s", err, stdout.String(), stderr.String())
	}
}

// TestPreviewBareLineAsksForNoFile: a repository without [[packages]] holds
// the whole pull on its one bare line, so no commit's files are asked for —
// partitionLines' identity arm, on the pull's side as on the walk's. The
// stand-in serves every commit's file listing and records what it answered,
// so a preview that placed the pull by its files would be counted here, not
// failed as an unknown route; tagged, the pending side is walked once.
func TestPreviewBareLineAsksForNoFile(t *testing.T) {
	pull := map[string]string{
		pullCommitsPath(7): `[` +
			apiCommit("aaa1111", "akira-toriyama", ":sparkles:(ui)^ add the palette") + `,` +
			apiCommit("bbb2222", "akira-toriyama", ":bug:~ fix a crash") + `]`,
		commitFilesPath("aaa1111"): apiFiles("ui/palette.go"),
		commitFilesPath("bbb2222"): apiFiles("crash.go"),
	}
	t.Run("untagged: the pull's listing alone", func(t *testing.T) {
		dir := testRepoUntagged(t)
		t.Chdir(dir)
		srv, seen := recordingWalkServer(t, pull)
		usePR(t, srv)

		code, _, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
		if want := []string{pullCommitsPath(7)}; !slices.Equal(*seen, want) {
			t.Errorf("requests = %q, want %q", *seen, want)
		}
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
	})
	t.Run("tagged: the listing, then one walk", func(t *testing.T) {
		dir, _ := testRepo(t)
		testCommit(t, dir, "akira-toriyama", ":sparkles:^ land a feature (#3)")
		head := strings.TrimSpace(testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD"))
		t.Chdir(dir)
		routes := map[string]string{
			commitPullsPath(head):      `[` + apiPullRef(3, "2026-07-17T00:00:00Z", head) + `]`,
			pullCommitsPath(3):         `[` + apiCommit("ccc3333", "akira-toriyama", ":sparkles:^ land a feature") + `]`,
			commitFilesPath("ccc3333"): apiFiles("feature.go"),
		}
		maps.Copy(routes, pull)
		srv, seen := recordingWalkServer(t, routes)
		usePR(t, srv)

		code, _, stderr := runGlyph(t, "preview", "--pr", "7", "--json")
		if want := []string{pullCommitsPath(7), commitPullsPath(head), pullCommitsPath(3)}; !slices.Equal(*seen, want) {
			t.Errorf("requests = %q, want %q", *seen, want)
		}
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
	})
}

// TestPreviewBareLineRendersTheScalarBody: without [[packages]] the bare line
// is the one line of preview's one loop, and the render alone keeps its shape
// — preview.Render's scalar body and the scalar verdict, the pre-packages
// surface byte for byte. Rendered as a line, as a root package is, the same
// pull heads its headline "**** — ", opens its table "### " and closes
// "folded, per line, … since **v0.1.0** ()" (mutation row
// preview-bare-line-renders-as-a-line), so the bytes are pinned whole:
// untagged with the notes folded in, and tagged with a pending side, as the
// body and as the --json verdict.
func TestPreviewBareLineRendersTheScalarBody(t *testing.T) {
	const untaggedBody = "<!-- glyph-pr-verdict -->\n" +
		"🔼 Merging this PR raises **minor** — the first release here would be **v0.1.0**.\n\n" +
		"| commit | sigil | bump |\n|---|---|---|\n" +
		"| :sparkles:(ui)^ add the palette | `^` | minor |\n" +
		"| :memo:= note the palette | `=` | none |\n\n" +
		"<details>\n<summary>Release notes preview</summary>\n\n" +
		"## Features\n\n- :sparkles:(ui)^ add the palette (#7) @akira-toriyama\n\n" +
		"## Dependencies\n\n- Bump golang.org/x/net from 0.1.0 to 0.2.0 (#7) dependabot\\[bot]\n\n" +
		"</details>\n\n" +
		"Computed from the 2 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. The base branch holds no v* release tag yet, so nothing merged earlier is folded in. Pushing more commits updates this comment.\n"
	const taggedBody = "<!-- glyph-pr-verdict -->\n" +
		"🔧 Merging this PR adds **patch**-level changes — the next release stays **v0.2.0** (a **minor** bump is already pending).\n\n" +
		"| commit | sigil | bump |\n|---|---|---|\n" +
		"| :bug:~ fix a crash | `~` | patch |\n\n" +
		"Computed from the 1 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with what is already merged on the base branch since **v0.1.0**. Pushing more commits updates this comment.\n"

	t.Run("untagged, with the notes", func(t *testing.T) {
		dir := testRepoUntagged(t)
		t.Chdir(dir)
		usePR(t, prServer(t, 7, `[`+
			apiCommit("aaa1111", "akira-toriyama", ":sparkles:(ui)^ add the palette")+`,`+
			apiCommit("bbb2222", "akira-toriyama", ":memo:= note the palette")+`,`+
			apiCommit("ddd4444", "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0")+`]`))

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "7", "--notes")
		if code != 0 || stdout != untaggedBody {
			t.Fatalf("exit %d, body:\n got: %q\nwant: %q\nstderr: %s", code, stdout, untaggedBody, stderr)
		}
	})
	t.Run("tagged, with a pending side", func(t *testing.T) {
		dir, _ := testRepo(t)
		testCommit(t, dir, "akira-toriyama", ":sparkles:^ land a feature (#3)")
		head := strings.TrimSpace(testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD"))
		t.Chdir(dir)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(7):    `[` + apiCommit("bbb2222", "akira-toriyama", ":bug:~ fix a crash") + `]`,
			commitPullsPath(head): `[` + apiPullRef(3, "2026-07-17T00:00:00Z", head) + `]`,
			pullCommitsPath(3):    `[` + apiCommit("ccc3333", "akira-toriyama", ":sparkles:^ land a feature") + `]`,
		}))

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "7")
		if code != 0 || stdout != taggedBody {
			t.Fatalf("exit %d, body:\n got: %q\nwant: %q\nstderr: %s", code, stdout, taggedBody, stderr)
		}
		verdict := `{"current":"v0.1.0","untagged":false,"level":"minor","next":"v0.2.0","pr":"patch","pending":"minor","body":"` +
			strings.ReplaceAll(taggedBody, "\n", `\n`) + "\"}\n"
		code, stdout, stderr = runGlyph(t, "preview", "--pr", "7", "--json")
		if code != 0 || stdout != verdict {
			t.Fatalf("exit %d, --json:\n got: %q\nwant: %q\nstderr: %s", code, stdout, verdict, stderr)
		}
	})
}

// TestPreviewReleaseFloorWarningIsOneSentence: a skipped pending walk is named
// on stderr by one producer whatever the shape — on the bare line in a
// sentence that says nothing of lines, for a pull whose touched lines have no
// tag with "on any line this PR touches" and "per line" — and not at all when
// the pull touches no line, where there was nothing to walk for. The bare line used to say it in words of
// its own ("no v* release tag in HEAD's history — previewing this PR's own
// verdict only"), and the packages half was asserted nowhere.
func TestPreviewReleaseFloorWarningIsOneSentence(t *testing.T) {
	bare := testRepoUntagged(t)
	lines, _ := packagesRepo(t)
	testGit(t, lines, "akira-toriyama", "tag", "-d", "haiku/v0.1.0")
	testGit(t, lines, "akira-toriyama", "tag", "-d", "curry/v0.1.0")
	sharedOnly := map[string]string{
		pullCommitsPath(9):    `[` + apiCommit("s1", "akira-toriyama", ":memo:= document the lines") + `]`,
		commitFilesPath("s1"): apiFiles("README.md"),
	}
	for _, tc := range []struct {
		name   string
		dir    string
		routes map[string]string
		want   string
		count  int
	}{
		{"bare line", bare, crossLinePull(9), floorWarningBare, 1},
		{"untagged lines", lines, crossLinePull(9), floorWarningLines, 1},
		{"no line touched", lines, sharedOnly, floorWarningLines, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.dir)
			usePR(t, walkServer(t, tc.routes))

			code, _, stderr := runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			if n := strings.Count(stderr, tc.want); n != tc.count {
				t.Errorf("the skipped walk is named %d time(s), want %d:\n%s", n, tc.count, stderr)
			}
		})
	}
}

// TestPreviewReadsTheRepoItIsGiven: --repo names the repository every read
// asks — the pull's listing, its commits' file listings and the pending walk
// — over $GITHUB_REPOSITORY, which usePR sets to another. No test ran preview
// with --repo, so a read that dropped the flag for the environment's
// repository survived the whole suite at each of the three. The stand-in
// serves both repositories and records what it answered, so a read of the
// wrong one is counted here rather than failed as an unknown route.
func TestPreviewReadsTheRepoItIsGiven(t *testing.T) {
	other := func(path string) string {
		return strings.Replace(path, "/repos/akira-toriyama/glyph/", "/repos/other/name/", 1)
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (dir string, routes map[string]string, want []string)
	}{
		{"bare line: the listing and the walk", func(t *testing.T) (string, map[string]string, []string) {
			dir, _ := testRepo(t)
			testCommit(t, dir, "akira-toriyama", ":sparkles:^ land a feature (#3)")
			head := strings.TrimSpace(testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD"))
			routes := map[string]string{
				pullCommitsPath(9):    `[` + apiCommit("h1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
				commitPullsPath(head): `[` + apiPullRef(3, "2026-07-17T00:00:00Z", head) + `]`,
				pullCommitsPath(3):    `[` + apiCommit("ccc3333", "akira-toriyama", ":sparkles:^ land a feature") + `]`,
			}
			return dir, routes, []string{pullCommitsPath(9), commitPullsPath(head), pullCommitsPath(3)}
		}},
		{"lines: the listing, the files and the walk", func(t *testing.T) (string, map[string]string, []string) {
			dir, _ := packagesRepo(t)
			merged := touch(t, dir, "akira-toriyama", ":sparkles:^ add an ingredient", "curry/curry.go")
			routes := crossLinePull(9)
			routes[commitPullsPath(merged)] = `[]`
			return dir, routes, []string{pullCommitsPath(9), commitFilesPath("h1"), commitFilesPath("c1"), commitFilesPath("s1"), commitPullsPath(merged)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, routes, want := tc.setup(t)
			both := map[string]string{}
			for path, body := range routes {
				both[path], both[other(path)] = body, body
			}
			srv, seen := recordingWalkServer(t, both)
			usePR(t, srv)
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "preview", "--pr", "9", "--repo", "other/name")
			for i := range want {
				want[i] = other(want[i])
			}
			if !slices.Equal(*seen, want) {
				t.Errorf("preview --repo other/name asked:\n %q\nwant:\n %q", *seen, want)
			}
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
		})
	}
}

// TestPreviewNotesCiteThePullUnderReview: the notes preview cites each commit
// by the pull under review and by its own sha, which that pull's head branch
// holds — on the bare line and per line alike. The presets' note line names
// no $hash, so the citation is read through one that names both: neither half
// was asserted on either shape, and a preview citing no pull, or no sha,
// survived the whole suite.
func TestPreviewNotesCiteThePullUnderReview(t *testing.T) {
	const presetLine = "line = '- $subject$[ ($pr)] @$author$[ with $coauthors]'"
	citing := func(t *testing.T, dir string) {
		t.Helper()
		path := filepath.Join(dir, "glyph.toml")
		b, err := os.ReadFile(path) // #nosec G304 -- a path under the test's own temp dir
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), presetLine) {
			t.Fatalf("the fixture's glyph.toml carries no %s to replace", presetLine)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(b), presetLine, "line = '- $subject ($hash, $pr)'", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bare := testRepoUntagged(t)
	lines, _ := packagesRepo(t)
	for _, tc := range []struct{ name, dir string }{{"bare line", bare}, {"lines", lines}} {
		t.Run(tc.name, func(t *testing.T) {
			citing(t, tc.dir)
			t.Chdir(tc.dir)
			usePR(t, walkServer(t, crossLinePull(9)))

			code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--notes")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			if !strings.Contains(stdout, "\n- :sparkles:(haiku)^ add a season (h1, #9)\n") {
				t.Errorf("the notes must cite the listed sha and the pull under review:\n%s", stdout)
			}
		})
	}
}
