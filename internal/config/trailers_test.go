package config

import (
	"strings"
	"testing"
)

// The load-time refusals. Each one exists because the alternative is a
// placeholder whose meaning depends on which commit rendered it — the failure
// mode note.line validation exists to prevent, arriving through a new door.
func TestLoadRefusesABadTrailerDeclaration(t *testing.T) {
	tests := []struct {
		name  string
		block string
		want  string
	}{
		{
			name:  "no token",
			block: "[[note.trailers]]\nname = \"why\"\n",
			want:  "token is required",
		},
		{
			name:  "a token holding a space could never bind",
			block: "[[note.trailers]]\ntoken = \"Why not\"\nname = \"why\"\n",
			want:  "voids its whole trailer block",
		},
		{
			name:  "a token holding a colon could never bind",
			block: "[[note.trailers]]\ntoken = \"Why:\"\nname = \"why\"\n",
			want:  "voids its whole trailer block",
		},
		{
			name:  "no name",
			block: "[[note.trailers]]\ntoken = \"Why\"\n",
			want:  "name is required",
		},
		{
			name:  "a name outside the placeholder alphabet",
			block: "[[note.trailers]]\ntoken = \"Why\"\nname = \"Why\"\n",
			want:  "outside note.line's placeholder alphabet",
		},
		{
			name:  "the same name twice",
			block: "[[note.trailers]]\ntoken = \"Why\"\nname = \"why\"\n\n[[note.trailers]]\ntoken = \"Ref\"\nname = \"why\"\n",
			want:  "declared twice",
		},
		{
			name:  "a name that is a built-in",
			block: "[[note.trailers]]\ntoken = \"Why\"\nname = \"author\"\n",
			want:  "is a built-in, which outranks it",
		},
		{
			name:  "a name a pattern already captures",
			block: "[[note.trailers]]\ntoken = \"Why\"\nname = \"subject\"\n",
			want:  "already captured by a pattern group",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(minimalConfig + tc.block))
			if err == nil {
				t.Fatalf("Load() accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// The positive control the refusals above need: the same shape, declared
// correctly, loads and binds. Without it every refusal above could be passing
// because the block never parsed at all.
func TestLoadAcceptsADeclaredTrailer(t *testing.T) {
	cfg, err := Load([]byte(minimalConfig + "[[note.trailers]]\ntoken = \"Why\"\nname = \"why\"\n"))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(cfg.Note.Trailers) != 1 {
		t.Fatalf("Trailers = %+v, want one entry", cfg.Note.Trailers)
	}
	if cfg.Note.Trailers[0].Token != "Why" || cfg.Note.Trailers[0].Name != "why" {
		t.Errorf("Trailers[0] = %+v, want {Why why}", cfg.Note.Trailers[0])
	}
}

// titleGrammar captures no subject group: the file whose bot lines only the
// fallback's $subject can carry.
const titleGrammar = "schema = 1\n[[patterns]]\npattern = '^(?P<title>:[a-z0-9_]+:(?P<semver_sigil>[=~^!%]) .+)'\n"

// TestATrailerCannotTakeTheFallbackSubject: the raw-line fallback binds
// $subject for every commit no pattern claims, whatever the patterns call
// their groups, and a trailer outranks a group at render — so a trailer named
// subject is a second meaning for the name even where no pattern captures
// it. Measured (2026-10-04, the same before t-f2cb): this file loaded and
// rendered each bot line as `- `, its text replaced by an absent trailer, at
// exit 0.
func TestATrailerCannotTakeTheFallbackSubject(t *testing.T) {
	_, err := Load([]byte(titleGrammar + "[note]\nline = '- $title$subject'\n[[note.trailers]]\ntoken = 'Subject'\nname = 'subject'\n"))
	if err == nil || !strings.Contains(err.Error(), `name "subject" is the one the raw-line fallback binds`) {
		t.Fatalf("Load = %v, want the refusal of a trailer named after the fallback's $subject", err)
	}
}

// TestATrailerMayTakeASkipGroupName: a trailer's name is refused only where
// it would mean two things on a rendered line, so it reads the groups a
// commit binds — the set note.line's names are checked against — and a group
// only a skip pattern captures binds nothing. Measured (2026-10-04, the same
// before t-f2cb): this file was refused as "$branch would mean two things".
func TestATrailerMayTakeASkipGroupName(t *testing.T) {
	cfg, err := Load([]byte(titleGrammar + "[[patterns]]\npattern = '^Merge (?P<branch>.+)'\nskip = true\n[note]\nline = '- $title$[ ($branch)]'\n[[note.trailers]]\ntoken = 'Branch'\nname = 'branch'\n"))
	if err != nil {
		t.Fatalf("Load refused a trailer named after a group only a skip pattern captures, which binds nothing: %v", err)
	}
	if len(cfg.Note.Trailers) != 1 || cfg.Note.Trailers[0].Name != "branch" {
		t.Errorf("Trailers = %+v, want the one branch entry", cfg.Note.Trailers)
	}
}

// A declared name joins the legal set note.line validates against, which is
// the whole point of declaring it — and an UNdeclared one still fails, so the
// union grew by exactly what was declared.
func TestADeclaredTrailerNameBecomesBindable(t *testing.T) {
	withLine := func(line, trailers string) error {
		body := strings.Replace(minimalConfig, `line = "- $subject"`, `line = "`+line+`"`, 1)
		_, err := Load([]byte(body + trailers))
		return err
	}

	if err := withLine("- $subject$[ — why: $why]", "[[note.trailers]]\ntoken = \"Why\"\nname = \"why\"\n"); err != nil {
		t.Errorf("a declared trailer name did not bind: %v", err)
	}
	err := withLine("- $subject$[ — why: $why]", "")
	if err == nil {
		t.Fatal("an undeclared name bound anyway")
	}
	if !strings.Contains(err.Error(), "$why is not a built-in") {
		t.Errorf("error = %q, want the undeclared-name refusal", err)
	}
}

// minimalConfig is the smallest file that loads, for the cases above to
// append a [[note.trailers]] block to.
const minimalConfig = `schema = 1

[[patterns]]
pattern = '^(?P<subject>:[a-z0-9_]+:(\((?P<scope>[a-z0-9-]+)\))?(?P<semver_sigil>[=~^!%]) .+)'

[note]
line = "- $subject"

[[note.sections]]
semver = "minor"
title = "Features"

`
