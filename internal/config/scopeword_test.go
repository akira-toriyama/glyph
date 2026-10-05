package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gemojiWith is the shipped gemoji preset with a [[packages]] block appended —
// the file an adopter holds, so the scope grammar asked is the one init writes.
func gemojiWith(t *testing.T, block string) []byte {
	t.Helper()
	data, ok := Preset("gemoji")
	if !ok {
		t.Fatal("gemoji preset missing")
	}
	return append(append([]byte{}, data...), block...)
}

// TestLoadRefusesAPackageNameNoScopeCanSpell: under the shipped grammar a
// package name the scope group cannot capture is a line no commit can name —
// rule 3's refusal offers it as the escape, and the scope that writes it
// matches no pattern (measured 2026-09-29: `:memo:(my_lib)~` refused by
// lint --range and lint --message alike). The refusal quotes the pattern as
// written, not a regexp/syntax rendering of it, and names both remedies. The
// root package is held to the rule: its default "." is no preset's word.
func TestLoadRefusesAPackageNameNoScopeCanSpell(t *testing.T) {
	for name, c := range map[string]struct{ block, want string }{
		"underscore default":              {"\n[[packages]]\npath = \"my_lib\"\n", `the default name "my_lib" (the last path segment)`},
		"uppercase default":               {"\n[[packages]]\npath = \"MyLib\"\n", `the default name "MyLib" (the last path segment)`},
		"dotted default":                  {"\n[[packages]]\npath = \"v2.0\"\n", `the default name "v2.0" (the last path segment)`},
		"major subdirectory default":      {"\n[[packages]]\npath = \"pubsub/v2\"\n", `the default name "pubsub/v2" (a major version subdirectory keeps its parent)`},
		"root default":                    {"\n[[packages]]\npath = \".\"\n", `the default name "." (the root package's path)`},
		"explicit name the group refuses": {"\n[[packages]]\npath = \"haiku\"\nname = \"Haiku\"\n", `name "Haiku" is not a word`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(gemojiWith(t, c.block))
			if err == nil {
				t.Fatal("Load succeeded: a package no commit scope can name loaded")
			}
			for _, want := range []string{"packages[0] (", c.want, "patterns[0]", "[a-z0-9-]+", "set name", "change that scope group"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, want substring %q", err, want)
				}
			}
			if strings.Contains(err.Error(), `[\-0-9a-z]+`) {
				t.Errorf("refusal = %q quotes regexp/syntax's rendering of the group, not the file's", err)
			}
		})
	}
	for name, block := range map[string]string{
		"major subdirectory named": "\n[[packages]]\npath = \"pubsub/v2\"\nname = \"pubsub-v2\"\n",
		"root named":               "\n[[packages]]\npath = \".\"\nname = \"core\"\n",
		"spellable default":        "\n[[packages]]\npath = \"travel/onsen\"\n",
		"underscore path, named":   "\n[[packages]]\npath = \"my_lib\"\nname = \"my-lib\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(gemojiWith(t, block)); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

// scopeSubject is the preset's subject pattern alone: one scope group,
// [a-z0-9-]+, the fleet's only alphabet (DESIGN §4.1 holds the census).
const scopeSubject = "[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(\\((?P<scope>[a-z0-9-]+)\\))?(?P<semver_sigil>[=~^!%]) .+)'\n"

// TestPackageNameScopeIsTheUnionOfReadScopes: one scope group that a commit's
// placement reads spelling the name is enough — which pattern wins is a
// property of each commit, the union validateLineNames takes — and a group
// placement never reads spells nothing: a skip pattern's commit is placed
// nowhere, an unlandable one is reported unclaimed with no groups, and of two
// groups named scope in one pattern Match keeps the last, whose capture
// overwrites the first even when its own alternative did not take part. The
// group is read under the pattern's own flags, so a (?i) group spells MyLib.
func TestPackageNameScopeIsTheUnionOfReadScopes(t *testing.T) {
	const shadowed = "[[patterns]]\npattern = '^:x:\\((?:(?P<scope>[a-z_]+)|(?P<scope>[A-Z]+))\\)(?P<semver_sigil>[=~^!%]) '\n"
	for name, src := range map[string]string{
		"a second grammar spells it":           scopeSubject + "[[patterns]]\npattern = '^(?P<type>[a-z]+)\\((?P<scope>[a-z_]+)\\)(?P<semver_sigil>[=~^!%]): .+'\n\n[[packages]]\npath = \"my_lib\"\n",
		"a narrow scope beside a wide one":     "[[patterns]]\npattern = '^(?P<scope>deps)(?P<semver_sigil>[=~^!%]) '\n" + scopeSubject + "\n[[packages]]\npath = \"haiku\"\n",
		"a fixed-sigil pattern's scope counts": scopeSubject + "[[patterns]]\npattern = '^revert\\((?P<scope>[a-z_]+)\\) .+'\nsemver_sigil = '~'\n\n[[packages]]\npath = \"my_lib\"\n",
		"the last of two scope groups spells":  shadowed + "\n[[packages]]\npath = \"lib\"\nname = \"MYLIB\"\n",
		"a case-folding group spells it":       "[[patterns]]\npattern = '(?i)^:[a-z]+:\\((?P<scope>[a-z]+)\\)(?P<semver_sigil>[=~^!%]) '\n\n[[packages]]\npath = \"MyLib\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load([]byte("schema = 1\n" + src)); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
	for name, src := range map[string]string{
		"only a skip pattern spells it":         scopeSubject + "[[patterns]]\npattern = '^Merge (?P<scope>.+)'\nskip = true\n",
		"only an unlandable pattern spells it":  scopeSubject + "[[patterns]]\npattern = '^amend! (?P<scope>.+)'\nunlandable = 'rebase with --autosquash first'\n",
		"only a shadowed scope group spells it": shadowed,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load([]byte("schema = 1\n" + src + "\n[[packages]]\npath = \"my_lib\"\n"))
			if err == nil || !strings.Contains(err.Error(), `"my_lib"`) {
				t.Fatalf("Load = %v, want the refusal naming my_lib: a scope no commit's placement reads spelled the name", err)
			}
		})
	}
}

