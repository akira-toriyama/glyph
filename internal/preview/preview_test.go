package preview

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

func feat() Commit {
	return Commit{Code: ":sparkles:", Level: gitmoji.BumpMinor, Subject: "add the palette"}
}

// TestHeadline pins every branch of the sentence a reviewer reads. The strings
// are the ones the jq this package replaced emitted, byte for byte: absorbing
// the glue must not silently reword the fleet's comments.
func TestHeadline(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want string
	}{
		{
			name: "both none — nothing pending either",
			in:   Input{Current: "v1.2.3"},
			want: "⏸️ Merging this PR moves nothing — and nothing release-worthy is pending since **v1.2.3**.",
		},
		{
			name: "PR none but a bump is pending",
			in: Input{Current: "v1.2.3",
				Pending: Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"}},
			want: "⏸️ This PR does not move the version — the next release stays **v1.3.0**.",
		},
		{
			name: "PR is the first thing to move it",
			in: Input{Current: "v1.2.3",
				PR: Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"}},
			want: "🔼 Merging this PR raises **minor** — the next release becomes **v1.2.3 → v1.3.0**.",
		},
		{
			name: "PR escalates over what is pending",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: gitmoji.BumpMajor, Next: "v2.0.0"},
				Pending: Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"}},
			want: "💥 Merging this PR raises **major** — the next release escalates **v1.3.0 → v2.0.0**.",
		},
		{
			name: "PR is lower than what is pending — version unmoved",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: gitmoji.BumpPatch, Next: "v1.2.4"},
				Pending: Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"}},
			want: "🔧 Merging this PR adds **patch**-level changes — the next release stays **v1.3.0** (a **minor** bump is already pending).",
		},
		{
			name: "equal levels — pending still owns the version",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"},
				Pending: Verdict{Level: gitmoji.BumpMinor, Next: "v1.3.0"}},
			want: "🔼 Merging this PR adds **minor**-level changes — the next release stays **v1.3.0** (a **minor** bump is already pending).",
		},
		{
			name: "untagged repo — this PR would cut the first release",
			in: Input{Current: "v0.0.0", Untagged: true,
				PR: Verdict{Level: gitmoji.BumpMinor, Next: "v0.1.0"}},
			want: "🔼 Merging this PR raises **minor** — the first release here would be **v0.1.0**.",
		},
		{
			name: "untagged repo — nothing to move",
			in:   Input{Current: "v0.0.0", Untagged: true},
			want: "⏸️ Merging this PR moves nothing — and this repository has no release tag yet, so there is no version to move.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Headline(tt.in); got != tt.want {
				t.Errorf("Headline()\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// TestHeadlineNeverNamesADraft is the fleet-safety invariant, not a wording
// preference: this comment is distributed to every owned repo, and only three
// keep a glyph rolling draft. A sentence claiming one where none exists is
// false, not merely noisy — so no branch may ever say "draft".
func TestHeadlineNeverNamesADraft(t *testing.T) {
	levels := []gitmoji.Bump{gitmoji.BumpNone, gitmoji.BumpPatch, gitmoji.BumpMinor, gitmoji.BumpMajor}
	for _, untagged := range []bool{false, true} {
		for _, pl := range levels {
			for _, ql := range levels {
				in := Input{
					Current:  "v1.2.3",
					Untagged: untagged,
					PR:       Verdict{Level: pl, Next: "v9.9.9"},
					Pending:  Verdict{Level: ql, Next: "v8.8.8"},
				}
				got := strings.ToLower(Headline(in))
				if strings.Contains(got, "draft") {
					t.Errorf("headline names a draft (untagged=%v pr=%s pending=%s): %s", untagged, pl, ql, got)
				}
				if got == "" {
					t.Errorf("empty headline for untagged=%v pr=%s pending=%s", untagged, pl, ql)
				}
			}
		}
	}
}

// TestRenderMarkdown pins the whole body: marker first (the sticky comment is
// found by it), headline, the evidence table, then the footer.
func TestRenderMarkdown(t *testing.T) {
	got := Render(Input{
		Current: "v0.1.0",
		PR: Verdict{Level: gitmoji.BumpMajor, Next: "v1.0.0", Commits: []Commit{
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: "cook the noodles separately"},
			{Code: ":boom:", Level: gitmoji.BumpMajor, Breaking: true, Subject: "rebuild the broth soy-first"},
		}},
		Pending: Verdict{Level: gitmoji.BumpMinor, Next: "v0.2.0"},
	})
	want := `<!-- glyph-pr-verdict -->
💥 Merging this PR raises **major** — the next release escalates **v0.2.0 → v1.0.0**.

| commit | code | bump |
|---|---|---|
| cook the noodles separately | :bug: ` + "`:bug:`" + ` | patch |
| rebuild the broth soy-first | :boom: ` + "`:boom:`" + ` | major 💥 |

Computed from the 2 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded with what is already merged on the base branch since **v0.1.0**. Pushing more commits updates this comment.
`
	if got != want {
		t.Errorf("Render()\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderStartsWithMarker: the sticky-comment contract. A caller finds its
// previous comment by this exact prefix; drift here silently turns every push
// into a new comment instead of an edit.
func TestRenderStartsWithMarker(t *testing.T) {
	for _, in := range []Input{
		{Current: "v1.0.0"},
		{Current: "v0.0.0", Untagged: true, PR: Verdict{Level: gitmoji.BumpMinor, Next: "v0.1.0", Commits: []Commit{feat()}}},
	} {
		if !strings.HasPrefix(Render(in), Marker+"\n") {
			t.Errorf("body does not start with the marker:\n%s", Render(in))
		}
	}
}

// TestRenderEscapesSubject: a subject is author-supplied text and the table is
// the evidence the headline rests on — a pipe or a newline must not break it.
func TestRenderEscapesSubject(t *testing.T) {
	got := Render(Input{
		Current: "v1.0.0",
		PR: Verdict{Level: gitmoji.BumpMajor, Next: "v2.0.0", Commits: []Commit{
			{Code: ":boom:", Level: gitmoji.BumpMajor, Subject: "drop the legacy | pipe api\nand its docs"},
		}},
	})
	row := ""
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "| drop") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no table row rendered:\n%s", got)
	}
	if !strings.Contains(row, `drop the legacy \| pipe api and its docs`) {
		t.Errorf("subject not escaped/flattened: %s", row)
	}
	// The row must still be a 3-column row: separator + 3 cells + separator.
	if n := strings.Count(row, "|") - strings.Count(row, `\|`); n != 4 {
		t.Errorf("escaped row has %d unescaped pipes, want 4: %s", n, row)
	}
}

// TestRenderEscapesTheCellTheRendererWillSee pins the ordering inside
// escapeCell, which is a safety property and not a style choice: mention-safety
// belongs to the rendered inline context, and flattening is what MAKES the
// context. The subject below is two paragraphs to the escaper — backticks on
// either side of a blank line cannot pair, so it sees a code span around the
// mention and leaves it alone — and one line to GitHub, where the same
// backticks pair differently, the span lands somewhere else, and the mention
// comes out in prose right after a span's closing delimiter, which does not
// shield. Escaping before flattening rendered this cell as a live mention
// (measured 2026-07-21):
//
//	| a ` b  c `@octocat d` | 🐛 `:bug:` | patch |
func TestRenderEscapesTheCellTheRendererWillSee(t *testing.T) {
	got := Render(Input{
		Current: "v1.0.0",
		PR: Verdict{Level: gitmoji.BumpPatch, Next: "v1.0.1", Commits: []Commit{
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: "a ` b\n\nc `@octocat d`"},
		}},
	})
	if !strings.Contains(got, "| a ` b  c ` ``@octocat`` d` |") {
		t.Errorf("the cell was escaped against a context it does not render in:\n%s", got)
	}
}

// TestRenderNeutralizesMentions: a bare @token in a subject must come out
// inside a backtick fence — this body is posted as a PR comment, so a raw "@v1"
// would not just render as a link to the GitHub user v1, it would notify them.
// The lone-backtick subject is t-fbg3 at this layer: a one-backtick fence
// paired with the author's stray backtick, which fused half the sentence into a
// code span and pushed the mention back out into prose. The fence is sized
// against the subject, so that one comes out with two backticks.
func TestRenderNeutralizesMentions(t *testing.T) {
	got := Render(Input{
		Current: "v1.0.0",
		PR: Verdict{Level: gitmoji.BumpPatch, Next: "v1.0.1", Commits: []Commit{
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: "pin release + update-tap callers to @v1"},
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: "accept a lone ` in the subject, as @octocat asked"},
		}},
	})
	for _, want := range []string{"callers to `@v1`", "as ``@octocat`` asked"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in the comment body:\n%s", want, got)
		}
	}
	// Nothing may leave an at-sign standing on its own: GitHub links one that
	// has no backtick in front of it, wherever in the body it sits.
	for i, r := range got {
		if r != '@' {
			continue
		}
		if i == 0 || got[i-1] != '`' {
			t.Errorf("unshielded at-sign at byte %d of the comment body:\n%s", i, got)
		}
	}
}

