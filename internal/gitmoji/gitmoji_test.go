package gitmoji

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites the golden docs table (and lets us regenerate it after an
// intentional change): `go test ./internal/gitmoji -run Golden -update`.
var update = flag.Bool("update", false, "rewrite golden files")

// wantSections is the ratified notes section order. Notes render sections in
// this order and skip the empty ones; pinning it here catches a reordering or a
// rename in rules.json.
var wantSections = []string{
	"Breaking Changes", "Features", "Fixes", "Performance", "Security",
	"Reverts", "UI & UX", "Data", "Dependencies", "Deprecations",
	"Feature Flags", "Other",
}

func mustLoad(t *testing.T) *Table {
	t.Helper()
	tbl, err := Load()
	if err != nil {
		t.Fatalf("Load() of the embedded rules.json failed: %v", err)
	}
	return tbl
}

// TestLoadSucceeds is the anti-rot guard: the embedded table parses and passes
// every structural invariant Load enforces.
func TestLoadSucceeds(t *testing.T) {
	mustLoad(t)
}

// TestCodeCount pins the curated set size. Growing the spec is a deliberate
// ratification: bump CodeCount and rules.json together, never silently.
func TestCodeCount(t *testing.T) {
	tbl := mustLoad(t)
	if got := len(tbl.Codes); got != CodeCount {
		t.Fatalf("rules.json has %d codes, want CodeCount=%d", got, CodeCount)
	}
	if CodeCount != 75 {
		t.Fatalf("CodeCount=%d, want 75 (the ratified gitmoji set)", CodeCount)
	}
}

// TestBumpDistribution locks the ratified buckets: exactly one auto-major
// (:boom:), one auto-minor (:sparkles:), 36 patch, 37 none. A single mis-bucketed
// row shifts these counts.
func TestBumpDistribution(t *testing.T) {
	tbl := mustLoad(t)
	counts := map[Bump]int{}
	for _, r := range tbl.Codes {
		counts[r.Bump]++
	}
	want := map[Bump]int{BumpMajor: 1, BumpMinor: 1, BumpPatch: 36, BumpNone: 37}
	for b, w := range want {
		if counts[b] != w {
			t.Errorf("bump %q: got %d rows, want %d", b, counts[b], w)
		}
	}
}

// TestLoadBearingAndRatifiedDeviations checks the specific codes whose bump is a
// deliberate house decision — the two version movers and the ratified divergences
// from the gitmoji spec's own semver field.
func TestLoadBearingAndRatifiedDeviations(t *testing.T) {
	tbl := mustLoad(t)
	want := map[string]Bump{
		":boom:":         BumpMajor, // only auto-major
		":sparkles:":     BumpMinor, // only auto-minor
		":construction:": BumpNone,  // WIP — never moves a version (and blocked at merge)
		// Ratified divergences from the spec's semver field:
		":wrench:":      BumpNone,  // fleet config is non-shipping (spec: patch)
		":alembic:":     BumpNone,  // experiments are exploratory (spec: patch)
		":thread:":      BumpPatch, // shipped runtime behavior (spec: null)
		":safety_vest:": BumpPatch, // shipped runtime behavior (spec: null)
		":airplane:":    BumpPatch, // shipped runtime behavior (spec: null)
		":t-rex:":       BumpPatch, // shipped runtime behavior (spec: null)
	}
	for code, wb := range want {
		r, ok := tbl.Lookup(code)
		if !ok {
			t.Errorf("Lookup(%q): not found", code)
			continue
		}
		if r.Bump != wb {
			t.Errorf("Lookup(%q).Bump = %q, want %q", code, r.Bump, wb)
		}
	}
}

// TestRuleInvariants: every code is a well-formed, unique textual gitmoji with a
// valid bump; a none row carries no section, and a version-moving row carries a
// section drawn from the ratified list.
func TestRuleInvariants(t *testing.T) {
	tbl := mustLoad(t)
	sectionSet := map[string]bool{}
	for _, s := range wantSections {
		sectionSet[s] = true
	}
	seen := map[string]bool{}
	for _, r := range tbl.Codes {
		if !codeShapeRE.MatchString(r.Code) {
			t.Errorf("code %q is not a well-formed textual gitmoji", r.Code)
		}
		if seen[r.Code] {
			t.Errorf("duplicate code %q", r.Code)
		}
		seen[r.Code] = true
		if !r.Bump.Valid() {
			t.Errorf("code %q has invalid bump %q", r.Code, r.Bump)
		}
		if r.Emoji == "" {
			t.Errorf("code %q has no emoji glyph", r.Code)
		}
		if r.Meaning == "" {
			t.Errorf("code %q has no meaning", r.Code)
		}
		switch r.Bump {
		case BumpNone:
			if r.Section != "" {
				t.Errorf("none code %q must carry no section, got %q", r.Code, r.Section)
			}
		default:
			if r.Section == "" {
				t.Errorf("version-moving code %q (%s) must carry a section", r.Code, r.Bump)
			} else if !sectionSet[r.Section] {
				t.Errorf("code %q references unknown section %q", r.Code, r.Section)
			}
		}
	}
}

// TestSectionsOrder pins the ratified notes section order.
func TestSectionsOrder(t *testing.T) {
	tbl := mustLoad(t)
	got := tbl.Sections
	if len(got) != len(wantSections) {
		t.Fatalf("sections = %v, want %v", got, wantSections)
	}
	for i := range wantSections {
		if got[i] != wantSections[i] {
			t.Fatalf("sections[%d] = %q, want %q (full: %v)", i, got[i], wantSections[i], got)
		}
	}
}

// TestVersionPresent: the pinned gitmoji spec version label must be set so a
// binary self-reports which spec snapshot it embeds.
func TestVersionPresent(t *testing.T) {
	tbl := mustLoad(t)
	if tbl.Version == "" {
		t.Fatalf("Table.Version is empty; rules.json must pin the gitmoji spec version")
	}
}

// TestBumpRankLattice locks the none<patch<minor<major ordering the max-fold
// relies on, and that an unknown bump string is rejected.
func TestBumpRankLattice(t *testing.T) {
	if BumpNone.Rank() >= BumpPatch.Rank() ||
		BumpPatch.Rank() >= BumpMinor.Rank() ||
		BumpMinor.Rank() >= BumpMajor.Rank() {
		t.Fatalf("bump ranks not strictly ordered: none=%d patch=%d minor=%d major=%d",
			BumpNone.Rank(), BumpPatch.Rank(), BumpMinor.Rank(), BumpMajor.Rank())
	}
	if Bump("nonsense").Valid() {
		t.Fatalf("an unknown bump string must be invalid")
	}
}

// TestRulesJSONIsCanonical proves the embedded rules.json is stored as exactly
// the canonical JSON CanonicalJSON emits, plus one trailing newline — so
// `glyph rules --json` (CanonicalJSON + the command's Fprintln newline)
// reproduces the file byte-for-byte and on-disk diffs stay clean. The trailing
// newline is asserted, not trimmed away, so a dropped or doubled final newline
// is caught.
func TestRulesJSONIsCanonical(t *testing.T) {
	tbl := mustLoad(t)
	got, err := tbl.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	want := append(bytes.Clone(got), '\n')
	if !bytes.Equal(rawRules, want) {
		t.Fatalf("rules.json is not canonical bytes + exactly one trailing newline.\n"+
			"Regenerate it from CanonicalJSON.\nfirst diff near:\n%s",
			firstDiff(want, rawRules))
	}
}

// firstDiff returns a short window around the first byte where a and b differ,
// to make a canonical-form mismatch legible without dumping the whole table.
func firstDiff(a, b []byte) string {
	n := min(len(b), len(a))
	for i := range n {
		if a[i] != b[i] {
			lo := max(i-40, 0)
			return "file: ..." + string(a[lo:i]) + "|" + string(a[i:min(i+40, len(a))]) +
				"\ncanon: ..." + string(b[lo:i]) + "|" + string(b[i:min(i+40, len(b))])
		}
	}
	return "(one is a prefix of the other; lengths differ)"
}

// TestMarkdownGolden guards the human/docs rendering: Markdown() must equal the
// committed docs/gitmoji-table.md. Regenerate intentional changes with -update.
func TestMarkdownGolden(t *testing.T) {
	tbl := mustLoad(t)
	golden := filepath.Join("..", "..", "docs", "gitmoji-table.md")
	got := tbl.Markdown()
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden %s (run with -update to create it): %v", golden, err)
	}
	if got != string(want) {
		t.Fatalf("Markdown() drifted from %s; run `go test ./internal/gitmoji -run Golden -update`", golden)
	}
}

