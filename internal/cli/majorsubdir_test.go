package cli

import (
	"strings"
	"testing"
)

// This file pins the major version subdirectory rule (DESIGN §4.1, "The tag
// line"; t-z9d3): a package at pubsub/v2 is versioned on the pubsub/ prefix
// as pubsub/v2.x.y — Go's rule, measured on google-cloud-go and etcd — so
// two lines share one prefix and every reader of tags (walk base, floor,
// drafts, --since-tag selection, the step) tells them apart by the major.

// majorRepo is the two-lines-one-prefix fixture: pubsub (v1) and pubsub/v2,
// each with a file, and the tags pubsub/v1.9.1 and pubsub/v2.7.0 on the
// declaring commit. extra is appended to [[packages]] as further entries.
func majorRepo(t *testing.T, extra string) (dir, base string) {
	t.Helper()
	dir = packagesRepoWith(t, "\n[[packages]]\npath = \"pubsub\"\n\n[[packages]]\npath = \"pubsub/v2\"\n"+extra, map[string]string{
		"pubsub/pubsub.go": "package pubsub\n", "pubsub/v2/pubsub.go": "package pubsub\n",
	})
	testGit(t, dir, "akira-toriyama", "tag", "pubsub/v1.9.1")
	testGit(t, dir, "akira-toriyama", "tag", "pubsub/v2.7.0")
	return dir, testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")
}

