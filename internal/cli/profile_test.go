package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/conventional"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// TestProfileSelectsTheGrammar pins the flag's whole point end to end: one
// message, two profiles, opposite verdicts — and each profile hard-errors on
// the OTHER vocabulary (legacy-token's bleed detection, both ways), so a repo
// cannot half-adopt a grammar without hearing about it.
func TestProfileSelectsTheGrammar(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		message string
		want    int
		rule    string // asserted in the envelope details when want != 0
	}{
		{"conventional message under conventional", []string{"--profile=conventional"}, "feat(cli)!: add a flag", 0, ""},
		{"conventional message under default", nil, "feat(cli)!: add a flag", 3, parser.RuleMalformedSubject},
		{"gitmoji message under default", nil, ":sparkles:(cli)! add a flag", 0, ""},
		{"gitmoji message under conventional", []string{"--profile=conventional"}, ":sparkles:(cli)! add a flag", 3, parser.RuleGitmojiToken},
		{"unknown type under conventional", []string{"--profile=conventional"}, "readme: fix a typo", 3, parser.RuleUnknownType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"lint", "--message", c.message}, c.args...)
			code, _, stderr := runGlyph(t, args...)
			if code != c.want {
				t.Fatalf("lint --message %q %v exited %d, want %d\nstderr: %s", c.message, c.args, code, c.want, stderr)
			}
			if c.rule == "" {
				return
			}
			env := decodeErrorEnvelope(t, stderr)
			var details []struct {
				Rule string `json:"rule"`
			}
			if err := json.Unmarshal(env.Details, &details); err != nil || len(details) == 0 {
				t.Fatalf("envelope details undecodable (%v):\n%s", err, stderr)
			}
			if details[0].Rule != c.rule {
				t.Fatalf("finding rule = %q, want %q", details[0].Rule, c.rule)
			}
		})
	}
}

// TestUnknownProfileIsUsage: naming a vocabulary glyph does not ship is an
// invocation defect — exit 2, never 3 (the fleet's lint gates hard-fail on 3,
// and a typo in a workflow input must not read as a convention violation).
func TestUnknownProfileIsUsage(t *testing.T) {
	code, _, stderr := runGlyph(t, "lint", "--message", "feat: x", "--profile=angular")
	if code != 2 {
		t.Fatalf("--profile=angular exited %d, want 2 (usage)\nstderr: %s", code, stderr)
	}
	if env := decodeErrorEnvelope(t, stderr); !strings.Contains(env.Message, "angular") {
		t.Fatalf("usage error does not name the offending profile: %s", env.Message)
	}
}

