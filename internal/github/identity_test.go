package github

import "testing"

// TestLoginFromNoreply pins the one address that names a login: GitHub's own
// noreply, in both of its forms, and nothing else — a personal address names
// nobody even when its local part is handle-shaped, because "saleh@lr0.org"
// says nothing about github.com/saleh (mutation row
// noreply-address-carries-no-identity).
func TestLoginFromNoreply(t *testing.T) {
	for _, tc := range []struct{ email, want string }{
		{"92862731+akira-toriyama@users.noreply.github.com", "akira-toriyama"},
		{"akira-toriyama@users.noreply.github.com", "akira-toriyama"},
		{"49699333+dependabot[bot]@users.noreply.github.com", "dependabot[bot]"},
		{"root@lr0.org", ""},
		{"saleh@example.com", ""},
		{"akira-toriyama@users.noreply.github.com.evil.example", ""},
		{"x@users.noreply.github.com ", ""},
		{"", ""},
	} {
		if got := LoginFromNoreply(tc.email); got != tc.want {
			t.Errorf("LoginFromNoreply(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
}
