package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins the packages walk (DESIGN §4.1): a repository declaring two
// lines, haiku/ and curry/, each with its own tag, and the property the
// live-fire harness glyph-monorepo-test exists to prove — a pull that
// touches both modules with a ^ in one and a ~ in the other moves the two
// lines differently, and a squash merge never loses which of the pull's
// commits touched which module.

// packagesRepo builds the two-line fixture: testutil.NewTwoLineRepo with
// haiku/v0.1.0 + curry/v0.1.0 on the commit that declared the lines. base is
// that commit.
func packagesRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = testutil.NewTwoLineRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0")
	return dir, testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

func appendTo(t *testing.T, dir, rel, content string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, rel), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// commitFilesPath names the endpoint the walk asks for a squash-merged
// pull's inner commit's files.
func commitFilesPath(sha string) string {
	return "/repos/akira-toriyama/glyph/commits/" + sha
}

// apiFiles renders GET commits/{sha}'s files array for the given paths.
func apiFiles(paths ...string) string {
	entries := make([]string, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, fmt.Sprintf(`{"filename":%q,"status":"modified"}`, p))
	}
	return `{"files":[` + strings.Join(entries, ",") + `]}`
}

// packagesVerdict decodes bump --json in packages mode.
type packagesVerdict struct {
	Current string `json:"current"`
	Level   string `json:"level"`
	Next    string `json:"next"`
	Commits []struct {
		SHA string `json:"sha"`
	} `json:"commits"`
	Packages []struct {
		Path    string `json:"path"`
		Current string `json:"current"`
		Level   string `json:"level"`
		Next    string `json:"next"`
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
		Reason string `json:"reason"`
	} `json:"packages"`
	Reason string `json:"reason"`
}

func decodePackagesVerdict(t *testing.T, stdout string) packagesVerdict {
	t.Helper()
	var res packagesVerdict
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("bump --json stdout is not JSON: %v\n%s", err, stdout)
	}
	return res
}

// squashAcrossLines lands the defining probe on main: one squash commit
// whose pull carried a ^ under haiku/ and a ~ under curry/. The squash's own
// net diff touches both (as a real squash would); its inner commits exist
// only in the API, where each one's files say which line it moves.
func squashAcrossLines(t *testing.T, dir string, number int) (sha string, routes map[string]string) {
	t.Helper()
	sha = testutil.Touch(t, dir, "akira-toriyama", fmt.Sprintf("Add a season and swap an ingredient (#%d)", number), "haiku/season.go", "curry/curry.go")
	routes = map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(number, "2026-09-10T00:00:00Z", sha) + `]`,
		pullCommitsPath(number): `[` +
			apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `]`,
		commitFilesPath("h1"): apiFiles("haiku/season.go"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
	}
	return sha, routes
}

// TestBumpSinceTagPackagesMoveIndependently is the defining probe: the pull's
// ^ moves haiku to v0.2.0 and its ~ moves curry to v0.1.1, from ONE walk,
// with the attribution read per inner commit over the API (the squash's net
// diff touches both lines and could not have told the sigils apart). The
// scalars are empty — no one line for them to describe — and stdout is the
// next tag of each moving line, config order, prefixed as a tag step needs.
func TestBumpSinceTagPackagesMoveIndependently(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if res.Current != "" || res.Level != "" || res.Next != "" {
		t.Fatalf("scalars must be empty when packages are declared, got current=%q level=%q next=%q", res.Current, res.Level, res.Next)
	}
	if len(res.Packages) != 2 {
		t.Fatalf("packages carries %d verdicts, want 2: %s", len(res.Packages), stdout)
	}
	h, c := res.Packages[0], res.Packages[1]
	if h.Path != "haiku" || h.Current != "v0.1.0" || h.Level != "minor" || h.Next != "v0.2.0" || len(h.Commits) != 1 {
		t.Fatalf("haiku = %+v, want v0.1.0 → minor → v0.2.0 over 1 commit", h)
	}
	if c.Path != "curry" || c.Current != "v0.1.0" || c.Level != "patch" || c.Next != "v0.1.1" || len(c.Commits) != 1 {
		t.Fatalf("curry = %+v, want v0.1.0 → patch → v0.1.1 over 1 commit", c)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("commits (the union) carries %d rows, want 2: %s", len(res.Commits), stdout)
	}

	code, stdout, stderr = runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "haiku/v0.2.0\ncurry/v0.1.1\n" {
		t.Fatalf("stdout = %q, want one next TAG per moving line", stdout)
	}
}

// TestSinceTagPackagesLandedCommitReadsFilesFromGit: a direct push is a
// landed identity, so its files come from local git and the API is never
// asked for them — the walkServer fails the test on an unexpected path, so
// the absence of a commits/{sha} route IS the assertion. Only the line it
// touched moves.
func TestSinceTagPackagesLandedCommitReadsFilesFromGit(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := testutil.Touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "curry/v0.1.1\n" {
		t.Fatalf("stdout = %q, want curry alone to move", stdout)
	}
}

// TestSinceTagPackagesSharedOnlyBumpIsRefused: a ^ under no package with no
// scope has nothing to carry it — the lint-class refusal (exit 3), naming
// the escapes it can take and, on the walk, the wedge per line whose range
// holds it.
func TestSinceTagPackagesSharedOnlyBumpIsRefused(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("a shared-only ^ exited %d, want 3\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"its files (go.work) belong to no declared package",
		"name the line it moves in the scope (one of haiku, curry), write = so it moves no line, or declare the package these files belong to",
		"haiku/ tag at or past", "curry/ tag at or past", sha[:7],
		"--since-tag=<prefix>vX.Y.Z",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("refusal is missing %q:\n%s", want, stderr)
		}
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if env.Code != 3 {
		t.Fatalf("envelope code = %d, want 3", env.Code)
	}
}

// TestSinceTagPackagesSharedOnlyNoneParticipatesInNoLine: a = under no package
// is shared housekeeping — it participates, in no line: in no line's verdict,
// and among the commits the walk read. Every line folds to none, so exit 1.
func TestSinceTagPackagesSharedOnlyNoneParticipatesInNoLine(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := testutil.Touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 1 {
		t.Fatalf("shared-only = exited %d, want 1\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	for _, p := range res.Packages {
		if p.Level != "none" || len(p.Commits) != 0 {
			t.Fatalf("%s = %+v, want none over 0 commits", p.Path, p)
		}
	}
	if len(res.Commits) != 1 {
		t.Fatalf("commits (the union) carries %d rows, want the one shared commit", len(res.Commits))
	}
	if !strings.Contains(res.Reason, "every line folds to none") {
		t.Fatalf("reason = %q, want it to say every line folds to none", res.Reason)
	}
}

// TestSinceTagPackagesScopeCarriesAndContradicts: rule 2 — a shared-only
// commit whose scope names a package moves that package — and the
// contradiction check: with a version sigil, a scope naming a package that
// owns none of the diff's files is refused, the finding naming a file with
// the package that owns it.
func TestSinceTagPackagesScopeCarriesAndContradicts(t *testing.T) {
	t.Run("scope carries a shared-only commit", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		sha := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(haiku)^ describe the season API", "README.md")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "haiku/v0.2.0\n" {
			t.Fatalf("scope-carried ^ exited %d with %q, want 0 and haiku/v0.2.0\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("scope contradicting the tree is refused", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		sha := testutil.Touch(t, dir, "akira-toriyama", ":bug:(haiku)~ swap an ingredient", "curry/curry.go")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 3 {
			t.Fatalf("a contradicting scope exited %d, want 3\nstderr: %s", code, stderr)
		}
		if want := "scope (haiku) names a line this commit does not move: curry/curry.go belongs to curry (curry) — the longest declared path owns a file: write the scope of a line it moves (curry), or drop the scope"; !strings.Contains(stderr, want) {
			t.Fatalf("refusal must name the contradiction (%q):\n%s", want, stderr)
		}
	})
}

// lineSection returns the notes rendered under "# <path>" — from that
// heading to the next line heading — and "" when the line has no section.
func lineSection(stdout, path string) string {
	_, after, ok := strings.Cut(stdout, "# "+path+"\n")
	if !ok {
		return ""
	}
	section, _, _ := strings.Cut(after, "\n# ")
	return section
}

