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
// The rules are git's own, transcribed from trailer.c (git 2.54.0:
// find_end_of_log_message, find_trailer_block_start, find_separator,
// trailer_block_get) and then measured against that git; the shapes and their
// answers are the test's table. Git's configuration — trailer.separators,
// trailer.<token>.*, core.commentChar — is never read: the oracle is git with
// its defaults, and a repository whose config widens git's answer gets
// glyph's narrower one.
//
// WHY A PARSER AND NOT A GREP. A `^Co-Authored-By:` grep over the raw message
// answers on four shapes where git answers nothing, and each wrong answer is
// silent: a trailer-shaped line sitting inside a body paragraph, a line below
// a `---` divider, a block with no git-generated line holding one prose line,
// and a block that is the message's only paragraph. The first of those puts a
// named person on a public release page as an author of code the commit does
// not claim.

// gitSpace is git's own isspace set — its ASCII table marks space, tab, CR
// and LF and nothing else (ctype.c), so `---\v` is no divider and a line of
// form feeds is not blank. Go's unicode.IsSpace is wider on both counts, and
// wider is the over-reporting direction: a line glyph read as blank where
// git reads prose would open a paragraph git does not see.
const gitSpace = " \t\r\n"

// gitGeneratedPrefixes are the lines git itself writes into a message,
// byte-exact (trailer.c, git_generated_prefixes). One of them in a block is
// what lets the block keep prose lines: `git commit -s` and `git cherry-pick
// -x` land their line beside a human's paragraph, and git still reads the
// trailers around it.
var gitGeneratedPrefixes = [...]string{"Signed-off-by: ", "(cherry picked from commit "}

// trailer is one parsed entry. Token is as written — case is preserved,
// matching is the caller's business — and Value has its continuations folded
// and its surrounding space trimmed.
type trailer struct {
	Token string
	Value string
}

// trailers returns the message's trailer block, in message order, or nil.
//
// The rule, git's:
//   - everything from a divider line down is discarded (isDivider);
//   - the block is the last paragraph, and never the title paragraph — a
//     message of one paragraph has no trailers however trailer-shaped its
//     lines are;
//   - that paragraph is the block when every line in it is a trailer line, a
//     continuation of one, or a `#` comment — ONE prose line voids it —
//     or when it holds a git-generated line (gitGeneratedPrefixes) and its
//     trailer lines are at least a quarter of its lines (trailerBlock);
//   - inside the block, a continuation folds onto the trailer above it with a
//     single space; a prose line, a comment, and a continuation under either
//     are dropped, so a 25% block answers its trailers alone.
func trailers(message string) []trailer {
	lines := strings.Split(message, "\n")
	for i, l := range lines {
		if isDivider(l) {
			lines = lines[:i]
			break
		}
	}

	var out []trailer
	last := -1 // index into out of the trailer a continuation folds onto; -1 under a line that is not one
	for _, l := range trailerBlock(lines) {
		switch {
		case isBlank(l):
			continue // only the block's trailing blank lines reach here
		case isGitSpace(l[0]):
			if last >= 0 {
				out[last].Value += " " + strings.TrimLeft(l, gitSpace)
			}
		default:
			token, rest, ok := splitTrailer(l)
			if !ok {
				last = -1 // a comment, or prose inside a 25% block
				continue
			}
			out = append(out, trailer{Token: token, Value: rest})
			last = len(out) - 1
		}
	}
	for i := range out {
		out[i].Value = strings.Trim(out[i].Value, gitSpace)
	}
	return out
}