// TestLoadRejectsInvalid feeds parseTable malformed inputs to prove Load's
// validation is load-bearing, not decorative.
func TestLoadRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"invalid json", `{"version":`},
		{"empty version", `{"version":"","sections":["Features"],"codes":[]}`},
		{"no sections", `{"version":"x","sections":[],"codes":[]}`},
		{"empty section name", `{"version":"x","sections":[""],"codes":[]}`},
		{"empty emoji", `{"version":"x","sections":["Fixes"],"codes":[{"code":":bug:","emoji":"","meaning":"m","bump":"patch","section":"Fixes"}]}`},
		{"empty meaning", `{"version":"x","sections":["Fixes"],"codes":[{"code":":bug:","emoji":"🐛","meaning":"","bump":"patch","section":"Fixes"}]}`},
		{"bad bump", `{"version":"x","sections":["Features"],"codes":[{"code":":sparkles:","emoji":"✨","meaning":"m","bump":"huge","section":"Features"}]}`},
		{"malformed code", `{"version":"x","sections":["Features"],"codes":[{"code":"sparkles","emoji":"✨","meaning":"m","bump":"minor","section":"Features"}]}`},
		{"none with section", `{"version":"x","sections":["Features"],"codes":[{"code":":memo:","emoji":"📝","meaning":"m","bump":"none","section":"Features"}]}`},
		{"unknown section", `{"version":"x","sections":["Features"],"codes":[{"code":":bug:","emoji":"🐛","meaning":"m","bump":"patch","section":"Nope"}]}`},
		{"version-mover missing section", `{"version":"x","sections":["Features"],"codes":[{"code":":bug:","emoji":"🐛","meaning":"m","bump":"patch","section":""}]}`},
		{"duplicate code", `{"version":"x","sections":["Features"],"codes":[{"code":":bug:","emoji":"🐛","meaning":"m","bump":"patch","section":"Features"},{"code":":bug:","emoji":"🐛","meaning":"m","bump":"patch","section":"Features"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseTable([]byte(c.json)); err == nil {
				t.Fatalf("parseTable accepted invalid input %q; want an error", c.name)
			}
		})
	}
}
