package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useActionsRun hands the process the run identity an Actions runner hands it:
// GITHUB_REF, and an event payload on disk naming the repository's default
// branch. Measured shape — glyph-test's ref-probe (2026-08-11) read
// `.repository.default_branch` out of the real payload at $GITHUB_EVENT_PATH on
// a workflow_dispatch, from main and from a topic branch alike.
func useActionsRun(t *testing.T, ref, defaultBranch string) {
	t.Helper()
	useActionsRunRaw(t, ref, fmt.Sprintf(`{"repository":{"full_name":"akira-toriyama/glyph","default_branch":%q}}`, defaultBranch))
}

// useActionsRunRaw is the same with the payload byte-controlled, for the
// unreadable-boundary shapes.
func useActionsRunRaw(t *testing.T, ref, payload string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing the event payload: %v", err)
	}
	t.Setenv(envActionsRef, ref)
	t.Setenv(envActionsEvent, path)
}

// useActionsRunNoPayload arms the guard with the ref alone — the shape that
// forces the repository-object fallback.
func useActionsRunNoPayload(t *testing.T, ref string) {
	t.Helper()
	t.Setenv(envActionsRef, ref)
	t.Setenv(envActionsEvent, "")
}

// repoObjectServer serves GET /repos/{owner}/{name} — the guard's second
// boundary source — in front of the ordinary release surface. `object` is the
// JSON body; an empty string answers `status` with no body, so a test can
// separate "GitHub said the repository has no default branch" from "GitHub
// never answered".
func repoObjectServer(t *testing.T, walk map[string]string, releases string, writes *[]apiWrite, object string, status int) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	rest := releaseHandler(t, walk, releases, writes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/akira-toriyama/glyph" {
			hits++
			if object == "" {
				w.WriteHeader(status)
				return
			}
			fmt.Fprint(w, object)
			return
		}
		rest(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestReleaseRefusesAWriteFromANonDefaultRef is the guard's whole claim, run
// against the real release surface so a refusal is proven by the ABSENCE of a
// write and not only by an exit code.
//
// The defect: `glyph release` walks <tag>..HEAD out of local git, so off the
// default branch the range holds the branch's unmerged commits. Each resolves
// to no merged pull request, falls to the direct-push arm and is classified
// from its own subject; walkFacts stays complete, so §4's exit-4 refusal for an
// unreadable walk never fires. The upsert then retags the one managed draft and
// re-points its target at the branch tip — green run, wrong draft, and Publish
// cuts the tag on a commit the default branch never held. GitHub cannot
// restrict which ref a workflow_dispatch runs from, so no caller can close it.
//
// The `refs/heads/mainline` row is the reason this compares exactly rather than
// by prefix: a prefix test waves it through, and the wave-through is a write.
// `refs/tags/v1.2.3` is the other family — a re-run from a tag.
func TestReleaseRefusesAWriteFromANonDefaultRef(t *testing.T) {
	cases := []struct {
		name          string
		ref           string
		defaultBranch string
		wantExit      int
	}{
		{"a push to the default branch is the release path", "refs/heads/main", "main", 0},
		{"a topic branch is refused", "refs/heads/topic", "main", 4},
		{"a tag ref is refused", "refs/tags/v1.2.3", "main", 4},
		{"a branch the default branch is a prefix of is refused", "refs/heads/mainline", "main", 4},
		{"a non-main default branch is honoured", "refs/heads/trunk", "trunk", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var writes []apiWrite
			walk := oneFixWalk(t)
			srv := releaseServer(t, walk, `[]`, &writes)
			usePR(t, srv)
			useActionsRun(t, c.ref, c.defaultBranch)

			code, _, stderr := runGlyph(t, "release")
			if code != c.wantExit {
				t.Fatalf("release from %s (default %s) exited %d, want %d\nstderr: %s",
					c.ref, c.defaultBranch, code, c.wantExit, stderr)
			}
			if c.wantExit == 0 {
				if len(writes) != 1 || writes[0].method != "POST" {
					t.Fatalf("writes = %+v, want exactly one POST — the guard refused a legitimate release", writes)
				}
				return
			}
			if len(writes) != 0 {
				t.Errorf("a refused release still wrote to the API: %+v", writes)
			}
			// The integer, never a substring: exit 4 is what every caller
			// branches on, and `lint --stdin=false` once proved a message can
			// read right while the code is wrong.
			if env := decodeErrorEnvelope(t, stderr); env.Code != 4 {
				t.Errorf("refusal exited with envelope code %d, want 4 (the refusal family — no new integer)", env.Code)
			}
			for _, want := range []string{c.ref, "refs/heads/" + c.defaultBranch, "--dry-run"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not name %q — a refusal nobody can diagnose gets worked around instead of fixed:\n%s", want, stderr)
				}
			}
		})
	}
}

