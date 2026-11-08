package preview

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
)

// This file pins how a line with no release tag is rendered: its versions
// are its FIRST release, never a null floor spelled as a tag (t-1n1s), its
// absence is said of the line on a packages body and of the base branch on
// the bare line, its footer agrees in number (t-c2a9), and on a shallow
// checkout no body claims what the base branch holds or names a version off
// a floor the clone never reached (t-txed).

// TestHeadlineUntaggedLineNamesItsFirstRelease: a line with no tag names
// every version as its FIRST release, pending side skipped or walked. Drawn
// through the tagged arms, a walked line with no tag read "travel/v0.0.0 →
// travel/v0.1.0" — a null floor spelled as a tag nobody cut (t-1n1s,
// measured 2026-11-04 on glyph-monorepo-test-2 `preview --pr 2`, camp
// tagged and travel not). Each tagged arm has its untagged twin here, and a
// null floor is never spelled out. The nothing-moves sentence is the one
// place the bare line and a packages line differ: the bare line's absence
// is the base branch's, a line's is the line's alone — "the base branch
// holds no release tag yet" under a footer naming a sibling's tag on that
// branch was false (review of #361, 2026-11-08). Mutation rows
// preview-untagged-line-spells-its-null-floor-as-a-tag and
// preview-untagged-line-denies-the-base-branch-a-tag.
func TestHeadlineUntaggedLineNamesItsFirstRelease(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		line bool
		want string
	}{
		{
			name: "walked, PR lower than what is pending",
			in: Input{Current: "curry/v0.0.0", Untagged: true,
				PR:      Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}},
			want: "🔧 Merging this PR adds **patch**-level changes — the first release here would be **curry/v0.1.0** (a **minor** bump is already pending).",
		},
		{
			name: "walked, PR higher than what is pending",
			in: Input{Current: "travel/v0.0.0", Untagged: true,
				PR:      Verdict{Level: bump.LevelMinor, Next: "travel/v0.1.0"},
				Pending: Verdict{Level: bump.LevelPatch, Next: "travel/v0.0.1"}},
			want: "🔼 Merging this PR raises **minor** — the first release here would be **travel/v0.1.0** (a **patch** bump is already pending).",
		},
		{
			name: "walked, PR none over a pending minor",
			in: Input{Current: "curry/v0.0.0", Untagged: true,
				Pending: Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}},
			want: "⏸️ This PR does not move the version — the first release here would be **curry/v0.1.0** (a **minor** bump is already pending).",
		},
		{
			name: "walked, 0.x major over a pending minor lands on the same first release",
			in: Input{Current: "curry/v0.0.0", Untagged: true,
				PR:      Verdict{Level: bump.LevelMajor, Next: "curry/v0.1.0"},
				Pending: Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}},
			want: "💥 Merging this PR raises **major** — the first release here would still be **curry/v0.1.0** (on 0.x a major steps the minor, and a **minor** bump is already pending).",
		},
		{
			name: "walked, nothing pending — the skipped shape, byte for byte",
			in: Input{Current: "travel/v0.0.0", Untagged: true,
				PR: Verdict{Level: bump.LevelMinor, Next: "travel/v0.1.0"}, Pending: Verdict{Level: bump.LevelNone}},
			want: "🔼 Merging this PR raises **minor** — the first release here would be **travel/v0.1.0**.",
		},
		{
			name: "nothing moves on the bare line: the base branch's absence",
			in:   Input{Current: "v0.0.0", Untagged: true},
			want: "⏸️ Merging this PR moves nothing — and the base branch holds no release tag yet, so there is no version to move.",
		},
		{
			name: "nothing moves on a packages line: the line's absence",
			in:   Input{Current: "curry/v0.0.0", Untagged: true},
			line: true,
			want: "⏸️ Merging this PR moves nothing — and this line has no release tag yet, so there is no version to move.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headlineOf(tt.in, tt.line); got != tt.want {
				t.Errorf("headlineOf()\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
	// The invariant under every lattice point: an untagged line's headline
	// never spells the null floor, whichever side is pending, on either
	// body, and whether the checkout is shallow.
	levels := []bump.Level{bump.LevelNone, bump.LevelPatch, bump.LevelMinor, bump.LevelMajor}
	for _, shallow := range []bool{false, true} {
		for _, line := range []bool{false, true} {
			for _, pl := range levels {
				for _, ql := range levels {
					in := Input{Current: "curry/v0.0.0", Untagged: true, Shallow: shallow,
						PR: Verdict{Level: pl, Next: "curry/v0.1.0"}, Pending: Verdict{Level: ql, Next: "curry/v0.0.1"}}
					if got := headlineOf(in, line); strings.Contains(got, "v0.0.0") || strings.Contains(got, "→") {
						t.Errorf("an untagged line's headline spells its null floor (shallow=%v line=%v pr=%s pending=%s): %s", shallow, line, pl, ql, got)
					}
				}
			}
		}
	}
}

// TestHeadlineOnAShallowCheckoutNamesNoVersion: on a shallow checkout an
// untagged headline keeps the two sides' levels and names NO version — a
// line with no release tag within the clone's reach may have one past it,
// and every version it could name steps from the null floor that tag
// refutes ("the first release here would be **curry/v0.0.1**" where
// curry/v0.1.0 lay past a --depth 3 clone's reach; review of #361, measured
// 2026-11-08). The absence is the line's on a packages body and the clone's
// on the bare line. Mutation row preview-shallow-line-names-a-first-release.
func TestHeadlineOnAShallowCheckoutNamesNoVersion(t *testing.T) {
	const bare = "no release tag is within this shallow checkout's reach, so the release it lands in cannot be named here."
	const line = "no release tag on this line is within this shallow checkout's reach, so the release it lands in cannot be named here."
	for _, tt := range []struct {
		name    string
		pr, pen Verdict
		line    bool
		want    string
	}{
		{"nothing moves, bare line", Verdict{}, Verdict{}, false, "⏸️ Merging this PR moves nothing — and " + bare},
		{"nothing moves, a line", Verdict{}, Verdict{}, true, "⏸️ Merging this PR moves nothing — and " + line},
		{"PR raises, nothing pending", Verdict{Level: bump.LevelMinor, Next: "v0.1.0"}, Verdict{}, false, "🔼 Merging this PR raises **minor** — " + bare},
		{"PR none over a pending minor", Verdict{}, Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}, true, "⏸️ This PR does not move the version (a **minor** bump is already pending) — " + line},
		{"PR higher than pending", Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}, Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1"}, true, "🔼 Merging this PR raises **minor** (a **patch** bump is already pending) — " + line},
		{"PR lower than pending", Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1"}, Verdict{Level: bump.LevelMinor, Next: "curry/v0.1.0"}, true, "🔧 Merging this PR adds **patch**-level changes (a **minor** bump is already pending) — " + line},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := headlineOf(Input{Current: "curry/v0.0.0", Untagged: true, Shallow: true, PR: tt.pr, Pending: tt.pen}, tt.line)
			if got != tt.want {
				t.Errorf("headlineOf()\n got: %s\nwant: %s", got, tt.want)
			}
			if strings.Contains(got, "v0.") {
				t.Errorf("a shallow headline names a version: %s", got)
			}
		})
	}
}

