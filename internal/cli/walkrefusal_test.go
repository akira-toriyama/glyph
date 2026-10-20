package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/testutil"
)

// This file pins the wedge (DESIGN §4; internal/walk/refusal.go): a walked
// commit no pattern claims is refused through one producer whether it sat
// inside a pull or was pushed straight to the released branch, every
// offender is named at once, the refusal outranks a later API failure, and
// the escape the error hands the operator opens the door.

// TestSinceTagResolvedPRLintNamesTheWedge pins the ratified Q1/t-2nzf error
// contract: a commit INSIDE a resolved merged PR that fails lint (a message
// that does not parse, or an unknown gitmoji) stays a hard exit 3 on the
// release walk — never a silent patch — but the error must not be the bare
// per-commit lint line: the walk found the PR itself (the caller never named
// one), and published history is immutable, so the same failure wedges EVERY
// future release until the range moves past the pull's MERGE POINT. The message
// therefore names the pull request, that merge point, and the escape (a hand-cut
// tag there, or an explicit --since-tag base naming one).
func TestSinceTagResolvedPRLintNamesTheWedge(t *testing.T) {
	for name, tc := range map[string]struct {
		inner string
		shas  []string
	}{
		"malformed":      {apiCommit("a1", "akira-toriyama", "no gitmoji leads this subject"), []string{"a1"}},
		"unknown-etmoji": {apiCommit("a1", "akira-toriyama", ":notarealmoji: tweak something"), []string{"a1"}},
		"two refused inner commits, one run": {apiCommit("a1", "akira-toriyama", "no gitmoji leads this subject") + "," +
			apiCommit("a2", "akira-toriyama", ":bug:~ a fine one between") + "," +
			apiCommit("a3", "akira-toriyama", ":notarealmoji: tweak something"), []string{"a1", "a3"}},
	} {
		t.Run(name, func(t *testing.T) {
			inner := tc.inner
			dir, _ := testutil.NewTaggedRepo(t)
			sha := squashCommit(t, dir, "Add a menu", 7)
			srv := walkServer(t, map[string]string{
				commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`,
				pullCommitsPath(7):   `[` + inner + `]`,
			})
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 3 {
				t.Fatalf("a resolved-PR lint failure exited %d, want 3 (hard, per Q1)\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("a lint failure wrote a payload:\n%s", stdout)
			}
			// A squash-merged pull has exactly one commit on main and it IS the
			// merge point, so naming the merge point still names the commit the
			// operator would reach for — the two only diverge on a merge-merged
			// pull (TestSinceTagWedgeEscapeIsTheOffendingCommit).
			for _, want := range []string{"akira-toriyama/glyph#7", "--since-tag", "by hand", sha[:7]} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the wedge error must carry %q (the PR, its merge point and the escape):\n%s", want, stderr)
				}
			}
			if !strings.Contains(stderr, "a1") {
				t.Errorf("the error must still name the offending commit:\n%s", stderr)
			}
			// The envelope carries every refused commit as details, the shape
			// every other refusal of the gate code carries ({sha, subject,
			// detail}) — a wrapper reading .error.details finds the commits
			// here as it does under lint --range and under the fold.
			assertWedgeDetails(t, decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):]), tc.shas...)
		})
	}
}

// assertWedgeDetails decodes a wedge envelope's details as the refused
// commits, in walk order, and checks their shas.
func assertWedgeDetails(t *testing.T, env errEnvelope, shas ...string) {
	t.Helper()
	var details []struct {
		SHA     string `json:"sha"`
		Subject string `json:"subject"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(env.Details, &details); err != nil || len(details) != len(shas) {
		t.Fatalf("details = %s, want %d {sha,subject,detail} row(s) (decode: %v)", env.Details, len(shas), err)
	}
	for i, sha := range shas {
		if details[i].SHA != sha || details[i].Subject == "" || details[i].Detail == "" {
			t.Errorf("details[%d] = %+v, want sha %q with a subject and a detail", i, details[i], sha)
		}
	}
}

// TestSinceTagDirectPushUnmatchedNamesTheWedge: a commit pushed straight to
// the released branch whose message no pattern claims is refused by the walk
// through the SAME producer as a pull's inner commit — the wedge error, which
// names where the commit sits and the base that clears it (a tag at or past
// the commit itself, since it is on the branch). Before this it fell through
// to the fold's refusal, whose remedy ("fix the message, add a pattern, or
// exclude the author") is written for a commit that has not landed. And as
// the fold did, the walk names EVERY refused commit at once (details), not
// the first: two offenders are one red run, not two.
func TestSinceTagDirectPushUnmatchedNamesTheWedge(t *testing.T) {
	for name, message := range map[string]string{
		"malformed":      "no gitmoji leads this subject",
		"unknown-etmoji": ":notarealmoji: tweak something",
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := testutil.NewTaggedRepo(t)
			testutil.Commit(t, dir, "akira-toriyama", message)
			bad := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Commit(t, dir, "akira-toriyama", ":bug:~ a fix between, pushed directly")
			mid := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Commit(t, dir, "akira-toriyama", "another subject no pattern claims")
			bad2 := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Commit(t, dir, "akira-toriyama", ":bug:~ a later fix, also pushed directly")
			later := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			srv := walkServer(t, map[string]string{
				commitPullsPath(bad):   `[]`,
				commitPullsPath(mid):   `[]`,
				commitPullsPath(bad2):  `[]`,
				commitPullsPath(later): `[]`,
			})
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag")
			if code != 3 {
				t.Fatalf("a direct push no pattern claims exited %d, want 3\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal wrote a payload:\n%s", stdout)
			}
			// The claimed commits' fallback ::warning:: lines precede the
			// envelope, as the fleet's sieve expects (output_test).
			env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
			for _, want := range []string{bad[:7], "AT OR PAST " + bad[:7], "AT OR PAST " + bad2[:7], "+1 more refused commit(s)", "at or past all of them", "--since-tag", "by hand", "released branch"} {
				if !strings.Contains(env.Message, want) {
					t.Errorf("the wedge error must carry %q (the first commit, where it sits, the escape and the count):\n%s", want, env.Message)
				}
			}
			for _, wrong := range []string{"add a pattern", "pull request #"} {
				if strings.Contains(env.Message, wrong) {
					t.Errorf("the error must not say %q — the commit is on the released branch and inside no pull:\n%s", wrong, env.Message)
				}
			}
			assertWedgeDetails(t, env, bad, bad2)

			// The escapes open: a base at or past every one of them walks
			// only the later commit. A base at the FIRST alone still wedges
			// on the second, whose escape the sentence also named.
			testutil.Git(t, dir, "akira-toriyama", "tag", "v0.2.0", bad)
			if code, _, stderr := runGlyph(t, "bump", "--since-tag=v0.2.0"); code != 3 || !strings.Contains(stderr, "AT OR PAST "+bad2[:7]) {
				t.Fatalf("bump --since-tag=v0.2.0 = exit %d, want 3 naming the second commit\nstderr: %s", code, stderr)
			}
			testutil.Git(t, dir, "akira-toriyama", "tag", "v0.3.0", bad2)
			code, stdout, stderr = runGlyph(t, "bump", "--since-tag=v0.3.0")
			if code != 0 || stdout != "v0.3.1\n" {
				t.Fatalf("bump --since-tag=v0.3.0 = exit %d stdout %q, want 0 / v0.3.1 (the escape must open)\nstderr: %s", code, stdout, stderr)
			}
		})
	}
}

// TestSinceTagRefusedCommitIsCountedOnce: a refused commit stays REPRESENTED
// in the walk like any other, so a stacked pull that lists it again does not
// refuse it twice — one commit, one details row, no "+1 more".
func TestSinceTagRefusedCommitIsCountedOnce(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	sha1 := squashCommit(t, dir, "Add a menu", 7)
	sha2 := squashCommit(t, dir, "Fix a crash", 8)
	base := apiPullRef(7, "2026-07-12T00:00:00Z", sha1)
	stacked := apiPullRef(8, "2026-07-13T00:00:00Z", sha2)
	srv := walkServer(t, map[string]string{
		commitPullsPath(sha1): `[` + base + `,` + stacked + `]`,
		commitPullsPath(sha2): `[` + stacked + `]`,
		pullCommitsPath(7):    `[` + apiCommit("x1", "akira-toriyama", "no gitmoji leads this subject") + `]`,
		pullCommitsPath(8): `[` +
			apiCommit("x1", "akira-toriyama", "no gitmoji leads this subject") + `,` +
			apiCommit("b1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("exit %d, want 3\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if strings.Contains(env.Message, "more refused") {
		t.Errorf("one refused commit listed by two pulls was counted twice:\n%s", env.Message)
	}
	assertWedgeDetails(t, env, "x1")
	// One row, two escapes: each pull's merge point wedges on its own, so a
	// base at #7's alone would leave the next run red on #8's.
	for _, want := range []string{"AT OR PAST " + sha1[:7], "AT OR PAST " + sha2[:7], "#7", "#8", "at or past all of them"} {
		if !strings.Contains(env.Message, want) {
			t.Errorf("the sentence must name both pulls' escapes (%q):\n%s", want, env.Message)
		}
	}
	if strings.Contains(stderr, "counted once") {
		t.Errorf("a refused commit was announced as counted:\n%s", stderr)
	}
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.2.0", sha2)
	if code, _, stderr := runGlyph(t, "bump", "--since-tag=v0.2.0"); code != 1 {
		t.Fatalf("a base at the last escape still exited %d, want 1 (nothing left to walk)\nstderr: %s", code, stderr)
	}
}

// TestSinceTagRefusedPullCommitMetAgainOnTheBranch: the other order of the
// stacked case — a commit refused inside a pull the walk expanded earlier,
// then walked as its own commit on the released branch (the pull's listing
// held it, GitHub links it to no pull). One row, no "counted once" for a
// commit that was not counted, and the commit is its own escape.
func TestSinceTagRefusedPullCommitMetAgainOnTheBranch(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	squash := squashCommit(t, dir, "Add a menu", 9)
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji leads this subject")
	bad := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{
		commitPullsPath(squash): `[` + apiPullRef(9, "2026-07-12T00:00:00Z", squash) + `]`,
		pullCommitsPath(9):      `[` + apiCommit(bad, "akira-toriyama", "no gitmoji leads this subject") + `]`,
		commitPullsPath(bad):    `[]`,
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("exit %d, want 3\nstderr: %s", code, stderr)
	}
	if strings.Contains(stderr, "counted once") {
		t.Errorf("a refused commit was announced as counted:\n%s", stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if strings.Contains(env.Message, "more refused") || !strings.Contains(env.Message, "AT OR PAST "+bad[:7]) {
		t.Errorf("one commit, one row, its own escape:\n%s", env.Message)
	}
	assertWedgeDetails(t, env, bad)
}

// TestSinceTagRefusalOutranksALaterAPIFailure: a refusal the walk already
// found is the verdict even when reading the rest of the range then fails —
// the range wedges whatever else it holds, and the walk that stopped at the
// first refusal never read past it either. A 4 here would hide a verdict the
// operator must act on behind a failure that clears on retry.
func TestSinceTagRefusalOutranksALaterAPIFailure(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji leads this subject")
	bad := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	sha := squashCommit(t, dir, "Add a menu", 7)
	srv := failingWalkServer(t, map[string]string{
		commitPullsPath(bad): `[]`,
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-07-12T00:00:00Z", sha) + `]`,
	}, func(r *http.Request) int {
		if r.URL.Path == pullCommitsPath(7) {
			return http.StatusInternalServerError
		}
		return 0
	})
	usePR(t, srv)
	t.Chdir(dir)

	code, _, stderr := runGlyph(t, "bump", "--since-tag")
	if code != 3 {
		t.Fatalf("a refusal followed by a 500 exited %d, want 3 (the refusal is the verdict)\nstderr: %s", code, stderr)
	}
	env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
	if !strings.Contains(env.Message, "AT OR PAST "+bad[:7]) || strings.Contains(env.Message, "boom") {
		t.Errorf("the envelope must be the wedge, not the later failure:\n%s", env.Message)
	}
	// The failure is not swallowed: it is said on stderr, with what it means
	// for the count.
	if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "boom") || !strings.Contains(stderr, "more refused commits") {
		t.Errorf("the later failure must be announced beside the refusal:\n%s", stderr)
	}
}

// TestNotesSinceTagDirectPushUnclaimedIsTheWedge: notes shares the walk, so
// the walk's refusal is notes' too — as it already was for a pull's inner
// commit. notes has no fold: it used to leave an unclaimed direct push out of
// every section (exit 0 beside other notes, 1 alone), or render its raw
// subject under a [[note.sections]] entry claiming its author; the walk now
// judges its own commits before any consumer reads them (DESIGN §4). The lag
// arm stays soft (TestSinceTagAllCommitsUnknownWarnsAboutTheRepo).
func TestNotesSinceTagDirectPushUnclaimedIsTheWedge(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji leads this subject")
	bad := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	srv := walkServer(t, map[string]string{commitPullsPath(bad): `[]`})
	usePR(t, srv)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 3 || stdout != "" {
		t.Fatalf("notes --since-tag over an unclaimed direct push = exit %d stdout %q, want 3 and nothing rendered\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "AT OR PAST "+bad[:7]) {
		t.Errorf("notes must hand down the walk's wedge, escape included:\n%s", stderr)
	}
}

// TestSinceTagWedgeEscapeIsTheOffendingCommit walks the escape the wedge error
// hands the operator and checks that the door actually opens.
//
// For a MERGE-merged pull the escape is the intuitive one — a base at or past
// the offending commit — and it is only intuitive again because of t-8xsb.
// Before mainFootprint, expanding a pull re-fetched its ENTIRE listing whenever
// its merge point was in range, so the offending commit came back over the API
// however far past it the tag sat; the hint had to send everyone to the merge
// COMMIT, throwing away every good commit in between. Now a listed commit that
// landed outside the range is dropped before it is parsed, so the first two rows
// below open (they exited 3 until this fix) and the hint names the commit.
//
// The four tags are the whole table, on one repository: pull #7 landed through
// the merge button with a non-conforming commit B1 inside it, and #8
// squash-merged afterwards so a cleared walk still has something to release.
func TestSinceTagWedgeEscapeIsTheOffendingCommit(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	mp := mergePR(t, dir, "akira-toriyama", 7, "no gitmoji leads this subject", ":bug:(ui)~ polish the menu")
	bad, good := mp.Branch[0], mp.Branch[1]
	later := squashCommit(t, dir, "Fix a crash", 8)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.2.0", bad)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.3.0", good)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.4.0", mp.Merge)
	pr7 := `[` + apiPullRef(7, "2026-07-20T00:00:00Z", mp.Merge) + `]`
	routes := map[string]string{
		commitPullsPath(bad):      pr7,
		commitPullsPath(good):     pr7,
		commitPullsPath(mp.Merge): pr7,
		commitPullsPath(later):    `[` + apiPullRef(8, "2026-07-21T00:00:00Z", later) + `]`,
		pullCommitsPath(7): `[` +
			apiCommit(bad, "akira-toriyama", "no gitmoji leads this subject") + `,` +
			apiCommit(good, "akira-toriyama", ":bug:(ui)~ polish the menu") + `]`,
		pullCommitsPath(8): `[` + apiCommit("c1", "akira-toriyama", ":bug:~ fix a crash") + `]`,
	}
	for _, tc := range []struct {
		name     string
		tag      string
		wantCode int
		wantOut  string
	}{
		{"a base BEFORE the pull is the wedge itself", "v0.1.0", 3, ""},
		{"a tag AT the offending commit", "v0.2.0", 0, "v0.2.1\n"},
		{"a tag strictly PAST the offending commit", "v0.3.0", 0, "v0.3.1\n"},
		{"a tag at the pull's merge commit", "v0.4.0", 0, "v0.4.1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := walkServer(t, routes)
			usePR(t, srv)
			t.Chdir(dir)

			code, stdout, stderr := runGlyph(t, "bump", "--since-tag="+tc.tag)
			if code != tc.wantCode || stdout != tc.wantOut {
				t.Fatalf("bump --since-tag=%s = exit %d stdout %q, want %d / %q\nstderr: %s", tc.tag, code, stdout, tc.wantCode, tc.wantOut, stderr)
			}
			if tc.wantCode != 3 {
				return
			}
			env := decodeErrorEnvelope(t, stderr)
			if !strings.Contains(env.Message, bad[:7]) {
				t.Errorf("the error must still name the offending commit %.7s:\n%s", bad, env.Message)
			}
			// The escape names the OFFENDING COMMIT, because the two rows above
			// prove a base there opens the door — sending the operator to the
			// merge point instead would silently drop everything between.
			if want := "AT OR PAST " + bad[:7]; !strings.Contains(env.Message, want) {
				t.Errorf("the escape must name the offending commit (%q), the nearest base that opens the door:\n%s", want, env.Message)
			}
		})
	}
}
