package attribution

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v5/internal/config"
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
// nothing can carry is an authoring error the gate names, with the escapes
// the commit can take in the message, never a silent none and never "every
// line". A scope that names no package changes nothing — it is not a third
// state between a declared line and nobody's (DESIGN §4.1, t-n5tw R1):
// (spanner)^ under an undeclared spanner/ and (ci)^ on root CI take one path,
// so carrying the first nowhere would let the second through as the silent
// none this rule refuses.
func TestSharedOnlyBumpIsRefused(t *testing.T) {
	for _, sigil := range []config.Sigil{config.SigilPatch, config.SigilMinor, config.SigilMajor, config.SigilPromote} {
		got, err := Attribute([]string{"README.md"}, "", sigil, []config.Package{haiku, curry})
		var r *Refusal
		if !errors.As(err, &r) || r.Reason != NoCarrier {
			t.Fatalf("sigil %s on a shared-only commit → [%s], %v; want a NoCarrier refusal", sigil, paths(got), err)
		}
		for _, escape := range []string{"haiku", "curry", "write =", "declare the package these files belong to"} {
			if !strings.Contains(err.Error(), escape) {
				t.Errorf("the refusal must name the escape %q, got %q", escape, err)
			}
		}
	}
	storage := config.Package{Path: "storage", Name: "storage"}
	for _, c := range []struct{ file, scope string }{
		{"spanner/x.go", "spanner"},
		{".github/workflows/ci.yml", "ci"},
	} {
		got, err := Attribute([]string{c.file}, c.scope, config.SigilMinor, []config.Package{storage})
		var r *Refusal
		if !errors.As(err, &r) || r.Reason != NoCarrier {
			t.Fatalf("(%s)^ on %s → [%s], %v; want a NoCarrier refusal — a scope naming no package carries nothing and excuses nothing", c.scope, c.file, paths(got), err)
		}
		// The declaration is named, its path is not guessed: glyph cannot tell
		// a module from root CI, and a first path segment is `.github` here —
		// a path no tag can carry (the loader refuses a leading dot).
		if !strings.Contains(err.Error(), `[[packages]] path = "<its directory>"`) {
			t.Errorf("the refusal must name the declaration escape, got %q", err)
		}
		for _, guess := range []string{`path = "spanner"`, `path = ".github"`} {
			if strings.Contains(err.Error(), guess) {
				t.Errorf("the refusal guesses a package path (%s): %q", guess, err)
			}
		}
	}
}

// TestNoFilesHaveNoCarrierUnderARootPackage pins what the root package is: a
// claim on files — every file no other package claims — not on commits. A
// commit that shows the tree no file (an empty commit, a claimed merge commit
// whose diff is never read) is placed by rules 2–3 whatever is declared: its
// scope carries it, = leaves it on no line, and any other sigil is refused.
// Carried by the root, a ~ merge of a haiku-only branch would step the root
// line beside haiku's (DESIGN §4.1, t-n5tw 3).
func TestNoFilesHaveNoCarrierUnderARootPackage(t *testing.T) {
	pkgs := []config.Package{haiku, root}
	got, err := Attribute(nil, "", config.SigilPatch, pkgs)
	var r *Refusal
	if !errors.As(err, &r) || r.Reason != NoCarrier {
		t.Fatalf("an empty ~ under a root package → [%s], %v; want a NoCarrier refusal", paths(got), err)
	}
	if want := "this commit touches no file, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, core), or write = so it moves no line"; err.Error() != want {
		t.Errorf("refusal = %q\n   want   %q", err, want)
	}
	if got, err := Attribute(nil, "core", config.SigilPatch, pkgs); err != nil || paths(got) != "." {
		t.Errorf("(core)~ with no files → [%s], %v; want the root line alone", paths(got), err)
	}
	if got, err := Attribute(nil, "", config.SigilNone, pkgs); err != nil || got != nil {
		t.Errorf("an empty = → [%s], %v; want no line, no refusal", paths(got), err)
	}
}

