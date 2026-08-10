package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/hook"
)

// This file is `glyph hook pre-push`: the verdict a pre-push hook asks for.
// git hands it the refs about to be pushed on stdin and the remote on argv; it
// answers by linting the commits the remote does not already have.
//
// Why the range arithmetic is HERE and not in the hook script: the script is a
// file installed once into ~34 clones that nothing refreshes — no pull, no
// fleet-sync, no CI job — so a script that computed anything would go on
// computing the old thing forever, in every repository, invisibly. DESIGN §2.1
// ratified exactly this split for the commit-msg hook's cleanup mode, and it is
// the same argument. The script's whole knowledge is "pipe stdin to glyph and
// forward one exit code".

// pushRef is one line of git's pre-push protocol: four space-separated fields,
// newline-terminated. Measured against git 2.54, the shapes that are NOT a
// branch update are what the fields have to distinguish:
//
//	refs/heads/x  <oid>  refs/heads/x  <zeros>   a new branch
//	(delete)      <zeros> refs/heads/x <oid>     a deletion — the literal word
//	refs/tags/t   <oid>  refs/tags/t   <zeros>   a tag; for an annotated tag the
//	                                             local oid is the TAG object
//	HEAD          <oid>  refs/heads/m  <oid>     `git push origin HEAD`
//
// The local ref is therefore not an identity — it can be a literal `HEAD`, a
// raw sha, or the word `(delete)`. Only the REMOTE ref names what is being
// written, which is why every decision below reads that one.
type pushRef struct {
	LocalRef  string
	LocalOID  string
	RemoteRef string
	RemoteOID string
}

// branchName returns the branch this line writes on the remote, or "" when the
// line is not a branch update at all.
//
// A deletion is recognised by the all-zero LOCAL oid rather than by the literal
// `(delete)`: the word is git's rendering of the ref field, and the oid is the
// fact. Tags are excluded on the remote ref's prefix because an annotated tag's
// local oid is a tag object, so nothing about the oid says "not a commit" until
// git has been asked — and asking is what the prefix saves.
func (p pushRef) branchName() string {
	if allZero(p.LocalOID) || !strings.HasPrefix(p.RemoteRef, "refs/heads/") {
		return ""
	}
	return strings.TrimPrefix(p.RemoteRef, "refs/heads/")
}

// allZero reports whether an oid is git's null object name. The check is on the
// bytes, never on a length of 40: a repository created with
// `--object-format=sha256` writes 64 zeros (measured), and a width test would
// read its deletions as real pushes of a commit that does not exist.
func allZero(oid string) bool {
	if oid == "" {
		return false
	}
	return strings.Trim(oid, "0") == ""
}

// parsePushRefs reads git's pre-push protocol.
//
// A line that is not four fields is an API failure, not a lint failure: this is
// git's own wire format, so a line glyph cannot read means glyph has
// misunderstood git, and the installed hook waves every non-lint exit through
// rather than blocking a push over it. Empty input is a successful empty
// result — git runs the hook with nothing on stdin when the push has nothing to
// do (measured).
func parsePushRefs(r io.Reader) ([]pushRef, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, core.APIf("reading the pre-push ref list: %v", err)
	}
	var refs []pushRef
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 {
			return nil, core.APIf("pre-push line %q has %d fields, want 4 (<local ref> <local sha> <remote ref> <remote sha>)", line, len(f))
		}
		refs = append(refs, pushRef{LocalRef: f[0], LocalOID: f[1], RemoteRef: f[2], RemoteOID: f[3]})
	}
	return refs, nil
}

// resolveRemote turns the hook's argv into a configured remote NAME.
//
// git passes the remote as it was named on the command line, so `$1` is not
// necessarily a name: `git push git@host:o/r.git HEAD` passes the URL as both
// arguments (measured). Falling back to the URL match is what keeps the
// tracking-ref lookup working there. An unresolvable remote returns "", and the
// caller degrades to excluding nothing — over-reporting outgoing commits, which
// costs a redundant lint, where under-reporting would pass unlinted work.
func resolveRemote(ctx context.Context, args []string) string {
	if len(args) == 0 {
		return ""
	}
	urls, err := gitsource.RemoteURLs(ctx, ".")
	if err != nil {
		return ""
	}
	if _, ok := urls[args[0]]; ok {
		return args[0]
	}
	if len(args) > 1 {
		for name, url := range urls {
			if url == args[1] {
				return name
			}
		}
	}
	return ""
}

// outgoingRevs builds the revision list for one branch line: what it adds, minus
// everything the remote is already known to hold.
//
// The two obvious spellings are both wrong, and both were measured wrong rather
// than argued:
//
//   - `<remote oid>..<local oid>` (git's own pre-push.sample) counts commits the
//     remote already has whenever the branch merged the default branch in — the
//     everyday shape here — and is `fatal: Invalid revision range` when the
//     remote oid was never fetched into this clone.
//   - `--not --remotes` reported ZERO outgoing commits for a branch that had been
//     pushed to a second remote first: a silent green over work no gate had seen.
//     Under-reporting is the one direction that cannot be tolerated.
//
// So the exclusion is every remote-tracking tip of THIS remote, plus the remote
// oid itself when this clone actually holds it. `have` is the caller's one
// batched answer for every line's remote oid.
//
// The default branch is the exception, and it is the one that matters: there
// the ref's own remote oid IS main's tip, so `^oid` alone is EXACT — everything
// already on main is its ancestor — while the tips are strictly too broad. That
// breadth is not academic. Measured against a real `git push`: a
// :construction: commit pushed to a topic branch first, then pushed to main,
// was excluded on the second push because the remote held it on the topic ref,
// so the one gate that exists to stop it said "nothing to lint" and let it onto
// main. Off the default branch the tips stay, because there the remote oid is a
// stale topic tip and dropping them re-judges every commit the branch merged in
// from main — on a repository with pre-glyph history, a hook that can never be
// satisfied.
func outgoingRevs(p pushRef, defaultBranch string, tips []string, have map[string]bool) []string {
	revs := []string{p.LocalOID}
	held := !allZero(p.RemoteOID) && have[p.RemoteOID]
	if held {
		revs = append(revs, "^"+p.RemoteOID)
	}
	if held && defaultBranch != "" && p.branchName() == defaultBranch {
		return revs
	}
	for _, t := range tips {
		revs = append(revs, "^"+t)
	}
	return revs
}