// TestRulesFollowsTheProfile: every `rules` surface answers for the selected
// vocabulary — the JSON is the conventional rules.json verbatim, the Markdown
// is its table, and --lint is LintRules(conventional), not a copy.
func TestRulesFollowsTheProfile(t *testing.T) {
	code, got, stderr := runGlyph(t, "rules", "--json", "--profile=conventional")
	if code != 0 {
		t.Fatalf("rules --json --profile=conventional exited %d\nstderr: %s", code, stderr)
	}
	stored, err := os.ReadFile(filepath.Join("..", "conventional", "rules.json"))
	if err != nil {
		t.Fatalf("reading conventional rules.json: %v", err)
	}
	if strings.TrimRight(got, "\n") != strings.TrimRight(string(stored), "\n") {
		t.Fatal("rules --json --profile=conventional does not reproduce the embedded conventional rules.json")
	}

	code, got, _ = runGlyph(t, "rules", "--profile=conventional")
	if code != 0 || !strings.HasPrefix(got, "# conventional type") {
		t.Fatalf("rules --profile=conventional should print the conventional Markdown table (exit %d): %q", code, got[:min(len(got), 60)])
	}

	code, got, stderr = runGlyph(t, "rules", "--lint", "--profile=conventional")
	if code != 0 {
		t.Fatalf("rules --lint --profile=conventional exited %d\nstderr: %s", code, stderr)
	}
	var payload struct {
		Rules []parser.LintRule `json:"rules"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("rules --lint output undecodable: %v", err)
	}
	if want := parser.LintRules(parser.GrammarConventional); !reflect.DeepEqual(payload.Rules, want) {
		t.Fatalf("rules --lint --profile=conventional printed %+v, want %+v", payload.Rules, want)
	}
}

// TestBumpRangeFollowsTheProfile is the verdict path end to end on a real
// (hermetic) repository: conventional commits classify under the conventional
// table — feat minors, fix patches, a BREAKING CHANGE footer majors — and the
// same range under the default profile is a hard lint error, not a quiet none.
func TestBumpRangeFollowsTheProfile(t *testing.T) {
	dir, base := testRepo(t)
	t.Chdir(dir)
	testCommit(t, dir, "akira-toriyama", "fix(cli): correct the flag")
	testCommit(t, dir, "akira-toriyama", "feat: add a menu")

	code, got, stderr := runGlyph(t, "bump", "--range", base+"..HEAD", "--profile=conventional", "--json")
	if code != 0 {
		t.Fatalf("bump --profile=conventional exited %d\nstderr: %s", code, stderr)
	}
	var res struct {
		Level string `json:"level"`
		Next  string `json:"next"`
	}
	if err := json.Unmarshal([]byte(got), &res); err != nil {
		t.Fatalf("bump --json undecodable: %v\n%s", err, got)
	}
	if res.Level != "minor" || res.Next != "v0.2.0" {
		t.Fatalf("bump = %s/%s, want minor/v0.2.0", res.Level, res.Next)
	}

	if code, _, _ = runGlyph(t, "bump", "--range", base+"..HEAD", "--json"); code != 3 {
		t.Fatalf("the same conventional range under the default profile exited %d, want 3 — a foreign vocabulary is a hard error, never a silent none", code)
	}

	testCommit(t, dir, "akira-toriyama", "refactor: rename the field\n\nBREAKING CHANGE: consumers must re-map")
	code, got, stderr = runGlyph(t, "bump", "--range", base+"..HEAD", "--profile=conventional", "--json")
	if code != 0 {
		t.Fatalf("bump after breaking footer exited %d\nstderr: %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(got), &res); err != nil {
		t.Fatalf("bump --json undecodable: %v", err)
	}
	if res.Level != "major" || res.Next != "v1.0.0" {
		t.Fatalf("bump = %s/%s, want major/v1.0.0 (the footer walk is shared across grammars)", res.Level, res.Next)
	}
}

// TestHookInstallCarriesTheProfile: the installed hook is the one artefact
// that outlives the invocation, so the profile must be spelled INTO it — and
// the default profile's bytes must stay exactly the pre-profile bytes, or a
// routine refresh would mark every fleet hook stale.
func TestHookInstallCarriesTheProfile(t *testing.T) {
	dir, _ := testRepo(t)
	t.Chdir(dir)
	code, _, stderr := runGlyph(t, "hook", "install", "--profile=conventional")
	if code != 0 {
		t.Fatalf("hook install --profile=conventional exited %d\nstderr: %s", code, stderr)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "commit-msg"))
	if err != nil {
		t.Fatalf("reading installed hook: %v", err)
	}
	if !strings.Contains(string(body), "glyph lint --stdin --profile=conventional <") {
		t.Fatal("installed commit-msg hook does not pass --profile=conventional to the lint")
	}
	pp, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "pre-push"))
	if err != nil {
		t.Fatalf("reading installed pre-push hook: %v", err)
	}
	if !strings.Contains(string(pp), "glyph hook pre-push --profile=conventional -- ") {
		t.Fatal("installed pre-push hook does not pass --profile=conventional")
	}
	// The default profile interpolates NOTHING: pre-profile installs must
	// compare equal, so the fleet's hooks read "unchanged", not "stale".
	if strings.Contains(hook.Kinds("")[0].Script, "--profile") || strings.Contains(hook.Kinds("gitmoji")[0].Script, "--profile") {
		t.Fatal("the default profile's hook bytes carry a --profile flag — every fleet hook would go stale")
	}
	if hook.Kinds("")[0].Script != hook.Kinds("gitmoji")[0].Script {
		t.Fatal(`Kinds("") and Kinds("gitmoji") disagree — the two spellings of the default must be one set of bytes`)
	}
}

// TestConventionalTableReachesClassify pins the rows the bump walk above
// cannot economically exercise (perf, revert, a none type) through the same
// Load the CLI wires, so a table edit that survives the conventional
// package's own tests still cannot reach the verdict path unnoticed.
func TestConventionalTableReachesClassify(t *testing.T) {
	tbl, err := conventional.Load()
	if err != nil {
		t.Fatalf("conventional.Load(): %v", err)
	}
	for typ, want := range map[string]string{"perf": "patch", "revert": "patch", "docs": "none"} {
		r, ok := tbl.Lookup(typ)
		if !ok {
			t.Fatalf("type %q missing from the loaded table", typ)
		}
		if string(r.Bump) != want {
			t.Fatalf("type %q bump = %s, want %s", typ, r.Bump, want)
		}
	}
}