// TestSinceTagPackagesExcludedAuthorIsPlacedByItsFiles: a commit the fold
// does not read is placed by its own diff, never by its message and never
// on every line (DESIGN §4.1; mutation row
// packages-excluded-author-placed-on-every-line). An exclude_authors commit
// moves no version — the fold still drops it — and appears in the notes of
// the lines its files touch: a haiku-only bump under haiku alone, the
// fleet-shaped `Bump X from A to B` (a message no pattern claims) under the
// line whose go.mod it touched, and a bump of root CI under no line at all,
// the shape rule 3 gives a shared-only `=`. The landed bot commits have no
// pulls route: the walk's author gate never resolves them, so the placement
// costs local git alone.
//
// Measured before the fix (t-sr1c, 2026-09-11 on glyph-monorepo-test): the
// haiku-only bump rendered under all five line headings, and lines with no
// commit of their own grew a section for it.
func TestSinceTagPackagesExcludedAuthorIsPlacedByItsFiles(t *testing.T) {
	dir, _ := packagesRepo(t)
	testutil.Touch(t, dir, "dependabot[bot]", ":arrow_up:(haiku)~ bump a haiku-only dependency", "haiku/poem.go")
	testutil.Touch(t, dir, "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0", "curry/go.mod")
	testutil.Touch(t, dir, "dependabot[bot]", "Bump actions/checkout from 4 to 5", ".github/workflows/ci.yml")
	fix := testutil.Touch(t, dir, "akira-toriyama", ":bug:(curry)~ a curry fix", "curry/rice.go")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(fix): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	haiku, curry := lineSection(stdout, "haiku"), lineSection(stdout, "curry")
	if !strings.Contains(haiku, "bump a haiku-only dependency") || strings.Contains(curry, "bump a haiku-only dependency") {
		t.Fatalf("a haiku-only bump is placed on haiku alone:\n%s", stdout)
	}
	if !strings.Contains(curry, "Bump golang.org/x/net") || strings.Contains(haiku, "Bump golang.org/x/net") {
		t.Fatalf("an unmatched bot subject is placed by its files, on curry alone:\n%s", stdout)
	}
	if strings.Contains(stdout, "Bump actions/checkout") {
		t.Fatalf("a bot commit under no package is placed nowhere:\n%s", stdout)
	}
	if !strings.Contains(haiku, "## Dependencies") {
		t.Fatalf("the bump renders through the author section note.sections gives it:\n%s", stdout)
	}

	code, stdout, stderr = runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Level != "none" || len(h.Commits) != 0 {
		t.Fatalf("haiku = %+v, want none over 0 commits — placement moves no version", h)
	}
	if c.Level != "patch" || len(c.Commits) != 1 {
		t.Fatalf("curry = %+v, want patch over the one fix — the bot commit is placed on it, never folded", c)
	}
}