// TestPackagesUnderAScopelessGrammarAreNotNamed is the positive control: a
// file whose patterns capture no scope has decided a commit carries none
// (whether a scope exists is the pattern file's decision, DESIGN §2), so its
// packages are placed by their paths alone and any name loads — the root's
// bare "." included.
func TestPackagesUnderAScopelessGrammarAreNotNamed(t *testing.T) {
	src := "schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n\n[[packages]]\npath = \"my_lib\"\n\n[[packages]]\npath = \".\"\n"
	if _, err := Load([]byte(src)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// TestLoadRefusesAPathGitCannotTag asks git itself, never a copy of its
// rules: a path loads exactly when git accepts both names the line derives
// from it — the stepped tag <prefix>vX.Y.Z and the placeholder
// <path>/Unreleased — as refnames (git check-ref-format) AND as tag names
// (git tag, what a human cuts a line's tag with, the wedge refusals' remedy
// included), which also refuses a name beginning with "-". Measured before
// the rule (2026-09-29): `bump` printed `a b/v0.1.0` at exit 0 for a tag
// `git tag` refuses; and before the git tag half (2026-10-04), `-dash`
// loaded and printed `-dash/v0.0.1`, which check-ref-format accepts and git
// tag refuses.
func TestLoadRefusesAPathGitCannotTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gitTag := tagRepo(t)
	for _, p := range []string{
		"haiku", "travel/onsen", "pubsub/v2", "v2", "trail.", "a./b", "@", "a@b", "a/-b", "a-", "日本語",
		"-dash", "-", "-x/v2",
		"a b", "x.lock", "a.lock/b", "d/e.lock", "a..b", ".hid", "a/.b", "c~d", "e:f", "q?r", "s*t", "u[v",
		"y^z", "a@{b", "w\\x", "a\tb", "del\x7f", "nul\x01",
	} {
		gitAccepts := true
		for _, name := range []string{Package{Path: p}.TagPrefix() + "v0.1.0", p + "/Unreleased"} {
			if exec.Command("git", "check-ref-format", "refs/tags/"+name).Run() != nil || !gitTag(name) {
				gitAccepts = false
			}
		}
		_, err := Load([]byte(packagesToml("\n[[packages]]\npath = " + tomlBasic(p) + "\n")))
		if (err == nil) != gitAccepts {
			t.Errorf("path %q: Load error %v, while git accepts its tag names: %v", p, err, gitAccepts)
		}
		if err != nil && !strings.Contains(err.Error(), "git check-ref-format") && !strings.Contains(err.Error(), "git tag refuses") {
			t.Errorf("path %q: refusal = %q, want it to name git's rule", p, err)
		}
	}
}

// tagRepo makes a scratch repository with one commit and returns whether
// `git tag` there creates a given name, deleting it again so no name shadows
// a later one. The env holds out the user's git config and background
// maintenance as testutil.GitEnv does — that package imports this one, so it
// cannot be imported here.
func tagRepo(t *testing.T) func(name string) bool {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=gc.auto", "GIT_CONFIG_VALUE_0=0",
		"GIT_CONFIG_KEY_1=gc.autoDetach", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=maintenance.auto", "GIT_CONFIG_VALUE_2=false",
	)
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		return cmd.Run()
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "x"}} {
		if err := git(args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return func(name string) bool {
		if git("tag", "--", name) != nil {
			return false
		}
		if err := git("tag", "-d", name); err != nil {
			t.Fatalf("git tag -d %q: %v", name, err)
		}
		return true
	}
}

// tomlBasic writes s as a TOML basic string, escaping what the corpus holds.
func tomlBasic(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\t", `\t`, "\x7f", `\u007f`, "\x01", `\u0001`).Replace(s) + `"`
}
