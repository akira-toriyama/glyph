package walk

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// Auto is what a bare --since-tag parses to (the flag's NoOptDefVal):
// resolve the walk base from the repository itself — the highest parseable v*
// tag HEAD contains (LatestVersionTag) — so a release job never duplicates
// glyph's version-tag policy in shell.
const Auto = "auto"

// below is the prefix of the OTHER resolved --since-tag form,
// --since-tag=below:TAG: the walk base is the highest parseable version tag
// HEAD contains STRICTLY below TAG — the predecessor of a release that already
// has its tag, the job auto cannot serve at tag-push time (glossary, "below:";
// t-s5n4 for the shell derivation this retired). git forbids ':' in a
// refname, so no real tag can spell the sentinel.
const below = "below:"

// CheckTag rejects a malformed --since-tag value before the walk runs —
// caller input, so usage (2), as cli's checkRangeFlag answers --range: the
// same mistake must not surface as a retryable-looking git failure (4). cli
// calls it at the entrance, before the repository is resolved, and release
// before its ref guard too; Since takes the value as accepted. The auto
// sentinel is not a name and skips the checks.
func CheckTag(tag string) error {
	if tag == Auto {
		return nil
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(tag), below); ok {
		// The bound must NAME a version — a predecessor is found by comparing
		// under it — and a release candidate names the release it is a
		// candidate for (bump.ParseBaseTag, not ParseVersion: goreleaser
		// hands this flag $GITHUB_REF_NAME on a `tags: ['v*']` trigger, and
		// v3.0.0-rc.1 once died here at exit 2 behind a tag that already existed
		// — TestSinceTagBelowAcceptsAReleaseCandidate). Candidates stay out of
		// the ANSWER set: LatestVersionTag parses every tag with ParseVersion.
		// The bare prefix (a workflow templating an unset variable) dies here as
		// the caller's input, like the empty tag below. Parsed ON ITS LINE:
		// whether the line a bound names is declared is resolveLinesScoped's
		// question, asked once the config is loaded.
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

// sinceTagRange turns the --since-tag value into the single line's revision
// range and, when the tag names a version, the version the bump steps from.
// Both come from ONE resolution, so the walk base and the step base are the
// same tag by construction: stepping from a different tag would version a
// verdict computed over another range. Auto and below: read the candidates
// (LatestVersionTag), and a HEAD whose history holds no such tag walks the
// whole history from v0.0.0; an explicit tag is taken as named wherever it
// sits — naming a tag names the release being redone — and names no base
// when it is not a version (nil: the bump falls back to the highest v* tag
// HEAD contains). baseTag is the range's left side when it is a tag, in its
// own spelling (Line.BaseTag): the resolved forms' answer, a typed value only
// when it is a tag (tagBase), "" on the whole-history arm.
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
	if rest, ok := strings.CutPrefix(tag, below); ok {
		// This is the single line, so a bound on any other line names a line
		// this repository does not have (resolveLinesScoped answers for
		// declared packages, and never reaches here with one).
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
// names, v its base version (a candidate's triple, ParseBaseTag).
// CheckTag calls it to refuse the flag before anything runs; sinceTagRange
// and resolveLinesScoped call it for the values, so the refusal has one
// sentence and the two resolvers carry none of their own.
func belowBound(rest string) (bound, prefix string, v bump.Version, err error) {
	bound = strings.TrimSpace(rest)
	prefix, v, perr := bump.ParseBaseTag(bound)
	if perr != nil {
		return "", "", bump.Version{}, core.Usagef("--since-tag=below: needs a version-shaped tag to resolve the predecessor of, got %q (%v)", rest, perr)
	}
	return bound, prefix, v, nil
}

// walkCap bounds the whole-history walk: the most commits an untagged
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
const walkCap = 200

// flagEscape is the remedy for a caller that HAS --since-tag. It is a
// parameter rather than a constant in the message because `preview` does not:
// its flags are --pr/--repo/--notes/--json, and a refusal that sends an
// operator to a flag the command rejects is a dead end (t-60dc, measured
// 2026-09-15: `preview --pr` exited 4 naming --since-tag=TAG).
const flagEscape = "; name the walk base yourself with --since-tag=TAG (cutting a tag at the intended base first if none exists)"

// wholeHistory is the untagged arm of a RESOLVED --since-tag (auto with no
// version tag in HEAD's history, below: with none under its bound): the whole
// history, walked — never skipped, because bump, notes and release are
// ANSWERING what the first release is (a skip folds zero commits and deletes
// the first rolling draft), and never unbounded in silence (DESIGN §4.1,
// "Only the untagged arm is capped"; t-354v). whyNone is stated of HEAD's
// history, never of the repository: "no version tag found" beside a
// `git tag -l` that lists one is the sentence t-gt9n forbids. The commits the
// walk would visit are counted FIRST, under the author exclusion the walk
// itself applies, and past walkCap the refusal is CodeAPI — nothing is broken
// underneath, no retry clears it — with the escape in the message; the count
// is exact, so the refusal names the real cost, and the second `git log` this
// arm spends is local and cheap.
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
	if walked > walkCap {
		if escape == "" {
			escape = flagEscape
		}
		return "", nil, core.APIf(
			"%s, and walking this repository's whole history would ask GitHub about %d commits, one round-trip each (the cap is %d) — refusing the unbounded walk%s",
			whyNone, walked, walkCap, escape)
	}
	// One API round-trip per commit over everything — a cost the caller
	// should see named (house rule: no silent unbounded work).
	w.Report.Warnf("%s — walking the repository's whole history (%d commit(s) the walk will ask GitHub about)", whyNone, walked)
	return "HEAD", &bump.Version{}, nil
}

// LatestVersionTag returns the highest parseable version tag ON ONE LINE and
// its parsed version; tag is empty for a line before its first release. The
// one resolver behind both the walk base and the step base, so every caller
// inherits one rule. l is the line asked about — l.Prefix its tag namespace,
// l.Holds its majors, which keeps pubsub/ and pubsub/v2 apart on one prefix —
// and a tag on any other line is not a candidate, so one line's release can
// never move another's base (DESIGN §4.1; mutation row
// packages-tag-of-one-line-baselines-another). below, when non-nil, bounds
// the answer to versions STRICTLY under it — the BOUND, not "the highest
// other tag": a v0.8.3 hotfix beside v0.9.0 resolves v0.8.2 — and compares as
// a version, so both spellings of the bound's own tag fall out together.
//
// EVERY tag is parsed and compared as a VERSION: git's --sort=-v:refname is a
// refname order, not a version order (gitsource.Checkout.Tags; glossary, "version
// sort"), and a wrong base is the silent-wrong-verdict class. A tie between
// two spellings of one version keeps the FIRST in git's order, so the answer
// stays deterministic without a preference git does not have. The candidates
// are the tags HEAD's history holds (releasesHEADHolds; DESIGN §4, "The walk
// base is a release HEAD contains", which also prices the --merged call a
// packages walk pays once per line).
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

// releasesHEADHolds is LatestVersionTag's candidate set: the tags HEAD's
// history holds. A shallow checkout cannot say — --merged stops at the depth
// — so it reads every tag, and the walk records the checkout as unread
// (Facts.Shallow), which release refuses (DESIGN §4; mutation row
// shallow-checkout-resolves-from-a-truncated-history).
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
// caller: the value when refs/tags/<value> exists, else "" — a branch, a
// remote-tracking name or a sha is resolved by GitHub against ITS refs, not
// the checkout's (DESIGN §4, "The compare link"). The resolved forms read
// their answer out of the tag listing and need no asking.
func (w Walker) tagBase(ctx context.Context, value string) (string, error) {
	ok, err := w.Git.IsTag(ctx, value)
	if err != nil || !ok {
		return "", err
	}
	return value, nil
}
