// Package markdown holds the escaping rules for author-supplied text that
// glyph embeds into GitHub-rendered Markdown (release-notes lines, the
// pr-verdict table). It is pure — no I/O, no globals — and deliberately tiny:
// each function neutralizes one specific GitHub rendering behavior that turns
// honest commit text into something it never meant to be.
package markdown

import (
	"regexp"
	"strings"
)

// codeSpan matches an inline code span. Text inside one is already
// mention-inert — GitHub does not link @names inside backticks — so it must
// pass through untouched (re-wrapping would nest backticks and break the span).
var codeSpan = regexp.MustCompile("`[^`]*`")

// mention matches a token GitHub would link as a user mention: an @ followed
// by a username-shaped word (alphanumeric and hyphens), where the @ is not
// glued to a preceding word character — that glued shape (user@host) is an
// email or a pinned ref like checkout@v4, which GitHub leaves alone already.
var mention = regexp.MustCompile("(^|[^A-Za-z0-9_`])(@[A-Za-z0-9][A-Za-z0-9-]*)")

// EscapeMentions code-quotes would-be @mentions in s: `@v1`, `@Observable`,
// `@name` stay readable but stop resolving to real GitHub users. Left raw,
// such a token in a release body silently lists a stranger under the
// release's Contributors, and in a PR comment it NOTIFIES them. Existing code
// spans are preserved verbatim; a team mention (@org/team) is neutralized by
// quoting its @org head. Only would-be mentions gain backticks — an @ glued
// to a word character (an email, a path@ref) is not a mention and stays raw.
func EscapeMentions(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	var b strings.Builder
	last := 0
	for _, span := range codeSpan.FindAllStringIndex(s, -1) {
		b.WriteString(quoteMentions(s[last:span[0]]))
		b.WriteString(s[span[0]:span[1]])
		last = span[1]
	}
	b.WriteString(quoteMentions(s[last:]))
	return b.String()
}

func quoteMentions(s string) string {
	return mention.ReplaceAllString(s, "$1`$2`")
}
