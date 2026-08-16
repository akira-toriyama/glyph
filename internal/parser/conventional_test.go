package parser

import (
	"strings"
	"testing"
)

// TestParseConventional pins the conventional grammar's positive space
// (DESIGN §2.2): the type-colon form, the scope and `!` slots, and — the part
// that was nearly ratified away — that the FOOTER walk is the same shared
// code, so BREAKING CHANGE: and NON-BREAKING: classify exactly as they do
// under the gitmoji grammar.
func TestParseConventional(t *testing.T) {
	cases := []struct {
		in   string
		want Commit
	}{
		{"feat: add a menu", Commit{Token: "feat", Subject: "add a menu"}},
		{"fix(cli)!: drop the flag", Commit{Token: "fix", Scope: "cli", Breaking: true, Subject: "drop the flag"}},
		{"chore(deps-dev): bump a linter", Commit{Token: "chore", Scope: "deps-dev", Subject: "bump a linter"}},
		// An unknown type still PARSES — membership is the injected oracle's
		// question (unknown-type at lint, a hard error in bump.Classify),
		// mirroring how an unknown :code: parses under the gitmoji grammar.
		{"readme: fix a typo", Commit{Token: "readme", Subject: "fix a typo"}},
		// The footer walk is shared: a BREAKING CHANGE footer classifies
		// breaking under this grammar exactly as under gitmoji.
		{"feat(api): rename the field\n\nBREAKING CHANGE: consumers must re-map", Commit{
			Token: "feat", Scope: "api", Breaking: true,
			Subject: "rename the field", Body: "BREAKING CHANGE: consumers must re-map",
		}},
		// NON-BREAKING parses and is recorded here too. No conventional rule
		// reads it (undeclared-removal is gitmoji-only) — the asymmetry is in
		// the rules that consume the record, not in what is recorded.
		{"refactor: prune dead code\n\nNON-BREAKING: nothing exported was touched", Commit{
			Token: "refactor", NonBreaking: true,
			Subject: "prune dead code", Body: "NON-BREAKING: nothing exported was touched",
		}},
		// No legacy-token leniency under this grammar: there is no pre-profile
		// history to keep walking, so a colon phrase after the type is simply
		// the subject.
		{"feat: fix: the parser", Commit{Token: "feat", Subject: "fix: the parser"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Parse(c.in, GrammarConventional)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("Parse(%q):\n got  %+v\n want %+v", c.in, got, c.want)
			}
		})
	}
}

// TestParseConventionalRejects pins the negative space: shapes the grammar
// must refuse, each as a lint error — never a silently zero Commit.
func TestParseConventionalRejects(t *testing.T) {
	cases := []string{
		"Feat: uppercase type", // the type slot is lowercase, like every token in the house
		"feat:no space after colon",
		"feat : add a thing",     // the colon glues to the type/scope/bang, as the spec writes it
		"feat(cli): ",            // blank subject
		"feat: \v",               // Unicode-whitespace subject — TrimSpace decides blankness, not the regexp
		":sparkles: add a thing", // the other profile's grammar (sharpened to gitmoji-token in Lint)
		"add a thing",            // no type at all
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if got, err := Parse(in, GrammarConventional); err == nil {
				t.Fatalf("Parse(%q) should fail, got %+v", in, got)
			}
		})
	}
}

// TestConventionalInvalidScopeSharpens ports t-edan's argument to the second
// grammar: a subject whose one defect is its scope names the scope, not the
// whole line, and the fix — when lowercasing alone legalises it — is the full
// corrected line, mechanical repairs included.
func TestConventionalInvalidScopeSharpens(t *testing.T) {
	vs := Lint("fix(Palette): Prune the preset.", LintOptions{Grammar: GrammarConventional})
	if len(vs) != 1 || vs[0].Rule != RuleInvalidScope {
		t.Fatalf("Lint = %+v, want exactly one invalid-scope finding", vs)
	}
	if want := "fix(palette): prune the preset"; vs[0].Fix != want {
		t.Fatalf("invalid-scope fix = %q, want %q", vs[0].Fix, want)
	}
}

// TestGitmojiTokenSharpens pins the mirror image of legacy-token: a
// conventional-profile subject that IS the other grammar's well-formed shape
// gets the sharper name, no fix (a cross-vocabulary mapping is a guess), and
// a line that merely resembles a colon-word stays malformed-subject.
func TestGitmojiTokenSharpens(t *testing.T) {
	vs := Lint(":sparkles:(ui)! add a menu", LintOptions{Grammar: GrammarConventional})
	if len(vs) != 1 || vs[0].Rule != RuleGitmojiToken {
		t.Fatalf("Lint = %+v, want exactly one gitmoji-token finding", vs)
	}
	if vs[0].Fix != "" {
		t.Fatalf("gitmoji-token carries fix %q — a cross-vocabulary rewrite is a guess Fix must not bless", vs[0].Fix)
	}
	if !strings.Contains(vs[0].Detail, ":sparkles:") {
		t.Fatalf("gitmoji-token detail %q does not name the offending token", vs[0].Detail)
	}
	// A stray colon-word is NOT the other grammar: it stays malformed-subject,
	// so the sharper rule cannot become a catch-all for any leading colon.
	vs = Lint("::: add a menu", LintOptions{Grammar: GrammarConventional})
	if len(vs) != 1 || vs[0].Rule != RuleMalformedSubject {
		t.Fatalf("Lint(:::…) = %+v, want malformed-subject", vs)
	}
}

