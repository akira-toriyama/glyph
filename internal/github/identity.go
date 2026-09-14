package github

import "regexp"

// noreply is GitHub's own commit address for an account that keeps its email
// private — <id>+<login>@users.noreply.github.com today, <login>@ before
// 2017 — the one place a login is written into a commit itself, so a commit
// git holds (the fallback arm, --range) names its account without a request.
// Anchored to the whole address and to that host only: any other address
// names nobody.
var noreply = regexp.MustCompile(`^(?:[0-9]+\+)?([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\[bot\])?)@users\.noreply\.github\.com$`)

// LoginFromNoreply returns the login a GitHub noreply author address carries,
// "" for any other address. It is the identity of last resort for a commit
// the API never described: a display name is NOT a login (t-39fy — "Saleh"
// is github.com/larrasket, and @Saleh is a stranger), so a commit whose
// address names nobody is credited by name and pages no one.
func LoginFromNoreply(email string) string {
	m := noreply.FindStringSubmatch(email)
	if m == nil {
		return ""
	}
	return m[1]
}
