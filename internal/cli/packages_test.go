package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/core"
	"github.com/akira-toriyama/glyph/v4/internal/testutil"
)

// This file pins the packages walk (DESIGN §4.1): a repository declaring two
// lines, haiku/ and curry/, each with its own tag, and the property the
// live-fire harness glyph-monorepo-test exists to prove — a pull that
// touches both modules with a ^ in one and a ~ in the other moves the two
// lines differently, and a squash merge never loses which of the pull's
// commits touched which module.

// packagesConfig is what a repository appends to the gemoji preset to
// declare its lines.
const packagesConfig = "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \"curry\"\n"

// packagesRepo builds the two-line fixture: the preset plus [[packages]] for
// haiku/ and curry/, one file under each, and haiku/v0.1.0 + curry/v0.1.0
// on the commit that declared them. base is that commit.
func packagesRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = packagesRepoWith(t, packagesConfig, map[string]string{"haiku/haiku.go": "package haiku\n", "curry/curry.go": "package curry\n"})
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	testGit(t, dir, "akira-toriyama", "tag", "curry/v0.1.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// packagesRepoWith is packagesRepo's body for any [[packages]] snippet and
// file set: the preset plus the snippet, the files, one declaring commit,
// no tags.
func packagesRepoWith(t *testing.T, snippet string, files map[string]string) string {
	t.Helper()
	dir := testutil.NewRepo(t)
	appendTo(t, dir, "glyph.toml", snippet)
	for rel, content := range files {
		writeFile(t, dir, rel, content)
	}
	testGit(t, dir, "akira-toriyama", "add", ".")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare the lines")
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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

// touchSeq makes every touch write distinct content, so two commits with the
// same message under the same path still each have a diff.
var touchSeq atomic.Int64

// touch commits one change under each of paths, as author, under message —
// a landed identity whose files local git answers for — and returns its sha.
func touch(t *testing.T, dir, author, message string, paths ...string) string {
	t.Helper()
	n := touchSeq.Add(1)
	for _, p := range paths {
		writeFile(t, dir, p, fmt.Sprintf("%s\n%d\n", p, n))
	}
	testGit(t, dir, author, "add", "-A", ".")
	testGit(t, dir, author, "commit", "-q", "--allow-empty", "-m", message)
	return testGit(t, dir, author, "rev-parse", "HEAD")
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
	sha = touch(t, dir, "akira-toriyama", fmt.Sprintf("Add a season and swap an ingredient (#%d)", number), "haiku/season.go", "curry/curry.go")
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
	sha := touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
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
	sha := touch(t, dir, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
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

// TestSinceTagPackagesSharedOnlyNoneParticipatesNowhere: a = under no package
// is shared housekeeping — on no line, in no verdict, but still among the
// commits the walk read. Every line folds to none, so exit 1.
func TestSinceTagPackagesSharedOnlyNoneParticipatesNowhere(t *testing.T) {
	dir, _ := packagesRepo(t)
	sha := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
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
// contradiction check: a scope naming a package the diff does not touch is
// refused, however the files fall.
func TestSinceTagPackagesScopeCarriesAndContradicts(t *testing.T) {
	t.Run("scope carries a shared-only commit", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		sha := touch(t, dir, "akira-toriyama", ":sparkles:(haiku)^ describe the season API", "README.md")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
		t.Chdir(dir)
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "haiku/v0.2.0\n" {
			t.Fatalf("scope-carried ^ exited %d with %q, want 0 and haiku/v0.2.0\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("scope contradicting the tree is refused", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		sha := touch(t, dir, "akira-toriyama", ":bug:(haiku)~ swap an ingredient", "curry/curry.go")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 3 {
			t.Fatalf("a contradicting scope exited %d, want 3\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "names a package this commit does not touch") {
			t.Fatalf("refusal must name the contradiction:\n%s", stderr)
		}
	})
}

// TestSinceTagPackagesATagNamesALine: --since-tag=<prefix>vX.Y.Z and
// below:<prefix>vX.Y.Z select that line alone — the verdict is that line's,
// the other is not converged — and --current is accepted exactly then. A
// bare --since-tag walks every line, so --current has two verdicts to
// describe and is refused; a tag on a line no [[packages]] entry declares is
// usage, never a walk of some other line.
func TestSinceTagPackagesATagNamesALine(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	for name, tc := range map[string]struct {
		args []string
		code int
		want string // stdout on 0, a stderr fragment otherwise
	}{
		"explicit tag selects haiku":   {[]string{"bump", "--since-tag=haiku/v0.1.0"}, 0, "haiku/v0.2.0\n"},
		"below selects curry":          {[]string{"bump", "--since-tag=below:curry/v0.2.0"}, 0, "curry/v0.1.1\n"},
		"current on one selected line": {[]string{"bump", "--since-tag=curry/v0.1.0", "--current", "v1.0.0"}, 0, "curry/v1.0.1\n"},
		"current over every line":      {[]string{"bump", "--since-tag", "--current", "v1.0.0"}, 2, "answers for 2 lines"},
		"an undeclared line is usage":  {[]string{"bump", "--since-tag=fish/v0.1.0"}, 2, "no [[packages]] entry declares"},
		"below an undeclared line":     {[]string{"bump", "--since-tag=below:fish/v0.2.0"}, 2, "no [[packages]] entry declares"},
		"a bare tag with no root line": {[]string{"bump", "--since-tag=v0.1.0"}, 2, "bare v* line"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != tc.code {
				t.Fatalf("%v exited %d, want %d\nstderr: %s", tc.args, code, tc.code, stderr)
			}
			if code == 0 && stdout != tc.want {
				t.Fatalf("%v stdout = %q, want %q", tc.args, stdout, tc.want)
			}
			if code != 0 && !strings.Contains(stderr, tc.want) {
				t.Fatalf("%v stderr is missing %q:\n%s", tc.args, tc.want, stderr)
			}
		})
	}

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=haiku/v0.1.0", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag=haiku/v0.1.0 --json exited %d\nstderr: %s", code, stderr)
	}
	if res := decodePackagesVerdict(t, stdout); len(res.Packages) != 1 || res.Packages[0].Path != "haiku" {
		t.Fatalf("a selected line must be the ONLY verdict, got %s", stdout)
	}
}

// TestSinceTagPackagesACandidateTagNamesALine: a tag names a line whenever
// it is version-SHAPED on that line, not only when it is a plain version —
// haiku/v0.2.0-rc.1 and curry/v0.1.1+build.1 select their line exactly as
// below: reads its bound (DESIGN §4.1; mutation row
// packages-candidate-tag-walks-every-line). The candidate is never an
// answer: the selected line steps from its highest PLAIN tag, as the single
// line does with a tag that names no base. The undeclared-line guard is
// reached for the shape too — exit 2, never git's "bad revision" at 4 — and
// a tag that is not version-shaped on any line (haiku/nightly) still names
// no line: every line walks from it.
//
// Measured before the fix (t-gt9n, 2026-09-11): the plain arm parsed only
// vX.Y.Z, so a candidate on a declared line fell through to "not a version
// on any line" and every declared line walked haiku/v3.0.0-rc.1..HEAD —
// each sibling re-folding commits it had released and stepping past its own
// highest tag, exit 0, nothing on stderr.
func TestSinceTagPackagesACandidateTagNamesALine(t *testing.T) {
	dir, _ := packagesRepo(t)
	for _, tag := range []string{"haiku/v0.2.0-rc.1", "curry/v0.1.1+build.1", "haiku/nightly"} {
		testGit(t, dir, "akira-toriyama", "tag", tag)
	}
	_, routes := squashAcrossLines(t, dir, 7)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	for name, tc := range map[string]struct {
		args []string
		code int
		want string // stdout on 0, a stderr fragment otherwise
	}{
		"a candidate selects haiku":          {[]string{"bump", "--since-tag=haiku/v0.2.0-rc.1"}, 0, "haiku/v0.2.0\n"},
		"build metadata selects curry":       {[]string{"bump", "--since-tag=curry/v0.1.1+build.1"}, 0, "curry/v0.1.1\n"},
		"current on the candidate's line":    {[]string{"bump", "--since-tag=haiku/v0.2.0-rc.1", "--current", "v1.0.0"}, 0, "haiku/v1.1.0\n"},
		"a candidate on an undeclared line":  {[]string{"bump", "--since-tag=fish/v0.1.0-rc.1"}, 2, "no [[packages]] entry declares"},
		"a bare candidate with no root line": {[]string{"bump", "--since-tag=v0.1.0-rc.1"}, 2, "bare v* line"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, tc.args...)
			if code != tc.code {
				t.Fatalf("%v exited %d, want %d\nstderr: %s", tc.args, code, tc.code, stderr)
			}
			if code == 0 && stdout != tc.want {
				t.Fatalf("%v stdout = %q, want %q", tc.args, stdout, tc.want)
			}
			if code != 0 && !strings.Contains(stderr, tc.want) {
				t.Fatalf("%v stderr is missing %q:\n%s", tc.args, tc.want, stderr)
			}
		})
	}

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=haiku/v0.2.0-rc.1", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag=haiku/v0.2.0-rc.1 --json exited %d\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if len(res.Packages) != 1 || res.Packages[0].Path != "haiku" {
		t.Fatalf("a candidate names its line, and that line is the ONLY verdict — a sibling walking from haiku's candidate re-folds what it released; got %s", stdout)
	}
	if res.Packages[0].Current != "v0.1.0" {
		t.Fatalf("the selected line steps from its highest PLAIN tag (a candidate is a question, never an answer), got current=%q", res.Packages[0].Current)
	}

	code, stdout, stderr = runGlyph(t, "bump", "--since-tag=haiku/nightly", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag=haiku/nightly --json exited %d\nstderr: %s", code, stderr)
	}
	if res := decodePackagesVerdict(t, stdout); len(res.Packages) != 2 {
		t.Fatalf("a tag that is not version-shaped on any line names no line — every line walks from it; got %s", stdout)
	}
}

