package markdown

import (
	"strings"
	"testing"
)

// A code span is what licenses Line to leave author bytes alone, and
// CommonMark pairs backtick RUNS across the whole inline context — the LINE,
// not the field. Escaping each field against its own spans asked the wrong
// question and answered it in the under-escaping direction escape.go forbids:
// a subject ending in an unclosed backtick re-paired with the first backtick of
// the author name, and the bytes across that seam were copied raw (t-9np1).
//
// These tests are the line-level half of escape_test.go's rule 3. That rule
// holds "no surviving trigger in prose" for ONE string; the defect lived
// entirely in the composition of two, so nothing there could have caught it.

// TestProseFieldsCannotPairBacktickRunsAcrossFields pins the two payloads
// measured live at e41051d, each with the field split that made it work.
func TestProseFieldsCannotPairBacktickRunsAcrossFields(t *testing.T) {
	for _, tc := range []struct {
		name         string
		subject      string
		glue         string
		second       string
		mustNotHold  string
		mustBeKilled []string
	}{
		{
			// The shipped preset: `- $subject$[ ($pr)] @$author`. git strips
			// '<' and '>' from an author name but leaves backticks and
			// brackets, so the reachable payload here is a link.
			name:         "live link across subject and author name",
			subject:      "fix the ` thing",
			glue:         " @",
			second:       "x`[CLICK ME](https://evil.example)`y",
			mustNotHold:  "[CLICK ME](https://evil.example)",
			mustBeKilled: []string{`\[`, `https\://`},
		},
		{
			// A custom note.line whose scope group is permissive enough to
			// carry angle brackets: `- $subject — $scope`.
			name:         "raw HTML across subject and scope",
			subject:      "fix the ` thing",
			glue:         " — ",
			second:       "`</details><h1>OWNED</h1>`",
			mustNotHold:  "</details><h1>OWNED</h1>",
			mustBeKilled: []string{`\</details>`, `\<h1>`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var l Line
			l.Raw("- ")
			l.Prose(tc.subject)
			l.Raw(tc.glue)
			l.Prose(tc.second)
			got := l.String()

			if strings.Contains(got, tc.mustNotHold) {
				t.Errorf("Line.String() = %q\nstill carries the live payload %q — the second "+
					"field's backtick paired with the first field's and the bytes between them "+
					"were copied raw", got, tc.mustNotHold)
			}
			for _, want := range tc.mustBeKilled {
				if !strings.Contains(got, want) {
					t.Errorf("Line.String() = %q\ndoes not carry %q; the trigger was not disarmed", got, want)
				}
			}
		})
	}
}

// TestStringIsAFixedPointAcrossFields keeps String's documented promise now
// that it recomputes from the parts rather than from a finished builder.
func TestStringIsAFixedPointAcrossFields(t *testing.T) {
	var l Line
	l.Raw("- ")
	l.Prose("fix the ` thing")
	l.Raw(" @")
	l.Prose("x`[a](b)`y")
	first := l.String()
	if second := l.String(); second != first {
		t.Errorf("String() is not repeatable:\n first  = %q\n second = %q", first, second)
	}
}

// FuzzLineProseNeverLeavesATriggerLive states the line-level invariant without
// escape_test.go's no-backtick premise, because the defect it guards REQUIRES a
// backtick: the premise that makes rule 3 honest for one string would exclude
// every input that can exhibit this bug.
//
// The invariant instead reads the finished line the way GitHub will: compute
// the code spans OF THE OUTPUT — exact by construction once the competitors are
// dead (escape.go's theorem) — and require every trigger lying outside them to
// be escaped. Inside a span nothing is escaped, by design, and that is now
// stated as a position rather than assumed away.
func FuzzLineProseNeverLeavesATriggerLive(f *testing.F) {
	for _, seed := range [][2]string{
		{"fix the ` thing", "x`[CLICK ME](https://evil.example)`y"},
		{"fix the ` thing", "`</details><h1>OWNED</h1>`"},
		{"a ` b", "c ` d"},
		{"", ""},
		{"``", "``"},
		{`\`, "<"},
		{"http://a", "www.b.com"},
		{"a `` b ` c", "d ` e `` f"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		// The composition shape is part of the input, not a constant. A guard
		// that fixes the glue to " @" proves the invariant for the shipped
		// preset and for no other template — and note.line is the user's, so
		// the glue is whatever they wrote. The first byte of a steers it.
		var glue string
		switch {
		case a == "":
			glue = " @"
		case a[0]%4 == 0:
			glue = " @"
		case a[0]%4 == 1:
			glue = "" // adjacent prose fields: no glue at all
		case a[0]%4 == 2:
			glue = " — "
		default:
			glue = "` ("
		}
		var l Line
		l.Raw("- ")
		l.Prose(a)
		l.Raw(glue)
		l.Prose(b)
		out := l.String()

		inSpan := make([]bool, len(out))
		for _, sp := range codeSpans(out) {
			for i := sp[0]; i < sp[1] && i < len(out); i++ {
				inSpan[i] = true
			}
		}

		for i := 0; i < len(out); i++ {
			if inSpan[i] {
				continue
			}
			switch {
			case out[i] == '<', out[i] == '[':
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("Line.Prose(%q) + Raw(glue) + Line.Prose(%q) = %q leaves a live %q at %d, outside every code span of the OUTPUT",
						a, b, out, out[i], i)
				}
			case out[i] == '&' && entityAt(out, i):
				if !oddBackslashRunBefore(out, i) {
					t.Fatalf("Line.Prose(%q) + Raw(glue) + Line.Prose(%q) = %q leaves an entity live at %d, outside every code span of the OUTPUT", a, b, out, i)
				}
			}
		}
		for _, trigger := range []string{"http://", "https://", "ftp://", "www."} {
			idx := indexFold(out, trigger)
			if idx >= 0 && !inSpan[idx] {
				t.Fatalf("Line.Prose(%q) + Raw(glue) + Line.Prose(%q) = %q left %q unbroken at %d, outside every code span of the OUTPUT", a, b, out, trigger, idx)
			}
		}
	})
}

// TestRunsLongerThanCmarkOpensAreNotBelieved pins the MAXBACKTICKS boundary.
// A code span is the ONLY reason this package copies author bytes raw, so a
// span glyph believes in and cmark does not is a stretch of author text shipped
// live — the under-escaping direction escape.go forbids. cmark registers
// backtick runs in an 80-wide array and a longer run is registered nowhere, so
// it can neither open nor close.
//
// Measured against GitHub 2026-09-11: a run of 80 renders its content as code;
// 81, 100 and 1001 render the bytes between them LIVE. The payload below
// reached a published release body and a pr-verdict comment from a single
// commit subject on the shipped preset (t-9np1).
func TestRunsLongerThanCmarkOpensAreNotBelieved(t *testing.T) {
	const payload = "[CLICK ME](https://evil.example)"
	for _, n := range []int{79, 80, 81, 100, 1001} {
		run := strings.Repeat("`", n)
		var l Line
		l.Raw("- ")
		l.Prose(run + payload + run)
		got := l.String()
		spanned := n <= maxBacktickRun

		if spanned {
			if !strings.Contains(got, payload) {
				t.Errorf("run of %d: a span cmark DOES open must keep the author's bytes as code, got %q", n, got)
			}
			continue
		}
		if strings.Contains(got, payload) {
			t.Errorf("run of %d: cmark opens no span this wide, so these bytes are prose and render "+
				"as a LIVE link; got %q", n, got)
		}
	}
}
