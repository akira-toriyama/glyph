package markdown

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// WHAT GITHUB ACTUALLY DOES — measured, not reasoned. Re-run this before you
// change a rule in markdown.go. No test in this file can check it: the package
// encodes a model of a renderer that lives on the other side of the network.
//
// Probe (needs `gh auth login`; the context makes the mention filter behave as
// it does on a PR comment in this repository):
//
//	probe() { printf '%s' "$1" \
//	  | jq -Rs '{text: ., mode: "gfm", context: "akira-toriyama/glyph"}' \
//	  | gh api -X POST /markdown --input -; }
//
// A response containing class="user-mention" is a LIVE MENTION: on a PR comment
// that is a notification sent to an uninvolved person, which is the incident
// (t-hykw) this package exists to prevent.
//
// Observed 2026-07-21:
//
//	INPUT                                   RESULT
//	callers to @v1                          LIVE MENTION
//	callers to &#64;v1                      LIVE MENTION   ← entities are inert
//	callers to `@v1`                        safe (<code>)
//	対応は@v1へ                              LIVE MENTION
//	対応は`@v1`へ                            safe (<code>)
//	x `@octocat y                           safe           ← backtick in FRONT
//	x @octocat` y                           safe           ← backtick BEHIND
//	x @octocat- y                           safe           ← hyphen behind
//	x (@octocat) x "@octocat" x -@octocat   LIVE MENTION   ← other punctuation
//	`code`@octocat                          LIVE MENTION   ← span closer does
//	@octocat`code`                          LIVE MENTION      NOT shield it
//	cc @user@octocat now                    LIVE MENTION (@user)
//	`@user`@octocat now                     LIVE MENTION (@octocat!) ← wrapping
//	`@user@octocat` now                     safe              creates one
//	a lone ` in the subject, as `@octocat` asked      safe
//	a lone ` in the subject, as ``@octocat`` asked    safe (<code>)
//	a ` b @octocat c `` d                   LIVE MENTION   ← no span at GitHub
//	`` `a` `b`                              safe; renders <code>a</code> then
//	                                        the LITERAL text `b` — the second
//	                                        span does not form (cmark memo)
//	`` `a` `x @octocat y`                   LIVE MENTION   ← same memo, via a
//	                                                         span glyph trusted
//	fix <http://a/`x> so @octocat can `see` it        LIVE MENTION (known
//	                                        limitation, see the test below)
//
// Two independent facts hold glyph's output safe, and the escaper is built to
// satisfy both: the mention filter skips <code> subtrees, AND it refuses an
// at-sign with a literal backtick glued to it. The second is what covers the
// shapes where GitHub declines to form the span glyph asked for.
// ─────────────────────────────────────────────────────────────────────────────

