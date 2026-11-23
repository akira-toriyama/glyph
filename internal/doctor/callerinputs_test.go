package doctor

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// callerWith renders a minimal workflow calling one glyph reusable, with the
// given with: block spliced in verbatim under the job ("" omits it).
func callerWith(reusable, with string) string {
	b := "name: caller\non:\n  pull_request:\njobs:\n  j:\n    uses: akira-toriyama/glyph/.github/workflows/" + reusable + "@v4.2.0\n"
	if with != "" {
		b += with + "\n"
	}
	return b
}

// crlf rewrites a fixture with CRLF line endings, a workflow saved on
// Windows: every line, a blank one too, carries a trailing `\r`.
func crlf(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// requiringInstallNotes swaps the shipped table for the one of the era the
// measured incident happened in — release.yml requiring install-notes from
// v0.8.0 through v6.1.0 — for the rest of the test. No reusable requires an
// input today, so the judging has no live row to exercise it; the fixture
// table keeps it exercised. The doctor tests run sequentially (none calls
// t.Parallel), so the swap is safe.
func requiringInstallNotes(t *testing.T) {
	t.Helper()
	shipped := reusableRequiredInputs
	reusableRequiredInputs = map[string]inputRow{
		"lint.yml":       {After: "v0.1.0"},
		"release.yml":    {After: "v0.7.0", Required: []string{"install-notes"}},
		"pr-verdict.yml": {After: "v0.3.0"},
	}
	t.Cleanup(func() { reusableRequiredInputs = shipped })
}

// TestReusableRequiredInputsMatchTheShippedWorkflows holds the requirements
// table to the reusables it describes, exactly as the permissions table is
// held: a reusable growing a `required: true` input without a row here would
// ship a check that blesses callers GitHub kills at startup, and this turns
// that into a red diff instead. The shipped file is parsed with the same
// crude discipline the check itself lives by — a `required: true` line inside
// the workflow_call inputs block belongs to the nearest input key above it.
func TestReusableRequiredInputsMatchTheShippedWorkflows(t *testing.T) {
	for reusable, row := range reusableRequiredInputs {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", reusable))
		if err != nil {
			t.Fatalf("reading the shipped reusable: %v", err)
		}
		got := shippedRequiredInputs(t, string(body))
		wantSet := map[string]bool{}
		for _, w := range row.Required {
			wantSet[w] = true
		}
		if len(got) != len(wantSet) {
			t.Fatalf("%s requires %v but reusableRequiredInputs says %v — update the row, and move its After to the newest "+
				"release tag: every release so far required otherwise", reusable, got, row.Required)
		}
		for name := range got {
			if !wantSet[name] {
				t.Errorf("%s marks %q required but the table does not list it — the check now blesses callers GitHub kills. "+
					"Add it, and move the row's After to the newest release tag", reusable, name)
			}
		}
	}
}

// shippedRequiredInputs extracts the required input names from a reusable's
// workflow_call inputs block.
func shippedRequiredInputs(t *testing.T, body string) map[string]bool {
	t.Helper()
	required := map[string]bool{}
	inInputs := false
	inputsIndent := -1
	current := ""
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		ind := indentOf(line)
		if trimmed == "inputs:" && !inInputs {
			inInputs = true
			inputsIndent = ind
			continue
		}
		if !inInputs {
			continue
		}
		if ind <= inputsIndent {
			break // outputs:, secrets:, or the next top-level key — inputs is done
		}
		if ind == inputsIndent+2 && strings.HasSuffix(trimmed, ":") {
			current = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if trimmed == "required: true" && current != "" {
			required[current] = true
		}
	}
	return required
}

// TestCallerInputsOutcomes walks the verdicts over real files, under the
// fixture table (requiringInstallNotes): the measured silent startup_failure
// (a release caller omitting install-notes, glyph-test3 2026-08-26), the
// healthy shapes in both YAML orders and both with: forms, and the
// not-a-caller cases.
func TestCallerInputsOutcomes(t *testing.T) {
	requiringInstallNotes(t)
	blockWith := "    with:\n      install-notes: |\n        ## Install\n        nothing: to install\n"
	tests := []struct {
		name       string
		files      map[string]string
		want       Status
		wantDetail string
	}{
		{
			name:  "the measured incident: a release caller with no with: at all",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "")},
			want:  StatusFail, wantDetail: "install-notes",
		},
		{
			name: "a with: block that names other keys but not the required one",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      dry-run: true\n")},
			want: StatusFail, wantDetail: "install-notes",
		},
		{
			name:  "install-notes passed as a block scalar",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", blockWith)},
			want:  StatusPass,
		},
		{
			name: "a with: whose value is a block scalar is a form doctor does not read — its text is not keys",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: |\n      install-notes: x")},
			want: StatusUnknown, wantDetail: "does not read: job j, line 7 `with: |`",
		},
		{
			name: "an alias for with: is a form doctor does not read",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: *w\n")},
			want: StatusUnknown, wantDetail: "does not read: job j, line 7 `with: *w`",
		},
		{
			name: "a quoted key is the input it quotes",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      \"install-notes\": x\n")},
			want: StatusPass,
		},
		{
			name: "a missing input beside an unread with: still fails",
			files: map[string]string{
				".github/workflows/release.yml": callerWith("release.yml", "    with: *w\n"),
				".github/workflows/second.yml":  callerWith("release.yml", "    with:\n      dry-run: true\n"),
			},
			want: StatusFail, wantDetail: "never names install-notes",
		},
		{
			name: "CRLF: a blank line inside an if: scalar is the scalar's, not the end of the job",
			files: map[string]string{".github/workflows/release.yml": crlf(callerWith("release.yml",
				"    if: >-\n      true\n\n    with:\n      install-notes: x"))},
			want: StatusPass,
		},
		{
			name: "CRLF: a blank line between uses: and with: is blank, as its LF twin's is",
			files: map[string]string{".github/workflows/release.yml": crlf(callerWith("release.yml",
				"\n    with:\n      install-notes: x"))},
			want: StatusPass,
		},
		{
			name: "with: written ABOVE its uses: — YAML orders siblings freely",
			files: map[string]string{".github/workflows/release.yml": "name: caller\non:\n  push:\njobs:\n  j:\n" +
				blockWith + "    uses: akira-toriyama/glyph/.github/workflows/release.yml@v4.2.0\n"},
			want: StatusPass,
		},
		{
			name: "flow-form with: { install-notes: x }",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: { install-notes: nothing, dry-run: false }")},
			want: StatusPass,
		},
		{
			name:  "a lint caller needs no inputs",
			files: map[string]string{".github/workflows/commit-lint.yml": callerWith("lint.yml", "")},
			want:  StatusPass,
		},
		{
			name:  "no glyph caller at all",
			files: map[string]string{".github/workflows/ci.yml": "name: ci\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"},
			want:  StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := inputsAt(checkoutAt(t, tt.files), true, "dev")
			if c.Status != tt.want {
				t.Fatalf("status = %s, want %s (observed %q, details %v)", c.Status, tt.want, c.Observed, c.Details)
			}
			if tt.wantDetail != "" {
				joined := strings.Join(c.Details, "\n")
				if !strings.Contains(joined, tt.wantDetail) {
					t.Errorf("details %v never name %q", c.Details, tt.wantDetail)
				}
			}
		})
	}
}

