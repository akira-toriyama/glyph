package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/github"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// This file pins what a file listing GitHub did not give whole — capped,
// or cut by a 422 — records and may decide (DESIGN §4.1, t-c6r5 (A), t-ft7p
// and t-esm5; internal/walk/partition.go: listFiles, diffUnlisted): the
// files it did list are attributed, the listing is recorded as unread so
// the facts say incomplete, a refusal attribution would hand down over the
// files it could see is withheld — the package past the cap may be the
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
// or with a scope naming a package that owns none of the visible files — makes
// release refuse the walk as INCOMPLETE (4), not the commit as a violation
// (3); bump, which only reports, answers 0 with the warning. The control is
// the same commit one file short of the cap: the listing is whole, and the
// refusal stands at 3 (mutation row capped-listing-refusal-is-the-gate-code).
// The refusal's remedy is the wedge escape per line whose range holds the
// merge point: a re-run lists the same capped files, so "re-run the release"
// alone was a remedy nothing could satisfy (t-esm5; mutation row
// unread-listing-remedy-is-a-rerun).
func TestReleasePackagesCappedRefusalIsNotTheGateCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  string
		files    string
		withheld string // the refusal as the warning quotes it: over the files READ
	}{
		{"no carrier", ":sparkles:^ add a season", listing(github.CommitFilesCap, underNoPackage),
			"attribution would refuse it (the files read of it (docs/f00000.md, docs/f00001.md, docs/f00002.md and 2997 more) belong to no declared package, and its sigil ^ claims"},
		{"contradiction", ":sparkles:(curry)^ add a season", listing(github.CommitFilesCap, underHaiku),
			"attribution would refuse it (scope (curry) names a line the files read of this commit do not move: haiku/gen/f0.go belongs to haiku (haiku), and 2999 more files likewise — "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := packagesRepo(t)
			routes := cappedPull(t, dir, tc.message, tc.files)
			merge := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
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
			for _, want := range []string{"no re-run lists past the cap", "a haiku/ tag at or past " + merge[:7], "a curry/ tag at or past " + merge[:7]} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal must name the escape a re-run cannot replace (missing %q):\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, "cannot be rewritten") || strings.Contains(stderr, `"code":3`) {
				t.Errorf("the wedge remedy was handed down over a truncated listing:\n%s", stderr)
			}
			if !strings.Contains(stderr, "over the files GitHub listed, "+tc.withheld) || !strings.Contains(stderr, "not a verdict") {
				t.Errorf("the withheld refusal must be warned about, in terms of the files read (%q):\n%s", tc.withheld, stderr)
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
		if code != 3 || !strings.Contains(stderr, "its files (docs/f00000.md, docs/f00001.md, docs/f00002.md and 2996 more) belong to no declared package") {
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
		if !strings.Contains(stderr, "over the files GitHub listed, attribution would refuse it (the files read of it (docs/f00000.md, docs/f00001.md, docs/f00002.md and 2997 more) belong to no declared package") || !strings.Contains(stderr, "not a verdict") {
			t.Errorf("the withheld refusal must be warned about, in terms of the files read:\n%s", stderr)
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
		if code != 3 || !strings.Contains(stderr, "its files (docs/f00000.md, docs/f00001.md, docs/f00002.md and 2996 more) belong to no declared package") {
			t.Fatalf("preview exited %d, want 3 with the attribution refusal over a whole listing\nstderr: %s", code, stderr)
		}
	})
}

// TestWalkAndPreviewWarnAnUnreadListingInOneSentence: the walk and preview's
// pull side place a commit by its files through one helper (walk.PlaceByFiles),
// so one event — a listing GitHub answered 422 for or capped, and the refusal
// attribution would make over it, withheld — is warned of in one sentence by
// both: bump --since-tag over a squash-merged pull's inner commit, preview
// over the open pull that lists the same commit. Two copies said it in two
// wordings ("… missing from this preview" against "… from this verdict",
// "attributed to no line" against "carried nowhere, and the walk is
// incomplete").
func TestWalkAndPreviewWarnAnUnreadListingInOneSentence(t *testing.T) {
	const message = ":sparkles:^ add a season"
	// unreadWarnings keeps the warnings about commit h1, its pull spelled #N.
	unreadWarnings := func(stderr string, pull int) []string {
		var out []string
		for line := range strings.SplitSeq(stderr, "\n") {
			if strings.HasPrefix(line, "::warning::glyph: commit h1") {
				out = append(out, strings.ReplaceAll(line, fmt.Sprintf("pull request #%d", pull), "pull request #N"))
			}
		}
		return out
	}
	for _, tc := range []struct{ name, files string }{
		{"422", apiUnknownSHA},
		{"capped", listing(github.CommitFilesCap, underNoPackage)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			walkDir, _ := packagesRepo(t)
			routes := cappedPull(t, walkDir, message, tc.files)
			usePR(t, walkServer(t, routes))
			t.Chdir(walkDir)
			code, _, walkErr := runGlyph(t, "bump", "--since-tag")
			if code != 1 {
				t.Fatalf("bump --since-tag exited %d, want 1 (the commit is carried nowhere)\nstderr: %s", code, walkErr)
			}

			pullDir, _ := packagesRepo(t)
			usePR(t, walkServer(t, map[string]string{
				pullCommitsPath(9):    `[` + apiCommit("h1", "akira-toriyama", message) + `]`,
				commitFilesPath("h1"): tc.files,
			}))
			t.Chdir(pullDir)
			code, _, pullErr := runGlyph(t, "preview", "--pr", "9")
			if code != 0 {
				t.Fatalf("preview exited %d, want 0\nstderr: %s", code, pullErr)
			}

			walked, pulled := unreadWarnings(walkErr, 7), unreadWarnings(pullErr, 9)
			if len(walked) != 2 || !slices.Equal(walked, pulled) {
				t.Errorf("the walk and preview warn of one unread listing differently:\n walk:    %q\n preview: %q", walked, pulled)
			}
		})
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
