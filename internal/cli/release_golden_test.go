package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// TestReleaseDryRunGolden pins the composed dry-run output — tag line, blank
// line, section order, the one-line --- separator, the verbatim footer — as
// bytes, the way the notes goldens pin Render. The compose tests around it
// assert fragments; this is the one place the WHOLE published surface is the
// assertion, so a stray blank line or a reordered section fails even when
// every fragment still matches. Deterministic because every SHA in the output
// is the fake API's fixed one and the tag is computed from the fixture.
//
// Regenerate with `go test ./internal/cli -run Golden -update` — and read the
// diff as the format spec it is before committing it (-update makes a
// rendering bug look intentional; the commit needs a Golden-change trailer).
func TestReleaseDryRunGolden(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("testdata", "release_dry_run.golden.md"))
	if err != nil {
		t.Fatal(err)
	}
	footer := filepath.Join(t.TempDir(), "install.md")
	if werr := os.WriteFile(footer, []byte("## Install\n\n`brew install glyph`\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}

	dir, _ := testRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	srv := dryServer(t, map[string]string{
		commitPullsPath(sha1): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha1) + `]`,
		commitPullsPath(sha2): `[` + apiPullRef(8, "2026-07-13T00:00:00Z", sha2) + `]`,
		pullCommitsPath(7):    `[` + apiCommit("a1", "akira-toriyama", ":sparkles:(ui) add a menu") + `]`,
		pullCommitsPath(8):    `[` + apiCommit("b1", "akira-toriyama", ":bug: fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "release", "--dry-run", "--footer-file", footer)
	if code != 0 {
		t.Fatalf("release --dry-run exited %d, want 0\nstderr: %s", code, stderr)
	}

	if *update {
		if werr := os.WriteFile(golden, []byte(stdout), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	want, rerr := os.ReadFile(golden)
	if rerr != nil {
		t.Fatalf("reading the golden (run with -update once to create it): %v", rerr)
	}
	if stdout != string(want) {
		t.Errorf("dry-run output does not match the golden spec\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}