// TestRenderPackagesUntaggedWalkedLineNamesItsFirstRelease is the body half
// of t-1n1s: a walked line with no tag heads its headline with its first
// release, and the footer still says what was folded in for it.
func TestRenderPackagesUntaggedWalkedLineNamesItsFirstRelease(t *testing.T) {
	got := Render(Input{Participating: 2, Packages: []Package{
		{Path: "camp", Current: "camp/v0.1.0",
			PR:      Verdict{Level: bump.LevelPatch, Next: "camp/v0.1.1", Commits: []Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "pitch the tent"}}},
			Pending: Verdict{Level: bump.LevelNone}},
		{Path: "travel", Current: "travel/v0.0.0", Untagged: true, PendingWalked: true,
			PR:      Verdict{Level: bump.LevelMinor, Next: "travel/v0.1.0", Commits: []Commit{{Sigil: "^", Level: bump.LevelMinor, Subject: "plan a trip"}}},
			Pending: Verdict{Level: bump.LevelNone}},
	}})
	if !strings.Contains(got, "**travel** — 🔼 Merging this PR raises **minor** — the first release here would be **travel/v0.1.0**.\n") {
		t.Errorf("the untagged line must name its first release:\n%s", got)
	}
	if strings.Contains(got, "travel/v0.0.0") {
		t.Errorf("the body spells a null floor as a tag nobody cut:\n%s", got)
	}
	if !strings.Contains(got, "since **camp/v0.1.0** (camp). travel has no release tag on the base branch yet, so everything merged so far is folded in for it. Pushing") {
		t.Errorf("the footer must still say the walked line's floor:\n%s", got)
	}
}

