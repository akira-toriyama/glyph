package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/testutil"
)

// testClone builds a bare remote and a clone of it, and returns the clone's
// path plus the remote's. A real clone is what makes these tests measure the
// thing: refs/remotes/origin/* and refs/remotes/origin/HEAD are written by git
// itself, and hand-forging them would test glyph against glyph.
func testClone(t *testing.T) (work, remote string) {
	t.Helper()
	testutil.GitOrSkip(t)
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "work")
	testutil.Git(t, root, "akira-toriyama", "init", "-q", "--bare", "-b", "main", remote)

	seed := filepath.Join(root, "seed")
	testutil.Git(t, root, "akira-toriyama", "init", "-q", "-b", "main", seed)
	testutil.Commit(t, seed, "akira-toriyama", ":tada: begin the project")
	testutil.Git(t, seed, "akira-toriyama", "remote", "add", "origin", remote)
	testutil.Git(t, seed, "akira-toriyama", "push", "-q", "origin", "main")

	testutil.Git(t, root, "akira-toriyama", "clone", "-q", remote, work)
	return work, remote
}

// rev resolves a revision in dir to its full object name.
func rev(t *testing.T, dir, r string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, dir, "akira-toriyama", "rev-parse", r))
}

// zeros is git's null object name at this repository's object width.
const zeros = "0000000000000000000000000000000000000000"