// TestSinceTagPackagesExcludedAuthorInnerCommitIsPlacedByItsFiles is the
// squash arm of the same rule: a human's pull carrying a dependabot commit is
// expanded (the pull's author is not excluded), and the bot's inner commit
// exists on no branch, so its files come from the API like any other inner
// commit's — the one request DESIGN §4.1's price names, recorded here as the
// positive control (the first cut never asked, and placed the commit on every
// line). Measured in this fixture: a pull of one human and one dependabot
// commit costs 4 requests, 2 + k over k = 2, where the first cut paid 3.
func TestSinceTagPackagesExcludedAuthorInnerCommitIsPlacedByItsFiles(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := testutil.Touch(t, dir, "akira-toriyama", "Bump a haiku dependency and fix curry (#8)", "haiku/go.mod", "curry/curry.go")
	srv, seen := recordingWalkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(8, "2026-09-26T00:00:00Z", sha) + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("d1", "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `]`,
		commitFilesPath("d1"): apiFiles("haiku/go.mod"),
		commitFilesPath("c1"): apiFiles("curry/curry.go"),
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if haiku, curry := lineSection(stdout, "haiku"), lineSection(stdout, "curry"); !strings.Contains(haiku, "Bump golang.org/x/net") || strings.Contains(curry, "Bump golang.org/x/net") {
		t.Fatalf("the bot's inner commit is placed by its files, on haiku alone:\n%s", stdout)
	}
	if !slices.Contains(*seen, commitFilesPath("d1")) {
		t.Fatalf("the bot's inner commit's files must be asked for — that is the placement; requests: %v", *seen)
	}
	if len(*seen) != 4 {
		t.Fatalf("requests = %d %v, want 4 = 2 + k over k = 2 inner commits", len(*seen), *seen)
	}
}

// TestSinceTagPackagesReleasedOnOneLineStaysReleased pins "the walk is one
// walk" (DESIGN §4.1): with haiku already released past a commit that curry
// has not released past, the union walk visits that commit — and it moves
// nothing on haiku, because haiku's OWN range does not hold it. Ignoring the
// per-line range would re-release haiku's shipped work as v0.3.0 (mutation row
// packages-line-range-not-consulted).
func TestSinceTagPackagesReleasedOnOneLineStaysReleased(t *testing.T) {
	dir, _ := packagesRepo(t)
	shipped := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	pending := testutil.Touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(shipped): `[]`,
		commitPullsPath(pending): `[]`,
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Current != "v0.2.0" || h.Level != "none" || len(h.Commits) != 0 {
		t.Fatalf("haiku = %+v, want v0.2.0 with nothing pending (its ^ shipped under haiku/v0.2.0)", h)
	}
	if c.Current != "v0.1.0" || c.Next != "v0.1.1" || len(c.Commits) != 1 {
		t.Fatalf("curry = %+v, want v0.1.0 → v0.1.1 over 1 commit", c)
	}
}

// TestSinceTagPackagesSquashInnerCommitIsGovernedByItsMergePoint: a
// squash-merged pull's inner commits exist on no branch, so the line ranges
// judge them by the pull's merge point. A pull merged before haiku's latest
// tag is released on haiku even though its inner haiku commit is in the
// union walk — and still pending on curry.
func TestSinceTagPackagesSquashInnerCommitIsGovernedByItsMergePoint(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha, routes := squashAcrossLines(t, dir, 7)
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	later := testutil.Touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	routes[commitPullsPath(later)] = `[]`
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Level != "none" || len(h.Commits) != 0 {
		t.Fatalf("haiku = %+v, want nothing pending (pull #7's merge point %.7s is under haiku/v0.2.0)", h, sha)
	}
	if c.Level != "patch" || len(c.Commits) != 1 {
		t.Fatalf("curry = %+v, want the pull's ~ still pending", c)
	}
}

// TestSinceTagPackagesNoFilesAskedWithoutPackages is the additive proof from
// the API's side: a repository with no [[packages]] never asks for a
// commit's files — the walkServer would fail the test on the request.
// (Every pre-existing walk test proves the same by construction; this one
// says so by name, beside the packages tests.)
func TestSinceTagPackagesNoFilesAskedWithoutPackages(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	sha := squashCommit(t, dir, "Add a season and swap an ingredient", 7)
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-09-10T00:00:00Z", sha) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
			apiCommit("c1", "akira-toriyama", ":bug:~ swap an ingredient") + `]`,
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if res.Packages != nil || res.Next != "v0.2.0" {
		t.Fatalf("the single line must answer as before, with no packages key: %s", stdout)
	}
}

// TestNotesSinceTagPackages: notes render per line — JSON carries one
// sections array per package and an EMPTY top-level sections (no one line
// for it to describe); plain stdout puts each line's body under a `# <path>`
// heading when several lines answer, and renders the one line's body bare
// when a tag selects it (goreleaser's tag-time rendering).
func TestNotesSinceTagPackages(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("notes --since-tag --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	var res struct {
		Sections []json.RawMessage `json:"sections"`
		Packages []struct {
			Path     string `json:"path"`
			Sections []struct {
				Title string   `json:"title"`
				Lines []string `json:"lines"`
			} `json:"sections"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("notes --json stdout is not JSON: %v\n%s", err, stdout)
	}
	if res.Sections == nil || len(res.Sections) != 0 {
		t.Fatalf("top-level sections must be [] in packages mode: %s", stdout)
	}
	if len(res.Packages) != 2 || res.Packages[0].Path != "haiku" || res.Packages[1].Path != "curry" {
		t.Fatalf("packages = %+v, want haiku then curry", res.Packages)
	}
	if h := res.Packages[0].Sections; len(h) != 1 || h[0].Title != "Features" || len(h[0].Lines) != 1 || !strings.Contains(h[0].Lines[0], "add a season") {
		t.Fatalf("haiku sections = %+v, want Features with the season", h)
	}
	if c := res.Packages[1].Sections; len(c) != 1 || c[0].Title != "Fixes" || !strings.Contains(c[0].Lines[0], "swap an ingredient") {
		t.Fatalf("curry sections = %+v, want Fixes with the ingredient", c)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "# haiku\n\n## Features\n") || !strings.Contains(stdout, "\n# curry\n\n## Fixes\n") {
		t.Fatalf("several lines render under # <path> headings, sections nested below:\n%s", stdout)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag=below:curry/v0.2.0")
	if code != 0 {
		t.Fatalf("notes --since-tag=below:curry/v0.2.0 exited %d, want 0\nstderr: %s", code, stderr)
	}
	if strings.Contains(stdout, "# curry") || strings.Contains(stdout, "add a season") {
		t.Fatalf("one selected line renders bare, and only its own commits:\n%s", stdout)
	}
	if !strings.Contains(stdout, "swap an ingredient") {
		t.Fatalf("the selected line's body is missing its commit:\n%s", stdout)
	}
}

// TestPackagesRangeAndPullSources: --range attributes from local git — every
// commit unreleased on every line, each line stepping from its own highest
// tag — and --pr is refused: a pull's listing carries messages and no files.
func TestPackagesRangeAndPullSources(t *testing.T) {
	dir, base := packagesRepo(t)
	testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	testutil.Touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 0 || stdout != "haiku/v0.2.0\ncurry/v0.1.1\n" {
		t.Fatalf("bump --range exited %d with %q, want 0 and both lines\nstderr: %s", code, stdout, stderr)
	}
	code, stdout, stderr = runGlyph(t, "notes", "--range", base+"..HEAD", "--json")
	if code != 0 || !strings.Contains(stdout, `"path":"haiku"`) {
		t.Fatalf("notes --range --json exited %d with %q\nstderr: %s", code, stdout, stderr)
	}
	for _, cmd := range []string{"bump", "notes"} {
		t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/glyph")
		code, _, stderr := runGlyph(t, cmd, "--pr", "7")
		if code != 2 || !strings.Contains(stderr, "cannot be attributed to a line") {
			t.Fatalf("%s --pr exited %d, want 2 with the attribution refusal\nstderr: %s", cmd, code, stderr)
		}
	}
}

// TestLintRangePackagesJudgesTheDiff: with packages declared, lint --range
// applies rules 2–3 and the contradiction check to each clean commit's own
// diff — a shared-only ^ and a version-claiming scope contradicting the tree
// are findings; a shared-only =, a scope-carried ^, a commit under a package
// and a = whose scope names another package are clean. That last one claims
// no version impact, so the tree has nothing to contradict (DESIGN §4.1,
// t-n5tw R2): it was a third finding, and once merged a wedge, over a commit
// that moves no line.
func TestLintRangePackagesJudgesTheDiff(t *testing.T) {
	dir, base := packagesRepo(t)
	shared := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
	contra := testutil.Touch(t, dir, "akira-toriyama", ":bug:(haiku)~ swap an ingredient", "curry/curry.go")
	testutil.Touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	testutil.Touch(t, dir, "akira-toriyama", ":sparkles:(curry)^ describe the ingredient API", "README.md")
	testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	retag := testutil.Touch(t, dir, "akira-toriyama", ":wrench:(haiku)= retag the curry notes", "curry/curry.go")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "2 commit-convention violation(s)") {
		t.Fatalf("want exactly the two attribution findings:\n%s", stderr)
	}
	for _, want := range []string{
		shared[:7], "its files (go.work) belong to no declared package",
		"commit " + contra[:7] + ": scope (haiku) names a line this commit does not move: curry/curry.go belongs to curry (curry) — the longest declared path owns a file: write the scope of a line it moves (curry), or drop the scope",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("findings are missing %q:\n%s", want, stderr)
		}
	}

	// The = is placed by its files: on curry's line, moving nothing — the
	// shape glyph-monorepo-test's e2e fires as live ammunition.
	code, stdout, stderr := runGlyph(t, "bump", "--range", retag+"~1.."+retag, "--json")
	if code != 1 {
		t.Fatalf("bump over the (haiku)= on a curry file exited %d, want 1 (every line none)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if h, c := res.Packages[0], res.Packages[1]; len(h.Commits) != 0 || len(c.Commits) != 1 || c.Level != "none" {
		t.Fatalf("haiku = %+v, curry = %+v; want the = on curry's line alone, at none", h, c)
	}
}

// TestLintRangePackagesScopeIsCheckedByOwnership: the contradiction check asks
// which package OWNS a file — the longest declared path — never which contains
// it. With travel and travel/onsen declared, (travel)~ on a file under
// travel/onsen names a line the commit does not move, and is refused naming
// the file with its owner and the scope that is true; (onsen)~ and no scope
// move travel/onsen alone and leave travel at none. Accepted by containment,
// the message would claim travel's line while only its nested package's moved
// (DESIGN §4.1, t-mfny (C); measured 2026-10-05 at 135eead: exit 3 with
// "does not touch (its files lie under travel/onsen)").
func TestLintRangePackagesScopeIsCheckedByOwnership(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"travel\"\n\n[[packages]]\npath = \"travel/onsen\"\n",
		map[string]string{"travel/t.md": "travel\n", "travel/onsen/o.md": "onsen\n"})
	testutil.Touch(t, dir, "akira-toriyama", ":bug:(travel)~ warm the water", "travel/onsen/o.md")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
	want := "scope (travel) names a line this commit does not move: travel/onsen/o.md belongs to onsen (travel/onsen) — the longest declared path owns a file: write the scope of a line it moves (onsen), or drop the scope"
	if code != 3 || !strings.Contains(stderr, want) {
		t.Fatalf("(travel)~ on travel/onsen's file exited %d, want 3 with\n  %s\nstderr: %s", code, want, stderr)
	}
	for _, message := range []string{":bug:(onsen)~ warm the water", ":bug:~ warm the water"} {
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", message)
		if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 0 {
			t.Errorf("%q exited %d, want 0 — the escape the finding names\nstderr: %s", message, code, stderr)
		}
		if code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD"); code != 0 || stdout != "travel/onsen/v0.0.1\n" {
			t.Errorf("%q bumped %q at exit %d, want travel/onsen alone\nstderr: %s", message, stdout, code, stderr)
		}
	}
}

// TestLintRangePackagesContradictionOffersNoDropWhereTheScopeIsRequired: the
// remedy is asked of the pattern that claimed the message. Under a grammar
// whose scope is not optional "or drop the scope" is no escape — the message
// without one matches no pattern — so the finding names the true scope alone,
// and the test takes it, then shows the dropped scope failing.
func TestLintRangePackagesContradictionOffersNoDropWhereTheScopeIsRequired(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, "glyph.toml", "schema = 1\n\n[[patterns]]\npattern = '^(?P<subject>[a-z]+\\((?P<scope>[a-z]+)\\)(?P<semver_sigil>[=~^!%]): .+)'\n\n[note]\nline = '- $subject'\n"+testutil.PackagesSnippet)
	testutil.WriteFile(t, dir, "haiku/haiku.go", "package haiku\n")
	testutil.WriteFile(t, dir, "curry/curry.go", "package curry\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", "chore(repo)=: declare the lines")
	testutil.Touch(t, dir, "akira-toriyama", "fix(haiku)~: swap an ingredient", "curry/curry.go")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
	want := "curry/curry.go belongs to curry (curry) — the longest declared path owns a file: write the scope of a line it moves (curry)\n"
	if code != 3 || !strings.Contains(stderr, want) || strings.Contains(stderr, "drop the scope") {
		t.Fatalf("exited %d; the finding must end %q and offer no dropped scope:\n%s", code, want, stderr)
	}
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", "fix(curry)~: swap an ingredient")
	if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 0 {
		t.Fatalf("the scope the finding names exited %d, want 0\nstderr: %s", code, stderr)
	}
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", "fix~: swap an ingredient")
	if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 3 || !strings.Contains(stderr, "matches none of the 1 configured patterns") {
		t.Fatalf("the dropped scope exited %d, want 3 as a message no pattern claims — the escape the finding must not name\nstderr: %s", code, stderr)
	}
}

// rootedConfig declares haiku beside a named root package — the shape under
// which every FILE has an owner, so the only commits rules 2–3 still meet are
// the ones that show the tree no file.
const rootedConfig = "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \".\"\nname = \"core\"\n"

// claimedMerge lands a merge commit a non-skip pattern claims: a side branch
// touching haiku/ alone, merged --no-ff under message. The presets skip only a
// subject that opens `Merge `, so this one is read — its scope and sigil, never
// its diff.
func claimedMerge(t *testing.T, dir, message string) string {
	t.Helper()
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	testutil.Touch(t, dir, "akira-toriyama", ":memo:= reword a line", "haiku/haiku.go")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", message, "side")
	return testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestLintRangePackagesNoFileCommitUnderARootPackage: a commit that shows the
// tree no file — an empty commit, a claimed merge commit — has no carrier but
// its scope, a root package declared or not (DESIGN §4.1, t-n5tw 3): the root
// package is a claim on files. Each finding says which of the two it is and
// names the root's scope among the escapes; the same empty commit scoped
// (core) is clean, which is the escape taken. This is lint's arm of the
// refusal's wording (walk.Attribute): the merge sentence is read here, and
// mutation row lint-refuses-a-merge-commit-as-touching-no-file hands it no commit.
func TestLintRangePackagesNoFileCommitUnderARootPackage(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, rootedConfig, map[string]string{"haiku/haiku.go": "package haiku\n"})
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	empty := testutil.Touch(t, dir, "akira-toriyama", ":bookmark:~ cut a release")
	merge := claimedMerge(t, dir, ":twisted_rightwards_arrows:~ merge the side branch")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "2 commit-convention violation(s)") {
		t.Fatalf("want exactly the two no-file findings:\n%s", stderr)
	}
	const escapes = ": name the line it moves in the scope (one of haiku, core), or write = so it moves no line"
	for _, want := range []string{
		"commit " + empty[:7] + ": this commit touches no file, so no package's tree can carry its sigil ~" + escapes,
		"commit " + merge[:7] + ": this merge commit's own diff is never read, so no package's tree can carry its sigil ~" + escapes,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("findings are missing %q:\n%s", want, stderr)
		}
	}

	scoped := testutil.Touch(t, dir, "akira-toriyama", ":bookmark:(core)~ cut a release")
	if code, stdout, stderr := runGlyph(t, "lint", "--range", scoped+"~1.."+scoped); code != 0 {
		t.Fatalf("an empty (core)~ exited %d, want 0 — the scope is the escape the finding names\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if code, stdout, stderr := runGlyph(t, "bump", "--range", scoped+"~1.."+scoped); code != 0 || stdout != "v0.0.1\n" {
		t.Fatalf("bump over the empty (core)~ exited %d with %q, want 0 and the root line alone at v0.0.1\nstderr: %s", code, stdout, stderr)
	}
}

// TestPackagesWalkRefusesAClaimedMergeInItsOwnWords is the walk's arm of the
// refusal's wording: partitionLines reaches attribution through
// walk.Attribute, so a claimed merge commit is refused as a merge commit — its diff
// never read — with the wedge per line, where the first cut said it "touches
// no declared package" of a merge that touched haiku/. A haiku scope carries
// it (rule 2). Mutation row walk-refuses-a-merge-commit-as-touching-no-file
// hands the helper no commit. The walk is --since-tag, which words the
// refusal with its wedge per line; --range words the same sentence for a
// range that may still be rewritten (TestBumpRangePackagesAttributionRefusalIsTheAuthoringOne).
func TestPackagesWalkRefusesAClaimedMergeInItsOwnWords(t *testing.T) {
	dir, _ := packagesRepo(t)
	merge := claimedMerge(t, dir, ":twisted_rightwards_arrows:~ merge the side branch")
	routes := map[string]string{commitPullsPath(merge): `[]`, commitPullsPath(testutil.Git(t, dir, "akira-toriyama", "rev-parse", "side")): `[]`}
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("bump --since-tag over a claimed ~ merge exited %d, want 3\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"commit " + merge[:7] + ": this merge commit's own diff is never read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry), or write = so it moves no line",
		"a haiku/ tag at or past " + merge[:7],
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, stderr)
		}
	}

	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":twisted_rightwards_arrows:(haiku)~ merge the side branch")
	routes[commitPullsPath(testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD"))] = `[]`
	if code, stdout, stderr := runGlyph(t, "bump", "--since-tag"); code != 0 || stdout != "haiku/v0.1.1\n" {
		t.Fatalf("the merge scoped (haiku) exited %d with %q, want 0 and haiku alone\nstderr: %s", code, stdout, stderr)
	}
}

// TestLintRangePackagesRawRevertNamesTheEscapesThatWork: `git revert` writes
// `Revert "…"`, which the presets claim with a pattern that fixes the sigil at
// ~ and captures no scope. Reverting a shared-only commit in a repository with
// no root package is therefore refused, and the refusal used to name two
// escapes that message cannot take — "name the package in the scope … or
// write =" (t-mfny (A)). It names the ones that work, and the test takes each
// in turn rather than trusting the sentence: the reword as =, the reword with
// a scope, the declaration; and it shows the two old ones still fail.
func TestLintRangePackagesRawRevertNamesTheEscapesThatWork(t *testing.T) {
	dir, _ := packagesRepo(t)
	testutil.Touch(t, dir, "akira-toriyama", ":memo:= add a README", "README.md")
	testutil.Git(t, dir, "akira-toriyama", "revert", "--no-edit", "HEAD")
	t.Chdir(dir)
	lint := func(t *testing.T) (int, string) {
		t.Helper()
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
		return code, stderr
	}
	reword := func(t *testing.T, message string) {
		t.Helper()
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", message)
	}

	code, stderr := lint(t)
	if code != 3 {
		t.Fatalf("a raw revert of a shared-only commit exited %d, want 3\nstderr: %s", code, stderr)
	}
	want := `its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: ` +
		`patterns[1], which claimed this message, fixes the sigil at ~ and captures no scope — ` +
		`reword it so another pattern claims it, with a scope naming the line it moves (one of haiku, curry) or as = so it moves no line, ` +
		`or declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." and a name declare the root package, which holds every file no other package claims)`
	if !strings.Contains(stderr, "::error::glyph: commit ") || !strings.Contains(stderr, ": "+want+"\n") {
		t.Fatalf("the finding must be\n  %s\ngot:\n%s", want, stderr)
	}
	for _, unreachable := range []string{"name the line it moves in the scope", "write = so it moves no line"} {
		if strings.Contains(stderr, unreachable) {
			t.Errorf("the finding names %q, which no message this pattern claims can do:\n%s", unreachable, stderr)
		}
	}

	for _, still := range []string{`Revert ":memo:(haiku)= add a README"`, `Revert ":memo:= add a README" =`} {
		reword(t, still)
		if code, stderr := lint(t); code != 3 {
			t.Errorf("%q exited %d, want 3 — the revert pattern reads neither a scope nor a = from it\nstderr: %s", still, code, stderr)
		}
	}
	reword(t, `:rewind:= Revert ":memo:= add a README"`)
	if code, stderr := lint(t); code != 0 {
		t.Errorf("reworded as = it exited %d, want 0\nstderr: %s", code, stderr)
	}
	reword(t, `:rewind:(curry)~ Revert ":memo:= add a README"`)
	if code, stderr := lint(t); code != 0 {
		t.Errorf("reworded with a scope it exited %d, want 0\nstderr: %s", code, stderr)
	}
	if code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD"); code != 0 || stdout != "curry/v0.1.1\n" {
		t.Errorf("the scoped reword's bump exited %d with %q, want 0 and curry alone\nstderr: %s", code, stdout, stderr)
	}
	reword(t, `Revert ":memo:= add a README"`)
	declared, err := os.ReadFile(filepath.Join(dir, "glyph.toml"))
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "glyph.toml", string(declared)+"\n[[packages]]\npath = \".\"\n")
	if code, stderr := lint(t); code != 2 {
		t.Errorf("the root declared with no name exited %d, want 2 — the presets' scope cannot spell its default, which is why the finding says \"and a name\"\nstderr: %s", code, stderr)
	}
	testutil.WriteFile(t, dir, "glyph.toml", string(declared)+"\n[[packages]]\npath = \".\"\nname = \"core\"\n")
	if code, stderr := lint(t); code != 0 {
		t.Errorf("with the root package declared and named the raw revert exited %d, want 0\nstderr: %s", code, stderr)
	}
}

// TestLintRangePackagesScopelessGrammarOffersNoScope: under a grammar that
// captures no scope the loader exempts every package name — the root's
// default "." included — so no commit can ever name a line, and the refusal
// must not offer one: an empty ~ is told to write =, the one thing its
// message can do. The positive control is the preset, whose refusal for the
// same commit names the scopes
// (TestLintRangePackagesNoFileCommitUnderARootPackage).
func TestLintRangePackagesScopelessGrammarOffersNoScope(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, "glyph.toml", "schema = 1\n\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n\n[note]\nline = '- $subject'\n\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \".\"\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare the lines")
	empty := testutil.Touch(t, dir, "akira-toriyama", ":bookmark:~ cut a release")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	want := "::error::glyph: commit " + empty[:7] + ": this commit touches no file, so no package's tree can carry its sigil ~: write = so it moves no line\n"
	if !strings.Contains(stderr, want) {
		t.Fatalf("the finding must be %q, got:\n%s", want, stderr)
	}
	for _, unreachable := range []string{"in the scope", "(.)", "one of"} {
		if strings.Contains(stderr, unreachable) {
			t.Errorf("the finding offers a scope (%q) under a grammar that captures none:\n%s", unreachable, stderr)
		}
	}
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--amend", "-m", ":bookmark:= cut a release")
	if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 0 {
		t.Fatalf("the = the finding names exited %d, want 0\nstderr: %s", code, stderr)
	}
}