// TestReleaseRefGuardRefusesBeforeTheWalk pins the placement. Every case above
// stays green with the guard moved down beside the upsert, and by then the run
// has paid the walk — at least one API round-trip per commit — to compute a
// verdict it is not allowed to act on. The stronger reason is that the walk is
// where the wrong ANSWER is produced, not only the wrong write.
func TestReleaseRefGuardRefusesBeforeTheWalk(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Errorf("the refused run reached the API: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	oneFixWalk(t)
	usePR(t, srv)
	useActionsRun(t, "refs/heads/topic", "main")

	code, _, stderr := runGlyph(t, "release")
	if code != 4 {
		t.Fatalf("release exited %d, want 4\nstderr: %s", code, stderr)
	}
	if requests != 0 {
		t.Errorf("the guard let %d request(s) through before refusing", requests)
	}
}

// TestReleaseDryRunIsNotJudgedByTheRefGuard pins the one release-time refusal a
// preview deliberately does not reproduce.
//
// cmd_release.go ratifies the opposite rule for the body cap and --target ("a
// dry run previews the real run"), and this is the argued exception: those are
// properties of the WORK a run would publish, so hiding them would preview a
// lie, while the ref is a property of the WRITE and a dry run has no authority
// in question. Exempting it can never make the preview more permissive than the
// run it previews, because the real run from the same ref refuses
// unconditionally. Judging it instead would red the sanctioned preview path
// every consumer exposes as a dispatch input, on the day the pin moves.
//
// stdout is compared against the same run with no run context at all: the
// machine surface is the contract, and a guard that garnished it would have
// moved a payload every caller parses.
func TestReleaseDryRunIsNotJudgedByTheRefGuard(t *testing.T) {
	walk := oneFixWalk(t)
	srv := dryServer(t, walk)
	usePR(t, srv)

	useActionsRun(t, "refs/heads/topic", "main")
	code, guarded, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 {
		t.Fatalf("a dry run from a topic branch exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "refs/heads/topic") || !strings.Contains(stderr, "not judged") {
		t.Errorf("the dry run does not say the ref went unjudged — silence here reads as approval:\n%s", stderr)
	}

	// Both empty is the no-run-context arm; t.Setenv cannot unset, and empty is
	// what the guard reads for absent anyway.
	t.Setenv(envActionsRef, "")
	t.Setenv(envActionsEvent, "")
	code, bare, stderr := runGlyph(t, "release", "--dry-run")
	if code != 0 {
		t.Fatalf("a dry run with no run context exited %d, want 0\nstderr: %s", code, stderr)
	}
	if guarded != bare {
		t.Errorf("the guard changed a dry run's stdout:\n with run context: %q\n without:          %q", guarded, bare)
	}
}

// TestReleaseRefGuardArmsOnEitherHalfOfTheRunContext closes the hole that makes
// "inert outside Actions" defensible rather than lazy: arming on the ref alone
// would let a step that sets `env: GITHUB_REF:` to empty — or a runner that
// renames one variable — turn the fleet's only write-side ref boundary back
// into a guard-shaped comment, green, with nothing in the log to show it. One
// witness present and the other empty is a refusal, and the two refusals say
// different things because they have different causes.
func TestReleaseRefGuardArmsOnEitherHalfOfTheRunContext(t *testing.T) {
	t.Run("a payload with no ref refuses", func(t *testing.T) {
		var writes []apiWrite
		walk := oneFixWalk(t)
		srv := releaseServer(t, walk, `[]`, &writes)
		usePR(t, srv)
		useActionsRun(t, "", "main")

		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("exited %d, want 4\nstderr: %s", code, stderr)
		}
		if len(writes) != 0 {
			t.Errorf("a run with no ref still wrote: %+v", writes)
		}
		if !strings.Contains(stderr, envActionsRef) || !strings.Contains(stderr, envActionsEvent) {
			t.Errorf("the refusal does not name which half of the run context was missing:\n%s", stderr)
		}
	})

	t.Run("a ref with no readable boundary refuses", func(t *testing.T) {
		var writes []apiWrite
		walk := oneFixWalk(t)
		srv, hits := repoObjectServer(t, walk, `[]`, &writes, "", http.StatusInternalServerError)
		usePR(t, srv)
		useActionsRunNoPayload(t, "refs/heads/main")

		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("exited %d, want 4\nstderr: %s", code, stderr)
		}
		if *hits == 0 {
			t.Error("the fallback never asked for the repository object — the second source is not wired")
		}
		if len(writes) != 0 {
			t.Errorf("a run with an unreadable boundary still wrote: %+v", writes)
		}
		// The ref was refs/heads/main and may well have been legitimate. Saying
		// "wrong ref" here sends a human to fix something that was never wrong.
		if strings.Contains(stderr, "from the default branch only") {
			t.Errorf("an unreadable boundary was reported as a wrong ref:\n%s", stderr)
		}
	})
}

