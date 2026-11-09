package gitsource

import (
	"bytes"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestMain holds the developer's own git config out of this package's
// IN-PROCESS git calls too. testutil.GitEnv pins the commands a test runs to
// build its fixture, but ConfigGet and ConfigBoolOrInt run git in this
// process's environment, where ~/.gitconfig answers: measured, a personal
// commit.cleanup=strip failed TestConfigGet ("unset" came back set to strip).
func TestMain(m *testing.M) {
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		os.Setenv(k, os.DevNull)
	}
	os.Exit(m.Run())
}

// TestLogRange: log walks BASE..HEAD oldest→newest carrying SHA, author name,
// parent count, and the verbatim message (subject + body, CJK intact).
func TestLogRange(t *testing.T) {
	dir := testutil.NewRepo(t)
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testutil.Commit(t, dir, "dependabot[bot]", ":arrow_up: bump a dep")
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu\n\nBody line.\n\n---（和訳）\nメニュー追加。")

	got, err := log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Log returned %d commits, want 3: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Message, ":bug: fix a crash") {
		t.Fatalf("commits are not oldest-first: first is %q", got[0].Message)
	}
	for i, c := range got {
		if len(c.SHA) != 40 {
			t.Fatalf("commit %d has a malformed SHA %q", i, c.SHA)
		}
		if c.Parents != 1 {
			t.Fatalf("commit %d has %d parents, want 1", i, c.Parents)
		}
	}
	if got[1].Author != "dependabot[bot]" {
		t.Fatalf("author = %q, want dependabot[bot]", got[1].Author)
	}
	last := got[2].Message
	if !strings.Contains(last, "---（和訳）\nメニュー追加。") {
		t.Fatalf("message body was not carried verbatim: %q", last)
	}
	if !strings.HasPrefix(last, ":sparkles:(ui) add a menu\n\nBody line.") {
		t.Fatalf("subject/body shape lost: %q", last)
	}
}

// TestLogStripsOnlyTheRecordNewline: git records a cleaned message with a
// closing newline that the text the commit-msg hook judges never has —
// cleanup.Apply returns git's message without it — so Message is %B minus
// exactly one trailing newline. A message recorded under --cleanup=verbatim
// keeps what the author wrote, blank lines at the end included (measured:
// `-m 'x\n\n'` records both newlines), so only one may go: two come back as one.
func TestLogStripsOnlyTheRecordNewline(t *testing.T) {
	dir := testutil.NewRepo(t)
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--cleanup=verbatim", "-m", ":memo:= kept as written\n\n")
	got, err := log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Log returned %d commits, want 2", len(got))
	}
	if got[0].Message != ":bug:~ fix a crash" {
		t.Errorf("Message = %q, want the subject without %%B's record newline", got[0].Message)
	}
	if got[1].Message != ":memo:= kept as written\n" {
		t.Errorf("verbatim Message = %q, want exactly one of the author's two trailing newlines removed", got[1].Message)
	}
}

// TestLogKeepsTheRecordedMessageForTheTrailerBlock: Recorded is %B untouched,
// the closing newline git records with — the bytes git's own commit reader
// reads the trailer block off, which notes read it off too (DESIGN §3). The
// newline is load-bearing there: `Conflicts:\n` closing the object is git's
// ignored list and `Conflicts:` closing the input an empty trailer, so a
// Recorded trimmed like Message hands the parser a shape git never sees.
func TestLogKeepsTheRecordedMessageForTheTrailerBlock(t *testing.T) {
	dir := testutil.NewRepo(t)
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash\n\nCo-authored-by: Peggy <p@x.y>\n\nConflicts:")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "--cleanup=verbatim", "-m", ":memo:= kept as written\n\n")
	got, err := log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Log returned %d commits, want 2", len(got))
	}
	if got[0].Recorded != ":bug:~ fix a crash\n\nCo-authored-by: Peggy <p@x.y>\n\nConflicts:\n" {
		t.Errorf("Recorded = %q, want %%B with the newline git records a cleaned message with", got[0].Recorded)
	}
	if got[1].Recorded != ":memo:= kept as written\n\n" {
		t.Errorf("verbatim Recorded = %q, want both of the author's trailing newlines", got[1].Recorded)
	}
	for _, c := range got {
		if c.Recorded != c.Message+"\n" {
			t.Errorf("Recorded %q and Message %q differ by more than the one closing newline", c.Recorded, c.Message)
		}
	}
}

// TestLogReadsAnAuthorNameHoldingTheUnitSeparator: git keeps a U+001F inside
// an author name (it strips crud only at the ends — measured on 2.54), so a
// field separator that byte can spell let the author choose where the next
// field starts. `dependabot[bot]<US>x` then read as dependabot[bot], the email
// as the parents and the parents as the head of the message; the email rows
// below are the ones a parents-shaped check alone lets through (an empty email
// looks like a root commit's parents, a 40-hex one like a parent), measured.
// Every field must come back exactly as git holds it.
func TestLogReadsAnAuthorNameHoldingTheUnitSeparator(t *testing.T) {
	const name = "dependabot[bot]\x1fx"
	for label, email := range map[string]string{
		"an ordinary email":       "t@example.invalid",
		"an empty email":          "",
		"a parents-shaped email":  "0123456789abcdef0123456789abcdef01234567",
		"a separator in the mail": "a\x1fb@example.invalid",
	} {
		t.Run(label, func(t *testing.T) {
			dir := testutil.NewRepo(t)
			base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.CommitFrom(t, dir, name, email, "garbage message\x1fwith a separator of its own")
			got, err := log(context.Background(), dir, base+"..HEAD")
			if err != nil {
				t.Fatalf("Log: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("Log returned %d commits, want 1: %+v", len(got), got)
			}
			c := got[0]
			if c.Author != name || c.Email != email || c.Parents != 1 || c.SHA != testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD") ||
				!strings.HasPrefix(c.Message, "garbage message\x1fwith a separator of its own") {
				t.Fatalf("the record was read shifted: %+v\nwant author %q, email %q, 1 parent, the message verbatim", c, name, email)
			}
		})
	}
}

// TestParseLogRefusesAMisframedRecord: a record whose fields do not land where
// logFormat put them is a git read failure (exit 4), never a commit read with
// its fields shifted — a shifted author is exactly how exclude_authors was
// bypassed. The first row is the positive control: well-framed output parses,
// so a parser that refused everything could not pass this test.
func TestParseLogRefusesAMisframedRecord(t *testing.T) {
	const (
		sha    = "0123456789abcdef0123456789abcdef01234567"
		parent = "89abcdef0123456789abcdef0123456789abcdef"
	)
	record := func(fields ...string) string { return strings.Join(fields, "\x00") + "\x00" }

	got, err := parseLog([]byte(record(sha, "a", "a@example.invalid", parent, ":bug:~ one\n") +
		record(parent, "b", "b@example.invalid", "", ":memo:= two\n")))
	if err != nil || len(got) != 2 || got[0].SHA != sha || got[0].Parents != 1 || got[1].Author != "b" || got[1].Parents != 0 {
		t.Fatalf("well-framed output = %+v, %v; want both records read whole", got, err)
	}

	for name, out := range map[string]string{
		"a name holding a field separator":    record(sha, "dependabot[bot]", "x", "t@example.invalid", parent, "garbage"),
		"a record one field short":            record(sha, "a", "a@example.invalid", ":bug:~ one\n"),
		"a sha that is not an object name":    record("not-a-sha", "a", "a@example.invalid", parent, "m\n"),
		"an abbreviated sha":                  record(sha[:12], "a", "a@example.invalid", "", "m\n"),
		"parents that are not object names":   record(sha, "a", "a@example.invalid", "t@example.invalid", "m\n"),
		"a parent of another hash's length":   record(sha, "a", "a@example.invalid", parent+"00", "m\n"),
		"output that does not close a record": strings.TrimSuffix(record(sha, "a", "a@example.invalid", "", "m\n"), "\x00"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseLog([]byte(out))
			if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("parseLog(%q) = %+v, %v; want a CodeAPI read failure", out, got, err)
			}
		})
	}
}

// TestLogReadsSHA256Repositories: an object name is 64 hex digits there, and
// glyph reads such a repository (lint --range 3 on a violation, bump v0.0.1),
// and a check hard-wired to SHA-1's 40 would refuse every one of its commits.
func TestLogReadsSHA256Repositories(t *testing.T) {
	testutil.RequireGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main", "--object-format=sha256")
	testutil.Commit(t, dir, "akira-toriyama", ":tada:= begin")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	got, err := log(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("Log on a SHA-256 repository: %v", err)
	}
	if len(got) != 2 || len(got[1].SHA) != 64 || got[1].Parents != 1 {
		t.Fatalf("Log on a SHA-256 repository = %+v, want 2 commits with 64-hex names", got)
	}
}

// TestLogReadsSignedCommitsUnderShowSignature: log.showSignature is a display
// setting a signing developer turns on, and under it git prints each
// signature's verdict on stdout AHEAD of the commit's record, --format or not —
// a merged signed tag's verdict too (measured on git 2.54). The record check
// refuses any byte that is not a record, so with that output in the read every
// history read of such a developer failed at 4, where before the check the text
// had sat in the SHA field and every message was still judged. Both readers
// must read the records whole. The raw read first is the positive control: if
// git stops printing there, this test no longer guards anything, and says so.
func TestLogReadsSignedCommitsUnderShowSignature(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.SignCommits(t, dir)
	testutil.Git(t, dir, "akira-toriyama", "config", "log.showSignature", "true")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ add a side feature")
	testutil.Git(t, dir, "akira-toriyama", "tag", "-s", "-m", "a signed tag", "side-v1")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", ":twisted_rightwards_arrows:= merge the signed tag", "side-v1")
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	if raw := testutil.Git(t, dir, "akira-toriyama", "log", "-1", "--format=%H"); strings.HasPrefix(raw, head) || !strings.Contains(raw, "merged tag") {
		t.Fatalf("git printed no signature verdicts ahead of the record under log.showSignature:\n%s\n— the fixture no longer signs or git changed, and this test guards nothing", raw)
	}

	type record struct {
		message string
		parents int
	}
	read := func(got []RawCommit) []record {
		var out []record
		for _, c := range got {
			if !isObjectName(c.SHA) || c.Author != "akira-toriyama" {
				t.Errorf("a record read shifted: %+v", c)
			}
			out = append(out, record{c.Message, c.Parents})
		}
		return out
	}
	got, err := log(context.Background(), dir, root+"..HEAD")
	if err != nil {
		t.Fatalf("Log under log.showSignature: %v", err)
	}
	if want := []record{{":sparkles:^ add a side feature", 1}, {":bug:~ fix a crash", 1}, {":twisted_rightwards_arrows:= merge the signed tag", 2}}; !slices.Equal(read(got), want) || got[2].SHA != head {
		t.Fatalf("Log read %+v, want %v ending at %s", got, want, head)
	}
	got, err = firstParentLog(context.Background(), dir, "HEAD", 3)
	if err != nil {
		t.Fatalf("firstParentLog under log.showSignature: %v", err)
	}
	if want := []record{{":tada:= begin the project", 0}, {":bug:~ fix a crash", 1}, {":twisted_rightwards_arrows:= merge the signed tag", 2}}; !slices.Equal(read(got), want) || got[2].SHA != head {
		t.Fatalf("firstParentLog read %+v, want %v ending at %s", got, want, head)
	}
}

