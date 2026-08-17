package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/github"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/spf13/cobra"
)

// This file is the release-time walk — the third input source beside --range
// (pure git) and --pr (pure API), combining both: it walks main's merge points
// since a tag out of LOCAL git — a squash commit, a rebase-merge's last commit,
// or the merge commit the merge button writes, whichever GitHub named
// merge_commit_sha — resolves each to the pull request that merged it over the
// API, and expands that PR into its individual (pre-squash) commits. Nothing is
// persisted — the walk recomputes from git every run, so a release is
// idempotent and self-healing (DESIGN §4).

// sinceTagAuto is what a bare --since-tag parses to (the flag's NoOptDefVal):
// resolve the walk base from the repository itself — the highest parseable v*
// tag — so a release job never duplicates glyph's version-tag policy in shell.
const sinceTagAuto = "auto"

// sinceTagBelow is the prefix of the OTHER resolved --since-tag form,
// --since-tag=below:TAG: the walk base is the highest parseable version tag
// STRICTLY below TAG — the predecessor of a release that already has its tag.
// It exists for the job auto cannot serve: at tag-push time the new tag is the
// highest v* tag, so auto resolves to the tag being cut and walks an empty
// range. goreleaser.yml re-derived this answer in shell (`git tag
// --sort=v:refname | awk`) and inherited the exact defect latestVersionTag's
// doc names — git's sort is not a version order — so with a prerelease tag
// present the predecessor came back wrong, and for the lowest sorted tag it
// came back EMPTY, failing the job behind a tag that already existed (t-s5n4).
// The sentinel is unambiguous: git forbids ':' in a refname, so no real tag
// can spell it.
const sinceTagBelow = "below:"

// addSinceTagFlag wires --since-tag onto a command. The value is optional
// (pflag's NoOptDefVal grammar): a bare --since-tag resolves the tag itself; a
// named tag must be attached with = (--since-tag=v1.2.3).
func addSinceTagFlag(cmd *cobra.Command, target *string, verb string) {
	cmd.Flags().StringVar(target, "since-tag", "",
		verb+" every merged PR's individual (pre-squash) commits on main since a tag (bare --since-tag: the highest v* tag; --since-tag=TAG names one; --since-tag=below:TAG resolves the highest version tag strictly below TAG)")
	cmd.Flags().Lookup("since-tag").NoOptDefVal = sinceTagAuto
}

// markInputSourceFlags is the source-selection grammar bump and notes share:
// exactly one of --range / --pr / --since-tag, and --repo only with the
// API-backed sources — combined with the purely local --range it would be
// silently ignored, and glyph does not ignore input silently.
func markInputSourceFlags(cmd *cobra.Command) {
	cmd.MarkFlagsOneRequired("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "pr", "since-tag")
	cmd.MarkFlagsMutuallyExclusive("range", "repo")
}

// sinceTagArgs is the Args guard for the commands carrying --since-tag: they
// take no positionals, and the one stray positional users actually produce is
// the space form of the flag's optional value (`--since-tag v1.2.3`), which
// pflag parses as a bare --since-tag plus a leftover. Walking the WRONG range
// silently is the worst outcome, so that shape gets a usage error spelling out
// the = form instead of cobra's generic unknown-command complaint.
func sinceTagArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && cmd.Flags().Changed("since-tag") {
		if tag, _ := cmd.Flags().GetString("since-tag"); tag == sinceTagAuto {
			return core.Usagef("--since-tag takes its tag attached with '=': --since-tag=%s", args[0])
		}
	}
	return cobra.NoArgs(cmd, args)
}

// checkSinceTagFlag rejects a malformed explicit tag before anything runs —
// caller input, so usage (2), mirroring checkRangeFlag: the same mistake must
// not surface as a retryable-looking git failure (4). The auto sentinel (a
// bare --since-tag) is not a name and skips the checks.
func checkSinceTagFlag(tag string) error {
	if tag == sinceTagAuto {
		return nil
	}
	if rest, ok := strings.CutPrefix(tag, sinceTagBelow); ok {
		// The bound must NAME a version — a predecessor is found by comparing
		// under it, and there is no comparing under a tag that is not one. A
		// workflow templating an unset variable produces the bare prefix, so
		// (like the empty tag below) it must die here as the caller's input,
		// never resolve to some tag it did not mean.
		//
		// A RELEASE CANDIDATE names one. The bound is parsed with
		// ParseBaseVersion, not ParseVersion, because goreleaser.yml hands this
		// flag $GITHUB_REF_NAME on a `tags: ['v*']` trigger — so the day the
		// same file gained `prerelease: auto`, the first v3.0.0-rc.1 died right
		// here at exit 2, behind a tag that already existed: no notes, no
		// binaries, no cask, no attestation. That is the precise failure the
		// below: form was introduced to end (t-s5n4), arriving through the one
		// input nobody had handed it. Candidates stay out of the ANSWER set —
		// latestVersionTag still parses every tag with ParseVersion — so what
		// changes is only what may be asked about.
		if _, perr := bump.ParseBaseVersion(strings.TrimSpace(rest)); perr != nil {
			return core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
		}
		return nil
	}
	if strings.TrimSpace(tag) == "" {
		// A workflow templating an unset variable produces exactly this; auto
		// is spelled by OMITTING the value, never by an empty one.
		return core.Usagef("--since-tag= names an empty tag — use a bare --since-tag for the highest v* tag, or name one with --since-tag=TAG")
	}
	if strings.HasPrefix(tag, "-") {
		return core.Usagef("--since-tag %q looks like an option, not a tag", tag)
	}
	if strings.Contains(tag, "..") {
		return core.Usagef("--since-tag %q looks like a revision range — name the tag alone; the walk appends ..HEAD itself", tag)
	}
	return nil
}

