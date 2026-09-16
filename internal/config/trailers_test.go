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