// trailerBlock returns the lines git reads as the trailer block — the block's
// own lines plus whatever blank lines close the message — or nil. Transcribed
// from find_trailer_block_start: the scan runs from the last line up to the
// blank line that ends the title paragraph, counting trailer lines, prose
// lines (a continuation under prose counts as prose) and whether a
// git-generated line was seen; comment lines count as nothing. At the first
// blank line past the message's closing blanks the block is accepted when
// a git-generated line was seen and trailers are at least 25% of the counted
// lines, or when every counted line was a trailer; otherwise the message has
// no trailers at all. The title paragraph itself is never reached: with no
// blank line below it the scan has nothing to walk, and the blank that
// closes it is where the scan stops.
func trailerBlock(lines []string) []string {
	endOfTitle := len(lines)
	for i, l := range lines {
		if isBlank(l) {
			endOfTitle = i
			break
		}
	}

	onlySpaces := true
	recognized := false
	trailerLines, nonTrailerLines, possibleContinuation := 0, 0, 0
	for i := len(lines) - 1; i >= endOfTitle; i-- {
		l := lines[i]
		switch {
		case isComment(l):
			nonTrailerLines += possibleContinuation
			possibleContinuation = 0
		case isBlank(l):
			if onlySpaces {
				continue
			}
			nonTrailerLines += possibleContinuation
			if (recognized && trailerLines*3 >= nonTrailerLines) || (trailerLines > 0 && nonTrailerLines == 0) {
				return lines[i+1:]
			}
			return nil
		default:
			onlySpaces = false
			switch {
			case hasGitGeneratedPrefix(l):
				trailerLines++
				possibleContinuation = 0
				recognized = true
			case isTrailerLine(l):
				trailerLines++
				possibleContinuation = 0
			case isGitSpace(l[0]):
				possibleContinuation++
			default:
				nonTrailerLines += 1 + possibleContinuation
				possibleContinuation = 0
			}
		}
	}
	return nil
}

// isDivider is find_end_of_log_message's test: `---` followed by a gitSpace
// byte or by the end of the line. `---x` and `----` are message text; `--- a
// title`, `---\tx` and a CRLF `---\r` all start the patch, and everything
// below one is not message.
func isDivider(l string) bool {
	return strings.HasPrefix(l, "---") && (len(l) == 3 || isGitSpace(l[3]))
}

func isGitSpace(b byte) bool { return strings.IndexByte(gitSpace, b) >= 0 }

func isBlank(l string) bool { return strings.Trim(l, gitSpace) == "" }

// isComment is git's default comment line (core.commentChar is never read).
func isComment(l string) bool { return strings.HasPrefix(l, "#") }

func hasGitGeneratedPrefix(l string) bool {
	for _, p := range gitGeneratedPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func isTrailerLine(l string) bool {
	_, _, ok := splitTrailer(l)
	return ok
}

// splitTrailer is find_separator with git's default separator: the token is
// one run of ASCII letters, digits and hyphens opening the line, then
// optional spaces or tabs, then the colon; rest is everything after the
// colon, untrimmed (the caller trims once continuations are folded). So
// `Why not: a reason` is prose — the run ends at the space and `n` follows,
// not the colon — as are `Fix#1: x`, `Révisé: x`, a line opening with the
// colon or with whitespace (no token), and `https://x: y`, whose first colon
// opens `://` before any whitespace (git's URL guard). `Why : x` is the token
// `Why`. An EMPTY rest parses: git reports `Why:` as a trailer with no value,
// and dropping it is the reader's job, not the parser's.
func splitTrailer(l string) (token, rest string, ok bool) {
	i := 0
	for i < len(l) && isTokenByte(l[i]) {
		i++
	}
	if i == 0 {
		return "", "", false
	}
	j := i
	for j < len(l) && (l[j] == ' ' || l[j] == '\t') {
		j++
	}
	if j >= len(l) || l[j] != ':' {
		return "", "", false
	}
	if j == i && strings.HasPrefix(l[j:], "://") {
		return "", "", false
	}
	return l[:i], l[j+1:], true
}

// isTokenByte is git's isalnum — ASCII only, nothing at or above 0x80 — plus
// the hyphen.
func isTokenByte(b byte) bool {
	return b == '-' || ('0' <= b && b <= '9') || ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

// trailerValue returns the value of the LAST trailer whose token matches,
// case-insensitively, or "".
//
// Last, not first: a trailer block is append-ordered, so a session that
// rewrote its reason leaves the corrected one at the bottom. Case-insensitive
// because git itself is — the fleet's own history writes `Co-authored-by:`
// and `Co-Authored-By:` for the same trailer.
func trailerValue(message, token string) string {
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

// coAuthorNames returns the display names credited by the message's
// `Co-authored-by` trailers, in message order, without addresses.
//
// A credit is the trailer value with its `<address>` removed. A credit that
// leaves no name behind is DROPPED rather than rendered as its address or as
// a blank: a release line naming an empty person, or publishing an address
// the commit put in angle brackets, are both worse than one missing credit.
//
// Names are returned verbatim for the renderer to fence. A display name is
// never a login — a co-author address establishes no identity glyph can
// verify — so nothing here may ever become an @mention (DESIGN §3, "The
// trailer block", t-39fy).
func coAuthorNames(message string) []string {
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
