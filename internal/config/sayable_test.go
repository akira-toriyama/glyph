package config

import (
	"slices"
	"testing"
)

// sayableOf loads src and asks what a message patterns[i] claims can write.
func sayableOf(t *testing.T, src []byte, i int) Sayable {
	t.Helper()
	cfg, err := Load(src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s, ok := cfg.Sayable(i)
	if !ok {
		t.Fatalf("Sayable(%d) answered for no pattern", i)
	}
	return s
}

// TestSayableUnderTheShippedPresets pins the two patterns of each preset a
// landed commit's attribution can meet. The grammar captures an optional scope
// spelling every declared name and its own sigil, = included. The raw `git
// revert` pattern captures neither — its sigil is the fixed ~ — so a message it
// claims can write no escape, and both exist only under the grammar: the fact
// rule 3's refusal was blind to when it told a revert's author to name the
// package in the scope or write = (t-mfny (A), measured 2026-10-05 at 135eead:
// `Revert ":memo:(haiku)= add a README"` still exits 3).
func TestSayableUnderTheShippedPresets(t *testing.T) {
	const block = "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \".\"\nname = \"core\"\n"
	for _, preset := range []string{"gemoji", "conventional"} {
		t.Run(preset, func(t *testing.T) {
			data, ok := Preset(preset)
			if !ok {
				t.Fatalf("%s preset missing", preset)
			}
			src := append(append([]byte{}, data...), block...)

			grammar := sayableOf(t, src, 0)
			want := Sayable{Pattern: 0, ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "core"}, SigilGroup: true, None: true, RootNeedsName: true}
			if !sameSayable(grammar, want) {
				t.Errorf("patterns[0] = %+v, want %+v", grammar, want)
			}

			revert := sayableOf(t, src, 1)
			want = Sayable{Pattern: 1, ElsewhereScopes: []string{"haiku", "core"}, ElsewhereNone: true, RootNeedsName: true}
			if !sameSayable(revert, want) {
				t.Errorf("patterns[1] = %+v, want %+v — the revert pattern fixes its sigil and captures no scope", revert, want)
			}
		})
	}
}

func sameSayable(a, b Sayable) bool {
	return a.Pattern == b.Pattern && a.ScopeGroup == b.ScopeGroup && a.ScopeOptional == b.ScopeOptional &&
		a.SigilGroup == b.SigilGroup && a.None == b.None && a.ElsewhereNone == b.ElsewhereNone &&
		a.RootNeedsName == b.RootNeedsName &&
		slices.Equal(a.Scopes, b.Scopes) && slices.Equal(a.ElsewhereScopes, b.ElsewhereScopes)
}

