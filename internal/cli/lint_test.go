package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
			message: "# reminder from my notes\n:sparkles:(x) add the thing\n",
			want:    3,
		},
		{
			name: "-F: an indented comment keeps a footer out of the trailer block",
			// git only drops a '#' at column 0. Dropping this one closed the gap
			// above NON-BREAKING:, making it a trailer at the hook and prose in
			// CI: hook 0, CI 3 (undeclared-removal).
			message: ":fire:(x) drop the thing\n\n  # why: leftover note here\nNON-BREAKING: it was dead\n",
			want:    3,
		},
		{
			name:    "-F under commit.cleanup=strip: comments really are dropped",
			config:  [][2]string{{"commit.cleanup", "strip"}},
			message: "# a note git will drop\n:bug:(x) fix the thing\n",
			want:    0,
		},
		{
			name:    "-F under commit.cleanup=verbatim: git records the bytes as written",
			config:  [][2]string{{"commit.cleanup", "verbatim"}},
			message: ":bug:(x) fix the thing.  \n",
			want:    3, // trailing-period, in both readings
		},
		{
			name:    "an editor runs: the template is not the message",
			editor:  true,
			message: ":sparkles:(x) add the thing\n",
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
			message: ":sparkles:(x) add the thing\n",
			want:    0,
		},
		{
			name: "an editor runs with -v: the diff below the cut line is not the message",
			// The verbose diff carries lines that are prose to the linter; if it
			// were not cut, the NON-BREAKING: footer would stop being a trailer.
			editor:  true,
			verbose: true,
			message: ":fire:(x) drop the thing\n\nNON-BREAKING: it was dead\n",
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testGit(t, dir, "akira-toriyama", "init", "-q", "-b", "main")
			testGit(t, dir, "akira-toriyama", "commit", "-q", "--allow-empty", "-m", ":tada: begin the project")
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
	code, stdout, _ := runGlyph(t, "lint", "--message", ":bug: fix a crash")
	if code != 0 {
		t.Fatalf("clean lint --message exited %d, want 0", code)
	}
	if stdout != "" {
		t.Fatalf("clean lint --message wrote %q to stdout, want nothing", stdout)
	}
}

// TestLintMessageLegacyTokenHardErrors: the retired Conventional token is a
// hard error at authoring time — v1.0.0's meaning is one grammar, zero
// migration debt. Only the release walk over immutable history (which never
// runs Lint) still tolerates it. The violation carries the ready-to-paste
// canonical rewrite, so the fix is a copy, not an exegesis of the grammar.
func TestLintMessageLegacyTokenHardErrors(t *testing.T) {
	code, _, stderr := runGlyph(t, "lint", "--message", ":wrench: ci(hub): raise the zizmor gate")
	if code != 3 {
		t.Fatalf("legacy-format lint exited %d, want 3\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "legacy-token") {
		t.Fatalf("stderr does not carry the legacy-token rule id:\n%s", stderr)
	}
	if !strings.Contains(stderr, `:wrench:(hub) raise the zizmor gate`) {
		t.Fatalf("stderr does not suggest the canonical rewrite:\n%s", stderr)
	}
}

// TestLintMessageViolations: violations exit 3 with a structured stderr
// envelope carrying the stable rule ids, keeping stdout pure. The envelope is
// DECODED, not grepped — its keys ("error", "code", "details", "rule") are the
// machine API the lint reusable's jq annotations branch on.
func TestLintMessageViolations(t *testing.T) {
	code, stdout, stderr := runGlyph(t, "lint", "--message", ":bug: Fix a crash.")
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
		Rule string `json:"rule"`
	}
	if err := json.Unmarshal(env.Details, &details); err != nil {
		t.Fatalf("envelope details are not the violations array: %v\n%s", err, env.Details)
	}
	rules := make([]string, len(details))
	for i, d := range details {
		rules[i] = d.Rule
	}
	if len(rules) != 2 || rules[0] != "uppercase-subject" || rules[1] != "trailing-period" {
		t.Fatalf("violation rules = %v, want [uppercase-subject trailing-period] in stable order", rules)
	}
}