// TestLogReadsUTF8UnderLogOutputEncoding: i18n.logOutputEncoding is a display
// setting too, and git re-encodes what `git log` prints to it (measured on git
// 2.54): under ISO-8859-1 the author and the message arrive as bytes that are
// not UTF-8, and under UTF-16 the whole record does, which parseLog refuses —
// every history read failed at 4. Both readers must hand back the text as
// recorded. The raw read first is the positive control.
func TestLogReadsUTF8UnderLogOutputEncoding(t *testing.T) {
	const author, message = "Zoë Ünïcode", ":bug:~ fix the café crash"
	for _, enc := range []string{"ISO-8859-1", "UTF-16"} {
		t.Run(enc, func(t *testing.T) {
			dir := testutil.NewRepo(t)
			root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.CommitFrom(t, dir, author, "zoe@example.invalid", message)
			testutil.Git(t, dir, "akira-toriyama", "config", "i18n.logOutputEncoding", enc)
			if raw := testutil.Git(t, dir, "akira-toriyama", "log", "-1", "--format=%an"); raw == author {
				t.Fatalf("git printed the author in UTF-8 under i18n.logOutputEncoding=%s — git no longer re-encodes there, and this test guards nothing", enc)
			}

			got, err := log(context.Background(), dir, root+"..HEAD")
			if err != nil {
				t.Fatalf("Log: %v", err)
			}
			if len(got) != 1 || got[0].Author != author || got[0].Message != message {
				t.Fatalf("Log read %+v, want one commit by %q saying %q", got, author, message)
			}
			got, err = firstParentLog(context.Background(), dir, "HEAD", 1)
			if err != nil {
				t.Fatalf("firstParentLog: %v", err)
			}
			if len(got) != 1 || got[0].Author != author || got[0].Message != message {
				t.Fatalf("firstParentLog read %+v, want one commit by %q saying %q", got, author, message)
			}
		})
	}
}

// TestLogMergeParents: a merge commit reports its true parent count.
func TestLogMergeParents(t *testing.T) {
	dir := testutil.NewRepo(t)
	base := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "feat")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix on a branch")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Commit(t, dir, "akira-toriyama", ":memo: docs on main")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'feat'", "feat")

	got, err := log(context.Background(), dir, base+"..HEAD")
	if err != nil {
		t.Fatalf("Log: unexpected error: %v", err)
	}
	var mergeParents int
	for _, c := range got {
		if strings.HasPrefix(c.Message, "Merge ") {
			mergeParents = c.Parents
		}
	}
	if mergeParents != 2 {
		t.Fatalf("merge commit parent count = %d, want 2", mergeParents)
	}
}

// TestLogEmptyRange: an empty range is a successful empty result, not an error.
func TestLogEmptyRange(t *testing.T) {
	dir := testutil.NewRepo(t)
	got, err := log(context.Background(), dir, "HEAD..HEAD")
	if err != nil {
		t.Fatalf("log(HEAD..HEAD): unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("log(HEAD..HEAD) = %+v, want empty", got)
	}
}

// TestLogErrorsAreAPI: a bad revision and a non-repo directory are classified
// as API/git failures (exit 4), and the error carries git's diagnostic.
func TestLogErrorsAreAPI(t *testing.T) {
	dir := testutil.NewRepo(t)
	for name, tc := range map[string]struct{ dir, rng string }{
		"bad revision":                         {dir, "no-such-ref..HEAD"},
		"not a repo":                           {t.TempDir(), "HEAD~1..HEAD"},
		"option-shaped range stays a revision": {dir, "--all"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := log(context.Background(), tc.dir, tc.rng)
			if err == nil {
				t.Fatalf("log(%q, %q) should fail", tc.dir, tc.rng)
			}
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("log(%q, %q) error = %v, want a *core.Error with the API code", tc.dir, tc.rng, err)
			}
		})
	}
}

// TestLogCancelIsInterrupted: a canceled context is the user's own Ctrl-C — the
// one thing CodeInterrupted means (SIGINT/SIGTERM, per the core contract).
func TestLogCancelIsInterrupted(t *testing.T) {
	dir := testutil.NewRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := log(ctx, dir, "HEAD~0..HEAD")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("Log with a canceled context = %v, want CodeInterrupted (%d)", err, core.CodeInterrupted)
	}
}

// TestLogDeadlineIsAPI: a deadline is NOT an interrupt. Only the user aborting
// the run is CodeInterrupted; a caller-imposed timeout firing against a wedged
// git is an ordinary I/O failure and must exit CodeAPI — exiting 130 would tell
// a release job "the operator stopped me" when in truth git hung.
func TestLogDeadlineIsAPI(t *testing.T) {
	dir := testutil.NewRepo(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := log(ctx, dir, "HEAD~0..HEAD")
	ce := core.AsError(err)
	if ce == nil {
		t.Fatalf("Log past its deadline = %v, want a *core.Error", err)
	}
	if ce.Code == core.CodeInterrupted {
		t.Fatalf("a deadline was classified as CodeInterrupted (%d) — that code means the user aborted; want CodeAPI (%d)", core.CodeInterrupted, core.CodeAPI)
	}
	if ce.Code != core.CodeAPI {
		t.Fatalf("Log past its deadline error code = %d, want CodeAPI (%d)", ce.Code, core.CodeAPI)
	}
}

// TestTags: git's --sort=-v:refname order, all tags included — the policy of
// what counts as a version stays with the caller. The order is NOT a version
// ordering and this test must not be read as promising one: it is a REFNAME
// sort, so it orders on the leading byte first and puts every `v…` above every
// `1…`. The fixture here happens to agree because every tag in it starts with v.
// See tags' own doc, and walk.LatestVersionTag, which read an earlier version of
// this comment as a promise and took git's first parseable entry.
// A repo without tags yields an empty list.
func TestTags(t *testing.T) {
	dir := testutil.NewRepo(t)

	got, err := tags(context.Background(), dir)
	if err != nil {
		t.Fatalf("Tags on an untagged repo: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Tags on an untagged repo = %v, want empty", got)
	}

	for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.1.1", "not-a-version"} {
		testutil.Git(t, dir, "akira-toriyama", "tag", tag)
	}
	got, err = tags(context.Background(), dir)
	if err != nil {
		t.Fatalf("Tags: unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Tags = %v, want 4 entries", got)
	}
	if got[0] != "v0.2.0" {
		t.Fatalf("Tags[0] = %q, want v0.2.0 (git's refname order over this all-v fixture)", got[0])
	}
}

// TestMergedTagsListsOnlyTagsRevContains: a tag on a side branch and a tag on
// a non-commit object are no release of HEAD's history; an annotated tag on
// it is, peeled to its commit. The plain listing, beside it, holds all four —
// the positive control that the fixture carries something to leave out.
func TestMergedTagsListsOnlyTagsRevContains(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.0")
	testutil.Git(t, dir, "akira-toriyama", "tag", "-a", "-m", "annotated", "v0.1.1")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "-b", "side")
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles:^ side work")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v9.0.0")
	testutil.Git(t, dir, "akira-toriyama", "checkout", "-q", "main")
	blob := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD:glyph.toml")
	testutil.Git(t, dir, "akira-toriyama", "tag", "v8.0.0", blob)

	all, err := tags(context.Background(), dir)
	if err != nil || len(all) != 4 {
		t.Fatalf("Tags = %v, %v; want all four (the control)", all, err)
	}
	got, err := mergedTags(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("mergedTags: %v", err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"v0.1.0", "v0.1.1"}) {
		t.Fatalf("mergedTags(HEAD) = %v, want [v0.1.0 v0.1.1] — the side branch's v9.0.0 and the blob's v8.0.0 are no release of HEAD", got)
	}
}

// TestMergedTagsOnAnUnbornHEADIsEmpty: before a repository's first commit HEAD
// names no commit, and git answers `tag --merged=HEAD` with "malformed object
// name HEAD" at exit 128 (measured, git 2.54). An unborn history holds no
// tags — tags answers empty there too — so mergedTags does as well; passing
// git's failure through turned `bump --pr` and `preview --pr` in such a
// checkout from exit 0 into exit 4. Outside a repository the listing still
// fails as git/IO: the empty answer is for a rev naming no commit, not for a
// git that could not run.
func TestMergedTagsOnAnUnbornHEADIsEmpty(t *testing.T) {
	testutil.RequireGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "akira-toriyama", "init", "-q", "-b", "main")

	all, err := tags(context.Background(), dir)
	if err != nil || len(all) != 0 {
		t.Fatalf("Tags on an unborn history = %v, %v; want empty, nil (the control)", all, err)
	}
	got, err := mergedTags(context.Background(), dir, "HEAD")
	if err != nil || len(got) != 0 {
		t.Fatalf("mergedTags(HEAD) on an unborn history = %v, %v; want empty, nil", got, err)
	}

	_, err = mergedTags(context.Background(), t.TempDir(), "HEAD")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("mergedTags outside a repository = %v, want CodeAPI", err)
	}
}

// TestMergedTagsFailsOnARevGitCannotRead: the empty answer is for a rev naming
// no commit, and a rev whose commit this repository holds and cannot read
// names one. Read as empty, a corrupt HEAD said the history holds no release
// and the walk stepped from v0.0.0 with a nil error. The failure carried is
// the listing's own (`git tag`), as isAncestor carries merge-base's.
func TestMergedTagsFailsOnARevGitCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		corrupt     func(t *testing.T, dir, sha string)
		wantMsg     string
		needsSignal bool
	}{
		"an empty object file":                                {emptyObject, "is empty", false},
		"an object file git cannot open":                      {unreadableObject, "git tag", false},
		"an object under a fan-out directory git cannot open": {unreadableFanout, "Permission denied", true},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.needsSignal {
				requireUnopenableSignal(t)
			}
			dir := testutil.NewRepo(t)
			testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.0")
			tc.corrupt(t, dir, testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD"))

			got, err := mergedTags(context.Background(), dir, "HEAD")
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("mergedTags over %s at HEAD = %v, %v; want CodeAPI carrying git's %q", name, got, err, tc.wantMsg)
			}
		})
	}
}

