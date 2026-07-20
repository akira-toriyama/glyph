package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// TestParse exercises the canonical `<:code:>[(scope)][!] <subject>` grammar
// plus the lenient legacy acceptance: a `<type>[(scope)][!]:` token after the
// gitmoji is dropped (its scope salvaged when the new position has none, its
// `!` still meaning breaking) so pre-glyph history keeps parsing — no flag-day.
func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Commit
	}{
		{
			name: "minimal",
			in:   ":bug: fix a crash on empty input",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash on empty input"},
		},
		{
			name: "scope",
			in:   ":sparkles:(ui) add a right-click window menu",
			want: Commit{Gitmoji: ":sparkles:", Scope: "ui", Subject: "add a right-click window menu"},
		},
		{
			name: "breaking bang with scope",
			in:   ":boom:(api)! replace the --items flag with a positional arg",
			want: Commit{Gitmoji: ":boom:", Scope: "api", Breaking: true, Subject: "replace the --items flag with a positional arg"},
		},
		{
			name: "breaking bang without scope",
			in:   ":truck:! rename Store to Repository",
			want: Commit{Gitmoji: ":truck:", Breaking: true, Subject: "rename Store to Repository"},
		},
		{
			name: "kebab and digits in scope",
			in:   ":bug:(grid-1f-4) keep defaults when an unknown key is present",
			want: Commit{Gitmoji: ":bug:", Scope: "grid-1f-4", Subject: "keep defaults when an unknown key is present"},
		},
		{
			name: "underscore code",
			in:   ":white_check_mark: cover the fold with a permutation test",
			want: Commit{Gitmoji: ":white_check_mark:", Subject: "cover the fold with a permutation test"},
		},
		{
			name: "legacy type with scope",
			in:   ":wrench: ci(hub): raise the zizmor gate",
			want: Commit{Gitmoji: ":wrench:", Scope: "hub", Subject: "raise the zizmor gate"},
		},
		{
			name: "legacy type without scope",
			in:   ":bug: fix: keep defaults when an unknown key is present",
			want: Commit{Gitmoji: ":bug:", Subject: "keep defaults when an unknown key is present"},
		},
		{
			name: "legacy breaking bang",
			in:   ":truck: refactor(core)!: rename Store to Repository",
			want: Commit{Gitmoji: ":truck:", Scope: "core", Breaking: true, Subject: "rename Store to Repository"},
		},
		{
			name: "new scope wins over legacy scope",
			in:   ":wrench:(a) ci(b): raise the gate",
			want: Commit{Gitmoji: ":wrench:", Scope: "a", Subject: "raise the gate"},
		},
		{
			name: "non-type word with colon stays in the subject",
			in:   ":memo: note: document the squash-merge bump model",
			want: Commit{Gitmoji: ":memo:", Subject: "note: document the squash-merge bump model"},
		},
		{
			name: "parenthesized text after the space is subject, not scope",
			in:   ":bug: (ui) fix a crash",
			want: Commit{Gitmoji: ":bug:", Subject: "(ui) fix a crash"},
		},
		{
			name: "body after blank line",
			in:   ":bug: fix a crash\n\nGuard the nil map before indexing.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "Guard the nil map before indexing."},
		},
		{
			name: "body without blank separator is tolerated",
			in:   ":bug: fix a crash\nGuard the nil map.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "Guard the nil map."},
		},
		{
			name: "japanese translation body passes through verbatim",
			in:   ":bug: fix a crash\n\nGuard the nil map.\n\n---（和訳）\nクラッシュを修正。",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "Guard the nil map.\n\n---（和訳）\nクラッシュを修正。"},
		},
		{
			name: "breaking change footer",
			in:   ":truck: rename Store to Repository\n\nBREAKING CHANGE: Store is gone from the public API.",
			want: Commit{Gitmoji: ":truck:", Breaking: true, Subject: "rename Store to Repository", Body: "BREAKING CHANGE: Store is gone from the public API."},
		},
		{
			name: "breaking-change hyphen footer",
			in:   ":truck: rename Store to Repository\n\nBREAKING-CHANGE: Store is gone.",
			want: Commit{Gitmoji: ":truck:", Breaking: true, Subject: "rename Store to Repository", Body: "BREAKING-CHANGE: Store is gone."},
		},
		{
			name: "lowercase breaking phrase is not a footer",
			in:   ":bug: fix a crash\n\nbreaking change: this text is prose, not a footer.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "breaking change: this text is prose, not a footer."},
		},
		{
			name: "mid-line breaking phrase is not a footer",
			in:   ":bug: fix a crash\n\nSee BREAKING CHANGE: docs for details.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "See BREAKING CHANGE: docs for details."},
		},
		{
			// The trap this closes: prose wrapping onto the phrase at a line
			// start is not a footer, and must not force a major release.
			name: "wrapped prose starting a line with the phrase is not a footer",
			in:   ":bug: fix a crash\n\nSpell the footer NON-BREAKING:, uppercase, mirroring\nBREAKING CHANGE: for the same reason that one is uppercase.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "Spell the footer NON-BREAKING:, uppercase, mirroring\nBREAKING CHANGE: for the same reason that one is uppercase."},
		},
		{
			name: "a real footer still counts when it opens a block",
			in:   ":bug: fix a crash\n\nSome prose about the change.\n\nBREAKING CHANGE: the flag is gone.",
			want: Commit{Gitmoji: ":bug:", Breaking: true, Subject: "fix a crash", Body: "Some prose about the change.\n\nBREAKING CHANGE: the flag is gone."},
		},
		{
			name: "a footer stacked under another trailer still counts",
			in:   ":bug: fix a crash\n\nCo-Authored-By: Someone <a@b.c>\nBREAKING CHANGE: the flag is gone.",
			want: Commit{Gitmoji: ":bug:", Breaking: true, Subject: "fix a crash", Body: "Co-Authored-By: Someone <a@b.c>\nBREAKING CHANGE: the flag is gone."},
		},
		{
			name: "crlf line endings are normalized",
			in:   ":bug: fix a crash\r\n\r\nGuard the nil map.",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash", Body: "Guard the nil map."},
		},
		{
			name: "trailing newline from git %B is trimmed",
			in:   ":bug: fix a crash\n",
			want: Commit{Gitmoji: ":bug:", Subject: "fix a crash"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("Parse(%q):\n got  %+v\n want %+v", c.in, got, c.want)
			}
		})
	}
}