// customRepo is a two-line repository (haiku, curry) under a hand-written
// pattern file — the shape a refusal's escapes are taken literally under,
// where the presets cannot show what a grammar of another shape is told.
// patterns is the file's [[patterns]] blocks; seed is its declaring commit's
// message, which those patterns must claim.
func customRepo(t *testing.T, patterns, seed string) string {
	t.Helper()
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, "glyph.toml", "schema = 1\n\n"+patterns+"\n[note]\nline = '- $subject'\n"+testutil.PackagesSnippet)
	testutil.WriteFile(t, dir, "haiku/haiku.go", "package haiku\n")
	testutil.WriteFile(t, dir, "curry/curry.go", "package curry\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", seed)
	return dir
}

// TestLintRangePackagesRootDeclarationTakesANameWhereTheLoaderAsksOne: the
// declaration escape says the root takes a name exactly where the loader
// would refuse its default — and whether it would is asked of the loader's own
// check, a warn pattern's scope group included. Read off what a message could
// be reworded to, which leaves warn patterns out, a file whose only scope
// group sits in a warn pattern was told `path = "."` declares the root
// package, and the file so written does not load (measured 2026-10-05: exit
// 2). Each case takes the declaration as its finding words it and watches the
// commit pass; the first also writes the declaration the finding must not
// send anyone to.
func TestLintRangePackagesRootDeclarationTakesANameWhereTheLoaderAsksOne(t *testing.T) {
	const scopeless = "[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	const warned = "\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:\\((?P<scope>[a-z0-9-]+)\\)(?P<semver_sigil>[=~^!%]) .+)'\nwarn = 'scoped subjects are discouraged here'\n"
	for name, c := range map[string]struct {
		patterns string
		root     string // the clause the finding gives the root declaration
		declared string // that declaration, written as worded
	}{
		"only a warned pattern captures a scope": {scopeless + warned, `path = "." and a name declare the root package`, "\n[[packages]]\npath = \".\"\nname = \"core\"\n"},
		"no pattern captures a scope":            {scopeless, `path = "." declares the root package`, "\n[[packages]]\npath = \".\"\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := customRepo(t, c.patterns, ":tada:= declare the lines")
			testutil.Touch(t, dir, "akira-toriyama", ":bug:~ fix the readme", "README.md")
			t.Chdir(dir)
			file, err := os.ReadFile(filepath.Join(dir, "glyph.toml"))
			if err != nil {
				t.Fatal(err)
			}

			code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
			want := `: write = so it moves no line, or declare the package these files belong to ([[packages]] path = "<its directory>"; ` + c.root + ", which holds every file no other package claims)\n"
			if code != 3 || !strings.Contains(stderr, want) {
				t.Fatalf("exited %d; the finding must end %q:\n%s", code, want, stderr)
			}

			if strings.Contains(c.declared, "name") {
				testutil.WriteFile(t, dir, "glyph.toml", string(file)+"\n[[packages]]\npath = \".\"\n")
				if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 2 {
					t.Errorf("the root declared with no name exited %d, want 2 — the warn pattern's scope group cannot spell its default, which is why the finding says \"and a name\"\nstderr: %s", code, stderr)
				}
			}
			testutil.WriteFile(t, dir, "glyph.toml", string(file)+c.declared)
			if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 0 {
				t.Errorf("with the root declared as the finding words it lint exited %d, want 0\nstderr: %s", code, stderr)
			}
			if code, stdout, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD"); code != 0 || stdout != "v0.0.1\n" {
				t.Errorf("with the root declared as the finding words it bump exited %d with %q, want 0 and the root line alone at v0.0.1\nstderr: %s", code, stdout, stderr)
			}
		})
	}
}