// TestHeadTreesListsWhatHEADRecordsAsADirectory: the package-paths check asks
// git, not the filesystem, whether a declared path is a subtree, because
// attribution reads git's path strings (t-fdd8 (1)). So the listing must be
// every tree HEAD records at any depth, byte for byte, and nothing else: a
// symlink to a directory is a 120000 blob, a directory on disk HEAD does not
// hold is not tracked, and asking from a subdirectory still answers for the
// whole tree (ls-tree without --full-tree lists only cwd's part, relative to
// it). A submodule's gitlink IS listed: attribution's owner matches a file
// equal to a package path, so a path naming a submodule claims its bumps, and
// a listing narrowed to trees would fail a path attribution still carries. An
// unborn HEAD records no tree to answer from, which is an error the caller
// renders as could-not-run.
func TestHeadTreesListsWhatHEADRecordsAsADirectory(t *testing.T) {
	dir := testutil.NewRepo(t)
	gitlink := "160000," + testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD") + ",sub"
	writeFiles(t, dir, "haiku/a", "travel/onsen/b")
	if err := os.Symlink("haiku", filepath.Join(dir, "currylink")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "akira-toriyama", "add", "-A")
	testutil.Git(t, dir, "akira-toriyama", "update-index", "--add", "--cacheinfo", gitlink)
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= lay out the lines")
	if err := os.MkdirAll(filepath.Join(dir, "untracked"), 0o750); err != nil {
		t.Fatal(err)
	}

	for _, from := range []string{dir, filepath.Join(dir, "travel")} {
		got, err := HeadTrees(context.Background(), from)
		if err != nil {
			t.Fatalf("HeadTrees(%s): %v", from, err)
		}
		if want := []string{"haiku", "sub", "travel", "travel/onsen"}; !slices.Equal(got, want) {
			t.Errorf("HeadTrees(%s) = %q, want %q — HEAD's trees at any depth and its gitlink, from the top, and no symlink or untracked directory", from, got, want)
		}
	}

	unborn := t.TempDir()
	testutil.Git(t, unborn, "akira-toriyama", "init", "-q", "-b", "main")
	if got, err := HeadTrees(context.Background(), unborn); err == nil {
		t.Errorf("HeadTrees on an unborn HEAD = %q, nil; want an error — no tree was recorded to answer from", got)
	}
	_, err := HeadTrees(context.Background(), t.TempDir())
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("HeadTrees outside a repository = %v, want CodeAPI", err)
	}
}

// TestDefaultBranchReadsARemoteNoRefCanNameAsUnrecorded: "" means nothing
// records the remote's default branch, and a remote whose name git's refs
// refuse (`remote.mirror..x.url` — config accepts it, `git remote add` does
// not) can have nothing recorded. git answers the two differently — an absent
// ref exits 1, a name no ref can carry exits 128 "No such ref" (measured on
// git 2.54) — and read as a failure the second made every caller's "nothing
// recorded" arm unreachable for that remote. A read that really fails, outside
// a repository, is still an error.
func TestDefaultBranchReadsARemoteNoRefCanNameAsUnrecorded(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	for remote, want := range map[string]string{"origin": "main", "upstream": "", "mirror..x": ""} {
		got, err := DefaultBranch(context.Background(), dir, remote)
		if err != nil || got != want {
			t.Errorf("DefaultBranch(%q) = %q, %v; want %q, nil", remote, got, err, want)
		}
	}

	_, err := DefaultBranch(context.Background(), t.TempDir(), "origin")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("DefaultBranch outside a repository = %v, want CodeAPI", err)
	}
}

// TestDefaultBranchReadsADanglingRefAsUnrecorded: a default branch renamed on
// the remote leaves refs/remotes/origin/HEAD naming a ref `git fetch --prune`
// just deleted, and symbolic-ref still answers exit 0 with the old name, so the
// name is verified before it is believed. Written by real git on a real rename:
// forged by hand this would test glyph against glyph. set-head is the positive
// control, the remedy the callers name. A verify that really fails is an error,
// never "unrecorded".
func TestDefaultBranchReadsADanglingRefAsUnrecorded(t *testing.T) {
	remote := testutil.NewRepo(t)
	work := filepath.Join(t.TempDir(), "work")
	testutil.Git(t, remote, "akira-toriyama", "clone", "-q", remote, work)
	ctx := context.Background()
	if got, err := DefaultBranch(ctx, work, "origin"); err != nil || got != "main" {
		t.Fatalf("DefaultBranch on a fresh clone = %q, %v; want main", got, err)
	}

	testutil.DangleOriginHead(t, remote, work)
	if got, err := DefaultBranch(ctx, work, "origin"); err != nil || got != "" {
		t.Errorf("DefaultBranch over a dangling origin/HEAD = %q, %v; want \"\", nil — the name is a ref that no longer exists", got, err)
	}

	testutil.Git(t, work, "akira-toriyama", "remote", "set-head", "origin", "-a")
	if got, err := DefaultBranch(ctx, work, "origin"); err != nil || got != "trunk" {
		t.Errorf("DefaultBranch after set-head = %q, %v; want trunk", got, err)
	}

	corrupt := testutil.NewRepo(t)
	testutil.Git(t, corrupt, "akira-toriyama", "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.Git(t, corrupt, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	breakObject(t, corrupt, testutil.Git(t, corrupt, "akira-toriyama", "rev-parse", "HEAD"))
	_, err := DefaultBranch(ctx, corrupt, "origin")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Errorf("DefaultBranch over a corrupt target = %v, want CodeAPI", err)
	}
}

// TestDefaultBranchFailsOnATargetGitCannotRead: the recorded name's commit is
// in the store and git cannot read it — the shapes a crash, a full disk, a
// permission slip and a bad write leave — and that is git failing, not a name
// that holds nothing. Read as unrecorded, the hook named set-head over an
// object store set-head cannot repair, and doctor rendered advice at exit 0
// where the read must exit 4. The error carries git's own diagnosis where git
// wrote one, and names the ref where it did not (the object file git cannot
// open, under a git that writes no error: line for it: `cat-file -e` is the
// signal there). Each row starts from a target that reads (the control).
func TestDefaultBranchFailsOnATargetGitCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		corrupt     func(t *testing.T, dir, sha string) (target string)
		wantMsg     func(t *testing.T) string
		needsSignal bool
	}{
		"an empty object file": {func(t *testing.T, dir, sha string) string {
			emptyObject(t, dir, sha)
			return sha
		}, literally("is empty"), false},
		"an object file git cannot open": {func(t *testing.T, dir, sha string) string {
			unreadableObject(t, dir, sha)
			return sha
		}, unopenableFileMsg, false},
		"an object under a fan-out directory git cannot open": {func(t *testing.T, dir, sha string) string {
			unreadableFanout(t, dir, sha)
			return sha
		}, literally("Permission denied"), true},
		"a commit git cannot parse": {func(t *testing.T, dir, _ string) string {
			return unparseableCommit(t, dir)
		}, literally("bogus commit object"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.needsSignal {
				requireUnopenableSignal(t)
			}
			wantMsg := tc.wantMsg(t)
			dir := testutil.NewRepo(t)
			head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Git(t, dir, "akira-toriyama", "update-ref", "refs/remotes/origin/main", head)
			testutil.Git(t, dir, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
			if got, err := DefaultBranch(context.Background(), dir, "origin"); err != nil || got != "main" {
				t.Fatalf("DefaultBranch over a readable target = %q, %v; want main (the control)", got, err)
			}

			// update-ref parses the object it is pointed at and refuses the
			// unparseable commit (exit 128, measured on git 2.54), so a target
			// other than head is written into the loose ref by hand — the bytes a
			// fetch would have left before the object went bad.
			if target := tc.corrupt(t, dir, head); target != head {
				if err := os.WriteFile(filepath.Join(dir, ".git", "refs", "remotes", "origin", "main"), []byte(target+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := DefaultBranch(context.Background(), dir, "origin")
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("DefaultBranch over %s = %q, %v; want CodeAPI — unrecorded names set-head as the remedy, and set-head repairs no object store", name, got, err)
			}
			if !strings.Contains(err.Error(), wantMsg) {
				t.Errorf("the error must carry git's own diagnosis: got %q, want it to mention %q", err, wantMsg)
			}
		})
	}
}

// literally is a wantMsg that is the same under every git.
func literally(msg string) func(*testing.T) string {
	return func(*testing.T) string { return msg }
}

// requireUnopenableSignal skips a row whose shape gives glyph no signal under
// the git on PATH: git ≤ 2.39 writes no error: line for a loose object it
// cannot open, and the only other probe, `cat-file -e`, is a stat that cannot
// see through a fan-out directory it cannot open — so that shape reads as
// absent there, a known limit DESIGN §4 states rather than a decision a test
// can hold (testutil.GitSignalsUnopenableObject).
func requireUnopenableSignal(t *testing.T) {
	t.Helper()
	if !testutil.GitSignalsUnopenableObject(t) {
		t.Skip("this git writes no error: line for a loose object it cannot open (git ≤ 2.39), so the shape reads as absent: a known limit, DESIGN §4")
	}
}

// unopenableFileMsg is what a namesCommit caller's error says about a loose
// object file git cannot open: git's own "Permission denied" where git writes
// the error: line (≥ 2.40), and glyph's sentence naming the ref where git
// writes nothing and `cat-file -e` is the signal (≤ 2.39).
func unopenableFileMsg(t *testing.T) string {
	t.Helper()
	if testutil.GitSignalsUnopenableObject(t) {
		return "Permission denied"
	}
	return "holds and cannot read"
}

// TestNamesCommitSeesAnUnopenableObjectGitIsSilentAbout: git ≤ 2.39 dies 128
// on a loose object it cannot open with no error: line — the same stderr as
// for a sha the store does not hold (measured on 2.39.5: `fatal: Not a valid
// object name` alone, where 2.40.1 and later put `error: unable to open loose
// object …: Permission denied` above it). `cat-file -e`, a stat, answers 0
// for that file and 1 for the absent sha, and is namesCommit's second signal.
// Held on every host through a git that is the real one with its error: lines
// taken off cat-file's stderr — the only difference between 2.39's stderr and
// 2.56's for this shape — so the row bites under a git that writes the line;
// the rows of TestDefaultBranchFailsOnATargetGitCannotRead and its siblings
// cover the same arm under a real 2.39.
func TestNamesCommitSeesAnUnopenableObjectGitIsSilentAbout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper git is a shell script")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	dir := testutil.NewRepo(t)
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "update-ref", "refs/remotes/origin/main", head)
	testutil.Git(t, dir, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	absent := "0123456789abcdef0123456789abcdef01234567"

	bin := t.TempDir()
	// Run the real git, drop the error: lines from cat-file's stderr, exit as
	// it did.
	wrapper := "#!/bin/sh\ncase \"$*\" in *cat-file*) err=$(mktemp); " + realGit + " \"$@\" 2>\"$err\"; rc=$?; grep -v '^error:' \"$err\" >&2; rm -f \"$err\"; exit $rc;; esac\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o700); err != nil { //nolint:gosec // a wrapper git must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if on, err := isAncestor(context.Background(), dir, head, "HEAD"); err != nil || !on {
		t.Fatalf("isAncestor through the wrapper over a readable store = %v, %v; want true, nil (the control)", on, err)
	}
	if on, err := isAncestor(context.Background(), dir, absent, "HEAD"); err != nil || on {
		t.Fatalf("isAncestor through the wrapper for an absent sha = %v, %v; want false, nil (the control: cat-file -e answers 1 there)", on, err)
	}

	unreadableObject(t, dir, head)
	got, err := DefaultBranch(context.Background(), dir, "origin")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), "holds and cannot read") {
		t.Errorf("DefaultBranch over an unopenable target through a git that says nothing = %q, %v; want CodeAPI naming the held object — read as unrecorded, the hook names set-head over a broken object store", got, err)
	}
	on, err := isAncestor(context.Background(), dir, head, "HEAD")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || on {
		t.Errorf("isAncestor over an unopenable commit through a git that says nothing = %v, %v; want false, CodeAPI", on, err)
	}
}

