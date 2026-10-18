package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/bump"
	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
)

// Auto is what a bare --since-tag parses to (the flag's NoOptDefVal):
// resolve the walk base from the repository itself — the highest parseable v*
// tag HEAD contains (latestVersionTag) — so a release job never duplicates
// glyph's version-tag policy in shell.
const Auto = "auto"

// Below is the prefix of the OTHER resolved --since-tag form,
// --since-tag=below:TAG: the walk base is the highest parseable version tag
// HEAD contains STRICTLY below TAG — the predecessor of a release that already
// has its tag. It exists for the job auto cannot serve: at tag-push time the
// new tag is the highest v* tag, so auto resolves to the tag being cut and
// walks an empty range. goreleaser.yml re-derived this answer in shell (`git tag
// --sort=v:refname | awk`) and inherited the exact defect latestVersionTag's
// doc names — git's sort is not a version order — so with a prerelease tag
// present the predecessor came back wrong, and for the lowest sorted tag it
// came back EMPTY, failing the job behind a tag that already existed (t-s5n4).
// The sentinel is unambiguous: git forbids ':' in a refname, so no real tag
// can spell it.
const Below = "below:"

// checkSinceTagFlag rejects a malformed explicit tag before anything runs —
// caller input, so usage (2), mirroring checkRangeFlag: the same mistake must
// not surface as a retryable-looking git failure (4). The auto sentinel (a
// bare --since-tag) is not a name and skips the checks.
func CheckTag(tag string) error {
	if tag == Auto {
		return nil
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(tag), Below); ok {
		// The bound must NAME a version — a predecessor is found by comparing
		// under it, and there is no comparing under a tag that is not one. A
		// workflow templating an unset variable produces the bare prefix, so
		// (like the empty tag below) it must die here as the caller's input,
		// never resolve to some tag it did not mean.
		//
		// A RELEASE CANDIDATE names one. The bound is parsed with
		// ParseBaseVersion, not ParseVersion, because goreleaser.yml hands this
		// flag $GITHUB_REF_NAME on a `tags: ['v*']` trigger — so the day
		// .goreleaser.yaml gained `prerelease: auto`, the first v3.0.0-rc.1 died
		// right here at exit 2, behind a tag that already existed: no notes, no
		// binaries, no cask, no attestation. That is the precise failure the
		// below: form was introduced to end (t-s5n4), arriving through the one
		// input nobody had handed it. Candidates stay out of the ANSWER set —
		// latestVersionTag still parses every tag with ParseVersion — so what
		// changes is only what may be asked about.
		//
		// Parsed ON ITS LINE: a bound spelled haiku/v1.2.3 is version-shaped
		// on the haiku/ line, and whether that line is declared is
		// resolveLinesScoped's question, asked once the config is loaded.
		_, _, _, err := belowBound(rest)
		return err
	}
	if strings.TrimSpace(tag) == "" {
		// A workflow templating an unset variable produces exactly this; auto
		// is spelled by OMITTING the value, never by an empty one.
		return core.Usagef("--since-tag= names an empty tag — use a bare --since-tag for the highest v* tag HEAD contains, or name one with --since-tag=TAG")
	}
	if strings.HasPrefix(tag, "-") {
		return core.Usagef("--since-tag %q looks like an option, not a tag", tag)
	}
	if strings.Contains(tag, "..") {
		return core.Usagef("--since-tag %q looks like a revision range — name the tag alone; the walk appends ..HEAD itself", tag)
	}
	return nil
}

