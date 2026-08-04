package cli

import "testing"

// TestFirstLine pins the contract firstLine's comment states and every
// exclusion path relies on (range.go, cmd_lint.go, sincetag.go): the subject
// the participation rules match against is the first line with its CR trimmed.
// A CRLF message — what the GitHub API hands back verbatim, unlike git's own
// cleanup — must not present `subject\r` to the prefix rules.
func TestFirstLine(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"LF subject and body":   {":bug: fix a crash\n\nBody.", ":bug: fix a crash"},
		"CRLF subject and body": {"fixup! :bug: fix a crash\r\nBody.", "fixup! :bug: fix a crash"},
		"lone trailing CR":      {":bug: fix a crash\r", ":bug: fix a crash"},
		"single line":           {":bug: fix a crash", ":bug: fix a crash"},
		"empty message":         {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := firstLine(tc.in); got != tc.want {
				t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
