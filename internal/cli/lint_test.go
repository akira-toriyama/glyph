package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/testutil"
)

// TestHookVerdictMatchesWhatGitRecords is the whole point of the hook: the
// verdict a developer gets while writing a message must be the verdict CI gets
// on the message git actually recorded. Anything else is glyph lying in one
// direction or the other — blessing a commit CI will reject, or refusing one CI
// would accept.
//
// It is written against REAL git rather than against a model of it: every case
// makes an actual commit, the commit-msg hook captures the exact bytes git hands
// a hook and the GIT_EDITOR it sets, and the two verdicts are then taken through
// the CLI itself — `lint --stdin` on the captured file (what the hook does) and
// `lint --range` on the recorded commit (what CI does). No case can pass because
// glyph and its test agree about git.
//
// Every row here disagreed before `--stdin` learned git's cleanup modes: glyph
// stripped comments and cut at scissors in ALL of them, which is what git does
// only when an editor runs.
func TestHookVerdictMatchesWhatGitRecords(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	cases := []struct {
		name string
		// config applied to the repo before committing, as key/value pairs.
		config [][2]string
		// message is what the author produces: the whole file with -F, the text
		// prepended to git's prepared buffer with an editor.
		message string
		editor  bool
		// below writes the message UNDER git's prepared buffer instead of above
		// it, which is where the template ends up as line 1.
		below   bool
		verbose bool
		// want is the exit code BOTH sides must reach.
		want int
	}{
		{
			name: "-F: a leading '#' line is the subject git records",
			// No editor ⇒ cleanup=whitespace ⇒ comments are content. glyph used
			// to drop the line and lint the one below it: hook 0, CI 3.
			message: "# reminder from my notes\n:sparkles:(x)^ add the thing\n",
			want:    3,
		},
		{
			name: "-F: an indented comment is content in both readings",
			// git only drops a '#' at column 0; this one stays in the body,
			// which v2 never reads — the subject matches either way.
			message: ":fire:(x)= drop the thing\n\n  # why: leftover note here\n",
			want:    0,
		},
		{
			name:    "-F under commit.cleanup=strip: comments really are dropped",
			config:  [][2]string{{"commit.cleanup", "strip"}},
			message: "# a note git will drop\n:bug:(x)~ fix the thing\n",
			want:    0,
		},
		{
			name:    "-F under commit.cleanup=verbatim: git records the bytes as written",
			config:  [][2]string{{"commit.cleanup", "verbatim"}},
			message: "# a note git records under verbatim\n:bug:(x)~ fix the thing\n",
			want:    3, // the '#' line survives, so no pattern claims the message
		},
		{
			name:    "an editor runs: the template is not the message",
			editor:  true,
			message: ":sparkles:(x)^ add the thing\n",
			want:    0,
		},
		{
			name: "an editor runs: a message typed UNDER the template is still the message",
			// git strips the comments above it, so the subject is the author's
			// line. Reading GIT_EDITOR's absence as "no editor ran" — which is
			// what core.editor and $EDITOR leave behind — lints the template's
			// first line as the subject: hook 3, CI 0, and no way past it but
			// --no-verify.
			editor:  true,
			below:   true,
			message: ":sparkles:(x)^ add the thing\n",
			want:    0,
		},
		{
			name: "an editor runs with -v: the diff below the cut line is not the message",
			// The verbose diff carries lines that are prose to the linter; if it
			// were not cut, the NON-BREAKING: footer would stop being a trailer.
			editor:  true,
			verbose: true,
			message: ":fire:(x)= drop the thing\n\nwhy: it was dead\n",
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testGit(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
			preset, _ := config.Preset("gemoji")
			if err := os.WriteFile(filepath.Join(dir, "glyph.toml"), preset, 0o644); err != nil {
				t.Fatalf("write glyph.toml: %v", err)
			}
			testGit(t, dir, "akira-toriyama", "add", "glyph.toml")
			testGit(t, dir, "akira-toriyama", "commit", "-q", "-m", ":tada:= begin the project")
			for _, kv := range tc.config {
				testGit(t, dir, "akira-toriyama", "config", kv[0], kv[1])
			}

			handed := filepath.Join(dir, "handed-to-the-hook.txt")
			editorEnv := filepath.Join(dir, "git-editor-env.txt")
			writeExecutable(t, filepath.Join(dir, ".git", "hooks", "commit-msg"),
				"#!/bin/sh\ncp \"$1\" "+handed+"\nprintf '%s' \"${GIT_EDITOR-UNSET}\" > "+editorEnv+"\nexit 0\n")

			if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600); err != nil {
				t.Fatalf("write file: %v", err)
			}
			testGit(t, dir, "akira-toriyama", "add", "a.txt")

			args := []string{"commit", "-q"}
			if tc.verbose {
				args = append(args, "-v")
			}
			if tc.editor {
				// core.editor rather than GIT_EDITOR on purpose: git then leaves
				// GIT_EDITOR UNSET in the hook's environment (measured), which is
				// the reading that must still mean "an editor ran".
				buffer := filepath.Join(dir, "buffer.txt")
				editor := filepath.Join(dir, "editor.sh")
				if err := os.WriteFile(buffer, []byte(tc.message), 0o600); err != nil {
					t.Fatalf("write buffer: %v", err)
				}
				script := "#!/bin/sh\ncat \"$1\" >> " + buffer + "\ncp " + buffer + " \"$1\"\n"
				if tc.below {
					script = "#!/bin/sh\ncat " + buffer + " >> \"$1\"\n"
				}
				writeExecutable(t, editor, script)
				testGit(t, dir, "akira-toriyama", "config", "core.editor", editor)
			} else {
				msg := filepath.Join(dir, "message.txt")
				if err := os.WriteFile(msg, []byte(tc.message), 0o600); err != nil {
					t.Fatalf("write message: %v", err)
				}
				args = append(args, "-F", msg)
			}
			testGit(t, dir, "akira-toriyama", args...)

			raw, err := os.ReadFile(handed) //nolint:gosec // a path this test just wrote
			if err != nil {
				t.Fatalf("the commit-msg hook did not run: %v", err)
			}
			env, err := os.ReadFile(editorEnv) //nolint:gosec // a path this test just wrote
			if err != nil {
				t.Fatalf("read the hook's GIT_EDITOR: %v", err)
			}

			t.Chdir(dir)
			// Hand glyph the environment git handed the hook, whichever way git
			// spelled it — that signal is half of what the mode is derived from.
			if string(env) == "UNSET" {
				unsetEnv(t, "GIT_EDITOR")
			} else {
				t.Setenv("GIT_EDITOR", string(env))
			}

			setStdin(t, string(raw))
			hookCode, _, hookErr := runGlyph(t, "lint", "--stdin")
			ciCode, _, ciErr := runGlyph(t, "lint", "--range", "HEAD~1..HEAD")
			recorded := testGit(t, dir, "akira-toriyama", "log", "-1", "--format=%B")

			if hookCode != ciCode {
				t.Fatalf("the hook and CI disagree about the same commit: hook %d, CI %d\n"+
					"  handed to the hook: %q\n  recorded by git:    %q\n  hook stderr: %s\n  CI stderr:   %s",
					hookCode, ciCode, raw, recorded, hookErr, ciErr)
			}
			if hookCode != tc.want {
				t.Errorf("both sides answered %d, want %d — they agree on the wrong verdict\n"+
					"  handed to the hook: %q\n  recorded by git:    %q\n  stderr: %s",
					hookCode, tc.want, raw, recorded, hookErr)
			}
		})
	}
}