// TestEscapeMentions pins the mention-neutralizing contract: a would-be
// @mention comes out inside a backtick fence, everything else — emails, pinned
// refs, the author's own code spans — passes through byte-for-byte. The
// bare-@v1 case is the real incident this package exists for: release notes
// reading "callers to @v1" listed the GitHub user v1 as a release contributor.
//
// Every case is also checked for IDEMPOTENCY, because a subject flows through
// two renderers (the release notes and the pr-verdict comment) and nothing
// stops a caller from escaping twice. The fixed point is structural rather than
// lucky: the fence puts a backtick immediately in front of the at-sign, and a
// backtick in front of an at-sign is not a mention (GitHub agrees — see the
// probe table above), so the second pass has nothing left to match.
func TestEscapeMentions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no at sign", "fix a crash when the config is empty", "fix a crash when the config is empty"},
		{"bare ref token", "pin release + update-tap callers to @v1", "pin release + update-tap callers to `@v1`"},
		{"swift macro", "@Observable TreeViewModel + memoized listItems", "`@Observable` TreeViewModel + memoized listItems"},
		{"start of string", "@name aliases for match predicates", "`@name` aliases for match predicates"},
		{"two mentions", "cc @alice and @bob", "cc `@alice` and `@bob`"},
		{"team mention fences the org head", "ping @org/team about it", "ping `@org`/team about it"},
		{"already code-quoted stays put", "reference it as `@name` (docs)", "reference it as `@name` (docs)"},
		{"email is not a mention", "reported by dev@example.com", "reported by dev@example.com"},
		{"pinned action ref is not a mention", "bump actions/checkout@v5", "bump actions/checkout@v5"},
		{"after punctuation", "the alias (@name) syntax", "the alias (`@name`) syntax"},
		{"lone at sign", "meet @ noon", "meet @ noon"},
		{"cjk before mention", "対応は@v1へ", "対応は`@v1`へ"},

		// The fence is longer than every backtick run in the INPUT, so it
		// cannot pair with anything the author wrote. Here the author's own
		// span forces a two-backtick fence for the bare token beside it.
		{"mixed quoted and bare", "`@name` chips that type @name for you", "`@name` chips that type ``@name`` for you"},

		// t-fbg3: a subject carrying an ODD number of stray backticks. A
		// single-backtick fence paired with the author's stray one, fused
		// "in the subject, as" into a code span and pushed the mention back
		// out into prose. A two-backtick fence has no partner to steal.
		{"one stray backtick", "accept a lone ` in the subject, as @octocat asked", "accept a lone ` in the subject, as ``@octocat`` asked"},
		{"three stray backticks", "a ` b ` c ` and @octocat", "a ` b ` c ` and ``@octocat``"},
		{"two stray backticks pair, mention sits outside", "a ` b ` and @octocat", "a ` b ` and ``@octocat``"},
		{"two stray backticks pair around the mention", "a ` @octocat ` and more", "a ` @octocat ` and more"},

		// A backtick already glued to the at-sign is GitHub's own suppressor
		// (probe: "x `@octocat y" renders no link), and inside a prose stretch
		// every backtick is literal text. Fencing these would gain nothing and
		// would cost the fixed point.
		{"stray backtick glued to the mention", "a lone `@octocat here", "a lone `@octocat here"},
		{"unterminated multi-backtick run", "``@octocat is not a span", "``@octocat is not a span"},
		{"escaped backticks open no span but do shield", "\\`@octocat\\`", "\\`@octocat\\`"},

		// A code span's CLOSING backtick is the one backtick that does not
		// shield: after rendering, the at-sign starts a fresh text node with
		// nothing in front of it ("`code`@octocat" is a live mention). The
		// fence goes in, and a space keeps it from fusing with the delimiter.
		{"mention right after a closing span", "`code`@octocat", "`code` ``@octocat``"},
		{"mention right before an opening span", "@octocat`code`", "``@octocat`` `code`"},

		// A backslash escapes nothing about a mention (GitHub renders
		// "\@octocat" as a live link) but it WOULD eat the fence written
		// straight after it, so the same separating space goes in. The fuzz
		// target found this on "\@0 @0", where the escaped fence left the pass
		// re-escaping its own output forever.
		{"escaping backslash cannot eat the fence", "fix \\@octocat now", "fix \\ `@octocat` now"},
		{"an escaped backslash escapes nothing", "fix \\\\@octocat now", "fix \\\\`@octocat` now"},

		// Multi-backtick spans: a run of N opens a span closed by the next run
		// of exactly N.
		{"double-backtick span is a span", "read ``@octocat`` verbatim", "read ``@octocat`` verbatim"},
		{"span containing a backtick", "read `` a ` b `` then @octocat", "read `` a ` b `` then ```@octocat```"},
		{"runs of different lengths do not pair", "``a` @octocat", "``a` ```@octocat```"},

		// Wrapping ENDS a text node, which promotes whatever follows to the
		// start of the next one: fencing only "@user" in "@user@octocat" hands
		// GitHub a live @octocat that the raw text never had. The chain is
		// fenced as one token.
		{"glued at-token chain", "cc @user@octocat now", "cc `@user@octocat` now"},
		{"fediverse handle", "ping @user@mastodon.social daily", "ping `@user@mastodon`.social daily"},

		// A blank line ends the paragraph, so backticks on either side of one
		// never pair and the mention between them is live prose. No caller
		// passes multi-line text today; the pass must not depend on that.
		{"backticks across a blank line shield nothing", "`ping\n\n@octocat`", "`ping\n\n``@octocat`` `"},

		// A trailing hyphen is not part of a GitHub username, and letting the
		// match eat it also ate the delimiter the next mention needs — the
		// second mention then stayed live (fuzz counterexample "@0-@0").
		{"trailing hyphen is not part of the name", "cc @a- and @b-@c", "cc `@a`- and `@b`-`@c`"},
		{"hyphen inside the name is kept", "cc @a-b for review", "cc `@a-b` for review"},

		// cmark-gfm forms only the FIRST of two equal-length spans once some
		// unmatched run has exhausted its closer search, so the second span is
		// literal text at GitHub and the mention inside it is live. glyph
		// reproduces the memo and fences it.
		{"a span the memo dissolves is not trusted", "`` `a` `x @octocat y`", "`` `a` `x ```@octocat``` y`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EscapeMentions(c.in)
			if got != c.want {
				t.Errorf("EscapeMentions(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := EscapeMentions(got); again != got {
				t.Errorf("EscapeMentions is not idempotent: EscapeMentions(%q) = %q", got, again)
			}
		})
	}
}