// TestDefaultBranchFailsOnADanglingRefOverAGoneAlternatesStore: over a store
// whose alternates directory is gone, git 2.56 writes its error: line on a
// dangling name's failed peel too, where 2.54.0 and 2.39.5 write it only where
// an object is looked up (measured). namesCommit reads that line before
// rev-parse's NO: a store git says is not all there cannot vouch that a name
// holds nothing, so a dangling origin/HEAD there is git's failure at 4, not
// the set-head advice — set-head records a name and repairs no store. A
// recorded name whose commit the store holds still reads, the peel answering
// beside the line. Held on every host through a git that is the real one with
// 2.56's line written ahead of cat-file's stderr; the dangling ref over the
// whole store is the control.
func TestDefaultBranchFailsOnADanglingRefOverAGoneAlternatesStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper git is a shell script")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	ctx := context.Background()
	dir := testutil.NewRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if got, err := DefaultBranch(ctx, dir, "origin"); err != nil || got != "" {
		t.Fatalf("DefaultBranch over a dangling origin/HEAD = %q, %v; want \"\", nil (the control)", got, err)
	}

	gone := goneAlternates(t, dir)
	bin := t.TempDir()
	// Write 2.56's line for the gone directory ahead of cat-file's stderr, then
	// run the real git.
	wrapper := "#!/bin/sh\ncase \"$*\" in *cat-file*) echo 'error: object directory " + gone + " does not exist; check .git/objects/info/alternates' >&2;; esac\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o700); err != nil { //nolint:gosec // a wrapper git must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := DefaultBranch(ctx, dir, "origin")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), gone+" does not exist") {
		t.Errorf("DefaultBranch over a dangling origin/HEAD, alternates gone = %q, %v; want CodeAPI quoting git's line — read as unrecorded, the hook and doctor name set-head over a store git says is not all there", got, err)
	}
	testutil.Git(t, dir, "akira-toriyama", "update-ref", "refs/remotes/origin/main", "HEAD")
	if got, err := DefaultBranch(ctx, dir, "origin"); err != nil || got != "main" {
		t.Errorf("DefaultBranch over a recorded name whose commit the store holds, alternates gone = %q, %v; want main, nil — nothing was absent", got, err)
	}
}

// breakObject overwrites one loose object with bytes that are no zlib stream.
// Fixture commits stay loose: GitEnv pins gc.auto off.
func breakObject(t *testing.T, dir, sha string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "objects", sha[:2], sha[2:])
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod the loose object: %v", err)
	}
	if err := os.WriteFile(path, []byte("this is no zlib stream"), 0o600); err != nil {
		t.Fatalf("break the loose object: %v", err)
	}
}

// emptyObject truncates one loose object to no bytes — what a crash or a full
// disk leaves ("object file … is empty").
func emptyObject(t *testing.T, dir, sha string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "objects", sha[:2], sha[2:])
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod the loose object: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("empty the loose object: %v", err)
	}
}

// unreadableObject takes every permission off one loose object: git can stat
// it, so the object is held, and cannot open it. Root opens it anyway.
func unreadableObject(t *testing.T, dir, sha string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	if err := os.Chmod(filepath.Join(dir, ".git", "objects", sha[:2], sha[2:]), 0); err != nil {
		t.Fatalf("chmod the loose object: %v", err)
	}
}

// unreadableFanout takes every permission off the fan-out directory holding
// one loose object — the shape git running as another user under umask 077
// leaves (a sudo fetch, a root container writing into a bind-mounted
// checkout). git cannot stat the object, so `cat-file -e` reads it as absent,
// and cannot open it, so every read says "Permission denied". Restored on
// cleanup: a mode 000 directory cannot be removed with the TempDir.
func unreadableFanout(t *testing.T, dir, sha string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root opens a mode 000 directory")
	}
	fanout := filepath.Join(dir, ".git", "objects", sha[:2])
	if err := os.Chmod(fanout, 0); err != nil {
		t.Fatalf("chmod the fan-out directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(fanout, 0o755) })
}

// truncatedObject cuts one loose object to its first twelve bytes — a write
// that stopped partway, leaving a zlib stream whose header never completes.
func truncatedObject(t *testing.T, dir, sha string) {
	t.Helper()
	path := filepath.Join(dir, ".git", "objects", sha[:2], sha[2:])
	whole, err := os.ReadFile(path) //nolint:gosec // a fixture path the test built
	if err != nil {
		t.Fatalf("read the loose object: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod the loose object: %v", err)
	}
	if err := os.WriteFile(path, whole[:12], 0o600); err != nil {
		t.Fatalf("truncate the loose object: %v", err)
	}
}

// goneAlternates points the store at an object directory that does not exist
// — a clone made with --reference to a mirror since deleted — and returns that
// directory. git writes an error: line on every object read and still answers.
func goneAlternates(t *testing.T, dir string) (gone string) {
	t.Helper()
	info := filepath.Join(dir, ".git", "objects", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	gone = filepath.Join(t.TempDir(), "gone")
	if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return gone
}

// unparseableCommit writes an object whose header names a commit and whose
// body is none (`hash-object -t commit --literally`), and returns its sha.
// `cat-file --batch-check` lists it as a commit; parsing it is "bogus commit
// object".
func unparseableCommit(t *testing.T, dir string) string {
	t.Helper()
	body := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(body, []byte("not a commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return testutil.Git(t, dir, "akira-toriyama", "hash-object", "-t", "commit", "--literally", "-w", body)
}

// TestRemoteURLsKeepsASpacedRemoteName: a remote name may hold a space —
// `[remote "my fork"]` in a hand-edited config, which git reads — and
// `--get-regexp`'s line form separates key and value with a space too, so
// cutting at the first one names the remote `my` and gives it the URL
// `fork.url https://…`, and doctor's pre-push advice then tells the user to
// run `git remote set-head my -a` on a remote that does not exist. The
// NUL-framed form is unambiguous: `key\nvalue\0`.
func TestRemoteURLsKeepsASpacedRemoteName(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "config", "remote.origin.url", "https://example.invalid/o/r.git")
	testutil.Git(t, dir, "akira-toriyama", "config", "remote.my fork.url", "https://example.invalid/me/r.git")

	got, err := RemoteURLs(context.Background(), dir)
	if err != nil {
		t.Fatalf("RemoteURLs: %v", err)
	}
	want := map[string]string{
		"origin":  "https://example.invalid/o/r.git",
		"my fork": "https://example.invalid/me/r.git",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("RemoteURLs = %q, want %q", got, want)
	}
}

// TestRemoteCanRecordHeadAsksGit: a name git's refs refuse can record no
// default branch, so `git remote set-head` is no remedy for it (DESIGN §7). A
// plain name can; a space or `..` cannot.
func TestRemoteCanRecordHeadAsksGit(t *testing.T) {
	dir := testutil.NewRepo(t)
	for remote, want := range map[string]bool{"origin": true, "my-fork": true, "my fork": false, "mirror..x": false} {
		got, err := RemoteCanRecordHead(context.Background(), dir, remote)
		if err != nil || got != want {
			t.Errorf("RemoteCanRecordHead(%q) = %v, %v; want %v, nil", remote, got, err, want)
		}
	}
}

// TestActionFilesListsWhatGitCounts pins the pin scan's file set (DESIGN §7,
// D2b): every action.yml / action.yaml git counts as part of the checkout, at
// any path — tracked (an initialized submodule's tracked files included) plus
// untracked files no ignore rule excludes — and nothing the filesystem merely
// holds. A submodule's composite is in scope by the same principle;
// `--recurse-submodules` cannot be combined with --others (git: "unsupported
// mode"), hence two listings. A tracked file deleted in the working tree is
// still listed (the caller reads its absence as observed); a sparse-checkout
// entry is listed and flagged, because its bytes were never put on disk to
// read.
func TestActionFilesListsWhatGitCounts(t *testing.T) {
	sub := testutil.NewRepo(t)
	writeFiles(t, sub, "inst/action.yml")
	testutil.Git(t, sub, "akira-toriyama", "add", "-A")
	testutil.Git(t, sub, "akira-toriyama", "commit", "-q", "-m", ":tada:= a shared composite")

	dir := testutil.NewRepo(t)
	writeFiles(t, dir, "action.yml", ".github/actions/a/action.yml", "tools/installer/action.yaml",
		"docs/action.yml.md", "tools/sparse/action.yml", "tools/gone/action.yml", "build/dep/action.yml")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "akira-toriyama", "add", "-A")
	testutil.Git(t, dir, "akira-toriyama", "-c", "protocol.file.allow=always", "submodule", "--quiet", "add", sub, ".github/actions/shared")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= lay out the actions")
	writeFiles(t, dir, "untracked/action.yml")
	testutil.Git(t, dir, "akira-toriyama", "update-index", "--skip-worktree", "tools/sparse/action.yml")
	for _, gone := range []string{"tools/sparse/action.yml", "tools/gone/action.yml"} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(gone))); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ActionFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("ActionFiles: %v", err)
	}
	want := []ActionFile{
		{Path: ".github/actions/a/action.yml"},
		{Path: ".github/actions/shared/inst/action.yml"},
		{Path: "action.yml"},
		{Path: "tools/gone/action.yml"},
		{Path: "tools/installer/action.yaml"},
		{Path: "tools/sparse/action.yml", SkipWorktree: true},
		{Path: "untracked/action.yml"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ActionFiles =\n  %+v\nwant\n  %+v\n(no ignored build/dep/action.yml, no docs/action.yml.md; the submodule's composite and the untracked one in)", got, want)
	}

	_, err = ActionFiles(context.Background(), t.TempDir())
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("ActionFiles outside a repository = %v, want CodeAPI", err)
	}
}

