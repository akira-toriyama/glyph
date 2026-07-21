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
	if repo.FullName != "akira-toriyama/glyph" || repo.Visibility != "public" {
		t.Errorf("identity = %q/%q, want the full name and visibility", repo.FullName, repo.Visibility)
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
// `glyph doctor` rests on, pinned at the source. GitHub answering 404 is an
// answer ABOUT the repository — there is none for this credential — and doctor
// exits 3 on it. Everything else is a failure to obtain any answer at all, and
// must NOT be branchable as a repository verdict: a 403 rate limit and a 5xx
// that outlived the retries once exited 3, telling a CI wrapper that a healthy
// repository was misconfigured (and never to retry it).
func TestRepositoryClassifiesAnAnswerApartFromNoAnswer(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"repository not found for this credential", http.StatusNotFound, `{"message":"Not Found"}`, true},
		{"primary rate limit", http.StatusForbidden, `{"message":"API rate limit exceeded"}`, false},
		{"secondary rate limit", http.StatusTooManyRequests, `{"message":"You have exceeded a secondary rate limit"}`, false},
		{"outage", http.StatusServiceUnavailable, `{"message":"Service Unavailable"}`, false},
		{"rejected credential", http.StatusUnauthorized, `{"message":"Bad credentials"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})

			_, err := c.Repository(context.Background(), "akira-toriyama", "glyph")
			if got := IsRepoUnknown(err); got != tt.want {
				t.Fatalf("IsRepoUnknown(%d) = %t, want %t — err was %v", tt.status, got, tt.want, err)
			}
			// Whatever the status, it stays an ordinary CodeAPI failure to
			// every layer that does not branch on the predicate.
			wantAPIError(t, err, "GET /repos/akira-toriyama/glyph")
		})
	}
}

// TestRepositoryStatusDoesNotLeakIntoTheCommitBranch: the two unflattened
// methods each own exactly one predicate. A 422 from the repository read must
// never read as "GitHub does not know that commit yet", which the release walk
// treats as API lag and silently falls back on.
func TestRepositoryStatusDoesNotLeakIntoTheCommitBranch(t *testing.T) {
	c := newClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	_, err := c.Repository(context.Background(), "akira-toriyama", "glyph")
	if IsCommitUnknown(err) {
		t.Fatal("a repository-read failure must not report IsCommitUnknown")
	}
	if !IsRepoUnknown(err) {
		t.Fatal("a 404 from the repository read must report IsRepoUnknown")
	}
}