// TestRenderPackagesFooterAgreesInNumber: the footer's untagged sentence is
// one sentence for one line and another for several — "haiku has … for it",
// "haiku and curry have … for them" — on both of its shapes. Joined with
// " and " into the singular sentence, two lines read "haiku and curry has
// no release tag … for it" (t-c2a9, measured 2026-11-04). Mutation row
// preview-footer-disagrees-in-number.
func TestRenderPackagesFooterAgreesInNumber(t *testing.T) {
	line := func(path string, walked bool) Package {
		return Package{Path: path, Current: path + "/v0.0.0", Untagged: true, PendingWalked: walked,
			PR: Verdict{Level: bump.LevelPatch, Next: path + "/v0.0.1", Commits: []Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "touch " + path}}}}
	}
	for _, tc := range []struct {
		paths  []string
		walked bool
		want   string
	}{
		{[]string{"haiku"}, false, " haiku has no release tag on the base branch yet, so nothing merged earlier is folded in for it. Pushing"},
		{[]string{"haiku", "curry"}, false, " haiku and curry have no release tag on the base branch yet, so nothing merged earlier is folded in for them. Pushing"},
		{[]string{"haiku", "curry", "fish"}, false, " haiku, curry and fish have no release tag on the base branch yet, so nothing merged earlier is folded in for them. Pushing"},
		{[]string{"haiku"}, true, " haiku has no release tag on the base branch yet, so everything merged so far is folded in for it. Pushing"},
		{[]string{"haiku", "curry"}, true, " haiku and curry have no release tag on the base branch yet, so everything merged so far is folded in for them. Pushing"},
	} {
		in := Input{Participating: len(tc.paths)}
		for _, p := range tc.paths {
			in.Packages = append(in.Packages, line(p, tc.walked))
		}
		if got := Render(in); !strings.Contains(got, tc.want) {
			t.Errorf("%d line(s), walked=%v: the footer must read\n  %q\ngot:\n%s", len(tc.paths), tc.walked, tc.want, got)
		}
	}
}