// TestPrePushSkipsDeletesTagsAndEmptyStdin: the lines a commit convention has
// no opinion about must cost nothing and say nothing.
//
// A deletion is recognised by the all-zero LOCAL oid, not by the literal
// `(delete)` git writes in the ref field; a tag is recognised by the remote
// ref's prefix, because an annotated tag's local oid is a tag object and
// nothing about the oid itself says so. Empty stdin is git running the hook on
// a push with nothing to do — an ordinary event, never an error. Silence
// rather than a warning is the point: an annotation on every tag push is the
// noise that teaches people to stop reading annotations.
func TestPrePushSkipsDeletesTagsAndEmptyStdin(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)
	head := rev(t, work, "HEAD")

	for name, stdin := range map[string]string{
		"empty stdin":     "",
		"delete by colon": "(delete) " + zeros + " refs/heads/gone " + head + "\n",
		"lightweight tag": "refs/tags/v1 " + head + " refs/tags/v1 " + zeros + "\n",
		"sha256 delete":   "(delete) " + strings.Repeat("0", 64) + " refs/heads/gone " + head + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			setStdin(t, stdin)
			code, stdout, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
			if code != 0 {
				t.Fatalf("exited %d, want 0\nstderr: %s", code, stderr)
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("must be silent; stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

// TestPrePushRangeExcludesWhatTheRemoteAlreadyHas: the range is what the push
// ADDS, and the two obvious spellings both get it wrong.
//
// The branch here merges the remote's main in, which is the everyday fleet
// shape. `<remote sha>..<local sha>` counts the merged-in commits again, so a
// commit the remote has had for weeks is re-judged on every push. This asserts
// the finding set instead of a count, because the failure it guards is naming
// the WRONG commits, not naming too many.
func TestPrePushRangeExcludesWhatTheRemoteAlreadyHas(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)

	// A commit that lands on the remote's main, and is therefore already theirs.
	testutil.Commit(t, work, "akira-toriyama", "Already on main and malformed")
	testutil.Git(t, work, "akira-toriyama", "push", "-q", "origin", "main")
	testutil.Git(t, work, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Commit(t, work, "akira-toriyama", "the outgoing one is malformed too")

	setStdin(t, "refs/heads/topic "+rev(t, work, "HEAD")+" refs/heads/topic "+zeros+"\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
	if code != 0 {
		t.Fatalf("a topic branch must not block; exited %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "the outgoing one") && !strings.Contains(stderr, "malformed-subject") {
		t.Fatalf("the outgoing commit must be judged:\n%s", stderr)
	}
	if strings.Contains(stderr, "1 commit-convention violation(s)") == false {
		t.Fatalf("exactly the outgoing commit must be judged — a commit already on the remote was re-judged:\n%s", stderr)
	}
}

// TestPrePushWarnsOffTheDefaultBranchAndBlocksOnIt: the ratified severity fold.
//
// :construction: is legal mid-branch and illegal at the merge, so the rule
// fires either way and only the CONSEQUENCE moves. Blocking it on a topic
// branch would make the branch unpushable, and the only escape is --no-verify,
// which turns the whole gate off (DESIGN §2.1) — a strictly worse trade than a
// late lint.
func TestPrePushWarnsOffTheDefaultBranchAndBlocksOnIt(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)
	testutil.Git(t, work, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Commit(t, work, "akira-toriyama", ":construction: try an idea")
	head := rev(t, work, "HEAD")

	t.Run("topic branch warns", func(t *testing.T) {
		setStdin(t, "refs/heads/topic "+head+" refs/heads/topic "+zeros+"\n")
		code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
		if code != 0 {
			t.Fatalf("a :construction: commit on a topic branch exited %d, want 0\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "commit-lint job will reject") {
			t.Fatalf("the finding must still be reported, and say CI will act on it:\n%s", stderr)
		}
	})

	t.Run("default branch blocks", func(t *testing.T) {
		setStdin(t, "refs/heads/topic "+head+" refs/heads/main "+rev(t, work, "origin/main")+"\n")
		code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
		if code != 3 {
			t.Fatalf("a :construction: commit reaching the default branch exited %d, want 3\nstderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "wip-merge-candidate") {
			t.Fatalf("the blocking envelope must carry the rule id:\n%s", stderr)
		}
	})
}

// TestPrePushDefaultBranchUnresolvedWarnsOnly: with no refs/remotes/<r>/HEAD
// there is no default branch to compare against, and glyph must not invent one.
//
// A clone made before `git remote set-head` has none. Guessing `main` blocks a
// legal commit on a master/develop repository, and the only escape there is
// --no-verify. Warning is the honest degradation, and the message has to name
// the command that restores the ref or the warning is unactionable.
func TestPrePushDefaultBranchUnresolvedWarnsOnly(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)
	testutil.Git(t, work, "akira-toriyama", "remote", "set-head", "origin", "--delete")
	testutil.Git(t, work, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Commit(t, work, "akira-toriyama", ":construction: try an idea")

	setStdin(t, "refs/heads/topic "+rev(t, work, "HEAD")+" refs/heads/main "+rev(t, work, "origin/main")+"\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
	if code != 0 {
		t.Fatalf("an unresolved default branch must not block; exited %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "git remote set-head origin -a") {
		t.Fatalf("the warning must name the command that restores the ref:\n%s", stderr)
	}
}

// TestPrePushCountsACommitOnTwoRefLinesOnce: one push can carry one commit on
// two lines, and reporting one violation twice is a report nobody trusts.
func TestPrePushCountsACommitOnTwoRefLinesOnce(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)
	testutil.Git(t, work, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Commit(t, work, "akira-toriyama", "no gitmoji here")
	head := rev(t, work, "HEAD")

	setStdin(t,
		"refs/heads/topic "+head+" refs/heads/topic "+zeros+"\n"+
			"refs/heads/topic "+head+" refs/heads/topic-copy "+zeros+"\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
	if code != 0 {
		t.Fatalf("exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "1 commit-convention violation(s)") {
		t.Fatalf("the commit rides two ref lines and must be judged once:\n%s", stderr)
	}
}

// TestPrePushResolvesTheRemoteFromItsURL: `git push <url> HEAD` passes the URL
// as BOTH arguments, so reading argv[0] as a remote name finds nothing, the
// tracking refs are never consulted, and the whole history reads as outgoing.
func TestPrePushResolvesTheRemoteFromItsURL(t *testing.T) {
	work, remote := testClone(t)
	t.Chdir(work)
	// A malformed commit the remote ALREADY has: it is what makes this test
	// bite. With the remote unresolved there are no tracking tips to exclude,
	// the whole history reads as outgoing, and this commit is judged a second
	// time — so the finding count, not the exit code, is the assertion.
	testutil.Commit(t, work, "akira-toriyama", "already on the remote and malformed")
	testutil.Git(t, work, "akira-toriyama", "push", "-q", "origin", "main")
	testutil.Git(t, work, "akira-toriyama", "checkout", "-q", "-b", "topic")
	testutil.Commit(t, work, "akira-toriyama", "no gitmoji here")

	setStdin(t, "refs/heads/topic "+rev(t, work, "HEAD")+" refs/heads/topic "+zeros+"\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", remote, remote)
	if code != 0 {
		t.Fatalf("exited %d, want 0\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "1 commit-convention violation(s)") {
		t.Fatalf("a URL-only push must still exclude what the remote holds — the seed commit "+
			"leaked into the judged set:\n%s", stderr)
	}
}

// TestPrePushMalformedProtocolIsNotALintFailure: a line glyph cannot read means
// glyph has misunderstood git's own wire format, not that a commit violates the
// convention. It must never be the gate code — the installed hook forwards 3
// and only 3, so classifying this as 3 would refuse a push over glyph's bug.
func TestPrePushMalformedProtocolIsNotALintFailure(t *testing.T) {
	work, _ := testClone(t)
	t.Chdir(work)
	setStdin(t, "refs/heads/topic deadbeef\n")
	code, _, stderr := runGlyph(t, "hook", "pre-push", "origin", "ignored")
	if code != 4 {
		t.Fatalf("a malformed protocol line exited %d, want 4\nstderr: %s", code, stderr)
	}
}
