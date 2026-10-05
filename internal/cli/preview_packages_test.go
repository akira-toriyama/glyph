package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	if !strings.Contains(res.Body, "the first release here would be **haiku/v0.1.0**") || !strings.Contains(res.Body, "haiku and curry has no release tag on the base branch yet") {
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
	// omitempty drops the key for a nil and an empty slice alike, so a pull
	// touching no line carries no "packages" key at all.
	if strings.Contains(stdout, `"packages"`) {
		t.Fatalf("a pull touching no declared line carries a packages key: %s", stdout)
	}
	if res.Current != "" || res.Level != "" || res.Next != "" || res.PR != "" || res.Pending != "" || res.Untagged {
		t.Fatalf("under packages every scalar is at its zero value whatever the pull touches — the mode decides, not the content: %s", stdout)
	}
	if !strings.HasPrefix(res.Body, "<!-- glyph-pr-verdict -->\n⏸️ Merging this PR moves nothing — no commit participating in it touches a declared package.\n") {
		t.Fatalf("body = %q", res.Body)
	}
}

// participatingCount reads the number a preview body's footer states.
func participatingCount(t *testing.T, body string) int {
	t.Helper()
	m := regexp.MustCompile(`Computed from the (\d+) commit\(s\) participating in this PR`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the body carries no participating count:\n%s", body)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPreviewPackagesCountsWhatTheFoldReads: the footer's "participating" is
// one set on both packages arms and on the single line — the commits the fold
// reads, each once (DESIGN §4.1; t-rrw0 (1), (2)+(3)). Every listing is
// previewed twice, under [[packages]] and on a single line, and the two
// footers must state one number: the single line's footer has always counted
// the fold's rows, so it is the oracle here, not a figure typed into the
// table.
//
// Measured on the source before this test (b42ca92): the no-line arm counted
// the raw listing — 3 for a shared =, a bot and a merge commit, where the
// single line says 1 — and said "its 3 commit(s) touch no declared package"
// of a listing that holds a merge commit, whose diff nothing reads; the
// per-line arm counted each table's distinct sigil-and-subject pairs — 2 for
// the pull the single line counts 4, under a haiku table of 3 rows (two
// commits sharing a subject collapsed, and the shared = in no table).
//
// Mutation rows preview-packages-counts-the-raw-listing,
// preview-footer-counts-the-tables-distinct-subjects and
// preview-footer-hides-the-commits-on-no-line.
func TestPreviewPackagesCountsWhatTheFoldReads(t *testing.T) {
	const (
		noLine       = "<!-- glyph-pr-verdict -->\n⏸️ Merging this PR moves nothing — no commit participating in it touches a declared package.\n\n"
		offLine      = " sits on no line."
		review       = "| :bug:(haiku)~ address review | `~` | patch |\n"
		haikuSince   = "folded, per line, with what is already merged on the base branch since **haiku/v0.1.0** (haiku)."
		pushingAgain = " Pushing more commits updates this comment.\n"
	)
	shared := apiCommit("s1", "akira-toriyama", ":memo:= document the lines")
	merge := apiMergeCommit("m1", "akira-toriyama", "Merge branch 'main' into topic")
	bot := apiCommit("d1", "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0")
	season := apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season")
	for _, tc := range []struct {
		name    string
		listing []string
		files   map[string]string
		count   int
		check   func(t *testing.T, body string)
	}{
		{
			name:    "no line touched: only the shared = participates",
			listing: []string{shared, bot, merge},
			files:   map[string]string{"s1": apiFiles("README.md"), "d1": apiFiles("go.work")},
			count:   1,
			check: func(t *testing.T, body string) {
				want := noLine + "Computed from the 1 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them." + pushingAgain
				if body != want {
					t.Errorf("body:\n got: %q\nwant: %q", body, want)
				}
			},
		},
		{
			name:    "no line touched: nothing participates",
			listing: []string{bot, merge},
			files:   map[string]string{"d1": apiFiles("go.work")},
			count:   0,
			check: func(t *testing.T, body string) {
				want := noLine + "Computed from the 0 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them." + pushingAgain
				if body != want {
					t.Errorf("body:\n got: %q\nwant: %q", body, want)
				}
			},
		},
		{
			name: "one line touched: two commits share a subject, and the shared = sits on no line",
			listing: []string{season, shared, bot, merge,
				apiCommit("r1", "akira-toriyama", ":bug:(haiku)~ address review"),
				apiCommit("r2", "akira-toriyama", ":bug:(haiku)~ address review")},
			files: map[string]string{"h1": apiFiles("haiku/season.go"), "s1": apiFiles("README.md"), "d1": apiFiles("haiku/go.mod"),
				"r1": apiFiles("haiku/season.go"), "r2": apiFiles("haiku/haiku.go")},
			count: 4,
			check: func(t *testing.T, body string) {
				if n := strings.Count(body, review); n != 2 {
					t.Errorf("haiku's table must hold both review commits, got %d row(s):\n%s", n, body)
				}
				if want := haikuSince + " 1 of them" + offLine + pushingAgain; !strings.HasSuffix(body, want) {
					t.Errorf("the footer must say how many of the commits it counts sit on no line — they are in no table above it; want the body to end\n  %q\ngot:\n%s", want, body)
				}
			},
		},
		{
			name:    "one line touched by a bot alone: nothing participates, and nothing is off a line",
			listing: []string{bot},
			files:   map[string]string{"d1": apiFiles("haiku/go.mod")},
			count:   0,
			check: func(t *testing.T, body string) {
				if want := haikuSince + pushingAgain; !strings.HasSuffix(body, want) || strings.Contains(body, offLine) {
					t.Errorf("no participating commit is off a line, so the footer names none; want the body to end\n  %q\ngot:\n%s", want, body)
				}
			},
		},
		{
			name:    "a commit whose refusal was withheld is on no line",
			listing: []string{season, apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient")},
			files:   map[string]string{"h1": apiFiles("haiku/season.go"), "c1": apiUnknownSHA},
			count:   2,
			check: func(t *testing.T, body string) {
				if want := haikuSince + " 1 of them" + offLine + pushingAgain; !strings.HasSuffix(body, want) {
					t.Errorf("want the body to end\n  %q\ngot:\n%s", want, body)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes := map[string]string{pullCommitsPath(9): `[` + strings.Join(tc.listing, ",") + `]`}
			for sha, body := range tc.files {
				routes[commitFilesPath(sha)] = body
			}
			srv := walkServer(t, routes)

			single, _ := testRepo(t)
			t.Chdir(single)
			usePR(t, srv)
			code, stdout, stderr := runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("the single line's preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			if n := participatingCount(t, stdout); n != tc.count {
				t.Fatalf("positive control: the single line counts %d participating commit(s) in this listing, and the case expects %d:\n%s", n, tc.count, stdout)
			}

			dir, _ := packagesRepo(t)
			t.Chdir(dir)
			code, stdout, stderr = runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			if n := participatingCount(t, stdout); n != tc.count {
				t.Errorf("the packages preview counts %d participating commit(s) where the single line counts %d for the same pull:\n%s", n, tc.count, stdout)
			}
			tc.check(t, stdout)
		})
	}
}

// TestPreviewPackagesWarnsEveryParticipatingCommitOnce: a pattern's warning
// follows its commit into every command that folds it (warnSigilVerdicts: loud
// in one and silent in another is how a warning dies). The packages preview
// warned per touched line, so a warned commit on NO line was silent here while
// the single line's preview said it — measured on 4a8183b: no warning against
// one — and a commit on two lines was said twice. The whole listing's rows are
// the pull's participating commits, and each is warned once.
func TestPreviewPackagesWarnsEveryParticipatingCommitOnce(t *testing.T) {
	const warning = "::warning::glyph: commit w1: a construction commit is legal here and unwelcome"
	const warned = "[[patterns]]\npattern = '^(?P<subject>:construction:(?P<semver_sigil>[=~^!%]) .+)'\nwarn = 'a construction commit is legal here and unwelcome'\n\n"
	// First match wins, so the warn pattern goes ahead of the preset's.
	warnFirst := func(t *testing.T, dir string) {
		t.Helper()
		path := filepath.Join(dir, "glyph.toml")
		b, err := os.ReadFile(path) // #nosec G304 -- a path under the test's own temp dir
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "[[patterns]]\n") {
			t.Fatalf("the fixture's glyph.toml declares no pattern to go ahead of")
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(b), "[[patterns]]\n", warned+"[[patterns]]\n", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, files := range map[string][]string{
		"on no line":   {"README.md"},
		"on one line":  {"haiku/haiku.go"},
		"on two lines": {"haiku/haiku.go", "curry/curry.go"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := walkServer(t, map[string]string{
				pullCommitsPath(9):    `[` + apiCommit("w1", "akira-toriyama", ":construction:= scaffold the work") + `]`,
				commitFilesPath("w1"): apiFiles(files...),
			})

			single, _ := testRepo(t)
			warnFirst(t, single)
			t.Chdir(single)
			usePR(t, srv)
			code, _, stderr := runGlyph(t, "preview", "--pr", "9")
			if code != 0 || strings.Count(stderr, warning) != 1 {
				t.Fatalf("positive control: the single line's preview exits 0 and says the warning once, got exit %d:\n%s", code, stderr)
			}

			dir, _ := packagesRepo(t)
			warnFirst(t, dir)
			t.Chdir(dir)
			code, _, stderr = runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
			}
			if n := strings.Count(stderr, warning); n != 1 {
				t.Errorf("the packages preview says the warning %d time(s) for a commit the single line warns once:\n%s", n, stderr)
			}
		})
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
	if code != 3 || !strings.Contains(stderr, "its files (go.work) belong to no declared package") || !strings.Contains(stderr, "fix it on the branch") {
		t.Fatalf("preview exited %d, want 3 with the attribution refusal\nstderr: %s", code, stderr)
	}
}

// TestPreviewPackagesRefusesAClaimedMergeInItsOwnWords is preview's arm of the
// refusal's wording (cli's attribute): a pull that lists a merge commit some
// non-skip pattern claims — its author merged the base in under a subject of
// their own — is refused as the walk will refuse it, in the merge commit's
// sentence: its diff is never asked of the API (no commits/{sha} route is
// served, and the stand-in fails the test on an unexpected path), so "touches
// no declared package" was a claim nothing had read. Mutation row
// preview-refuses-a-merge-commit-as-touching-no-file hands the helper no commit.
func TestPreviewPackagesRefusesAClaimedMergeInItsOwnWords(t *testing.T) {
	dir, _ := packagesRepo(t)
	usePR(t, walkServer(t, map[string]string{
		pullCommitsPath(9): `[` + apiMergeCommit("m1", "akira-toriyama", ":twisted_rightwards_arrows:~ merge main into the branch") + `]`,
	}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "preview", "--pr", "9")
	want := "commit m1 in pull request akira-toriyama/glyph#9: this merge commit's own diff is never read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry), or write = so it moves no line — the release walk will refuse this commit the same way"
	if code != 3 || !strings.Contains(stderr, want) {
		t.Fatalf("preview exited %d, want 3 with\n  %s\nstderr: %s", code, want, stderr)
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
	if strings.Contains(res.Body, "curry has no release tag on the base branch yet, so nothing merged earlier is folded in for it") {
		t.Errorf("the body denies a pending side this same run walked and reported:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "curry has no release tag on the base branch yet, so everything merged so far is folded in for it") {
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

// TestPreviewPackagesUnlistedFilesAreCaveated: a 422 for one of the pull's
// own commits' file listing is the walk's unread listing on the PR side
// (DESIGN §4.1, t-esm5): preview exits 0 and the body carries the PR-side
// INCOMPLETE caveat naming the commit, in the cause-neutral sentence — the
// capped one said "only past the cap" for a listing GitHub never gave. A
// refusal over the empty listing is withheld, so the unscoped ~ is attributed
// to no line and curry goes unmentioned; a scope naming curry still carries
// it there (rule 2). Every sentence is true in both: the warning says a line
// MAY be missing, as the walk's does, because the scoped commit's curry is
// in the preview. When c1 is the pull's only commit, the "moves nothing"
// body claims nothing about files GitHub did not list and points at no
// figures it does not carry. When the pending walk meets the same 422 on a
// merged pull, the pending caveat names it. Before the fix every case died
// at 4 on the raw `github: GET …/commits/c1: 422`, taking the whole comment
// down (mutation row unlisted-commit-files-die-as-a-raw-api-error).
func TestPreviewPackagesUnlistedFilesAreCaveated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		curry   bool
	}{
		{"unscoped, attributed to no line", ":bug:~ swap an ingredient", false},
		{"scoped, carried by its scope", ":bug:(curry)~ swap an ingredient", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			routes := crossLinePull(9)
			routes[pullCommitsPath(9)] = `[` +
				apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
				apiCommit("c1", "akira-toriyama", tc.message) + `]`
			routes[commitFilesPath("c1")] = apiUnknownSHA
			usePR(t, walkServer(t, routes))
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0 — an unread listing is caveated, not a failure\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			res := decodePreviewLines(t, stdout)
			var lines []string
			for _, p := range res.Packages {
				lines = append(lines, p.Path+":"+p.PR)
			}
			want := []string{"haiku:minor"}
			if tc.curry {
				want = append(want, "curry:patch")
			}
			if strings.Join(lines, " ") != strings.Join(want, " ") {
				t.Fatalf("packages = %v, want %v", lines, want)
			}
			caveat := "> This PR's own side of this fold is INCOMPLETE: GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1). A line one of those commits touches in files GitHub did not list may be missing from the figures above, so treat each as a floor rather than the answer."
			if !strings.Contains(res.Body, caveat) {
				t.Fatalf("the body must carry the PR-side caveat naming c1:\n%s", res.Body)
			}
			if strings.Contains(res.Body, "past the cap") {
				t.Errorf("the caveat blames the cap for a listing GitHub never gave:\n%s", res.Body)
			}
			if strings.Contains(res.Body, "**curry**") != tc.curry {
				t.Errorf("curry is mentioned exactly when c1's scope carried it there (%t):\n%s", tc.curry, res.Body)
			}
			if !strings.Contains(stderr, "::warning::glyph: commit c1 in pull request #9: GitHub answered 422 for its file listing, so a line it touches in files GitHub did not list may be missing from this preview") {
				t.Errorf("preview must warn about the unlisted files, saying only what it knows:\n%s", stderr)
			}
		})
	}

	t.Run("the only commit, moves nothing it could read", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `]`,
			commitFilesPath("c1"): apiUnknownSHA,
		}))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		res := decodePreviewLines(t, stdout)
		headline := "<!-- glyph-pr-verdict -->\n⏸️ Merging this PR moves nothing in the files GitHub listed — no commit participating in it touches a declared package there.\n"
		caveat := "(c1). A line one of those commits touches in files GitHub did not list may move all the same, so treat \"moves nothing\" as a floor rather than the answer."
		if !strings.HasPrefix(res.Body, headline) || !strings.Contains(res.Body, caveat) {
			t.Fatalf("the body must say it moves nothing only in the files GitHub listed, and caveat that naming c1:\n%s", res.Body)
		}
		if strings.Contains(res.Body, "figures above") {
			t.Errorf("the caveat points at figures the moves-nothing body does not carry:\n%s", res.Body)
		}
	})

	t.Run("the pending walk meets it", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		_, routes := squashAcrossLines(t, dir, 7)
		routes[commitFilesPath("c1")] = apiUnknownSHA
		routes[pullCommitsPath(9)] = `[` + apiCommit("p1", "akira-toriyama", ":sparkles:(haiku)^ add a verse") + `]`
		routes[commitFilesPath("p1")] = apiFiles("haiku/verse.go")
		usePR(t, walkServer(t, routes))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		res := decodePreviewLines(t, stdout)
		pending := "> The pending side of this fold is INCOMPLETE: GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1 in pull request #7"
		if !strings.Contains(res.Body, pending) {
			t.Fatalf("the pending caveat must name the merged pull's unlisted commit:\n%s", res.Body)
		}
		if strings.Contains(res.Body, "This PR's own side of this fold is INCOMPLETE") {
			t.Errorf("the pull's own listing was whole, so its side carries no caveat:\n%s", res.Body)
		}
	})
}
