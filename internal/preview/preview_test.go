package preview

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/bump"
)

func feat() Commit {
	return Commit{Sigil: "^", Level: bump.LevelMinor, Subject: "add the palette"}
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
				Pending: Verdict{Level: bump.LevelMinor, Next: "v1.3.0"}},
			want: "⏸️ This PR does not move the version — the next release stays **v1.3.0**.",
		},
		{
			name: "PR is the first thing to move it",
			in: Input{Current: "v1.2.3",
				PR: Verdict{Level: bump.LevelMinor, Next: "v1.3.0"}},
			want: "🔼 Merging this PR raises **minor** — the next release becomes **v1.2.3 → v1.3.0**.",
		},
		{
			name: "PR escalates over what is pending",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: bump.LevelMajor, Next: "v2.0.0"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "v1.3.0"}},
			want: "💥 Merging this PR raises **major** — the next release escalates **v1.3.0 → v2.0.0**.",
		},
		{
			name: "0.x — PR major over pending minor: the level rises, the version holds",
			in: Input{Current: "v0.3.0",
				PR:      Verdict{Level: bump.LevelMajor, Next: "v0.4.0"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "v0.4.0"}},
			want: "💥 Merging this PR raises **major** — the next release stays **v0.4.0** (on 0.x a major steps the minor, and a **minor** bump is already pending).",
		},
		{
			name: "0.x — the same on a package line, spelled as tags",
			in: Input{Current: "camp/v0.3.0",
				PR:      Verdict{Level: bump.LevelMajor, Next: "camp/v0.4.0"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "camp/v0.4.0"}},
			want: "💥 Merging this PR raises **major** — the next release stays **camp/v0.4.0** (on 0.x a major steps the minor, and a **minor** bump is already pending).",
		},
		{
			name: "PR is lower than what is pending — version unmoved",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: bump.LevelPatch, Next: "v1.2.4"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "v1.3.0"}},
			want: "🔧 Merging this PR adds **patch**-level changes — the next release stays **v1.3.0** (a **minor** bump is already pending).",
		},
		{
			name: "equal levels — pending still owns the version",
			in: Input{Current: "v1.2.3",
				PR:      Verdict{Level: bump.LevelMinor, Next: "v1.3.0"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "v1.3.0"}},
			want: "🔼 Merging this PR adds **minor**-level changes — the next release stays **v1.3.0** (a **minor** bump is already pending).",
		},
		{
			name: "untagged repo — this PR would cut the first release",
			in: Input{Current: "v0.0.0", Untagged: true,
				PR: Verdict{Level: bump.LevelMinor, Next: "v0.1.0"}},
			want: "🔼 Merging this PR raises **minor** — the first release here would be **v0.1.0**.",
		},
		{
			name: "untagged repo — nothing to move",
			in:   Input{Current: "v0.0.0", Untagged: true},
			want: "⏸️ Merging this PR moves nothing — and the base branch holds no release tag yet, so there is no version to move.",
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
	levels := []bump.Level{bump.LevelNone, bump.LevelPatch, bump.LevelMinor, bump.LevelMajor}
	for _, untagged := range []bool{false, true} {
		for _, pl := range levels {
			for _, ql := range levels {
				// same drives the two Nexts equal, the 0.x collapse's shape.
				for _, same := range []bool{false, true} {
					pending := "v8.8.8"
					if same {
						pending = "v9.9.9"
					}
					in := Input{
						Current:  "v1.2.3",
						Untagged: untagged,
						PR:       Verdict{Level: pl, Next: "v9.9.9"},
						Pending:  Verdict{Level: ql, Next: pending},
					}
					got := strings.ToLower(Headline(in))
					if strings.Contains(got, "draft") {
						t.Errorf("headline names a draft (untagged=%v pr=%s pending=%s same=%v): %s", untagged, pl, ql, same, got)
					}
					if got == "" {
						t.Errorf("empty headline for untagged=%v pr=%s pending=%s same=%v", untagged, pl, ql, same)
					}
				}
			}
		}
	}
}

