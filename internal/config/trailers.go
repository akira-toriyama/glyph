package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// [[note.trailers]] — the repository's own trailer vocabulary.
//
// THE SPLIT THIS ENCODES. A built-in ($author, $pr, $hash, $coauthors) is a
// fact glyph knows how to establish for any repository: its token is a
// git-wide convention and its value has a structure glyph parses. A
// [[note.trailers]] entry is a word THIS repository decided to write down —
// `Why`, `Ref`, `Escape` — whose value glyph cannot interpret and therefore
// renders as prose, fenced, like any other text a commit author typed.
//
// The consequence is the property worth keeping: a config-named trailer can
// never become an @mention, so whether glyph pages a stranger is not a key a
// repository sets. CLAUDE.md's "treat glyph.toml as the grammar" puts the
// vocabulary here; DESIGN §3, "The trailer block", keeps identity in code.

// IsTrailerTokenByte is git's trailer-token alphabet, and its one home:
// find_separator (trailer.c) reads a token as one run of isalnum bytes and
// hyphens — ASCII only, nothing at or above 0x80 — then optional blanks and
// the colon. buildTrailers checks a declared token against it and
// internal/notes' block scan reads a line's token with it, so a token the
// loader admits is one the scan can read. Measured with git's commit reader
// (`git log --format=%(trailers)`, git 2.54.0 and 2.56.0; TestTrailers'
// token rows re-read each answer off git on every run): `Fix-1:` and `123:`
// open trailers, while `Fix#1:`, `Révisé:`, `Why_not:`, `Ref.:` and
// `Why not:` open prose.
func IsTrailerTokenByte(b byte) bool {
	return b == '-' || ('0' <= b && b <= '9') || ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

// isTrailerToken reports whether s is a whole trailer token: one or more
// IsTrailerTokenByte bytes and nothing else.
func isTrailerToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !IsTrailerTokenByte(s[i]) {
			return false
		}
	}
	return true
}

// rawTrailer is the decode shape of one [[note.trailers]] entry.
type rawTrailer struct {
	Token string `toml:"token"`
	Name  string `toml:"name"`
}

// NoteTrailer binds a git trailer token to a note.line placeholder name.
type NoteTrailer struct {
	Token string
	Name  string
}

// buildTrailers validates the declared trailers and returns them in file
// order. It refuses rather than repairs, on the same four grounds the rest of
// this file refuses: an empty or malformed token, a name outside note.line's
// own placeholder alphabet, a duplicate name, and a name that already means
// something else — a built-in, a group some commit binds, or FallbackGroup.
// The last two are the ones that matter: validateLineNames computes a union,
// and a union cannot be allowed to grow two meanings for one name. They read
// the set validateLineNames reads (boundGroupNames), so a group only a skip
// or unlandable pattern captures, which binds nothing, leaves the name free,
// and the fallback's name is taken whatever the patterns call their groups:
// a trailer outranks a group at render, so one named `subject` replaced every
// fallback line's text with its own value — empty on a bot commit, at exit 0
// (measured, t-f2cb).
func buildTrailers(raws []rawTrailer, patterns []Pattern) ([]NoteTrailer, error) {
	out := make([]NoteTrailer, 0, len(raws))
	seen := make(map[string]bool, len(raws))
	groups := boundGroupNames(patterns)

	for i, rt := range raws {
		token := strings.TrimSpace(rt.Token)
		if token == "" {
			return nil, fmt.Errorf("note.trailers[%d]: token is required — it is the git trailer this entry reads", i)
		}
		if !isTrailerToken(token) {
			return nil, fmt.Errorf("note.trailers[%d]: token %q holds a byte outside ASCII letters, digits and hyphens — the only bytes git reads in a trailer token, so no trailer git reads is named that and this entry could never bind", i, token)
		}
		if rt.Name == "" {
			return nil, fmt.Errorf("note.trailers[%d]: name is required — it is the $placeholder note.line writes", i)
		}
		if !nameRE.MatchString(rt.Name) {
			return nil, fmt.Errorf("note.trailers[%d]: name %q is outside note.line's placeholder alphabet (%s), so no template could ever write it", i, rt.Name, nameRE)
		}
		if seen[rt.Name] {
			return nil, fmt.Errorf("note.trailers[%d]: name %q is declared twice; one placeholder cannot read two trailers", i, rt.Name)
		}
		if slices.Contains(lineBuiltins, rt.Name) {
			return nil, fmt.Errorf("note.trailers[%d]: name %q is a built-in; a trailer of that name would replace it on every line, so no template could render the built-in", i, rt.Name)
		}
		if groups[rt.Name] {
			return nil, fmt.Errorf("note.trailers[%d]: name %q is already captured by a pattern group; $%s would mean two things depending on which commit rendered it", i, rt.Name, rt.Name)
		}
		if rt.Name == FallbackGroup {
			return nil, fmt.Errorf("note.trailers[%d]: name %q is the one the raw-line fallback binds for a commit no pattern claims, and a trailer outranks it — $%s would mean two things depending on which commit rendered it, and every such line would render the trailer, empty where none is written, in place of its text", i, rt.Name, rt.Name)
		}

		seen[rt.Name] = true
		out = append(out, NoteTrailer{Token: token, Name: rt.Name})
	}
	return out, nil
}

// nameRE is note.line's placeholder alphabet, without the leading '$'. It is
// derived from varRE rather than restated so the two cannot drift.
var nameRE = regexp.MustCompile(`^` + strings.TrimPrefix(varRE.String(), `\$`) + `$`)