// refusalOf is Attribute's refusal for a commit, with what only a caller
// knows set on it the way cli's attribute sets it.
func refusalOf(t *testing.T, files []string, scope string, sigil config.Sigil, pkgs []config.Package, tell func(*Refusal)) string {
	t.Helper()
	_, err := Attribute(files, scope, sigil, pkgs)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("Attribute(%v, %q, %s) = %v, want a refusal", files, scope, sigil, err)
	}
	if tell != nil {
		tell(r)
	}
	return r.Error()
}

// TestNoCarrierNamesOnlyTheEscapesTheCommitCanTake is the sentence's format
// spec, whole strings on purpose: glyph-monorepo-test's e2e and any consumer
// that greps a finding read these words. The opening says what the tree was
// shown — files no package owns, no file, a merge commit's unread diff, a diff
// the caller could not read whole — and the escapes are the ones this
// commit's message can write under the pattern that claimed it (scope, =,
// then the declaration, which needs files). The shipped raw-revert pattern
// fixes ~ and captures no scope: its refusal says so and names the reword
// that works, where it used to name two escapes the message cannot write
// (t-mfny (A); measured 2026-10-05 at 135eead: `Revert ":memo:(haiku)= …"`
// exits 3, `:rewind:= Revert "…"` exits 0).
func TestNoCarrierNamesOnlyTheEscapesTheCommitCanTake(t *testing.T) {
	// Where the loader would refuse the root's default name the declaration
	// says the root takes a name: under the presets `path = "."` alone does
	// not load. The caller says which (Sayable.RootNeedsName); no other field
	// decides it — a scope only a warn pattern captures leaves the message
	// nothing to write and the loader still asking for the name.
	const declare = `declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." and a name declare the root package, which holds every file no other package claims)`
	const declareUnnamed = `declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." declares the root package, which holds every file no other package claims)`
	two := []config.Package{haiku, curry}
	rooted := []config.Package{haiku, root}
	bareRoot := []config.Package{haiku, {Path: ".", Name: "."}}
	docs := []string{"docs/a.md", "./docs/b.md", "docs/c.md", "docs/d.md", "docs/e.md"}
	pattern := func(s config.Sayable) func(*Refusal) {
		return func(r *Refusal) { r.Pattern = &s }
	}
	for name, c := range map[string]struct {
		files []string
		sigil config.Sigil
		pkgs  []config.Package
		tell  func(*Refusal)
		want  string
	}{
		"files no package owns, a pattern nobody described": {
			[]string{"README.md"}, config.SigilMinor, two, nil,
			"its files (README.md) belong to no declared package, and its sigil ^ claims a version impact nothing can carry: name the line it moves in the scope (one of haiku, curry), write = so it moves no line, or " + declare,
		},
		"more files than the sentence quotes": {
			docs, config.SigilMinor, two, nil,
			"its files (docs/a.md, docs/b.md, docs/c.md and 2 more) belong to no declared package, and its sigil ^ claims a version impact nothing can carry: name the line it moves in the scope (one of haiku, curry), write = so it moves no line, or " + declare,
		},
		"a merge commit": {
			nil, config.SigilPatch, rooted, func(r *Refusal) { r.Merge = true },
			"this merge commit's own diff is never read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, core), or write = so it moves no line",
		},
		"a diff the caller could not read": {
			nil, config.SigilPatch, two, func(r *Refusal) { r.Unread = true },
			"no file of this commit was read, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry), or write = so it moves no line",
		},
		"a diff the caller read in part": {
			docs, config.SigilMinor, two, func(r *Refusal) { r.Unread = true },
			"the files read of it (docs/a.md, docs/b.md, docs/c.md and 2 more) belong to no declared package, and its sigil ^ claims a version impact nothing can carry: name the line it moves in the scope (one of haiku, curry), write = so it moves no line, or " + declare,
		},
		"the shipped revert pattern": {
			[]string{"README.md"}, config.SigilPatch, two,
			pattern(config.Sayable{Pattern: 1, ElsewhereScopes: []string{"haiku", "curry"}, ElsewhereNone: true, RootNeedsName: true}),
			"its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: patterns[1], which claimed this message, fixes the sigil at ~ and captures no scope — reword it so another pattern claims it, with a scope naming the line it moves (one of haiku, curry) or as = so it moves no line, or " + declare,
		},
		"a scope only a warned pattern captures, files to declare": {
			[]string{"README.md"}, config.SigilPatch, two,
			pattern(config.Sayable{SigilGroup: true, None: true, RootNeedsName: true}),
			"its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: write = so it moves no line, or " + declare,
		},
		"a scope group that spells the root's default name": {
			[]string{"README.md"}, config.SigilPatch, two,
			pattern(config.Sayable{ScopeGroup: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true}),
			"its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: name the line it moves in the scope (one of haiku, curry), write = so it moves no line, or " + declareUnnamed,
		},
		"a fixed = behind a sigil the message can leave out": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{SigilGroup: true, SigilDroppable: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: leave the sigil out so it moves no line",
		},
		"a sigil to leave out, beside a scope and files": {
			[]string{"README.md"}, config.SigilPatch, two,
			pattern(config.Sayable{ScopeGroup: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, SigilDroppable: true}),
			"its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: name the line it moves in the scope (one of haiku, curry), leave the sigil out so it moves no line, or " + declareUnnamed,
		},
		"a = the message can write or leave unwritten": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{SigilGroup: true, None: true, SigilDroppable: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: write = so it moves no line",
		},
		"a sigil group that reads no = and cannot be left out": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{SigilGroup: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: patterns[0], which claimed this message, captures no = as the sigil and captures no scope",
		},
		"a grammar with no scope, a root at its default name": {
			nil, config.SigilPatch, bareRoot,
			pattern(config.Sayable{SigilGroup: true, None: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: write = so it moves no line",
		},
		"a scope the pattern captures, a sigil it fixes": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{ScopeGroup: true, Scopes: []string{"haiku", "curry"}}),
			"this commit touches no file, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of haiku, curry)",
		},
		"a scope group spelling one name of two": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{ScopeGroup: true, Scopes: []string{"curry"}, SigilGroup: true, None: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: name the line it moves in the scope (one of curry), or write = so it moves no line",
		},
		"groups that spell neither escape, a scope elsewhere": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{Pattern: 2, ScopeGroup: true, SigilGroup: true, ElsewhereScopes: []string{"haiku"}}),
			"this commit touches no file, so no package's tree can carry its sigil ~: patterns[2], which claimed this message, captures no = as the sigil and captures no scope naming a line — reword it so another pattern claims it, with a scope naming the line it moves (one of haiku)",
		},
		"only = elsewhere": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{ElsewhereNone: true}),
			"this commit touches no file, so no package's tree can carry its sigil ~: patterns[0], which claimed this message, fixes the sigil at ~ and captures no scope — reword it so another pattern claims it as =, so it moves no line",
		},
		"no escape in any pattern, files to declare": {
			[]string{"README.md"}, config.SigilPatch, two,
			pattern(config.Sayable{}),
			"its files (README.md) belong to no declared package, and its sigil ~ claims a version impact nothing can carry: patterns[0], which claimed this message, fixes the sigil at ~ and captures no scope — " + declareUnnamed,
		},
		"no escape in any pattern, no file": {
			nil, config.SigilPatch, two,
			pattern(config.Sayable{}),
			"this commit touches no file, so no package's tree can carry its sigil ~: patterns[0], which claimed this message, fixes the sigil at ~ and captures no scope",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := refusalOf(t, c.files, "", c.sigil, c.pkgs, c.tell); got != c.want {
				t.Errorf("refusal =\n  %q\nwant\n  %q", got, c.want)
			}
		})
	}
}