// TestBumpMajorSubdirectoryLinesShareThePrefix: a fix under pubsub/ steps
// the v1 line from pubsub/v1.9.1, a feature under pubsub/v2/ steps the v2
// line from pubsub/v2.7.0 — each from its own highest tag ON ITS MAJOR,
// neither reading the other's (mutation rows
// major-subdirectory-kept-in-the-tag-prefix, line-reads-tags-of-every-major).
func TestBumpMajorSubdirectoryLinesShareThePrefix(t *testing.T) {
	dir, _ := majorRepo(t, "")
	s1 := touch(t, dir, "akira-toriyama", ":bug:~ fix the v1 client", "pubsub/client.go")
	s2 := touch(t, dir, "akira-toriyama", ":sparkles:^ add keep alive support", "pubsub/v2/keepalive.go")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(s1): `[]`, commitPullsPath(s2): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag", "--json")
	if code != 0 {
		t.Fatalf("bump --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	res := decodePackagesVerdict(t, stdout)
	if len(res.Packages) != 2 {
		t.Fatalf("packages = %+v, want two lines", res.Packages)
	}
	v1, v2 := res.Packages[0], res.Packages[1]
	if v1.Path != "pubsub" || v1.Current != "v1.9.1" || v1.Next != "v1.9.2" || len(v1.Commits) != 1 || v1.Commits[0].SHA != s1 {
		t.Errorf("pubsub = %+v, want v1.9.1 → v1.9.2 from its own commit", v1)
	}
	if v2.Path != "pubsub/v2" || v2.Current != "v2.7.0" || v2.Next != "v2.8.0" || len(v2.Commits) != 1 || v2.Commits[0].SHA != s2 {
		t.Errorf("pubsub/v2 = %+v, want v2.7.0 → v2.8.0 from its own commit", v2)
	}
	code, stdout, _ = runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "pubsub/v1.9.2\npubsub/v2.8.0\n" {
		t.Fatalf("bump stdout = %q (exit %d), want the next tag of each line on ONE prefix", stdout, code)
	}
}

// TestBumpMajorSubdirectoryAloneReadsItsMajor is the measured defect: with
// only pubsub/v2 declared, its current is pubsub/v2.7.0 — not v0.0.0 on a
// pubsub/v2/ prefix nothing tags (the first cut), and not the v1 tag beside
// it — and a fix steps to pubsub/v2.7.1.
func TestBumpMajorSubdirectoryAloneReadsItsMajor(t *testing.T) {
	dir := packagesRepoWith(t, "\n[[packages]]\npath = \"pubsub/v2\"\n", map[string]string{"pubsub/pubsub.go": "package pubsub\n", "pubsub/v2/pubsub.go": "package pubsub\n"})
	testGit(t, dir, "akira-toriyama", "tag", "pubsub/v1.9.1")
	testGit(t, dir, "akira-toriyama", "tag", "pubsub/v2.7.0")
	sha := touch(t, dir, "akira-toriyama", ":bug:~ fix the exporter", "pubsub/v2/exporter.go")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || stdout != "pubsub/v2.7.1\n" {
		t.Fatalf("bump = %q (exit %d), want pubsub/v2.7.1\nstderr: %s", stdout, code, stderr)
	}
}

// TestReleaseMajorSubdirectoryFloorAndDraftsPerMajor: the published floor
// and the managed drafts are per line even on a shared prefix — a
// published pubsub/v2.7.0 is no floor under the v1 line's v1.9.2, and the
// v2 line's rolling draft is updated by the v2 line and never deleted as
// the v1 line's stray (mutation row line-reads-tags-of-every-major).
func TestReleaseMajorSubdirectoryFloorAndDraftsPerMajor(t *testing.T) {
	dir, _ := majorRepo(t, "")
	s1 := touch(t, dir, "akira-toriyama", ":bug:~ fix the v1 client", "pubsub/client.go")
	s2 := touch(t, dir, "akira-toriyama", ":sparkles:^ add keep alive support", "pubsub/v2/keepalive.go")
	var writes []apiWrite
	releases := `[` + publishedJSON(1, "pubsub/v2.7.0") + `,` + publishedJSON(2, "pubsub/v1.9.1") + `,` + draftJSON(42, "pubsub/v2.7.1") + `]`
	usePR(t, releaseServer(t, map[string]string{commitPullsPath(s1): `[]`, commitPullsPath(s2): `[]`}, releases, &writes))
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("release exited %d, want 0 (the v2 floor is not the v1 line's)\nstderr: %s", code, stderr)
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %+v, want one POST (pubsub/v1.9.2) and one PATCH (the v2 draft retagged) and no DELETE", writes)
	}
	if writes[0].method != "POST" || writes[0].body["tag_name"] != "pubsub/v1.9.2" {
		t.Errorf("write 0 = %+v, want the v1 line's draft created as pubsub/v1.9.2", writes[0])
	}
	if writes[1].method != "PATCH" || !strings.HasSuffix(writes[1].path, "/42") || writes[1].body["tag_name"] != "pubsub/v2.8.0" {
		t.Errorf("write 1 = %+v, want the v2 line's draft 42 retagged to pubsub/v2.8.0", writes[1])
	}
}

// TestLockedLineRefusesAMajorStep: a `!` under pubsub/v2 would tag
// pubsub/v3.0.0, a version off the v2 line that no module claims; a `!`
// under pubsub/ beside a declared pubsub/v2 would tag the v2 line's own
// floor. Both refuse at 3, on bump and on preview alike; the control is the
// same `!` on a pubsub/ line with no locked sibling, which steps to v2.0.0
// as every free line always has (mutation row
// locked-line-steps-past-its-major).
func TestLockedLineRefusesAMajorStep(t *testing.T) {
	for name, tc := range map[string]struct {
		path, want string
	}{
		"locked line":  {"pubsub/v2/breaking.go", "off its line"},
		"free sibling": {"pubsub/breaking.go", "declares as its own package"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := majorRepo(t, "")
			sha := touch(t, dir, "akira-toriyama", ":boom:! drop the old API", tc.path)
			usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
			t.Chdir(dir)

			code, _, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 3 || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "DESIGN §4.1") {
				t.Fatalf("bump exited %d, want 3 naming the line it cannot step onto\nstderr: %s", code, stderr)
			}
		})
	}
	t.Run("control: no locked sibling, the free line steps its major", func(t *testing.T) {
		dir := packagesRepoWith(t, "\n[[packages]]\npath = \"pubsub\"\n", map[string]string{"pubsub/pubsub.go": "package pubsub\n"})
		testGit(t, dir, "akira-toriyama", "tag", "pubsub/v1.9.1")
		sha := touch(t, dir, "akira-toriyama", ":boom:! drop the old API", "pubsub/breaking.go")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(sha): `[]`}))
		t.Chdir(dir)

		code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
		if code != 0 || stdout != "pubsub/v2.0.0\n" {
			t.Fatalf("bump = %q (exit %d), want pubsub/v2.0.0\nstderr: %s", stdout, code, stderr)
		}
	})
	t.Run("preview says what CI will say", func(t *testing.T) {
		dir, _ := majorRepo(t, "")
		usePR(t, walkServer(t, map[string]string{
			pullCommitsPath(9):    `[` + apiCommit("b1", "akira-toriyama", ":boom:! drop the old API") + `]`,
			commitFilesPath("b1"): apiFiles("pubsub/v2/breaking.go"),
		}))
		t.Chdir(dir)
		code, _, stderr := runGlyph(t, "preview", "--pr", "9")
		if code != 3 || !strings.Contains(stderr, "off its line") {
			t.Fatalf("preview exited %d, want 3\nstderr: %s", code, stderr)
		}
	})
}

