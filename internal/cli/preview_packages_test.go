package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// This file pins `glyph preview` for a packages repository (DESIGN §4.1,
// "Preview"): one verdict per line the pull's commits are attributed to, in
// config order, a line the pull does not touch never mentioned.

// previewVerdictLines decodes preview --json in packages mode.
type previewVerdictLines struct {
	Current  string `json:"current"`
	Untagged bool   `json:"untagged"`
	Level    string `json:"level"`
	Next     string `json:"next"`
	PR       string `json:"pr"`
	Pending  string `json:"pending"`
	Body     string `json:"body"`
	Packages []struct {
		Path     string `json:"path"`
		Current  string `json:"current"`
		Untagged bool   `json:"untagged"`
		Level    string `json:"level"`
		Next     string `json:"next"`
		PR       string `json:"pr"`
		Pending  string `json:"pending"`
	} `json:"packages"`
}

func decodePreviewLines(t *testing.T, stdout string) previewVerdictLines {
	t.Helper()
	var res previewVerdictLines
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("preview --json stdout is not JSON: %v\n%s", err, stdout)
	}
	return res
}

// crossLinePull is the open-pull twin of squashAcrossLines: pull #9's
// listing carries a ^ under haiku/, a ~ under curry/ and a shared-only =,
// each commit's files served on its own commits/{sha} route (the commits
// exist on the pull's branch only, so the API is where preview reads them).
func crossLinePull(number int) map[string]string {
	return map[string]string{
		pullCommitsPath(number): `[` +
			apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `,` +
			apiCommit("s1", "akira-toriyama", ":memo:= document the lines") + `]`,
		commitFilesPath("h1"): apiFiles("haiku/season.go"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
		commitFilesPath("s1"): apiFiles("README.md"),
	}
}

// TestPreviewPackagesOneVerdictPerTouchedLine: the defining probe previewed
// — two headlines, haiku minor and curry patch, each with its own table; a
// third declared line the pull does not touch (fish) is not mentioned; the
// shared-only = commit sits in no line; every scalar is at its zero value and
// packages carries the folded verdict per line (mutation rows
// preview-packages-untouched-line-is-mentioned,
// preview-packages-scalar-verdict-describes-one-line and
// preview-packages-scalar-claims-the-pull-moves-nothing). pr and pending are
// scalars like the rest: the first cut set them to none — "this pull moves
// nothing" beside a packages[] whose lines move (t-xbk0, measured 2026-09-26
// on glyph-monorepo-test #30, haiku folding major under pr=none).
func TestPreviewPackagesOneVerdictPerTouchedLine(t *testing.T) {
	dir, base := packagesRepo(t)
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \"fish\"\n")
	writeFile(t, dir, "fish/fish.go", "package fish\n")
	testGit(t, dir, "akira-toriyama", "add", ".")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare a third line")
	declared := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "fish/v0.1.0")
	routes := crossLinePull(9)
	routes[commitPullsPath(declared)] = `[]`
	_ = base
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if res.Current != "" || res.Level != "" || res.Next != "" {
		t.Fatalf("scalars must describe no line under packages: %s", stdout)
	}
	if res.PR != "" || res.Pending != "" {
		t.Fatalf("pr / pending must be empty — not computed — under packages; pr=%q pending=%q claims the pull moves nothing beside a packages[] whose lines move: %s", res.PR, res.Pending, stdout)
	}
	if len(res.Packages) != 2 || res.Packages[0].Path != "haiku" || res.Packages[1].Path != "curry" {
		t.Fatalf("packages = %+v, want haiku then curry (fish untouched, unmentioned)", res.Packages)
	}
	h, c := res.Packages[0], res.Packages[1]
	if h.Current != "v0.1.0" || h.PR != "minor" || h.Level != "minor" || h.Next != "v0.2.0" || h.Untagged {
		t.Fatalf("haiku = %+v, want v0.1.0 → minor → v0.2.0", h)
	}
	if c.PR != "patch" || c.Next != "v0.1.1" {
		t.Fatalf("curry = %+v, want patch → v0.1.1", c)
	}
	body := res.Body
	if !strings.HasPrefix(body, "<!-- glyph-pr-verdict -->\n**haiku** — 🔼") || !strings.Contains(body, "\n**curry** — 🔧") {
		t.Fatalf("body must open with one headline per touched line:\n%s", body)
	}
	if !strings.Contains(body, "**haiku/v0.1.0 → haiku/v0.2.0**") || !strings.Contains(body, "**curry/v0.1.0 → curry/v0.1.1**") {
		t.Fatalf("versions must be spelled as the line's tags:\n%s", body)
	}
	if strings.Contains(body, "fish") {
		t.Fatalf("a line the pull does not touch must not be mentioned:\n%s", body)
	}
	if !strings.Contains(body, "### haiku\n") || !strings.Contains(body, "### curry\n") || strings.Contains(body, "document the lines") {
		t.Fatalf("one table per touched line, the shared-only commit in none:\n%s", body)
	}
	if !strings.Contains(body, "since **haiku/v0.1.0** (haiku), **curry/v0.1.0** (curry)") {
		t.Fatalf("the footer must name every line's base:\n%s", body)
	}
}

