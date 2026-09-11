package github

import (
	"context"
	"fmt"
	"net/url"

	"github.com/akira-toriyama/glyph/v3/internal/core"
)

// CommitFilesCap is where GitHub stops listing a commit's files however far
// the pagination follows — an API contract, so the adapter owns the number.
// Measured 2026-09-10 on torvalds/linux's root commit (17k files): 300 per
// page by default, rel="last" at page 10, page 11 empty. A listing of exactly
// this many files may be missing some, and a package touched only past the
// cap is unreachable, not absent — the caller records that as an incomplete
// walk (DESIGN §4.1).
const CommitFilesCap = 3000

// apiCommitFiles is the slice of GET commits/{sha} the attribution reads: the
// files array, each entry under its current name and — for a rename — the
// name it left. Every page of the listing carries the whole commit object
// with a different slice of files, so pages are decoded as this shape and
// their files concatenated.
type apiCommitFiles struct {
	Files []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
	} `json:"files"`
}

// CommitFiles returns the paths a commit's own diff touches
// (GET /repos/{owner}/{repo}/commits/{sha}, the files array, following the
// Link header across the listing's pages), a renamed file under both its
// names, and whether the listing reached CommitFilesCap. It answers for a sha
// no branch holds — a squash-merged pull's inner commits (measured 2026-09-10
// on glyph-test #83) — which is what the release walk needs it for; a commit
// the checkout holds is asked of local git instead (gitsource.DiffTreeFiles),
// free.
//
// The status is not flattened: a 422 here means what it means on
// commits/{sha}/pulls — GitHub does not know the sha — and IsCommitUnknown
// may read it. No per_page is sent: the endpoint's own page is 300 files,
// three times the client's usual 100, and fewer round trips is the point.
func (c *Client) CommitFiles(ctx context.Context, owner, repo, sha string) (files []string, capped bool, err error) {
	u := fmt.Sprintf("%s/repos/%s/%s/commits/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	files = []string{}
	for pages := 0; u != ""; pages++ {
		if pages >= maxPages {
			return nil, false, core.APIf("github: pagination exceeded %d pages", maxPages)
		}
		var page apiCommitFiles
		next, gerr := c.get(ctx, u, &page)
		if gerr != nil {
			return nil, false, gerr
		}
		for _, f := range page.Files {
			if f.Filename != "" {
				files = append(files, f.Filename)
			}
			if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
				files = append(files, f.PreviousFilename)
			}
		}
		u = next
	}
	return files, len(files) >= CommitFilesCap, nil
}