// writeFiles writes a one-line file at each slash path under dir.
func writeFiles(t *testing.T, dir string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		testutil.WriteFile(t, dir, rel, rel+"\n")
	}
}

// TestIsTagIsAnExactRef: isTag answers whether refs/tags/<name> exists, and
// nothing else names one — a branch, a sha, and a revision expression over a
// real tag (`v0.1.0~0`, which rev-parse would resolve to the tag's commit)
// are all NO, as an answer rather than an error; an annotated, prefixed tag
// is a YES. Outside a repository it fails as git/IO.
func TestIsTagIsAnExactRef(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Git(t, dir, "akira-toriyama", "tag", "v0.1.0")
	testutil.Git(t, dir, "akira-toriyama", "tag", "-a", "-m", "annotated", "curry/v0.1.0")
	testutil.Git(t, dir, "akira-toriyama", "branch", "old-main")
	sha := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	for name, want := range map[string]bool{
		"v0.1.0":       true,
		"curry/v0.1.0": true,
		"old-main":     false,
		sha:            false,
		"v0.1.0~0":     false,
		"v0.1.0^{}":    false,
		"v0.2.0":       false,
	} {
		got, err := isTag(context.Background(), dir, name)
		if err != nil || got != want {
			t.Errorf("isTag(%q) = %v, %v; want %v, nil", name, got, err, want)
		}
	}

	_, err := isTag(context.Background(), t.TempDir(), "v0.1.0")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("isTag outside a repository = %v, want CodeAPI", err)
	}
}

// TestTagListingsStayOnePerLineUnderColumnConfig: column.ui=always — and
// column.tag=always, the same switch for this one command — lays `git tag
// --list` out in columns even into a pipe (measured on git 2.54), so three tags
// came back as one line and were read as one name. The raw read first is the
// positive control.
func TestTagListingsStayOnePerLineUnderColumnConfig(t *testing.T) {
	for _, key := range []string{"column.ui", "column.tag"} {
		t.Run(key, func(t *testing.T) {
			dir := testutil.NewRepo(t)
			for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.1.1"} {
				testutil.Git(t, dir, "akira-toriyama", "tag", tag)
			}
			testutil.Git(t, dir, "akira-toriyama", "config", key, "always")
			if raw := testutil.Git(t, dir, "akira-toriyama", "tag", "--list"); strings.Contains(raw, "\n") {
				t.Fatalf("git listed the tags one per line under %s=always:\n%s\n— git no longer columns there, and this test guards nothing", key, raw)
			}

			want := []string{"v0.2.0", "v0.1.1", "v0.1.0"}
			got, err := tags(context.Background(), dir)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("Tags = %q, %v; want %q", got, err, want)
			}
			got, err = mergedTags(context.Background(), dir, "HEAD")
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("mergedTags(HEAD) = %q, %v; want %q", got, err, want)
			}
		})
	}
}

// TestHead returns the checkout's HEAD sha — what the rolling draft's
// target_commitish records so the eventual Publish tags the commit the
// verdict was computed at, not whatever main has moved to since.
func TestHead(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	want := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	got, err := Head(context.Background(), dir)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got != want {
		t.Fatalf("Head = %q, want %q", got, want)
	}
}

// TestHeadErrorsAreAPI: a directory that is not a repository fails as an
// ordinary git/IO error (4) carrying git's own fatal line.
func TestHeadErrorsAreAPI(t *testing.T) {
	testutil.RequireGit(t)
	_, err := Head(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("want an error outside a repository")
	}
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("error = %v, want CodeAPI", err)
	}
}

// TestHave: the repository's own commits come back, and everything else — a
// well-formed sha nobody has, a tree that is not a commit, outright garbage —
// is simply absent rather than an error. The listing have is asked about is
// GitHub's, so it routinely names commits that exist in no clone.
func TestHave(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	tree := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD^{tree}")
	absent := "0123456789abcdef0123456789abcdef01234567"

	got, err := have(context.Background(), dir, []string{root, head, tree, absent, "not-a-sha"})
	if err != nil {
		t.Fatalf("Have: %v", err)
	}
	for _, sha := range []string{root, head} {
		if !got[sha] {
			t.Fatalf("Have does not report %.7s, which this repository holds", sha)
		}
	}
	for name, sha := range map[string]string{"a tree": tree, "an absent commit": absent, "garbage": "not-a-sha"} {
		if got[sha] {
			t.Fatalf("Have reports %s (%q) as a commit this repository holds", name, sha)
		}
	}
}

// TestHaveEmpty: no shas is a successful empty answer and must not shell out —
// the release walk calls this per pull request, and a squash-merged one whose
// listing the API truncated to nothing would otherwise pay for a git process.
func TestHaveEmpty(t *testing.T) {
	got, err := have(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("have(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("have(nil) = %v, want empty", got)
	}
}

// TestHaveFailsOnAHeldObjectGitCannotRead: `cat-file --batch-check` answers
// `missing` at exit 0 for a held object it cannot read exactly as for a sha the
// store does not hold, and writes an error: line only for the first. Read as
// absent, a released commit the clone holds broken has no landing site and the
// walk counts it again (TestSinceTagFailsOnAReleasedCommitGitCannotRead, the
// shape end to end). Every shape here is a loose object the store holds, plus
// a store whose alternates directory is gone — git can vouch for nothing it
// does not find there. Each row starts from a store that reads (the control).
// The two permission shapes are the ones git ≤ 2.39 writes nothing for, and
// have has no second signal, so they skip there (DESIGN §4, the known limit).
func TestHaveFailsOnAHeldObjectGitCannotRead(t *testing.T) {
	absent := "0123456789abcdef0123456789abcdef01234567"
	for name, tc := range map[string]struct {
		corrupt     func(t *testing.T, dir, sha string)
		wantMsg     string
		needsSignal bool
	}{
		"an empty object file":                                {emptyObject, "is empty", false},
		"an object file that is no zlib stream":               {breakObject, "git cat-file", false},
		"a truncated object file":                             {truncatedObject, "git cat-file", false},
		"an object file git cannot open":                      {unreadableObject, "Permission denied", true},
		"an object under a fan-out directory git cannot open": {unreadableFanout, "Permission denied", true},
		"an alternates directory that is gone": {func(t *testing.T, dir, _ string) {
			goneAlternates(t, dir)
		}, "alternates", false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.needsSignal {
				requireUnopenableSignal(t)
			}
			dir := testutil.NewRepo(t)
			root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Commit(t, dir, "akira-toriyama", ":bug: second")
			head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			if got, err := have(context.Background(), dir, []string{root, head, absent}); err != nil || !got[root] || !got[head] || got[absent] {
				t.Fatalf("have over a readable store = %v, %v; want both commits and not the absent sha (the control)", got, err)
			}

			tc.corrupt(t, dir, root)
			got, err := have(context.Background(), dir, []string{root, head, absent})
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("have over %s = %v, %v; want CodeAPI — read as absent, the walk counts a released commit again", name, got, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("the error must carry git's own diagnosis: got %q, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

// TestHeldObjectSignalIgnoresStderrNoise: the signal for a held object git
// cannot read is the error: severity, not stderr being non-empty. Trace output
// and git's warning: about a ref named as a full sha (with its advice
// paragraph) are stderr on a store that reads every object, and an absent sha
// stays absent under both — for have, which reads the batch's stderr, and for
// isAncestor, which reads merge-base's on its honest no and the probe's
// through namesCommit on its 128.
func TestHeldObjectSignalIgnoresStderrNoise(t *testing.T) {
	absent := "0123456789abcdef0123456789abcdef01234567"
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	testutil.Commit(t, dir, "akira-toriyama", ":memo: a commit off the released branch")
	offBranch := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Git(t, dir, "akira-toriyama", "branch", absent)
	testutil.Git(t, dir, "akira-toriyama", "branch", root)
	t.Setenv("GIT_TRACE", "1")

	got, err := have(context.Background(), dir, []string{root, absent})
	if err != nil || !got[root] || got[absent] {
		t.Fatalf("have under trace output and an ambiguous refname = %v, %v; want the held commit alone, nil", got, err)
	}
	for name, sha := range map[string]string{"an absent sha": absent, "an ancestor": root, "another line of history": offBranch} {
		on, err := isAncestor(context.Background(), dir, sha, "HEAD")
		if err != nil || on != (sha == root) {
			t.Errorf("isAncestor(%s) under trace output and an ambiguous refname = %v, %v; want %v, nil", name, on, err, sha == root)
		}
	}
}

// TestHeldObjectSignalIsFoundBehindStderrNoise is the other direction: under
// GIT_TRACE the trace lines come first on stderr, and the error: line git
// writes for a held object it cannot read sits below them. A reader that
// looked at the first line alone would read the object as absent and the walk
// would count it again, with the trace on.
func TestHeldObjectSignalIsFoundBehindStderrNoise(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: second")
	t.Setenv("GIT_TRACE", "1")
	emptyObject(t, dir, root)

	_, err := have(context.Background(), dir, []string{root})
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("have over an empty object under trace output = %v; want CodeAPI carrying git's \"is empty\" — the error: line is below the trace lines", err)
	}
	on, err := isAncestor(context.Background(), dir, root, "HEAD")
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || on {
		t.Errorf("isAncestor over an empty object under trace output = %v, %v; want false, CodeAPI", on, err)
	}
}

// TestHaveUnderGoneAlternatesAnswersWhereNothingIsMissing: a store whose
// alternates directory is gone writes git's error: line on every object read,
// and have fails on it only beside a `missing` — an absence is what such a
// store cannot vouch for; a listing it holds in full it answers. The second
// read is the control that the store does write the line.
func TestHaveUnderGoneAlternatesAnswersWhereNothingIsMissing(t *testing.T) {
	absent := "0123456789abcdef0123456789abcdef01234567"
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: second")
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	goneAlternates(t, dir)

	got, err := have(context.Background(), dir, []string{root, head})
	if err != nil || !got[root] || !got[head] {
		t.Errorf("have over a listing the store holds in full, under gone alternates = %v, %v; want both commits, nil — nothing is missing, so there is no absence to vouch for", got, err)
	}
	_, err = have(context.Background(), dir, []string{root, head, absent})
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), "alternates") {
		t.Fatalf("have with a sha the store does not hold, under gone alternates = %v; want CodeAPI naming the alternates (the control)", err)
	}
}

// TestHaveCancelIsInterrupted is TestIsAncestorCancelIsInterrupted's shape for
// the batch read: the fake git announces itself and blocks, the cancel lands
// while it runs, and the killed child comes back as an *exec.ExitError. Read
// status first, that is "git cat-file: signal: killed" at exit 4 — the code
// the fleet's wrappers retry — on a run the operator stopped.
func TestHaveCancelIsInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub git is a shell script")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	started := filepath.Join(dir, "started")
	stub := "#!/bin/sh\ntouch " + started + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o700); err != nil { //nolint:gosec // a stub git must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	_, err := have(ctx, dir, []string{"0123456789abcdef0123456789abcdef01234567"})
	if _, serr := os.Stat(started); serr != nil {
		t.Fatalf("the fake git never started, so the cancel landed before the child and the run proves nothing: %v", serr)
	}
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("have cancelled mid-run = %v, want CodeInterrupted (%d) — a killed child arrives as an *exec.ExitError, so reading the exit status before the context turns the abort into git failing at 4", err, core.CodeInterrupted)
	}
}

// TestHeldObjectSignalIsReadUnderThePinnedLocale: errorLine reads git's
// severity prefix, and git translates the prefix with the message — `Fehler:`
// under de_DE (measured on git 2.54, which ships translations). The readers
// run their child under LC_ALL=C, appended after the parent's environment so
// it wins over whatever LC_ALL the parent carries; without the pin every
// non-English developer machine reads every unreadable object as absent and
// every corrupt target as unrecorded. Two legs: real git, where the git on
// PATH translates under some locale this host has (a git built NO_GETTEXT, or
// a host without the locale, has no German to show, and that leg says so); and
// a fake git modelled on the measured one, which writes the German prefix
// unless its LC_ALL is C, so the decision is held on every host.
func TestHeldObjectSignalIsReadUnderThePinnedLocale(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub git is a shell script")
	}
	absent := "0123456789abcdef0123456789abcdef01234567"
	t.Run("a git that translates", func(t *testing.T) {
		dir := testutil.NewRepo(t)
		root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Commit(t, dir, "akira-toriyama", ":bug: second")
		translates := func(env ...string) bool {
			cmd := exec.Command("git", "-C", dir, "cat-file", "-t", absent+"^{}")
			cmd.Env = append(os.Environ(), env...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			return stderr.Len() > 0 && !strings.Contains(stderr.String(), "fatal:")
		}
		var locale []string
		for _, candidate := range [][]string{
			{"LC_ALL=de_DE.UTF-8", "LANG=de_DE.UTF-8", "LANGUAGE=de"},
			{"LC_ALL=en_US.UTF-8", "LANG=en_US.UTF-8", "LANGUAGE=de"},
			{"LC_ALL=C.UTF-8", "LANG=C.UTF-8", "LANGUAGE=de"},
		} {
			if translates(candidate...) {
				locale = candidate
				break
			}
		}
		if locale == nil {
			t.Skip("the git on PATH shows no German under any locale this host has (built NO_GETTEXT, or no de locale installed); the fake-git leg holds the decision here")
		}
		for _, kv := range locale {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
		testutil.Git(t, dir, "akira-toriyama", "update-ref", "refs/remotes/origin/main", root)
		testutil.Git(t, dir, "akira-toriyama", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
		emptyObject(t, dir, root)

		_, err := have(context.Background(), dir, []string{root, absent})
		if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), "error:") {
			t.Errorf("have over an empty object under %v = %v; want CodeAPI quoting git's English error: line", locale, err)
		}
		on, err := isAncestor(context.Background(), dir, root, "HEAD")
		if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || on {
			t.Errorf("isAncestor over an empty object under %v = %v, %v; want false, CodeAPI", locale, on, err)
		}
		got, err := DefaultBranch(context.Background(), dir, "origin")
		if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
			t.Errorf("DefaultBranch over an empty target under %v = %q, %v; want CodeAPI — read as unrecorded, the hook names set-head over a broken object store", locale, got, err)
		}
	})

	t.Run("a fake git modelled on the measured one", func(t *testing.T) {
		bin := t.TempDir()
		// What nix git 2.54 writes under de_DE for an empty loose object listed
		// to --batch-check, with the prefix English only under LC_ALL=C.
		stub := "#!/bin/sh\n" +
			"case \"$LC_ALL\" in C|POSIX) p='error:';; *) p='Fehler:';; esac\n" +
			"case \"$*\" in *--batch-check*) while read -r s; do echo \"$s missing\"; done; echo \"$p Objektdatei .git/objects/01/23 ist leer.\" >&2; exit 0;; esac\n" +
			"exit 128\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o700); err != nil { //nolint:gosec // a stub git must be executable
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("LANG", "de_DE.UTF-8")
		t.Setenv("LC_ALL", "")

		_, err := have(context.Background(), t.TempDir(), []string{absent})
		if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || !strings.Contains(err.Error(), "error:") {
			t.Fatalf("have against a git that translates the prefix unless LC_ALL is C = %v; want CodeAPI quoting the English line — the child must run under LC_ALL=C whatever the parent's locale", err)
		}
	})
}

