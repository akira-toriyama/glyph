package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestNotesMarkdown: the full Markdown body under the gemoji preset — the
// section order the config declares, a major landing in Breaking Changes, a
// none commit landing nowhere (no none-section is configured), and the
// note.line template rendered with the author credited by name: under
// --range the API attaches no login, so the template's at-sign goes with the
// mention it cannot make (t-ayhq). Each line carries the whole commit subject
// — gitmoji, scope and sigil included — which is the preset's ratified
// subject group (DESIGN §3), not a rendering that forgot to apply the
// template.
//
// Every commit here reached main without a merged pull — which is EVERY
// commit under --range, since the range walk resolves no pulls at all — so
// this is the empty arm of the preset's optional span, byte-exact: no `()`,
// and no double space where the span would sit. The populated arm is
// TestReleaseDryRunGolden, whose two lines both carry `(#N)`; break the span
// in either direction and one of the two fails.
func TestNotesMarkdown(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a command palette")
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the palette")
	testutil.Commit(t, dir, "akira-toriyama", ":recycle:! rework the store")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Breaking Changes\n\n" +
		"- :recycle:! rework the store akira-toriyama\n\n" +
		"## Features\n\n" +
		"- :sparkles:(ui)^ add a command palette akira-toriyama\n\n" +
		"## Fixes\n\n" +
		"- :bug:~ fix a crash akira-toriyama\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesSectionsAloneDecide: exclude_authors keeps a bot out of the FOLD,
// never out of the notes — whether a commit appears is note.sections'
// decision alone (ratified), so a bot's raw bump lands in the author-axis
// Dependencies section and a bot's conforming commit lands in its semver
// section too.
func TestNotesSectionsAloneDecide(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "dependabot[bot]", "Bump a dep from 1 to 2")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0", code)
	}
	if !strings.Contains(stdout, "## Dependencies") || !strings.Contains(stdout, "Bump a dep from 1 to 2") {
		t.Fatalf("the bot commit must land in the Dependencies author section, rendered from its raw first line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "## Fixes") {
		t.Fatalf("the human fix must reach the notes:\n%s", stdout)
	}
}

// TestNotesFallbackOpensNoBlockBehindTheBullet is the measured shape end to end:
// an exclude_authors commit is never judged, so lint and notes both exit 0 and its
// first line lands right behind the preset's "- ", where GitHub would open the
// block. The indent comes through git as written.
func TestNotesFallbackOpensNoBlockBehindTheBullet(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "dependabot[bot]", "## Breaking Changes")
	testutil.Commit(t, dir, "dependabot[bot]", "> [!WARNING] x")
	testutil.Commit(t, dir, "dependabot[bot]", "    indented")
	testutil.Commit(t, dir, "dependabot[bot]", "[ ] ship it")
	testutil.Commit(t, dir, "dependabot[bot]", "[　] ship it") // an ideographic space: the filter's blank is Unicode's
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD"); code != 0 {
		t.Fatalf("lint --range exited %d, want 0\nstderr: %s", code, stderr)
	}
	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Dependencies\n\n" +
		`- \## Breaking Changes dependabot\[bot]` + "\n" +
		`- \> \[!WARNING] x dependabot\[bot]` + "\n" +
		`- indented dependabot\[bot]` + "\n" +
		`- \[\ ] ship it dependabot\[bot]` + "\n" +
		"- \\[\\　] ship it dependabot\\[bot]\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesHTMLBlockBehindATemplateLineEndingIsDisarmed: a note.line literal
