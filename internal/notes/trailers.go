package notes

import (
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// Git trailer-block parsing, held to git's own commit reader —
// `git log --format='%(trailers:only,unfold)'` — by a differential asserting
// EQUALITY (trailers_test.go): the trailers glyph reads off a landed commit
// are the ones git reads off it, no more and no fewer. Layer contract: this
// file reads a commit message and nothing else — no git, no GitHub, no
// configuration value. The one thing it takes from internal/config is git's
// token alphabet (config.IsTrailerTokenByte), the home the loader checks a
// declared [[note.trailers]] token against, so the two cannot drift into a
// declaration that can never bind. The message it reads is the one git
// recorded, closing newline included (notes.Commit.Recorded): the
// end-of-message rules below are byte rules, and `Conflicts:\n` closing the
// object is git's ignored list where `Conflicts:` closing the input is an
// empty trailer — a caller that drops the newline changes the answer, and
// nothing here can tell that it did.
//
// The oracle is the commit reader, not `git interpret-trailers --parse`. The
// two share every rule but one: interpret-trailers reads a message being
// written or a patch mail, where a `---` line starts the diff, so it discards
// everything from the first `---` down; the commit reader applies no divider
// (pretty.c sets no_divider for `%(trailers)`, as trailer_iterator_init does
// for every reader inside git), because a landed commit carries no patch.
// glyph reads landed commits, so a `---` line is message text here, whatever
// follows it (DESIGN §3 names the fleet commits whose credit a divider hid).
//
// The rules are git 2.56.0's, transcribed from trailer.c
// (find_end_of_log_message with no_divider, find_trailer_block_start,
// find_separator, trailer_block_get), commit.c (ignored_log_message_bytes)
// and pretty.c (skip_blank_lines), then measured against that git; the shapes
// and their answers are the test's table, and the table is itself re-read off
// git on every run. Git 2.54.0 and 2.55.0 differ from it in one rule, the URL
// rule of splitTrailer (their trailer.c, commit.c, wt-status.c and pretty.c
// diffed against 2.56.0's; older gits were not read). Git's configuration —
// trailer.separators, trailer.<token>.*, core.commentChar — is never read: the
// oracle is git with its defaults, and a repository whose config changes git's
// answer gets the default one.
//
// WHY A PARSER AND NOT A GREP. A `^Co-Authored-By:` grep over the raw message
// answers on three shapes where git answers nothing, and each wrong answer is
// silent: a trailer-shaped line sitting inside a body paragraph, a block with
// no git-generated line holding one prose line, and a block that is the
// message's only paragraph. The first of those puts a named person on a public
// release page as an author of code the commit does not claim.

// gitSpace is git's own isspace set — its ASCII table marks space, tab, CR
// and LF and nothing else (ctype.c), so a line of form feeds is not blank and
// a line opening with `\v` is prose, not a continuation. Go's unicode.IsSpace
// is wider on both counts, and a line glyph read as blank where git reads
// prose would open a paragraph git does not see.
const gitSpace = " \t\r\n"

// gitGeneratedPrefixes are the lines git itself writes into a message,
// byte-exact (trailer.c, git_generated_prefixes): the trailing space is part
// of the prefix, so `Signed-off-by:S` is an ordinary trailer line and lets no
// prose stand. One of them in a block is what lets the block keep prose
// lines: `git commit -s` and `git cherry-pick -x` land their line beside a
// human's paragraph, and git still reads the trailers around it.
var gitGeneratedPrefixes = [...]string{"Signed-off-by: ", "(cherry picked from commit "}

// scissorsLine is git's cut line under its default comment char (wt-status.c,
// cut_line): everything from it down is the diff `git commit -v` showed the
// author, never message.
const scissorsLine = "# ------------------------ >8 ------------------------\n"

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
//   - the message starts at its first non-blank line (skipBlankLines) and
//     ends where its commit reader says it does (logMessageEnd): above the
//     scissors line and above a trailing run of comments, empty lines and an
//     old-style `Conflicts:` list; a `---` line ends nothing;
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
	message = skipBlankLines(message)
	lines := strings.Split(message[:logMessageEnd(message)], "\n")

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

// skipBlankLines is the commit reader's own first step (pretty.c,
// parse_commit_message → skip_blank_lines): the message starts at its first
// line holding a non-space byte, so leading blank lines — which `git commit`
// strips but fast-import, commit-tree and `--cleanup=verbatim` keep — never
// stand as the title paragraph. Without it a message opening with an empty
// line and a trailer-shaped line read that line as a block where git reads
// the title (measured 2026-11-08 by TestTrailersEqualGit).
func skipBlankLines(msg string) string {
	for msg != "" {
		line := msg
		if nl := strings.IndexByte(msg, '\n'); nl >= 0 {
			line = msg[:nl+1]
		}
		if !isBlank(line) {
			break
		}
		msg = msg[len(line):]
	}
	return msg
}

// logMessageEnd is find_end_of_log_message with no_divider set, which leaves
// ignored_log_message_bytes (commit.c) alone: the byte offset where the
// commit reader stops reading message. The scissors line cuts first — when it
// opens the message nothing is message, otherwise the first one following a
// newline. Above that cut, a trailing run of lines is not message either: `#`
// comment lines, EMPTY lines (a line of spaces or a lone CR is not one here —
// the block scan's own blank test absorbs those), the exact line
// `Conflicts:\n` and the tab-indented paths under it. Transcribed quirks, kept
// because the oracle has them: git marks "no run" with offset 0, so a run that
// begins on the message's first line is kept whole; a `Conflicts:` list stays
// open until a line that ends a run closes it, so a path line under a later
// comment still belongs to the run.
func logMessageEnd(msg string) int {
	cutoff := len(msg)
	if strings.HasPrefix(msg, scissorsLine) {
		cutoff = 0
	} else if i := strings.Index(msg, "\n"+scissorsLine); i >= 0 && i+1 < cutoff {
		cutoff = i + 1
	}

	runStart := 0
	inConflicts := false
	for bol := 0; bol < cutoff; {
		next := len(msg)
		if nl := strings.IndexByte(msg[bol:], '\n'); nl >= 0 {
			next = bol + nl + 1
		}
		switch {
		case msg[bol] == '#' || msg[bol] == '\n':
			if runStart == 0 {
				runStart = bol
			}
		case strings.HasPrefix(msg[bol:], "Conflicts:\n"):
			inConflicts = true
			if runStart == 0 {
				runStart = bol
			}
		case inConflicts && msg[bol] == '\t':
		case runStart != 0:
			runStart = 0
			inConflicts = false
		}
		bol = next
	}
	if runStart != 0 {
		return runStart
	}
	return cutoff
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
// one run of ASCII letters, digits and hyphens (config.IsTrailerTokenByte)
// opening the line, then optional spaces or tabs, then the colon; rest is
// everything after the colon, untrimmed (the caller trims once continuations
// are folded). So `Why not: a reason` is prose — the run ends at the space
// and `n` follows, not the colon — as are `Fix#1: x`, `Révisé: x`, and a line
// opening with the colon or with whitespace (no token). `Why : x` is the
// token `Why`. An EMPTY rest parses: git reports `Why:` as a trailer with no
// value, and dropping it is the reader's job, not the parser's.
//
// The URL rule is git 2.56.0's (d122e37976, "trailers: stop recognizing URLs
// as trailers"): a colon that opens `://` straight after the token is no
// separator, so `https://x: y` and a bare `https://github.com/o/r/issues/1`
// are prose — one of them in a block voids it, credits included — while
// `Why ://x`, with a blank before the colon, is still the token `Why`. Git
// 2.54 and 2.55 have no such rule and read those lines as the trailer `https`,
// so under them a Co-authored-by beside a bare URL line is credited; glyph
// follows 2.56, which credits no more than git's current reader and never a
// person GitHub does not (DESIGN §3). This rule is the one place the two gits
// answer differently, so the differential names the version it judges against
// rather than floating with whichever git is on PATH.
func splitTrailer(l string) (token, rest string, ok bool) {
	i := 0
	for i < len(l) && config.IsTrailerTokenByte(l[i]) {
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