// sinceTagInput resolves the repository, the walk range and the version base,
// then walks. bump, notes and release share it; the returned source names the
// range for the reason line, base (when the tag names a version) is what the
// bump steps from, and the walk's own facts come back with the commits — its
// expansion provenance AND whether it could read the range at all (release
// reports them, the others discard them).
func sinceTagInput(ctx context.Context, cfg *config.Config, tagFlag, repoFlag string) ([]walked, walkFacts, string, *bump.Version, error) {
	if err := checkSinceTagFlag(tagFlag); err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	owner, repo, err := resolveRepo(repoFlag)
	if err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	revRange, base, err := sinceTagRange(ctx, cfg, tagFlag)
	if err != nil {
		return nil, walkFacts{}, "", nil, err
	}
	commits, facts, err := walkSince(ctx, newGitHub(), cfg, owner, repo, revRange)
	return commits, facts, revRange, base, err
}

// sinceTagRange turns the --since-tag value into a git revision range and, when
// the tag names a version, the version the bump steps from. Both come from ONE
// resolution, so the walk base and the step base are the same tag by
// construction — naming a tag names the release being redone; stepping from a
// different (higher) tag would version a verdict computed over another range.
// Auto resolves the highest parseable v* tag; below:TAG the highest one
// strictly under TAG's version — the predecessor of a tag already cut, and
// under a RELEASE CANDIDATE the predecessor of the release it is a candidate
// for; a repository with no such tag walks the whole history and steps from
// v0.0.0.
// An explicit tag that is not a version still walks, but names no base (nil —
// the bump falls back to the highest v* tag).
func sinceTagRange(ctx context.Context, cfg *config.Config, tagFlag string) (revRange string, base *bump.Version, err error) {
	tag := strings.TrimSpace(tagFlag)
	if tag == sinceTagAuto {
		latest, v, lerr := latestVersionTag(ctx, nil)
		if lerr != nil {
			return "", nil, lerr
		}
		if latest == "" {
			return wholeHistory(ctx, cfg, "no version tag found")
		}
		return latest + "..HEAD", &v, nil
	}
	if rest, ok := strings.CutPrefix(tag, sinceTagBelow); ok {
		// checkSinceTagFlag guaranteed the bound parses before anything ran.
		// A pre-release bound compares as its base version — exact, not a
		// rounding: see ParseBaseVersion for why the two select the same tag.
		bound, perr := bump.ParseBaseVersion(strings.TrimSpace(rest))
		if perr != nil {
			return "", nil, core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
		}
		prev, v, lerr := latestVersionTag(ctx, &bound)
		if lerr != nil {
			return "", nil, lerr
		}
		if prev == "" {
			// The repository's first release: nothing sits below it, and dying
			// here would fail a job standing behind a tag that already exists.
			// Same walk, same guard, as auto before the first tag.
			return wholeHistory(ctx, cfg, fmt.Sprintf("no version tag below %s", strings.TrimSpace(rest)))
		}
		return prev + "..HEAD", &v, nil
	}
	if v, perr := bump.ParseVersion(tag); perr == nil {
		return tag + "..HEAD", &v, nil
	}
	return tag + "..HEAD", nil, nil
}

// sinceTagWalkCap bounds the whole-history walk: the most commits an untagged
// repository may hold before a resolved --since-tag refuses to walk it. The
// walk pays at least one API round-trip per commit it visits, and in CI the
// job runs under the Actions GITHUB_TOKEN's 1,000-request hourly budget — 200
// keeps one speculative first-release walk at a fifth of that budget. A first
// release large enough to cross it is one the operator should bound by hand.
const sinceTagWalkCap = 200

// wholeHistory is the untagged arm of a RESOLVED --since-tag (auto with no
// version tag, below: with none under its bound): the whole history, walked —
// never skipped, and never unbounded in silence.
//
// Not skipped, because this walk is how a first release is computed at all:
// preview's release-floor guard skips its pending walk on the argument that
// nothing is unreleased where nothing was ever released, but bump/notes/
// release are ANSWERING what the first release is, and a skip here folds zero
// commits, verdicts none, and deletes the first rolling draft forever.
//
// Never unbounded, because the walk pays one API round-trip per visited
// commit and this is the one resolution with no tag to bound it. Distributed
// fleet-wide, release.yml runs it on every push of a repository that has not
// released yet — measured on the v0.11.1 rollout, four untagged repositories
// walked their entire histories (t-354v). So the commits the walk would visit
// are counted FIRST, from the same log and under the same author exclusion
// the walk itself applies, and past the cap glyph refuses: a fail-loud
// CodeAPI refusal in the checkPublishedFloor family — nothing is broken
// underneath, no retry clears it, and the escape (name the base) is in the
// message. The count is exact, not estimated, so the refusal names the real
// cost; the doubled `git log` on this arm is local and cheap.
func wholeHistory(ctx context.Context, cfg *config.Config, whyNone string) (string, *bump.Version, error) {
	raws, err := gitsource.Log(ctx, ".", "HEAD")
	if err != nil {
		return "", nil, err
	}
	walked := 0
	for _, raw := range raws {
		if !slices.Contains(cfg.ExcludeAuthors, raw.Author) {
			walked++
		}
	}
	if walked > sinceTagWalkCap {
		return "", nil, core.APIf(
			"%s, and walking this repository's whole history would ask GitHub about %d commits, one round-trip each (the cap is %d) — refusing the unbounded walk; name the walk base yourself with --since-tag=TAG (cutting a tag at the intended base first if none exists)",
			whyNone, walked, sinceTagWalkCap)
	}
	// One API round-trip per commit over everything — a cost the caller
	// should see named (house rule: no silent unbounded work).
	warnf("%s — walking the repository's whole history (%d commit(s) the walk will ask GitHub about)", whyNone, walked)
	return "HEAD", &bump.Version{}, nil
}

