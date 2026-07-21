package github

// This file is the repository-object read `glyph doctor` diagnoses a repository
// through — the merge settings glyph's squash-safe machinery depends on but can
// never observe from the inside. Same contract as the rest of the adapter: bytes
// move, failures classify at the source, and no policy lives here. Which setting
// is a defect, which is a house preference, and what breaks when it drifts is the
// caller's (internal/doctor) decision.

import (
	"context"
	"fmt"
	"net/url"
)

// Repo is the slice of GET /repos/{owner}/{repo} doctor reads: the three merge
// methods, the squash subject/body policy, and what the API itself says this
// credential may do here.
//
// The merge-method flags are *bool, NOT bool, deliberately. GitHub omits them
// entirely from the response given to a credential that cannot see a
// repository's settings, and a plain bool would decode that silence as false —
// reporting "squash merging is disabled" about a repository that has it on.
// A confidently wrong answer is worse than no answer for a diagnostic tool, so
// nil means "the API did not say" and every check reading one degrades to
// could-not-run. The two squash enums encode the same absence as "".
type Repo struct {
	FullName   string
	Private    bool
	Visibility string

	AllowMergeCommit *bool
	AllowRebaseMerge *bool
	AllowSquashMerge *bool

	SquashMergeCommitTitle   string
	SquashMergeCommitMessage string

	// Permissions is the API's OWN statement about this credential's access,
	// or nil when it made none (an anonymous read of a public repository
	// reports no permissions object). glyph never infers a scope the API did
	// not report — see the token check in internal/doctor.
	Permissions *RepoPermissions
}

// RepoPermissions is the repository object's permissions block: the five
// access levels GitHub reports for the authenticated credential.
type RepoPermissions struct {
	Admin    bool `json:"admin"`
	Maintain bool `json:"maintain"`
	Push     bool `json:"push"`
	Triage   bool `json:"triage"`
	Pull     bool `json:"pull"`
}

// apiRepo is the wire shape; only the fields doctor reads are decoded. It is
// field-for-field convertible to Repo (the tagless domain mirror), the same
// pairing apiPull/PullRef and apiRelease/Release use — a future Repo-only
// field turns the conversion below into a compile error rather than a silent
// drop.
type apiRepo struct {
	FullName   string `json:"full_name"`
	Private    bool   `json:"private"`
	Visibility string `json:"visibility"`

	AllowMergeCommit *bool `json:"allow_merge_commit"`
	AllowRebaseMerge *bool `json:"allow_rebase_merge"`
	AllowSquashMerge *bool `json:"allow_squash_merge"`

	SquashMergeCommitTitle   string `json:"squash_merge_commit_title"`
	SquashMergeCommitMessage string `json:"squash_merge_commit_message"`

	Permissions *RepoPermissions `json:"permissions"`
}

// Repository reads the repository object (GET /repos/{owner}/{repo}) — one
// request, no pagination. It is also the token probe doctor reports, and it is
// the second method (with CommitPulls) that does NOT flatten its failure: the
// status is the only thing that separates "GitHub answered, and the answer is
// that this credential has no such repository" from "GitHub never answered at
// all", and doctor turns that distinction into two different exit codes. See
// IsRepoUnknown, which is the only sanctioned reader of it — flattening here
// made a transient 503 report as a repository defect.
func (c *Client) Repository(ctx context.Context, owner, repo string) (Repo, error) {
	u := fmt.Sprintf("%s/repos/%s/%s", c.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	var raw apiRepo
	if _, err := c.get(ctx, u, &raw); err != nil {
		return Repo{}, err
	}
	return Repo(raw), nil
}