// TestScopeContradictingTheTreeIsRefused pins the contradiction check: with a
// version sigil, a scope naming a package that owns none of the commit's files
// is refused, and the message names a file with the package that owns it. Owns
// is rule 1's word, the longest prefix: a parent's name over its nested
// package's file, and the root package's name over a declared package's, are
// contradictions too — containment would accept a message naming a line the
// commit leaves where it is, and make the root's name the one scope nothing
// can contradict (DESIGN §4.1, t-mfny (C)). A scope that names no package —
// (ci), (deps) — is never checked.
func TestScopeContradictingTheTreeIsRefused(t *testing.T) {
	travel := config.Package{Path: "travel", Name: "travel"}
	onsen := config.Package{Path: "travel/onsen", Name: "onsen"}
	for name, c := range map[string]struct {
		files []string
		scope string
		pkgs  []config.Package
		owned string
	}{
		"another package's file":                    {[]string{"curry/b.go"}, "haiku", []config.Package{haiku, curry}, "curry/b.go belongs to curry (curry)"},
		"a parent's name over its nested package":   {[]string{"travel/onsen/o.md"}, "travel", []config.Package{travel, onsen}, "travel/onsen/o.md belongs to onsen (travel/onsen)"},
		"the root's name over a declared package":   {[]string{"haiku/a.go"}, "core", []config.Package{haiku, root}, "haiku/a.go belongs to haiku (haiku)"},
		"the deciding file listed after loose ones": {[]string{".github/workflows/a.yml", "Makefile", "curry/b.go"}, "haiku", []config.Package{haiku, curry}, "curry/b.go belongs to curry (curry)"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Attribute(c.files, c.scope, config.SigilPatch, c.pkgs)
			var r *Refusal
			if !errors.As(err, &r) || r.Reason != Contradiction {
				t.Fatalf("(%s)~ over %v → [%s], %v; want a Contradiction refusal", c.scope, c.files, paths(got), err)
			}
			if !strings.Contains(err.Error(), c.owned) {
				t.Errorf("the refusal must name a file with its owner (%q), got %q", c.owned, err)
			}
			for _, old := range []string{"does not touch", "lie under ."} {
				if strings.Contains(err.Error(), old) {
					t.Errorf("the refusal still says %q: %q", old, err)
				}
			}
		})
	}
	// Naming one of two touched packages is not a contradiction.
	got, err := Attribute([]string{"curry/b.go", "haiku/a.go"}, "haiku", config.SigilMinor, []config.Package{haiku, curry})
	if err != nil || paths(got) != "haiku curry" {
		t.Errorf("(haiku) over both → [%s], %v; want both lines, no refusal", paths(got), err)
	}
	// A free-form scope passes through.
	got, err = Attribute([]string{"curry/b.go"}, "ci", config.SigilMinor, []config.Package{haiku, curry})
	if err != nil || paths(got) != "curry" {
		t.Errorf("(ci) over curry/ → [%s], %v; want curry, no refusal", paths(got), err)
	}
}