// writeExecutable writes body at path (creating its directory) with the mode a
// hook or an editor needs.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // git must be able to run it
		t.Fatalf("write %s: %v", path, err)
	}
}

// unsetEnv removes a variable for one test and restores it afterwards —
// t.Setenv's missing half, and the state that matters here: git leaves
// GIT_EDITOR UNSET when core.editor supplied the editor.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

// TestLintMessageClean: a convention-clean message is a silent success — no
// payload, exit 0 (Rule of Silence).
func TestLintMessageClean(t *testing.T) {
	t.Chdir(testutil.NewRepo(t))
	code, stdout, _ := runGlyph(t, "lint", "--message", ":bug:~ fix a crash")
	if code != 0 {
		t.Fatalf("clean lint --message exited %d, want 0", code)
	}
	if stdout != "" {
		t.Fatalf("clean lint --message wrote %q to stdout, want nothing", stdout)
	}
}

// TestLintMessageViolations: violations exit 3 with a structured stderr
// envelope carrying the stable rule ids, keeping stdout pure. The envelope is
// DECODED, not grepped — its keys ("error", "code", "details", "rule") are the
// machine API the lint reusable's jq annotations branch on.
func TestLintMessageViolations(t *testing.T) {
	t.Chdir(testutil.NewRepo(t))
	code, stdout, stderr := runGlyph(t, "lint", "--message", "Fix a crash without any pattern shape")
	if code != 3 {
		t.Fatalf("lint --message with violations exited %d, want 3", code)
	}
	if stdout != "" {
		t.Fatalf("violations must go to stderr, stdout got %q", stdout)
	}
	env := decodeErrorEnvelope(t, stderr)
	if env.Code != 3 {
		t.Fatalf("envelope code = %d, want 3 (the exit code, restated for jq)", env.Code)
	}
	var details []struct {
		Subject string `json:"subject"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("envelope details are not the violations array: %v\n%s", err, env.Details)
	}
	if len(details) != 1 || !strings.Contains(details[0].Detail, "matches none") {
		t.Fatalf("details = %+v, want one pattern-mismatch finding", details)
	}
	if details[0].Subject != "Fix a crash without any pattern shape" {
		t.Fatalf("finding must carry the subject, got %+v", details[0])
	}
}

// TestLintStdin: --stdin reads the message from the input stream (the
// commit-msg hook path) and stays authoring-mode — :construction:= is legal.
func TestLintStdin(t *testing.T) {
	t.Chdir(testutil.NewRepo(t))
	setStdin(t, ":construction:= try things\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 0 {
		t.Fatalf("lint --stdin exited %d, want 0 (WIP is legal at authoring time)\nstderr: %s", code, stderr)
	}
}

// TestLintStdinViolation: the stdin path still lints — a violation exits 3.
func TestLintStdinViolation(t *testing.T) {
	t.Chdir(testutil.NewRepo(t))
	setStdin(t, "no gitmoji here\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 3 {
		t.Fatalf("lint --stdin with a bad message exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "matches none") {
		t.Fatalf("stderr envelope is missing the pattern-mismatch finding:\n%s", stderr)
	}
}

// TestLintModeFlagsAreExclusiveAndRequired: exactly one of --range / --pr /
// --message / --stdin — none or two is a usage error (exit 2).
func TestLintModeFlagsAreExclusiveAndRequired(t *testing.T) {
	if code, _, _ := runGlyph(t, "lint"); code != 2 {
		t.Fatalf("lint with no mode exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--message", ":bug:~ x", "--stdin"); code != 2 {
		t.Fatalf("lint with two modes exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--pr", "7", "--range", "a..b"); code != 2 {
		t.Fatalf("lint with --pr and --range exited %d, want 2", code)
	}
	// The bad-invocation classes bump's --pr already refuses, refused here the
	// same way: a non-positive number (what a workflow yields from a null PR
	// number) is usage — the gate code 3 belongs to convention violations, and
	// no request may go out for input no retry can fix.
	if code, _, _ := runGlyph(t, "lint", "--pr", "0"); code != 2 {
		t.Fatalf("lint --pr 0 exited %d, want 2", code)
	}
}

// pullPath names the single-pull endpoint for the akira-toriyama/glyph
// repository the tests query, the way pullCommitsPath names the listing.
func pullPath(number int) string {
	return fmt.Sprintf("/repos/akira-toriyama/glyph/pulls/%d", number)
}

// apiOnePullBody renders GET pulls/{n} with only what the title lint reads.
func apiOnePullBody(title, login string) string {
	m, _ := json.Marshal(title)
	return fmt.Sprintf(`{"number":7,"title":%s,"user":{"login":%q}}`, m, login)
}

// TestLintPRTitle is the squash-title gate working end to end: a title in the
// grammar passes silently; a title outside it exits 3, is annotated by the
// binary itself (the producer contract of t-sws7), and still ends in one
// sievable envelope. CONTRIBUTING ratifies the title as a commit subject —
// measured before this existed, 158 landed subjects had failed the grammar
// this way, each read by the release walk's fallback as a silent none.
func TestLintPRTitle(t *testing.T) {
	t.Run("clean title", func(t *testing.T) {
		srv := walkServer(t, map[string]string{pullPath(7): apiOnePullBody(":bug:(lint)~ fix a crash", "akira-toriyama")})
		usePR(t, srv)
		code, stdout, stderr := runGlyph(t, "lint", "--pr", "7")
		if code != 0 {
			t.Fatalf("lint --pr with a clean title exited %d, want 0\nstderr: %s", code, stderr)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("a clean title must be silent, got stdout %q stderr %q", stdout, stderr)
		}
	})

	t.Run("malformed title", func(t *testing.T) {
		srv := walkServer(t, map[string]string{pullPath(7): apiOnePullBody("add a menu without a gitmoji", "akira-toriyama")})
		usePR(t, srv)
		code, stdout, stderr := runGlyph(t, "lint", "--pr", "7")
		if code != 3 {
			t.Fatalf("lint --pr with a malformed title exited %d, want 3\nstderr: %s", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("violations must go to stderr, stdout got %q", stdout)
		}
		if !strings.Contains(stderr, "::error::glyph: ") || !strings.Contains(stderr, "matches none") {
			t.Fatalf("the finding must arrive as the binary's own ::error:: annotation:\n%s", stderr)
		}
		env := decodeErrorEnvelope(t, stderr[strings.Index(stderr, "{"):])
		if env.Code != 3 {
			t.Errorf("envelope carries code %d, want 3", env.Code)
		}
	})
}

// TestLintPRTitleExcludesBots pins the exclusion side: the squash attributes
// the landed commit to the pull's AUTHOR, so a bot's title is excluded exactly
// as a bot's commit is. Without this, every dependabot pull — a daily,
// fleet-wide event whose titles are not glyph's grammar and not the bot's to
// change — would red the gate, and lint.yml forwards the code verbatim.
func TestLintPRTitleExcludesBots(t *testing.T) {
	srv := walkServer(t, map[string]string{pullPath(9): apiOnePullBody("build(deps): bump the actions group", "dependabot[bot]")})
	usePR(t, srv)
	code, stdout, stderr := runGlyph(t, "lint", "--pr", "9")
	if code != 0 {
		t.Fatalf("lint --pr on a bot's pull exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("an excluded title must be silent, got stdout %q stderr %q", stdout, stderr)
	}
}

// TestLintRange: over a PR-shaped range the merge-candidate rules apply
// (:construction:= blocks), excluded commits (bots, autosquash) are skipped
// rather than failed, and each violation carries its commit SHA.
func TestLintRange(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")    // bot: skipped
	testCommit(t, dir, "akira-toriyama", "fixup! :bug:~ fix a crash")   // autosquash: skipped
	testCommit(t, dir, "akira-toriyama", ":construction:= try an idea") // WIP with a sigil: the author's call, clean
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")      // unmatched: violation
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("violations must go to stderr, stdout got %q", stdout)
	}
	if !strings.Contains(stderr, "matches none") {
		t.Fatalf("stderr envelope is missing the pattern-mismatch finding:\n%s", stderr)
	}
	if strings.Contains(stderr, "construction") {
		t.Fatalf("a WIP commit carrying a sigil is the author's call, not a violation:\n%s", stderr)
	}
	if strings.Contains(stderr, "build(deps)") {
		t.Fatalf("bot commit leaked into the violations:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"sha"`) {
		t.Fatalf("range violations must carry commit SHAs:\n%s", stderr)
	}
}

