package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/github"
)

// This file pins what a file listing GitHub truncated may decide (DESIGN
// §4.1, t-c6r5 (A) and t-ft7p): a refusal attribution would hand down over
// the files it could see is withheld — the package past the cap may be the
// very one it says is missing or named — and the walk's own incompleteness
// answers, at 4 for a writing command, never at 3, the code the fleet's
// gates and the installed hooks read as "the author wrote a bad message".
// The exact integers are asserted, never truthiness (CLAUDE.md).

// listing renders GET commits/{sha}'s files array: n modified files, each
// path from pathf(i).
func listing(n int, pathf func(i int) string) string {
	entries := make([]string, 0, n)
	for i := range n {
		entries = append(entries, fmt.Sprintf(`{"filename":%q,"status":"modified"}`, pathf(i)))
	}
	return `{"files":[` + strings.Join(entries, ",") + `]}`
}

func underNoPackage(i int) string { return fmt.Sprintf("docs/f%05d.md", i) }
func underHaiku(i int) string     { return fmt.Sprintf("haiku/gen/f%d.go", i) }

// TestReleasePackagesCappedRefusalIsNotTheGateCode: a capped inner commit
// whose visible files attribution would refuse — under no package with a ^,
// or with a scope naming a package the visible files do not touch — makes
// release refuse the walk as INCOMPLETE (4), not the commit as a violation
// (3); bump, which only reports, answers 0 with the warning. The control is
// the same commit one file short of the cap: the listing is whole, and the
// refusal stands at 3 (mutation row capped-listing-refusal-is-the-gate-code).
func TestReleasePackagesCappedRefusalIsNotTheGateCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		files   string
	}{
		{"no carrier", ":sparkles:^ add a season", listing(github.CommitFilesCap, underNoPackage)},
		{"contradiction", ":sparkles:(curry)^ add a season", listing(github.CommitFilesCap, underHaiku)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			routes := cappedPull(t, dir, tc.message, tc.files)
			usePR(t, dryServer(t, routes))
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "release", "--dry-run")
			if code != 4 {
				t.Fatalf("release exited %d, want 4 (an incomplete walk, not a convention violation)\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			for _, want := range []string{"did not read", "maximum 3000 files", "h1"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal must name the cap as the shortfall (missing %q):\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, "cannot be rewritten") || strings.Contains(stderr, `"code":3`) {
				t.Errorf("the wedge remedy was handed down over a truncated listing:\n%s", stderr)
			}
			if !strings.Contains(stderr, "not a verdict") {
				t.Errorf("the withheld refusal must be warned about:\n%s", stderr)
			}

			// bump reports and does not act: the commit is carried nowhere,
			// so every line folds to none — exit 1, the nothing-to-release
			// answer, with both warnings on stderr and never the gate code.
			code, stdout, stderr = runGlyph(t, "bump", "--since-tag", "--json")
			if code != 1 {
				t.Fatalf("bump --since-tag exited %d, want 1 (every line none: the commit is carried nowhere)\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "GitHub lists no more than that") || !strings.Contains(stderr, "not a verdict") {
				t.Errorf("bump must warn about the cap and the withheld refusal:\n%s", stderr)
			}
		})
	}

	t.Run("control: one file short of the cap, the refusal stands", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		routes := cappedPull(t, dir, ":sparkles:^ add a season", listing(github.CommitFilesCap-1, underNoPackage))
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, "release", "--dry-run")
		if code != 3 || !strings.Contains(stderr, "touches no declared package") {
			t.Fatalf("release exited %d, want 3 with the attribution refusal over a whole listing\nstderr: %s", code, stderr)
		}
	})
}

// TestPreviewPackagesCappedRefusalIsNotTheGateCode is the same rule on the
// pull's own side, where there is no exit to refuse with and only a body to
// say it in: a capped commit attribution would refuse is attributed to no
// line, preview exits 0, and the comment carries the PR-side INCOMPLETE
// caveat — in the "moves nothing" body when nothing else was carried, under
// the headlines when a capped commit still reached a line. The control is
// the whole listing, refused at 3 as before (mutation row
// capped-listing-refusal-is-the-gate-code).
func TestPreviewPackagesCappedRefusalIsNotTheGateCode(t *testing.T) {
	caveat := "> This PR's own side of this fold is INCOMPLETE: 1 commit(s) returned the maximum 3000 files"
	t.Run("no carrier, carried nowhere", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("h1", "akira-toriyama", ":sparkles:^ add a season") + `]`,
			commitFilesPath("h1"): listing(github.CommitFilesCap, underNoPackage),
		}))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		res := decodePreviewLines(t, stdout)
		if len(res.Packages) != 0 {
			t.Fatalf("packages = %+v, want none: a commit refused over a truncated listing is attributed to no line", res.Packages)
		}
		if !strings.Contains(res.Body, "moves nothing") || !strings.Contains(res.Body, caveat) || !strings.Contains(res.Body, "(h1)") {
			t.Fatalf("the moves-nothing body must carry the PR-side caveat naming the commit:\n%s", res.Body)
		}
		if !strings.Contains(stderr, "not a verdict") {
			t.Errorf("the withheld refusal must be warned about:\n%s", stderr)
		}
	})

	t.Run("attributable under the cap, the line is still qualified", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("h1", "akira-toriyama", ":sparkles:(haiku)^ add a season") + `]`,
			commitFilesPath("h1"): listing(github.CommitFilesCap, underHaiku),
		}))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "preview", "--pr", "9", "--json")
		if code != 0 {
			t.Fatalf("preview exited %d, want 0\nstderr: %s", code, stderr)
		}
		res := decodePreviewLines(t, stdout)
		if len(res.Packages) != 1 || res.Packages[0].Path != "haiku" || res.Packages[0].PR != "minor" {
			t.Fatalf("packages = %+v, want haiku minor from the files the listing did hold", res.Packages)
		}
		headline := strings.Index(res.Body, "**haiku**")
		warning := strings.Index(res.Body, caveat)
		table := strings.Index(res.Body, "### haiku")
		if headline < 0 || warning < 0 || table < 0 || headline >= warning || warning >= table {
			t.Fatalf("the PR-side caveat must sit under the headlines and above the tables (a line curry-side past the cap is missing from them):\n%s", res.Body)
		}
	})

	t.Run("control: one file short of the cap, the refusal stands", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("h1", "akira-toriyama", ":sparkles:^ add a season") + `]`,
			commitFilesPath("h1"): listing(github.CommitFilesCap-1, underNoPackage),
		}))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, "preview", "--pr", "9")
		if code != 3 || !strings.Contains(stderr, "touches no declared package") {
			t.Fatalf("preview exited %d, want 3 with the attribution refusal over a whole listing\nstderr: %s", code, stderr)
		}
	})
}