// latestVersionTag returns the highest parseable version tag and its parsed
// version; tag is empty for a repository before its first release. The one
// resolver behind both the walk base and the bump base.
//
// below, when non-nil, bounds the answer to versions STRICTLY under it — how
// below:TAG resolves the predecessor of a tag already cut. Strictly below the
// BOUND, not merely "the highest other tag": cutting a v0.8.3 hotfix while
// v0.9.0 exists must resolve v0.8.2, never walk backwards from v0.9.0. The
// bound compares as a version too, so both spellings of the bound's own tag
// (v1.2.3 beside 1.2.3) fall out of the answer together.
//
// EVERY tag is parsed and compared as a VERSION. The order git reports is not
// one, and this used to take git's first parseable entry: `--sort=-v:refname`
// is a REFNAME sort that happens to compare digit runs numerically, so it
// orders on the leading byte first and every `refs/tags/v…` lands above every
// `refs/tags/1…` ('v' > '1'). ParseVersion accepts a tag WITHOUT the v (its
// pattern is ^v?…), so both spellings reach this loop, and measured on tags
// {v0.0.1, v0.0.2, 9.9.9, 100.0.0} git reports v0.0.2 first — the resolver
// answered v0.0.2 where the highest version is 100.0.0, moving the walk base
// and the version base together to a tag two releases behind. A wrong base is
// the silent-wrong-verdict class: the walk re-folds released commits and the
// bump steps from the wrong number, with nothing on stderr.
//
// A tie — the same version spelled twice, v1.2.3 beside 1.2.3 — keeps the
// FIRST in git's order, so the answer stays deterministic without inventing a
// preference between two tags git considers equally valid.
func latestVersionTag(ctx context.Context, below *bump.Version) (tag string, v bump.Version, err error) {
	tags, terr := gitsource.Tags(ctx, ".")
	if terr != nil {
		return "", bump.Version{}, terr
	}
	for _, t := range tags {
		pv, perr := bump.ParseVersion(t)
		if perr != nil {
			continue
		}
		if below != nil && pv.Compare(*below) >= 0 {
			continue
		}
		if tag == "" || pv.Compare(v) > 0 {
			tag, v = t, pv
		}
	}
	return tag, v, nil
}

// walked is one folded commit plus the provenance only the walk knows: the
// merged pull request it was expanded from (0 on the fallback path) and
// whether its SHA is a *landed* identity (glossary) — a commit the released
// branch actually holds. The two downstream consumers read different halves:
// classification reads the bare parser.Commit (plain), the notes read the
// citation (notesCommits) — the pull beside the sha, and the pull ALONE for a
// footprint-less commit, whose listed sha exists on no branch and used to be
// published anyway (t-xxhj: a body citing shas `git branch -r --contains`
// answers nothing for).
type walked struct {
	Raw    gitsource.RawCommit
	Pull   int
	Landed bool
}

// walkedSigilCommits strips the provenance for the fold: FoldSigils reads
// commits, not citations.
func walkedSigilCommits(ws []walked) []bump.SigilCommit {
	out := make([]bump.SigilCommit, 0, len(ws))
	for _, w := range ws {
		out = append(out, bump.SigilCommit{SHA: w.Raw.SHA, Author: w.Raw.Author, Message: w.Raw.Message})
	}
	return out
}

// walkedNoteCommits hands the fold to the notes with its citation: the pull
// number beside every expanded commit, and the SHA blanked for a commit that
// landed under no identity of its own — the squash arm, the one arm whose
// listed shas no branch holds. Blanked HERE, where DESIGN §4's arms are a
// known fact, because internal/notes renders what it is given and holds no
// arm knowledge.
func walkedNoteCommits(ws []walked) []notes.SigilCommit {
	out := make([]notes.SigilCommit, 0, len(ws))
	for _, w := range ws {
		sha := w.Raw.SHA
		if w.Pull > 0 && !w.Landed {
			sha = ""
		}
		out = append(out, notes.SigilCommit{SHA: sha, Pull: w.Pull, Author: w.Raw.Author, Message: w.Raw.Message})
	}
	return out
}

// pullExpansion records one merged pull request the walk expanded — resolved
// from its canonical commit — and how many participating commits it
// contributed, after the walk-wide SHA dedup: a stacked PR whose commits all
// rode in with its base PR reports 0, and so does a merge-merged PR whose
// commits the walk already folded in on the fallback path. This is the walk
// reporting its own expansion facts: how a verdict was assembled, in a form a
// human or a CI step can read back afterwards without re-deriving the walk's
// exclusion rules somewhere else.
//
// It records what the walk DID, and never why a number is what it is. A count
// of 0 is the case that invites a wrong reading, and both of its causes are
// innocent — see the two named above — so nothing may treat 0 as "this pull
// changed nothing".
type pullExpansion struct {
	Number  int `json:"number"`
	Commits int `json:"commits"`
}

// walkFacts is what the walk observed about ITSELF, beside the commits it
// folded: the expansion provenance the verdict reports, and whether the walk
// came back knowing it had NOT read the whole range.
//
// That second half exists because downstream the two are indistinguishable — a
// range that genuinely holds nothing and a range the walk could not read both
// arrive as an empty fold — and glyph acted on the difference: it deleted the
// rolling draft on a verdict it had just told the operator to re-run, and it
// wrote a draft whose version was computed without the pull it had just
// reported missing. The walk already warns about every one of these; this is
// the same fact in a form a decision can be taken on.
type walkFacts struct {
	// Pulls is the per-pull expansion provenance (see pullExpansion).
	Pulls []pullExpansion
	// AllUnknown: every commit the walk asked about was unknown to the queried
	// repository. One unknown SHA is API lag; ALL of them is what a wrong --repo
	// (or an inherited $GITHUB_REPOSITORY in a fork or a reusable-workflow
	// context) looks like from inside the walk.
	AllUnknown bool
	// LostPulls are merged pulls whose commits stood aside for a merge point
	// nothing resolved, in walk order — the fold is short by whatever they
	// changed. DESIGN §4 refuses to guess at their listings; this records that
	// the refusal happened.
	LostPulls []int
	// Dropped names the commits the walk could not read AT ALL because the API
	// had not indexed them: a merge commit unknown to GitHub is a merge POINT
	// the walk could not resolve, whose own message never says what it merged.
	// Deliberately NOT every fallback — a commit the fold reads from its own
	// message was READ, just from a weaker source, and DESIGN §4 blesses that
	// path. The line is "something is missing from the fold", not "the fold
	// used second-best evidence".
	Dropped []string
	// Truncated are pulls whose commit listing came back at GitHub's hard cap
	// (github.PullCommitsCap), in walk order. The API stops at that many however
	// far the pagination follows, so a listing of exactly the cap is one glyph
	// cannot claim to have read whole — the commits past it are unreachable, not
	// absent, and any one of them could carry the deciding gitmoji.
	//
	// It belongs here rather than in the warning alone for the same reason the
	// other three do: the walk already SAID it could not read the range, and then
	// deleted a draft and lowered a version on the strength of the fold anyway.
	// Measured before this arm existed: a pull returning 250 :memo: commits, with
	// a v0.2.0 draft present, exited 1 with action "delete" and the write set was
	// exactly one DELETE — in the same run whose stderr carried the truncation
	// warning.
	Truncated []int
	// Shallow: this checkout is a --depth clone, so git cannot answer the
	// footprint question at all — a commit it does not HAVE is indistinguishable
	// from one that never landed on the released branch.
	//
	// It is the only member of this struct that is a property of the CHECKOUT
	// rather than of what the API returned, and it is the one most likely to
	// appear: actions/checkout defaults to fetch-depth: 1, so a single line
	// removed from a release workflow produces it. Measured on a real
	// `git clone --depth 1`: the walk folds commits an earlier tag already
	// shipped and answers minor where the full-history control on the same
	// repository answers patch — loud (two warnings) and green.
	Shallow bool
}

