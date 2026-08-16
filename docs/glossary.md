# glyph — glossary

The vocabulary glyph's code, its doc comments and its task bodies use as terms of
art. It exists to stop the user and Claude Code from meaning two different things
by one word: most of the entries below are pairs that are easy to collapse and
expensive to collapse (*merge point* vs *merge commit*, *residual* vs *stale*
draft, *fail* vs *unknown*, *neutralize* vs *escape* vs *fence*), and the
**distinction is the content** — an entry that only restated the name would be
worth nothing.

**Ordering: grouped by area, not alphabetical.** Almost every term here is only
meaningful in relation to its neighbours — *merge point → canonical commit →
footprint → landed → covered → lost* is one chain, and alphabetical order would
shred it into six unrelated stubs. Each term appears in **bold** exactly once, so
Ctrl-F on the word is as good as an index.

Each entry ends with where it lives: a file and a *symbol*, never a line number
(line numbers rot; this file was written at `d0ec9a7`). Cited paths are from the
repository root; the two documents it links, [`DESIGN.md`](DESIGN.md) and
[`commit-convention.md`](commit-convention.md), are its neighbours in `docs/`.
Where an entry quotes a figure or an observed render
("14 of them are deleted", "GitHub links that"), it is the measurement **recorded
in the tree** at that symbol, with the date the doc comment gives — not one re-run
while writing this glossary. The renderer probes in particular are dated
2026-07-21 and are worth re-running before changing a rule.

Maintaining it: a term is added or renamed in the **same PR** as the code change
that introduces or renames it, and when a cited symbol disappears its entry goes
with it. A glossary that documents a name the tree no longer has is worse than a
missing entry, because it is the one place a reader trusts not to be stale.

**Contents**