// prePushRun is the verdict. args is git's argv for the hook, forwarded
// verbatim by the script.
func prePushRun(ctx context.Context, args []string, known func(string) bool) error {
	// Same reason as the commit-msg arm: the hook that called this is the one
	// artefact nothing refreshes, and its own run is when the developer is here.
	warnIfHookStale(ctx, hook.Kinds[1])

	refs, err := parsePushRefs(in)
	if err != nil {
		return err
	}

	var branches []pushRef
	for _, p := range refs {
		if p.branchName() != "" {
			branches = append(branches, p)
		}
	}
	if len(branches) == 0 {
		// Nothing a commit convention has an opinion about: an up-to-date push,
		// a deletion, a tag. Silence rather than a warning, because a tag push is
		// a normal act and an annotation on every one of them is the noise that
		// teaches people to stop reading them.
		return nil
	}

	remote := resolveRemote(ctx, args)
	var tips []string
	if remote != "" {
		if tips, err = gitsource.RemoteTips(ctx, ".", remote); err != nil {
			return err
		}
	}
	remoteOIDs := make([]string, 0, len(branches))
	for _, p := range branches {
		if !allZero(p.RemoteOID) {
			remoteOIDs = append(remoteOIDs, p.RemoteOID)
		}
	}
	have, err := gitsource.Have(ctx, ".", remoteOIDs)
	if err != nil {
		return err
	}

	// The default branch decides the CONSEQUENCE of a finding, never whether the
	// rule fires. Unresolved is not fatal: the ref is local, a clone made before
	// `git remote set-head` has none, and guessing `main` blocks a legal commit
	// on a master/develop repository whose only escape is --no-verify — the
	// escape that turns the whole gate off (DESIGN §2.1).
	defaultBranch := ""
	if remote != "" {
		if defaultBranch, err = gitsource.DefaultBranch(ctx, ".", remote); err != nil {
			return err
		}
	}

	// One walk over the union, deduped by sha: a commit can appear on two lines
	// of one push (measured, pushing a branch and a tag-shaped alias of it), and
	// linting it twice would report one violation twice.
	seen := map[string]bool{}
	onDefault := map[string]bool{}
	var raws []gitsource.RawCommit
	for _, p := range branches {
		commits, lerr := gitsource.LogRevs(ctx, ".", outgoingRevs(p, defaultBranch, tips, have))
		if lerr != nil {
			return lerr
		}
		for _, c := range commits {
			if p.branchName() == defaultBranch && defaultBranch != "" {
				onDefault[c.SHA] = true
			}
			if seen[c.SHA] {
				continue
			}
			seen[c.SHA] = true
			raws = append(raws, c)
		}
	}

	findings, checked := lintRaws(raws, known)
	if checked == 0 {
		// The two causes need different sentences, and reporting the first as the
		// second is what the live-fire run caught: an empty walk means the remote
		// already holds everything this push carries, while a non-empty one means
		// the convention has no opinion about any of it.
		if len(raws) == 0 {
			warnf("nothing linted: the remote already holds every commit this push carries")
			return nil
		}
		warnf("nothing linted: all %d outgoing commit(s) are excluded from the convention (bots, merge commits, autosquash artifacts, raw git reverts)", len(raws))
		return nil
	}
	if len(findings) == 0 {
		return nil
	}

	var blocking []rangeViolation
	for _, f := range findings {
		if onDefault[f.SHA] {
			blocking = append(blocking, f)
		}
	}
	if len(blocking) > 0 {
		return &core.Error{
			Code:    core.CodeLint,
			Msg:     fmt.Sprintf("%d commit-convention violation(s) among %d commit(s) being pushed to %s", len(blocking), checked, defaultBranch),
			Details: blocking,
		}
	}

	// Off the default branch the finding is real but the consequence is not: this
	// is mid-branch by construction, :construction: is legal here and illegal at
	// the merge, and a hook that refused it would make the branch unpushable.
	warnf("%d commit-convention violation(s) in this push — not blocking off the default branch, but the commit-lint job will reject them: %s",
		len(findings), summariseFindings(findings))
	if defaultBranch == "" {
		warnf("this clone does not record %s's default branch, so nothing in this push could block; `git remote set-head %s -a` restores it", remoteLabel(remote), remoteLabel(remote))
	}
	return nil
}

// summariseFindings folds the findings onto the one line an annotation is.
func summariseFindings(fs []rangeViolation) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("%.7s %s", f.SHA, f.Rule))
	}
	return strings.Join(parts, "; ")
}

// remoteLabel names the remote in a message when one was resolved, and says so
// when none was — a message that reads "`git remote set-head  -a`" is worse
// than one that admits it does not know which remote to name.
func remoteLabel(remote string) string {
	if remote == "" {
		return "<remote>"
	}
	return remote
}