// TestPreviewPackagesFoldsEachLinesPendingSide: the pending side is one walk
// and applies per line — a ^ already merged under curry escalates curry's
// answer while haiku's stays the PR's own.
func TestPreviewPackagesFoldsEachLinesPendingSide(t *testing.T) {
	dir, _ := packagesRepo(t)
	merged := touch(t, dir, "akira-toriyama", ":sparkles:^ add an ingredient", "curry/curry.go")
	routes := crossLinePull(9)
	routes[commitPullsPath(merged)] = `[]`
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Pending != "none" || h.Level != "minor" {
		t.Fatalf("haiku = %+v, want nothing pending and the PR's minor", h)
	}
	if c.Pending != "minor" || c.PR != "patch" || c.Level != "minor" || c.Next != "v0.2.0" {
		t.Fatalf("curry = %+v, want the pending minor to win over the PR's patch", c)
	}
	if !strings.Contains(res.Body, "**curry** — 🔧 Merging this PR adds **patch**-level changes — the next release stays **curry/v0.2.0**") {
		t.Fatalf("curry's headline must say the pending bump wins:\n%s", res.Body)
	}
}

// TestPreviewPackagesUntaggedLinesSkipTheWalk: when no touched line has a
// release tag there is no pending side to walk — walkServer proves the
// walk never ran — and each line reports its first release.
func TestPreviewPackagesUntaggedLinesSkipTheWalk(t *testing.T) {
	dir, _ := packagesRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "haiku/v0.1.0")
	testGit(t, dir, "akira-toriyama", "tag", "-d", "curry/v0.1.0")
	usePR(t, walkServer(t, crossLinePull(9)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if !res.Packages[0].Untagged || res.Packages[0].Next != "v0.1.0" {
		t.Fatalf("haiku = %+v, want untagged with a first release of v0.1.0", res.Packages[0])
	}
	if !strings.Contains(res.Body, "the first release here would be **haiku/v0.1.0**") || !strings.Contains(res.Body, "haiku and curry has no release tag yet") {
		t.Fatalf("body must say the lines have not released:\n%s", res.Body)
	}
}

// TestPreviewPackagesNothingTouchedSaysSo: a pull whose commits carry
// nothing on any line — shared-only = — says it moves nothing, with the
// marker in place and no walk.
func TestPreviewPackagesNothingTouchedSaysSo(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9):    `[` + apiCommit("s1", "akira-toriyama", ":memo:= document the lines") + `]`,
		commitFilesPath("s1"): apiFiles("README.md"),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if len(res.Packages) != 0 {
		t.Fatalf("packages = %+v, want none", res.Packages)
	}
	if res.Current != "" || res.Level != "" || res.Next != "" || res.PR != "" || res.Pending != "" || res.Untagged {
		t.Fatalf("under packages every scalar is at its zero value whatever the pull touches — the mode decides, not the content: %s", stdout)
	}
	if !strings.HasPrefix(res.Body, "<!-- glyph-pr-verdict -->\n⏸️ Merging this PR moves nothing — its 1 commit(s) touch no declared package.") {
		t.Fatalf("body = %q", res.Body)
	}
}

// TestPreviewPackagesSharedOnlyBumpIsRefused: a ^ under no package in the
// pull is refused here, at exit 3, the way the walk and lint --range refuse
// it — preview says what CI will say, while the branch can still be fixed.
func TestPreviewPackagesSharedOnlyBumpIsRefused(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9):    `[` + apiCommit("s1", "akira-toriyama", ":sparkles:^ add a workspace file") + `]`,
		commitFilesPath("s1"): apiFiles("go.work"),
	}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "preview", "--pr", "9")
	if code != 3 || !strings.Contains(stderr, "touches no declared package") || !strings.Contains(stderr, "fix it on the branch") {
		t.Fatalf("preview exited %d, want 3 with the attribution refusal\nstderr: %s", code, stderr)
	}
}

// TestPreviewPackagesNotesPerLine: --notes folds one notes preview per
// touched line into the block, under the line's own heading.
func TestPreviewPackagesNotesPerLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, crossLinePull(9)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--notes")
	if code != 0 {
		t.Fatalf("preview --notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "<summary>Release notes preview</summary>\n\n# haiku\n\n## Features\n") || !strings.Contains(stdout, "\n# curry\n\n## Fixes\n") {
		t.Fatalf("notes must render per line under the line's heading:\n%s", stdout)
	}
}