// sinceTagRange turns the --since-tag value into a git revision range and, when
// the tag names a version, the version the bump steps from. Both come from ONE
// resolution, so the walk base and the step base are the same tag by
// construction — naming a tag names the release being redone; stepping from a
// different (higher) tag would version a verdict computed over another range.
// Auto resolves the highest parseable v* tag HEAD contains; below:TAG the
// highest one strictly under TAG's version — the predecessor of a tag already
// cut, and under a RELEASE CANDIDATE the predecessor of the release it is a
// candidate for; a HEAD whose history holds no such tag walks the whole
// history and steps from v0.0.0. Only the resolved forms read the candidates:
// an explicit tag is taken as named wherever it sits, because naming a tag
// names the release being redone.
// An explicit tag that is not a version still walks, but names no base (nil —
// the bump falls back to the highest v* tag HEAD contains).
//
// baseTag is the range's left side when it is a tag, in its own spelling
// (line.BaseTag): the resolved forms' answer, a typed value only when it is a
// tag (tagBase), "" on the whole-history arm.
func (w Walker) sinceTagRange(ctx context.Context, cfg *config.Config, tagFlag string) (revRange, baseTag string, base *bump.Version, err error) {
	tag := strings.TrimSpace(tagFlag)
	if tag == Auto {
		latest, v, lerr := w.LatestVersionTag(ctx, config.Line{}, nil)
		if lerr != nil {
			return "", "", nil, lerr
		}
		if latest == "" {
			revRange, base, err = w.wholeHistory(ctx, cfg, "no version tag in HEAD's history", "")
			return revRange, "", base, err
		}
		return latest + "..HEAD", latest, &v, nil
	}
	if rest, ok := strings.CutPrefix(tag, Below); ok {
		// This is the single line, so a bound on any other line names a line
		// this repository does not have (resolveLinesScoped answers for
		// declared packages, and never reaches here with one). A pre-release
		// bound compares as its base version — exact, not a rounding: see
		// ParseBaseVersion for why the two select the same tag.
		bound, prefix, under, perr := belowBound(rest)
		if perr != nil {
			return "", "", nil, perr
		}
		if prefix != "" {
			return "", "", nil, core.Usagef("--since-tag=below:%s names the %s line, but this repository declares no [[packages]] — its one line is the bare vX.Y.Z tags", bound, prefix)
		}
		prev, v, lerr := w.LatestVersionTag(ctx, config.Line{}, &under)
		if lerr != nil {
			return "", "", nil, lerr
		}
		if prev == "" {
			// The repository's first release: nothing sits below it, and dying
			// here would fail a job standing behind a tag that already exists.
			// Same walk, same guard, as auto before the first tag.
			revRange, base, err = w.wholeHistory(ctx, cfg, fmt.Sprintf("no version tag below %s in HEAD's history", bound), "")
			return revRange, "", base, err
		}
		return prev + "..HEAD", prev, &v, nil
	}
	baseTag, err = w.tagBase(ctx, tag)
	if err != nil {
		return "", "", nil, err
	}
	if v, perr := bump.ParseVersion(tag); perr == nil {
		return tag + "..HEAD", baseTag, &v, nil
	}
	return tag + "..HEAD", baseTag, nil, nil
}

// belowBound parses what follows --since-tag=below: — the one place the
// bound is read. bound is the tag as written (trimmed), prefix the line it
// names, v its base version (a candidate's triple, ParseBaseVersionOn).
// checkSinceTagFlag calls it to refuse the flag before anything runs;
// sinceTagRange and resolveLinesScoped call it for the values, so the
// refusal has one sentence and the two resolvers carry none of their own.
func belowBound(rest string) (bound, prefix string, v bump.Version, err error) {
	bound = strings.TrimSpace(rest)
	prefix = bump.LinePrefix(bound)
	v, perr := bump.ParseBaseVersionOn(prefix, bound)
	if perr != nil {
		return "", "", bump.Version{}, core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
	}
	return bound, prefix, v, nil
}

// Cap bounds the whole-history walk: the most commits an untagged
// repository may hold before a resolved --since-tag refuses to walk it. The
// walk pays at least one API round-trip per commit it visits, and in CI the
// job runs under the Actions GITHUB_TOKEN's 1,000-request hourly budget — 200
// keeps one speculative first-release walk at a fifth of that budget. A first
// release large enough to cross it is one the operator should bound by hand.
//
// Only this arm is capped: a tagged range — the single line's or the packages
// union's — is the unreleased work a release must read, and its base moves only
// by releasing it, so a cap there would refuse a release with no remedy (DESIGN
// §4.1, "Only the untagged arm is capped").
const Cap = 200

// FlagEscape is the remedy for a caller that HAS --since-tag. It is a
// parameter rather than a constant in the message because `preview` does not:
// its flags are --pr/--repo/--notes/--json, and a refusal that sends an
// operator to a flag the command rejects is a dead end (t-60dc, measured
// 2026-09-15: `preview --pr` exited 4 naming --since-tag=TAG).
const FlagEscape = "; name the walk base yourself with --since-tag=TAG (cutting a tag at the intended base first if none exists)"