// TestRenderShallowFloorGuard: on a shallow checkout whose reach holds no
// tag the floor guard skips the walk (Input.Shallow), and neither body may
// then claim the base branch holds no release tag — the clone cannot know —
// nor name a version off the null floor the clone never confirmed. Both
// carry the walk's shallow clause where a walked shallow checkout puts it,
// qualifying what the headline SAYS rather than a figure it does not carry
// (t-txed; "may be missing from the figure above" under "moves nothing"
// was the review of #361's m1). The clause is the caller's
// (walk.ShallowShortfall); the renderer reads it as any PendingShort.
// Mutation rows preview-shallow-line-names-a-first-release and
// preview-shallow-caveat-qualifies-a-figure-nobody-named.
func TestRenderShallowFloorGuard(t *testing.T) {
	const short = "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)"
	single := Render(Input{Current: "v0.0.0", Untagged: true, Shallow: true, PendingShort: short,
		PR: Verdict{Level: bump.LevelMinor, Next: "v0.1.0", Commits: []Commit{feat()}}})
	for _, want := range []string{
		"\n🔼 Merging this PR raises **minor** — no release tag is within this shallow checkout's reach, so the release it lands in cannot be named here.\n",
		"\n> [!WARNING]\n> The pending side of this fold is INCOMPLETE: " + short + ". Anything already merged but unreleased may be missing from what the headline says, and so may the release tag it would step from, so treat it as a floor rather than the answer.\n",
		"erase them. No v* release tag is within this shallow checkout's reach, so nothing merged earlier is folded in. Pushing",
	} {
		if !strings.Contains(single, want) {
			t.Errorf("the single line's shallow body must carry\n  %q\ngot:\n%s", want, single)
		}
	}
	lines := Render(Input{Participating: 1, Shallow: true, PendingShort: short, Packages: []Package{
		{Path: "haiku", Current: "haiku/v0.0.0", Untagged: true,
			PR: Verdict{Level: bump.LevelMinor, Next: "haiku/v0.1.0", Commits: []Commit{feat()}}},
	}})
	for _, want := range []string{
		"**haiku** — 🔼 Merging this PR raises **minor** — no release tag on this line is within this shallow checkout's reach, so the release it lands in cannot be named here.\n",
		"may be missing from what each headline says, and so may the release tag a line with none within reach would step from, so treat each as a floor rather than the answer.\n",
		"erase them. haiku has no release tag within this shallow checkout's reach, so nothing merged earlier is folded in for it. Pushing",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the packages shallow body must carry\n  %q\ngot:\n%s", want, lines)
		}
	}
	for _, body := range []string{single, lines} {
		if strings.Contains(body, "holds no") || strings.Contains(body, "on the base branch yet") || strings.Contains(body, "v0.1.0") || strings.Contains(body, "figure") {
			t.Errorf("a shallow checkout's body asserts what the base branch holds, names a version off an unconfirmed floor, or qualifies a figure it does not carry:\n%s", body)
		}
	}
}

// TestRenderShallowWalkedLine: the walk ran on a shallow checkout because a
// touched sibling's tag was within reach, and a touched line with none is
// still said to have none within the REACH — its headline names no version,
// its footer sentence says the walk folded what the reach holds — while the
// tagged sibling keeps its figures and the caveat still qualifies what each
// headline says (the t-txed class on the walked arm; review of #361, m2).
func TestRenderShallowWalkedLine(t *testing.T) {
	const short = "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)"
	got := Render(Input{Participating: 2, Shallow: true, PendingShort: short, Packages: []Package{
		{Path: "haiku", Current: "haiku/v0.1.0", PendingWalked: true,
			PR: Verdict{Level: bump.LevelMinor, Next: "haiku/v0.2.0", Commits: []Commit{feat()}}, Pending: Verdict{Level: bump.LevelNone}},
		{Path: "curry", Current: "curry/v0.0.0", Untagged: true, PendingWalked: true,
			PR: Verdict{Level: bump.LevelPatch, Next: "curry/v0.0.1", Commits: []Commit{{Sigil: "~", Level: bump.LevelPatch, Subject: "swap an ingredient"}}}, Pending: Verdict{Level: bump.LevelNone}},
	}})
	for _, want := range []string{
		"**haiku** — 🔼 Merging this PR raises **minor** — the next release becomes **haiku/v0.1.0 → haiku/v0.2.0**.\n",
		"**curry** — 🔧 Merging this PR raises **patch** — no release tag on this line is within this shallow checkout's reach, so the release it lands in cannot be named here.\n",
		"may be missing from what each headline says, and so may the release tag a line with none within reach would step from, so treat each as a floor rather than the answer.\n",
		"since **haiku/v0.1.0** (haiku). curry has no release tag within this shallow checkout's reach, so everything merged within that reach is folded in for it. Pushing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the walked shallow body must carry\n  %q\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "curry/v0.0.1") || strings.Contains(got, "on the base branch yet") {
		t.Errorf("the walked untagged line names a version off an unconfirmed floor, or is called untagged on the base branch:\n%s", got)
	}
}