// TestLintRangePackagesDropTheScopeIsProvenAgainstTheMessage: "drop the scope"
// is offered only when the message with its scope taken out was run through
// the patterns and came back the same pattern's, with no scope and the same
// sigil (config.Sayable). It was read off the pattern's tree — a scope under
// a ?, a *, a {0,n} or ONE BRANCH OF AN ALTERNATION, or a group that can
// capture nothing, counted as droppable — and under
// `(?:type\((?P<scope>…)\)|release)` the finding said "or drop the scope" of a
// message that, scope dropped, matches no pattern (measured 2026-10-05 before
// this rule: exit 3 at lint --range, bump and preview). The tree cannot say
// what is left once the scope is gone, nor which pattern takes it: every case here
// takes each escape its finding names and asserts the exit code, then writes
// the dropped message a finding does not offer and shows why it must not.
func TestLintRangePackagesDropTheScopeIsProvenAgainstTheMessage(t *testing.T) {
	const optional = "[[patterns]]\npattern = '^(?P<subject>[a-z]+(\\((?P<scope>[a-z]+)\\))?(?P<semver_sigil>[=~^!%]): .+)'\n"
	type rewrite struct {
		message string
		code    int
		why     string
	}
	for name, c := range map[string]struct {
		patterns string
		seed     string
		message  string
		remedy   string // the finding's last clause
		rewrites []rewrite
	}{
		"a scope in one branch of an alternation": {
			"[[patterns]]\npattern = '^(?P<subject>(?:[a-z]+\\((?P<scope>[a-z0-9-]+)\\)|release)(?P<semver_sigil>[=~^!%]): .+)'\n",
			"release=: declare the lines", "fix(haiku)~: swap an ingredient",
			"write the scope of a line it moves (curry)",
			[]rewrite{
				{"fix(curry)~: swap an ingredient", 0, "the scope the finding names"},
				{"fix~: swap an ingredient", 3, "the other branch is the literal `release`, so the dropped message matches no pattern"},
			},
		},
		"an earlier pattern claims the message once its scope is gone": {
			"[[patterns]]\npattern = '^wip[=~^!%]: '\nunlandable = 'a wip commit is squashed before it lands'\n\n" + optional,
			"chore=: declare the lines", "wip(haiku)~: swap an ingredient",
			"write the scope of a line it moves (curry)",
			[]rewrite{
				{"wip(curry)~: swap an ingredient", 0, "the scope the finding names"},
				{"wip~: swap an ingredient", 3, "patterns[0] claims the dropped message and marks it unlandable"},
			},
		},
		"the same file, a message no earlier pattern takes": {
			"[[patterns]]\npattern = '^wip[=~^!%]: '\nunlandable = 'a wip commit is squashed before it lands'\n\n" + optional,
			"chore=: declare the lines", "fix(haiku)~: swap an ingredient",
			"write the scope of a line it moves (curry), or drop the scope",
			[]rewrite{
				{"fix(curry)~: swap an ingredient", 0, "the scope the finding names"},
				{"fix~: swap an ingredient", 0, "the drop the finding names"},
			},
		},
		"a ticket inside the scope's optional group": {
			"[[patterns]]\npattern = '^(?P<subject>[a-z]+(?:\\((?P<scope>[a-z]+)#(?P<ticket>[0-9]+)\\))?(?P<semver_sigil>[=~^!%]): .+)'\n",
			"chore=: declare the lines", "fix(haiku#12)~: swap an ingredient",
			"write the scope of a line it moves (curry)",
			[]rewrite{
				{"fix(curry#12)~: swap an ingredient", 0, "the scope the finding names"},
				{"fix(#12)~: swap an ingredient", 3, "the scope alone cannot be dropped: the ticket goes with it, which \"drop the scope\" does not say"},
			},
		},
		"a scope that can be empty but not absent": {
			"[[patterns]]\npattern = '^(?P<subject>[a-z]+\\((?P<scope>[a-z]*)\\)(?P<semver_sigil>[=~^!%]): .+)'\n",
			"chore()=: declare the lines", "fix(haiku)~: swap an ingredient",
			"write the scope of a line it moves (curry)",
			[]rewrite{
				{"fix(curry)~: swap an ingredient", 0, "the scope the finding names"},
				{"fix~: swap an ingredient", 3, "the parentheses are not optional, so the dropped message matches no pattern"},
			},
		},
		"a non-capturing {0,1} around the scope": {
			"[[patterns]]\npattern = '^(?P<subject>[a-z]+(?:\\((?P<scope>[a-z0-9-]+)\\)){0,1}(?P<semver_sigil>[=~^!%]): .+)'\n",
			"chore=: declare the lines", "fix(haiku)~: swap an ingredient",
			"write the scope of a line it moves (curry), or drop the scope",
			[]rewrite{
				{"fix(curry)~: swap an ingredient", 0, "the scope the finding names"},
				{"fix~: swap an ingredient", 0, "the drop the finding names"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := customRepo(t, c.patterns, c.seed)
			testutil.Touch(t, dir, "akira-toriyama", c.message, "curry/curry.go")
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
			want := "curry/curry.go belongs to curry (curry) — the longest declared path owns a file: " + c.remedy + "\n"
			if code != 3 || !strings.Contains(stderr, want) {
				t.Fatalf("%q exited %d; the finding must end %q:\n%s", c.message, code, want, stderr)
			}
			for _, r := range c.rewrites {
				testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", r.message)
				if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != r.code {
					t.Errorf("%q exited %d, want %d — %s\nstderr: %s", r.message, code, r.code, r.why, stderr)
				}
			}
		})
	}
}

// TestLintRangePackagesFixedNoneIsReachedByLeavingTheSigilOut: a pattern whose
// sigil group cannot capture = can still read a message as = — by its fixed
// semver_sigil, when the message leaves the sigil out. That is an escape, and
// the refusal used to deny it existed: under `[a-z]+(?P<semver_sigil>[~^!])?: `
// over a fixed = an empty `fix~: …` was told "nothing a message it claims can
// write carries this commit", while `fix: …` passes (measured 2026-10-05
// before this rule: lint 0, bump 1). It is named now, and proven the way the dropped
// scope is: the message with its sigil taken out goes through the patterns.
// The second grammar holds the sigil in one branch of an alternation, where
// leaving it out matches nothing — so the escape is not named, and neither is
// the old clause, which `chore: …` under the same pattern shows was false.
func TestLintRangePackagesFixedNoneIsReachedByLeavingTheSigilOut(t *testing.T) {
	const noEscape = ": patterns[0], which claimed this message, captures no = as the sigil and captures no scope"
	lint := func(t *testing.T) (int, string) {
		t.Helper()
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
		return code, stderr
	}

	t.Run("an optional sigil over a fixed =", func(t *testing.T) {
		dir := customRepo(t, "[[patterns]]\npattern = '^(?P<subject>[a-z]+(?P<semver_sigil>[~^!])?: .+)'\nsemver_sigil = '='\n", "chore: declare the lines")
		empty := testutil.Touch(t, dir, "akira-toriyama", "fix~: cut a release")
		t.Chdir(dir)

		code, stderr := lint(t)
		want := "::error::glyph: commit " + empty[:7] + ": this commit touches no file, so no package's tree can carry its sigil ~: leave the sigil out so it moves no line\n"
		if code != 3 || !strings.Contains(stderr, want) {
			t.Fatalf("an empty fix~ exited %d; the finding must be %q:\n%s", code, want, stderr)
		}
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--amend", "-m", "fix: cut a release")
		if code, stderr := lint(t); code != 0 {
			t.Errorf("with the sigil left out lint exited %d, want 0 — the escape the finding names\nstderr: %s", code, stderr)
		}
		if code, _, stderr := runGlyph(t, "bump", "--range", "HEAD~1..HEAD"); code != 1 {
			t.Errorf("with the sigil left out bump exited %d, want 1 (it moves no line)\nstderr: %s", code, stderr)
		}

		testutil.Touch(t, dir, "akira-toriyama", "fix~: fix the readme", "README.md")
		code, stderr = lint(t)
		want = `nothing can carry: leave the sigil out so it moves no line, or declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." declares the root package, which holds every file no other package claims)` + "\n"
		if code != 3 || !strings.Contains(stderr, want) {
			t.Fatalf("fix~ on a shared file exited %d; the finding must end %q:\n%s", code, want, stderr)
		}
		testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", "fix: fix the readme")
		if code, stderr := lint(t); code != 0 {
			t.Errorf("with the sigil left out lint exited %d, want 0\nstderr: %s", code, stderr)
		}
	})

	t.Run("a sigil in one branch of an alternation", func(t *testing.T) {
		dir := customRepo(t, "[[patterns]]\npattern = '^(?P<subject>(?:fix(?P<semver_sigil>[~^!])|chore): .+)'\nsemver_sigil = '='\n", "chore: declare the lines")
		empty := testutil.Touch(t, dir, "akira-toriyama", "fix~: cut a release")
		t.Chdir(dir)

		code, stderr := lint(t)
		want := "::error::glyph: commit " + empty[:7] + ": this commit touches no file, so no package's tree can carry its sigil ~" + noEscape + "\n"
		if code != 3 || !strings.Contains(stderr, want) {
			t.Fatalf("an empty fix~ exited %d; the finding must be %q:\n%s", code, want, stderr)
		}
		for _, r := range []struct {
			message string
			code    int
			why     string
		}{
			{"fix: cut a release", 3, "leaving the sigil out matches no pattern here, which is why the finding does not name it"},
			{"chore: cut a release", 0, "a message the same pattern claims does pass, which is why the finding no longer says none can"},
		} {
			testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--amend", "-m", r.message)
			if code, stderr := lint(t); code != r.code {
				t.Errorf("%q exited %d, want %d — %s\nstderr: %s", r.message, code, r.code, r.why, stderr)
			}
		}
	})
}

// TestPackagesNameNoScopeCanSpellIsUsage: a package name the shipped grammar
// cannot spell is a config that does not load — exit 2 on every verdict
// command, the commit-msg hook's --message included — never the 3 whose one
// scope escape cannot be written. Measured before the rule (2026-09-29):
// lint --range refused the shared-only ~ offering "(one of my_lib, other)",
// and `:memo:(my_lib)~` then matched none of the patterns at every gate.
func TestPackagesNameNoScopeCanSpellIsUsage(t *testing.T) {
	dir := testutil.NewPackagesRepo(t, "\n[[packages]]\npath = \"my_lib\"\n\n[[packages]]\npath = \"other\"\n", map[string]string{"my_lib/a.go": "package a\n", "other/b.go": "package b\n"})
	testutil.Touch(t, dir, "akira-toriyama", ":memo:~ fix the readme", "README.md")
	t.Chdir(dir)

	for _, args := range [][]string{
		{"lint", "--range", "HEAD~1..HEAD"},
		{"lint", "--message", ":memo:(my_lib)~ fix the readme"},
		{"bump", "--range", "HEAD~1..HEAD"},
	} {
		code, _, stderr := runGlyph(t, args...)
		if code != int(core.CodeUsage) {
			t.Fatalf("%v exited %d, want exactly %d (a config that does not load)\nstderr: %s", args, code, core.CodeUsage, stderr)
		}
		if env := decodeErrorEnvelope(t, stderr); !strings.Contains(env.Message, `packages[0] ("my_lib")`) || !strings.Contains(env.Message, "set name") {
			t.Errorf("%v refusal = %q, want it to name packages[0] (\"my_lib\") and `set name`", args, env.Message)
		}
	}
}

// TestBumpPackagesScalarsDescribeNoLine pins the machine surface's rule
// (DESIGN §4.1; mutation row packages-scalar-verdict-describes-one-line):
// with packages declared, current / level / next are EMPTY and the answer is
// in packages alone. A consumer that reads only the scalars — every caller
// written for the single line — must find nothing to act on rather than one
// line's number presented as the repository's.
func TestBumpPackagesScalarsDescribeNoLine(t *testing.T) {
	dir, base := packagesRepo(t)
	testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("bump --range --json exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if res.Current != "" || res.Level != "" || res.Next != "" {
		t.Fatalf("scalars describe no line under packages, got current=%q level=%q next=%q", res.Current, res.Level, res.Next)
	}
	if len(res.Packages) != 2 || res.Packages[0].Next != "v0.2.0" {
		t.Fatalf("the answer must be in packages: %s", stdout)
	}
}

// crissCross is the history t-wjbs's refutation built (criss.sh, measured
// 2026-10-26): off the declaring commit, branch A lands a under haiku/ and
// branch B lands b under curry/; A merges b and B merges a, a criss-cross,
// and the lines are tagged on the two merges — haiku/v0.1.0 on A's,
// curry/v0.1.0 on B's — so the lines' bases have two best common ancestors,
// a and b. main merges both branches and lands one pending haiku fix. The
// union starts at the ancestor `git merge-base --octopus` picks, so the
// OTHER one is walked although both tags hold it: held. union is every sha
// the walk visits, for the stand-in's routes.
//
// With side set, branch C lands one commit under that message (haiku/c.go)
// off the declaring commit before A and B, and main merges it after them:
// unreleased on both lines, yet older than a and b, so the walk — oldest
// first by commit date — visits it before held. Every commit is stamped
// from one clock (tick), a second apart, so that order is the fixture's and
// not the machine's: commits made within one second leave git's order to
// how it traversed the merges.
type crissCross struct {
	t                          *testing.T
	dir, a, b, held, side, fix string
	union                      []string
	clock                      int
}

// tick stamps the next commit one second after the last.
func (cc *crissCross) tick() {
	cc.clock++
	at := fmt.Sprintf("2030-01-01T00:%02d:%02d+0000", cc.clock/60, cc.clock%60)
	cc.t.Setenv("GIT_AUTHOR_DATE", at)
	cc.t.Setenv("GIT_COMMITTER_DATE", at)
}

// touch lands one more commit on main, after the fixture's last.
func (cc *crissCross) touch(message, path string) string {
	cc.t.Helper()
	cc.tick()
	return testutil.Touch(cc.t, cc.dir, "akira-toriyama", message, path)
}

func newCrissCross(t *testing.T, messageA, messageB, side string) crissCross {
	t.Helper()
	cc := crissCross{t: t, dir: testutil.NewTwoLineRepo(t)}
	git := func(args ...string) string { cc.tick(); return testutil.Git(t, cc.dir, "akira-toriyama", args...) }
	if side != "" {
		git("checkout", "-q", "-b", "C")
		cc.side = cc.touch(side, "haiku/c.go")
		git("checkout", "-q", "main")
	}
	git("checkout", "-q", "-b", "A")
	a := cc.touch(messageA, "haiku/a.go")
	git("checkout", "-q", "main")
	git("checkout", "-q", "-b", "B")
	b := cc.touch(messageB, "curry/b.go")
	git("checkout", "-q", "A")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'B' into A", b)
	git("tag", "haiku/v0.1.0")
	git("checkout", "-q", "B")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'A' into B", a)
	git("tag", "curry/v0.1.0")
	git("checkout", "-q", "main")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'A'", "haiku/v0.1.0")
	git("merge", "-q", "--no-ff", "-m", "Merge branch 'B'", "curry/v0.1.0")
	if side != "" {
		git("merge", "-q", "--no-ff", "-m", "Merge branch 'C'", "C")
	}
	fix := cc.touch(":bug:~ fix a haiku", "haiku/x.go")

	// The positive control: the history is the shape under test — the union
	// starts at one of the two best common ancestors and walks the other,
	// which both tags hold.
	base := git("merge-base", "--octopus", "haiku/v0.1.0", "curry/v0.1.0")
	held := a
	switch base {
	case a:
		held = b
	case b:
	default:
		t.Fatalf("the union starts at %.7s, want one of the criss-cross's two common ancestors %.7s and %.7s", base, a, b)
	}
	union := strings.Fields(git("log", "--format=%H", base+"..HEAD"))
	if !slices.Contains(union, held) {
		t.Fatalf("the union %.7s..HEAD does not walk %.7s", base, held)
	}
	for _, tag := range []string{"haiku/v0.1.0", "curry/v0.1.0"} {
		if !slices.Contains(strings.Fields(git("log", "--format=%H", tag)), held) {
			t.Fatalf("%s does not hold %.7s, so it is not released on every line", tag, held)
		}
	}
	if cc.side != "" && slices.Index(union, cc.side) < slices.Index(union, held) {
		t.Fatalf("the walk visits %.7s before the side commit %.7s, want the side commit first", held, cc.side)
	}
	cc.a, cc.b, cc.held, cc.fix, cc.union = a, b, held, fix, union
	return cc
}

// TestSinceTagPackagesCommitReleasedOnEveryLineIsDroppedNotRefused: a commit
// the union walk holds that every line has already released is dropped with
// the notice before the walk judges its message — never refused at 3
// (DESIGN §4.1, "The walk is one walk"). Every line's tag already sits past
// it, so the refusal named an escape already taken, over work both lines had
// shipped. #274 moved the walk's refusal of a message no pattern claims
// ahead of the partition, where the drop ran, so a direct push of that shape
// went from v5.0.0's notice and exit 0 to exit 3 on every verdict command
// (t-wjbs correct-a-1, measured 2026-10-26 on 82aebc5); a squash-merged
// pull's inner commit at such a merge point was refused on both. The two
// arms judge by one identity, the commit's governing one: its own sha on the
// released branch, the pull's merge point for a listed commit that landed
// under none.
func TestSinceTagPackagesCommitReleasedOnEveryLineIsDroppedNotRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		messageA, messageB string
		routes             func(cc crissCross) map[string]string
		dropped            func(cc crissCross) string
	}{
		"a direct push no pattern claims": {
			messageA: "no gitmoji leads this subject", messageB: "nor this one",
			routes:  func(crissCross) map[string]string { return map[string]string{} },
			dropped: func(cc crissCross) string { return cc.held[:7] },
		},
		"a squash-merged pull's inner commit no pattern claims": {
			messageA: "Add a season (#80)", messageB: "Swap an ingredient (#81)",
			routes: func(cc crissCross) map[string]string {
				return map[string]string{
					commitPullsPath(cc.a): `[` + apiPullRef(80, "2026-07-13T00:00:00Z", cc.a) + `]`,
					pullCommitsPath(80):   `[` + apiCommit("u80", "akira-toriyama", "no gitmoji leads this subject") + `]`,
					commitPullsPath(cc.b): `[` + apiPullRef(81, "2026-07-13T00:00:00Z", cc.b) + `]`,
					pullCommitsPath(81):   `[` + apiCommit("u81", "akira-toriyama", "nor this one") + `]`,
				}
			},
			dropped: func(cc crissCross) string {
				if cc.held == cc.a {
					return "u80"
				}
				return "u81"
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cc := newCrissCross(t, tc.messageA, tc.messageB, "")
			routes := tc.routes(cc)
			for _, sha := range cc.union {
				if _, ok := routes[commitPullsPath(sha)]; !ok {
					routes[commitPullsPath(sha)] = `[]`
				}
			}
			usePR(t, walkServer(t, routes))
			t.Chdir(cc.dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
			if code != 0 {
				t.Fatalf("bump --since-tag --json exited %d, want 0 — the commit is released on every line\nstderr: %s", code, stderr)
			}
			res := decodePackagesVerdict(t, stdout)
			h, c := res.Packages[0], res.Packages[1]
			if h.Path != "haiku" || h.Next != "v0.1.1" || len(h.Commits) != 1 || h.Commits[0].SHA != cc.fix {
				t.Fatalf("haiku = %+v, want v0.1.0 → v0.1.1 over the pending fix alone", h)
			}
			if c.Path != "curry" || c.Level != "none" || len(c.Commits) != 0 {
				t.Fatalf("curry = %+v, want none: nothing is pending on it", c)
			}
			if want := "::notice::glyph: commit " + tc.dropped(cc) + " is inside the union walk but already released on every line — not counted"; !strings.Contains(stderr, want) {
				t.Errorf("the drop must be said:\n  %s\nstderr: %s", want, stderr)
			}

			if code, _, stderr := runGlyph(t, "notes", "--since-tag"); code != 0 {
				t.Errorf("notes --since-tag shares the walk and exited %d, want 0\nstderr: %s", code, stderr)
			}
		})
	}
}

