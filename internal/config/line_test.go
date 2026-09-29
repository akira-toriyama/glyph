package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render is a stand-in for the notes renderer: it walks the spans the way
// internal/notes does — a placeholder resolves through the table, an optional
// span drops whole when any placeholder in it resolves empty — so this
// package can assert the SHAPE it compiles without importing the renderer
// (which imports this package).
func render(spans []LineSpan, vals map[string]string) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Optional {
			empty := false
			for _, p := range s.Parts {
				if p.Placeholder && vals[p.Text] == "" {
					empty = true
				}
			}
			if empty {
				continue
			}
		}
		for _, p := range s.Parts {
			if p.Placeholder {
				b.WriteString(vals[p.Text])
			} else {
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

// TestParseLineOptionalSpan pins both arms of the shipped preset's template.
// Asserting only the empty arm would pass with the span feature deleted (a
// template that renders nothing optional renders nothing optional); asserting
// only the populated arm would pass with Optional ignored. The pair is the
// assertion.
func TestParseLineOptionalSpan(t *testing.T) {
	spans, err := ParseLine("- $subject$[ ($pr)] @$author")
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	withPull := render(spans, map[string]string{"subject": "add the demo feature", "pr": "#61", "author": "akira-toriyama"})
	if withPull != "- add the demo feature (#61) @akira-toriyama" {
		t.Errorf("with a pull the span must render whole, got %q", withPull)
	}
	withoutPull := render(spans, map[string]string{"subject": "add the demo feature", "author": "akira-toriyama"})
	if withoutPull != "- add the demo feature @akira-toriyama" {
		t.Errorf("without a pull the span must take its parens AND its space with it, got %q", withoutPull)
	}
}

// TestParseLineLeavesABareBracketLiteral: only `$[` opens a span. A bare `[`
// is Markdown the author wrote — the `- [$scope] $subject` idiom and every
// link — and compiling it as a span would silently delete the text inside it
// for any commit whose scope is empty.
func TestParseLineLeavesABareBracketLiteral(t *testing.T) {
	spans, err := ParseLine("- [$scope] $subject [docs](https://example.invalid)")
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	for _, s := range spans {
		if s.Optional {
			t.Fatalf("a bare [ opened an optional span: %+v", spans)
		}
	}
	got := render(spans, map[string]string{"subject": "fix it"})
	if got != "- [] fix it [docs](https://example.invalid)" {
		t.Errorf("literal brackets must survive verbatim, got %q", got)
	}
}

// TestParseLineSpanBracketsNest: a span ends at the first ] that is not
// closing a bracket the span itself opened. Measured with the first ] of any
// kind closing it, this template rendered `- add the demo feature]` for a
// scope-less commit — a stray bracket in every line, from a span that closed
// one character early. Both arms again: the bracketed group renders when the
// scope resolves, and the whole span leaves when it does not.
func TestParseLineSpanBracketsNest(t *testing.T) {
	spans, err := ParseLine("- $subject$[ [$scope]]")
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if got, want := render(spans, map[string]string{"subject": "fix the scoped thing", "scope": "cli"}), "- fix the scoped thing [cli]"; got != want {
		t.Errorf("scoped: got %q, want %q", got, want)
	}
	if got, want := render(spans, map[string]string{"subject": "fix the demo crash"}), "- fix the demo crash"; got != want {
		t.Errorf("scope-less: got %q, want %q", got, want)
	}
}

// TestParseLineRefusals: the malformed spans, refused at parse time so they
// fail the config rather than the release that would have rendered them.
func TestParseLineRefusals(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"unterminated", "- $subject$[ ($pr) @$author", "unterminated optional span"},
		{"unpaired bracket inside a span", "- $subject$[ [$pr)] @$author", "unterminated optional span"},
		{"nested", "- $subject$[ ($pr$[ ,$hash]) ] @$author", "nested optional span"},
		{"no placeholder", "- $subject$[ (see the pull)] @$author", "holds no $placeholder"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseLine(c.line)
			if err == nil {
				t.Fatalf("ParseLine(%q) succeeded, want an error containing %q", c.line, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want substring %q", err, c.want)
			}
		})
	}
}

// TestLoadFileMalformedLineNamesTheFile: a malformed template is a CONFIG
// failure, and the message has to say which file and which key — the reader
// is a developer whose commit-lint job just went red, holding only stderr.
func TestLoadFileMalformedLineNamesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glyph.toml")
	body := "schema = 1\n" + minimalPatterns + "[note]\nline = '- $subject$[ ($pr) @$author'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatalf("LoadFile accepted an unterminated optional span")
	}
	want := path + ": note.line: "
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want it to start with %q", err, want)
	}
}