// wholeHistory is the untagged arm of a RESOLVED --since-tag (auto with no
// version tag in HEAD's history, below: with none under its bound): the whole
// history, walked — never skipped, and never unbounded in silence. whyNone
// states that fact about HEAD's history, never about the repository: under a
// candidate set of the tags HEAD contains, "no version tag found" beside a
// `git tag -l` that lists one is the sentence t-gt9n forbids.
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
// CodeAPI refusal in the draftplan.CheckPublishedFloor family — nothing is
// broken underneath, no retry clears it, and the escape (name the base) is in
// the message. The count is exact, not estimated, so the refusal names the
// real cost; the doubled `git log` on this arm is local and cheap.
func (w Walker) wholeHistory(ctx context.Context, cfg *config.Config, whyNone, escape string) (string, *bump.Version, error) {
	raws, err := w.Git.Log(ctx, "HEAD")
	if err != nil {
		return "", nil, err
	}
	walked := 0
	for _, raw := range raws {
		if !slices.Contains(cfg.ExcludeAuthors, raw.Author) {
			walked++
		}
	}
	if walked > Cap {
		if escape == "" {
			escape = FlagEscape
		}
		return "", nil, core.APIf(
			"%s, and walking this repository's whole history would ask GitHub about %d commits, one round-trip each (the cap is %d) — refusing the unbounded walk%s",
			whyNone, walked, Cap, escape)
	}
	// One API round-trip per commit over everything — a cost the caller
	// should see named (house rule: no silent unbounded work).
	w.Report.Warnf("%s — walking the repository's whole history (%d commit(s) the walk will ask GitHub about)", whyNone, walked)
	return "HEAD", &bump.Version{}, nil
}

// latestVersionTag returns the highest parseable version tag ON ONE LINE and
// its parsed version; tag is empty for a line before its first release. The
// one resolver behind both the walk base and the bump base. l is the line
// asked about: l.Prefix is its tag namespace ("" for the bare line,
// config.Package.tagPrefix for a package) and l.Holds its majors, which keeps
// pubsub/ and pubsub/v2 apart on one prefix. A tag on any other line — a curry/
// tag asked about haiku/, a bare v* tag asked about any package, a nested
// line's tag asked about its parent — is not a candidate, so one line's release
// can never move another line's base (DESIGN §4.1; mutation row
// packages-tag-of-one-line-baselines-another).
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
//
// The candidates are the tags HEAD's history HOLDS (releasesHEADHolds), never
// every tag the clone carries: a tag outside that history is another
// history's release — a maintenance branch's, a side branch's, or one cut
// later on a descendant of an older checkout — and baselining on it re-folds
// what this branch's releases shipped or folds none at all (DESIGN §4, "The
// walk base is a release HEAD contains"; mutation row
// walk-base-reads-a-tag-head-does-not-contain). Every caller inherits the
// rule: the walk base, below:'s predecessor, and the step base a source
// without one falls back to (currentVersion, preview's current). The price is
// paid per call, and a packages walk calls once per declared line: two git
// processes, of which --merged is the dear one — 0.14 s over
// google-cloud-go's 7,935 tags without a commit-graph (a fresh clone carries
// none), 0.03 s with one, against 0.01 s for the plain listing (measured
// 2026-09-29) — about 29 s over its 204 lines at that rate.
func (w Walker) LatestVersionTag(ctx context.Context, l config.Line, below *bump.Version) (tag string, v bump.Version, err error) {
	tags, terr := w.releasesHEADHolds(ctx)
	if terr != nil {
		return "", bump.Version{}, terr
	}
	for _, t := range tags {
		pv, perr := bump.ParseVersionOn(l.Prefix, t)
		if perr != nil || !l.Holds(pv.Major) {
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

// releasesHEADHolds is latestVersionTag's candidate set: the tags HEAD's
// history holds. A shallow checkout cannot say — its history stops at the
// depth, so --merged drops every release past it (measured: a --depth 1 clone
// with its tags fetched read v2.0.0 -> v2.1.0 as v0.0.0 -> v0.1.0) — and it
// reads every tag instead, the set this resolver read before. Nothing is
// trusted on that answer that was not already: every walk over a shallow
// checkout records it as unread (Facts.Shallow), which release refuses
// (mutation row shallow-checkout-resolves-from-a-truncated-history).
func (w Walker) releasesHEADHolds(ctx context.Context) ([]string, error) {
	shallow, err := w.Git.IsShallow(ctx)
	if err != nil {
		return nil, err
	}
	if shallow {
		return w.Git.Tags(ctx)
	}
	return w.Git.MergedTags(ctx, "HEAD")
}

// tagBase is the compare link's base for a --since-tag value typed by the
// caller: the value, when refs/tags/<value> exists, else "". The resolved
// forms (auto, below:) read their answer out of the tag listing and need no
// asking. A typed value can be a branch, a remote-tracking name, a sha or a
// revision expression, and GitHub resolves such a spelling against ITS refs,
// not the checkout's — measured 2026-10-05 on glyph: `main...<sha>` 200 and
// identical, `origin/main...<sha>` 404 — so only a tag is the walk's base on
// both sides.
func (w Walker) tagBase(ctx context.Context, value string) (string, error) {
	ok, err := w.Git.IsTag(ctx, value)
	if err != nil || !ok {
		return "", err
	}
	return value, nil
}