// TestSinceTagPackagesStackedPullJudgesEachSighting: a squash-merged pull's
// inner commit that a stacked pull #90 lists again is judged under each
// pull's merge point — its governing identity there — and one judged
// sighting refuses it, the refusal naming the escape of every judged sighting
// and none at an identity every line released. Here the first pull's merge
// point is the criss-cross's held
// commit, released on every line, and #90's is pending on both, so the
// refusal names #90's merge point per line and never the held one, which
// every line already passed (DESIGN §4.1, "The walk is one walk").
//
// "#90 after" is t-wjbs design-1: the held pull listed the commit first, it
// went in unjudged, and #90's sighting met the walk's "already represented"
// arm, so the union walk answered 0 (haiku/v0.2.0) where v5.0.0, ba484da and
// the haiku line's own walk answered 3 (measured 2026-10-27; mutation row
// packages-walk-skips-a-later-sighting-it-never-judged). "#90 before" is the
// same pulls the other way round — the order a refusal must not hang on, which
// it did for the unclaimed message until the later sighting was judged too
// (t-wjbs correct-5): #90 refuses the commit first, and the held pull's
// re-sighting adds no escape at an identity every line released (mutation row
// packages-walk-names-an-escape-every-line-passed). Which line a CLAIMED
// commit two pulls list joins still follows the first sighting; that predates
// this rule and is the main loop's follow-up. The same commit by an
// exclude_authors author is judged at neither sighting, so nothing is refused
// (t-wjbs correct-r1-1, measured by review 2026-10-27: exit 0 on v5.0.0,
// ba484da, 80c849a and 36b7c53; mutation row
// packages-walk-judges-a-later-sighting-of-an-excluded-author).
func TestSinceTagPackagesStackedPullJudgesEachSighting(t *testing.T) {
	for _, tc := range []struct {
		name, author string
		before       bool
	}{
		{name: "#90 after the held pull", author: "akira-toriyama"},
		{name: "#90 before the held pull", author: "akira-toriyama", before: true},
		{name: "an excluded author's commit, #90 after the held pull", author: "dependabot[bot]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const stacked = "Stack a season (#90)"
			side := ""
			if tc.before {
				side = stacked
			}
			cc := newCrissCross(t, "Add a season (#80)", "Swap an ingredient (#81)", side)
			p90 := cc.side
			if !tc.before {
				p90 = cc.touch(stacked, "haiku/y.go")
			}
			inner := apiCommit("u1", tc.author, "no gitmoji leads this subject")
			routes := map[string]string{
				commitPullsPath(cc.a): `[` + apiPullRef(80, "2026-07-13T00:00:00Z", cc.a) + `]`,
				pullCommitsPath(80):   `[` + inner + `]`,
				commitPullsPath(cc.b): `[` + apiPullRef(81, "2026-07-13T00:00:00Z", cc.b) + `]`,
				pullCommitsPath(81):   `[` + inner + `]`,
				commitPullsPath(p90):  `[` + apiPullRef(90, "2026-07-14T00:00:00Z", p90) + `]`,
				pullCommitsPath(90):   `[` + inner + `,` + apiCommit("h9", "akira-toriyama", ":sparkles:^ stack a season") + `]`,
				commitFilesPath("u1"): apiFiles("haiku/a.go"),
				commitFilesPath("h9"): apiFiles("haiku/y.go"),
			}
			for _, sha := range cc.union {
				if _, ok := routes[commitPullsPath(sha)]; !ok {
					routes[commitPullsPath(sha)] = `[]`
				}
			}
			usePR(t, walkServer(t, routes))
			t.Chdir(cc.dir)

			if tc.author == "dependabot[bot]" {
				// exclude_authors is never judged by its message, at any
				// sighting: #90's stack of a season moves haiku, and nothing
				// is refused, on the union walk and the line's own alike.
				for _, args := range [][]string{{"bump", "--since-tag"}, {"bump", "--since-tag=haiku/v0.1.0"}} {
					if code, stdout, stderr := runGlyph(t, args...); code != 0 || stdout != "haiku/v0.2.0\n" {
						t.Errorf("%v exited %d with %q, want 0 and haiku/v0.2.0: an excluded author's message is not judged\nstderr: %s", args, code, stdout, stderr)
					}
				}
				return
			}
			code, _, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 3 {
				t.Fatalf("bump --since-tag exited %d, want 3: #90's merge point is pending on both lines\nstderr: %s", code, stderr)
			}
			env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
			assertWedgeDetails(t, env, "u1")
			for _, want := range []string{"AT OR PAST " + p90[:7], "Cut a haiku/ tag at or past " + p90[:7] + " / a curry/ tag at or past " + p90[:7] + " by hand"} {
				if !strings.Contains(env.Message, want) {
					t.Errorf("the refusal must name #90's escape (%q):\n%s", want, env.Message)
				}
			}
			if strings.Contains(env.Message, cc.held[:7]) {
				t.Errorf("the refusal names %.7s, a merge point every line already passed:\n%s", cc.held, env.Message)
			}

			if code, _, stderr := runGlyph(t, "bump", "--since-tag=haiku/v0.1.0"); code != 3 {
				t.Errorf("the haiku line's own walk exited %d, want 3, as the union walk answers\nstderr: %s", code, stderr)
			}
		})
	}
}

