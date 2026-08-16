// Package bump owns glyph's version semantics: the bump lattice, folding a
// set of levels (max — order-independent, so squash order can never change
// the version), stepping a version, and the sigil fold that maps a commit
// range onto the lattice under a v2 config. It is pure — no I/O.
package bump

// Level is a rung on the semver lattice none < patch < minor < major. It is
// a string so it round-trips through JSON surfaces as a human-readable token.
// It lives here — the package that owns version semantics — since v2 removed
// the embedded rules table that used to carry it.
type Level string

const (
	LevelNone  Level = "none"  // never moves the version
	LevelPatch Level = "patch" // a shipped, user-observable change
	LevelMinor Level = "minor" // a new feature
	LevelMajor Level = "major" // a breaking change
)

// Reduce folds levels with max over the lattice. The fold is
// order-independent and idempotent (fuzz-pinned); an empty input is none —
// no release.
func Reduce(levels []Level) Level {
	top := LevelNone
	for _, l := range levels {
		if l.Rank() > top.Rank() {
			top = l
		}
	}
	return top
}

// Valid reports whether l is one of the four defined rungs.
func (l Level) Valid() bool { return l.Rank() >= 0 }

// Rank projects a Level onto the lattice ordering (0..3); an unknown level
// is -1. The max-fold compares ranks, so the fold is order-independent and
// idempotent.
func (l Level) Rank() int {
	switch l {
	case LevelNone:
		return 0
	case LevelPatch:
		return 1
	case LevelMinor:
		return 2
	case LevelMajor:
		return 3
	default:
		return -1
	}
}