// TestIsAncestor: a YES is a real ancestor or the rev itself; a NO covers both
// spellings git gives it — an honest exit 1 for a commit on another line of
// history, and exit 128 for an object this repository does not hold. The walk
// reads both as "did not land here", so neither may surface as an error.
func TestIsAncestor(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix a crash")
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
	testutil.Commit(t, dir, "akira-toriyama", ":memo: a commit off the released branch")
	offBranch := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	blob := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD:glyph.toml")

	for name, tc := range map[string]struct {
		sha  string
		want bool
	}{
		"an ancestor":                 {root, true},
		"the rev itself":              {head, true},
		"another line of history":     {offBranch, false},
		"an object we do not hold":    {"0123456789abcdef0123456789abcdef01234567", false},
		"a name that is not an sha":   {"not-a-sha", false},
		"an object that is no commit": {blob, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := isAncestor(context.Background(), dir, tc.sha, "HEAD")
			if err != nil {
				t.Fatalf("isAncestor(%q): unexpected error: %v", tc.sha, err)
			}
			if got != tc.want {
				t.Fatalf("isAncestor(%q) = %v, want %v", tc.sha, got, tc.want)
			}
		})
	}
	if got, err := isAncestor(context.Background(), dir, root, "no-such-rev"); err != nil || got {
		t.Fatalf("isAncestor over a rev that names nothing = %v, %v; want false, nil", got, err)
	}
}

// TestIsAncestorFailsWhereGitCouldNotAnswer: a NO is a claim about the repository
// ("this commit never landed"), so only git's two spellings of one read as NO
// (TestIsAncestor). Every failure here exits non-zero as well, most with the same
// 128, and read as NO each is a wrong answer about which commits a release counts,
// returned with a nil error. The held objects git cannot read — empty, unopenable,
// a commit whose body does not parse, non-zlib bytes — are the rows the probe must
// tell from a name that holds nothing: `rev-parse --verify --quiet ^{commit}`
// answers 1 for the absent sha and for some of them, 128 for others, and which
// is which moves with the git version. The gone commit and the honest no under
// gone alternates are the rows merge-base answers with exit 1 beside an error:
// line (the first on git ≤ 2.40 only; 2.54 and later exit 128 there), where a
// status-only reading is a NO. The stub that fails only the ancestry question
// leaves the probes answering, so that row reaches the last arm rather than
// failing in a probe.
func TestIsAncestorFailsWhereGitCouldNotAnswer(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	history := func(t *testing.T) (dir, root, mid, head string) {
		t.Helper()
		dir = testutil.NewRepo(t)
		root = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Commit(t, dir, "akira-toriyama", ":bug: second")
		mid = testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
		testutil.Commit(t, dir, "akira-toriyama", ":bug: third")
		return dir, root, mid, testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	}
	stubGit := func(t *testing.T, script string) {
		t.Helper()
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec // a stub git must be executable
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	for name, arrange := range map[string]func(t *testing.T) (dir, sha, rev, wantMsg string){
		"the commit asked about is corrupt": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			breakObject(t, dir, root)
			return dir, root, "HEAD", "git merge-base"
		},
		"the commit asked about is an empty object file": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			emptyObject(t, dir, root)
			return dir, root, "HEAD", "is empty"
		},
		"the commit asked about is an object file git cannot open": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			unreadableObject(t, dir, root)
			return dir, root, "HEAD", "git merge-base"
		},
		"the commit asked about is under a fan-out directory git cannot open": func(t *testing.T) (string, string, string, string) {
			requireUnopenableSignal(t)
			dir, root, _, _ := history(t)
			unreadableFanout(t, dir, root)
			return dir, root, "HEAD", "Permission denied"
		},
		"the honest no over a store whose alternates directory is gone": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
			testutil.Commit(t, dir, "akira-toriyama", ":memo: a commit off the released branch")
			side := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
			goneAlternates(t, dir)
			return dir, side, "HEAD", "alternates"
		},
		"the commit asked about is a truncated object file": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			truncatedObject(t, dir, root)
			return dir, root, "HEAD", "git merge-base"
		},
		"the commit asked about is a commit git cannot parse": func(t *testing.T) (string, string, string, string) {
			dir, _, _, _ := history(t)
			return dir, unparseableCommit(t, dir), "HEAD", "bogus commit object"
		},
		"the rev asked about is an empty object file": func(t *testing.T) (string, string, string, string) {
			dir, root, mid, _ := history(t)
			emptyObject(t, dir, mid)
			return dir, root, mid, "is empty"
		},
		"a commit on the walk is corrupt, both names answering": func(t *testing.T) (string, string, string, string) {
			dir, root, mid, head := history(t)
			breakObject(t, dir, root)
			return dir, head, mid, "git merge-base"
		},
		"a commit on the walk is gone, both names answering": func(t *testing.T) (string, string, string, string) {
			dir, root, mid, head := history(t)
			testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
			testutil.Commit(t, dir, "akira-toriyama", ":memo: a commit off the released branch")
			side := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
			if err := os.Remove(filepath.Join(dir, ".git", "objects", mid[:2], mid[2:])); err != nil {
				t.Fatal(err)
			}
			return dir, side, head, "git merge-base"
		},
		"a directory that is no repository": func(t *testing.T) (string, string, string, string) {
			return t.TempDir(), "0123456789abcdef0123456789abcdef01234567", "HEAD", "not a git repository"
		},
		"a git that never started": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			t.Setenv("PATH", t.TempDir())
			return dir, root, "HEAD", "executable file not found"
		},
		"a git a signal killed": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			stubGit(t, "kill -9 $$\n")
			return dir, root, "HEAD", "signal: killed"
		},
		"a status that is neither answer, both names answering": func(t *testing.T) (string, string, string, string) {
			dir, root, _, _ := history(t)
			stubGit(t, "case \"$*\" in *merge-base*) echo 'fatal: a status git has no spelling for' >&2; exit 2;; esac\nexec "+realGit+" \"$@\"\n")
			return dir, root, "HEAD", "a status git has no spelling for"
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, sha, rev, wantMsg := arrange(t)
			got, err := isAncestor(context.Background(), dir, sha, rev)
			ce := core.AsError(err)
			if ce == nil || ce.Code != core.CodeAPI {
				t.Fatalf("isAncestor = %v, %v; want CodeAPI — read as NO this is \"this commit never landed\" on evidence nobody collected", got, err)
			}
			if got {
				t.Errorf("isAncestor = true beside the error %v", err)
			}
			if !strings.Contains(err.Error(), wantMsg) {
				t.Errorf("the error must carry git's own diagnosis: got %q, want it to mention %q", err, wantMsg)
			}
		})
	}
}