// complete reports that the walk read the range it was asked about. Everything
// glyph does that it cannot take back — deleting a draft, lowering the version a
// human is about to publish — is gated on it.
func (f walkFacts) complete() bool {
	return !f.AllUnknown && !f.Shallow && len(f.LostPulls) == 0 && len(f.Dropped) == 0 && len(f.Truncated) == 0
}

// shortfall says, in one clause, what the walk could not read — for the warning
// that explains why glyph declined to act and for the line the draft carries to
// the human who will press Publish.
func (f walkFacts) shortfall(owner, repo string) string {
	var parts []string
	if f.AllUnknown {
		parts = append(parts, fmt.Sprintf("every commit in it was unknown to %s/%s (check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to)", owner, repo))
	}
	if f.Shallow {
		parts = append(parts, "this is a shallow checkout, so git cannot tell a commit that never landed on the released branch from one it does not have (check out with fetch-depth: 0)")
	}
	for _, n := range f.LostPulls {
		parts = append(parts, fmt.Sprintf("merged pull request #%d has commits here that stood aside for a merge point nothing resolved", n))
	}
	if len(f.Dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d commit(s) GitHub had not indexed could not be read (%s)", len(f.Dropped), strings.Join(f.Dropped, ", ")))
	}
	for _, n := range f.Truncated {
		parts = append(parts, fmt.Sprintf("merged pull request #%d returned the maximum %d commits, so GitHub truncated its listing and the rest could not be read", n, github.PullCommitsCap))
	}
	return strings.Join(parts, "; ")
}