// TestReleaseRefGuardFallsBackToTheRepositoryObject proves the second source is
// a live path, not a comment. It is what makes this design survive a payload
// reshuffle: with one source, the day GitHub stops carrying the field every
// pinned repository refuses at once and the only recovery is reverting the pin
// in eleven of them.
func TestReleaseRefGuardFallsBackToTheRepositoryObject(t *testing.T) {
	const object = `{"full_name":"akira-toriyama/glyph","default_branch":"trunk"}`

	t.Run("the API's answer allows the matching ref", func(t *testing.T) {
		var writes []apiWrite
		walk := oneFixWalk(t)
		srv, hits := repoObjectServer(t, walk, `[]`, &writes, object, http.StatusOK)
		usePR(t, srv)
		useActionsRunNoPayload(t, "refs/heads/trunk")

		code, _, stderr := runGlyph(t, "release")
		if code != 0 {
			t.Fatalf("exited %d, want 0\nstderr: %s", code, stderr)
		}
		if *hits != 1 {
			t.Errorf("the repository object was fetched %d times, want exactly 1", *hits)
		}
		if len(writes) != 1 {
			t.Errorf("writes = %+v, want the ordinary single POST", writes)
		}
	})

	t.Run("the API's answer refuses the other ref", func(t *testing.T) {
		var writes []apiWrite
		walk := oneFixWalk(t)
		srv, _ := repoObjectServer(t, walk, `[]`, &writes, object, http.StatusOK)
		usePR(t, srv)
		useActionsRunNoPayload(t, "refs/heads/main")

		code, _, stderr := runGlyph(t, "release")
		if code != 4 {
			t.Fatalf("exited %d, want 4\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "repository object") {
			t.Errorf("the refusal does not say where the boundary came from, so it cannot be argued with:\n%s", stderr)
		}
	})
}

// TestReleaseRefGuardFailsClosedOnAnUnreadableBoundary walks the payload shapes
// that are present but say nothing. Each must reach the fallback and, when that
// also fails, refuse — never compare against "" (which would make
// `refs/heads/` the boundary and refuse with the wrong reason) and never
// proceed.
func TestReleaseRefGuardFailsClosedOnAnUnreadableBoundary(t *testing.T) {
	for _, c := range []struct {
		name    string
		payload string
	}{
		{"not JSON at all", "<html>502 Bad Gateway</html>"},
		{"an empty object", `{}`},
		{"a repository with no default branch", `{"repository":{"full_name":"akira-toriyama/glyph"}}`},
		{"an empty file", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var writes []apiWrite
			walk := oneFixWalk(t)
			// 404, not 5xx: a 4xx is the caller's input and is not retried, so
			// these four shapes cost no backoff. The transport-failure path,
			// which does pay the ~21s schedule, is exercised once in
			// TestReleaseRefGuardArmsOnEitherHalfOfTheRunContext.
			srv, hits := repoObjectServer(t, walk, `[]`, &writes, "", http.StatusNotFound)
			usePR(t, srv)
			useActionsRunRaw(t, "refs/heads/main", c.payload)

			code, _, stderr := runGlyph(t, "release")
			if code != 4 {
				t.Fatalf("exited %d, want 4\nstderr: %s", code, stderr)
			}
			if *hits == 0 {
				t.Error("an unreadable payload did not fall back to the repository object")
			}
			if len(writes) != 0 {
				t.Errorf("a run with an unverified boundary still wrote: %+v", writes)
			}
		})
	}
}