// TestEscapeMentionsKnownLimitations pins the two shapes glyph knowingly gets
// wrong, so that a future change that fixes them fails here loudly instead of
// slipping past unnoticed, and so nobody re-derives them as new discoveries.
// Both are recorded in the probe table above as live mentions.
//
//  1. codeSpans knows only backticks. An inline construct that legally swallows
//     a backtick FIRST — a URI autolink, or a raw HTML tag with a backtick in a
//     quoted attribute — leaves the next backtick to pair with a later one, and
//     glyph reads the phantom span as inert. Closing this needs a full inline
//     parser (autolinks, raw HTML, entity references, link destinations), which
//     is a different program from a commit-message linter.
//
//  2. An at-sign the AUTHOR wrote as a character reference is invisible here:
//     the source has no "@" for the pattern to find, and GitHub decodes it to
//     one before the mention filter runs. Rewriting it is what this branch's
//     first attempt did in the opposite direction and it is a trap either way;
//     an author who types &#64; into a commit subject is asking for the mention
//     they get.
func TestEscapeMentionsKnownLimitations(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"autolink swallows the opening backtick", "fix <http://a/`x> so @octocat can `see` it"},
		{"author-written character reference", "callers to &#64;v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EscapeMentions(c.in); got != c.in {
				t.Errorf("EscapeMentions(%q) = %q; the limitation moved — re-run the probe and update the docs", c.in, got)
			}
		})
	}
}