// can carry a line ending inside a backtick span, and a value on its second line
// is at a block start where rules 1 and 3 do not run (the scan believes the
// span). GitHub decides blocks first: raw, an HTML comment, a processing
// instruction or a declaration there ends the paragraph, no span forms and the
// mention behind it is live (measured 2026-11-08 through POST /markdown on the
// bytes notes wrote); rule 5's backslash keeps the span and the mention inert.
func TestNotesHTMLBlockBehindATemplateLineEndingIsDisarmed(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("gemoji preset missing")
	}
	const shipped = "line = '- $subject$[ ($pr)] @$author$[ with $coauthors]'"
	if !strings.Contains(string(preset), shipped) {
		t.Fatalf("the gemoji preset no longer carries %q; move this test's edit with it", shipped)
	}
	seedConfig(t, dir, strings.Replace(string(preset), shipped, "line = \"- `ping\\n$subject` x\"", 1))
	testutil.Commit(t, dir, "dependabot[bot]", "<!-- --> cc @octocat now")
	testutil.Commit(t, dir, "dependabot[bot]", "<? ?> cc @octocat now")
	testutil.Commit(t, dir, "dependabot[bot]", "<!X> cc @octocat now")
	t.Chdir(dir)

	if code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD"); code != 0 {
		t.Fatalf("lint --range exited %d, want 0\nstderr: %s", code, stderr)
	}
	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Dependencies\n\n" +
		"- `ping\n\\<!-- --> cc @octocat now` x\n" +
		"- `ping\n\\<? ?> cc @octocat now` x\n" +
		"- `ping\n\\<!X> cc @octocat now` x\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesRefusesATemplateThatOpensAnHTMLBlock: a note.line that puts "<" at a
// block start is a config the loader refuses, so notes answers 2 — no verdict was
// reached — and names the key, the template and where the "<" stands. Before the
// refusal this template loaded and rendered `\&#64;octocat` inside `<div>` as a
// live @octocat (t-3bwg, TestLoadRefusesAnHTMLBlockAtABlockStart).
func TestNotesRefusesATemplateThatOpensAnHTMLBlock(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	preset, ok := config.Preset("gemoji")
	if !ok {
		t.Fatalf("gemoji preset missing")
	}
	const shipped = "line = '- $subject$[ ($pr)] @$author$[ with $coauthors]'"
	if !strings.Contains(string(preset), shipped) {
		t.Fatalf("the gemoji preset no longer carries %q; move this test's edit with it", shipped)
	}
	seedConfig(t, dir, strings.Replace(string(preset), shipped, "line = '- <div>$subject</div>'", 1))
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ callers to &#64;octocat now")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != int(core.CodeUsage) {
		t.Fatalf("notes exited %d, want %d\nstdout: %s\nstderr: %s", code, core.CodeUsage, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("a refused config printed a body:\n%s", stdout)
	}
	env := decodeErrorEnvelope(t, stderr)
	if want := `note.line: "- <div>$subject</div>" puts "<" at a block start, line 1 column 3: `; env.Code != int(core.CodeUsage) || !strings.Contains(env.Message, want) {
		t.Errorf("envelope = %+v, want code %d and a message holding %q", env, core.CodeUsage, want)
	}
}

