package attribution

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/config"
)

var (
	haiku = config.Package{Path: "haiku", Name: "haiku"}
	curry = config.Package{Path: "curry", Name: "curry"}
	// nested sits INSIDE haiku: its files must leave haiku's line.
	nested = config.Package{Path: "haiku/sub", Name: "sub"}
	root   = config.Package{Path: ".", Name: "core"}
)

func paths(pkgs []config.Package) string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Path)
	}
	return strings.Join(out, " ")
}

// TestFilesDecide pins rule 1 and the longest-prefix ownership: a commit
// moves every line its files lie under, a nested package takes its files out
// of its parent, the root package holds only the remainder, and the answer
// comes back in config order whatever order the files arrived in.
func TestFilesDecide(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		pkgs  []config.Package
		want  string
	}{
		{"one package", []string{"haiku/a.go"}, []config.Package{haiku, curry}, "haiku"},
		{"two packages, config order", []string{"curry/b.go", "haiku/a.go"}, []config.Package{haiku, curry}, "haiku curry"},
		{"file named exactly as the package path", []string{"haiku"}, []config.Package{haiku}, "haiku"},
		{"sibling directory sharing the prefix is not the package", []string{"haikus/a.go"}, []config.Package{haiku, root}, "."},
		{"nested package takes its file out of the parent", []string{"haiku/sub/x.go"}, []config.Package{haiku, nested}, "haiku/sub"},
		{"nested and parent both touched", []string{"haiku/sub/x.go", "haiku/y.go"}, []config.Package{haiku, nested}, "haiku haiku/sub"},
		{"nested wins whatever its config position", []string{"haiku/sub/x.go"}, []config.Package{nested, haiku}, "haiku/sub"},
		{"root holds what no other package claims", []string{"README.md"}, []config.Package{haiku, root}, "."},
		{"root does not steal a package's file", []string{"haiku/a.go"}, []config.Package{root, haiku}, "haiku"},
		{"a leading ./ cannot defeat the prefix", []string{"./haiku/a.go"}, []config.Package{haiku}, "haiku"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Attribute(c.files, "", config.SigilMinor, c.pkgs)
			if err != nil {
				t.Fatalf("Attribute: %v", err)
			}
			if paths(got) != c.want {
				t.Errorf("Attribute = [%s], want [%s]", paths(got), c.want)
			}
		})
	}
}

// TestScopeCarriesWhatPathCannot pins rule 2: a shared-only commit whose scope
// names a package lands on that package — and only that one.
func TestScopeCarriesWhatPathCannot(t *testing.T) {
	got, err := Attribute([]string{".github/workflows/ci.yml", "go.work"}, "curry", config.SigilPatch, []config.Package{haiku, curry})
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if paths(got) != "curry" {
		t.Errorf("Attribute = [%s], want [curry]", paths(got))
	}
}

// TestSharedOnlyNoneIsCarriedNowhere pins the `=` arm of rule 3: shared
// housekeeping with no scope participates in no line and is NOT a refusal.
func TestSharedOnlyNoneIsCarriedNowhere(t *testing.T) {
	got, err := Attribute([]string{"README.md"}, "", config.SigilNone, []config.Package{haiku, curry})
	if err != nil {
		t.Fatalf("a shared-only = must not be refused: %v", err)
	}
	if got != nil {
		t.Errorf("Attribute = [%s], want nothing — there is no line for it to appear on", paths(got))
	}
	// A free-form scope that names no package changes nothing.
	got, err = Attribute([]string{"README.md"}, "docs", config.SigilNone, []config.Package{haiku, curry})
	if err != nil || got != nil {
		t.Errorf("(docs) on a shared-only = → [%s], %v; want nothing, nil", paths(got), err)
	}
}

// TestSharedOnlyBumpIsRefused pins the other arm of rule 3: a version claim
// nothing can carry is an authoring error the gate names, with both escapes
// in the message, never a silent none and never "every line".
func TestSharedOnlyBumpIsRefused(t *testing.T) {
	for _, sigil := range []config.Sigil{config.SigilPatch, config.SigilMinor, config.SigilMajor, config.SigilPromote} {
		got, err := Attribute([]string{"README.md"}, "", sigil, []config.Package{haiku, curry})
		var r *Refusal
		if !errors.As(err, &r) || r.Reason != NoCarrier {
			t.Fatalf("sigil %s on a shared-only commit → [%s], %v; want a NoCarrier refusal", sigil, paths(got), err)
		}
		for _, escape := range []string{"haiku", "curry", "write ="} {
			if !strings.Contains(err.Error(), escape) {
				t.Errorf("the refusal must name the escape %q, got %q", escape, err)
			}
		}
	}
}

// TestScopeContradictingTheTreeIsRefused pins the contradiction check: a
// package-named scope on a commit whose files lie under another package is
// refused, and the message says where the files actually are. A scope that
// names no package — (ci), (deps) — is never checked.
func TestScopeContradictingTheTreeIsRefused(t *testing.T) {
	got, err := Attribute([]string{"curry/b.go"}, "haiku", config.SigilNone, []config.Package{haiku, curry})
	var r *Refusal
	if !errors.As(err, &r) || r.Reason != Contradiction {
		t.Fatalf("(haiku) over curry/ → [%s], %v; want a Contradiction refusal", paths(got), err)
	}
	if !strings.Contains(err.Error(), "curry") {
		t.Errorf("the refusal must say where the files lie, got %q", err)
	}
	// Naming one of two touched packages is not a contradiction.
	got, err = Attribute([]string{"curry/b.go", "haiku/a.go"}, "haiku", config.SigilMinor, []config.Package{haiku, curry})
	if err != nil || paths(got) != "haiku curry" {
		t.Errorf("(haiku) over both → [%s], %v; want both lines, no refusal", paths(got), err)
	}
	// A free-form scope passes through.
	got, err = Attribute([]string{"curry/b.go"}, "ci", config.SigilMinor, []config.Package{haiku, curry})
	if err != nil || paths(got) != "curry" {
		t.Errorf("(ci) over curry/ → [%s], %v; want curry, no refusal", paths(got), err)
	}
}

// TestNoPackagesIsNotAsked pins the boundary with the single line: with
// nothing declared there is nothing to attribute, and no sigil is refused.
func TestNoPackagesIsNotAsked(t *testing.T) {
	got, err := Attribute([]string{"a.go"}, "", config.SigilMajor, nil)
	if err != nil || got != nil {
		t.Errorf("Attribute with no packages = [%s], %v; want nothing, nil", paths(got), err)
	}
}
