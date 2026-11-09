package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// TestRepositoryDecodesTheSettingsDoctorReads pins the wire→domain mapping,
// including the absence encoding: a field GitHub omits must arrive as nil (or
// ""), never as a zero value a caller would read as "the setting is off".
func TestRepositoryDecodesTheSettingsDoctorReads(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/akira-toriyama/glyph" {
			t.Errorf("path = %q, want the repository object path", got)
		}
		fmt.Fprint(w, `{"full_name":"akira-toriyama/glyph","private":false,"visibility":"public",
		  "allow_squash_merge":true,"allow_merge_commit":false,
		  "squash_merge_commit_title":"COMMIT_OR_PR_TITLE",
		  "permissions":{"admin":true,"pull":true}}`)
	})

	repo, err := c.Repository(context.Background(), "akira-toriyama", "glyph")
	if err != nil {
		t.Fatalf("Repository: %v", err)
	}
	if repo.Visibility != "public" {
		t.Errorf("visibility = %q, want public", repo.Visibility)
	}
	if repo.AllowSquashMerge == nil || !*repo.AllowSquashMerge {
		t.Errorf("allow_squash_merge = %v, want true", repo.AllowSquashMerge)
	}
	if repo.AllowMergeCommit == nil || *repo.AllowMergeCommit {
		t.Errorf("allow_merge_commit = %v, want false", repo.AllowMergeCommit)
	}
	// The omitted field is the point: nil is "GitHub did not say", which the
	// caller reports as could-not-run rather than as a setting that is off.
	if repo.AllowRebaseMerge != nil {
		t.Errorf("an OMITTED allow_rebase_merge decoded to %v, want nil — silence is not false", *repo.AllowRebaseMerge)
	}
	if repo.SquashMergeCommitMessage != "" {
		t.Errorf("an omitted squash_merge_commit_message decoded to %q, want empty", repo.SquashMergeCommitMessage)
	}
	if repo.Permissions == nil || !repo.Permissions.Admin || repo.Permissions.Push {
		t.Errorf("permissions = %+v, want the API's own block verbatim", repo.Permissions)
	}
}

// TestRepositoryClassifiesAnAnswerApartFromNoAnswer is the exit-code contract
// `glyph doctor` rests on, pinned at the source. A 401, a 403 that is no rate
// limit and a 404 are answers about this credential, and doctor exits 3 on them.
// Everything else is a failure to obtain any answer at all, and must NOT be
// branchable as a repository verdict: a 403 rate limit and a 5xx that outlived
// the retries once exited 3, telling a CI wrapper that a healthy repository was
// misconfigured (and never to retry it).
//
// The two 403s that matter have the wire shapes api.github.com answered on
// 2026-11-04: the primary limit carried X-RateLimit-Remaining: 0 with no
// Retry-After, the permission refusal a nonzero count. Each signal of a rate
// limit gets a row whose other two are silent, so losing one fails its own row.
func TestRepositoryClassifiesAnAnswerApartFromNoAnswer(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		headers map[string]string
		want    bool
	}{
		{"repository not found for this credential", http.StatusNotFound, `{"message":"Not Found"}`, nil, true},
		{"rejected credential", http.StatusUnauthorized, `{"message":"Bad credentials"}`, nil, true},
		{"permission refused", http.StatusForbidden, `{"message":"Must have admin rights to Repository."}`,
			map[string]string{"X-RateLimit-Remaining": "4953"}, true},
		{"SSO refused", http.StatusForbidden, `{"message":"Resource protected by organization SAML enforcement. You must grant your OAuth token access to this organization."}`, nil, true},
		{"primary rate limit as api.github.com answers it", http.StatusForbidden, `{"message":"API rate limit exceeded for 203.0.113.7."}`,
			map[string]string{"X-RateLimit-Remaining": "0"}, false},
		{"a rate limit named only by the remaining count", http.StatusForbidden, `{"message":"Forbidden"}`,
			map[string]string{"X-RateLimit-Remaining": "0"}, false},
		{"a rate limit named only by Retry-After", http.StatusForbidden, `{"message":"Forbidden"}`,
			map[string]string{"Retry-After": "30"}, false},
		{"a secondary rate limit named only by its message", http.StatusForbidden, `{"message":"You have exceeded a secondary rate limit."}`, nil, false},
		{"rate limit at 429", http.StatusTooManyRequests, `{"message":"You have exceeded a secondary rate limit"}`, nil, false},
		{"outage", http.StatusServiceUnavailable, `{"message":"Service Unavailable"}`, nil, false},
		{"bad gateway", http.StatusBadGateway, `{"message":"Server Error"}`, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})

			_, err := c.Repository(context.Background(), "akira-toriyama", "glyph")
			if got := IsRepoRefused(err); got != tt.want {
				t.Fatalf("IsRepoRefused(%d) = %t, want %t — err was %v", tt.status, got, tt.want, err)
			}
			// Whatever the status, it stays an ordinary CodeAPI failure to
			// every layer that does not branch on the predicate.
			wantAPIError(t, err, "GET /repos/akira-toriyama/glyph")
		})
	}
}

// TestRepositoryStatusDoesNotLeakIntoTheCommitBranch: each unflattened method
// is read through its own predicate — IsRepoRefused for the repository read,
// IsCommitUnknown for the two commit endpoints. A failure from the repository
// read must never read as "GitHub does not know that commit yet", which the
// release walk treats as API lag and silently falls back on.
func TestRepositoryStatusDoesNotLeakIntoTheCommitBranch(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	_, err := c.Repository(context.Background(), "akira-toriyama", "glyph")
	if IsCommitUnknown(err) {
		t.Fatal("a repository-read failure must not report IsCommitUnknown")
	}
	if !IsRepoRefused(err) {
		t.Fatal("a 404 from the repository read must report IsRepoRefused")
	}
}
