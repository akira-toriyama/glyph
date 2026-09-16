package config

import (
	"fmt"
	"regexp"
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
// vocabulary here; DESIGN §2 keeps identity in code.

// trailerTokenRE is git's own token shape: no whitespace, and no colon (the
// separator). Measured — `Why not: a reason` does not parse as a trailer at
// all, it voids the whole block, so accepting such a token here would declare
// a name that can never bind.
var trailerTokenRE = regexp.MustCompile(`^[^\s:]+$`)

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
// something else — a built-in or a pattern group. The last is the one that
// matters: validateLineNames computes a union, and a union cannot be allowed
// to grow two meanings for one name.
func buildTrailers(raws []rawTrailer, patterns []Pattern) ([]NoteTrailer, error) {
	out := make([]NoteTrailer, 0, len(raws))
	seen := make(map[string]bool, len(raws))

	for i, rt := range raws {
		token := strings.TrimSpace(rt.Token)
		if token == "" {
			return nil, fmt.Errorf("note.trailers[%d]: token is required — it is the git trailer this entry reads", i)
		}
		if !trailerTokenRE.MatchString(token) {
			return nil, fmt.Errorf("note.trailers[%d]: token %q holds whitespace or a colon, which git does not parse as a trailer token — a line shaped like that voids its whole trailer block, so this entry could never bind", i, token)
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
		for _, b := range LineBuiltins {
			if rt.Name == b {
				return nil, fmt.Errorf("note.trailers[%d]: name %q is a built-in, which outranks it — the trailer would never render", i, rt.Name)
			}
		}
		for _, p := range patterns {
			for _, g := range p.re.SubexpNames() {
				if g != "" && g == rt.Name {
					return nil, fmt.Errorf("note.trailers[%d]: name %q is already captured by a pattern group; $%s would mean two things depending on which commit rendered it", i, rt.Name, rt.Name)
				}
			}
		}

		seen[rt.Name] = true
		out = append(out, NoteTrailer{Token: token, Name: rt.Name})
	}
	return out, nil
}

// nameRE is note.line's placeholder alphabet, without the leading '$'. It is
// derived from varRE rather than restated so the two cannot drift.
var nameRE = regexp.MustCompile(`^` + strings.TrimPrefix(varRE.String(), `\$`) + `$`)