// TestNotesEmptyIsNoRelease: a docs-only range is the soft no-release exit —
// nothing on stdout, exit 1, the reason in the stderr envelope.
func TestNotesEmptyIsNoRelease(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the notes model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 1 {
		t.Fatalf("docs-only notes exited %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("no-release must print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no release notes") {
		t.Fatalf("stderr should say why there are no notes:\n%s", stderr)
	}
}

// TestNotesEmptyJSON: --json still emits the machine verdict (sections
// normalized to [], a reason) on stdout and exits 1 without a second stderr
// envelope.
func TestNotesEmptyJSON(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the notes model")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD", "--json")
	if code != 1 {
		t.Fatalf("docs-only notes --json exited %d, want 1", code)
	}
	if !strings.Contains(stdout, `"sections":[]`) {
		t.Fatalf("sections must normalize to [], got:\n%s", stdout)
	}
	type notesReason struct {
		Reason string `json:"reason"`
	}
	res := decodeJSON[notesReason](t, stdout)
	if res.Reason == "" {
		t.Fatalf("reason must explain the empty verdict: %s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("--json already carries the verdict; stderr should stay empty, got:\n%s", stderr)
	}
}

// TestNotesJSONShape: the machine schema is {sections:[{title, lines}]} on one
// line.
func TestNotesJSONShape(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a command palette")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:! drop the broken fallback")
	t.Chdir(dir)

	code, stdout, _ := runGlyph(t, "notes", "--range", base+"..HEAD", "--json")
	if code != 0 {
		t.Fatalf("notes --json exited %d, want 0", code)
	}
	if strings.Count(strings.TrimRight(stdout, "\n"), "\n") != 0 {
		t.Fatalf("notes --json must be a single line, got:\n%s", stdout)
	}
	type notesSections struct {
		Sections []struct {
			Title string   `json:"title"`
			Lines []string `json:"lines"`
		} `json:"sections"`
	}
	res := decodeJSON[notesSections](t, stdout)
	if len(res.Sections) != 2 {
		t.Fatalf("want the Breaking Changes and Features sections, got %s", stdout)
	}
	if res.Sections[0].Title != "Breaking Changes" || res.Sections[1].Title != "Features" {
		t.Fatalf("section order = %q, %q; want Breaking Changes, Features", res.Sections[0].Title, res.Sections[1].Title)
	}
	if len(res.Sections[0].Lines) != 1 || !strings.Contains(res.Sections[0].Lines[0], "drop the broken fallback") {
		t.Fatalf("breaking section lines = %v", res.Sections[0].Lines)
	}
	if !strings.Contains(res.Sections[1].Lines[0], "add a command palette") {
		t.Fatalf("feature section lines = %v", res.Sections[1].Lines)
	}
}

// TestNotesUnknownShortcodeRendersBySigil: a shortcode the dictionary lacks
// still folds by its sigil and renders in its semver section. The unmatched
// case is TestNotesRangeRefusesAMessageNoPatternClaims'.
func TestNotesUnknownShortcodeRendersBySigil(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	const subject = ":not_a_real_code:^ mystery change"
	testutil.Commit(t, dir, "akira-toriyama", subject)
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes over a shortcode the dictionary lacks exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "## Features") || !strings.Contains(stdout, subject) {
		t.Fatalf("the ^ commit must render in Features:\n%s", stdout)
	}
}

// TestNotesRangeRefusesAMessageNoPatternClaims: notes over a --range refuses
// a message no pattern claims — an unlandable claim included — as bump does,
// byte for byte: both read the range through walk.Pending, the one place a
// --range refuses (DESIGN §4.1, rule 3), as the release walk refuses it for
// both. Rendered instead, the commit landed in no section under the preset
// and notes published the range's other notes (0) where bump refuses it (3).
// The range holds a commit that lands in a section because notes' own fold
// runs only to count an empty body's participants (participating), so a
// range with nothing to render would be refused there too. An exclude_authors
// author's message is never judged and still renders through the bot
// fallback (TestNotesSectionsAloneDecide).
func TestNotesRangeRefusesAMessageNoPatternClaims(t *testing.T) {
	for name, message := range map[string]string{
		"no pattern claims it": "no gitmoji in this one",
		"an unlandable claim":  "fixup! :bug:~ fix a crash",
	} {
		t.Run(name, func(t *testing.T) {
			dir, base := testutil.NewTaggedRepo(t)
			testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a command palette")
			testutil.Commit(t, dir, "akira-toriyama", message)
			t.Chdir(dir)

			code, _, want := runGlyph(t, "bump", "--range", base+"..HEAD")
			if code != 3 {
				t.Fatalf("positive control: bump refuses this range at 3, got %d\nstderr: %s", code, want)
			}
			code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
			if code != 3 || stdout != "" || stderr != want {
				t.Fatalf("notes --range exited %d with %q, want 3, nothing on stdout and bump's refusal\n--- notes ---\n%s\n--- bump ---\n%s", code, stdout, stderr, want)
			}
		})
	}
}