// TestParseRejects: a message that does not open with a well-formed textual
// gitmoji subject is a lint failure (exit code 3), never a silent zero Commit.
func TestParseRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty message", ""},
		{"blank message", "\n\n"},
		{"no gitmoji", "fix: keep defaults"},
		{"conventional only", "feat(ui): add a menu"},
		{"emoji glyph form", "✨ add a right-click menu"},
		{"no space after code", ":bug:fix a crash"},
		{"missing subject", ":bug: "},
		{"blank subject (two spaces)", ":bug:  "},
		{"whitespace-only subject", ":bug: \t "},
		{"unicode-blank subject (vertical tab)", ":bug: \v"},
		{"unicode-blank subject (nbsp)", ":bug:  "},
		{"double space before subject", ":bug:  fix a crash"},
		{"bare code", ":bug:"},
		{"uppercase code", ":Bug: fix a crash"},
		{"unclosed scope", ":bug:(ui fix a crash"},
		{"scope after bang", ":bug:!(ui) fix a crash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if err == nil {
				t.Fatalf("Parse(%q) should fail, got %+v", c.in, got)
			}
			ce := core.AsError(err)
			if ce == nil {
				t.Fatalf("Parse(%q) error is not a *core.Error: %v", c.in, err)
			}
			if ce.Code != core.CodeLint {
				t.Fatalf("Parse(%q) error code = %d, want %d (lint)", c.in, ce.Code, core.CodeLint)
			}
		})
	}
}

// testKnown is a toy membership oracle: the real one is a gitmoji.Table lookup,
// injected so the parser package never depends on the rules table.
func testKnown(code string) bool {
	switch code {
	case ":bug:", ":sparkles:", ":boom:", ":wrench:", ":memo:", ":construction:", ":truck:":
		return true
	}
	return false
}

