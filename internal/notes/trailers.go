package notes

import "strings"

// Git trailer-block parsing, held to `git interpret-trailers --parse` by a
// differential (trailers_test.go). Layer contract: this file reads a commit
// message and nothing else — no git, no GitHub, no config. The one invariant
// the differential asserts is a SUBSET, not equality: glyph may report FEWER
// trailers than git, never more. Over-reporting is the direction that credits
// a person the commit never claimed, so an edge this parser is unsure of is
// resolved by reporting nothing.
//
// Every rule below was measured against git 2.54.0 rather than read from the
// documentation; the shapes and their answers are the test's table.
//
// WHY A PARSER AND NOT A GREP. A `^Co-Authored-By:` grep over the raw message
// answers on four shapes where git answers nothing, and each wrong answer is
// silent: a trailer-shaped line sitting inside a body paragraph, a line below
// a `---` divider, a block holding one prose line, and a block that is the
// message's only paragraph. The first of those puts a named person on a
// public release page as an author of code the commit does not claim.

// divider is the line git treats as the start of a patch: everything from it
// down is not message. A `---` ABOVE the trailer block therefore hides the
// whole block (measured: 0 trailers), which is why truncation comes first.
const divider = "---"

// trailer is one parsed entry. Token is as written — case is preserved,
// matching is the caller's business — and Value has its continuations folded
// and its surrounding space trimmed.
type trailer struct {
	Token string
	Value string
}

// trailers returns the message's trailer block, in message order, or nil.
//
// The rule, measured:
//   - everything from a bare `---` line down is discarded;
//   - the block is the LAST paragraph, and never the only one — a message of
//     one paragraph has no trailers however trailer-shaped its lines are;
//   - every line in it must be trailer-shaped, a continuation (leading space
//     or tab), or a `#` comment. ONE prose line disqualifies the whole block,
//     comment lines included in neither direction;
//   - a continuation folds onto the trailer above it with a single space, and
//     a continuation with nothing above it disqualifies the block.
func trailers(message string) []trailer {
	lines := strings.Split(message, "\n")
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == divider {
			lines = lines[:i]
			break
		}
	}

	block, ok := lastParagraph(lines)
	if !ok {
		return nil
	}

	var out []trailer
	for _, l := range block {
		switch {
		case strings.HasPrefix(l, "#"):
			continue
		case isContinuation(l):
			if len(out) == 0 {
				return nil // nothing to continue: not a trailer block
			}
			out[len(out)-1].Value = strings.TrimSpace(out[len(out)-1].Value + " " + strings.TrimSpace(l))
		default:
			token, value, ok := splitTrailer(l)
			if !ok {
				return nil // one prose line voids the block
			}
			out = append(out, trailer{Token: token, Value: value})
		}
	}
	return out
}

// lastParagraph returns the final run of non-blank lines, and whether the
// message had a paragraph BEFORE it. The second answer is the subject rule:
// git reads no trailers out of a message that is all subject (measured — both
// `Why: a reason` alone and a subject with a trailer glued under it answer 0).
func lastParagraph(lines []string) ([]string, bool) {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 {
		return nil, false
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	if start == 0 {
		return nil, false // the block would be the message's first paragraph
	}
	return lines[start:end], true
}

// isContinuation reports whether the line continues the trailer above it.
// Leading space or tab is the whole rule (measured: both fold, with a single
// space). A blank line never reaches here — it ends the paragraph.
func isContinuation(l string) bool {
	return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
}

// splitTrailer parses `token: value`. The token must be non-empty and hold no
// whitespace — measured, `Why not: a reason` voids the block rather than
// parsing as a token — and `:` is the only separator git accepts without
// configuration (`Why = a reason` voids it too). An EMPTY value parses: git
// reports `Why:` as a trailer with no value, and dropping it is the reader's
// job, not the parser's.
func splitTrailer(l string) (token, value string, ok bool) {
	i := strings.IndexByte(l, ':')
	if i <= 0 {
		return "", "", false
	}
	token = l[:i]
	if strings.ContainsAny(token, " \t") {
		return "", "", false
	}
	return token, strings.TrimSpace(l[i+1:]), true
}

// TrailerValue returns the value of the LAST trailer whose token matches,
// case-insensitively, or "".
//
// Last, not first: a trailer block is append-ordered, so a session that
// rewrote its reason leaves the corrected one at the bottom. Case-insensitive
// because git itself is — the fleet's own history writes `Co-authored-by:`
// and `Co-Authored-By:` for the same trailer.
func TrailerValue(message, token string) string {
	var v string
	for _, t := range trailers(message) {
		if strings.EqualFold(t.Token, token) {
			v = t.Value
		}
	}
	return v
}

// coAuthoredBy is git's own token for the convention. It is a constant rather
// than a configurable name because the token is a git-wide convention with a
// structured value, which is what separates a built-in from the repository
// vocabulary `[[note.trailers]]` names.
const coAuthoredBy = "Co-authored-by"

// CoAuthorNames returns the display names credited by the message's
// `Co-authored-by` trailers, in message order, without addresses.
//
// A credit is the trailer value with its `<address>` removed. A credit that
// leaves no name behind is DROPPED rather than rendered as its address or as
// a blank: a release line naming an empty person, or publishing an address
// the commit put in angle brackets, are both worse than one missing credit.
//
// Names are returned verbatim for the renderer to fence. A display name is
// never a login — a co-author address establishes no identity glyph can
// verify — so nothing here may ever become an @mention (DESIGN §2, t-39fy).
func CoAuthorNames(message string) []string {
	var out []string
	for _, t := range trailers(message) {
		if !strings.EqualFold(t.Token, coAuthoredBy) {
			continue
		}
		if name := strings.TrimSpace(stripAddress(t.Value)); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// stripAddress removes a trailing `<…>` from a credit. Only a trailing one:
// a name may legitimately hold angle brackets, and the address is the last
// field by the convention's own shape.
func stripAddress(v string) string {
	i := strings.LastIndexByte(v, '<')
	if i < 0 || !strings.HasSuffix(strings.TrimSpace(v), ">") {
		return v
	}
	return v[:i]
}
