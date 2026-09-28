package config

import (
	"strings"
	"testing"
)

// unlandableToml is the shape the unlandable key was made for (t-t84a): the
// sigil grammar, the fixup!/squash! skip, and git's amend! subject claimed as
// unlandable. No capture group on the amend! pattern — the key alone is a
// verdict source, so the "could never yield a verdict" load check admits it.
const unlandableToml = `schema = 1
exclude_authors = ['dependabot[bot]']

[[patterns]]
pattern = '^(?P<subject>:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) .+)'

[[patterns]]
pattern = '^(fixup|squash)! '
skip = true

[[patterns]]
pattern = '^amend! '
unlandable = 'rebase with --autosquash before it lands'
`

const amendMessage = "amend! :bug:~ fix b\n\n:boom:! fix b\n"

// TestMatchReportsUnlandableAsUnclaimed pins the shape decision: an unlandable
// claim comes back as the UNMATCHED shape with the reason beside it, never as
// a match. Every history consumer already refuses an unmatched message and
// already checks exclude_authors first, so one that never reads the new field
// fails closed; a Matched=true shape would hand SigilLevel a zero sigil and
// fold the commit as a silent none wherever a consumer forgot the new arm.
func TestMatchReportsUnlandableAsUnclaimed(t *testing.T) {
	cfg := mustLoad(t, unlandableToml)
	m, err := cfg.Match(amendMessage)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if m.Matched || m.Skip || m.PatternIndex != -1 {
		t.Fatalf("Match = %+v, want the unmatched shape (Matched=false, Skip=false, PatternIndex=-1)", m)
	}
	want := "patterns[2] marks this message unlandable: rebase with --autosquash before it lands"
	if m.Unlandable != want {
		t.Fatalf("Unlandable = %q, want %q", m.Unlandable, want)
	}
	if got := cfg.UnclaimedDetail(m); got != want {
		t.Errorf("UnclaimedDetail = %q, want the unlandable sentence %q", got, want)
	}

	plain, err := cfg.Match("no gitmoji at all")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if plain.Unlandable != "" {
		t.Errorf("a message no pattern claims carries Unlandable = %q, want empty", plain.Unlandable)
	}
	if got := cfg.UnclaimedDetail(plain); got != "matches none of the 3 configured patterns" {
		t.Errorf("UnclaimedDetail(unmatched) = %q", got)
	}
}

// TestUnlandableIsOrdinaryFirstMatchWins: the key is a verdict of the pattern
// that wins, not a scan over the file. fixup! on the outside of an amend! is
// what git turns into a plain fixup (measured, git 2.54: 'fixup! amend! X' →
// `fixup`, 'amend! fixup! X' → `fixup -C`), so the skip must win the first and
// the unlandable pattern the second.
func TestUnlandableIsOrdinaryFirstMatchWins(t *testing.T) {
	cfg := mustLoad(t, unlandableToml)
	outer, err := cfg.Match("fixup! amend! :bug:~ fix b")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !outer.Matched || !outer.Skip {
		t.Errorf("'fixup! amend! …' = %+v, want the skip (git squashes it as a plain fixup)", outer)
	}
	inner, err := cfg.Match("amend! fixup! :bug:~ fix b")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if inner.Matched || inner.Unlandable == "" {
		t.Errorf("'amend! fixup! …' = %+v, want unlandable (git applies it as fixup -C)", inner)
	}

	// The two cases above are each claimed by one pattern; these overlap, so
	// only file order separates the answers. An unlandable pattern below a
	// skip that also matches loses to it, and one above a sigil pattern that
	// also matches wins over it.
	below := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^(fixup|squash)! '\nskip = true\n[[patterns]]\npattern = 'amend! '\nunlandable = 'x'\n")
	if m, err := below.Match("fixup! amend! :bug:~ fix b"); err != nil || !m.Matched || !m.Skip || m.Unlandable != "" {
		t.Errorf("skip above an overlapping unlandable pattern = %+v (err %v), want the skip", m, err)
	}
	above := mustLoad(t, "schema = 1\n[[patterns]]\npattern = '^amend! '\nunlandable = 'x'\n[[patterns]]\npattern = '(?P<semver_sigil>[=~^!%]) '\n")
	if m, err := above.Match("amend! :bug:~ fix b"); err != nil || m.Matched || m.Unlandable == "" {
		t.Errorf("unlandable above an overlapping sigil pattern = %+v (err %v), want the unlandable claim", m, err)
	}
}