// TestCodeSpans pins the span scan on its own, because a mention's fate is
// decided here: a mention glyph believes is inside a code span is left raw.
// The scan reproduces cmark-gfm, which is what GitHub runs — including the
// memoization the CommonMark spec does not mention.
func TestCodeSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // the spans, delimiters included
	}{
		{"none", "no backticks here", nil},
		{"single", "a `b` c", []string{"`b`"}},
		{"two spans", "`a` and `b`", []string{"`a`", "`b`"}},
		{"lone backtick is literal", "a lone ` here", nil},
		{"three backticks close the first pair only", "a ` b ` c ` d", []string{"` b `"}},
		{"double backtick span", "``a``", []string{"``a``"}},
		{"double backtick span holds a single backtick", "`` a ` b ``", []string{"`` a ` b ``"}},
		{"a run closes only on an equal-length run", "``a` b", nil},
		{"a single-backtick span may hold a longer run", "`a``b`", []string{"`a``b`"}},
		{"unmatched opener does not consume the rest", "``a `b` c", []string{"`b`"}},
		{"escaped backtick cannot open", "\\`a` b", nil},
		{"escaped backtick still closes", "`foo\\`bar`", []string{"`foo\\`"}},
		{"escaped backslash leaves the backtick live", "\\\\`a`", []string{"`a`"}},
		{"empty span", "``", nil},
		{"trailing backslash does not run off the end", "a \\", nil},
		{"a span may cross a plain newline", "`a\nb`", []string{"`a\nb`"}},
		{"a span cannot cross a blank line", "`a\n\nb`", nil},
		{"a blank line of spaces still breaks it", "`a\n \t\n b`", nil},
		{"the break bounds the search, not the scan", "`a\n\nb `c` d", []string{"`c`"}},

		// cmark's closer search is memoized: a search that runs off the end of
		// the block records, per run length, where the last run of that length
		// sat, and refuses any later opener whose recorded partner is behind
		// it. So an unmatched run poisons every SECOND span of a given length.
		// GitHub renders "`` `a` `b`" as <code>a</code> followed by the literal
		// text "`b`" — verified, see the probe table.
		{"a spent search blinds the second span of a length", "`` `a` `b`", []string{"`a`"}},
		{"...and only from the second on", "`` `a`", []string{"`a`"}},
		{"...and only for that length", "``` `a` `b` ``c``", []string{"`a`", "``c``"}},
		{"an opener finds its own length across the others", "`` `a` ``b``", []string{"`` `a` ``"}},
		{"no unmatched run, no memo", "`a` `b` `c`", []string{"`a`", "`b`", "`c`"}},
		{"the memo does not cross a paragraph", "`` `a` `b`\n\n`c` `d`", []string{"`a`", "`c`", "`d`"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, sp := range codeSpans(c.in) {
				got = append(got, c.in[sp[0]:sp[1]])
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("codeSpans(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// FuzzEscapeMentionsNeverLeaksAMention holds the invariants that make this
// function a safety boundary rather than a formatting nicety.
//
// Its oracle is deliberately built from scratch out of the MEASURED GitHub
// rules in the probe table, and calls nothing the implementation uses — not
// codeSpans, not the mention pattern. The previous oracle asked codeSpans where
// the code spans were and the production pattern where the mentions were, so it
// could only ever confirm that glyph agreed with itself: it ran 21 million
// executions green over a version that neutralized nothing at all (it rewrote
// the at-sign to &#64;, which GitHub decodes and links).
//
// The three invariants:
//
//	FIXED POINT   — a subject is escaped once by the notes and once by the
//	                pr-verdict comment; the second pass must be a no-op.
//	NO REWRITING  — the input survives as a subsequence of the output. glyph may
//	                only ADD delimiters around the author's bytes. Any scheme
//	                that rewrites the at-sign itself fails here, which is
//	                exactly the regression this fuzz target missed before.
//	NO LIVE TOKEN — every at-sign in the output that GitHub could still link
//	                carries a backtick immediately in front of it, and any run
//	                longer than the author's own is BALANCED by an equal run
//	                behind the token. The first half is the shield (it holds
//	                whether or not GitHub forms the span, which is why it can be
//	                checked without parsing one); the second half is what tells
//	                a fence apart from the closing delimiter of the previous
//	                mention, which does NOT shield, and it is what catches a
//	                fence that fused with an authored run.
//
// The last invariant is asserted under a premise that keeps it local: if no two
// backtick runs in the INPUT have the same length, CommonMark can form no code
// span in it (a span needs an opener and a closer of equal length), so the whole
// input is prose and every mention in it is glyph's to shield — there is no
// author-owned span whose contents the oracle would have to recognize as
// already inert. Roughly every input without a matched pair of backticks
// qualifies, which is most of them and all of the t-fbg3 family.
func FuzzEscapeMentionsNeverLeaksAMention(f *testing.F) {
	f.Add("pin release + update-tap callers to @v1")
	f.Add("accept a lone ` in the subject, as @octocat asked")
	f.Add("a ` b ` c ` and @octocat")
	f.Add("read ``@octocat`` verbatim")
	f.Add("`code`@octocat")
	f.Add("@octocat`code`")
	f.Add("cc @user@octocat now")
	f.Add("\\`@octocat\\`")
	f.Add("mail dev@example.com when actions/checkout@v5 breaks")
	f.Add("対応は@v1へ")
	f.Add("cc @a- and @b-@c")
	f.Add("`ping\n\n@octocat`")
	f.Add("`` `a` `x @octocat y`")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		got := EscapeMentions(s)

		if again := EscapeMentions(got); again != got {
			t.Fatalf("EscapeMentions(%q) = %q, not a fixed point: re-escaping gives %q", s, got, again)
		}
		if !isSubsequence(s, got) {
			t.Fatalf("EscapeMentions(%q) = %q dropped or rewrote author bytes; it may only add delimiters", s, got)
		}
		if repeatsARunLength(s) {
			return // the input may hold a code span; the local rules below cannot judge it
		}
		longest := longestBacktickRun(s)
		for _, at := range linkableAtSigns(got) {
			before, after := backticksBefore(got, at.start), backtickRun(got, at.end)
			if before == 0 {
				t.Fatalf("EscapeMentions(%q) = %q left %q unshielded: GitHub links an at-sign with no backtick in front of it",
					s, got, got[at.start:at.end])
			}
			// A run longer than anything the author wrote is a fence glyph put
			// there. It must be balanced: a fence that fused with an authored
			// run, or a mention standing right after some OTHER mention's
			// closing fence, shows up as a length mismatch — and the latter is
			// a live mention, because a span delimiter does not shield.
			if (before > longest || after > longest) && before != after {
				t.Fatalf("EscapeMentions(%q) = %q fenced %q with %d backticks in front and %d behind; the fence must be balanced",
					s, got, got[at.start:at.end], before, after)
			}
		}
	})
}

// atSign is one candidate mention in a string: the index of the at-sign and the
// index just past the token GitHub would link.
type atSign struct{ start, end int }

// linkableToken re-derives GitHub's mention shape from the probe table instead
// of importing the production pattern, so that a mistake in that pattern shows
// up as a fuzz failure rather than being mirrored by the oracle. Anchored, and
// applied to the text after an at-sign: alphanumerics and hyphens, never ending
// on a hyphen, and a chain of at-tokens counts as one token because wrapping
// only its head promotes the tail to a live mention.
var linkableToken = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*`)

// linkableAtSigns returns every at-sign in s that GitHub's mention filter could
// turn into a link, ignoring the backtick shielding the caller then checks for.
// The scan is byte-wise rather than a single sweep of one pattern, so that a
// mention hidden behind the previous match's delimiter — the "@0-@0" leak —
// cannot escape the oracle the way it escaped the implementation.
func linkableAtSigns(s string) []atSign {
	var out []atSign
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		if i > 0 && isMentionWordByte(s[i-1]) {
			continue // an email or a pinned ref: GitHub does not link it
		}
		if name := linkableToken.FindString(s[i+1:]); name != "" {
			out = append(out, atSign{start: i, end: i + 1 + len(name)})
		}
	}
	return out
}

// isMentionWordByte reports whether b is a byte GitHub treats as part of a word
// in front of an at-sign, which stops the token from being a mention. The
// backtick is NOT in this set: it shields, but the oracle has to see the
// at-sign in order to demand that shield.
func isMentionWordByte(b byte) bool {
	return b == '_' ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9')
}

// backticksBefore returns the length of the run of backticks ending just before
// i.
func backticksBefore(s string, i int) int {
	n := 0
	for i-n > 0 && s[i-n-1] == '`' {
		n++
	}
	return n
}

// repeatsARunLength reports whether s could hold a code span at all: a span
// needs an opening run of backticks and a LATER run of exactly the same length,
// so a string whose runs all differ in length is plain text however many
// backticks it holds — which is the premise the local rules above rest on.
//
// A backslash in front of a backtick disqualifies the whole string instead of
// being modelled. CommonMark is asymmetric there — an escaped backtick shortens
// the run that could OPEN a span but still counts in full for the run that
// CLOSES one — so run lengths alone stop deciding the question. The line below
// has runs of two and of one, all different, and yet GitHub renders "` @0`" as
// a code span, the leading backtick having been escaped out of its own run:
//
//	\`` @0`
//
// This oracle answers "could there be a span" and only has to err towards yes;
// the fuzz target found the false alarm before this line existed.
func repeatsARunLength(s string) bool {
	if strings.Contains(s, `\`+"`") {
		return true
	}
	seen := map[int]bool{}
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		n := backtickRun(s, i)
		if seen[n] {
			return true
		}
		seen[n] = true
		i += n
	}
	return false
}

