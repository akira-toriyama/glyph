package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode"

	"github.com/akira-toriyama/glyph/v3/internal/core"
	"github.com/akira-toriyama/glyph/v3/internal/github"
	"github.com/akira-toriyama/glyph/v3/internal/gitsource"
)

// This file is the GitHub-side input plumbing — the remote twin of range.go. It
// answers "which repository, with which credential, against which host", and
// turns a pull request's individual (pre-squash) commits into the very same
// participating-commit list the local --range walk produces. Command logic stays
// in the cmd_*.go files; the participation rules stay in internal/bump and
// internal/config, so a PR classifies identically whether it is read from git or
// from the API.

// The environment GitHub Actions already populates, so a caller in CI needs no
// flags at all. GITHUB_API_URL is what a GitHub Enterprise runner points at its
// own host — honoring it is both the Enterprise story and what lets a test stand
// an httptest.Server in for api.github.com with no test-only hook.
const (
	envRepo    = "GITHUB_REPOSITORY" // owner/name
	envAPIURL  = "GITHUB_API_URL"
	envToken   = "GITHUB_TOKEN"
	envGHToken = "GH_TOKEN" // what the gh CLI exports; accepted as a fallback
)

// resolveRepo picks the repository to query: an explicit --repo wins, else
// GITHUB_REPOSITORY, else the clone's origin remote (t-ygmv). With none of
// the three there is nothing to ask and no request has gone out, so a
// missing or malformed value is the caller's input — usage, never an API
// failure.
//
// The environment sits ABOVE origin on purpose: in Actions the variable is
// the authority and origin is whatever actions/checkout wrote, and outside
// Actions the variable is unset, so origin only ever answers where nobody
// else did. Only the remote named origin is read — a second remote is a
// choice the caller makes with --repo — and its URL must point at the host
// the API client will query (github.com, or GITHUB_API_URL's host): a
// clone of some other forge would otherwise be asked about on api.github.com
// and come back as a 404 wearing the API code.
//
// Interior whitespace is judged here too (ratified 2026-07-22): TrimSpace alone
// let `a b/c` sail to the wire and come back as a 404 wearing the API code, so
// the caller was told to retry an input no retry can fix. Same rule as the
// empty-flag guard (#64): the entrance names what is wrong with caller input,
// at exit 2, before any request goes out.
func resolveRepo(ctx context.Context, flag string) (owner, repo string, err error) {
	spec := strings.TrimSpace(flag)
	if spec == "" {
		spec = strings.TrimSpace(os.Getenv(envRepo))
	}
	if spec == "" {
		spec, err = originRepo(ctx)
		if err != nil {
			return "", "", err
		}
	}
	if spec == "" {
		return "", "", core.Usagef("--repo owner/name is required (or set %s, which GitHub Actions sets for you, or run inside a clone whose origin remote points at GitHub)", envRepo)
	}
	owner, repo, found := strings.Cut(spec, "/")
	if !found || owner == "" || repo == "" || strings.Contains(repo, "/") ||
		strings.ContainsFunc(spec, unicode.IsSpace) {
		return "", "", core.Usagef("--repo %q is not owner/name", spec)
	}
	return owner, repo, nil
}

// originRepo reads owner/name off the origin remote of the clone the command
// runs in: "" when there is no clone, no origin, or an origin whose URL has
// no owner/name to give (every one of those means "nothing to infer", and
// resolveRepo's usage error is the right answer). An origin on a host the
// client will NOT query is usage too, but a loud one — it names both hosts,
// because silently asking api.github.com about a GitLab clone is the shape
// this guard exists to refuse.
func originRepo(ctx context.Context) (string, error) {
	urls, err := gitsource.RemoteURLs(ctx, ".")
	if err != nil {
		return "", nil
	}
	raw, ok := urls["origin"]
	if !ok {
		return "", nil
	}
	host, path, ok := splitRemoteURL(raw)
	if !ok {
		return "", nil
	}
	if want := apiHost(); !strings.EqualFold(host, want) {
		return "", core.Usagef("origin remote %q is on %s, but the API host is %s — name the repository with --repo owner/name, or set %s to the host that serves it", raw, host, want, envAPIURL)
	}
	return path, nil
}

// apiHost is the repository host the API client's base URL implies:
// github.com for the public API, else GITHUB_API_URL's hostname (GitHub
// Enterprise Server serves web and API from one host; an httptest server in
// a test is its own host, which no origin points at — so a test that wants
// the fallback names the host explicitly). Ports are dropped on both sides:
// an ssh origin and an https API never share one.
func apiHost() string {
	base := strings.TrimSpace(os.Getenv(envAPIURL))
	if base == "" {
		return "github.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return base
	}
	if u.Hostname() == "api.github.com" {
		return "github.com"
	}
	return u.Hostname()
}