// TestHeadlineNeverEscalatesToTheSameVersion pins the sentence against the
// arithmetic it describes: every (current, PR level, pending level) the
// lattice allows is stepped with bump.Version.Next — the one place the 0.x
// rule lives — and no headline may draw an arrow from a version to itself.
// On 0.x a major over a pending minor lands where the minor already does,
// and the escalates arm rendered that as "v0.4.0 → v0.4.0" (t-d0d9,
// measured 2026-09-26 on glyph-monorepo-test #31). The other rows are the
// positive control: there every higher level steps to a different version
// and the escalates arm must still fire, as must the 0.x pairs over a patch.
func TestHeadlineNeverEscalatesToTheSameVersion(t *testing.T) {
	levels := []bump.Level{bump.LevelPatch, bump.LevelMinor, bump.LevelMajor}
	escalated := 0
	for _, current := range []string{"v0.3.0", "v1.2.3"} {
		cur, err := bump.ParseVersion(current)
		if err != nil {
			t.Fatal(err)
		}
		for _, pl := range levels {
			for _, ql := range levels {
				prNext := cur.Next(bump.Decision{Level: pl}).String()
				pendingNext := cur.Next(bump.Decision{Level: ql}).String()
				got := Headline(Input{Current: current,
					PR:      Verdict{Level: pl, Next: prNext},
					Pending: Verdict{Level: ql, Next: pendingNext}})
				if strings.Contains(got, "**"+prNext+" → "+prNext+"**") {
					t.Errorf("%s pr=%s pending=%s: the headline escalates a version to itself: %s", current, pl, ql, got)
				}
				if prNext == pendingNext && rank(pl) > rank(ql) && !strings.Contains(got, "stays **"+pendingNext+"**") {
					t.Errorf("%s pr=%s pending=%s: same next version, but the headline does not say it stays: %s", current, pl, ql, got)
				}
				if strings.Contains(got, "escalates") {
					escalated++
				}
			}
		}
	}
	// The pairs a higher level really moves: all three on v1.2.3, and on
	// v0.3.0 the two over a pending patch (v0.3.1 → v0.4.0) — only major
	// over minor collapses there.
	if escalated != 5 {
		t.Errorf("escalates fired %d times over the lattice, want 5 (the pairs a higher level really moves)", escalated)
	}
}

