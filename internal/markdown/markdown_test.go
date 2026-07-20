package markdown

import "testing"

// TestEscapeMentions pins the mention-neutralizing contract: a would-be
// @mention gains backticks, everything else — emails, pinned refs, existing
// code spans — passes through byte-for-byte. The bare-@v1 case is the real
// incident this package exists for: release notes reading "callers to @v1"
// listed the GitHub user v1 as a release contributor.
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
		{"team mention quotes the org head", "ping @org/team about it", "ping `@org`/team about it"},
		{"already code-quoted stays put", "reference it as `@name` (docs)", "reference it as `@name` (docs)"},
		{"mixed quoted and bare", "`@name` chips that type @name for you", "`@name` chips that type `@name` for you"},
		{"email is not a mention", "reported by dev@example.com", "reported by dev@example.com"},
		{"pinned action ref is not a mention", "bump actions/checkout@v5", "bump actions/checkout@v5"},
		{"after punctuation", "the alias (@name) syntax", "the alias (`@name`) syntax"},
		{"lone at sign", "meet @ noon", "meet @ noon"},
		{"cjk before mention", "対応は@v1へ", "対応は`@v1`へ"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EscapeMentions(c.in); got != c.want {
				t.Errorf("EscapeMentions(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