// walkSince walks the range's commits oldest first and folds every merged PR's
// individual commits into one participating list, recording per-pull expansion
// provenance alongside. Author-excluded commits are skipped before any API call
// — the routine fleet-sync direct push never costs a request. Everything else
// is asked about: a commit's own shape cannot tell a pull request's merge point
// from a local merge, so resolution answers that, and the unresolved ones fall
// through to fallbackCommit, which applies the message rules. A last pass
// reconciles the pulls commits stood aside for against the pulls actually
// expanded, so a merged pull request whose canonical commit never resolved is
// named in a warning instead of silently lost.
func walkSince(ctx context.Context, c *github.Client, cfg *config.Config, owner, repo, revRange string) ([]walked, walkFacts, error) {
	raws, err := gitsource.Log(ctx, ".", revRange)
	if err != nil {
		return nil, walkFacts{}, err
	}
	// Asked once per WALK, before anything else, and not inside the expansion.
	//
	// It used to be asked lazily on the first pull that actually expanded, to
	// save one `git rev-parse` on a release that folds nothing. That saving cost
	// the fact: a walk where no pull resolves — the API lagging, an automation
	// authoring the merge commits, a range of direct pushes — never asked at all,
	// so the shallowest reading of the shallowest checkout was the one that went
	// unreported. The question is about the CHECKOUT, not about any pull, so it
	// belongs where the checkout is first read.
	shallow, serr := gitsource.IsShallow(ctx, ".")
	if serr != nil {
		return nil, walkFacts{}, serr
	}
	if shallow {
		warnf("this is a SHALLOW checkout: the walk cannot tell a commit that never landed on the released branch from one git does not have, so a merged pull request's commits can be counted again even though an earlier tag shipped them. Check out with full history (actions/checkout with fetch-depth: 0) before releasing")
	}
	var commits []walked
	// Normalized to [] up front so the JSON surface never emits null: .pulls is
	// indexable on every verdict, the none verdict included, with no null-check
	// at each consumer.
	facts := walkFacts{Pulls: []pullExpansion{}, Shallow: shallow}
	// seen holds every SHA already REPRESENTED in the fold. One commit can be
	// reachable from TWO merged PRs — a stacked branch carries its base PR's
	// pre-squash commits, so after both squash-merge, both listings contain
	// them — and without this set each occurrence would land in the notes and
	// the fold once per PR. A merge-merged PR is the other producer: its commits
	// sit on main under the SHAs the PR listing reports, so the walk meets each
	// of them twice (once as a walked commit, once inside the expansion). The
	// walk runs oldest first, so a shared commit is attributed to whatever first
	// landed it on main — and git's revision walk reaches a parent only through
	// its child, so a merge point is always walked AFTER the commits it merges,
	// whatever the commit clocks say.
	//
	// "Represented" is the whole membership rule, so it holds the CANONICAL
	// commit of every resolved pull too, not only the inner commits that pull
	// expanded into (see the found arm). Both are equally already-counted, and
	// both come back around: a pull squash-merged into a topic branch leaves its
	// squash commit inside the listing of the pull that later landed that
	// branch.
	seen := map[string]bool{}
	// inRange is every SHA this walk will visit. It is what makes "covered"
	// mean something checkable: a commit stands aside only for a pull whose
	// canonical commit is IN HERE, so a pull left unexpanded at the end is a
	// resolution failure and never a pull that lives in some other history. A
	// pull merged into a DIFFERENT base branch (a backport line, a fork) is
	// associated with commits that reached main another way while its own merge
	// point is nowhere on main — deferring to it would drop those commits, and
	// expanding it would fold a branch main never took.
	inRange := make(map[string]bool, len(raws))
	for _, raw := range raws {
		if raw.SHA != "" {
			inRange[raw.SHA] = true
		}
	}
	// The covered ledger, in walk order, and the pulls actually expanded. A
	// commit that reports itself covered by pull #N stands aside for #N's own
	// canonical commit — and if that commit never resolves, #N is a merged pull
	// request nothing in the walk represents. Every commit of it would then skip
	// itself and the release would lose it in silence, which is exactly the
	// t-7zt7 failure the fix exists to end (found by review, on this path). The
	// end of the walk reconciles the two sets.
	coveredPulls, coveredOrder, expanded := map[int]bool{}, []int{}, map[int]bool{}
	// foldPull expands one merged pull request into the walk, canonical being the
	// walked commit that resolved to it (its merge point): the listing is
	// filtered BEFORE anything judges it (see the two filters below), and the
	// pull's contribution is recorded. Only a RESOLVED canonical commit may
	// expand a pull — the reconciliation at the end of the walk deliberately
	// does not call this (see there), because a listing fetched without a
	// canonical commit in range says nothing about which of its commits belong
	// to this walk.
	//
	// What the listing DOES say is filtered twice: by where each commit landed
	// on the released branch (mainFootprint — the range governs anything git
	// can place, which is what stops a pull from folding back work an earlier
	// tag shipped), and by seen (an already-represented commit must never be
	// able to fail the release — its message may be a squash subject, which
	// need not match any pattern).
	//
	// What remains is PRE-FLIGHTED against the pattern file: a non-excluded
	// inner commit no pattern claims would refuse the fold downstream anyway
	// (same Match, same config — it cannot disagree), but down there the PR
	// association is gone, and a bare per-commit refusal is uniquely unhelpful
	// HERE — see wedgeHint.
	foldPull := func(number int, canonical string) error {
		listing, perr := pullRawCommits(ctx, c, owner, repo, number)
		if perr != nil {
			return wedgeHint(perr, owner, repo, number, canonical, "")
		}
		// Asked HERE and not inside pullRawCommits, which the --pr path shares and
		// which carries no facts: only a walk has a verdict that can be acted on
		// irreversibly, so only a walk needs the fact rather than the warning.
		// pullRawCommits still emits that warning for both callers.
		if len(listing) >= github.PullCommitsCap {
			facts.Truncated = append(facts.Truncated, number)
		}
		// Where did each listed commit LAND on the released branch? That is the
		// only question that lets the walk RANGE — a git fact — govern a listing
		// that knows nothing about it. A commit with no landing site is governed
		// by the pull alone and folds exactly as it always did; one that landed
		// is folded only if its landing site is in range.
		landed, ferr := mainFootprint(ctx, canonical, listing)
		if ferr != nil {
			return ferr
		}
		contributed := 0
		for i, r := range listing {
			if on := landed[i]; on != "" {
				if !inRange[on] {
					noticef("commit %.7s in pull request #%d landed on the released branch as %.7s, which is outside %s — it shipped under an earlier tag and is not counted again", r.SHA, number, on, revRange)
					continue
				}
				// The landing site is the identity that matters downstream: the
				// notes must cite a commit the repository actually has, and a
				// rebase-merge's listing reports pre-rebase shas that exist on
				// no branch. Substituting here also makes the walk-wide dedup
				// below compare like with like.
				r.SHA = on
			}
			if r.SHA != "" && seen[r.SHA] {
				noticef("commit %.7s in pull request #%d is already represented in this walk (a stacked branch carries its base PR's pre-squash commits; a merge-merged PR's commits sit on main under these same SHAs; a sub-PR's squash commit rides inside the PR that landed its branch) — counted once", r.SHA, number)
				continue
			}
			if !slices.Contains(cfg.ExcludeAuthors, r.Author) {
				m, merr := cfg.Match(r.Message)
				if merr != nil {
					return wedgeHint(core.Lintf("commit %.7s: %v", r.SHA, merr), owner, repo, number, canonical, onMain(r.SHA, inRange))
				}
				if !m.Matched {
					return wedgeHint(core.Lintf("commit %.7s matches none of the %d configured patterns", r.SHA, len(cfg.Patterns)), owner, repo, number, canonical, onMain(r.SHA, inRange))
				}
			}
			if r.SHA != "" {
				seen[r.SHA] = true
			}
			commits = append(commits, walked{Raw: r, Pull: number, Landed: landed[i] != ""})
			contributed++
		}
		facts.Pulls = append(facts.Pulls, pullExpansion{Number: number, Commits: contributed})
		expanded[number] = true
		return nil
	}
	visited, unknown := 0, 0
	for _, raw := range raws {
		// A merge point here is a POINTER to a pull request, not a message
		// being judged, so only its AUTHOR excludes it before resolution — and
		// the authors are the config's exclude_authors, whose commits can never
		// move the version, so resolving one would buy nothing and must not
		// cost an API round-trip (the routine bot push runs on every
		// repository, every day). The commit itself still joins the walk:
		// whether it appears in the notes is note.sections' decision, not the
		// resolution gate's. Neither its subject nor its shape may exclude it:
		// a two-parent commit is what the "Create a merge commit" button
		// writes — skipping it for that dropped the entire pull request out of
		// the release, silently (t-7zt7). Message rules apply where a message
		// IS judged: in the fold, the lint and the notes downstream.
		if slices.Contains(cfg.ExcludeAuthors, raw.Author) {
			if raw.SHA != "" && seen[raw.SHA] {
				continue
			}
			if raw.SHA != "" {
				seen[raw.SHA] = true
			}
			commits = append(commits, walked{Raw: raw, Landed: true})
			continue
		}
		visited++
		number, found, covering, rerr := mergedPullFor(ctx, c, owner, repo, raw.SHA, inRange)
		// Ledger first, whatever this commit resolves to: a commit can name the
		// pull that merged it AND a pull that merely carries it (a sub-PR's
		// squash commit is inside the branch of the pull that landed it), and
		// the reconciliation below needs both sightings.
		for _, n := range covering {
			if !coveredPulls[n] {
				coveredPulls[n] = true
				coveredOrder = append(coveredOrder, n)
			}
		}
		lag := false
		switch {
		case github.IsCommitUnknown(rerr):
			// GitHub does not know the SHA yet (422) — the walk outran the API
			// right after a push. DESIGN §4's API lag: fall back, never fail.
			unknown++
			lag = true
		case rerr != nil:
			return nil, walkFacts{}, rerr
		case found:
			if ferr := foldPull(number, raw.SHA); ferr != nil {
				return nil, walkFacts{}, ferr
			}
			// The canonical commit is now represented BY that expansion, so it
			// joins the same set — recorded after the expansion, never before,
			// because a rebase-merge's merge_commit_sha is the last rebased
			// commit and appears in the listing it must still be folded from.
			if raw.SHA != "" {
				seen[raw.SHA] = true
			}
			continue
		case len(covering) > 0:
			// Part of a merged PR whose canonical (merge_commit_sha) commit is
			// in this very range and represents it: a rebase-merge's non-final
			// commits, and — now that a merge commit resolves — every commit a
			// merge-merged PR put on main beside its merge point (gitsource.Log
			// runs without --first-parent, so the walk sees both). That
			// canonical commit expands the whole PR, so counting this one too
			// would double it. If it never does, the ledger above names the pull
			// at the end — standing aside is only safe because something in the
			// walk was supposed to stand in, and when nothing did, a human has to
			// hear about it.
			continue
		}
		// The fallback: no merged pull request explains this commit (a direct
		// push, or the API lagging), so its own message is the evidence — the
		// same evidence the fold and the notes read for every commit. One case
		// deserves more than the default: a merge commit GitHub has not
		// indexed yet is a merge POINT the walk could not resolve, and its own
		// message never says what it merged (the pattern file will skip it as
		// a merge), so the pull behind it is lost to this run. That is a fact
		// a release decision needs, not only a warning (t-7zt7's silence, in
		// miniature).
		if lag && raw.Parents >= 2 {
			warnf("merge commit %.7s is not known to GitHub yet (API lag), so the pull request it may have merged could not be resolved — nothing was counted from it; if the release looks short, re-run it once GitHub has indexed the commit", raw.SHA)
			facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
			continue
		}
		if raw.SHA != "" && seen[raw.SHA] {
			noticef("commit %.7s was already folded in by a merged pull request — counted once", raw.SHA)
			continue
		}
		if lag {
			// The lag fallback stays SOFT on an unmatched message, unlike the
			// fold (Q2): this commit's true content is the pull request the
			// API has not indexed yet — a squash subject is not a commit
			// message anyone wrote — so refusing the range here would red
			// every release for the minutes GitHub lags behind a merge.
			// DESIGN §4 blesses the weaker source and records the shortfall:
			// the commit is dropped from the fold, warned about, and carried
			// in facts.Dropped so release refuses to ACT on the incomplete
			// walk while bump and notes merely report it.
			if m, merr := cfg.Match(raw.Message); merr != nil || !m.Matched {
				warnf("commit %.7s is not known to GitHub yet (API lag) and its own message matches no pattern — nothing was counted from it; re-run once GitHub has indexed the commit", raw.SHA)
				facts.Dropped = append(facts.Dropped, fmt.Sprintf("%.7s", raw.SHA))
				continue
			}
			warnf("commit %.7s is not known to GitHub yet (API lag) — reading its own message", raw.SHA)
		} else if m, merr := cfg.Match(raw.Message); merr != nil || !m.Matched || !m.Skip {
			// A skip-pattern commit (a local git merge under the presets) is
			// passed over in silence, as v1 skipped structural merges; every
			// other fallback announces that a message, not a pull request, is
			// the evidence.
			warnf("commit %.7s has no merged pull request (a direct push, or the API lagging) — reading its own message", raw.SHA)
		}
		if raw.SHA != "" {
			seen[raw.SHA] = true
		}
		// Landed by construction: the fallback reads the walked commit
		// itself, an identity the released branch holds.
		commits = append(commits, walked{Raw: raw, Landed: true})
	}
	// Reconcile the ledger: a pull the walk only ever saw from the INSIDE. Its
	// commits stood aside for a canonical commit that never resolved — GitHub
	// not knowing the merge commit yet (a release job runs seconds after the
	// merge, and the merge commit is indexed after the commits it merges), or
	// an exclude_authors author having authored it (the author gate skips it
	// before the API).
	//
	// WARN, and deliberately do NOT expand. An earlier round of t-7zt7 folded the
	// pull's whole listing in here, and it was unsound for one reason: a pull's
	// API listing is the pull's ENTIRE history and carries nothing about this
	// walk's range, which is a git fact the listing never saw. Expanding it is
	// therefore guessing which of the pull's commits belong to THIS release, and
	// review broke that guess twice —
	//
	//   - double counting: a rebase-merged pull lists its ORIGINAL pre-rebase
	//     SHAs, which can never equal the main-branch SHAs seen holds, so the
	//     filter above passes them and the same change renders twice;
	//   - resurrecting released work: a pull whose earlier commits shipped under
	//     the previous tag has them folded straight back in — a minor bump
	//     manufactured out of a commit released one tag ago, announced by nothing.
	//
	// Both are the silent-wrong-verdict class this file exists to kill, so the
	// honest answer is to say what was lost and refuse to guess: a named warning
	// turns a silent loss (exit 1, `no release`, not one diagnostic — the t-7zt7
	// silence surviving on the new path) into a visible one.
	for _, number := range coveredOrder {
		if expanded[number] {
			continue
		}
		facts.LostPulls = append(facts.LostPulls, number)
		warnf("pull request #%d has commits in %s but nothing in the range resolved to it — GitHub does not associate its merge commit with the pull yet, or an exclude_authors author merged it with a real merge commit (which the walk skips before resolution); those commits stood aside for that merge point and are NOT counted, so this release is short by whatever #%d changed. The pull's own commit listing is its whole history and cannot say which commits belong to %s, so the walk will not guess — re-run the release once GitHub has caught up with the merge point", number, revRange, number, revRange)
	}
	if visited > 0 && unknown == visited {
		facts.AllUnknown = true
		// One unknown SHA is lag; EVERY SHA unknown is what a wrong --repo (or
		// an inherited GITHUB_REPOSITORY in a fork / reusable-workflow
		// context) looks like. Still a soft fallback — but named, so the
		// misconfiguration is findable in the log.
		warnf("all %d commit(s) in %s were unknown to %s/%s — unless they were pushed moments ago, check that --repo/$GITHUB_REPOSITORY names the repository this checkout belongs to", visited, revRange, owner, repo)
	}
	return commits, facts, nil
}