// TestLintRangeAnnotatesEachFinding pins the producer half of the annotation
// contract lint.yml now leans on: one `::error::` per finding, written by the
// binary that computed it, every one of them before the envelope so the
// fleet's sieve still recovers pure JSON.
//
// The caller-side reconstruction this replaced was where a whole run's
// annotations went missing in silence (t-sws7): lint.yml rebuilt the
// per-finding lines with jq over a stream carrying two shapes, and a run that
// warned before it failed emitted no annotations at all. lint.yml now replays
// the diagnostic stream verbatim and frames only the summary — so if glyph
// stops writing these lines, nobody writes them, and the per-commit pointers a
// reviewer acts on vanish with no red anywhere. The mutation row naming this
// test is what keeps that from happening quietly.
func TestLintRangeAnnotatesEachFinding(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", "first without any pattern shape")
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")
	t.Chdir(dir)
	wipSHA := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD~1")[:7]
	malformedSHA := testGit(t, dir, "akira-toriyama", "rev-parse", "HEAD")[:7]

	code, _, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}

	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	cut := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "{") {
			cut = i
			break
		}
	}
	if cut < 0 {
		t.Fatalf("no line opens the envelope, so `sed -n '/^[{]/,$p'` recovers nothing:\n%s", stderr)
	}

	var annotations []string
	for _, l := range lines[:cut] {
		if strings.HasPrefix(l, "::error::glyph: ") {
			annotations = append(annotations, l)
		}
	}
	if len(annotations) != 2 {
		t.Fatalf("want one ::error:: annotation per finding (2), got %d — lint.yml no longer "+
			"rebuilds these, so what the binary does not write, no reviewer sees:\n%s",
			len(annotations), stderr)
	}
	for _, want := range []struct{ sha, rule string }{
		{wipSHA, "matches none"},
		{malformedSHA, "matches none"},
	} {
		anchored := false
		for _, a := range annotations {
			if strings.Contains(a, want.sha) && strings.Contains(a, want.rule) {
				anchored = true
				break
			}
		}
		if !anchored {
			t.Errorf("no annotation anchors %s to commit %s — a finding without its commit "+
				"cannot be acted on:\n%s", want.rule, want.sha, stderr)
		}
	}

	// The machine half must survive the human half: everything from the first
	// `{`-opening line is still one decodable envelope carrying the same two
	// findings, or the fleet's `jq -e '.error.code == 3'` branch breaks.
	env := decodeErrorEnvelope(t, strings.Join(lines[cut:], "\n"))
	if env.Code != 3 {
		t.Errorf("sieved envelope carries code %d, want 3", env.Code)
	}
	var details []json.RawMessage
	if err := json.Unmarshal(env.Details, &details); err != nil || len(details) != 2 {
		t.Errorf("sieved envelope's details did not survive the annotations (decode: %v, count %d, want 2):\n%s",
			err, len(details), stderr)
	}
}