// TestLint pins the five lint rules and their stable machine identifiers:
// malformed-subject, unknown-gitmoji, wip-merge-candidate, uppercase-subject,
// trailing-period. Rule order in the result is fixed so CI output is stable.
func TestLint(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		mergeCandidate bool
		want           []string // violation rule ids, in order
	}{
		{
			name: "clean new format",
			in:   ":bug: fix a crash",
			want: nil,
		},
		{
			name: "clean legacy format keeps linting",
			in:   ":wrench: ci(hub): raise the zizmor gate",
			want: nil,
		},
		{
			name: "malformed subject",
			in:   "feat: add a menu",
			want: []string{RuleMalformedSubject},
		},
		{
			name: "unknown gitmoji",
			in:   ":not-a-real-code: fix a crash",
			want: []string{RuleUnknownGitmoji},
		},
		{
			name: "trailing period",
			in:   ":bug: fix a crash.",
			want: []string{RuleTrailingPeriod},
		},
		{
			name: "uppercase subject",
			in:   ":bug: Fix a crash",
			want: []string{RuleUppercaseSubject},
		},
		{
			name: "identifier-start subject is a style violation by design",
			in:   ":truck:! Store becomes Repository",
			want: []string{RuleUppercaseSubject},
		},
		{
			name:           "wip blocks a merge candidate",
			in:             ":construction: try things",
			mergeCandidate: true,
			want:           []string{RuleWIPMergeCandidate},
		},
		{
			name: "wip is fine while authoring",
			in:   ":construction: try things",
			want: nil,
		},
		{
			name:           "violations accumulate in stable order",
			in:             ":not-a-real-code: Fix a crash.",
			mergeCandidate: true,
			want:           []string{RuleUnknownGitmoji, RuleUppercaseSubject, RuleTrailingPeriod},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Lint(c.in, LintOptions{Known: testKnown, MergeCandidate: c.mergeCandidate})
			var rules []string
			for _, v := range got {
				if v.Rule == "" || v.Detail == "" {
					t.Fatalf("Lint(%q) produced a violation with empty fields: %+v", c.in, v)
				}
				rules = append(rules, v.Rule)
			}
			if fmt.Sprint(rules) != fmt.Sprint(c.want) {
				t.Fatalf("Lint(%q) rules = %v, want %v", c.in, rules, c.want)
			}
		})
	}
}

// TestLintWithoutMembership: a nil Known skips only the membership rule — the
// shape and style rules still apply (used by callers that lint pure grammar).
func TestLintWithoutMembership(t *testing.T) {
	if vs := Lint(":not-a-real-code: fix a crash", LintOptions{}); len(vs) != 0 {
		t.Fatalf("Lint without Known should skip membership, got %+v", vs)
	}
	vs := Lint(":not-a-real-code: Fix a crash", LintOptions{})
	if len(vs) != 1 || vs[0].Rule != RuleUppercaseSubject {
		t.Fatalf("Lint without Known should still catch style rules, got %+v", vs)
	}
}

// FuzzParseNeverPanics: Parse must survive arbitrary bytes; when it accepts a
// message, the structural invariants hold (a shaped gitmoji, a non-blank
// subject — not merely non-empty: a whitespace-only subject would sail
// through every lint rule).
func FuzzParseNeverPanics(f *testing.F) {
	f.Add(":bug: fix a crash")
	f.Add(":sparkles:(ui)! add a menu")
	f.Add(":wrench: ci(hub): raise the gate\n\nBREAKING CHANGE: x")
	f.Add("")
	f.Add("✨ add a menu")
	f.Add(":bug:(ui\x00) fix")
	f.Add(":bug:  ")
	f.Fuzz(func(t *testing.T, msg string) {
		c, err := Parse(msg)
		if err != nil {
			return
		}
		if !strings.HasPrefix(c.Gitmoji, ":") || !strings.HasSuffix(c.Gitmoji, ":") || len(c.Gitmoji) < 3 {
			t.Fatalf("Parse(%q) accepted a malformed gitmoji %q", msg, c.Gitmoji)
		}
		if strings.TrimSpace(c.Subject) == "" {
			t.Fatalf("Parse(%q) accepted a blank subject %q", msg, c.Subject)
		}
	})
}