// TestPreviewPackagesExcludedAuthorIsPlacedByItsFiles: preview renders the
// set the walk will render (DESIGN §4.1: preview says what CI will say). The
// first cut dropped an exclude_authors commit from every line while the walk
// placed it on every line — two answers in one run (t-sr1c, measured
// 2026-09-11 on glyph-monorepo-test #4 and #22). A bot commit is placed by
// its files: the line it touches is mentioned, with the bump in its notes
// and a verdict of none, and a bot commit under no package touches nothing
// (mutation row preview-packages-excluded-author-dropped).
func TestPreviewPackagesExcludedAuthorIsPlacedByItsFiles(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9): `[` +
			apiCommit("d1", "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `]`,
		commitFilesPath("d1"): apiFiles("haiku/go.mod"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
		pullCommitsPath(10):   `[` + apiCommit("d2", "dependabot[bot]", "Bump actions/checkout from 4 to 5") + `]`,
		commitFilesPath("d2"): apiFiles(".github/workflows/ci.yml"),
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--notes", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if len(res.Packages) != 2 || res.Packages[0].Path != "haiku" || res.Packages[0].PR != "none" || res.Packages[1].Path != "curry" || res.Packages[1].PR != "patch" {
		t.Fatalf("a bot commit places its line in the preview (verdict none) beside the human's: %s", stdout)
	}
	if haiku, curry := lineSection(res.Body, "haiku"), lineSection(res.Body, "curry"); !strings.Contains(haiku, "Bump golang.org/x/net") || strings.Contains(curry, "Bump golang.org/x/net") {
		t.Fatalf("the bump renders under haiku's notes alone:\n%s", res.Body)
	}

	code, stdout, stderr = runGlyph(t, "preview", "--pr", "10")
	if code != 0 || !strings.Contains(stdout, "moves nothing") {
		t.Fatalf("a bot commit under no package touches nothing: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// packagesRepoWithUntaggedLine is the out-of-scope fixture: three declared
// lines where the two the pull will not touch each widen the union in a
// different way — `fish` has no tag at all (the whole history), and `curry`
// is tagged one commit EARLIER than `haiku` (one commit further back). Both
// arms matter: a scope that let tagged strangers in would be invisible
// against same-commit tags.
func packagesRepoWithUntaggedLine(t *testing.T) string {
	t.Helper()
	dir := packagesRepoWith(t, packagesConfig+"\n[[packages]]\npath = \"fish\"\n",
		map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n", "fish/fish.go": "package fish\n"})
	testGit(t, dir, "akira-toriyama", "tag", "curry/v0.1.0")
	touch(t, dir, "akira-toriyama", ":memo:(haiku)= season the haiku", "haiku/haiku.go")
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	return dir
}

// TestPreviewPackagesWalkIgnoresAnUntouchedUntaggedLine: the pending walk's
// RANGE is the touched lines' own, never every declared line's. Resolved over
// all of them, one declared line with no tag took the union to the whole
// history — past the cap that refused the whole command for a pull touching
// only released lines (measured 2026-09-15 on a 211-commit fixture: exit 4 for
// a haiku-only pull), and under it one API round-trip per commit of the
// history for a line nobody asked about (9 where the touched line's own range
// held 1).
//
// walkServer is the assertion: it fails the test on any request the routes do
// not carry, so a walk that reached past haiku's tag would ask about the
// declaring commit and be caught.
func TestPreviewPackagesWalkIgnoresAnUntouchedUntaggedLine(t *testing.T) {
	dir := packagesRepoWithUntaggedLine(t)
	merged := touch(t, dir, "akira-toriyama", ":memo:= note the season", "haiku/notes.md")
	routes := map[string]string{
		pullCommitsPath(9):      `[` + apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `]`,
		commitFilesPath("h1"):   apiFiles("haiku/season.go"),
		commitPullsPath(merged): `[]`,
	}
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0 — a declared line the pull does not touch must not refuse it\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	if len(res.Packages) != 1 || res.Packages[0].Path != "haiku" {
		t.Fatalf("packages = %+v, want haiku alone — the untouched lines are not mentioned", res.Packages)
	}
	if strings.Contains(res.Body, "fish") || strings.Contains(res.Body, "curry") {
		t.Errorf("the body mentions a line the pull does not touch:\n%s", res.Body)
	}
}

// TestPreviewPackagesCapRefusalNamesPreviewsOwnRemedy: when the pull DOES
// touch a line with no tag, the whole-history walk is the honest answer and
// the cap may still refuse it — but the refusal must name a remedy preview
// can perform. It named `--since-tag=TAG`, which preview does not accept
// (measured 2026-09-15: `preview --pr` exited 4 sending an operator to a flag
// the command rejects). Positive control: the default sentence is what the
// other callers still get, asserted in TestPreviewRefusalNamesNoFlagPreviewLacks.
func TestPreviewPackagesCapRefusalNamesPreviewsOwnRemedy(t *testing.T) {
	dir := packagesRepoWith(t, packagesConfig,
		map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n"})
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0") // curry never released
	for i := range sinceTagWalkCap + 1 {
		testCommit(t, dir, "akira-toriyama", fmt.Sprintf(":memo:= note %d", i))
	}
	usePR(t, walkServer(t, crossLinePull(9)))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 4 {
		t.Fatalf("preview exited %d, want 4 — past the cap the unbounded walk is refused\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	out := stdout + stderr
	if !strings.Contains(out, "cut curry/v0.0.0") {
		t.Errorf("the refusal does not name the tag to cut:\n%s", out)
	}
	if strings.Contains(out, "name the walk base yourself with --since-tag=TAG") {
		t.Errorf("the refusal sends the operator to a flag preview rejects:\n%s", out)
	}
	if !strings.Contains(out, "has no --since-tag flag") {
		t.Errorf("the refusal does not say why --since-tag is not the way out here:\n%s", out)
	}
}

// TestPreviewPackagesUntaggedTouchedLineAgreesWithItsVerdict: one run, one
// answer. A touched line with no tag still has its pending side walked
// whenever a tagged sibling in the same pull takes the walk to the whole
// history — and the body used to deny it while the machine verdict beside it
// reported it, because both were rendered from the same `untagged` flag.
// Measured 2026-09-15: body "the first release here would be curry/v0.0.1"
// against JSON next=v0.1.0, and `bump` on the same checkout answers
// curry/v0.1.0 — the human-readable half was the wrong one.
func TestPreviewPackagesUntaggedTouchedLineAgreesWithItsVerdict(t *testing.T) {
	dir, _ := packagesRepo(t)
	testGit(t, dir, "akira-toriyama", "tag", "-d", "curry/v0.1.0")
	touch(t, dir, "akira-toriyama", ":sparkles:^ add an ingredient", "curry/curry.go")
	routes := crossLinePull(9)
	// curry has no tag, so the union is the whole history: every commit in it
	// is asked about, and walkServer fails on any route missing.
	for sha := range strings.FieldsSeq(testGit(t, dir, "akira-toriyama", "rev-list", "HEAD")) {
		routes[commitPullsPath(sha)] = `[]`
	}
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
	if code != 0 {
		t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePreviewLines(t, stdout)
	var curry struct{ next, pending string }
	for _, p := range res.Packages {
		if p.Path == "curry" {
			curry.next, curry.pending = p.Next, p.Pending
		}
	}
	if curry.pending != "minor" || curry.next != "v0.1.0" {
		t.Fatalf("curry's machine verdict = %+v, want the walked pending minor and v0.1.0", curry)
	}
	if !strings.Contains(res.Body, "**curry/"+curry.next+"**") {
		t.Errorf("the body names a different version than the machine verdict (%s):\n%s", curry.next, res.Body)
	}
	if strings.Contains(res.Body, "curry has no release tag yet, so nothing merged earlier is folded in for it") {
		t.Errorf("the body denies a pending side this same run walked and reported:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "curry has no release tag yet, so everything merged so far is folded in for it") {
		t.Errorf("the body must say what an untagged line's walked floor is:\n%s", res.Body)
	}
}

// TestPreviewRefusalNamesNoFlagPreviewLacks: the whole-history refusal names
// the escape hatch its CALLER has. `preview` reads a pull request and has no
// --since-tag, so sending an operator there is a dead end — measured
// 2026-09-15, `preview --pr` exited 4 telling one to "name the walk base
// yourself with --since-tag=TAG". Positive control: the default escape does
// name the flag, so this is a real difference and not an empty string.
func TestPreviewRefusalNamesNoFlagPreviewLacks(t *testing.T) {
	if !strings.Contains(sinceTagEscape, "--since-tag=TAG") {
		t.Fatal("the default escape no longer names --since-tag — this guard's premise is gone and the case below would pass vacuously")
	}
	if f := newPreviewCmd().Flags().Lookup("since-tag"); f != nil {
		t.Fatal("preview now registers --since-tag; previewWalkEscape tells operators it does not, and the two must move together")
	}
	if strings.Contains(previewWalkEscape, "--since-tag=TAG") {
		t.Errorf("preview's refusal sends the operator to a flag preview rejects: %q", previewWalkEscape)
	}
}