// TestNoteLineCannotCiteAnUnlandableGroup: a name only an unlandable pattern
// captures can never bind — Match reports the claim with no groups and the
// notes fall back to $subject — so citing it is the resolves-empty-forever
// template the loader refuses, exactly as if no pattern captured it.
func TestNoteLineCannotCiteAnUnlandableGroup(t *testing.T) {
	_, err := Load([]byte(`schema = 1
[[patterns]]
pattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'

[[patterns]]
pattern = '^amend! (?P<target>.+)'
unlandable = 'rebase with --autosquash before it lands'

[note]
line = '- $subject$[ (rewrites $target)]'
`))
	if err == nil || !strings.Contains(err.Error(), "$target") {
		t.Fatalf("Load = %v, want the note.line refusal naming $target", err)
	}
}

// TestLintRefusesUnlandable is the history half: CI's range, the pre-push
// hook and a pull request's title all judge through Lint, and an unlandable
// message is a violation there carrying the file author's reason — not the
// generic no-match sentence, whose remedy ("write it to match one") is wrong
// for a message a pattern did claim.
func TestLintRefusesUnlandable(t *testing.T) {
	cfg := mustLoad(t, unlandableToml)
	v := cfg.Lint(amendMessage, "akira")
	if v.OK || v.Excluded {
		t.Fatalf("Lint = %+v, want a violation", v)
	}
	if !strings.Contains(v.Reason, "unlandable: rebase with --autosquash before it lands") {
		t.Errorf("Reason = %q, want the unlandable pattern's reason", v.Reason)
	}
	if strings.Contains(v.Reason, "matches none") {
		t.Errorf("Reason = %q claims no pattern matched; the unlandable pattern did", v.Reason)
	}
	if bot := cfg.Lint(amendMessage, "dependabot[bot]"); !bot.Excluded {
		t.Errorf("an excluded author's unlandable message = %+v, want Excluded: exclusion precedes every message rule", bot)
	}
}

// TestLintAuthoringLetsUnlandableThrough is the authoring half, the reason the
// key exists instead of plain no-match: git spells amend! itself, so a hook
// refusal leaves --no-verify as the only way forward. The pass is loud — the
// warning carries the reason and says the later gates refuse it, which is
// what keeps the hook from blessing silently what CI rejects (DESIGN §2.1).
// Everything that is not an unlandable claim judges exactly as Lint does.
func TestLintAuthoringLetsUnlandableThrough(t *testing.T) {
	cfg := mustLoad(t, unlandableToml)
	v := cfg.LintAuthoring(amendMessage)
	if !v.OK {
		t.Fatalf("LintAuthoring = %+v, want OK: the hook must not refuse a subject git wrote", v)
	}
	for _, want := range []string{"rebase with --autosquash before it lands", "refuses it"} {
		if !strings.Contains(v.Warn, want) {
			t.Errorf("Warn = %q, want it to contain %q", v.Warn, want)
		}
	}

	for _, msg := range []string{":bug:~ fix b", "fixup! :bug:~ fix b", "no gitmoji at all"} {
		if a, h := cfg.LintAuthoring(msg), cfg.Lint(msg, ""); a != h {
			t.Errorf("LintAuthoring(%q) = %+v, Lint = %+v: only an unlandable claim may differ", msg, a, h)
		}
	}
}