// TestJobWithDropsEachLinesCommentBeforeTheFold holds the value jobWith hands
// a check that grades it: a comment ends where its line ends, so it is cut
// from each line before a deeper line folds on. Folded first, the value under
// `binary: # which tool` sat behind the comment and read as no value at all —
// an artifact input GitHub refuses, passed by workflow-caller-artifact
// (glyph-monorepo-test-2 run 43689423925; PyYAML reads `probe` there too).
func TestJobWithDropsEachLinesCommentBeforeTheFold(t *testing.T) {
	tests := []struct {
		name, with, want string
	}{
		{"a comment on the key line, the value under it", "    with:\n      binary: # which tool\n        probe\n", "probe"},
		{"a comment on the value's own line", "    with:\n      binary:\n        probe  # the tool\n", "probe"},
		{"a comment behind the value", "    with:\n      binary: probe # the tool\n", "probe"},
		{"a comment and nothing else", "    with:\n      binary: # nothing yet\n      dry-run: true\n", ""},
		{"a # inside quotes is the value", "    with:\n      binary: \"a # b\"  # c\n", `"a # b"`},
		{"a # with no space before it is the value", "    with:\n      binary: a#b\n", "a#b"},
		{"a block scalar's header", "    with:\n      binary: >-  # folded\n        probe\n", ">-"},
		{"flow form behind a comment", "    with: { binary: probe }  # the tool", "probe"},
		{"flow form, an empty value behind a comment", "    with: { binary: \"\" }  # none", `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(callerWith("release.yml", tt.with), "\n")
			uses := 0
			for i, l := range lines {
				if strings.Contains(l, "uses:") {
					uses = i + 1
				}
			}
			with, unread := jobWith(lines, uses)
			got, named := with["binary"]
			if unread != "" {
				t.Fatalf("jobWith left %q unread", unread)
			}
			if !named {
				t.Fatalf("binary is not a with: key of %q", tt.with)
			}
			if got != tt.want {
				t.Errorf("binary = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnreadWithIsCouldNotRunInBothCallerChecks holds DESIGN §7's grade for
// what a caller check cannot read: a with: in a form jobWith does not read is
// could-not-run in both checks that read it, naming the file, the job and the
// text, never a pass over keys it cannot see and never a fail over keys it
// cannot see either (an alias `with: *w` over `&w { binary: probe }` passed a
// caller release.yml refuses; PyYAML reads binary there).
func TestUnreadWithIsCouldNotRunInBothCallerChecks(t *testing.T) {
	requiringInstallNotes(t)
	files := map[string]string{".github/workflows/release.yml": callerWith("release.yml",
		"    env: &w { binary: probe, install-notes: x }\n    with: *w\n")}
	checks := map[string]Check{
		"workflow-caller-inputs":   inputsAt(checkoutAt(t, files), true, "dev"),
		"workflow-caller-artifact": artifactAt(checkoutAt(t, files), true, loaded(t, packagesConfig("camp"))),
	}
	observed := map[string]string{
		"workflow-caller-inputs":   "1 caller(s) whose with: doctor does not read; no judged caller is short",
		"workflow-caller-artifact": "1 caller(s) whose with: doctor does not read; no judged caller names an artifact input",
	}
	for id, c := range checks {
		if c.Status != StatusUnknown {
			t.Errorf("%s: status = %s, want unknown (observed %q, details %v)", id, c.Status, c.Observed, c.Details)
			continue
		}
		if c.Observed != observed[id] {
			t.Errorf("%s: observed = %q, want %q — each check's own word for what it judged", id, c.Observed, observed[id])
		}
		want := ".github/workflows/release.yml calls release.yml@v4.2.0 with a with: doctor does not read: job j, line 8 `with: *w`"
		if !slices.Contains(c.Details, want) {
			t.Errorf("%s: details %v never say %q", id, c.Details, want)
		}
		if !strings.Contains(c.Message, withUnreadWhy) || !strings.Contains(c.Message, "unverified here, not verified") {
			t.Errorf("%s: message %q must say which forms doctor reads and that it verified nothing", id, c.Message)
		}
		if c.Fix != withUnreadFix {
			t.Errorf("%s: fix = %q, want %q", id, c.Fix, withUnreadFix)
		}
	}
}

// TestCallerInputsAsksNothingOfWithWhereNothingIsRequired holds the inputs
// check to its question: at a pin that requires no input nothing can be
// missing, so a with: it does not read is no gap — read first, `with: *w` on
// a single-line repository made doctor exit 4 where 0 is right. The artifact
// check asks of the same with: and stays could-not-run.
func TestCallerInputsAsksNothingOfWithWhereNothingIsRequired(t *testing.T) {
	shipped := reusableRequiredInputs
	reusableRequiredInputs = map[string]inputRow{"release.yml": {After: "v0.7.0"}}
	t.Cleanup(func() { reusableRequiredInputs = shipped })
	files := map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: *w\n")}
	if c := inputsAt(checkoutAt(t, files), true, "dev"); c.Status != StatusPass {
		t.Errorf("workflow-caller-inputs: status = %s, want pass — nothing is required (observed %q, details %v)", c.Status, c.Observed, c.Details)
	}
	if c := artifactAt(checkoutAt(t, files), true, loaded(t, packagesConfig("camp"))); c.Status != StatusUnknown {
		t.Errorf("workflow-caller-artifact: status = %s, want unknown — it asks of with: (observed %q)", c.Status, c.Observed)
	}
}

// TestJobWithReadsTheFormsItNamesAndNoOther holds jobWith's principle: a form
// it positively recognises is read — a quoted key as the key it quotes, a
// flow mapping split on the commas outside quotes — and any other is unread,
// never a key set (each want is PyYAML's reading of the same lines).
func TestJobWithReadsTheFormsItNamesAndNoOther(t *testing.T) {
	tests := []struct {
		name, with string
		want       map[string]string // nil: unread
	}{
		{"a double-quoted key", "    with:\n      \"binary\": probe\n", map[string]string{"binary": "probe"}},
		{"a single-quoted key", "    with:\n      'binary': x\n", map[string]string{"binary": "x"}},
		{"a quoted key in the flow form", "    with: { \"binary\": probe }", map[string]string{"binary": "probe"}},
		{"a comma inside quotes", "    with: { install-notes: \"a, binary: b\" }", map[string]string{"install-notes": `"a, binary: b"`}},
		{"an apostrophe in a plain flow value", "    with: { a: don't, binary: probe }", map[string]string{"a": "don't", "binary": "probe"}},
		{"an alias value is kept for the grade", "    with:\n      binary: *e\n", map[string]string{"binary": "*e"}},
		{"with: behind a comment, the block form under it", "    with: # c\n      binary: probe\n", map[string]string{"binary": "probe"}},
		{"a job key spelled `with  :`", "    with  :\n      binary: probe\n", map[string]string{"binary": "probe"}},
		{"a quoted job key \"with\"", "    \"with\":\n      binary: probe\n", map[string]string{"binary": "probe"}},
		{"`with  :` in the flow form", "    with  : { binary: probe }", map[string]string{"binary": "probe"}},
		{"a quoted 'with' in the flow form", "    'with': { binary: probe }", map[string]string{"binary": "probe"}},
		{"a sequence entry at the job's indent", "    needs:\n    - build\n    with:\n      binary: probe\n", map[string]string{"binary": "probe"}},
		{"a quoted job key \"with\" over an alias", "    \"with\": *w\n", nil},
		{"a merge key at the job's indent", "    <<: *job\n", nil},
		{"an alias for with:", "    with: *w\n", nil},
		{"an anchored flow with:", "    with: &w { binary: probe }\n", nil},
		{"a tagged flow with:", "    with: !!map { binary: probe }\n", nil},
		{"a flow mapping closing on a later line", "    with: {\n      binary: probe }\n", nil},
		{"a nested collection in the flow form", "    with: { a: { b: c }, binary: probe }\n", nil},
		{"a merge key", "    with:\n      <<: *x\n", nil},
		{"a ? key", "    with:\n      ? binary\n      : probe\n", nil},
		{"a key carrying a tag", "    with:\n      !!str binary: probe\n", nil},
		{"a quoted key with an escape", "    with:\n      \"bin\\x61ry\": probe\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(callerWith("release.yml", tt.with), "\n")
			got, unread := jobWith(lines, 6)
			if tt.want == nil {
				if unread == "" || got != nil {
					t.Errorf("jobWith = %v, %q — want unread and no keys", got, unread)
				}
				return
			}
			if unread != "" || !maps.Equal(got, tt.want) {
				t.Errorf("jobWith = %v, %q — want %v, read", got, unread, tt.want)
			}
		})
	}
}

// TestCallerFixesNameWhereEachCallerLives holds the pointer both caller
// checks close their fix with to README itself: the lint.yml caller sits
// under Install, the other two under Getting started's step 3, and a fix
// naming step 3 for all three sent a failing lint.yml caller to a step that
// holds no lint caller.
func TestCallerFixesNameWhereEachCallerLives(t *testing.T) {
	requiringInstallNotes(t)
	lint := map[string]string{".github/workflows/commit-lint.yml": caller("lint.yml", "permissions:\n  contents: read")}
	if c := permsAt(checkoutAt(t, lint), true, "dev"); c.Status != StatusFail || !strings.HasSuffix(c.Fix, readmeCallers) {
		t.Errorf("workflow-caller-permissions: status %s, fix %q — want a fail whose fix ends with %q", c.Status, c.Fix, readmeCallers)
	}
	release := map[string]string{".github/workflows/release.yml": callerWith("release.yml", "")}
	if c := inputsAt(checkoutAt(t, release), true, "dev"); c.Status != StatusFail || !strings.HasSuffix(c.Fix, readmeCallers) {
		t.Errorf("workflow-caller-inputs: status %s, fix %q — want a fail whose fix ends with %q", c.Status, c.Fix, readmeCallers)
	}

	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README: %v", err)
	}
	readme := string(body)
	section := func(from, to string) string {
		t.Helper()
		_, rest, found := strings.Cut(readme, from)
		if !found {
			t.Fatalf("README has no %q", from)
		}
		sect, _, found := strings.Cut(rest, to)
		if !found {
			t.Fatalf("README has no %q after %q", to, from)
		}
		return sect
	}
	const uses = "uses: akira-toriyama/glyph/.github/workflows/"
	homes := []struct {
		pointer, from, to string
		holds, lacks      []string
	}{
		{`lint.yml's under Install ("GitHub Actions")`, "\n## Install\n", "\n## ", []string{"lint.yml"}, nil},
		{"pr-verdict.yml's and release.yml's under Getting started, step 3", "\n**3. ", "\n## ",
			[]string{"pr-verdict.yml", "release.yml"}, []string{"lint.yml"}},
	}
	for _, h := range homes {
		if !strings.Contains(readmeCallers, h.pointer) {
			t.Errorf("readmeCallers = %q, which never says %q", readmeCallers, h.pointer)
		}
		sect := section(h.from, h.to)
		for _, reusable := range h.holds {
			if !strings.Contains(sect, uses+reusable+"@") {
				t.Errorf("README's section from %q holds no %s caller, and the fix sends a reader there", h.from, reusable)
			}
		}
		for _, reusable := range h.lacks {
			if strings.Contains(sect, uses+reusable+"@") {
				t.Errorf("README's section from %q holds a %s caller: the fix names another home for it", h.from, reusable)
			}
		}
	}
	if install := section("\n## Install\n", "\n## "); !strings.Contains(install, "**GitHub Actions**") {
		t.Errorf("README's Install has no **GitHub Actions** paragraph, the one the fix names")
	}
}

// TestAbsentWorkflowsDirSplitsOnRootProvenance pins the t-wzsw decision for
// all three local caller-side checks at once: under a git-named root an
// absent .github/workflows is an observed absence (pass — a fresh repository
// can reach doctor exit 0 before wiring CI), while under a bare "." it stays
// unknown, because a wrong working directory reads exactly the same.
func TestAbsentWorkflowsDirSplitsOnRootProvenance(t *testing.T) {
	checks := map[string]func(string, bool) Check{
		"workflow-glyph-pins":         func(r string, v bool) Check { return pinsAt(r, v, nil, nil) },
		"workflow-caller-permissions": func(r string, v bool) Check { return permsAt(r, v, "dev") },
		"workflow-caller-inputs":      func(r string, v bool) Check { return inputsAt(r, v, "dev") },
	}
	for name, fn := range checks {
		t.Run(name, func(t *testing.T) {
			if c := fn(t.TempDir(), true); c.Status != StatusPass {
				t.Errorf("verified root, absent dir: status = %s, want %s (observed %q)", c.Status, StatusPass, c.Observed)
			}
			if c := fn(t.TempDir(), false); c.Status != StatusUnknown {
				t.Errorf("unverified root, absent dir: status = %s, want %s (observed %q)", c.Status, StatusUnknown, c.Observed)
			}
		})
	}
}

// TestUnlistableWorkflowsDirRemedyNamesTheCause holds the could-not-run arms
// of a workflows listing to the remedy of their cause. Without a git-named top
// level the remedy is a checkout — and only that: outside one doctor does not
// start without --repo, so the flag is no advice. Under one, doctor already
// reads that root from any subdirectory, so "the repository root" names a
// cause that is not there: a listing refused for permissions is the
// directory's permissions, and anything else at the path (a regular file
// here) must become a listable directory — the split checkConfig draws for
// glyph.toml. Each arm's Message is each check's own account of what the
// unread directory costs, held to the byte: the pin check continues the
// git-named arm's clause where the caller checks start a sentence.
func TestUnlistableWorkflowsDirRemedyNamesTheCause(t *testing.T) {
	const outside = "re-run from inside a git checkout of the repository being diagnosed"
	const refused = "fix the directory's permissions and re-run"
	const noRoot = "doctor reads the LOCAL checkout for this check, and git could not name one here. "
	const unread = "the checkout's workflows were never read"
	perms := "A caller granting less than its reusable declares dies as startup_failure before any job — unverified here, not verified"
	inputs := "A caller omitting a required input dies as startup_failure before any job — unverified here, not verified"
	checks := map[string]struct {
		run                     func(string, bool) Check
		outsideMsg, verifiedMsg string
	}{
		idWorkflowPinned: {func(r string, v bool) Check { return pinsAt(r, v, nil, nil) },
			noRoot + "If this repository genuinely has no workflows there is nothing pinned and nothing to drift — but that is unverified, not verified",
			unread + ", so whatever they pin is unverified, not verified"},
		idCallerPerms:  {func(r string, v bool) Check { return permsAt(r, v, "dev") }, noRoot + perms, unread + ". " + perms},
		idCallerInputs: {func(r string, v bool) Check { return inputsAt(r, v, "dev") }, noRoot + inputs, unread + ". " + inputs},
	}
	for id, tc := range checks {
		t.Run(id, func(t *testing.T) {
			check := func(arm string, c Check, fix, msg string) {
				t.Helper()
				if c.Status != StatusUnknown || c.Fix != fix {
					t.Errorf("%s: status %s, fix %q — want %s and %q", arm, c.Status, c.Fix, StatusUnknown, fix)
				}
				if c.Message != msg {
					t.Errorf("%s: message %q, want %q", arm, c.Message, msg)
				}
				if s := c.Message + " " + c.Fix; strings.Contains(s, "repository root") {
					t.Errorf("%s: %q sends the user to the repository root, which doctor finds from any subdirectory", arm, s)
				}
			}

			check("no git-named root", tc.run(t.TempDir(), false), outside, tc.outsideMsg)

			root := checkoutAt(t, map[string]string{".github/workflows": "not a directory\n"})
			check("a regular file at the workflows path", tc.run(root, true),
				"make "+filepath.Join(root, ".github", "workflows")+" a directory that can be listed, then re-run", tc.verifiedMsg)

			root = checkoutAt(t, map[string]string{".github/workflows/": ""})
			lockDir(t, filepath.Join(root, ".github", "workflows"))
			check("an unlistable directory under a git-named root", tc.run(root, true), refused, tc.verifiedMsg)
		})
	}
}

// lockDir makes dir unlistable, skipping the test where mode 000 does not stop
// a listing (root, or a filesystem that ignores modes).
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this filesystem (or a root-equivalent uid) ignores mode 000, so an unlistable directory cannot be staged")
	}
}