// mainFootprint answers, for each commit in a merged pull request's API listing,
// which commit on the RELEASED branch it landed as — the empty string when it
// landed under no identity of its own.
//
// This is the missing half of the expansion. A pull's listing is the pull's
// ENTIRE history and carries nothing about the walk's range, which is a git
// fact; DESIGN §4 says so, and says it as the reason the walk refuses to expand
// an UNRESOLVED pull. The resolved arm expanded the listing whole anyway, and
// the gap between the two is a fabricated release: with a version tag cut inside
// a merge-merged pull's topic branch, commits that shipped under that tag were
// folded back into the next one (t-8xsb — exit 0, empty stderr, a minor invented
// out of released work). Once each listed commit is mapped to where it landed,
// the range can govern it, and both arms follow the same principle.
//
// Two questions, asked of LOCAL GIT alone — GitHub reports no merge method, and
// asking for one would be a round-trip per pull to learn something git already
// knows. They are asked in this order because the first is a git FACT and the
// second is an inference the first narrows:
//
//  1. Did this listed commit land under its OWN sha? The merge button leaves the
//     pull's commits on the released branch verbatim, so a listed sha that this
//     repository holds AND that is an ancestor of HEAD landed as itself. Asked of
//     HEAD rather than of the merge commit on purpose: a commit that reached the
//     branch by another route is just as released, and the range is what decides
//     whether it belongs here. That holds even when this pull's rebase ALSO wrote
//     a copy of the change — the double landing keeps git's answer, ratified
//     (t-nsww, DESIGN §4).
//  2. Whatever is LEFT, does it align against the run a rebase would have
//     written? A rebase rewrites the commits it replays but preserves their
//     messages and their order, and GitHub names the LAST of the run as
//     merge_commit_sha — so the N first-parent commits ending there align
//     positionally with the N unplaced entries, in listing order. The alignment
//     is VERIFIED message by message and abandoned whole unless every one
//     matches: a guess about which commits belong to a release is precisely what
//     this function exists to remove, and half a mapping is worse than none.
//     The window can also reach PAST the run: a replay the rebase dropped as
//     already upstream aligns, message-verified, to the older commit that made
//     it redundant — the other half of the ratified double-landing answer
//     (t-nsww, DESIGN §4).
//
// Step 2 used to run only when step 1 placed NOTHING, on the reading that a
// listing is either all-verbatim or all-rewritten. It is neither, and both ways
// out of that assumption were measured against the real API on
// akira-toriyama/glyph-test (drill for t-7h15):
//
//   - A listing can be MIXED. A stacked pull request carries its base pull's
//     commits, and GitHub computes the listing against a STORED base sha rather
//     than re-deriving it, so those commits stay listed after the base pull lands
//     them on main under their own shas (measured: pull #28 still listed the
//     merge-button-landed ed1bd9e after #27 merged). Rebase-merge the stacked
//     pull and step 1 places that one entry, step 2 never ran, and every REWRITTEN
//     entry kept an empty landing site — which foldPull reads as "the pull alone
//     governs" and folds regardless of the range. That is t-8xsb resurrected
//     through the front door: measured minor where patch was correct, exit 0, no
//     warning, and the notes citing pre-rebase shas main does not contain.
//   - A rebase does NOT replay everything it is given. It drops the base-merge
//     commit that "merge main into the branch" leaves — an entirely ordinary
//     branch, and GitHub permits rebase-merge over it (measured: pull #26 listed
//     three commits, two landed). That entry has no landing site under any shape,
//     so leaving it in the window made the count wrong and the alignment was
//     abandoned WHOLE, taking the two commits it could have placed with it. It is
//     excluded on its parent count — the same fact bump.ExcludedFromClassification
//     already reads, so nothing downstream needs it placed.
//
// A squash leaves no footprint at all and reaches step 2 with the whole listing,
// where it fails to align: the pull alone governs it, exactly as before.
//
// The unalignable case therefore still degrades to the old behaviour rather than
// to an error or a warning. A squash and a rebase whose window does not align are
// not distinguishable without the merge method — including when step 1 placed
// some of the listing, since a squash-merged STACKED pull is that shape too — and
// squash is the fleet's norm, so a diagnostic here would fire on nearly every
// release and teach people to ignore it. What it costs is bounded: exactly the
// expansion that ran before any of this existed.
func mainFootprint(ctx context.Context, canonical string, listing []gitsource.RawCommit) ([]string, error) {
	landed := make([]string, len(listing))
	shas := make([]string, 0, len(listing))
	for _, r := range listing {
		if r.SHA != "" {
			shas = append(shas, r.SHA)
		}
	}
	// One call to drop everything this repository does not hold — which is most
	// of the listing for the squash and rebase shapes, so the per-sha ancestry
	// test below runs only where it can answer yes.
	have, err := gitsource.Have(ctx, ".", shas)
	if err != nil {
		return nil, err
	}
	// window is the listing positions a rebase could still account for, in listing
	// order: everything step 1 did not place, minus the entries a rebase never
	// replays. Its LENGTH is what makes the alignment checkable, so an entry that
	// cannot have a landing site has to leave — one that stays shifts every
	// position after it and the whole mapping is thrown away.
	window := make([]int, 0, len(listing))
	for i, r := range listing {
		if r.SHA != "" && have[r.SHA] {
			on, aerr := gitsource.IsAncestor(ctx, ".", r.SHA, "HEAD")
			if aerr != nil {
				return nil, aerr
			}
			if on {
				landed[i] = r.SHA
				continue
			}
		}
		// A merge commit is never replayed by a rebase (measured — see above), so
		// it has no landing site to find and no position to hold.
		if r.Parents >= 2 {
			continue
		}
		window = append(window, i)
	}
	// Nothing left to place: the whole merge-button shape, and also the pull that
	// listed no commits at all. Neither may fall through, and the reason is not
	// the saved subprocess — `canonical` is a sha this CHECKOUT may not hold, so
	// logging against it turns "there was nothing to align" into an exit-4 git
	// error mid-walk. One guard for both because it is one question: is there
	// anything an alignment could answer about?
	if len(window) == 0 {
		return landed, nil
	}
	mains, err := gitsource.FirstParentLog(ctx, ".", canonical, len(window))
	if err != nil {
		return nil, err
	}
	if len(mains) != len(window) {
		return landed, nil
	}
	for k, i := range window {
		if strings.TrimSpace(mains[k].Message) != strings.TrimSpace(listing[i].Message) {
			return landed, nil
		}
	}
	for k, i := range window {
		landed[i] = mains[k].SHA
	}
	return landed, nil
}