// FuzzParseRoundTrip: any well-formed new-format subject composed from valid
// parts parses back to exactly those parts. Subjects that themselves spell a
// legacy `<type>[(scope)][!]:` token are skipped — the lenient legacy pass
// deliberately eats those (documented one-way lossiness, no flag-day).
func FuzzParseRoundTrip(f *testing.F) {
	f.Add("bug", "ui", false, "fix a crash")
	f.Add("sparkles", "", true, "add a menu")
	f.Add("white_check_mark", "grid-1f-4", false, "cover the fold")
	f.Fuzz(func(t *testing.T, code, scope string, breaking bool, subject string) {
		code = sanitizeFuzz(code, "abcdefghijklmnopqrstuvwxyz0123456789_+-", "x")
		if code == "" || !lowerAlnum(code[0]) {
			code = "x" + code
		}
		scope = sanitizeFuzz(scope, "abcdefghijklmnopqrstuvwxyz0123456789-", "")
		if scope != "" && !lowerAlnum(scope[0]) {
			scope = "s" + scope
		}
		// Trim, don't just check: the shape requires the subject to OPEN with a
		// non-space (a leading space would be a malformed subject, not a
		// round-trip candidate).
		subject = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(subject))
		if subject == "" {
			t.Skip("empty subject after sanitizing")
		}
		if legacyTokenRE.MatchString(subject) {
			t.Skip("subject spells a legacy token — eaten by design")
		}

		msg := ":" + code + ":"
		want := Commit{Gitmoji: msg, Scope: scope, Breaking: breaking, Subject: subject}
		if scope != "" {
			msg += "(" + scope + ")"
		}
		if breaking {
			msg += "!"
		}
		msg += " " + subject

		got, err := Parse(msg)
		if err != nil {
			t.Fatalf("Parse(%q) rejected a well-formed message: %v", msg, err)
		}
		if got != want {
			t.Fatalf("Parse(%q):\n got  %+v\n want %+v", msg, got, want)
		}
	})
}