// TestLintMessageUnknownCode: an unknown gitmoji is a violation against the
// real embedded table — never a silent pass.
func TestLintMessageUnknownCode(t *testing.T) {
	code, _, stderr := runGlyph(t, "lint", "--message", ":not-a-real-code: fix a crash")
	if code != 3 {
		t.Fatalf("unknown code exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "unknown-gitmoji") {
		t.Fatalf("stderr envelope is missing unknown-gitmoji:\n%s", stderr)
	}
}

// TestLintStdin: --stdin reads the message from the input stream (the
// commit-msg hook path) and stays authoring-mode — :construction: is legal.
func TestLintStdin(t *testing.T) {
	setStdin(t, ":construction: try things\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 0 {
		t.Fatalf("lint --stdin exited %d, want 0 (WIP is legal at authoring time)\nstderr: %s", code, stderr)
	}
}

// TestLintStdinViolation: the stdin path still lints — a violation exits 3.
func TestLintStdinViolation(t *testing.T) {
	setStdin(t, "no gitmoji here\n")
	code, _, stderr := runGlyph(t, "lint", "--stdin")
	if code != 3 {
		t.Fatalf("lint --stdin with a bad message exited %d, want 3", code)
	}
	if !strings.Contains(stderr, "malformed-subject") {
		t.Fatalf("stderr envelope is missing malformed-subject:\n%s", stderr)
	}
}

// TestLintModeFlagsAreExclusiveAndRequired: exactly one of --range /
// --message / --stdin — none or two is a usage error (exit 2).
func TestLintModeFlagsAreExclusiveAndRequired(t *testing.T) {
	if code, _, _ := runGlyph(t, "lint"); code != 2 {
		t.Fatalf("lint with no mode exited %d, want 2", code)
	}
	if code, _, _ := runGlyph(t, "lint", "--message", ":bug: x", "--stdin"); code != 2 {
		t.Fatalf("lint with two modes exited %d, want 2", code)
	}
}

// TestLintRange: over a PR-shaped range the merge-candidate rules apply
// (:construction: blocks), excluded commits (bots, autosquash) are skipped
// rather than failed, and each violation carries its commit SHA.
func TestLintRange(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")   // bot: skipped
	testCommit(t, dir, "akira-toriyama", "fixup! :bug: fix a crash")   // autosquash: skipped
	testCommit(t, dir, "akira-toriyama", ":construction: try an idea") // WIP: violation here
	testCommit(t, dir, "akira-toriyama", "no gitmoji in this one")     // malformed: violation
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 3 {
		t.Fatalf("lint --range exited %d, want 3\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("violations must go to stderr, stdout got %q", stdout)
	}
	for _, want := range []string{"wip-merge-candidate", "malformed-subject"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr envelope is missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "build(deps)") {
		t.Fatalf("bot commit leaked into the violations:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"sha"`) {
		t.Fatalf("range violations must carry commit SHAs:\n%s", stderr)
	}
}

// TestLintRangeClean: a range of clean and skipped commits is a silent 0.
func TestLintRangeClean(t *testing.T) {
	dir, base := testRepo(t)
	testCommit(t, dir, "akira-toriyama", ":bug: fix a crash")
	testCommit(t, dir, "dependabot[bot]", "build(deps): bump a dep")
	testCommit(t, dir, "akira-toriyama", ":sparkles:(ui) add a menu\n\nBody.\n\n---（和訳）\nメニュー。")
	t.Chdir(dir)

	code, stdout, stderr := runGlyph(t, "lint", "--range", base+"..HEAD")
	if code != 0 {
		t.Fatalf("clean lint --range exited %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("clean lint --range wrote %q to stdout, want nothing", stdout)
	}
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