// wedgeHint decorates a lint failure surfaced while expanding a merged PR on
// the release walk (ratified Q1/t-2nzf: such failures stay hard — never a
// silent patch). A bare per-commit lint line is uniquely unhelpful here: the
// caller never named a PR (the walk resolved it), and published history is
// immutable, so the SAME failure — a commit that bypassed the lint gate —
// wedges every future release until the walk range moves past it. The message
// therefore names the pull request and the escape. Non-lint failures pass
// through untouched.
//
// "Published history", not "squash history": since a merge commit resolves
// (t-7zt7), the offending commit is just as often one a merge-merged PR put on
// main under its own SHA. Neither shape is rewritable on a branch other repos
// have pulled — but a message that named only squashes read as inapplicable to
// exactly the repositories where this newly happens, which is the worst moment
// to sound like someone else's problem.
//
// WHERE THE ESCAPE POINTS depends on whether the offending commit is somewhere a
// tag can be cut, and onMain is what answers that. A merge- or rebase-merged
// pull put its commits on the released branch, so the walk can now drop the ones
// an earlier tag already shipped (mainFootprint) — which means a base past the
// offending commit clears the wedge, the intuitive escape, restored. A
// SQUASH-merged pull has no such commit: its listing exists only over the API,
// its one commit on main IS its merge point, and nothing short of a base at or
// past that merge point stops the walk from re-fetching the whole listing.
//
// That distinction used to be invisible, and the cost was a hint that lied by
// omission. Before mainFootprint, expanding a pull re-read its entire listing
// whenever its merge point was in range, so for a merge-merged pull a base past
// the offending commit wedged again and the hint had to send everyone to the
// merge point (verified then: tag AT the offending commit and tag strictly past
// it both still exited 3). Now both subtests exit 0, so pointing a merge-merged
// pull at its merge point would throw away every commit between — a short
// release recommended by glyph itself.
func wedgeHint(err error, owner, repo string, number int, mergePoint, offending string) error {
	ce := core.AsError(err)
	if ce == nil || ce.Code != core.CodeLint {
		return err
	}
	base, why := mergePoint, "the pull request was squash-merged, so its commits exist only over the API and re-reading the listing is the only way the walk sees them at all; its ONE commit on the released branch is that merge point"
	if offending != "" {
		base, why = offending, "the offending commit is itself on the released branch (the pull was merge- or rebase-merged), and the walk no longer folds back a listed commit that landed outside the range"
	}
	return &core.Error{Code: core.CodeLint, Details: ce.Details, Msg: fmt.Sprintf(
		"%s — inside merged pull request %s/%s#%d, which the release walk resolved from its merge point %.7s; the commit is already on a published branch and cannot be rewritten, so every release wedges here until the walk starts past it. The walk must start AT OR PAST %.7s — %s. Cut a release tag there by hand, or name such a tag with an explicit --since-tag=TAG (DESIGN §4)",
		ce.Msg, owner, repo, number, mergePoint, base, why)}
}