// TestLoadRefusesAPlaceholderNothingBinds is the mirror of the no-placeholder
// refusal, and the span is what makes it worth a load error rather than a
// shrug. A name nothing binds resolves empty for every commit, so inside a
// span it drops the span on every line and takes the punctuation with it: the
// release body is quietly missing a column instead of visibly wrong. Measured
// before this check existed — `$[ ($pull)]`, where the built-in is `$pr`,
// rendered every line with no citation and no parens, at exit 0.
//
// Both arms, or neither: a name a pattern DOES capture has to keep loading, or
// the refusal is just a ban on pattern groups.
func TestLoadRefusesAPlaceholderNothingBinds(t *testing.T) {
	// The first pattern captures semver_sigil and subject; scope belongs to the
	// second only, which is what proves the legal set is the UNION.
	const patterns = "[[patterns]]\npattern = '^(?P<semver_sigil>[=~^!]) (?P<subject>.+)'\n" +
		"[[patterns]]\npattern = '^\\((?P<scope>[a-z]+)\\) (?P<semver_sigil>[=~^!]) (?P<subject>.+)'\n"

	t.Run("a name no pattern captures", func(t *testing.T) {
		_, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ ($pull)]'\n"))
		if err == nil {
			t.Fatalf("Load accepted a placeholder no pattern can fill")
		}
		for _, want := range []string{"$pull", "author, coauthors, hash, pr, scope, semver_sigil, subject"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want substring %q", err, want)
			}
		}
	})

	t.Run("a group only the second pattern captures", func(t *testing.T) {
		if _, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ [$scope]]'\n")); err != nil {
			t.Errorf("Load rejected a group one pattern captures: %v", err)
		}
	})

	t.Run("the built-ins", func(t *testing.T) {
		if _, err := Load([]byte("schema = 1\n" + patterns + "[note]\nline = '- $subject$[ ($pr)] @$author $hash'\n")); err != nil {
			t.Errorf("Load rejected the built-ins: %v", err)
		}
	})
}

// TestNoteLineMayCiteTheFallbackSubject: the raw-line fallback binds $subject
// for every commit no pattern claims, whatever the patterns call their
// groups, so a file that names its subject group otherwise may cite it beside
// that group — the one spelling that renders a bot line's text. Measured
// before (2026-09-29): such a file citing $subject was refused as resolving
// empty for every commit, and its `- $title @$author` rendered each bot line
// as `-  dependabot\[bot]`, the text gone, at exit 0.
func TestNoteLineMayCiteTheFallbackSubject(t *testing.T) {
	src := "schema = 1\n[[patterns]]\npattern = '^(?P<title>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n[note]\nline = '- $title$subject'\n"
	if _, err := Load([]byte(src)); err != nil {
		t.Fatalf("Load refused $subject beside $title, which the fallback binds for every unmatched commit: %v", err)
	}
}

// TestNoteLineRefusesTheFallbackSubjectAlone: where no pattern whose groups a
// commit binds captures `subject`, the fallback binds $subject for a commit no
// pattern claims and for no other, so it completes a template and never
// carries one — a template whose only name beyond the built-ins and trailers
// is that $subject renders every line a pattern claims without its text.
// Measured (2026-10-04) with $subject legal unconditionally: the gemoji
// preset with its group renamed to `title` and note.line left alone loaded
// and printed `-  akira` for each matched commit at exit 0, where adfc5e1
// refused it at exit 2; with sections on the semver axis only, the fallback
// never renders, so $subject filled no line at all. The refusal names the
// groups the file can bind, title among them, and not the fallback's name.
func TestNoteLineRefusesTheFallbackSubjectAlone(t *testing.T) {
	preset, ok := Preset("gemoji")
	if !ok {
		t.Fatal("gemoji preset missing")
	}
	renamed := strings.ReplaceAll(string(preset), "?P<subject>", "?P<title>")
	if renamed == string(preset) {
		t.Fatal("the gemoji preset captures no subject group to rename — re-derive this fixture")
	}
	const title = "schema = 1\n[[patterns]]\npattern = '^(?P<title>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n"
	for name, src := range map[string]string{
		"the preset with its group renamed": renamed,
		"semver sections only":              title + "[note]\nline = '- $subject @$author'\n[[note.sections]]\nsemver = 'patch'\ntitle = 'Fixes'\n",
		"a trailer beside it":               title + "[note]\nline = '- $subject$[ — $why]'\n[[note.trailers]]\ntoken = 'Why'\nname = 'why'\n",
		"only a skip pattern captures it":   title + "[[patterns]]\npattern = '^Merge (?P<subject>.+)'\nskip = true\n[note]\nline = '- $subject'\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load([]byte(src))
			if err == nil {
				t.Fatal("Load accepted a template whose only group is the fallback's $subject: every line a pattern claims renders without its text")
			}
			for _, want := range []string{"note.line: $subject is not a built-in", "the names this file can bind are: ", "semver_sigil, title"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, want substring %q", err, want)
				}
			}
			if strings.Contains(err.Error(), ", subject") {
				t.Errorf("refusal = %q lists subject among the names this file binds", err)
			}
		})
	}
	if _, err := Load([]byte(strings.Replace(renamed, "line = '- $subject", "line = '- $title$subject", 1))); err != nil {
		t.Errorf("the renamed preset citing $title beside $subject did not load: %v", err)
	}
}

// TestNoteLineCannotCiteASkipGroup: skip is total — a skipped commit is in no
// section — so a group only a skip pattern captures binds for no rendered
// line, exactly as one only an unlandable pattern captures. Measured before
// (2026-09-29): `$[ (from $branch)] $branch` from a skip pattern loaded and
// rendered `- :bug:~ fix it ` at exit 0, span dropped and the bare one empty.
func TestNoteLineCannotCiteASkipGroup(t *testing.T) {
	_, err := Load([]byte("schema = 1\n[[patterns]]\npattern = '^(?P<subject>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n" +
		"[[patterns]]\npattern = '^Merge (?P<branch>.+)'\nskip = true\n[note]\nline = '- $subject$[ (from $branch)]'\n"))
	if err == nil || !strings.Contains(err.Error(), "$branch") {
		t.Fatalf("Load = %v, want the note.line refusal naming $branch", err)
	}
}