// reasonCount reads the number a no-release reason says participate.
func reasonCount(t *testing.T, reason string) int {
	t.Helper()
	m := regexp.MustCompile(`(\d+) commit\(s\) participate in `).FindStringSubmatch(reason)
	if m == nil {
		t.Fatalf("the reason counts no participating commits: %q", reason)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// notesNoReleaseReason runs `notes --json` with args, requires the empty
// verdict (exit 1) and returns its reason.
func notesNoReleaseReason(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := runGlyph(t, append([]string{"notes", "--json"}, args...)...)
	if code != 1 {
		t.Fatalf("notes %v exited %d, want 1 — the case must land nothing in a section\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	type notesReason struct {
		Reason string `json:"reason"`
	}
	res := decodeJSON[notesReason](t, stdout)
	return res.Reason
}

// bumpNoReleaseVerdict runs `bump --json` with args, requires the none
// verdict (exit 1) and returns it — the positive control for what
// "participate" counts.
func bumpNoReleaseVerdict(t *testing.T, args ...string) packagesVerdict {
	t.Helper()
	code, stdout, stderr := runGlyph(t, append([]string{"bump", "--json"}, args...)...)
	if code != 1 {
		t.Fatalf("bump %v exited %d, want 1\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	return decodeJSON[packagesVerdict](t, stdout)
}

// wantBareLineReason requires the single line's spelling of the empty
// reason: it ends at "none lands in a section" — "on any line" is said only
// of lines, and a repository that declares none has no lines to say it of
// (mutation row notes-bare-line-reason-says-on-any-line).
func wantBareLineReason(t *testing.T, reason string) {
	t.Helper()
	if !strings.HasSuffix(reason, "none lands in a section") {
		t.Errorf("the bare line's reason must end at \"none lands in a section\":\n%s", reason)
	}
}

// TestNotesNoReleaseCountsParticipatingCommits: notes' no-release reason says
// "N commit(s) participate", bump's words, so N is bump's set — the commits
// the fold reads: not an exclude_authors author, matched, not claimed by a
// skip pattern (DESIGN §4.1). Each case asks bump over the same input, and
// bump's own answer is what notes must agree with.
//
// Counting the listing it is handed, notes disagrees with bump: over one
// single-line range of two =, a bot and a merge commit, bump says 2 and notes
// 4; over a --since-tag walk of one = and a bot, 2 against bump's 1; and
// under [[packages]], 4 where bump's top-level commits lists 1.
// Mutation row notes-reason-counts-the-raw-listing;
// the packages case is TestNotesPackagesNoReleaseCountsParticipatingCommits.
func TestNotesNoReleaseCountsParticipatingCommits(t *testing.T) {
	t.Run("a range holding a bot and a merge commit", func(t *testing.T) {
		dir, base := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the notes model")
		testutil.Commit(t, dir, "renovate[bot]", "Update dependency x to v2")
		mergeInto(t, dir, "akira-toriyama", "akira-toriyama", "topic", "Merge branch 'topic'", ":memo:= document the sections")
		t.Chdir(dir)

		b := bumpNoReleaseVerdict(t, "--range", base+"..HEAD")
		if n := reasonCount(t, b.Reason); n != 2 || len(b.Commits) != 2 {
			t.Fatalf("positive control: bump reads the two = alone, got %d in %q over %d row(s)", n, b.Reason, len(b.Commits))
		}
		reason := notesNoReleaseReason(t, "--range", base+"..HEAD")
		if reasonCount(t, reason) != 2 {
			t.Errorf("notes counts the bot and the merge commit as participating — bump says 2 of this range:\n%s", reason)
		}
		wantBareLineReason(t, reason)
	})

	t.Run("a since-tag walk", func(t *testing.T) {
		dir, _ := testutil.NewTaggedRepo(t)
		testutil.Commit(t, dir, "akira-toriyama", ":memo:= document the notes model")
		pushed := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Commit(t, dir, "renovate[bot]", "Update dependency x to v2")
		usePR(t, walkServer(t, map[string]string{commitPullsPath(pushed): `[]`}))
		t.Chdir(dir)

		b := bumpNoReleaseVerdict(t, "--since-tag")
		if n := reasonCount(t, b.Reason); n != 1 {
			t.Fatalf("positive control: bump reads the one =, got %d in %q", n, b.Reason)
		}
		reason := notesNoReleaseReason(t, "--since-tag")
		if reasonCount(t, reason) != 1 {
			t.Errorf("notes counts the bot as participating — bump says 1 of this walk:\n%s", reason)
		}
		wantBareLineReason(t, reason)
	})
}

// TestNotesPackagesNoReleaseCountsParticipatingCommits is the per-line arm
// of TestNotesNoReleaseCountsParticipatingCommits, its own test so the
// ledger row for the lines names a test holding only the packages assertion
// (notes-packages-reason-counts-the-raw-walk).
func TestNotesPackagesNoReleaseCountsParticipatingCommits(t *testing.T) {
	dir, base := packagesRepo(t)
	testutil.Touch(t, dir, "akira-toriyama", ":memo:= document the lines", "README.md")
	testutil.Touch(t, dir, "renovate[bot]", "Update dependency x to v2", "curry/go.mod")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Touch(t, dir, "renovate[bot]", "Update dependency y to v3", "haiku/go.mod")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "main")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'topic'", "topic")
	t.Chdir(dir)

	b := bumpNoReleaseVerdict(t, "--range", base+"..HEAD")
	if len(b.Commits) != 1 {
		t.Fatalf("positive control: bump's top-level commits lists the shared = alone, got %d row(s): %+v", len(b.Commits), b.Commits)
	}
	reason := notesNoReleaseReason(t, "--range", base+"..HEAD")
	if reasonCount(t, reason) != 1 || !strings.Contains(reason, "none lands in a section on any line") {
		t.Errorf("notes counts the bots and the merge commit as participating — bump lists 1 commit of this range:\n%s", reason)
	}
}

// TestNotesRequiresRange: notes with no input mode at all is usage. The
// squash-safe inputs ship too (--pr, --since-tag) and cobra requires exactly one
// of the three, so this pins the empty invocation, not an unimplemented feature.
func TestNotesRequiresRange(t *testing.T) {
	if code, _, stderr := runGlyph(t, "notes"); code != 2 {
		t.Fatalf("notes without --range exited %d, want 2\nstderr: %s", code, stderr)
	}
}

// TestNotesOutsideRepoIsAPI: git failures classify as API (exit 4).
func TestNotesOutsideRepoIsAPI(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, stderr := runGlyph(t, "notes", "--range", "main..HEAD"); code != 4 {
		t.Fatalf("notes outside a repo exited %d, want 4\nstderr: %s", code, stderr)
	}
}

// TestNotesCreditsByIdentityNotShape (t-39fy, t-ayhq): the author credit is a
// live mention of the LOGIN GitHub attached to the commit, never of anything
// the commit itself carries. Under --range the API describes nothing, so no
// line pages anyone: the fleet-shaped noreply address (GitEnv's) renders its
// author by name, plain, exactly as a personal address does — the login the
// address spells is typed by whoever configured git, and GitHub attributes
// the commit by the id beside it (glyph-test-2, 2026-11-04: an address
// spelling a nonexistent login was attributed to the account the id named).
// "Saleh" is handle-shaped and is github.com/larrasket, so rendered by shape
// it paged a stranger; the at-sign goes with the mention a name cannot make
// (mutation rows noreply-address-trusted-as-identity,
// author-credit-goes-live-by-shape).
func TestNotesCreditsByIdentityNotShape(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testutil.CommitFrom(t, dir, "Saleh", "root@lr0.org", ":bug:~ fix the parser")
	testutil.CommitFrom(t, dir, "Robert Pająk", "robert@example.com", ":bug:~ fix the exporter")
	testutil.CommitFrom(t, dir, "akira-toriyama", "92862731+refute-a3-nonexistent-zz@users.noreply.github.com", ":bug:~ fix the address")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Fixes\n\n" +
		"- :bug:~ fix a crash akira-toriyama\n" +
		"- :bug:~ fix the parser Saleh\n" +
		"- :bug:~ fix the exporter Robert Pająk\n" +
		"- :bug:~ fix the address akira-toriyama\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesRangeReadsAFinalConflictsLineAsGitDoes: the --range reader hands
// the trailer parser the message as git recorded it, closing newline
// included, so a bare `Conflicts:` line closing a commit is git's ignored
// list here as it is for `%(trailers)` — not a trailer line that lifts a
// 2-of-9 Signed-off-by block over the 25% line (Trent credited, git credits
// nobody) or a one-line block that hides the paragraph above it (Peggy
// dropped, git credits her). `git commit -m` records every message with that
// newline, which is why the fixture needs no verbatim commit. git's own
// answer is read beside glyph's, so the row cannot pin what git does not
// give (measured 2026-11-08, git 2.54.0 and 2.56.0; mutation row
// trailer-block-read-off-the-trimmed-message).
func TestNotesRangeReadsAFinalConflictsLineAsGitDoes(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ a bare Conflicts line under a 2-of-9 sign-off block\n\nbody\n\nSigned-off-by: S <s@x.y>\np1\np2\np3\np4\np5\np6\np7\nCo-authored-by: Trent <t@x.y>\nConflicts:")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ a bare Conflicts paragraph closing the message\n\nbody\n\nCo-authored-by: Peggy <p@x.y>\n\nConflicts:")
	if got := testutil.Git(t, dir, "akira-toriyama", "log", "--reverse", "--format=%(trailers:key=Co-authored-by,valueonly,unfold)", base+"..HEAD"); strings.TrimSpace(got) != "Peggy <p@x.y>" {
		t.Fatalf("git's commit reader credits %q on the fixture, and the test expects Peggy alone — the fixture is wrong, not git", got)
	}
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("notes exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Fixes\n\n" +
		"- :bug:~ a bare Conflicts line under a 2-of-9 sign-off block akira-toriyama\n" +
		"- :bug:~ a bare Conflicts paragraph closing the message akira-toriyama with Peggy\n"
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}

// TestNotesSinceTagCreditsTheListedLogin: on the walk the API describes a
// squash-merged pull's inner commits, and its author.login — not the name —
// is what the credit pages; a commit GitHub mapped to no account (author:
// null) is credited by name, and so is a direct push on the fallback arm,
// whose noreply address the API never described (t-ayhq).
func TestNotesSinceTagCreditsTheListedLogin(t *testing.T) {
	dir, _ := testutil.NewTaggedRepo(t)
	sha := squashCommit(t, dir, "Fix two things", 7)
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix it by direct push")
	direct := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	usePR(t, walkServer(t, map[string]string{
		commitPullsPath(sha): `[` + apiPullRef(7, "2026-09-13T00:00:00Z", sha) + `]`,
		pullCommitsPath(7): `[` +
			apiCommitBy("s1", "larrasket", "Saleh", ":bug:~ fix the parser") + `,` +
			apiCommitBy("s2", "", "Saleh", ":bug:~ fix it from an unmapped address") + `]`,
		commitPullsPath(direct): `[]`,
	}))
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "notes", "--since-tag")
	if code != 0 {
		t.Fatalf("notes --since-tag exited %d, want 0\nstderr: %s", code, stderr)
	}
	want := "## Fixes\n\n" +
		"- :bug:~ fix the parser (#7) @larrasket\n" +
		"- :bug:~ fix it from an unmapped address (#7) Saleh\n" +
		"- :bug:~ fix it by direct push akira-toriyama\n" +
		"\n" + compareLine("v0.1.0", direct)
	if stdout != want {
		t.Fatalf("notes stdout:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
}