// TestIsAncestorCancelIsInterrupted: the abort must not be readable as an
// answer about the repository.
//
// This is a REGRESSION test with a specific shape, and the shape is the whole
// point. A context cancelled BEFORE the child starts makes os/exec return
// ctx.Err() and never produces an *exec.ExitError, so it exercises none of the
// ordering: the bug needed the child to be RUNNING when the cancel lands, at
// which point exec.CommandContext kills it and the abort comes back wearing an
// ExitError — indistinguishable, to a status-first reading, from git's own
// "no, not an ancestor". Reading it that way returned (false, nil): a wrong
// answer about which commits a release counts, with a nil error so no caller
// could tell.
//
// Reproducing that needs the child alive at a moment the test controls, which
// real `git merge-base` (microseconds) cannot offer. So PATH is pointed at a
// git that announces itself and then blocks: the test waits for the
// announcement — proof the process is running — and only then cancels. No
// sleeps, no timing assumptions.
func TestIsAncestorCancelIsInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub git is a shell script")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	started := filepath.Join(dir, "started")
	stub := "#!/bin/sh\ntouch " + started + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	_, err := isAncestor(ctx, dir, "any-sha", "HEAD")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("isAncestor cancelled mid-run = %v, want CodeInterrupted (%d) — a killed child arrives as an *exec.ExitError, so reading the exit status before the context turns the abort into a silent \"did not land\"", err, core.CodeInterrupted)
	}
}

// TestIsAncestorInterruptDuringTheProbeCarriesOut is the same abort one read
// deeper: merge-base has failed for its own reason and the cancel lands in the
// probe asking whether the name holds a commit. The probe's interrupt is
// neither nil nor a NO, so a loop that does not return it moves on, the sticky
// cancel fails the next probe the same way, and isAncestor hands back
// merge-base's own failure in the abort's place — exit 4, the code the fleet's
// wrappers retry, on a run the operator stopped, with no layer above asking the
// context again. The fake git fails the ancestry question, announces the probe
// and blocks; the cancel lands only once it has, and the test fails unless it
// did (a cancel landing elsewhere proves nothing about this arm).
func TestIsAncestorInterruptDuringTheProbeCarriesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub git is a shell script")
	}
	dir := t.TempDir()
	bin := t.TempDir()
	asked := filepath.Join(dir, "asked")
	stub := "#!/bin/sh\ncase \"$*\" in *merge-base*) echo 'fatal: the ancestry question failed for its own reason' >&2; exit 2;; esac\ntouch " + asked + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o700); err != nil { //nolint:gosec // a stub git must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(asked); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	_, err := isAncestor(ctx, dir, "any-sha", "HEAD")
	if _, serr := os.Stat(asked); serr != nil {
		t.Fatalf("the fake git never reached the probe, so the cancel landed elsewhere and the run proves nothing: %v", serr)
	}
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeInterrupted {
		t.Fatalf("isAncestor with the cancel landing in the probe = %v, want CodeInterrupted (%d), not merge-base's failure in the abort's place", err, core.CodeInterrupted)
	}
}

// TestFirstParentLog: the n commits ending at rev along first parents, oldest
// first — the shape a rebase-merge leaves behind. A merged side branch must not
// appear: --first-parent is what makes the run alignable.
func TestFirstParentLog(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug: first")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side")
	testutil.Commit(t, dir, "akira-toriyama", ":memo: on the side branch")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Commit(t, dir, "akira-toriyama", ":sparkles: second")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'side'", "side")

	got, err := firstParentLog(context.Background(), dir, "HEAD", 3)
	if err != nil {
		t.Fatalf("firstParentLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("firstParentLog(3) returned %d commits, want 3: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Message, ":bug: first") {
		t.Fatalf("commits are not oldest-first: first is %q", got[0].Message)
	}
	if !strings.HasPrefix(got[2].Message, "Merge branch 'side'") {
		t.Fatalf("last commit = %q, want the merge commit (the run ends AT rev)", got[2].Message)
	}
	for _, c := range got {
		if strings.Contains(c.Message, "on the side branch") {
			t.Fatalf("a merged side-branch commit appeared in a --first-parent run: %+v", got)
		}
	}
}

// TestFirstParentLogShortNearTheRoot: asking for more commits than the history
// holds is a SHORT answer, not an error — the contract mainFootprint's length
// guard depends on. Without that guard a young repository indexes a listing-long
// loop into a shorter slice and panics.
func TestFirstParentLogShortNearTheRoot(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug: the only other commit")

	got, err := firstParentLog(context.Background(), dir, "HEAD", 10)
	if err != nil {
		t.Fatalf("firstParentLog past the root: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("firstParentLog(10) on a 2-commit history = %d commits, want 2", len(got))
	}
}

// TestIsShallow: a full clone says no, a --depth 1 clone says yes. The release
// walk branches on this because a truncated history cannot tell a commit that
// never landed from one git simply does not have.
func TestIsShallow(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", ":bug: fix a crash")

	full, err := isShallow(context.Background(), dir)
	if err != nil {
		t.Fatalf("isShallow(full): %v", err)
	}
	if full {
		t.Fatal("IsShallow reports a full checkout as shallow")
	}

	clone := filepath.Join(t.TempDir(), "shallow")
	testutil.Git(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", "1", "file://"+dir, clone)
	shallow, err := isShallow(context.Background(), clone)
	if err != nil {
		t.Fatalf("isShallow(shallow): %v", err)
	}
	if !shallow {
		t.Fatal("IsShallow reports a --depth 1 clone as full — the walk would grade itself on a partial repository")
	}
}

// TestHooksDir asks git rather than assuming .git/hooks, because core.hooksPath
// relocates them — the family's older repos set it, and writing to .git/hooks
// there installs a hook git never runs.
func TestHooksDir(t *testing.T) {
	dir := testutil.NewRepo(t)

	got, err := HooksDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("HooksDir: %v", err)
	}
	if filepath.Base(got) != "hooks" {
		t.Fatalf("HooksDir = %q, want a path ending in hooks", got)
	}

	testutil.Git(t, dir, "akira-toriyama", "config", "core.hooksPath", "scripts/hooks")
	got, err = HooksDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("HooksDir with core.hooksPath: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "scripts/hooks") {
		t.Fatalf("HooksDir with core.hooksPath = %q, want it to honour the relocation", got)
	}
}

// TestHooksDirErrorsAreAPI: outside a repository there is no answer to give,
// and a git/IO failure is exit 4 by contract.
func TestHooksDirErrorsAreAPI(t *testing.T) {
	testutil.RequireGit(t)
	_, err := HooksDir(context.Background(), t.TempDir())
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("HooksDir outside a repository = %v, want CodeAPI", err)
	}
}

// TestConfigGet: an unset key is an ANSWER (", false, nil), a set one carries
// its value, and only a git that could not be asked at all is an error. The
// caller is the commit-msg hook deciding what git will do to the message it was
// handed, and "unset" is the commonest correct answer it gets.
func TestConfigGet(t *testing.T) {
	dir := testutil.NewRepo(t)

	val, set, err := ConfigGet(context.Background(), dir, "commit.cleanup")
	if err != nil || set || val != "" {
		t.Fatalf("ConfigGet on an unset key = (%q, %v, %v), want (\"\", false, nil) — git spells unset as exit 1, which is not a failure", val, set, err)
	}

	testutil.Git(t, dir, "akira-toriyama", "config", "commit.cleanup", "scissors")
	val, set, err = ConfigGet(context.Background(), dir, "commit.cleanup")
	if err != nil || !set || val != "scissors" {
		t.Fatalf("ConfigGet on a set key = (%q, %v, %v), want (\"scissors\", true, nil)", val, set, err)
	}
}

// TestConfigGetUnreachableDirIsAPI: exit 1 is "no such key" and every other
// failure is exit 4 by contract — telling them apart is why this does not simply
// swallow every error as unset. A directory that does not exist is the failure
// that is unambiguously not an unset key: outside a repository git still answers
// from the global config, and answers exit 1.
func TestConfigGetUnreachableDirIsAPI(t *testing.T) {
	testutil.RequireGit(t)
	_, _, err := ConfigGet(context.Background(), filepath.Join(t.TempDir(), "no-such-dir"), "commit.cleanup")
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("ConfigGet on a missing directory = %v, want CodeAPI", err)
	}
}

// TestConfigBoolOrInt: commit.verbose is read as the integer git acts on,
// through git's own normaliser — every spelling below is one git accepts — and
// -1 is the value --type=bool gets wrong (it prints true; git commit reads -1 as
// unset and does not cut). Unset is an answer, not an error; a value git cannot
// parse is the API error.
func TestConfigBoolOrInt(t *testing.T) {
	dir := testutil.NewRepo(t)
	if n, set, err := ConfigBoolOrInt(context.Background(), dir, "commit.verbose"); err != nil || set || n != 0 {
		t.Fatalf("unset commit.verbose = (%d, %v, %v), want (0, false, nil)", n, set, err)
	}
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"true", 1}, {"yes", 1}, {"on", 1}, {"false", 0}, {"off", 0}, {"", 0},
		{"2", 2}, {"1k", 1024}, {"0", 0}, {"-1", -1},
	} {
		testutil.Git(t, dir, "akira-toriyama", "config", "commit.verbose", tc.value)
		n, set, err := ConfigBoolOrInt(context.Background(), dir, "commit.verbose")
		if err != nil || !set || n != tc.want {
			t.Errorf("commit.verbose=%q = (%d, %v, %v), want (%d, true, nil)", tc.value, n, set, err, tc.want)
		}
	}
	testutil.Git(t, dir, "akira-toriyama", "config", "commit.verbose", "banana")
	if _, _, err := ConfigBoolOrInt(context.Background(), dir, "commit.verbose"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeAPI {
		t.Fatalf("an unparseable commit.verbose = %v, want CodeAPI", err)
	}
}

