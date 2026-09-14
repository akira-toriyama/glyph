package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v3/internal/github"
)

// This file pins t-ft7p: the files cap is GitHub's count of listed entries,
// and a rename is one entry the adapter returns under two names, so a whole
// listing of renames is whole and the walk that read it is complete.

// renameListing renders n rename entries, each moving one file within
// haiku/ — one entry, two names.
func renameListing(n int) string {
	entries := make([]string, 0, n)
	for i := range n {
		entries = append(entries, fmt.Sprintf(`{"filename":"haiku/gen/f%d.go","previous_filename":"haiku/old/f%d.go","status":"renamed"}`, i, i))
	}
	return `{"files":[` + strings.Join(entries, ",") + `]}`
}

// cappedPull lands pull #7 on main as one squash whose single inner commit
// carries message and whose file listing is files — the shape the walk
// asks the API for, since the inner commit exists on no branch.
func cappedPull(t *testing.T, dir, message, files string) map[string]string {
	t.Helper()
	sha := touch(t, dir, "akira-toriyama", "Add a season (#7)", "haiku/season.go")
	return map[string]string{
		commitPullsPath(sha):  `[` + apiPullRef(7, "2026-09-10T00:00:00Z", sha) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("h1", "akira-toriyama", message) + `]`,
		commitFilesPath("h1"): files,
	}
}

// TestReleasePackagesRenamesDoNotFalselyCapTheWalk (t-ft7p): a whole listing
// of CommitFilesCap/2 renames is CommitFilesCap names and not one entry past
// what GitHub lists, so the walk is complete and release answers the line
// the renames moved. The control is the cap in rename entries, which is a
// truncated listing however it is counted (mutation row
// rename-second-name-counts-toward-the-files-cap).
func TestReleasePackagesRenamesDoNotFalselyCapTheWalk(t *testing.T) {
	dir, _ := packagesRepo(t)
	routes := cappedPull(t, dir, ":sparkles:(haiku)^ move the generated files", renameListing(github.CommitFilesCap/2))
	usePR(t, dryServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 {
		t.Fatalf("release exited %d, want 0: %d renames are a whole listing\nstdout: %s\nstderr: %s", code, github.CommitFilesCap/2, stdout, stderr)
	}
	if strings.Contains(stderr, "GitHub lists no more than that") {
		t.Fatalf("a whole listing of renames was reported capped:\n%s", stderr)
	}
	if !strings.Contains(stdout, "haiku/v0.2.0") {
		t.Fatalf("the renames under haiku/ must move haiku:\n%s", stdout)
	}

	t.Run("control: the cap in rename entries is truncated", func(t *testing.T) {
		dir, _ := packagesRepo(t)
		routes := cappedPull(t, dir, ":sparkles:(haiku)^ move the generated files", renameListing(github.CommitFilesCap))
		usePR(t, dryServer(t, routes))
		t.Chdir(dir)

		code, _, stderr := runGlyph(t, "release", "--dry-run")
		if code != 4 || !strings.Contains(stderr, "maximum 3000 files") {
			t.Fatalf("release exited %d, want 4 on a listing at the cap\nstderr: %s", code, stderr)
		}
	})
}