// onMain reports the sha a wedge escape can be cut at: the commit itself when
// the walk can see it on the released branch, else "" for a commit that lives
// only inside a pull request's API listing. By the time foldPull asks, a listed
// commit that landed outside the range has already been dropped, so being in
// range is exactly the same question as having a landing site at all.
func onMain(sha string, inRange map[string]bool) string {
	if sha != "" && inRange[sha] {
		return sha
	}
	return ""
}

// mergedPullFor resolves a merge point on main to the pull request that merged
// it: a commit can be ASSOCIATED with many PRs (a revert, a mention), so the
// match is MergeCommitSHA == sha and actually merged — never list order. One
// equality serves every merge style, because GitHub points merge_commit_sha at
// whichever commit represents the merge: the squash commit, the last rebased
// commit, or — for the merge button — the merge commit itself.
//
// covering reports the other side of that equality, by NUMBER and not merely as
// a flag: every merged pull that carries this commit while some OTHER sha
// represents it (a rebase-merge's earlier commits, a merge-merged PR's branch
// commits, a sub-PR's squash commit riding inside the branch of the pull that
// landed it). Such a commit stands aside for that pull's canonical commit — so
// the walk must be able to tell, at the end, whether that canonical commit ever
// turned up. The numbers are what make that reconciliation possible; a bare
// bool let a merged pull request disappear whenever its merge point failed to
// resolve (t-7zt7). Both results can be non-empty at once, and both are used.
//
// inRange gates covering on the canonical commit being one this walk visits.
// Without that gate "covered" is not a claim about this release at all: a pull
// merged into ANOTHER base branch is associated with commits that reached main
// by a different route, and its merge point is on a history main never took —
// standing aside for it would drop a commit from the release, and rescuing it
// would fold in a branch that was never merged here.
func mergedPullFor(ctx context.Context, c *github.Client, owner, repo, sha string, inRange map[string]bool) (number int, found bool, covering []int, err error) {
	pulls, err := c.CommitPulls(ctx, owner, repo, sha)
	if err != nil {
		return 0, false, nil, err
	}
	for _, p := range pulls {
		if p.MergedAt == "" {
			continue
		}
		if p.MergeCommitSHA == sha {
			if !found {
				// First match wins, as before this returned early: list order is
				// GitHub's and the verdict must not start depending on it.
				number, found = p.Number, true
			}
			continue
		}
		if inRange[p.MergeCommitSHA] {
			covering = append(covering, p.Number)
		}
	}
	return number, found, covering, nil
}