// TestSinceTagPackagesBelowNothingUnderTheBoundNamesTheBound: below: on a
// line whose tags all sit at or above the bound walks the whole history —
// that line's first release, as the single line does — and the warning says
// WHY: no version tag BELOW THE BOUND on that line. The remedy is the same
// tag as for a line with no tag at all (cut <path>/v0.0.0 before the line's
// first change), the diagnosis is not: the first cut reported "no version
// tag on the haiku/ line", a sentence `git tag -l 'haiku/v*'` refutes, and
// past the walk cap that sentence is the exit-4 refusal body the operator
// is stopped by (t-gt9n).
func TestSinceTagPackagesBelowNothingUnderTheBoundNamesTheBound(t *testing.T) {
	dir, base := packagesRepo(t)
	root := testGit(t, dir, "akira-toriyama", "rev-list", "--max-parents=0", "HEAD")
	_, routes := squashAcrossLines(t, dir, 7)
	routes[commitPullsPath(root)] = `[]`
	routes[commitPullsPath(base)] = `[]`
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag=below:haiku/v0.1.0", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag=below:haiku/v0.1.0 exited %d, want 0 (a first release walks the whole history)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "no version tag below haiku/v0.1.0 on the haiku/ line in HEAD's history") || !strings.Contains(stderr, "cut haiku/v0.0.0") {
		t.Fatalf("the warning must name the bound the line has nothing under, and the remedy:\n%s", stderr)
	}
	if strings.Contains(stderr, "no version tag on the") {
		t.Fatalf("the line HAS a tag — the diagnosis must not claim otherwise:\n%s", stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if len(res.Packages) != 1 || res.Packages[0].Path != "haiku" || res.Packages[0].Current != "v0.0.0" {
		t.Fatalf("below: selects haiku alone, stepping from v0.0.0 before its first release, got %s", stdout)
	}
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
	touch(t, dir, "dependabot[bot]", ":arrow_up:(haiku)~ bump a haiku-only dependency", "haiku/poem.go")
	touch(t, dir, "dependabot[bot]", "Bump golang.org/x/net from 0.1.0 to 0.2.0", "curry/go.mod")
	touch(t, dir, "dependabot[bot]", "Bump actions/checkout from 4 to 5", ".github/workflows/ci.yml")
	fix := touch(t, dir, "akira-toriyama", ":bug:(curry)~ a curry fix", "curry/rice.go")
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
	sha := touch(t, dir, "akira-toriyama", "Bump a haiku dependency and fix curry (#8)", "haiku/go.mod", "curry/curry.go")
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
	shipped := touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	pending := touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
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
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.2.0")
	later := touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
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
	dir, _ := testRepo(t)
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

// TestSinceTagPackagesFilesCapIsAnIncompleteWalk: an inner commit whose file
// listing came back at GitHub's cap is recorded as a walk that could not
// read its range — bump warns and answers, and the facts say incomplete so
// that a writing command refuses (the same gate Truncated sits behind).
func TestSinceTagPackagesFilesCapIsAnIncompleteWalk(t *testing.T) {
	dir, _ := packagesRepo(t)
	_, routes := squashAcrossLines(t, dir, 7)
	capped := make([]string, 0, 3000)
	for i := range 3000 {
		capped = append(capped, fmt.Sprintf("haiku/gen/f%d.go", i))
	}
	routes[commitFilesPath("h1")] = apiFiles(capped...)
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0 (bump reports, it does not act)\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "GitHub lists no more than that") {
		t.Fatalf("the cap must be warned about:\n%s", stderr)
	}

	// The PR side's shape — no pull, no escapes — renders bare shas joined
	// by commas, as preview's caveat always has.
	facts := walkFacts{FilesCapped: []unreadListing{{SHA: "h1"}, {SHA: "h2"}}}
	if facts.complete() {
		t.Fatalf("a walk with a capped file listing reports itself complete")
	}
	if s := facts.shortfall("o", "r"); !strings.Contains(s, "maximum 3000 files") || !strings.HasSuffix(s, "(h1, h2)") {
		t.Fatalf("shortfall does not name the cap and the bare shas: %q", s)
	}
	if (walkFacts{FilesUnknown: []unreadListing{{SHA: "c1"}}}).complete() {
		t.Fatalf("a walk with a file listing GitHub answered 422 for reports itself complete")
	}
}

// TestSinceTagPackagesUnlistedFilesAreAnIncompleteWalk: GitHub answering 422
// for a squash-arm inner commit's file listing is a capped listing with
// nothing listed (DESIGN §4.1, t-esm5) — neither API lag nor a raw API
// failure. bump and notes report and warn naming the commit; release refuses
// the walk at 4 with the shortfall and its own remedy. Before the fix all
// three died at 4 on the raw `github: GET …/commits/c1: 422`. A refusal
// attribution would hand down over the empty listing is withheld, so the
// unscoped ~ is carried nowhere; a scope naming a package still carries it
// there (rule 2), the answer a truncated listing already lets stand (mutation
// row unlisted-commit-files-die-as-a-raw-api-error).
func TestSinceTagPackagesUnlistedFilesAreAnIncompleteWalk(t *testing.T) {
	for _, tc := range []struct {
		name         string
		message      string
		curryLevel   string
		curryCommits int
	}{
		{"unscoped, carried nowhere", ":bug:~ swap an ingredient", "none", 0},
		{"scoped, carried by its scope", ":bug:(curry)~ swap an ingredient", "patch", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			merge, routes := squashAcrossLines(t, dir, 7)
			routes[pullCommitsPath(7)] = `[` +
				apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `,` +
				apiCommit("c1", "akira-toriyama", tc.message) + `]`
			routes[commitFilesPath("c1")] = apiUnknownSHA
			usePR(t, dryServer(t, routes))
			t.Chdir(dir)

			unlisted := "commit c1 in pull request #7: GitHub answered 422 for its file listing"
			code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
			if code != 0 {
				t.Fatalf("bump --since-tag exited %d, want 0 (bump reports an incomplete walk, it does not act)\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			res := decodePackagesVerdict(t, stdout)
			h, c := res.Packages[0], res.Packages[1]
			if h.Level != "minor" || len(h.Commits) != 1 {
				t.Fatalf("haiku = %+v, want minor over h1 — its listing was whole", h)
			}
			if c.Level != tc.curryLevel || len(c.Commits) != tc.curryCommits {
				t.Fatalf("curry = %+v, want %s over %d commit(s)", c, tc.curryLevel, tc.curryCommits)
			}
			if !strings.Contains(stderr, "::warning::glyph: "+unlisted) || strings.Contains(stderr, "github: GET") {
				t.Fatalf("bump must warn about the unlisted files, never hand back the raw API error:\n%s", stderr)
			}
			// The withheld refusal is worded over what was read — nothing —
			// never as a fact about the commit: "touches no file" beside "GitHub
			// did not list the commit's whole diff" is two answers in one line.
			withheld := "over the files GitHub listed, attribution would refuse it (no file of this commit was read, so no package's tree can carry its sigil ~: "
			if strings.Contains(stderr, withheld) != (tc.curryCommits == 0) || strings.Contains(stderr, "touches no file") {
				t.Fatalf("an unscoped commit's withheld refusal must say no file was read, and a scoped one is not refused:\n%s", stderr)
			}

			code, stdout, stderr = runGlyph(t, "notes", "--since-tag")
			if code != 0 {
				t.Fatalf("notes --since-tag exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, "add a season") || strings.Contains(stdout, "swap an ingredient") != (tc.curryCommits == 1) {
				t.Fatalf("notes must carry h1, and c1 only where its scope carried it:\n%s", stdout)
			}
			if !strings.Contains(stderr, "::warning::glyph: "+unlisted) {
				t.Fatalf("notes must warn about the unlisted files:\n%s", stderr)
			}

			code, _, stderr = runGlyph(t, "release", "--dry-run", "--json")
			if code != 4 {
				t.Fatalf("release exited %d, want 4 (an incomplete walk)\nstderr: %s", code, stderr)
			}
			env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
			if env.Code != 4 {
				t.Fatalf("envelope code = %d, want 4", env.Code)
			}
			for _, want := range []string{
				"did not read",
				"GitHub answered 422 for the file listing of 1 commit(s)",
				"c1 in pull request #7",
				"re-run once GitHub lists its files",
				"a haiku/ tag at or past " + merge[:7],
				"a curry/ tag at or past " + merge[:7],
			} {
				if !strings.Contains(env.Message, want) {
					t.Errorf("the refusal must name the unlisted files and their remedy (missing %q):\n%s", want, env.Message)
				}
			}
			if strings.Contains(env.Message, "github: GET") {
				t.Errorf("the refusal is the raw API error, not the walk's shortfall:\n%s", env.Message)
			}
		})
	}
}

// laterPage422Server is releaseServer with one commit's file listing paged:
// page 1 answers page1 with a rel="next" link, and page 2 answers GitHub's
// 422 — a stand-in only, since no live trigger for a 422 past the first page
// is known.
func laterPage422Server(t *testing.T, routes map[string]string, sha, page1 string, writes *[]apiWrite) *httptest.Server {
	t.Helper()
	h := releaseHandler(t, routes, `[]`, writes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != commitFilesPath(sha) {
			h(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"No commit found for SHA"}`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=2>; rel="next"`, r.Host, r.URL.Path))
		fmt.Fprint(w, page1)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSinceTagPackagesLaterPage422KeepsTheListedFiles: a 422 on page 2 of a
// commit's file listing keeps the files page 1 listed — the capped
// listing's rule (DESIGN §4.1): attribution runs over what GitHub did list,
// the listing is recorded as unread, and only the refusal that needs the
// whole listing is withheld. Page 1 lists curry/curry.go for the unscoped
// ~, so bump moves curry and warns, and release still refuses the walk at 4.
// Thrown away, the listed file left curry at none and the warning claimed
// attribution over "the files GitHub listed" found no package (mutation row
// later-page-422-discards-the-listed-files).
func TestSinceTagPackagesLaterPage422KeepsTheListedFiles(t *testing.T) {
	dir, _ := packagesRepo(t)
	merge, routes := squashAcrossLines(t, dir, 7)
	delete(routes, commitFilesPath("c1"))
	var writes []apiWrite
	usePR(t, laterPage422Server(t, routes, "c1", apiFiles("curry/curry.go"), &writes))
	t.Chdir(dir)

	unlisted := "::warning::glyph: commit c1 in pull request #7: GitHub answered 422 for its file listing"
	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Level != "minor" || len(h.Commits) != 1 {
		t.Fatalf("haiku = %+v, want minor over h1", h)
	}
	if c.Level != "patch" || len(c.Commits) != 1 || c.Commits[0].SHA != "c1" {
		t.Fatalf("curry = %+v, want patch over c1 — page 1 listed curry/curry.go", c)
	}
	if !strings.Contains(stderr, unlisted) || strings.Contains(stderr, "attribution would refuse") {
		t.Fatalf("bump must warn about the unread listing and refuse nothing over the file it did list:\n%s", stderr)
	}

	code, stdout, stderr = runGlyph(t, "notes", "--since-tag")
	if code != 0 || !strings.Contains(stdout, "swap an ingredient") || !strings.Contains(stderr, unlisted) {
		t.Fatalf("notes --since-tag exited %d, want 0 carrying c1 under curry with the warning\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	code, _, stderr = runGlyph(t, "release", "--dry-run", "--json")
	if code != 4 {
		t.Fatalf("release exited %d, want 4 (the listing is still unread)\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	for _, want := range []string{"GitHub answered 422 for the file listing of 1 commit(s)", "c1 in pull request #7", "a curry/ tag at or past " + merge[:7]} {
		if env.Code != 4 || !strings.Contains(env.Message, want) {
			t.Errorf("the refusal must be the unread listing at 4 (code %d, missing %q):\n%s", env.Code, want, env.Message)
		}
	}
	if len(writes) != 0 {
		t.Errorf("a dry run wrote to the API: %+v", writes)
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
	touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
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
// diff — a shared-only ^ and a scope contradicting the tree are findings; a
// shared-only =, a scope-carried ^ and a commit under a package are clean.
func TestLintRangePackagesJudgesTheDiff(t *testing.T) {
	dir, base := packagesRepo(t)
	shared := touch(t, dir, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
	contra := touch(t, dir, "akira-toriyama", ":bug:(haiku)~ swap an ingredient", "curry/curry.go")
	touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	touch(t, dir, "akira-toriyama", ":sparkles:(curry)^ describe the ingredient API", "README.md")
	touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "2 commit-convention violation(s)") {
		t.Fatalf("want exactly the two attribution findings:\n%s", stderr)
	}
	for _, want := range []string{shared[:7], "its files (go.work) belong to no declared package", contra[:7], "names a package this commit does not touch"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("findings are missing %q:\n%s", want, stderr)
		}
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
	testGit(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	touch(t, dir, "akira-toriyama", ":memo:= reword a line", "haiku/haiku.go")
	testGit(t, dir, "akira-toriyama", "switch", "-q", "main")
	testGit(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", message, "side")
	return testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestLintRangePackagesNoFileCommitUnderARootPackage: a commit that shows the
// tree no file — an empty commit, a claimed merge commit — has no carrier but
// its scope, a root package declared or not (DESIGN §4.1, t-n5tw 3): the root
// package is a claim on files. Each finding says which of the two it is and
// names the root's scope among the escapes; the same empty commit scoped
// (core) is clean, which is the escape taken. This is lint's arm of the
// refusal's wording (cli's attribute): the merge sentence is read here, and
// mutation row lint-refuses-a-merge-commit-as-touching-no-file hands it no commit.
func TestLintRangePackagesNoFileCommitUnderARootPackage(t *testing.T) {
	dir := packagesRepoWith(t, rootedConfig, map[string]string{"haiku/haiku.go": "package haiku\n"})
	base := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	empty := touch(t, dir, "akira-toriyama", ":bookmark:~ cut a release")
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

	scoped := touch(t, dir, "akira-toriyama", ":bookmark:(core)~ cut a release")
	if code, stdout, stderr := runGlyph(t, "lint", "--range", scoped+"~1.."+scoped); code != 0 {
		t.Fatalf("an empty (core)~ exited %d, want 0 — the scope is the escape the finding names\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if code, stdout, stderr := runGlyph(t, "bump", "--range", scoped+"~1.."+scoped); code != 0 || stdout != "v0.0.1\n" {
		t.Fatalf("bump over the empty (core)~ exited %d with %q, want 0 and the root line alone at v0.0.1\nstderr: %s", code, stdout, stderr)
	}
}

// TestPackagesWalkRefusesAClaimedMergeInItsOwnWords is the walk's arm of the
// refusal's wording: partitionLines reaches attribution through cli's
// attribute, so a claimed merge commit is refused as a merge commit — its diff
// never read — with the wedge per line, where the first cut said it "touches
// no declared package" of a merge that touched haiku/. A haiku scope carries
// it (rule 2). Mutation row walk-refuses-a-merge-commit-as-touching-no-file
// hands the helper no commit.
func TestPackagesWalkRefusesAClaimedMergeInItsOwnWords(t *testing.T) {
	dir, base := packagesRepo(t)
	merge := claimedMerge(t, dir, ":twisted_rightwards_arrows:~ merge the side branch")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("bump --range over a claimed ~ merge exited %d, want 3\nstderr: %s", code, stderr)
	}
	for _, want := range []string{
		"commit " + merge[:7] + ": this merge commit's own diff is never read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry), or write = so it moves no line",
		"a haiku/ tag at or past " + merge[:7],
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, stderr)
		}
	}

	testGit(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", ":twisted_rightwards_arrows:(haiku)~ merge the side branch")
	if code, stdout, stderr := runGlyph(t, "bump", "--range", base+"..HEAD"); code != 0 || stdout != "haiku/v0.1.1\n" {
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
	touch(t, dir, "akira-toriyama", ":memo:= add a README", "README.md")
	testGit(t, dir, "akira-toriyama", "revert", "--no-edit", "HEAD")
	t.Chdir(dir)
	lint := func(t *testing.T) (int, string) {
		t.Helper()
		code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
		return code, stderr
	}
	reword := func(t *testing.T, message string) {
		t.Helper()
		testGit(t, dir, "akira-toriyama", "commit", "-q", "--amend", "-m", message)
	}

	code, stderr := lint(t)
	if code != 3 {
		t.Fatalf("a raw revert of a shared-only commit exited %d, want 3\nstderr: %s", code, stderr)
	}
	want := `its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: ` +
		`patterns[1], which claimed this message, fixes the sigil at ~ and captures no scope — ` +
		`reword it so another pattern claims it, with a scope naming the line it moves (one of haiku, curry) or as = so it moves no line, ` +
		`or declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." declares the root package, which holds every file no other package claims)`
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
	appendTo(t, dir, "glyph.toml", "\n[[packages]]\npath = \".\"\nname = \"core\"\n")
	if code, stderr := lint(t); code != 0 {
		t.Errorf("with the root package declared the raw revert exited %d, want 0\nstderr: %s", code, stderr)
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
	writeFile(t, dir, "glyph.toml", "schema = 1\n\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n\n[note]\nline = '- $subject'\n\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \".\"\n")
	testGit(t, dir, "akira-toriyama", "add", ".")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare the lines")
	empty := touch(t, dir, "akira-toriyama", ":bookmark:~ cut a release")
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
	testGit(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--amend", "-m", ":bookmark:= cut a release")
	if code, _, stderr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD"); code != 0 {
		t.Fatalf("the = the finding names exited %d, want 0\nstderr: %s", code, stderr)
	}
}

// TestPackagesNameNoScopeCanSpellIsUsage: a package name the shipped grammar
// cannot spell is a config that does not load — exit 2 on every verdict
// command, the commit-msg hook's --message included — never the 3 whose one
// scope escape cannot be written. Measured before the rule (2026-09-29):
// lint --range refused the shared-only ~ offering "(one of my_lib, other)",
// and `:memo:(my_lib)~` then matched none of the patterns at every gate.
func TestPackagesNameNoScopeCanSpellIsUsage(t *testing.T) {
	dir := packagesRepoWith(t, "\n[[packages]]\npath = \"my_lib\"\n\n[[packages]]\npath = \"other\"\n", map[string]string{"my_lib/a.go": "package a\n", "other/b.go": "package b\n"})
	touch(t, dir, "akira-toriyama", ":memo:~ fix the readme", "README.md")
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

// TestPrePushPackagesInheritsAttribution: the pre-push hook goes through the
// same lintRaws, so a shared-only ^ is blocked before it reaches the default
// branch — the one gate that can still refuse it while the commit is
// rewritable.
func TestPrePushPackagesInheritsAttribution(t *testing.T) {
	work, _ := testClone(t)
	appendTo(t, work, "glyph.toml", packagesConfig)
	writeFile(t, work, "haiku/haiku.go", "package haiku\n")
	writeFile(t, work, "curry/curry.go", "package curry\n")
	testutil.Git(t, work, "akira-toriyama", "add", ".")
	testutil.Git(t, work, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare two lines")
	testutil.Git(t, work, "akira-toriyama", "push", "-q", "origin", "main")
	head := touch(t, work, "akira-toriyama", ":sparkles:^ add a workspace file", "go.work")
	t.Chdir(work)

	setStdin(t, "refs/heads/main "+head+" refs/heads/main "+rev(t, work, "origin/main")+"\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
	if code != 3 {
		t.Fatalf("a shared-only ^ reaching the default branch exited %d, want 3\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "its files (go.work) belong to no declared package") {
		t.Fatalf("the blocking envelope must carry the attribution finding:\n%s", stderr)
	}
}

// TestSinceTagPackagesUntaggedLineWalksTheWholeHistory: a package with no tag
// of its own baselines at nothing — the whole history is walked, under the
// single line's cap, and the warning names the remedy §4.1 gives (cut
// <path>/v0.0.0 at the commit before the line's first change).
func TestSinceTagPackagesUntaggedLineWalksTheWholeHistory(t *testing.T) {
	dir := testutil.NewRepo(t)
	appendTo(t, dir, "glyph.toml", packagesConfig)
	writeFile(t, dir, "haiku/haiku.go", "package haiku\n")
	writeFile(t, dir, "curry/curry.go", "package curry\n")
	root := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "add", ".")
	testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= declare two lines")
	declared := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testGit(t, dir, "akira-toriyama", "tag", "haiku/v0.1.0")
	sha := touch(t, dir, "akira-toriyama", ":bug:~ swap an ingredient", "curry/curry.go")
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(root):     `[]`,
		commitPullsPath(declared): `[]`,
		commitPullsPath(sha):      `[]`,
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "no version tag on the curry/ line(s) in HEAD's history") || !strings.Contains(stderr, "cut curry/v0.0.0") {
		t.Fatalf("the whole-history warning must name the untagged line and its remedy:\n%s", stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	h, c := res.Packages[0], res.Packages[1]
	if h.Level != "none" {
		t.Fatalf("haiku = %+v, want none (its only change is under haiku/v0.1.0)", h)
	}
	if c.Current != "v0.0.0" || c.Next != "v0.0.1" || len(c.Commits) != 2 {
		t.Fatalf("curry = %+v, want v0.0.0 → v0.0.1 over the declaring commit and the swap", c)
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
	touch(t, dir, "akira-toriyama", ":sparkles:^ add a season", "haiku/season.go")
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