// TestContradictingNoneIsPlacedByItsFiles pins the check's one exemption: a =
// claims no version impact, so there is nothing for the tree to contradict,
// and a = whose scope names another package is placed by its files like any
// other = (DESIGN §4.1, t-n5tw R2). Refused, `:wrench:(haiku)= update
// CODEOWNERS` on a root file — carried by haiku under rule 2 while no root
// package is declared — wedged the walk the moment `.` was, over a commit
// that moves no version on any line either way (measured 2026-10-05 at
// 135eead: lint and bump exit 3).
func TestContradictingNoneIsPlacedByItsFiles(t *testing.T) {
	for name, c := range map[string]struct {
		files []string
		pkgs  []config.Package
		want  string
	}{
		"another package's file": {[]string{"curry/b.go"}, []config.Package{haiku, curry}, "curry"},
		"a root-owned file":      {[]string{".github/CODEOWNERS"}, []config.Package{haiku, root}, "."},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Attribute(c.files, "haiku", config.SigilNone, c.pkgs)
			if err != nil || paths(got) != c.want {
				t.Errorf("(haiku)= over %v → [%s], %v; want [%s] and no refusal", c.files, paths(got), err, c.want)
			}
		})
	}
}

// TestContradictionNamesTheOwnersAndTheScopesThatWork is the contradiction
// sentence's format spec, whole strings on purpose (consumers grep them). The
// evidence is one owned file per package the commit moves, in config order,
// with the rest counted — a file no package owns is counted and never said
// to belong to anything, and the root package is called that, with its name
// where a scope can write it. The remedy names only what the claiming pattern
// lets the message write: the scopes of the lines it moves that the pattern's
// scope group spells, and dropping the scope only where the caller proved it
// of this message (Sayable.ScopeDroppable). A scope neither proven droppable
// nor required by the pattern's tree is called neither — the state a scope in
// one branch of an alternation is in.
func TestContradictionNamesTheOwnersAndTheScopesThatWork(t *testing.T) {
	const rule = " — the longest declared path owns a file: "
	sub := []config.Package{haiku, curry, nested}
	pattern := func(s config.Sayable) func(*Refusal) {
		return func(r *Refusal) { r.Pattern = &s }
	}
	for name, c := range map[string]struct {
		files []string
		pkgs  []config.Package
		tell  func(*Refusal)
		want  string
	}{
		"one owner, a pattern nobody described": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry}, nil,
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry)" + rule + "write the scope of a line it moves (curry), or drop the scope",
		},
		"two owners in config order, the rest counted": {
			[]string{"haiku/sub/z.go", "Makefile", "curry/b.go", "curry/a.go", "haiku/sub/x.go", ".github/ci.yml", "curry/c.go"}, sub, nil,
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry), haiku/sub/z.go belongs to sub (haiku/sub), 3 more files likewise, and 2 files under no package" + rule + "write the scope of a line it moves (curry, sub), or drop the scope",
		},
		"one more file, one loose file": {
			[]string{"curry/b.go", "curry/a.go", "go.work"}, []config.Package{haiku, curry}, nil,
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry), 1 more file likewise, and 1 file under no package" + rule + "write the scope of a line it moves (curry), or drop the scope",
		},
		"the root package owns the file": {
			[]string{".github/CODEOWNERS"}, []config.Package{haiku, root},
			pattern(config.Sayable{ScopeGroup: true, ScopeDroppable: true, Scopes: []string{"haiku", "core"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: .github/CODEOWNERS belongs to core (the root package)" + rule + "write the scope of a line it moves (core), or drop the scope",
		},
		"a root name the claiming pattern cannot write": {
			[]string{".github/CODEOWNERS"}, []config.Package{haiku, root},
			pattern(config.Sayable{ScopeGroup: true, ScopeDroppable: true, Scopes: []string{"haiku"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: .github/CODEOWNERS belongs to the root package" + rule + "drop the scope",
		},
		"a scope the pattern requires": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry},
			pattern(config.Sayable{ScopeGroup: true, ScopeRequired: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry)" + rule + "write the scope of a line it moves (curry)",
		},
		"a scope not required, and not proven droppable": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry},
			pattern(config.Sayable{ScopeGroup: true, Scopes: []string{"haiku", "curry"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry)" + rule + "write the scope of a line it moves (curry)",
		},
		"one of two moved lines spellable": {
			[]string{"curry/b.go", "haiku/sub/x.go"}, sub,
			pattern(config.Sayable{ScopeGroup: true, ScopeDroppable: true, Scopes: []string{"haiku", "sub"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry), and haiku/sub/x.go belongs to sub (haiku/sub)" + rule + "write the scope of a line it moves (sub), or drop the scope",
		},
		"a required scope that spells no line it moves": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry},
			pattern(config.Sayable{Pattern: 3, ScopeGroup: true, ScopeRequired: true, Scopes: []string{"haiku"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry)" + rule + "patterns[3], which claimed this message, requires a scope and spells none of the lines it moves — the scope must name one of them (curry) or no package",
		},
		"neither required nor proven droppable, spelling no line it moves": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry},
			pattern(config.Sayable{Pattern: 3, ScopeGroup: true, Scopes: []string{"haiku"}, SigilGroup: true, None: true}),
			"scope (haiku) names a line this commit does not move: curry/b.go belongs to curry (curry)" + rule + "patterns[3], which claimed this message, spells none of the lines it moves — the scope must name one of them (curry) or no package",
		},
		"a diff the caller read in part": {
			[]string{"curry/b.go"}, []config.Package{haiku, curry}, func(r *Refusal) { r.Unread = true },
			"scope (haiku) names a line the files read of this commit do not move: curry/b.go belongs to curry (curry)" + rule + "write the scope of a line it moves (curry), or drop the scope",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := refusalOf(t, c.files, "haiku", config.SigilPatch, c.pkgs, c.tell); got != c.want {
				t.Errorf("refusal =\n  %q\nwant\n  %q", got, c.want)
			}
		})
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