// TestConfigLastOf: of two names git reads through one handler, the LAST
// definition wins whichever name it carries — in one file, in both orders, and
// with `-c` (GIT_CONFIG_PARAMETERS) after every file — and the value comes back
// raw, a quoted trailing space included. core.commentChar / core.commentString
// are the names, and git 2.54's own cleanup agreed with every row (measured with
// `git stripspace --strip-comments`; internal/cli's
// TestHookVerdictMatchesWhatGitRecords asks real commits).
func TestConfigLastOf(t *testing.T) {
	dir := testutil.NewRepo(t)
	last := func() (string, bool) {
		t.Helper()
		v, set, err := ConfigLastOf(context.Background(), dir, "core.commentChar", "core.commentString")
		if err != nil {
			t.Fatalf("ConfigLastOf: %v", err)
		}
		return v, set
	}
	if v, set := last(); set || v != "" {
		t.Fatalf("both unset = (%q, %v), want (\"\", false)", v, set)
	}
	testutil.Git(t, dir, "akira-toriyama", "config", "core.commentcharx", "decoy")
	if v, set := last(); set {
		t.Fatalf("a key that only starts with a wanted name was read: %q", v)
	}
	testutil.Git(t, dir, "akira-toriyama", "config", "core.commentChar", ";")
	testutil.Git(t, dir, "akira-toriyama", "config", "core.commentString", "//")
	if v, _ := last(); v != "//" {
		t.Fatalf("commentChar then commentString = %q, want the later \"//\"", v)
	}
	testutil.Git(t, dir, "akira-toriyama", "config", "--unset", "core.commentChar")
	testutil.Git(t, dir, "akira-toriyama", "config", "core.commentChar", ";")
	if v, _ := last(); v != ";" {
		t.Fatalf("commentString then commentChar = %q, want the later \";\"", v)
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.commentstring=@'")
	if v, _ := last(); v != "@" {
		t.Fatalf("-c after the file = %q, want \"@\"", v)
	}
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	testutil.Git(t, dir, "akira-toriyama", "config", "--unset", "core.commentChar")
	testutil.Git(t, dir, "akira-toriyama", "config", "core.commentString", "// ")
	if v, _ := last(); v != "// " {
		t.Fatalf("a quoted trailing space = %q, want it kept", v)
	}
}

// TestGitPath: where git keeps one of its own files is git's answer — in a
// linked worktree MERGE_MSG lives under that worktree's own git directory, not
// the main checkout's, which is where the commit-msg hook's merge check looks.
func TestGitPath(t *testing.T) {
	dir := testutil.NewRepo(t)
	got, err := GitPath(context.Background(), dir, "MERGE_MSG")
	if err != nil || filepath.ToSlash(got) != ".git/MERGE_MSG" {
		t.Fatalf("GitPath(MERGE_MSG) = %q, %v; want .git/MERGE_MSG", got, err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	testutil.Git(t, dir, "akira-toriyama", "worktree", "add", "-q", "-b", "wt", wt)
	got, err = GitPath(context.Background(), wt, "MERGE_MSG")
	if err != nil || !strings.HasSuffix(filepath.ToSlash(got), ".git/worktrees/wt/MERGE_MSG") {
		t.Fatalf("GitPath(MERGE_MSG) in a linked worktree = %q, %v; want the worktree's own git directory", got, err)
	}
}

// TestDiffTreeFiles: the paths a commit's own diff touches, with a rename
// reported under BOTH names — detection is off so a move across two subtrees
// names the line the file left as well as the one it joined — a root commit
// answered against the empty tree, and a merge commit answered empty (its
// diff is never attributed to anything).
func TestDiffTreeFiles(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.WriteFile(t, dir, "haiku/h.go", "package haiku\n")
	testutil.WriteFile(t, dir, "curry/c.go", "package curry\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= two subtrees")
	two := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "mv", "haiku/h.go", "curry/h.go")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":truck:~ move a file across subtrees")
	moved := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
	testutil.WriteFile(t, dir, "side.txt", "side\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":memo:= on the side")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--no-ff", "-m", "Merge branch 'side'", "side")
	merge := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	for name, tc := range map[string]struct {
		sha  string
		want []string
	}{
		"the root commit":       {root, []string{"glyph.toml"}},
		"two subtrees":          {two, []string{"curry/c.go", "haiku/h.go"}},
		"a rename names both":   {moved, []string{"curry/h.go", "haiku/h.go"}},
		"a merge commit is nil": {merge, nil},
	} {
		got, err := diffTreeFiles(context.Background(), dir, tc.sha)
		if err != nil {
			t.Fatalf("%s: DiffTreeFiles: %v", name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: DiffTreeFiles = %q, want %q", name, got, tc.want)
		}
	}
}

// TestDiffTreeFilesReadsItsHeaderUnderLogOutputEncoding: diff-tree's %P header
// is pretty-printed, so i18n.logOutputEncoding re-encodes it like `git log`'s
// record (measured on git 2.54: under UTF-16 a non-empty one opens with a
// byte-order mark and holds a NUL per digit). Read as it came, the header was
// cut at its own first NUL and the parent's hex digits came back as the file
// list. A root's header is empty and stays empty — the control beside it. The
// raw read first is the positive control.
func TestDiffTreeFilesReadsItsHeaderUnderLogOutputEncoding(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:^ add a file")
	head := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "config", "i18n.logOutputEncoding", "UTF-16")
	if raw := testutil.Git(t, dir, "akira-toriyama", "diff-tree", "--always", "--format=%P", "--name-only", "-r", head); strings.HasPrefix(raw, root) {
		t.Fatalf("git printed the %%P header as plain hex under i18n.logOutputEncoding=UTF-16 (%q) — git no longer re-encodes there, and this test guards nothing", raw)
	}

	for name, tc := range map[string]struct {
		sha  string
		want []string
	}{
		"the root commit":    {root, []string{"glyph.toml"}},
		"an ordinary commit": {head, []string{"a.txt"}},
	} {
		got, err := diffTreeFiles(context.Background(), dir, tc.sha)
		if err != nil {
			t.Fatalf("%s: DiffTreeFiles: %v", name, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: DiffTreeFiles = %q, want %q", name, got, tc.want)
		}
	}
}

// TestDiffTreeFilesAtAShallowBoundaryIsUnreadable: git reads a shallow clone's
// boundary commit as a root, so `--root` reported its WHOLE tree as its own diff
// (measured: `curry/b haiku/a` where the full clone says `curry/b`), and
// attribution then found a carrier for anything — a shared-only `^` passed
// `lint --range` at 0 in a --depth 1 clone and was refused at 3 in the full one
// (t-esm5 (1)). The boundary must answer unreadable, never a diff this clone
// cannot compute. The controls keep their diffs: the commit above the boundary,
// and a TRUE root commit inside the same shallow clone — git lists that one in
// .git/shallow too, but it has no parent to be cut off from, so its tree is its
// own diff (measured).
func TestDiffTreeFilesAtAShallowBoundaryIsUnreadable(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, "haiku/a", "a\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:(haiku)^ start haiku")
	testutil.WriteFile(t, dir, "curry/b", "b\n")
	testutil.Git(t, dir, "akira-toriyama", "add", ".")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":sparkles:(curry)^ start curry")
	boundary := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	// An unrelated history merged in: its root is inside the depth-2 clone.
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "--orphan", "side")
	testutil.WriteFile(t, dir, "side/s", "s\n")
	testutil.Git(t, dir, "akira-toriyama", "add", "side/s")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= another root")
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-f", "main")
	testutil.Git(t, dir, "akira-toriyama", "merge", "-q", "--allow-unrelated-histories", "--no-ff", "-m", "Merge branch 'side'", "side")
	testutil.WriteFile(t, dir, "haiku/a", "a2\n")
	testutil.Git(t, dir, "akira-toriyama", "commit", "-q", "-am", ":bug:(haiku)~ fix haiku")
	above := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")

	clone := filepath.Join(t.TempDir(), "shallow")
	testutil.Git(t, t.TempDir(), "akira-toriyama", "clone", "-q", "--depth", "3", "file://"+dir, clone)

	files, err := diffTreeFiles(context.Background(), clone, boundary)
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI || files != nil {
		t.Fatalf("DiffTreeFiles at the shallow boundary = %q, %v; want no files and an API-class unreadable answer — its own diff is [curry/b], and the clone cannot compute it", files, err)
	}
	if !IsShallowBoundary(err) {
		t.Fatalf("the boundary's error is not recognisable as one (IsShallowBoundary false): %v — its callers could only exit 4", err)
	}
	for name, tc := range map[string]struct {
		sha  string
		want []string
	}{
		"the commit above the boundary": {above, []string{"haiku/a"}},
		"a true root inside the clone":  {root, []string{"side/s"}},
	} {
		got, err := diffTreeFiles(context.Background(), clone, tc.sha)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: DiffTreeFiles = %q, %v; want %q", name, got, err, tc.want)
		}
	}
}

// TestDiffTreeFilesUnknownShaIsAPI: an object this checkout does not hold is
// a git failure in the API class, never an empty answer — an empty answer
// would attribute the commit to nothing and quietly drop it from every line.
func TestDiffTreeFilesUnknownShaIsAPI(t *testing.T) {
	dir := testutil.NewRepo(t)
	_, err := diffTreeFiles(context.Background(), dir, "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatalf("DiffTreeFiles on an absent sha returned no error")
	}
	if ce := core.AsError(err); ce == nil || ce.Code != core.CodeAPI {
		t.Fatalf("DiffTreeFiles error = %v, want CodeAPI", err)
	}
	if IsShallowBoundary(err) {
		t.Fatalf("an object this checkout does not hold read as a shallow boundary: %v — the walk would carry a missing commit nowhere instead of failing", err)
	}
}

// TestMergeBase: the common ancestor of several revs is the older base on a
// linear history and the fork point across branches; one rev answers itself
// without shelling out.
func TestMergeBase(t *testing.T) {
	dir := testutil.NewRepo(t)
	root := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ first")
	first := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", ":bug:~ second")
	second := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "-c", "side", root)
	testutil.Commit(t, dir, "akira-toriyama", ":memo:= aside")
	aside := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Git(t, dir, "akira-toriyama", "switch", "-q", "main")

	for name, tc := range map[string]struct {
		revs []string
		want string
	}{
		"one rev is itself":         {[]string{second}, second},
		"linear: the older base":    {[]string{second, first}, first},
		"across a fork: the root":   {[]string{second, aside}, root},
		"three revs, one on a fork": {[]string{first, second, aside}, root},
	} {
		got, err := mergeBase(context.Background(), dir, tc.revs)
		if err != nil {
			t.Fatalf("%s: mergeBase: %v", name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: mergeBase = %.7s, want %.7s", name, got, tc.want)
		}
	}
}