// TestSayableReadsTheClaimingPatternsOwnGroups: what a message can write is
// asked of the groups Match reads — the scope group's own sub-expression per
// declared name, the sigil group's for =, each the LAST group of its name —
// and of whether a match can pass the scope by. One pattern per case, a
// two-package file.
func TestSayableReadsTheClaimingPatternsOwnGroups(t *testing.T) {
	const packages = "\n[[packages]]\npath = \"haiku\"\n\n[[packages]]\npath = \"curry\"\n"
	// wide keeps the file loadable when the pattern under test spells no name.
	const wide = "[[patterns]]\npattern = '^wide\\((?P<scope>[a-z]+)\\)(?P<semver_sigil>[~^]) '\n"
	for name, c := range map[string]struct {
		pattern string
		want    Sayable
	}{
		"a scope the message must write": {
			"pattern = '^(?P<type>[a-z]+)\\((?P<scope>[a-z]+)\\)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true},
		},
		"a scope behind a ?": {
			"pattern = '^(?P<type>[a-z]+)(\\((?P<scope>[a-z]+)\\))?(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true},
		},
		"a scope in one branch of an alternation": {
			"pattern = '^(?:x\\((?P<scope>[a-z]+)\\)|y)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true},
		},
		"a scope behind {0,1}": {
			"pattern = '^x(?:\\((?P<scope>[a-z]+)\\)){0,1}(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true},
		},
		"a scope that may capture nothing": {
			"pattern = '^x\\((?P<scope>[a-z]*)\\)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true},
		},
		"a scope narrowed to one name": {
			"pattern = '^x\\((?P<scope>curry|deps)\\)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, Scopes: []string{"curry"}, SigilGroup: true, None: true},
		},
		"a scope spelling no declared name": {
			"pattern = '^x\\((?P<scope>deps|ci)\\)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, SigilGroup: true, None: true},
		},
		"only the last of two scope groups is read": {
			"pattern = '^x\\((?:(?P<scope>[a-z]+)|(?P<scope>[A-Z]+))\\)(?P<semver_sigil>[=~^!%]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, SigilGroup: true, None: true},
		},
		"a sigil group that cannot be =": {
			"pattern = '^x(\\((?P<scope>[a-z]+)\\))?(?P<semver_sigil>[~^!]): '",
			Sayable{ScopeGroup: true, ScopeOptional: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true},
		},
		"an optional sigil over a fixed one": {
			"pattern = '^x(?P<semver_sigil>[=~^!%])?: '\nsemver_sigil = '~'",
			Sayable{SigilGroup: true, None: true},
		},
		"a fixed sigil and no scope": {
			"pattern = '^Revert '\nsemver_sigil = '~'",
			Sayable{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := sayableOf(t, []byte("schema = 1\n[[patterns]]\n"+c.pattern+"\n"+wide+packages), 0)
			c.want.ElsewhereScopes = []string{"haiku", "curry"}
			c.want.RootNeedsName = true // wide's scope group is read and does not spell "."
			if !sameSayable(got, c.want) {
				t.Errorf("Sayable(0) = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestSayableElsewhereCountsOnlyWhereALandedMessageMayGo: the reword a refusal
// offers must lead somewhere. A skip pattern's commit is placed nowhere and an
// unlandable one never lands, so neither is a pattern to reword under; a warn
// pattern is one the file's author accepts but would rather not see, so the
// refusal does not send a message there; and the claiming pattern is not its
// own elsewhere. A fixed = counts beside a captured one — either way the
// reworded commit moves no line.
func TestSayableElsewhereCountsOnlyWhereALandedMessageMayGo(t *testing.T) {
	const revert = "[[patterns]]\npattern = '^Revert '\nsemver_sigil = '~'\n"
	const packages = "\n[[packages]]\npath = \"haiku\"\n"
	for name, c := range map[string]struct {
		others string
		scopes []string
		none   bool
	}{
		"nothing else in the file":              {"", nil, false},
		"a grammar":                             {"[[patterns]]\npattern = '^:x:(\\((?P<scope>[a-z]+)\\))?(?P<semver_sigil>[=~^!%]) '\n", []string{"haiku"}, true},
		"a pattern fixed at =":                  {"[[patterns]]\npattern = '^chore: '\nsemver_sigil = '='\n", nil, true},
		"a pattern fixed at ^":                  {"[[patterns]]\npattern = '^feat: '\nsemver_sigil = '^'\n", nil, false},
		"only a skip pattern captures them":     {"[[patterns]]\npattern = '^Merge \\((?P<scope>[a-z]+)\\)(?P<semver_sigil>=)?'\nskip = true\n", nil, false},
		"only an unlandable one captures them":  {"[[patterns]]\npattern = '^fixup! \\((?P<scope>[a-z]+)\\)(?P<semver_sigil>=)?'\nunlandable = 'autosquash first'\n", nil, false},
		"only a warned pattern captures them":   {"[[patterns]]\npattern = '^:x:(\\((?P<scope>[a-z]+)\\))?(?P<semver_sigil>[=~^!%]) '\nwarn = 'write the new form'\n", nil, false},
		"a scope elsewhere, no = anywhere":      {"[[patterns]]\npattern = '^:x:\\((?P<scope>[a-z]+)\\)(?P<semver_sigil>[~^]) '\n", []string{"haiku"}, false},
		"a second revert-like pattern, nothing": {"[[patterns]]\npattern = '^Reapply '\nsemver_sigil = '~'\n", nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := sayableOf(t, []byte("schema = 1\n"+revert+c.others+packages), 0)
			if !slices.Equal(got.ElsewhereScopes, c.scopes) || got.ElsewhereNone != c.none {
				t.Errorf("elsewhere = %v, none %v; want %v, none %v", got.ElsewhereScopes, got.ElsewhereNone, c.scopes, c.none)
			}
			if got.ScopeGroup || got.SigilGroup || len(got.Scopes) != 0 || got.None {
				t.Errorf("the claiming pattern's own facts leaked from another pattern: %+v", got)
			}
		})
	}
}

// TestSayableRootNeedsNameIsTheLoadersAnswer: whether a refusal says the root
// declaration takes a name is the loader's verdict on `path = "."` alone —
// every pattern whose groups a commit binds, a warn pattern included — and not
// a reading of where a message could be reworded to, which leaves warn
// patterns out. Each case is put to Load itself with the root declared and no
// name, and the two must agree: a model of the loader's rule checked against
// itself would have passed the defect. Read off the reword view, a file whose
// only scope group sits in a warn pattern was told `path = "."` declares the
// root package, and that file exits 2 (measured 2026-10-05 at d0c1da7 on
// lint --range, bump --range, bump --since-tag and preview).
func TestSayableRootNeedsNameIsTheLoadersAnswer(t *testing.T) {
	const scopeless = "[[patterns]]\npattern = '^:[a-z_]+:(?P<semver_sigil>[=~^!%]) '\n"
	for name, c := range map[string]struct {
		patterns string
		want     bool
	}{
		"no pattern captures a scope":             {scopeless, false},
		"the claiming pattern captures one":       {"[[patterns]]\npattern = '^:[a-z_]+:(\\((?P<scope>[a-z0-9-]+)\\))?(?P<semver_sigil>[=~^!%]) '\n", true},
		"only a warned pattern captures one":      {scopeless + "[[patterns]]\npattern = '^:[a-z_]+:\\((?P<scope>[a-z0-9-]+)\\)(?P<semver_sigil>[=~^!%]) '\nwarn = 'scoped subjects are discouraged here'\n", true},
		"only a skip pattern captures one":        {scopeless + "[[patterns]]\npattern = '^Merge \\((?P<scope>[a-z]+)\\)'\nskip = true\n", false},
		"only an unlandable pattern captures one": {scopeless + "[[patterns]]\npattern = '^fixup! \\((?P<scope>[a-z]+)\\)'\nunlandable = 'autosquash first'\n", false},
		"a scope group that spells the default":   {"[[patterns]]\npattern = '^:[a-z_]+:(\\((?P<scope>[a-z0-9.-]+)\\))?(?P<semver_sigil>[=~^!%]) '\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			src := "schema = 1\n" + c.patterns + "\n[[packages]]\npath = \"haiku\"\n"
			got := sayableOf(t, []byte(src), 0)
			if got.RootNeedsName != c.want {
				t.Errorf("RootNeedsName = %v, want %v", got.RootNeedsName, c.want)
			}
			_, err := Load([]byte(src + "\n[[packages]]\npath = \".\"\n"))
			if refused := err != nil; refused != got.RootNeedsName {
				t.Errorf("RootNeedsName = %v, but the loader's answer to the root declared with no name is: %v", got.RootNeedsName, err)
			}
		})
	}
}

// TestSayableAnswersForNoPatternOutOfRange: a commit the fold does not read
// (an exclude_authors author) has no claiming pattern, and the caller's -1
// must not read patterns[0]'s facts.
func TestSayableAnswersForNoPatternOutOfRange(t *testing.T) {
	cfg, err := Load(gemojiWith(t, "\n[[packages]]\npath = \"haiku\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, i := range []int{-1, len(cfg.Patterns)} {
		if s, ok := cfg.Sayable(i); ok {
			t.Errorf("Sayable(%d) = %+v, want no answer", i, s)
		}
	}
}