// TestLockedLineFirstReleaseIsItsMajor: a pubsub/v3 with no tag yet
// releases as pubsub/v3.0.0 whatever the level — the line holds nothing
// below its major — and the untagged remedy names that tag.
func TestLockedLineFirstReleaseIsItsMajor(t *testing.T) {
	dir, _ := majorRepo(t, "\n[[packages]]\npath = \"pubsub/v3\"\n")
	touch(t, dir, "akira-toriyama", ":bug:~ start the v3 client", "pubsub/v3/client.go")
	// An untagged line walks the whole history, so every commit is asked about.
	routes := map[string]string{}
	for sha := range strings.FieldsSeq(testGit(t, dir, "akira-toriyama", "rev-list", "HEAD")) {
		routes[commitPullsPath(sha)] = `[]`
	}
	usePR(t, walkServer(t, routes))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 0 || !strings.Contains(stdout, "pubsub/v3.0.0\n") {
		t.Fatalf("bump = %q (exit %d), want pubsub/v3.0.0 among the moving lines\nstderr: %s", stdout, code, stderr)
	}
	if !strings.Contains(stderr, "pubsub/v3.*") || !strings.Contains(stderr, "cut pubsub/v3.0.0") {
		t.Errorf("the untagged line's remedy must name its own major's floor:\n%s", stderr)
	}
}

// TestSinceTagSelectsTheLineByMajor: a tag names a line, and on a shared
// prefix the version's major says which — pubsub/v2.7.0 selects pubsub/v2
// alone, pubsub/v1.9.1 selects pubsub alone; --current off the selected
// line's major is the caller's mistake (2).
func TestSinceTagSelectsTheLineByMajor(t *testing.T) {
	dir, _ := majorRepo(t, "")
	s1 := touch(t, dir, "akira-toriyama", ":bug:~ fix the v1 client", "pubsub/client.go")
	s2 := touch(t, dir, "akira-toriyama", ":sparkles:^ add keep alive support", "pubsub/v2/keepalive.go")
	usePR(t, walkServer(t, map[string]string{commitPullsPath(s1): `[]`, commitPullsPath(s2): `[]`}))
	t.Chdir(dir)

	for tag, want := range map[string]string{"pubsub/v2.7.0": "pubsub/v2", "pubsub/v1.9.1": "pubsub", "below:pubsub/v2.7.1": "pubsub/v2"} {
		code, stdout, stderr := runGlyph(t, "bump", "--since-tag="+tag, "--json")
		if code != 0 {
			t.Fatalf("--since-tag=%s exited %d\nstderr: %s", tag, code, stderr)
		}
		res := decodePackagesVerdict(t, stdout)
		if len(res.Packages) != 1 || res.Packages[0].Path != want {
			t.Errorf("--since-tag=%s selected %+v, want the %s line alone", tag, res.Packages, want)
		}
	}
	code, _, stderr := runGlyph(t, "bump", "--since-tag=pubsub/v2.7.0", "--current", "v1.0.0")
	if code != 2 || !strings.Contains(stderr, "not on the pubsub/v2.* line") {
		t.Fatalf("--current v1.0.0 on the v2 line exited %d, want 2\nstderr: %s", code, stderr)
	}
}