// TestRenderNoTableWhenNoCommits: a PR whose commits are all excluded (a bot's,
// say) has no evidence to show, and an empty table header would read as a bug.
func TestRenderNoTableWhenNoCommits(t *testing.T) {
	got := Render(Input{Current: "v1.0.0"})
	if strings.Contains(got, "| commit |") {
		t.Errorf("rendered a table header with no rows:\n%s", got)
	}
	if !strings.Contains(got, "the 0 commit(s) participating") {
		t.Errorf("footer does not report the zero count:\n%s", got)
	}
}

// TestRenderNotes: the notes preview folds into <details> so the comment stays
// scannable — the headline is the message, the notes are for whoever asks.
func TestRenderNotes(t *testing.T) {
	got := Render(Input{
		Current: "v1.0.0",
		PR:      Verdict{Level: gitmoji.BumpMinor, Next: "v1.1.0", Commits: []Commit{feat()}},
		Notes:   "### Features\n\n- add the palette (abc1234)\n",
	})
	if !strings.Contains(got, "<details>\n<summary>Release notes preview</summary>") {
		t.Errorf("notes not folded into details:\n%s", got)
	}
	if !strings.Contains(got, "- add the palette (abc1234)") {
		t.Errorf("notes body missing:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank-line run in the body (details block spacing):\n%q", got)
	}
	if i, j := strings.Index(got, "<details>"), strings.Index(got, "Computed from"); i > j {
		t.Errorf("notes block must precede the footer:\n%s", got)
	}
}