// TestLintRangeClean: a range of clean and skipped commits is a silent 0.
func TestLintRangeClean(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug:~ fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui)^ add a menu\n\nBody.\n\n---（和訳）\nメニュー。")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("clean lint --range exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("clean lint --range wrote %q to stdout, want nothing", stdout)
	}
	// The positive control for TestLintRangeJudgingNothingSaysSo: this range
	// DID judge commits, so the nothing-linted annotation must not fire. A
	// warning that cries on every clean run is one a fleet learns to ignore,
	// which costs exactly what it was added to buy.
	if stderr != "" {
		t.Fatalf("a range that judged commits must stay silent, stderr got:\n%s", stderr)
	}
}

// TestLintRangeJudgingNothingSaysSo pins the one verdict the exit-code contract
// cannot express. `0` means "every commit I checked conforms"; over a range that
// checked none, that is vacuously true and reads to the caller exactly like a
// clean pass. lint.yml guards one cause of it in YAML, on the caller's side of
// the pin; the local invocation CLAUDE.md prescribes before pushing has no guard
// at all.
//
// Both causes are asserted and each must name itself, because they call for
// different fixes: an empty range is the caller's range to correct (a stale or
// unfetched base collapses to one), while an all-excluded range is glyph working
// exactly as designed. The exit code is pinned to 0 on both — an all-bot range
// is a daily fleet event and lint.yml forwards glyph's code verbatim, so a
// non-zero here would red every repository's gate on a healthy push.
func TestLintRangeJudgingNothingSaysSo(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")
	t.Chdir(dir)

	t.Run("empty range", func(t *testing.T) {
		code, stdout, stderr := runGlyph(t, "lint", "--range", "HEAD..HEAD")
		if code != 0 {
			t.Fatalf("an empty range exited %d, want 0\nstderr: %s", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("an empty range wrote %q to stdout, want nothing", stdout)
		}
		if !strings.Contains(stderr, "::warning::") || !strings.Contains(stderr, "nothing linted") {
			t.Fatalf("a range that judged nothing must say so:\n%s", stderr)
		}
		if !strings.Contains(stderr, "holds no commits") {
			t.Fatalf("the empty-range cause must name itself — the caller's range is what needs fixing:\n%s", stderr)
		}
	})

	t.Run("every commit excluded", func(t *testing.T) {
		code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
		if code != 0 {
			t.Fatalf("an all-excluded range exited %d, want 0\nstderr: %s", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("an all-excluded range wrote %q to stdout, want nothing", stdout)
		}
		if !strings.Contains(stderr, "nothing linted") || !strings.Contains(stderr, "excluded from the convention") {
			t.Fatalf("an all-excluded range must say so, and say which cause it was:\n%s", stderr)
		}
		if !strings.Contains(stderr, "all 1 commit(s)") {
			t.Fatalf("the annotation must carry how many commits were passed over:\n%s", stderr)
		}
	})
}

// TestLintRangeOutsideRepo: git failures classify as API (exit 4), never as
// lint or usage.
func TestLintRangeOutsideRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, _ := runGlyph(t, "lint", "--range", "main..HEAD"); code != 4 {
		t.Fatalf("lint --range outside a repo exited %d, want 4", code)
	}
}

// TestLintRangeUsageGuards: an empty or option-shaped --range is caught as
// usage (exit 2) before git ever runs.
func TestLintRangeUsageGuards(t *testing.T) {
	if code, _, _ := runGlyph(t, "lint", "--range", ""); code != 2 {
		t.Fatalf("lint --range '' exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--range", "--all"); code != 2 {
		t.Fatalf("lint --range --all exited %d, want 2", code)
	}
}
