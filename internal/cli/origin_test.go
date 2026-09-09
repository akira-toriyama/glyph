package cli

import (
	"strings"
	"testing"
)

// TestSplitRemoteURL pins the three spellings a GitHub clone's origin comes
// in, and that anything else is "not a coordinate" rather than a guess.
func TestSplitRemoteURL(t *testing.T) {
	cases := []struct {
		raw        string
		host, path string
		ok         bool
	}{
		{"https://github.com/akira-toriyama/glyph.git", "github.com", "akira-toriyama/glyph", true},
		{"https://github.com/akira-toriyama/glyph", "github.com", "akira-toriyama/glyph", true},
		{"https://tommy@github.com/akira-toriyama/glyph.git", "github.com", "akira-toriyama/glyph", true},
		{"git@github.com:akira-toriyama/glyph.git", "github.com", "akira-toriyama/glyph", true},
		{"git@github.com:akira-toriyama/glyph", "github.com", "akira-toriyama/glyph", true},
		{"ssh://git@github.com/akira-toriyama/glyph.git", "github.com", "akira-toriyama/glyph", true},
		{"ssh://git@ghe.example:2222/akira-toriyama/glyph.git", "ghe.example", "akira-toriyama/glyph", true},
		{"http://127.0.0.1:8080/akira-toriyama/glyph.git", "127.0.0.1", "akira-toriyama/glyph", true},
		{"git@gitlab.com:group/sub/repo.git", "", "", false},
		{"https://github.com/onlyowner", "", "", false},
		{"https://github.com/", "", "", false},
		{"/Volumes/workspace/glyph", "", "", false},
		{"./dir:with:colons/repo", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		host, path, ok := splitRemoteURL(c.raw)
		if host != c.host || path != c.path || ok != c.ok {
			t.Errorf("splitRemoteURL(%q) = (%q, %q, %v), want (%q, %q, %v)", c.raw, host, path, ok, c.host, c.path, c.ok)
		}
	}
}

// TestRepoDefaultsToOrigin: outside Actions nothing sets GITHUB_REPOSITORY,
// and the clone in front of the command already names its repository — so a
// local `bump --pr` with no flag asks about origin, on the wire (t-ygmv). The
// origin points at the test server's own host because the fallback only
// trusts an origin on the host the client will query.
func TestRepoDefaultsToOrigin(t *testing.T) {
	srv := prServer(t, 3, `[`+apiCommit("a1", "akira-toriyama", ":bug:~ fix a crash")+`]`)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "remote", "add", "origin", srv.URL+"/akira-toriyama/glyph.git")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "bump", "--pr", "3")
	if code != 0 || stdout != "v0.1.1\n" {
		t.Fatalf("bump --pr with only an origin remote: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// TestEnvironmentBeatsOrigin: in Actions the variable is the authority and
// origin is whatever actions/checkout wrote; the fallback answers only where
// nobody else did.
func TestEnvironmentBeatsOrigin(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITHUB_REPOSITORY", "akira-toriyama/from-env")
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "remote", "add", "origin", "git@github.com:akira-toriyama/from-origin.git")
	t.Chdir(dir)

	owner, repo, err := resolveRepo(t.Context(), "")
	if err != nil || owner+"/"+repo != "akira-toriyama/from-env" {
		t.Fatalf("resolveRepo = %q/%q, %v; want the environment over origin", owner, repo, err)
	}
	owner, repo, err = resolveRepo(t.Context(), "akira-toriyama/from-flag")
	if err != nil || owner+"/"+repo != "akira-toriyama/from-flag" {
		t.Fatalf("resolveRepo = %q/%q, %v; want --repo over both", owner, repo, err)
	}
}

// TestOriginOnAnotherHostIsUsage: an origin the API client would never be
// asked about is refused at the entrance, naming both hosts — silently asking
// api.github.com about a GitLab clone would come back as a 404 wearing the
// API code, telling the caller to retry an input no retry can fix.
func TestOriginOnAnotherHostIsUsage(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "remote", "add", "origin", "git@gitlab.com:akira-toriyama/glyph.git")
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "7")
	if code != 2 {
		t.Fatalf("bump --pr with a gitlab origin exited %d, want 2 (usage)", code)
	}
	for _, want := range []string{"gitlab.com", "github.com", "--repo"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal should carry %q:\n%s", want, stderr)
		}
	}
}

// TestOriginWithoutACoordinateIsStillUsage: a local-path origin has no
// owner/name to give, so the answer is the same usage error as no origin at
// all — never a guess at a repository from a path.
func TestOriginWithoutACoordinateIsStillUsage(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	dir, _ := testRepo(t)
	testGit(t, dir, "akira-toriyama", "remote", "add", "origin", t.TempDir())
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--pr", "7")
	if code != 2 || !strings.Contains(stderr, "--repo owner/name is required") {
		t.Fatalf("bump --pr with a path origin: exit %d, want 2 with the required-repo usage:\n%s", code, stderr)
	}
}

func TestAPIHost(t *testing.T) {
	cases := []struct{ base, want string }{
		{"", "github.com"},
		{"https://api.github.com", "github.com"},
		{"https://ghe.example/api/v3", "ghe.example"},
		{"http://127.0.0.1:8080", "127.0.0.1"},
	}
	for _, c := range cases {
		t.Setenv("GITHUB_API_URL", c.base)
		if got := apiHost(); got != c.want {
			t.Errorf("apiHost() under %q = %q, want %q", c.base, got, c.want)
		}
	}
}