// TestRenderNoNotesBlockWhenEmpty: an empty Notes preview renders no block, so
// an empty <details> is never a dead disclosure triangle.
func TestRenderNoNotesBlockWhenEmpty(t *testing.T) {
	if got := Render(Input{Current: "v1.0.0"}); strings.Contains(got, "<details>") {
		t.Errorf("rendered an empty details block:\n%s", got)
	}
}

// TestRenderNeutralizesMarkupInTheCell pins the t-j0c6 fix at the preview sink,
// which is the sharper of the two: this body is posted as a PR COMMENT, so a
// subject that breaks out of its container does it in a comment authored by a
// bot, over the reviewer's signature.
//
// Measured against GitHub 2026-07-21, before the fix — the injected <h1> left
// the collapsed block entirely and landed at the top level of the comment:
//
//	<details><summary>Release notes preview</summary>
//	<ul><li>⚡️ speed up </li></ul></details><h1>OWNED</h1> the loop (abc1234)
//
// and after it, contained and inert:
//
//	<details><summary>Release notes preview</summary><ul><li>⚡️ speed up
//	&lt;/details&gt;&lt;h1&gt;OWNED&lt;/h1&gt; the loop (abc1234)</li></ul></details>
func TestRenderNeutralizesMarkupInTheCell(t *testing.T) {
	got := Render(Input{
		Current: "v1.0.0",
		PR: Verdict{Level: gitmoji.BumpPatch, Next: "v1.0.1", Commits: []Commit{
			{Code: ":zap:", Level: gitmoji.BumpPatch, Subject: "speed up </details><h1>OWNED</h1> the loop"},
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: `add a tracker <img src="https://evil.example/p.png">`},
			{Code: ":bug:", Level: gitmoji.BumpPatch, Subject: "see [x](http://evil.example) and www.evil.example"},
		}},
		Notes: "## Fixes\n\n- ⚡️ speed up the loop (abc1234)\n",
	})
	for _, want := range []string{
		`| speed up \</details>\<h1>OWNED\</h1> the loop |`,
		`| add a tracker \<img src="https\://evil.example/p.png"> |`,
		`| see \[x](http\://evil.example) and www\.evil.example |`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cell not neutralized, want %q in:\n%s", want, got)
		}
	}
	// The whole point: exactly one LIVE </details> in the body, and it is
	// glyph's own. The escaped one in the cell is text, not a tag — counting
	// substrings alone would have credited it as a break-out.
	live := 0
	for i := range got {
		if strings.HasPrefix(got[i:], "</details>") && (i == 0 || got[i-1] != '\\') {
			live++
		}
	}
	if live != 1 {
		t.Errorf("body carries %d live </details>, want exactly 1 (glyph's own):\n%s", live, got)
	}
	// No unescaped '<' survives anywhere a subject reached.
	for i, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue // glyph's own markup (the marker, the <details> block)
		}
		for j := 0; j < len(line); j++ {
			if line[j] == '<' && (j == 0 || line[j-1] != '\\') {
				t.Errorf("line %d carries an unescaped '<' at %d: %s", i, j, line)
			}
		}
	}
}
