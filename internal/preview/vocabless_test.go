package preview

import "testing"

// TestCodeCellRendersPerVocabulary pins the verdict table's token cell: a
// textual gitmoji prints the rendered-glyph + literal pair (GitHub draws the
// bare :code: as its emoji), while a conventional type — which has no rendered
// form — prints the backticked literal once, not "feat `feat`" (measured on
// glyph-test2's first verdict comment). Shape-checked, deliberately: the
// preview stays ignorant of which vocabulary it renders.
func TestCodeCellRendersPerVocabulary(t *testing.T) {
	if got, want := codeCell(":sparkles:"), ":sparkles: `:sparkles:`"; got != want {
		t.Fatalf("codeCell(gitmoji) = %q, want %q", got, want)
	}
	if got, want := codeCell("feat"), "`feat`"; got != want {
		t.Fatalf("codeCell(type) = %q, want %q", got, want)
	}
}
