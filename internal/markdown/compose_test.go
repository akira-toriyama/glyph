package markdown

import (
	"strings"
	"testing"
)

// TestLineSealsTheEscapeOrder pins the property Line exists for (t-3f4s): the
// per-field passes run inside Text and Prose, and the mention fence runs LAST,
// inside String, over the whole assembled line — including Raw stretches and
// sized against backticks that other fragments contributed. The mutation ledger
// names this test: a String that returns its bytes unfenced turns every
// release-notes line and preview cell into a potential live @mention, which is
// a notification to a stranger (incident t-hykw), not a rendering nit.
func TestLineSealsTheEscapeOrder(t *testing.T) {
	t.Run("fence runs last and over the assembled line", func(t *testing.T) {
		// The measured incident shape (notes.entryLine's comment, 2026-07-21):
		// the scope carries a backtick, the subject carries the mentions, and
		// only a fence sized over the ASSEMBLED line beats the run the scope
		// smuggled into the shared inline context.
		var l Line
		l.Raw("- 🐛 ")
		l.Raw("**")
		l.Text("readme`")
		l.Raw(":** ")
		l.Prose("credit @alice and @bob for the fix")
		l.Raw(" (abc1234)")
		got := l.String()
		// The scope's backtick arrives escaped (escapeText), still counts for
		// fence sizing (escaped backticks pair at cmark like any other), so the
		// fence is two — and both mentions are fenced, not just the subject's.
		want := "- 🐛 **readme\\`:** credit ``@alice`` and ``@bob`` for the fix (abc1234)"
		if got != want {
			t.Fatalf("composed line:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("the fence covers Raw stretches too", func(t *testing.T) {
		var l Line
		l.Raw("cc @octocat")
		if got, want := l.String(), "cc `@octocat`"; got != want {
			t.Fatalf("got %q, want %q — a Raw stretch left out of the fence pass is a mention hole", got, want)
		}
	})

	t.Run("Text and Prose route to their field policies, flattened", func(t *testing.T) {
		var l Line
		l.Text("a\nb<")
		l.Raw(" ")
		l.Prose("keep `code` kill\n<i>")
		got := l.String()
		// Text: flattened, every escapable byte disarmed. Prose: flattened, the
		// author's span survives, the raw-HTML opener does not.
		want := `a b\< keep ` + "`code`" + ` kill \<i>`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("String repeats", func(t *testing.T) {
		var l Line
		l.Prose("thanks @octocat")
		first := l.String()
		if second := l.String(); second != first {
			t.Fatalf("String is not repeatable: first %q, then %q", first, second)
		}
		if !strings.Contains(first, "`@octocat`") {
			t.Fatalf("mention not fenced: %q", first)
		}
	})
}