// TestConventionalFixIsPasteable is TestLintFixIsPasteable's clause for the
// second grammar: every Fix a conventional finding carries, pasted as the
// message's first line, lints green. The property is the field's whole
// contract and it must hold per grammar — a fix composed under the wrong
// separator would paste red.
func TestConventionalFixIsPasteable(t *testing.T) {
	known := func(typ string) bool { return typ == "feat" || typ == "fix" }
	opts := LintOptions{Grammar: GrammarConventional, Known: known, MergeCandidate: true}
	for _, msg := range []string{
		"fix: Fix a crash.",
		"feat(cli): Add a flag.",
		"fix(Palette): prune the preset",
	} {
		vs := Lint(msg, opts)
		if len(vs) == 0 {
			t.Fatalf("fixture %q lints clean — it must trip at least one fixable rule", msg)
		}
		for _, v := range vs {
			if v.Fix == "" {
				t.Fatalf("Lint(%q) finding %q carries no fix — pick fixtures whose repairs are mechanical", msg, v.Rule)
			}
			lines := strings.Split(msg, "\n")
			lines[0] = v.Fix
			if left := Lint(strings.Join(lines, "\n"), opts); len(left) != 0 {
				t.Fatalf("pasting fix %q for %q still lints red: %+v", v.Fix, msg, left)
			}
		}
	}
}

// TestFooterVerdictAgreesAcrossGrammars is the parity seed (the full suite is
// the parity task's): one body, two subject grammars, and the classification
// facts the body carries — Breaking, NonBreaking — must come out identical,
// because the footer walk is shared code. If this ever fails, a grammar has
// grown its own footer semantics, which is the drift DESIGN §2.2 ratified
// against.
func TestFooterVerdictAgreesAcrossGrammars(t *testing.T) {
	bodies := []string{
		"",
		"\n\nBREAKING CHANGE: consumers must re-map",
		"\n\nBREAKING-CHANGE: hyphenated spelling",
		"\n\nCloses #12\nBREAKING CHANGE: stacked under a colon-less reference",
		"\n\nNON-BREAKING: nothing exported was touched",
		"\n\nprose paragraph\nBREAKING CHANGE: wrapped into prose, must NOT count",
	}
	for _, body := range bodies {
		g, err := Parse(":bug: fix a crash"+body, GrammarGitmoji)
		if err != nil {
			t.Fatalf("gitmoji Parse(%q): %v", body, err)
		}
		c, err := Parse("fix: fix a crash"+body, GrammarConventional)
		if err != nil {
			t.Fatalf("conventional Parse(%q): %v", body, err)
		}
		if g.Breaking != c.Breaking || g.NonBreaking != c.NonBreaking {
			t.Fatalf("footer verdict split on body %q:\n gitmoji      Breaking=%v NonBreaking=%v\n conventional Breaking=%v NonBreaking=%v",
				body, g.Breaking, g.NonBreaking, c.Breaking, c.NonBreaking)
		}
	}
}

// FuzzParseConventionalNeverPanics is FuzzParseNeverPanics for the second
// grammar: any input either errors or yields a Commit whose token is a
// plausible type and whose subject is non-blank.
func FuzzParseConventionalNeverPanics(f *testing.F) {
	f.Add("fix: fix a crash")
	f.Add("feat(ui)!: add a menu")
	f.Add("chore: raise the gate\n\nBREAKING CHANGE: x")
	f.Add("")
	f.Add(":sparkles: add a menu")
	f.Add("fix(ui\x00): fix")
	f.Add("fix:  ")
	f.Fuzz(func(t *testing.T, msg string) {
		c, err := Parse(msg, GrammarConventional)
		if err != nil {
			return
		}
		if c.Token == "" || strings.ContainsAny(c.Token, ": ") {
			t.Fatalf("Parse(%q) accepted a malformed type %q", msg, c.Token)
		}
		if strings.TrimSpace(c.Subject) == "" {
			t.Fatalf("Parse(%q) accepted a blank subject %q", msg, c.Subject)
		}
	})
}

// FuzzParseConventionalRoundTrip: any well-formed conventional subject
// composed from valid parts parses back to exactly those parts. Unlike the
// gitmoji grammar there is no legacy-token skip — this grammar eats nothing.
func FuzzParseConventionalRoundTrip(f *testing.F) {
	f.Add("fix", "ui", false, "fix a crash")
	f.Add("feat", "", true, "add a menu")
	f.Add("chore", "deps-dev", false, "bump a linter")
	f.Fuzz(func(t *testing.T, typ, scope string, breaking bool, subject string) {
		typ = sanitizeFuzz(typ, "abcdefghijklmnopqrstuvwxyz0123456789-", "x")
		if typ == "" || typ[0] < 'a' || typ[0] > 'z' {
			t.Skip("type must open with a lowercase letter")
		}
		scope = sanitizeFuzz(scope, "abcdefghijklmnopqrstuvwxyz0123456789-", "")
		if scope != "" && !lowerAlnum(scope[0]) {
			scope = "s" + scope
		}
		subject = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(subject))
		if subject == "" {
			t.Skip("empty subject after sanitizing")
		}

		msg := typ
		want := Commit{Token: typ, Scope: scope, Breaking: breaking, Subject: subject}
		if scope != "" {
			msg += "(" + scope + ")"
		}
		if breaking {
			msg += "!"
		}
		msg += ": " + subject

		got, err := Parse(msg, GrammarConventional)
		if err != nil {
			t.Fatalf("Parse(%q) rejected a well-formed message: %v", msg, err)
		}
		if got != want {
			t.Fatalf("Parse(%q):\n got  %+v\n want %+v", msg, got, want)
		}
	})
}