// splitRemoteURL takes a git remote URL apart into its host and its
// owner/name path, for the three spellings a GitHub clone comes in:
// https://host/owner/name(.git), ssh://git@host[:port]/owner/name(.git), and
// the scp-like git@host:owner/name(.git). Anything else — a local path, a
// deeper path, an empty segment — is not a repository coordinate (ok false).
func splitRemoteURL(raw string) (host, path string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	var rest string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", false
		}
		host, rest = u.Hostname(), u.Path
	default:
		// scp-like: [user@]host:path — the colon marks the host end only when
		// no slash precedes it, which is what separates it from a local path
		// such as ./dir:with:colons/repo.
		i := strings.Index(raw, ":")
		if i < 0 || strings.Contains(raw[:i], "/") {
			return "", "", false
		}
		host, rest = raw[:i], raw[i+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	rest = strings.TrimSuffix(strings.Trim(rest, "/"), ".git")
	owner, name, found := strings.Cut(rest, "/")
	if host == "" || !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return host, owner + "/" + name, true
}

// checkPRFlag rejects a non-positive pull-request number before any request goes
// out — pull requests are numbered from 1, so this is caller input.
func checkPRFlag(number int) error {
	if number < 1 {
		return core.Usagef("--pr %d is not a pull-request number (they start at 1)", number)
	}
	return nil
}

// githubToken resolves the credential: GITHUB_TOKEN, else GH_TOKEN (what the gh
// CLI exports), else empty — which still reads a public repository, at the
// anonymous rate limit. It is a named function rather than an inline lookup
// because `doctor` reports WHETHER a credential was configured, and that answer
// must come from the same resolution the client actually uses.
func githubToken() string {
	if token := os.Getenv(envToken); token != "" {
		return token
	}
	return os.Getenv(envGHToken)
}

// newGitHub builds the API client from the environment: the token (see
// githubToken) and the REST host (GITHUB_API_URL, else the public one).
func newGitHub() *github.Client {
	token := githubToken()
	var opts []github.Option
	if base := strings.TrimSpace(os.Getenv(envAPIURL)); base != "" {
		opts = append(opts, github.WithBaseURL(base))
	}
	return github.New(token, opts...)
}

// pullInput resolves the repository, reads a pull request's individual
// commits — the ones that exist BEFORE the squash rewrites them into a single
// subject, which is the whole reason glyph exists — and names the source for
// the reason line (owner/name#N). bump and notes share it, so both read a
// pull request the same way; what the commits MEAN is the pattern file's
// question, asked downstream.
func pullInput(ctx context.Context, number int, repoFlag string) ([]gitsource.RawCommit, string, error) {
	if err := checkPRFlag(number); err != nil {
		return nil, "", err
	}
	owner, repo, err := resolveRepo(ctx, repoFlag)
	if err != nil {
		return nil, "", err
	}
	raws, err := pullRawCommits(ctx, newGitHub(), owner, repo, number)
	return raws, fmt.Sprintf("%s/%s#%d", owner, repo, number), err
}

// pullRawCommits is the read half of pullInput: the listing as GitHub
// returns it, converted to the local raw shape and with the truncation warning
// already emitted, but NOT yet parsed.
//
// The split exists for the release walk, which must filter this listing against
// its walk-wide SHA set BEFORE anything parses it. A commit the walk already
// folded in is already represented in the verdict, so re-reading its message
// can only do harm: a pull request squash-merged into a topic branch leaves its
// own squash subject (`Add a menu (#6)` — not gitmoji-formed, as no squash
// subject is) inside the listing of the pull that later landed that branch, and
// parsing it there wedged the release permanently (t-7zt7). Parse only what the
// walk has not already accounted for.
func pullRawCommits(ctx context.Context, c *github.Client, owner, repo string, number int) ([]gitsource.RawCommit, error) {
	raws, err := c.PullCommits(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	if len(raws) >= github.PullCommitsCap {
		// No silent caps: the verdict may be computed from a truncated PR, and
		// a missing commit could carry the deciding sigil.
		warnf("pull request #%d returned %d commits — GitHub truncates this listing at %d, so some commits (and their sigils) may be missing from the verdict", number, len(raws), github.PullCommitsCap)
	}
	local := make([]gitsource.RawCommit, len(raws))
	for i, r := range raws {
		local[i] = gitsource.RawCommit(r)
	}
	return local, nil
}