1. [The release walk](#1-the-release-walk) — walk, walk base, auto, below:, range, fold, participate, merge point, canonical commit, footprint, landed, stand aside, covered pull, lost pull, expansion, provenance, fallback path, API lag, shallow checkout, truncated listing, incomplete walk, walkFacts, Dropped, shortfall, wedge, wedge escape
2. [Verdicts and the rolling draft](#2-verdicts-and-the-rolling-draft) — verdict, level, source, reason, target, action, rolling draft, glyph-managed draft, residual draft, stale draft, published floor, pending, incomplete banner
3. [Convention and lint](#3-convention-and-lint) — rules table, bump lattice, section, rule id, legacy token, merge candidate, generated subject, cleanup
4. [The render boundary](#4-the-render-boundary) — inline context, phantom span, neutralize, escape, fence, flatten, pipe escape, over-escaping is the safe direction
5. [Repository preconditions (`doctor`)](#5-repository-preconditions-doctor) — check, check id, pass/fail/advice/unknown, release-tag pin
6. [Exit codes and streams](#6-exit-codes-and-streams) — gate code, soft no-release, annotation, error envelope
7. [Fleet distribution](#7-fleet-distribution) — fleet, reusable workflow, composite action, distribution layer, dist-gate, merge preview, sticky comment

---

## 1. The release walk

The `--since-tag` machinery: the third input source, and the only one that
combines local git with the API.
[DESIGN §4](DESIGN.md#4-squash-safe-mechanism--release-time-re-read-stateless)
is its long form.

**walk** — the release-time re-read: read `BASE..HEAD` from *local* git, resolve
each commit to the merged pull request it is the merge point of, and expand that
pull into its individual (pre-squash) commits. Nothing is persisted, so a release
is idempotent and self-healing. Distinguish from the other two input sources,
which are not walks in this sense: `--range` classifies commit *messages* out of
git and never calls the API, and `--pr` reads one pull request's commits over the
API and knows nothing of any range. "Exactly one of the three" is `bump` and
`notes` only — they are the two commands that carry all three flags and mark them
mutually exclusive. `release` has just the walk (bare `release` walks from the
highest v* tag, so no flag is required at all), and `preview` takes `--pr`, which
it marks required. `internal/cli/sincetag.go: walkSince`, `internal/cli/range.go:
participatingCommits`, `internal/cli/pr.go: participatingPull`,
`internal/cli/sincetag.go: markInputSourceFlags` (used by
`internal/cli/cmd_bump.go` and `internal/cli/cmd_notes.go` alone)

**walk base** — the tag the walked range starts at. Distinguish from the **step
base** (also *version base*): the version the bump steps *from*. They are
deliberately returned by one resolution of one tag, because naming a tag names
the release being redone — stepping from a different, higher tag would version a
verdict computed over another range. An explicit tag that is not a version still
walks but names no step base, and the bump then falls back to the highest v* tag.
`internal/cli/sincetag.go: sinceTagRange`, `internal/cli/cmd_bump.go:
currentVersion`

**auto** — what a bare `--since-tag` (no value) resolves to: the highest
*parseable* version tag. Distinguish from "git's first tag": every tag is parsed
and compared **as a version**, because `--sort=-v:refname` is a refname sort that
orders on the leading byte first — measured on `{v0.0.1, v0.0.2, 9.9.9, 100.0.0}`
git reports `v0.0.2` first. Taking git's order moved the walk base and the
version base together to a tag two releases behind, silently. A tag must be
attached with `=` (`--since-tag=v1.2.3`); the space form is caught as a usage
error rather than walking the wrong range.
`internal/cli/sincetag.go: sinceTagAuto, latestVersionTag, sinceTagArgs`,
`internal/gitsource/gitsource.go: Tags`

**below:** — the other *resolved* `--since-tag` form
(`--since-tag=below:TAG`): the highest parseable version tag **strictly below**
TAG's version — the predecessor of a tag already cut. It answers the tag-push
question auto cannot: at tag-push time the new tag *is* the highest v\* tag, so
auto would walk an empty range. Strictly below the bound, not "the highest
other tag" — cutting a v0.8.3 hotfix while v0.9.0 exists resolves v0.8.2. With
no version tag below the bound (the first release), the walk covers the whole
history, same as auto before the first tag — bounded in both forms by the
release-floor cap: past `sinceTagWalkCap` walk-visible commits the walk is
refused (fail-loud 4) with the escape in the message, because it pays one API
round-trip per visited commit and nothing else bounds it. The bound must itself be
version-shaped (usage error otherwise), and the sentinel cannot collide with a
real tag: git forbids `:` in refnames. This resolution used to be re-derived in
shell inside `goreleaser.yml` and inherited the refname-sort defect above
(t-s5n4). `internal/cli/sincetag.go: sinceTagBelow, sinceTagRange`

**range** — the `TAG..HEAD` revision range string (the code calls it `revRange`,
and reports it as `source`). `--since-tag` names the **tag alone**; the walk
appends `..HEAD` itself, so a value containing `..` is rejected as usage.
`internal/cli/sincetag.go: checkSinceTagFlag`

**fold** — the max-reduce of commit levels over the bump lattice. It is
order-independent and idempotent (fuzz-pinned), so squash order can never change
a version. The word is used for both the operation and its input set — "an empty
fold" means the commit set came out empty, which is the ambiguity *walkFacts*
exists to resolve. `internal/bump/bump.go: Reduce`

**participate** — a commit that survives the participation rules and reaches
classification. An excluded commit is *skipped, never a violation*. Two
exclusion questions are kept rigorously apart, and conflating them is the t-7zt7
defect: `ExcludedFromClassification` asks "is this commit's own **message**
classifiable?" (bot authors, 2+ parents, git-generated subjects) and governs
lint, the `--range` walk and the fallback path; `ExcludedFromResolution` asks
"can this commit on main **point at** a merged pull request?" and takes the
**author alone** — a merge point's subject and parent count are how GitHub
*shapes a pointer*, not evidence about it. `internal/bump/bump.go:
ExcludedFromClassification, ExcludedFromResolution`

**merge point** — whatever commit GitHub named `merge_commit_sha` for a pull
request: the squash commit, a rebase-merge's **last** replayed commit, or (the
merge-commit button) the merge commit itself, whose `merge_commit_sha` is its own
sha. One equality resolves all three styles. Distinguish sharply from **merge
commit**, which is a *shape* (2+ parents) — a merge point need not be a merge
commit, and judging the shape before resolving is what dropped whole pull
requests out of releases silently (t-7zt7). `internal/cli/sincetag.go:
mergedPullFor`

**canonical commit** — the same object seen from the walk's side: the *walked*
commit that resolved to a pull request, and the anchor the footprint mapping
needs. Only a resolved canonical commit may expand a pull.
`internal/cli/sincetag.go: foldPull, mainFootprint`

**footprint** — the answer, per commit in a pull's API listing, to "which commit
on the released branch did this land as" — decided from **local git alone**,
because GitHub reports no merge method. Three shapes: the merge button leaves
commits on the branch verbatim (a listed sha the repository holds *and* that is
an ancestor of HEAD landed as itself); a rebase rewrote them all but preserved
their messages and order and named the last as `merge_commit_sha`, so the N
first-parent commits ending there align positionally — an alignment **verified
message by message and abandoned whole** if any differs; a squash left no
footprint at all, and the pull alone governs. This is what lets the walk's
*range* (a git fact) govern a listing that knows nothing about it; without it a
tag cut inside a landed pull's footprint folded already-shipped commits into the
next release (t-8xsb — exit 0, empty stderr, a minor invented out of released
work). `internal/cli/sincetag.go: mainFootprint`, `internal/gitsource/gitsource.go:
Have, IsAncestor, FirstParentLog`

**landed** — a listed commit's on-branch identity, or empty when it landed under
no identity of its own. A landed commit is folded only if its landing site is in
range, and it is folded **under the on-branch sha** — which is also why a
rebase-merged pull no longer puts pre-rebase shas, which exist on no branch, into
the notes. The notes cite the pull *beside* that sha (`(#123, abc1234)`); a
commit that landed under no identity of its own cites the pull *alone*
(`(#123)`), because its listed sha is exactly such a pre-squash sha and the pull
is the one address that outlives the squash (t-xxhj).
`internal/cli/sincetag.go: mainFootprint, foldPull, notesCommits`

**stand aside** — what a walked commit does when it reports itself carried by a
pull whose merge point is some *other* sha in this range: it is not counted,
because that merge point is supposed to expand the whole pull. Safe only if
something stands in — hence the covered/lost reconciliation below.
`internal/cli/sincetag.go: mergedPullFor` (its `covering` return)

**covered pull** — a merged pull a walked commit stands aside for. `covering` is
reported by **number**, not as a bool, so the end of the walk can reconcile
"pulls something deferred to" against "pulls actually expanded"; a bare bool let
a merged pull disappear whenever its merge point failed to resolve (t-7zt7).
*Covered* is gated on the pull's canonical commit being **in this range**:
without that gate it is not a claim about this release at all, since a pull
merged into another base branch is associated with commits that reached main
another way. `internal/cli/sincetag.go: mergedPullFor` (`covering`, `inRange`)

**lost pull** — a covered pull that was never expanded, because its canonical
commit never resolved (GitHub had not indexed the merge commit yet, or an
automation authored it and the author gate skipped it before the API). The walk
**warns and deliberately does not expand it**: a listing carries nothing about
the range, so recovering it would be guessing, and the guess was measured wrong
two ways (a rebase-merged pull renders twice; a pull whose earlier commits
shipped under the previous tag manufactures a minor out of released work). The
release is genuinely short by whatever that pull changed. Note what the warning
does *not* promise: API lag clears itself and warns once, but a repository whose
merge button an **automation** presses warns on every release, structurally.
`internal/cli/sincetag.go: walkSince` (the `coveredOrder` reconciliation),
`walkFacts.LostPulls`

**expansion** — turning one resolved pull request into its individual commits and
folding them in, after two filters applied *before anything is parsed*: the
footprint filter and the walk-wide seen-sha filter. `internal/cli/sincetag.go:
foldPull`

**provenance** (*expansion provenance*) — the walk reporting on itself: per
resolved pull, its number and how many participating commits it contributed,
published as `.pulls` on the `--json` verdict so a human or a CI step can audit
how a verdict was assembled without re-deriving the exclusion rules in shell. It
records what the walk **did** and never why a number is what it is: a count of
**0** has two innocent causes (a stacked pull whose commits rode in with its base
pull; a merge-merged pull whose commits the fallback already folded), so 0 never
means "this pull changed nothing". `internal/cli/sincetag.go: pullExpansion,
walkFacts.Pulls`

**fallback path** — where a walked commit that resolved to *no* merged pull
request is classified from its **own message** (a direct push, a local `git
merge`, or the API not knowing the sha yet). Leniency is confined here and
nowhere else: an unparseable message or an unknown code emits a `::warning::` and
counts **none** — never a silent patch, never exit 3 (the hard unknown-code error
stays with the lint gate; t-kbqx, so `internal/bump` stays pure). One exception
(ratified Q10): a **breaking marker is never suppressed** — an unknown code
carrying one counts major, normalized to `:boom:` so it folds and hoists into
Breaking Changes. The asymmetry is deliberate: a typo can over-bump a version, a
breaking change must never be silently dropped from one.
`internal/cli/sincetag.go: fallbackCommit, fallbackReason, gitmojiBoom`

**API lag** — GitHub answering **422** for a sha it does not know yet, which is
what a walk running seconds after a push meets. It is the **only** status that
reaches the fallback path: a 403 rate limit, a 5xx that outlived the retry
schedule (t-bjrv) and a dead socket leave the walk as an error and exit 4 — the
outage window is an exit-code question, not a classification one. Deliberately
not true of a 404, which is how a bad credential answers for *every* commit of a
private repository. `internal/github/github.go: IsCommitUnknown`

**shallow checkout** — a `--depth` clone, in which git cannot answer the
footprint question at all: a commit it does not *have* is indistinguishable from
one that never landed. The only member of `walkFacts` that is a property of the
**checkout** rather than of an API answer, and the likeliest to appear
(`actions/checkout` defaults to `fetch-depth: 1`). Probed once per walk, before
any expansion, because a walk where nothing resolves is still a walk over a
truncated history. `internal/gitsource/gitsource.go: IsShallow`,
`walkFacts.Shallow`

**truncated listing** — a pull whose commit listing came back at GitHub's hard
cap of **250**, however far pagination follows. A listing of exactly the cap is
one glyph cannot claim to have read whole: the commits past it are *unreachable,
not absent*, and any of them could carry the deciding gitmoji.
`internal/github/github.go: PullCommitsCap`, `walkFacts.Truncated`

**incomplete walk** — a walk that came back knowing it had **not** read the whole
range. The distinction it exists to make: downstream, a range that genuinely
holds nothing and a range the walk could not read arrive as the *same* empty
fold, and glyph acted on both alike — it deleted the rolling draft on a reading
it had just told the operator to re-run (t-441z), and retagged an existing v1.0.0
draft down to v0.1.1 out of a fold missing the `:boom:` pull. So `release`
**fails loud (4)** on one, before the releases listing is even fetched: no
delete, no retag, no draft, no verdict — the same refusal to judge an unread
range as the wedge and the covered pull (ratified t-pysg, replacing #66's
warn-and-refuse-to-destroy). The commands that only *report* — `bump`, `notes`
— warn per cause instead, and `preview` carries the shortfall in its comment
body. `internal/cli/sincetag.go: walkFacts.complete`,
`internal/cli/cmd_release.go: releaseRun`

**walkFacts** — the struct carrying the above: `Pulls` (provenance) plus the
**five** ways a walk can come back short — `AllUnknown` (every commit unknown to
the queried repository, which is what a wrong `--repo` or an inherited
`$GITHUB_REPOSITORY` looks like from inside), `Shallow`, `LostPulls`, `Dropped`,
`Truncated`. `complete()` is exactly "none of the five". `internal/cli/sincetag.go:
walkFacts`

**Dropped** — the narrowest of the five, and the one whose name over-promises.
Nothing is recorded unless the commit reached the fallback path through *API lag*
— the 422 arm, where GitHub did not know the sha. Under that condition three
outcomes record: a merge commit excluded on its parent count (a merge point that
may have carried a whole pull), a message that does not parse, and a code that is
not in the rules table and carries no breaking marker. Every other fallback
records nothing, including the same three shapes reached because the commit simply
has no pull request — a direct push or a local `git merge` is a message glyph
**read** and judged, not evidence it could not obtain. So `Dropped` is not "the
fold used second-best evidence"; it is "something is missing from the fold, and
only GitHub can supply it". `internal/cli/sincetag.go: fallbackCommit` (the
`dropped` closure, gated on `fallbackReason.lag`)

**shortfall** — the one-clause sentence naming what the walk could not read, so
the same fact can be spliced into a `::warning::`, into the draft body a human
reads days later, and into the PR comment's caveat. `internal/cli/sincetag.go:
walkFacts.shortfall`

**wedge** — the state a repository is in when a commit that bypassed the lint
gate sits inside a *resolved* pull request: the lint failure stays hard (exit 3,
ratified Q1/t-2nzf — never a silent patch), and published history is immutable,
so the **same** failure recurs on every future release until the walk starts past
it. `internal/cli/sincetag.go: wedgeHint`

**wedge escape** — the base that clears a wedge, and which one depends on whether
the offending commit is somewhere a tag can be cut. A merge- or rebase-merged
pull put its commits on the released branch, so a base **at or past the offending
commit** clears it — the nearest escape, which matters because every commit
between it and the merge point is one a farther base would silently drop. A
**squash-merged** pull has no such commit: its commits exist only over the API
and its one commit on main *is* its merge point, so nothing short of a base at or
past that merge point works. The error message names the pull, the merge point,
the base and why that base is the one. `internal/cli/sincetag.go: wedgeHint,
onMain`

---

## 2. Verdicts and the rolling draft

**verdict** — the composed answer of a verdict command: the classified commits,
the folded level and the reason (plus tag, target, body and action for `release`).
`release` computes it **once** and derives both the version and the notes from
that single commit set — calling `bump` and `notes` separately walks twice, and a
merge landing between the walks could version one range and describe another.
`internal/cli/cmd_bump.go: classifyVerdict`, `internal/cli/cmd_release.go:
releaseResult`

**level** — a rung of the bump lattice for one commit or one fold (`none`,
`patch`, `minor`, `major`). Distinguish from **breaking**, which is an orthogonal
non-suppressible boolean, not a rung. `internal/gitmoji/gitmoji.go: Bump`

**source** — the human name of what the verdict was computed over: a revision
range, `owner/name#N`, or the walked range. Where it surfaces is narrower than the
name suggests. It is spliced into the reason line of a *none* verdict ("N
commit(s) participate in *source* and every level is none"), and into the
sentences a run writes when the walk could not read the range; an ordinary release
verdict's reason names the deciding commit instead, and the string does not appear
at all. It is also not a key on any `--json` surface — none of `{current, level,
next, commits, reason}`, `{sections, reason}`, `{current, level, tag, target,
body, action, url, commits, pulls, reason}` or `{current, untagged, level, next,
pr, pending, body}` carries it, so a consumer that wants to know what was walked
reads the flags it passed, not the verdict. `internal/cli/cmd_bump.go: bumpInput`,
`internal/cli/cmd_notes.go: notesInput`, `internal/cli/pr.go: pullInput`

**reason** — the one-line answer to "why this bump": the **oldest** commit that
reaches the folded level. `internal/cli/cmd_bump.go: decidingReason`

**target** — `target_commitish`: the sha the draft's eventual tag will point at,
defaulting to the checkout's HEAD. No tag exists until a human publishes. The
verdict carries it as `target` — resolved on the dry run too, so a `--target`
typo is visible in the preview rather than first surfacing on the real write.
`internal/cli/cmd_release.go: releaseRun`, `internal/gitsource/gitsource.go: Head`

**action** — which draft convergence the run performs: `create`, `update`
(grow/retag), `delete`, or `none`. `--dry-run` computes the action too and writes
nothing. `internal/cli/cmd_release.go: actionCreate…actionNone`

**rolling draft** — the **one** glyph-managed unpublished draft release a
repository keeps, created or updated **by release id** and retagged in place when
the next version moves. Never a second draft. By id and not tag name because
tag-name resolution can hit a *published* release sharing the draft's intended
tag (cli/cli#9367). `internal/cli/cmd_release.go: planDrafts`,
`internal/github/release.go: UpdateRelease`

**glyph-managed draft** — an unpublished draft whose tag name is the house shape
`vX.Y.Z` (with the `v`). Published releases and a human's hand-named drafts are
never glyph's to touch. `internal/cli/cmd_release.go: glyphDrafts`

**residual draft** vs **stale draft** — both are glyph-managed drafts glyph
removes, in two different situations, and the words are not interchangeable in
this codebase. A **residual** draft is one that survives a **none** verdict —
nothing should exist, so it is deleted and the draft state converges on "no
release is due". A **stale** draft is one of the *others* when the verdict **is**
a release: one draft is kept and retagged, every other one is stale. An
incomplete walk deletes neither — it exits 4 before the listing is read. The
asymmetry runs one step further, and it is the same distinction: a stale draft
is removed **after** the rolling draft is written, and one the API will not
delete is a warning on a green run, because the verdict has already landed and
the next run converges it. A residual draft's delete *is* the whole action of a
none verdict, so it still fails loud (4).
`internal/cli/cmd_release.go: releaseNone`
(residual), `planDrafts` (stale), `convergeStrays` (the post-write pass),
[DESIGN §4](DESIGN.md#4-squash-safe-mechanism--release-time-re-read-stateless)

**published floor** — the highest **published** (non-draft) house-shaped version.
The next version must be *strictly* greater, or the draft could never be
published: its tag is taken, or permanently burned if a published release was
deleted. Fails loud (4) rather than creating an unpublishable draft.
`internal/cli/cmd_release.go: checkPublishedFloor, highestPublished`

**pending** — what is already merged on the base branch but **not yet released**:
the walk's own verdict since the latest tag, and one of the two sides the merge
preview folds. Distinguish from a draft that is "pending publication", which this
codebase does not call pending. A repository with no v* tag has an *uncomputed*
rather than empty pending side — walking it would cost an API round-trip per
commit of the whole history for an answer that cannot matter — and the comment
says so. `internal/preview/preview.go: Input.Pending, Input.Untagged`,
`internal/cli/cmd_preview.go: previewRun`

**incomplete banner** — retired with t-pysg: the `> [!WARNING]` block an
incompletely-walked draft used to carry at the top of its body, from the era
when `release` still wrote drafts on an incomplete walk (#66). `release` now
fails loud (4) before writing anything, so no draft composed from an unread
range exists to mark. The need it served survives where the reader still gets a
positive claim from a short walk: `preview`'s PR comment, via
`internal/preview/preview.go: Input.PendingShort`

---

## 3. Convention and lint

*The convention itself is not documented here.* It is shared by every repository
under this account and lives in exactly one place, which is the whole point of
[`commit-convention.md`](commit-convention.md) — a pointer to the canonical copy in
`akira-toriyama/.github`. The gitmoji code and the subject shape, the three
breaking markers, the removal codes and the `NON-BREAKING:` footer are normative
*there*, and restating them here would be the second source of truth that pointer
exists to prevent. What remains below is only glyph's implementation vocabulary:
the words for the machinery that reads the convention and enforces it.

**profile** — the bundle one commit vocabulary owns: a subject **grammar** (the
shape `Parse` and `Lint` judge under, selected by `parser.Grammar`), a token →
bump table, and the lint rules that only make sense inside that vocabulary. Two
exist: `gitmoji` — the default and the fleet's own, deliberately the zero
`Grammar` value so a caller that states nothing means what every caller meant
before profiles existed — and `conventional` (`<type>[(scope)][!]: <subject>`,
ratified 2026-08-16 for adopter repositories — external organizations taking
glyph up — where a gitmoji vocabulary cannot be imposed). Everything a profile does *not* own is one shared code path — the
footer walk, the trailer-block rules, git's cleanup, the release walk, the
fold, the exit codes — which is what keeps the two profiles' verdicts on an
identical body identical, and is the half of the design easiest to get wrong:
the profile is the subject line's business only. Ratified in
[DESIGN §2, §2.2, §3.1 and §6](DESIGN.md).
`internal/parser/parser.go: Grammar, GrammarGitmoji, GrammarConventional`

**rules table** — one profile's embedded token → semver mapping: **75** codes
for the gitmoji vocabulary, **11** types for the conventional one, each
embedded with `//go:embed` so the pinned binary *is* the pinned rules (no
separately synced config can drift). One table ENGINE — validation, lookup,
the renderers — lives in `internal/gitmoji`, parameterized by a per-vocabulary
`Spec`, so what a well-formed table is can never fork between profiles;
`internal/conventional` is data plus its count pin only, and its rows are
*derived* (each type takes its canonical gitmoji counterpart's bump and
section, pinned by the derivation test). Membership is injected into the
parser, so the grammar and the table evolve separately; an unknown token is a
hard lint error, never a silent patch. Print either with `glyph rules`
(`--md`, `--json`; the profile flag selects the vocabulary).
`internal/gitmoji/gitmoji.go: Table, Spec, ParseTable, Load, CodeCount`,
`internal/conventional/conventional.go: Load, TypeCount`

**bump lattice** — `none(0) < patch(1) < minor(2) < major(3)`. `:boom:` is the
only code that auto-majors and `:sparkles:` the only one that auto-minors;
capability-adjacent codes (i18n, offline, a11y, UX) deliberately stay patch so an
AI author cannot accidentally minor a routine change.
`internal/gitmoji/gitmoji.go: Bump.Rank`

**section** — the release-notes group a code carries (13 in the table, in render
order). Notes inclusion tracks the **section, not the bump**: a removal is
bump-none and still surfaces under *Removals*, so a deletion or rename is visible
to a human audit even though the version does not move. A code with no section
drops out of the notes — *unless the commit is breaking*, and that test comes
first: `Group` hoists a breaking commit into Breaking Changes before it ever looks
at the code's section, so a sectionless code carrying a breaking marker still
renders. `internal/gitmoji/gitmoji.go: Rule.Section`, `internal/notes/notes.go:
Group`

**rule id** — the stable kebab-case key a lint finding carries. It is machine
API — CI jobs and agents branch on the `rule` key of a `--json` finding, never
on the `detail` beside it, which is prose and will be reworded. A mechanical
repair, where one exists, is the finding's `fix` key — the corrected subject
line, paste-and-pass — not a phrase to regex out of `detail`. The same
discipline as `doctor`'s check id, one layer down. The ids themselves are
enumerated in [DESIGN §2](DESIGN.md#2-commit-format), the copy
`TestDesignDocNamesEveryRuleID` holds in step with the `Rule*` constants — this
entry stopped carrying its own list after shipping one that was a rule short
(`legacy-token` arrived in #94 and no gate read this file) — and self-printed
by `glyph rules --lint`: each id with `merge_candidate_only`, no prose, so an
agent looks the vocabulary up from the pinned binary it is talking to.
`internal/parser/parser.go: Violation, RuleMalformedSubject…RuleUndeclaredRemoval`,
`internal/cli/cmd_lint.go: rangeViolation`

**legacy token** — the retired Conventional `<type>[(scope)][!]: ` token that
pre-glyph history carries *between* the gitmoji and the subject. Handled by
surface since v1.0.0: the release **walk** still eats it (scope salvaged when
the canonical slot has none, its `!` still meaning breaking) so immutable
history keeps parsing and bumping, while the authoring **lint** rejects it as
the hard error `legacy-token`, with the canonical rewrite in the detail when
one exists. The type vocabulary is closed on purpose, so an ordinary subject
with a colon (`:memo: note: …`) is not eaten. The old load-bearing asymmetry —
the legacy scope slot is `[^()]+` while the canonical one is lowercase
kebab-case, so only the legacy path could deliver a scope with anything to
escape — is thereby closed at authoring time: no legacy slot reaches it.
`internal/parser/parser.go: legacyTokenRE`

**merge candidate** — a commit on its way into main (a PR range), where
`:construction:` is a violation. At **authoring** time — the commit-msg hook and
`--message` — it stays legal, because its verdict genuinely changes with time.
The pre-push hook sits between the two and splits the difference: the rule fires
over the outgoing commits, and only the *consequence* is gated on the ref being
the remote's default branch, since a push to a topic branch is by construction
still mid-branch. `internal/parser/parser.go: LintOptions.MergeCandidate, wipCode`,
`internal/cli/cmd_lint.go: lintOne, lintRangeRun, lintRaws`,
`internal/cli/prepush.go: prePushRun`

**generated subject** — a subject prefix git or GitHub writes itself (`Merge `,
`Revert `, `fixup! `, `squash! `). Such commits carry no author-chosen gitmoji
and are excluded rather than failed — an author cannot rewrite a subject git
generated, so judging them at the hook would leave `--no-verify` as the only
escape, which turns the whole gate off. `internal/bump/bump.go:
generatedSubjects`

**cleanup** — the reduction of a raw commit-message **file** (what git hands a
commit-msg hook, still holding the editor template, the status block and, under
`commit.verbose`, a scissors line with the whole diff) to the message git will
actually record. Only the authoring path (`--stdin`) calls it: a `--range` walk
reads messages git has already cleaned, and running it there would swallow a
genuinely empty message and any body line starting with `#`.
`internal/parser/cleanup.go: Cleanup`

**cleanup mode** — *which* cleanup, of git's five: `verbatim` (none),
`whitespace`, `strip` (whitespace + comment lines), `scissors`, and `default`,
which is not a cleanup but a choice between `strip` and `whitespace` by whether a
message is **edited**. glyph resolves it per commit from `commit.cleanup` and
`GIT_EDITOR`; a mode assumed instead of resolved is how the hook and CI came to
disagree about the same message. DESIGN §2.1.
`internal/parser/cleanup.go: CleanupMode, ResolveCleanupMode`,
`internal/cli/cmd_lint.go: hookCleanupMode`

**edited** — whether git will open an editor for this commit, which decides both
the `default` mode and the scissors cut. The hook's only evidence is
`GIT_EDITOR=:`, git's marker for "no editor will run" (`-m`, `-F`, `--amend
--no-edit`); **unset** is not evidence of the opposite — `core.editor` and
`$EDITOR` both leave it unset — so unset counts as edited.
`internal/cli/cmd_lint.go: hookCleanupMode`

**cut line / scissors line** — the one literal git writes above the diff under
`commit -v`: `# ` followed by 24 dashes, ` >8 `, 24 dashes. Matched exactly and at
the start of a line, because git matches it that way; an indented lookalike is
neither a cut nor even a comment to git, so cutting there hides text git records.
`internal/parser/cleanup.go: cutLine, truncateAtCutLine`

---

## 4. The render boundary

*"Render boundary" is this file's umbrella name for `internal/markdown`'s
vocabulary, not a phrase the code uses.* Everything in the package is a **model
of a renderer no unit test can call**; every rule was measured against GitHub's
own renderer (`gh api -X POST /markdown`, mode=gfm) on 2026-07-21, with the probes
and their observed output kept in the test files beside the code they pin — the
mention table at the top of `markdown_test.go`, the escaped-form and raw-HTML
tables at the top of `escape_test.go`. The one time a rule was changed from
reasoning alone, the reasoning was wrong. Most of this vocabulary is the #61
hardening (t-j0c6), which is the whole of `escape.go` — flatten, neutralize,
escape. The fence half is older: the mention fence (today `escapeMentions`) and
`mention` arrived in #38,
which wrapped a would-be mention in a single backtick pair, and #54 made the fence
as long as the input demands (`longestBacktickRun`). Until now all of it existed
only inside those doc comments.

**inline context** — the rendered stretch a construct is parsed in: one
paragraph, or one table cell (measured: a stray backtick in one cell forms no
span with a backtick in the next). Mention-safety is a property of **the whole
assembled inline context** and of no single field in it — which is why the fence
runs last, over the assembled line: escaping fields separately sized the
subject's fence against the subject alone, and a backtick carried by the *scope*
stole it, producing a live mention out of a commit that lints clean today.
`internal/notes/notes.go: entryLine`, `internal/preview/preview.go: escapeCell`

**phantom span** — a code span glyph's backtick-only scanner believes in that
GitHub does **not** form, because some construct parsed earlier ate a backtick: a
URI autolink, a bare URL (GFM needs no angle brackets), a raw HTML tag with a
backtick in a quoted attribute, or a link destination. All four measured live
(t-dwra). The direction of harm is what matters: a mention *inside* a phantom
span reads as already-inert, is left raw, and renders as a live mention. The fix
is not a smarter scanner — teaching it those grammars would be a renderer inside
an escaper, and would still have been wrong — but removing the competing
constructs beforehand, which makes the backtick-only model exact **by
construction**. `internal/markdown/markdown.go: codeSpans, paragraphSpans`,
`internal/markdown/escape.go` (file header)

The next three are **three different operations**, applied at different times to
different kinds of value. Do not use them interchangeably.

**neutralize** (`escapeMarkup`, reached through `Line.Prose`) — over **prose**:
add backslashes to disarm the
inline constructs that can inject structure, point somewhere the author never
wrote, or *delete the author's own words*. Four rules, each testing a **byte and
never a grammar** — `<`, the extended-autolink triggers (`://` after
http/https/ftp, and the `.` of `www.`), `[`, and an entity-shaped `&`. Emphasis,
strikethrough and the author's own code spans are left working: a subject is
prose the author meant to be read. It only **adds** bytes, so the author's text
survives as a subsequence and the pass is a fixed point. Not theoretical: of the
16 fleet subjects carrying a `<`, 14 were being **deleted** by GitHub's
sanitizer (`<Chip>`, `Optional<Any>`, `[overlay.theme.<name>]`), and escaping
repairs those and regresses none. `internal/markdown/escape.go: escapeMarkup,
escapeProse`

**escape** (`escapeText`, reached through `Line.Text`) — over a **plain-text
field**, which in practice is the
commit *scope*: backslash every ASCII punctuation byte CommonMark lets a
backslash escape, minus two deliberate exclusions (`-`, which is a marker only at
the start of a line and would otherwise put a backslash in essentially every
scoped line in the fleet; and `@`, because `\@octocat` is a *live* mention and the
backslash would additionally trip the fence's own backslash guard). It is a flat
byte loop precisely because "this is a plain-text field" means no grammar applies
to it. **It is not idempotent** and a plain-text escaper cannot be — call it once,
at render, on the raw field. `internal/markdown/escape.go: escapeText, escapable`

**fence** (`escapeMentions`, reached through `Line.String`) — wrap a would-be
`@mention` in a backtick run so
GitHub renders it as a code token and links nobody. Wrapping is the **only**
neutralization GitHub honors: an entity (`&#64;`) or a backslash does nothing at
all, because mentions are attached by a **post-processor over the rendered
document**, where every one of those has already decoded back to an at-sign. The
fence is as long as the input demands — `longest authored backtick run + 1` —
because a single inserted backtick is *syntax* that pairs with the author's, which
fused a run of the author's words into a code span and pushed the mention back
out into prose (t-fbg3). A separating space is written where the fence would
otherwise touch a byte that consumes it. Left raw, such a token silently lists a
stranger under a release's Contributors and, in a PR comment, **notifies** them
(t-hykw). `internal/markdown/markdown.go: escapeMentions, mention,
longestBacktickRun`

**flatten** (`flatten`, the first thing `Line.Text` and `Line.Prose` do) — not
one of the three: it *replaces* bytes (every CommonMark line terminator,
including a bare CR, becomes one space) and therefore lives outside
`escapeMentions`, whose no-rewriting invariant a fuzz oracle enforces. It must run
**first**, because it is what *decides* the inline context — to an escaper a blank
line ends the paragraph and backticks on either side cannot pair, while in the
flattened line they can. A bare CR is a line terminator at GitHub, so a subject
carrying one ended the notes list item and the rest was parsed as a fresh block: a
commit could fabricate a release-notes section (t-bz0r).
`internal/markdown/escape.go: flatten`

**line builder** (`markdown.Line`) — the package's **only exported surface**:
`Raw` appends glyph's own markup, `Text` and `Prose` append author-supplied
fields through the passes above, `String` runs the fence over the assembled
line. Since #104 the escape order is no longer a contract a caller could break —
it is applied by construction inside the type, and the surface golden
(`internal/markdown/testdata/exported-surface.golden.txt`, five declarations,
all `Line`) is what keeps the passes unreachable from outside. Why that order is
the only safe one is argued once, at the type's doc comment.
`internal/markdown/compose.go: Line`

**pipe escape** — the fourth pass a preview cell goes through, after flatten,
neutralize and fence, and the only one that does not live in `internal/markdown`:
every `|` gets a backslash, so a subject cannot break the table the headline's
evidence rests on. It runs **last**, and the order is not cosmetic — it adds
backslashes, and an escaping backslash in front of a fence swallows the fence.
Deferring it costs nothing, because a pipe escaped inside a code span still renders
as a pipe. `internal/preview/preview.go: escapeCell`

**"over-escaping is the safe direction"** — the package's one design rule, which
a later "let me make this more precise" pass must not violate: **every
inexactness falls on the over-escaping side**. A backslash in front of a byte
that was inert anyway is invisible (measured across paragraphs, table cells and
`<details>` blocks); a construct glyph failed to recognize is live, and live means
a stranger gets notified. The same logic decides the modelling calls elsewhere in
the package: an unrecognized entity name is escaped, `cmark`'s memoized closer
search is modelled (its only cost is a span glyph no longer trusts), and the
preview's level ranking treats anything unrecognized as `none` because a preview
that over-claims a bump is worse than one that under-claims.
`internal/markdown/escape.go` (file header), `internal/markdown/markdown.go:
backtickSearch`, `internal/preview/preview.go: rank`

---

## 5. Repository preconditions (`doctor`)

`doctor` checks the configuration glyph depends on but cannot see from inside a
release run. When one of those settings drifts nothing turns red — the workflows
stay green and the verdict is simply computed over a repository that no longer
matches the model.

**check** — one independent finding: id, status, what was **observed**, what was
**expected**, why it matters, and `Fix` — the concrete command **or edit** that
resolves it, which is a `gh api -X PATCH` only for the repository-settings checks.
`workflow-glyph-pins` hands back a workflow-file edit (pin each reference to a
release tag), `token-repo-read` a credential to configure, and any check that
could not run hands back "re-run", with the condition that must change first.
Two load-bearing properties: **read-only, always** (a diagnostic that mutates
cannot be run casually, and this one is meant to be), and **independent** — one
unreadable input degrades *that* check and no other. One check per line of
`Run`'s literal, in that stable order — the count lives there, not here: this
entry shipped "7" while `Run` built 8 (`commit-msg-hook` arrived in #81 and no
gate read this file). `internal/doctor/doctor.go: Check, Run`

**check id** — the stable kebab-case machine key (`squash-merge-enabled`,
`workflow-glyph-pins`, …). Branch on the id, **never** on the message, which is
prose and will be reworded. Treat the id block as the versioned surface it is.
`internal/doctor/doctor.go: IDTokenAccess…IDCommitMsgHook`

**pass / fail / advice / unknown** — four statuses, and the last two are the
point.
- **fail** — the assumption is violated and something concrete breaks. Drives
  exit 3.
- **unknown** — the check could not **run**: nothing was observed, so nothing may
  be concluded. Drives exit 4. "We could not check" is not "it is fine", so
  `OK` is false for unknown as well as fail.
- **advice** — a divergence from a house convention that costs glyph nothing
  mechanically. Never affects the exit: a report that fails over settings glyph
  handles correctly teaches the fleet to ignore the report, which is the one
  failure mode a voluntary check cannot survive.

The fail/unknown line is drawn on **what the API said**, not on whether the call
returned an error: a 404 from the repository read *is* an answer (there is no such
repository for this credential) and fails at 3, while a 403 rate limit, a 5xx that
outlived the retry schedule, a dead socket or an unparseable body is no answer at
all. Collapsing them made a transient GitHub outage tell the fleet's CI wrappers —
which branch on `.error.code == 3` to hard-fail and treat everything else as
retryable infra — that the repository was misconfigured, and never retry.
`internal/doctor/doctor.go: Status, checkTokenAccess`, `internal/github/github.go:
IsRepoUnknown`

Two severities are argued rather than obvious, and both are **coupled to the
walk**: `allow_squash_merge=false` is a *fail*, not because only a squash commit
resolves (every style does) but because squash is the only landing style with **no
partial state** — a squash-merged pull's one commit on main *is* its
`merge_commit_sha`, so it is never half-resolved. `allow_merge_commit` /
`allow_rebase_merge` are *advice*, and that severity is downstream of two
things: the walk expanding merge commits correctly, and the lost-pull warning
staying loud. Revert either and the severity must move back to fail.
`internal/doctor/doctor.go: checkSquashEnabled, checkMergeCommit, checkRebaseMerge`

**release-tag pin** — a `uses: akira-toriyama/glyph/…@vX.Y.Z` reference naming a
concrete release tag, scanned in the **local** checkout (a pin is a fact about the
tree in front of you, so `--repo` does not move this check). A moving ref, a
missing ref and a **commit-sha** pin all fail, for different stated reasons — a
sha is immutable but a reusable derives its binary version from the *tag* the
caller pinned. Whether the pin is the **latest** release is deliberately not
checked: `glyph-pin-audit.yml` in `akira-toriyama/.github` already owns that
question fleet-wide, and two answers to it would be one too many. The scan's trap:
a `uses:`-shaped line **need not be an executing step** — every reusable ships a
commented caller stub, and a fleet-sync step *writes* stubs from a `run: |`
heredoc, so comments are dropped, block scalars are skipped by indentation,
`uses:` is recognised only as the line's own YAML key, and the owner/repo match is
case-**insensitive** because GitHub's resolution is.

There are **two** pin surfaces per glyph release and this check sees one of them.
A consumer that uses the install action pins the action with `uses: …@vX.Y.Z` and
*also* hands it the release to download, as `with: version: vX.Y.Z` on the same
step — the action's `version` input is required and has no default, precisely so
no hand-bumped default can drift. `scanUses` reads only lines whose own YAML key
is `uses:`, so the `version:` two lines below is invisible to it: a repository
whose every `uses:` names the new tag while its `version:` still names an older one
passes this check while installing a different glyph release than its workflows
name. The two ship lockstep from one glyph release; bump them in the same edit.
`internal/doctor/workflows.go:
checkWorkflowPins, scanUses, pinProblem, isGlyphRef`,
`.github/actions/install/action.yml` (`inputs.version`)

---

## 6. Exit codes and streams

| code | meaning |
|---|---|
| 0 | ok |
| 1 | no release (soft) |
| 2 | usage — bad invocation or input; fix the args, do not retry |
| 3 | the gate code — what glyph was asked to judge does not conform |
| 4 | no trustworthy answer — GitHub API / git / network / IO failure, or a refusal to judge a range or repository state glyph could not read (the incomplete walk, the published floor, the unbounded first-release walk), or a refusal to write from a ref glyph has no authority over (a release run off the default branch) |
| 130 | interrupted (SIGINT/SIGTERM), emitted silently |

`internal/core/errors.go: Code, ExitCode`

**gate code** — exit **3**: the *subject glyph was asked to judge* violates the
convention. Same class, two subjects: a commit message under `lint`, and a
repository's own configuration under `doctor`. No new integer for the second. It
is emphatically **not** 2 (the invocation itself was fine) and not 4 (glyph could
not reach an answer at all). `internal/core/errors.go: CodeLint`,
`internal/cli/cmd_doctor.go: doctorVerdict`

**soft no-release** — exit **1**: every participating commit classified `none`. A
normal answer, not an error — which is why `preview` deliberately exits **0** on
the same fold ("this PR moves nothing" is exactly what a reviewer asked), while
`bump`, `notes` and `release` exit 1. `internal/core/errors.go: CodeNoRelease`

**annotation** — a `::warning::` or `::notice::` line on **stderr**. Folded onto
one physical line, because a workflow command *is* one line: the runner parses up
to the first newline and drops the rest, so a multi-line interpolation loses
exactly the part the annotation existed to deliver. `internal/cli/output.go:
warnf, noticef, oneLine`

**error envelope** — the single `{"error":{"code","message"[,"details"]}}` object
written to stderr **last**, after the command returned. This is the other half of
the **stream contract**: stdout carries the payload, and stderr has a *shape* —
every line is either a `::`-prefixed workflow command or part of that one
envelope. A consumer therefore sieves the envelope out (`sed -n '/^[{]/,$p'`)
before handing it to `jq`; `jq` over the two shapes together is a parse error, and
both shipped reusables buried that failure under `|| true`, so a run that warned
before it failed printed **no** `::error::` at all (t-sws7).
`internal/cli/output.go: renderError`

---

## 7. Fleet distribution

**fleet** — the repositories under this account that consume glyph's reusable
workflows and the shared commit convention. Every figure for the fleet's *size*
stated in this tree comes from one measurement, on 2026-07-21: **31 of 34**
non-archived repositories allowed merge commits and rebase merges
([DESIGN §7](DESIGN.md#7-repository-preconditions-glyph-doctor),
`internal/doctor/doctor.go` package comment), and the fleet's history held 9,548
commit subjects — the denominator the escaping rules' rendering cost is sized
against, and stated where that sizing is argued rather than here
(`internal/markdown/escape.go: escapeMarkup`, rules 3 and 4).

**reusable workflow** — a workflow with a `workflow_call` trigger, invoked from
another repository's workflow by `uses:` at a pinned tag. glyph ships **three**:
`lint.yml` (commit lint), `release.yml` (the rolling-draft release) and
`pr-verdict.yml` (the merge preview). The binary and the workflow ship from one
repo at one tag, and the reusable derives the binary version from
`job.workflow_ref` — the tag the caller pinned — so it cannot drift from the
workflow revision (a hand-bumped default did drift once: `lint.yml` sat at v0.4.0
through the v0.5.0 tag). `.github/workflows/{lint,release,pr-verdict}.yml`,
`internal/workflows` (the tests that guard this)

**composite action** — `.github/actions/install`: the **single** source of glyph's
security-critical install logic (download the pinned tarball, verify it against
`checksums.txt` **and** its build provenance with `gh attestation verify`,
fail-closed with bounded retry, add it to `PATH`), auto-detecting the runner's
OS/arch. The distinction that bites: a **relative** `uses: ./…` inside a
*reusable workflow* resolves against the **caller's** workspace, never the
reusable's own repo — so glyph's reusables check out glyph's source at
`job.workflow_sha` first and then use a relative `uses:` against that checkout,
while a **consumer** references the action the ordinary way, by full
`owner/repo/path@vX.Y.Z`. `.github/actions/install/action.yml`,
`internal/workflows/doc.go`

**distribution layer** — the files by which glyph reaches the fleet *without
going through the Go compiler*: the three reusable workflows plus
`goreleaser.yml`, the composite install action, and `.goreleaser.yaml`. The term
earns its entry by its boundary, which is easy to draw one file too wide in both
directions: `build.yml` is **not** distribution layer (this repo's own CI, shipped
to nobody), and neither are the fleet-synced consumers (`commit-lint.yml`,
`task-status.yml`, …) — their canonical copies are owned by
`akira-toriyama/.github` and arrive here by sync, not by pull request.
`scripts/dist-gate.sh: DIST_PATTERN`

**dist-gate** — the pull-request gate that makes evidence for a distribution-layer
change *mandatory*: such a file in the diff with no `internal/workflows`
`*_test.go` change fails the PR. It closes a measured gap — `bite` acts on Go
diffs only and the extras smoke reads no YAML, so a `:bug:` fix touching only
`.goreleaser.yaml` or only the install action used to ship with zero tests.
Waived by a `Dist-gate-exempt: <reason>` git trailer on every non-merge commit,
go-bite's exact shape. `scripts/dist-gate.sh`, `.github/workflows/build.yml`
(the `dist-gate` job), `internal/workflows/distgate_test.go`

**merge preview** — the answer to "what does merging this PR do to the version",
rendered as a whole Markdown comment body: the PR's own individual commits folded
with what is already *pending* on the base branch, both stepping from the same
tag. It says "the next release", never "the rolling draft" — distributed
fleet-wide it lands mostly on repos that tag straight from main, where naming a
draft would not be noise but *false*. `internal/preview/preview.go: Render,
Headline`, `internal/cli/cmd_preview.go`

**sticky comment** — one PR comment edited in place on every push rather than a
new comment each time. The caller finds its previous comment by the leading
marker `<!-- glyph-pr-verdict -->`, so the marker is part of the contract, not
decoration. `internal/preview/preview.go: Marker`

---

## Not in this vocabulary

**canonical pin** — a real term, but not glyph's: the phrase appears **nowhere**
in this tree (`rg -i "canonical pin"` → no matches). It belongs to
`akira-toriyama/.github`, which uses it in `docs/fleet-change-policy.md` and lays
out the mechanism in `docs/glyph-rollout-runbook.md` ("What is pinned, and by
whom"). There it means the glyph release the whole fleet is supposed to be on — the
tag written into the `fleet/` caller workflows that fleet-sync distributes, against
which `glyph-pin-audit.yml` fails while any repo's real `uses:` ref, *or the
`version:` its glyph install step passes*, disagrees. So it is the fleet-wide
counterpart of the *release-tag pin* above, which is the same fact seen from inside
one checkout — and glyph deliberately does not judge it, because that would be a
second answer to a question the fleet already answers. Look it up there, not here.

Also not glyph's, and easy to mistake for it: the *canonical copy* of a
fleet-distributed file. `commit-convention.md` in this repository is a pointer
whose canonical copy lives in `akira-toriyama/.github`, and the fleet-sync workflow
overwrites the pointer on its next run — a canonical **copy**, nothing to do with a
pin.