// isSubsequence reports whether every byte of a appears in b in order — the
// shape of "glyph only inserted delimiters". Deleting or rewriting an author's
// byte fails it, and so does any neutralization that replaces the at-sign.
func isSubsequence(a, b string) bool {
	i := 0
	for j := 0; i < len(a) && j < len(b); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

// TestOracleRejectsTheSchemesThatFailed guards the fuzz oracle itself: an
// oracle that cannot fail is worse than no oracle, and this one replaced a
// version that stayed green through a total regression. Each case is a
// neutralization glyph has actually shipped or nearly shipped, fed to the
// oracle by hand; the oracle must reject all of them.
func TestOracleRejectsTheSchemesThatFailed(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		out    string
		reject string // which invariant must catch it
	}{
		{"the entity rewrite", "callers to @v1", "callers to &#64;v1", "rewriting"},
		{"doing nothing at all", "cc @alice and @bob", "cc @alice and @bob", "unshielded"},
		{"fencing only the head of a chain", "cc @user@octocat now", "cc `@user`@octocat now", "unbalanced"},
		{"a fence fused with an authored run", "a lone `@octocat here", "a lone ```@octocat`` here", "unbalanced"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !isSubsequence(c.in, c.out) {
				if c.reject == "rewriting" {
					return
				}
				t.Fatalf("%q -> %q was rejected as rewriting, expected %s", c.in, c.out, c.reject)
			}
			if repeatsARunLength(c.in) {
				t.Fatalf("the premise excludes %q, so the oracle would never judge this case", c.in)
			}
			longest := longestBacktickRun(c.in)
			for _, at := range linkableAtSigns(c.out) {
				before, after := backticksBefore(c.out, at.start), backtickRun(c.out, at.end)
				if before == 0 && c.reject == "unshielded" {
					return
				}
				if (before > longest || after > longest) && before != after && c.reject == "unbalanced" {
					return
				}
			}
			t.Fatalf("the oracle accepted %q -> %q; it should have rejected it as %s", c.in, c.out, c.reject)
		})
	}
}

// TestEscapeMentionsFencesNoPaddedSpan pins a detail of the fence that would
// bite silently: CommonMark strips ONE leading and ONE trailing space from a
// code span when BOTH are present, so a fence whose content began or ended with
// a space would render one character short of what the author wrote. The token
// is an at-sign followed by alphanumerics and hyphens, so this can never
// happen — the assertion is here to notice if the token shape ever widens.
func TestEscapeMentionsFencesNoPaddedSpan(t *testing.T) {
	for _, in := range []string{
		"cc @alice and @bob", "`code`@octocat", "@octocat`code`", "a ` b ` c ` and @octocat",
		"cc @user@octocat now", "対応は@v1へ", "the alias (@name) syntax",
	} {
		got := EscapeMentions(in)
		for _, sp := range codeSpans(got) {
			span := got[sp[0]:sp[1]]
			if backtickRun(span, 0) <= longestBacktickRun(in) {
				continue // the author's own span; its spaces are the author's business
			}
			content := strings.Trim(span, "`")
			if strings.HasPrefix(content, " ") || strings.HasSuffix(content, " ") {
				t.Errorf("EscapeMentions(%q) produced the space-padded fence %q, which renders one space short", in, span)
			}
		}
	}
}