// TestRenderMarkdown pins the whole body: marker first (the sticky comment is
// found by it), headline, the evidence table, then the footer.
//
// The fixture is a PROMOTION, and it has to be: a 0.x repository reaching
// v1.0.0 is now something only a '%' commit can do, so pairing v0.1.0 with a
// v1.0.0 next over a plain '!' would pin a shape glyph can no longer compute.
// It also exercises the row a promoting commit produces — sigil '%', level
// major — which is what keeps it out of the default branch of every consumer
// that switches on a level.
func TestRenderMarkdown(t *testing.T) {
	got := Render(Input{
		Current: "v0.1.0",
		PR: Verdict{Level: bump.LevelMajor, Next: "v1.0.0", Commits: []Commit{
			{Sigil: "~", Level: bump.LevelPatch, Subject: "cook the noodles separately"},
			{Sigil: "%", Level: bump.LevelMajor, Subject: "rebuild the broth soy-first"},
		}},
		Pending: Verdict{Level: bump.LevelMinor, Next: "v0.2.0"},
	})
	want := `<!-- glyph-pr-verdict -->
💥 Merging this PR raises **major** — the next release escalates **v0.2.0 → v1.0.0**.

| commit | sigil | bump |
|---|---|---|
| cook the noodles separately | ` + "`~`" + ` | patch |
| rebuild the broth soy-first | ` + "`%`" + ` | major |

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
		{Current: "v0.0.0", Untagged: true, PR: Verdict{Level: bump.LevelMinor, Next: "v0.1.0", Commits: []Commit{feat()}}},
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
		PR: Verdict{Level: bump.LevelMajor, Next: "v2.0.0", Commits: []Commit{
			{Sigil: "!", Level: bump.LevelMajor, Subject: "drop the legacy | pipe api\nand its docs"},
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
		PR: Verdict{Level: bump.LevelPatch, Next: "v1.0.1", Commits: []Commit{
			{Sigil: "~", Level: bump.LevelPatch, Subject: "a ` b\n\nc `@octocat d`"},
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
		PR: Verdict{Level: bump.LevelPatch, Next: "v1.0.1", Commits: []Commit{
			{Sigil: "~", Level: bump.LevelPatch, Subject: "pin release + update-tap callers to @v1"},
			{Sigil: "~", Level: bump.LevelPatch, Subject: "accept a lone ` in the subject, as @octocat asked"},
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
		PR:      Verdict{Level: bump.LevelMinor, Next: "v1.1.0", Commits: []Commit{feat()}},
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
		PR: Verdict{Level: bump.LevelPatch, Next: "v1.0.1", Commits: []Commit{
			{Sigil: "~", Level: bump.LevelPatch, Subject: "speed up </details><h1>OWNED</h1> the loop"},
			{Sigil: "~", Level: bump.LevelPatch, Subject: `add a tracker <img src="https://evil.example/p.png">`},
			{Sigil: "~", Level: bump.LevelPatch, Subject: "see [x](http://evil.example) and www.evil.example"},
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

// TestRenderPackages pins the per-line body (DESIGN §4.1, "Preview"): the
// marker, one headline per line led by the line's name with versions
// spelled as tags, one table per line, and a footer stating the caller's
// participating count and naming every line's base — the single line's
// sentences, per line.
func TestRenderPackages(t *testing.T) {
	got := Render(Input{Participating: 2, Packages: []Package{
		{Path: "haiku", Current: "haiku/v0.1.0",
			PR:      Verdict{Level: bump.LevelMinor, Next: "haiku/v0.2.0", Commits: []Commit{{Sigil: "^", Level: bump.LevelMinor, Subject: "add a season"}}},
			Pending: Verdict{Level: bump.LevelNone}},
		{Path: "curry", Current: "curry/v0.1.0",
			PR:      Verdict{Level: bump.LevelPatch, Next: "curry/v0.1.1", Commits: []Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "swap an ingredient"}}},
			Pending: Verdict{Level: bump.LevelMinor, Next: "curry/v0.2.0"}},
	}})
	want := `<!-- glyph-pr-verdict -->
**haiku** — 🔼 Merging this PR raises **minor** — the next release becomes **haiku/v0.1.0 → haiku/v0.2.0**.
**curry** — 🔧 Merging this PR adds **patch**-level changes — the next release stays **curry/v0.2.0** (a **minor** bump is already pending).

### haiku

| commit | sigil | bump |
|---|---|---|
| add a season | ` + "`^`" + ` | minor |

### curry

| commit | sigil | bump |
|---|---|---|
| swap an ingredient | ` + "`~`" + ` | patch |

Computed from the 2 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded, per line, with what is already merged on the base branch since **haiku/v0.1.0** (haiku), **curry/v0.1.0** (curry). Pushing more commits updates this comment.
`
	if got != want {
		t.Errorf("Render(packages)\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderPackagesCountsASharedCommitOnce: a commit that moves two lines
// sits in both tables and is one commit in the footer's count — the caller's
// count, each sha once, never the tables' rows added up; an untagged line is
// named as such instead of given a base.
func TestRenderPackagesCountsASharedCommitOnce(t *testing.T) {
	move := Commit{Sigil: "~", Level: bump.LevelPatch, Subject: "move a file across the lines"}
	got := Render(Input{Participating: 1, Packages: []Package{
		{Path: "haiku", Current: "haiku/v0.1.0", PR: Verdict{Level: bump.LevelPatch, Next: "haiku/v0.1.1", Commits: []Commit{move}}, Pending: Verdict{Level: bump.LevelNone}},
		{Path: "curry", Current: "curry/v0.0.0", Untagged: true, PR: Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1", Commits: []Commit{move}}},
	}})
	if !strings.Contains(got, "Computed from the 1 commit(s) participating") {
		t.Errorf("a commit in two lines must be counted once:\n%s", got)
	}
	if !strings.Contains(got, "since **haiku/v0.1.0** (haiku). curry has no release tag on the base branch yet, so nothing merged earlier is folded in for it.") {
		t.Errorf("the untagged line must be named, not given a base:\n%s", got)
	}
	if strings.Count(got, "move a file across the lines") != 2 {
		t.Errorf("a commit moving two lines sits in both tables:\n%s", got)
	}
}

// TestRenderPackagesCountsTwoCommitsWithOneSubjectAsTwo: two commits may share
// a subject — ":bug:(haiku)~ address review", twice — and they are two rows
// and two commits. The footer counted the tables' distinct sigil-and-subject
// pairs (a row carries no sha), so it said 2 under a table of three rows
// (t-rrw0 (1)); it states the caller's count, which is taken from shas.
// Mutation row preview-footer-counts-the-tables-distinct-subjects.
func TestRenderPackagesCountsTwoCommitsWithOneSubjectAsTwo(t *testing.T) {
	review := Commit{Sigil: "~", Level: bump.LevelPatch, Subject: ":bug:(haiku)~ address review"}
	got := Render(Input{Participating: 3, Packages: []Package{{
		Path: "haiku", Current: "haiku/v0.1.0",
		PR:      Verdict{Level: bump.LevelMinor, Next: "haiku/v0.2.0", Commits: []Commit{{Sigil: "^", Level: bump.LevelMinor, Subject: ":sparkles:(haiku)^ add a season"}, review, review}},
		Pending: Verdict{Level: bump.LevelNone},
	}}})
	if rows := strings.Count(got, "| :bug:(haiku)~ address review | `~` | patch |\n"); rows != 2 {
		t.Fatalf("positive control: the table must hold both commits, got %d row(s):\n%s", rows, got)
	}
	if !strings.Contains(got, "Computed from the 3 commit(s) participating in this PR") {
		t.Errorf("three rows of three commits, and the footer under them counts otherwise:\n%s", got)
	}
}

// TestRenderPackagesSaysHowManySitOnNoLine: a participating commit on no line
// is in no table, so the footer that counts it says so — else the count reads
// as a miscount against the rows above it (DESIGN §4.1). The sentence is its
// own, after the one naming the bases and before the untagged lines'; it
// agrees in number; and it is absent when every participating commit is on a
// line. Mutation row preview-footer-hides-the-commits-on-no-line.
func TestRenderPackagesSaysHowManySitOnNoLine(t *testing.T) {
	in := Input{Participating: 4, Packages: []Package{
		{Path: "haiku", Current: "haiku/v0.1.0",
			PR:      Verdict{Level: bump.LevelMinor, Next: "haiku/v0.2.0", Commits: []Commit{{Sigil: "^", Level: bump.LevelMinor, Subject: "add a season"}}},
			Pending: Verdict{Level: bump.LevelNone}},
		{Path: "curry", Current: "curry/v0.0.0", Untagged: true,
			PR: Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1", Commits: []Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "swap an ingredient"}}}},
	}}
	const head = "Computed from the 4 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them — folded, per line, with what is already merged on the base branch since **haiku/v0.1.0** (haiku)."
	const tail = " curry has no release tag on the base branch yet, so nothing merged earlier is folded in for it. Pushing more commits updates this comment.\n"
	for _, tc := range []struct {
		offLine int
		says    string
	}{
		{0, ""},
		{1, " 1 of them sits on no line."},
		{2, " 2 of them sit on no line."},
	} {
		in.OffLine = tc.offLine
		got := Render(in)
		if want := "\n" + head + tc.says + tail; !strings.HasSuffix(got, want) {
			t.Errorf("OffLine %d: the body must end\n  %q\ngot:\n%s", tc.offLine, want, got)
		}
		if tc.offLine == 0 && strings.Contains(got, "on no line") {
			t.Errorf("no participating commit is off a line, and the footer names some:\n%s", got)
		}
	}
}

// TestRenderNoLine pins the body of a pull, in a repository with lines, whose
// commits sit on none (DESIGN §4.1, "Preview"): the marker, one sentence with
// no count in it, and the footer's participating count — never "N of them
// sit on no line" here, where the sentence above has said it of every one.
// With the PR side short the sentence claims no more than the files GitHub
// listed and the caveat makes a floor of "moves nothing", pointing at no
// figures. Mutation row preview-moves-nothing-counts-the-pulls-commits.
func TestRenderNoLine(t *testing.T) {
	got := RenderNoLine(Input{Participating: 1, OffLine: 1})
	want := `<!-- glyph-pr-verdict -->
⏸️ Merging this PR moves nothing — no commit participating in it touches a declared package.

Computed from the 1 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. Pushing more commits updates this comment.
`
	if got != want {
		t.Errorf("RenderNoLine()\n got:\n%s\nwant:\n%s", got, want)
	}

	got = RenderNoLine(Input{Participating: 1, OffLine: 1, PRShort: "GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1)"})
	want = `<!-- glyph-pr-verdict -->
⏸️ Merging this PR moves nothing in the files GitHub listed — no commit participating in it touches a declared package there.

> [!WARNING]
> This PR's own side of this fold is INCOMPLETE: GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1). A line one of those commits touches in files GitHub did not list may move all the same, so treat "moves nothing" as a floor rather than the answer.

Computed from the 1 commit(s) participating in this PR — squash-safe, a squash-merge cannot erase them. Pushing more commits updates this comment.
`
	if got != want {
		t.Errorf("RenderNoLine(PRShort)\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderPackagesPRShort: the PR-side caveat sits with the pending one,
// under the headlines and above the tables — it qualifies the conclusion,
// not the evidence — and is absent byte for byte when every listing was
// whole. prShortBlock is the same text for the "moves nothing" body
// (TestRenderNoLine), which carries no figures: there the caveat
// makes a floor of "moves nothing" instead of pointing at figures above.
// Its own sentence names no cause, because PRShort does: a listing GitHub
// answered 422 for is not one it cut at the cap (t-esm5).
func TestRenderPackagesPRShort(t *testing.T) {
	in := Input{Packages: []Package{{
		Path: "haiku", Current: "haiku/v0.1.0",
		PR:      Verdict{Level: bump.LevelMinor, Next: "haiku/v0.2.0", Commits: []Commit{{Sigil: "^", Level: bump.LevelMinor, Subject: "add a season"}}},
		Pending: Verdict{Level: bump.LevelNone},
	}}}
	if out := Render(in); strings.Contains(out, "This PR's own side") {
		t.Fatalf("a whole listing must render no PR-side caveat:\n%s", out)
	}
	in.PRShort = "GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1)"
	out := Render(in)
	caveat := "\n> [!WARNING]\n> This PR's own side of this fold is INCOMPLETE: GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1). A line one of those commits touches in files GitHub did not list may be missing from the figures above, so treat each as a floor rather than the answer.\n"
	headline, warning, table := strings.Index(out, "**haiku**"), strings.Index(out, caveat), strings.Index(out, "### haiku")
	if headline < 0 || warning < 0 || table < 0 || headline >= warning || warning >= table {
		t.Fatalf("the PR-side caveat must sit under the headlines and above the tables:\n%s", out)
	}
	if prShortBlock(in.PRShort, true) != caveat || prShortBlock("", true) != "" || prShortBlock("", false) != "" {
		t.Fatalf("prShortBlock must be the rendered caveat and nothing when there is none: %q", prShortBlock(in.PRShort, true))
	}
	nothing := "\n> [!WARNING]\n> This PR's own side of this fold is INCOMPLETE: GitHub answered 422 for the file listing of 1 commit(s), so a line they touch in files it did not list could not be read (c1). A line one of those commits touches in files GitHub did not list may move all the same, so treat \"moves nothing\" as a floor rather than the answer.\n"
	if got := prShortBlock(in.PRShort, false); got != nothing {
		t.Fatalf("prShortBlock with no figures = %q, want %q", got, nothing)
	}
}