// TestSinceTagPackagesUnclaimedNamesTheEscapePerLine: under [[packages]] a
// walked commit no pattern claims joins every line whose range holds it, so
// its wedge clears line by line — a tag on each such line at or past the
// commit's governing identity — and the refusal names that tag per line, as
// rule 3's attribution refusal does (DESIGN §4.1). The single line's "Cut a
// release tag there by hand" cleared neither line: a bare tag is no line's,
// and one line's tag left the other wedged (t-wjbs correct-a-2, measured
// 2026-10-26 on 82aebc5 for both shapes and on v5.0.0 for the pull's inner
// commit; v5.0.0 refused the direct push with the fold's authoring wording,
// before #274 gave direct pushes the walk's refusal). The test takes the
// escapes in turn — the bare tag first, which must change nothing — and
// reads the sentence shrink to the line still wedged.
func TestSinceTagPackagesUnclaimedNamesTheEscapePerLine(t *testing.T) {
	for name, land := range map[string]func(t *testing.T, dir string) (at string, routes map[string]string){
		"a direct push": func(t *testing.T, dir string) (string, map[string]string) {
			sha := testutil.Touch(t, dir, "akira-toriyama", "no gitmoji leads this subject", "haiku/z.go")
			return sha, map[string]string{commitPullsPath(sha): `[]`}
		},
		"a squash-merged pull's inner commit": func(t *testing.T, dir string) (string, map[string]string) {
			sha := testutil.Touch(t, dir, "akira-toriyama", "Add a season (#70)", "haiku/season.go")
			return sha, map[string]string{
				commitPullsPath(sha): `[` + apiPullRef(70, "2026-07-13T00:00:00Z", sha) + `]`,
				pullCommitsPath(70):  `[` + apiCommit("u1", "akira-toriyama", "no gitmoji leads this subject") + `]`,
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			at, routes := land(t, dir)
			usePR(t, walkServer(t, routes))
			t.Chdir(dir)

			refusal := func(step string) string {
				t.Helper()
				code, _, stderr := runGlyph(t, "bump", "--since-tag")
				if code != 3 {
					t.Fatalf("%s: bump --since-tag exited %d, want 3\nstderr: %s", step, code, stderr)
				}
				return decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):]).Message
			}
			both := "Cut a haiku/ tag at or past " + at[:7] + " / a curry/ tag at or past " + at[:7] + " by hand, or name such a tag with --since-tag=<prefix>vX.Y.Z (DESIGN §4.1)"
			msg := refusal("no tag cut")
			for _, want := range []string{"every release of a line whose range holds it wedges here until that line's walk starts past it", "AT OR PAST " + at[:7], both} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal is missing %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "Cut a release tag") {
				t.Errorf("the single line's escape names a tag no line reads:\n%s", msg)
			}

			testutil.Git(t, dir, "akira-toriyama", "tag", "v0.0.1", at)
			if msg := refusal("a bare tag cut"); !strings.Contains(msg, both) {
				t.Errorf("a bare tag is no line's and must leave both lines wedged:\n%s", msg)
			}

			testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.1", at)
			msg = refusal("haiku's tag cut")
			if !strings.Contains(msg, "Cut a curry/ tag at or past "+at[:7]+" by hand") || strings.Contains(msg, "haiku/ tag") {
				t.Errorf("with haiku's walk past the commit only curry's tag is left to cut:\n%s", msg)
			}

			testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.1", at)
			if code, stdout, stderr := runGlyph(t, "bump", "--since-tag"); code != 1 {
				t.Fatalf("the escapes named exited %d with %q, want 1: both walks start past the commit, and nothing is left\nstderr: %s", code, stdout, stderr)
			}
		})
	}
}