// TestReleaseRefGuardPrefersTheEventPayloadOverTheAPI pins the source ORDER.
// The payload is free; the fallback is one more request on a path that already
// pays per commit, and #156 declined the round trip precisely because its
// failure mode is a fail-closed guard refusing every manual run in the fleet.
// A silent flip of the order would be invisible except in the API budget.
func TestReleaseRefGuardPrefersTheEventPayloadOverTheAPI(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	// The object would ALLOW a ref the payload refuses, so a test that passes
	// here cannot be passing by agreement between the two sources.
	srv, hits := repoObjectServer(t, walk, `[]`, &writes, `{"default_branch":"topic"}`, http.StatusOK)
	usePR(t, srv)
	useActionsRun(t, "refs/heads/topic", "main")

	code, _, stderr := runGlyph(t, "release")
	if code != 4 {
		t.Fatalf("exited %d, want 4 — the API answer overrode the payload\nstderr: %s", code, stderr)
	}
	if *hits != 0 {
		t.Errorf("the repository object was fetched %d times though the payload answered — the free source is no longer first", *hits)
	}
}

// TestReleaseRefGuardIsInertWithoutARunContext holds the deliberate hole open
// where it can be seen. No documented human caller of a WRITING `glyph release`
// exists — README lists no local use, and scripts/fleet-preflight.sh reaches
// the release path only with --dry-run — so refusing here would close a caller
// class that does not exist while breaking the one that does. The notice is the
// price: silence would make an unguarded write indistinguishable from a guarded
// one in a log.
func TestReleaseRefGuardIsInertWithoutARunContext(t *testing.T) {
	var writes []apiWrite
	walk := oneFixWalk(t)
	srv := releaseServer(t, walk, `[]`, &writes)
	usePR(t, srv)
	t.Setenv(envActionsRef, "")
	t.Setenv(envActionsEvent, "")

	code, _, stderr := runGlyph(t, "release")
	if code != 0 {
		t.Fatalf("a release with no run context exited %d, want 0\nstderr: %s", code, stderr)
	}
	if len(writes) != 1 {
		t.Fatalf("writes = %+v, want the ordinary single POST", writes)
	}
	if !strings.Contains(stderr, "not judged") {
		t.Errorf("an unjudged write said nothing about being unjudged:\n%s", stderr)
	}
}