// lowerAlnum reports whether b may open a code or scope ([a-z0-9]).
func lowerAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// sanitizeFuzz keeps only bytes from allowed, lowercasing ASCII letters first;
// fallback seeds the value when everything was stripped.
func sanitizeFuzz(s, allowed, fallback string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(allowed, r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

// TestUndeclaredRemoval pins the rule that closes t-n158: a removal or rename
// must say whether it breaks anyone. glyph cannot know whether the removed
// symbol was public — that is the consuming repo's knowledge — so it refuses to
// let the question go unanswered rather than guessing.
func TestUndeclaredRemoval(t *testing.T) {
	known := func(string) bool { return true }
	has := func(vs []Violation, rule string) bool {
		for _, v := range vs {
			if v.Rule == rule {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name    string
		message string
		want    bool
	}{
		// The sill incident, reduced: a public preset pruned with :fire:,
		// nothing declared, released as minor and broke downstream wand.
		{name: "bare :fire: is undeclared", message: ":fire: prune catppuccin-latte", want: true},
		{name: "bare :coffin: is undeclared", message: ":coffin: drop the dead branch", want: true},
		// A rename is worse than a deletion: the old name resolves to something
		// else at runtime instead of failing.
		{name: "bare :truck: is undeclared", message: ":truck: rename FieldKit to ThemeKit", want: true},

		{name: "! declares it", message: ":fire:(theme)! prune catppuccin-latte", want: false},
		{
			name:    "BREAKING CHANGE footer declares it",
			message: ":fire: prune catppuccin-latte\n\nBREAKING CHANGE: the preset is gone",
			want:    false,
		},
		{
			name:    "NON-BREAKING footer declares it",
			message: ":fire: drop an unused private helper\n\nNON-BREAKING: the helper was never exported",
			want:    false,
		},
		// The footer is uppercase and case-sensitive, exactly like BREAKING
		// CHANGE:. A body may legitimately read "this is non-breaking: …", and a
		// footer that switches a rule off must not be spellable by accident.
		{
			name:    "lowercase non-breaking does not declare it",
			message: ":fire: drop a helper\n\nnon-breaking: it was never exported",
			want:    true,
		},
		{
			name:    "title-case Non-Breaking does not declare it",
			message: ":fire: drop a helper\n\nNon-Breaking: it was never exported",
			want:    true,
		},
		// A bare footer is the reflex answer — the magic word without the thought
		// the rule exists to force. It does not count.
		{
			name:    "NON-BREAKING with no reason does not declare it",
			message: ":fire: drop a helper\n\nNON-BREAKING:",
			want:    true,
		},
		{
			name:    "NON-BREAKING with blank reason does not declare it",
			message: ":fire: drop a helper\n\nNON-BREAKING:    ",
			want:    true,
		},

		// Everything that takes nothing away is untouched — this rule must not
		// tax ordinary work.
		{name: ":sparkles: is unaffected", message: ":sparkles: add a thing", want: false},
		{name: ":bug: is unaffected", message: ":bug: fix a thing", want: false},
		{name: ":wastebasket: is unaffected", message: ":wastebasket: deprecate the old call", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := Lint(tt.message, LintOptions{Known: known, MergeCandidate: true})
			if got := has(vs, RuleUndeclaredRemoval); got != tt.want {
				t.Errorf("undeclared-removal = %v, want %v (violations: %+v)", got, tt.want, vs)
			}
		})
	}
}

// A scope outside lowercase kebab-case used to surface as malformed-subject,
// which points at the whole line and sends the author hunting the gitmoji or
// the separating space when all they wrote was (Palette) for (palette) (t-edan).
//
// It bites hardest right here: undeclared-removal tells the author to add `!`,
// and the Swift repos this rule most protects (sill, wand, facet, halo, perch)
// scope by PascalCase module name — so following the instruction produced a
// confusing error. Worse, legacyTokenRE's scope slot is already `[^()]+`, so the
// RETIRED form accepts (Palette) while the canonical form rejects it.
func TestInvalidScope(t *testing.T) {
	known := func(string) bool { return true }

	tests := []struct {
		name     string
		message  string
		wantRule string
		wantHint string // substring the detail must carry, "" to skip
	}{
		{
			name:     "PascalCase scope names the scope and suggests the fix",
			message:  ":fire:(Palette)! prune catppuccin-latte",
			wantRule: RuleInvalidScope,
			wantHint: "write (palette)",
		},
		{
			name:     "camelCase scope suggests the fix",
			message:  ":sparkles:(themeKit) add a thing",
			wantRule: RuleInvalidScope,
			wantHint: "write (themekit)",
		},
		{
			name:     "a scope lowercasing cannot rescue gets no suggestion",
			message:  ":fire:(ThemeKit,ThemeKitUI) prune a preset",
			wantRule: RuleInvalidScope,
			wantHint: "lowercase kebab-case",
		},
		// Not a scope problem: these must keep the catch-all rule.
		{name: "no gitmoji is still malformed", message: "just a subject", wantRule: RuleMalformedSubject},
		{name: "missing space is still malformed", message: ":fire:prune a preset", wantRule: RuleMalformedSubject},
		{name: "blank subject after a scope is still malformed", message: ":fire:(Palette) ", wantRule: RuleMalformedSubject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := Lint(tt.message, LintOptions{Known: known})
			if len(vs) != 1 {
				t.Fatalf("want exactly 1 violation (the parse failure short-circuits), got %+v", vs)
			}
			if vs[0].Rule != tt.wantRule {
				t.Errorf("rule = %q, want %q (detail: %s)", vs[0].Rule, tt.wantRule, vs[0].Detail)
			}
			if tt.wantHint != "" && !strings.Contains(vs[0].Detail, tt.wantHint) {
				t.Errorf("detail %q does not carry %q", vs[0].Detail, tt.wantHint)
			}
		})
	}
}

// The canonical form must not be stricter than the retired one it replaced: a
// scope glyph rejects after `:fire:` cannot be a scope it accepts after a legacy
// `refactor(...)` token. This pins the asymmetry that made t-edan bite.
func TestKebabScopeAcceptedInBothForms(t *testing.T) {
	known := func(string) bool { return true }
	for _, msg := range []string{
		":fire:(palette)! prune catppuccin-latte",
		":fire: refactor(palette)!: prune catppuccin-latte",
	} {
		if vs := Lint(msg, LintOptions{Known: known}); len(vs) != 0 {
			t.Errorf("Lint(%q) = %+v, want clean", msg, vs)
		}
	}
}

// The rule fires at authoring time too — it is NOT gated on MergeCandidate the
// way wip-merge-candidate is. :construction: is legal mid-branch and illegal
// only at the merge, so its verdict really does change with time; whether a
// removal breaks anyone is settled when the commit is written. Deferring it to
// the range walk only moves the fix from one line in an open editor to a
// rewrite of already-pushed history.
func TestUndeclaredRemovalFiresAtAuthoringTime(t *testing.T) {
	vs := Lint(":fire: prune a preset", LintOptions{Known: func(string) bool { return true }})
	found := false
	for _, v := range vs {
		if v.Rule == RuleUndeclaredRemoval {
			found = true
		}
	}
	if !found {
		t.Errorf("undeclared-removal did not fire at authoring time (violations: %+v)", vs)
	}
}