// threeAttributionRefusals lands three direct pushes attribution refuses — a
// shared-only ^, a scope contradicting the tree, another shared-only ^ — on
// packagesRepo's tagged lines.
func threeAttributionRefusals(t *testing.T, dir string) (shas []string, routes map[string]string) {
	t.Helper()
	routes = map[string]string{}
	for _, c := range [][2]string{
		{":sparkles:^ add a CI job", "ci.yml"},
		{":bug:(curry)~ fix a haiku", "haiku/x.go"},
		{":sparkles:^ add a docs page", "docs.md"},
	} {
		sha := testutil.Touch(t, dir, "akira-toriyama", c[0], c[1])
		shas = append(shas, sha)
		routes[commitPullsPath(sha)] = `[]`
	}
	return shas, routes
}

// TestSinceTagPackagesHandsDownEveryAttributionRefusalAtOnce: attribution's
// refusals go out once the whole range is partitioned, every refused commit
// in details and every line's escape in the sentence — the whole-range rule
// §3 states for a message no pattern claims, and what lint --range over the
// same range answers (DESIGN §4.1, rule 3). The walk handed down the first
// alone, so the escape it named left the next refusal to the next run: three
// red runs for three commits (t-wjbs correct-a-3, measured 2026-10-26 on
// 82aebc5 and v5.0.0 alike). Tags cut where the one sentence says clear the
// range in one step.
func TestSinceTagPackagesHandsDownEveryAttributionRefusalAtOnce(t *testing.T) {
	dir, base := packagesRepo(t)
	shas, routes := threeAttributionRefusals(t, dir)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	// The yardstick: lint --range names all three.
	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	assertWedgeDetails(t, decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):]), shas...)

	code, _, stderr = runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("bump --since-tag exited %d, want 3\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	assertWedgeDetails(t, env, shas...)
	all := shas[0][:7] + ", " + shas[1][:7] + ", " + shas[2][:7]
	for _, want := range []string{
		"commit " + shas[0][:7] + ": its files (ci.yml) belong to no declared package",
		"(+2 more refused commit(s), all in details)",
		"cut a haiku/ tag at or past all of " + all + " / a curry/ tag at or past all of " + all + " by hand",
	} {
		if !strings.Contains(env.Message, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, env.Message)
		}
	}

	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.1", shas[2])
	testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.1", shas[2])
	if code, _, stderr := runGlyph(t, "bump", "--since-tag"); code != 1 {
		t.Fatalf("the tags the refusal named exited %d, want 1: one red run, and every refused commit is behind both walks\nstderr: %s", code, stderr)
	}
}

// TestSinceTagPackagesAttributionEscapeNamesOnlyTheLinesHoldingEachCommit:
// attribution's refusal names a tag on a line only for the refused commits
// that line's range holds. curry is tagged on the declaring commit and haiku
// on s1, so s1 wedges curry alone and s2 both: haiku's tag clears it at s2,
// curry's at s1 and s2 both. Naming every line passed the whole suite, before
// this branch and on it (t-wjbs correct-2, measured 2026-10-27 by review;
// mutation row attribution-wedge-names-a-tag-on-every-line).
func TestSinceTagPackagesAttributionEscapeNamesOnlyTheLinesHoldingEachCommit(t *testing.T) {
	dir := testutil.NewTwoLineRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "tag", "curry/v0.1.0")
	s1 := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a CI job", "ci.yml")
	testutil.Git(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	s2 := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a docs page", "docs.md")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(s1): `[]`, commitPullsPath(s2): `[]`}))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("bump --since-tag exited %d, want 3\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	assertWedgeDetails(t, env, s1, s2)
	if want := "cut a haiku/ tag at or past " + s2[:7] + " / a curry/ tag at or past all of " + s1[:7] + ", " + s2[:7] + " by hand"; !strings.Contains(env.Message, want) {
		t.Errorf("each line's tag must name only the commits its range holds (%q):\n%s", want, env.Message)
	}
}

// TestBumpRangePackagesAttributionRefusalIsTheAuthoringOne: --range reads a
// local range whose commits may still be rewritten, so attribution's refusal
// is the fold's — the escapes the commit can take and "refusing to version the
// range" — never the walk's claim that the commit sits on a published branch
// with a tag to cut past it (DESIGN §4: the fold's authoring remedy is kept
// for --range). Measured 2026-10-26 on 82aebc5 and v5.0.0 alike (t-wjbs
// correct-a-4): one unpushed commit in a checkout with no remote was told it
// "is already on a published branch and cannot be rewritten" and to cut a tag
// on each line. Every refusal of the range goes out at once, as the fold's.
func TestBumpRangePackagesAttributionRefusalIsTheAuthoringOne(t *testing.T) {
	dir, base := packagesRepo(t)
	shas, _ := threeAttributionRefusals(t, dir)
	t.Chdir(dir)

	for _, cmd := range []string{"bump", "notes"} {
		code, stdout, stderr := runGlyph(t, cmd, "--range", base+"..HEAD")
		if code != 3 || stdout != "" {
			t.Fatalf("%s --range exited %d with %q, want 3 and nothing on stdout\nstderr: %s", cmd, code, stdout, stderr)
		}
		env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
		assertWedgeDetails(t, env, shas...)
		if want := "commit " + shas[0][:7] + ": its files (ci.yml) belong to no declared package"; !strings.HasPrefix(env.Message, want) {
			t.Errorf("%s --range: the refusal must open on the first refused commit (%q):\n%s", cmd, want, env.Message)
		}
		if want := " (+2 more violation(s), all in details); refusing to version the range"; !strings.HasSuffix(env.Message, want) {
			t.Errorf("%s --range: the refusal must close as the fold's does (%q):\n%s", cmd, want, env.Message)
		}
		for _, walks := range []string{"published branch", "tag at or past", "--since-tag"} {
			if strings.Contains(env.Message, walks) {
				t.Errorf("%s --range: %q is the walk's escape, for a commit nobody can rewrite:\n%s", cmd, walks, env.Message)
			}
		}
	}
}

// TestSinceTagPackagesAttributionRefusalOutranksALaterAPIFailure: collecting
// every attribution refusal means reading past the first, so a read that then
// fails must not hide the refusal already found — the walk's own rule
// (TestSinceTagRefusalOutranksALaterAPIFailure), and the answer the partition
// gave when it stopped at the first refusal and read no further.
func TestSinceTagPackagesAttributionRefusalOutranksALaterAPIFailure(t *testing.T) {
	dir, _ := packagesRepo(t)
	shared := testutil.Touch(t, dir, "akira-toriyama", ":sparkles:^ add a CI job", "ci.yml")
	sha := testutil.Touch(t, dir, "akira-toriyama", "Add a season (#7)", "haiku/season.go")
	srv := failingWalkServer(t, map[string]string{
		commitPullsPath(shared): `[]`,
		commitPullsPath(sha):    `[` + apiPullRef(7, "2026-07-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):      `[` + apiCommit("h1", "akira-toriyama", ":sparkles:^ add a season") + `]`,
	}, func(r *http.Request) int {
		if r.URL.Path == commitFilesPath("h1") {
			return http.StatusInternalServerError
		}
		return 0
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("an attribution refusal followed by a 500 exited %d, want 3 (the refusal is the verdict)\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if !strings.Contains(env.Message, "commit "+shared[:7]+": its files (ci.yml) belong to no declared package") || strings.Contains(env.Message, "boom") {
		t.Errorf("the envelope must be the refusal, not the later failure:\n%s", env.Message)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "boom") || !strings.Contains(stderr, "more refused commits") || !strings.Contains(stderr, "the rest of the range could not be read") {
		t.Errorf("the later failure must be announced beside the refusal:\n%s", stderr)
	}
}
