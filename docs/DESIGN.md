# glyph — design

The canonical design for glyph, a self-built, gitmoji-driven release engine.
The per-gitmoji semver table is **not** duplicated here — it lives in
`internal/gitmoji/rules.json` (the machine source of truth) and is self-printed
by `glyph rules`. This document is the *why* and the *shape*.

## 1. Problem

The fleet squash-merges everywhere. GitHub's
`squash_merge_commit_title = COMMIT_OR_PR_TITLE` rewrites the squash subject to
the **PR title** on any multi-commit PR, erasing per-commit types from `main`.
Every tool that types a release from **commit text** (git-cliff today,
semantic-release, release-please, cocogitto) is therefore fooled. glyph instead
derives the bump and notes from the **individual commits inside the PR**, and
makes gitmoji the type driver.

Two inversions from the prior house convention:

- **gitmoji drives classification/semver.** Previously the Conventional type
  decided the bump and the gitmoji was stripped before parsing. Now the leading
  `:code:` *is* the type.
- **The bump is computed from the PR's individual commits at merge time**, not
  from `main`'s post-squash history — the one thing git-cliff structurally
  cannot do, and the reason a self-built tool is justified.

A 2026-07-17 survey of the field (release-drafter, release-please, changesets,
knope, tagpr, semantic-release, python-semantic-release, git-cliff, and the
gitmoji plugins) placed those two claims precisely — see the t-q5e1 task body
for the cited detail. What it changed:

- **The scope of "fooled" is commit-text readers, not all history readers.**
  release-drafter reads `main` and is NOT fooled, because it types from PR labels
  and changed paths and never looks at commit text at all.
- **Only the second hop is novel.** The squash-commit → PR resolution glyph does
  in `--since-tag` is prior art: release-drafter runs the identical
  `associatedPullRequests` hop, and release-please resolves a squash commit to
  its PR to read a human override out of the PR *body*. Neither re-expands the
  PR into its own commits — release-drafter's PR fragment has no `commits`
  connection, and its version resolver structurally cannot see commit text.
  python-semantic-release does recover per-commit types under squash, but by
  parsing the squash **body** for a bullet list — the text GitHub drops unless
  `squash_merge_commit_message = COMMIT_MESSAGES`. That fragility is the reason
  to read the API instead of the message.
- **gitmoji-as-type is NOT novel** and must not be sold as such:
  `semantic-release-gitmoji` and python-semantic-release's `EmojiCommitParser`
  both map textual shortcodes to semver with nearly glyph's own defaults, and the
  latter uses the same subject grammar. glyph's table is bigger (75 codes) and
  compiled in rather than configured; that is a packaging choice, not an invention.
- **Deferring the tag to publish is NOT a differentiator** — inherent to any
  draft-based tool (release-drafter carries `tag_name` on the unpublished draft
  exactly as glyph does).
- **Two real differentiators worth defending**: glyph can resolve to *no release*
  (release-drafter falls back to `patch` whenever no category matches, so it can
  never say "nothing shipped"), and glyph's walk is self-baselining
  (release-drafter requires a previously PUBLISHED release or it warns and returns
  nothing — which is also the strongest argument for the backlogged initial-tag knob).

Only permitted external dependency: the gitmoji spec dataset. (cobra is the lone
runtime import, per house pattern.)

## 2. Commit format

```
<:code:>[(<scope>)][!] <subject>
```

- **`:code:`** — exactly one leading gitmoji in **textual** form (`:sparkles:`),
  column 0, mandatory. Textual (not the glyph) for pure-ASCII stability (no
  U+FE0F / ZWJ), grep-ability, and deterministic AI authoring. GitHub renders the
  glyph in its UI.
- **`(scope)`** — optional; parentheses only, lowercase kebab, no leading space.
- **`!`** — optional breaking marker, immediately after code or scope.
- **subject** — English, imperative, lowercase start, no trailing period.
- **body/footer** — optional; a body ends with the `---（和訳）` separator + a
  Japanese translation (house rule). Footer may carry `BREAKING CHANGE:`,
  `Closes #N`, `Co-Authored-By:`. A footer block is read as one: git trailers
  (`token: value`) and issue references in GitHub's colon-less closing-keyword
  form (`Closes #12`, `Fixes owner/repo#12`) may stack with no blank line
  between them, and only prose ends the block. That the colon-less form counts
  is load-bearing rather than cosmetic — reading it as prose closed the block
  and discarded a `BREAKING CHANGE:` footer stacked beneath it, shipping a major
  as a minor out of a shape this very list blesses. A line that merely OPENS
  with a closing keyword ("fixes the crash reported in #12 by …") is prose and
  still ends the block.

The redundant Conventional `<type>` word is dropped — the gitmoji's own trailing
`:` plays the type-colon role. The parser is **lenient**: it accepts-and-ignores
a legacy `<type>(scope)!:` token so existing history keeps linting and bumping —
no flag-day.

Linter shape check (membership is checked in code against the embedded table):

```
^(:[a-z0-9][a-z0-9_+-]*:)(\([a-z0-9][a-z0-9-]*\))?(!)? (\S.*)$
```

An unknown `:code:` is a **hard lint error (exit 3)**, never a silent patch. The
linter also rejects a trailing `.`, an uppercase first subject letter, and any
`:construction:` commit that reaches a merge candidate.

## 3. gitmoji → semver

Lattice: `none(0) < patch(1) < minor(2) < major(3)`. Default-none. Every gitmoji
in the spec is explicitly enumerated in `rules.json`; buckets:

- **major:** `:boom:` only auto-majors.
- **minor:** `:sparkles:` only. A new feature is an explicit authoring decision;
  capability-adjacent codes (i18n, offline, a11y, UX) deliberately stay patch so
  an AI author cannot accidentally minor a routine change.
- **patch:** anything altering shipped / user-observable behavior.
- **none:** internal / non-shipping / meta — kept in history, never moves the
  version. Excluded from notes *unless the code carries a section*: removals
  (`:fire:`/`:coffin:`/`:truck:`) stay none but surface under a **Removals**
  section, so a deletion or rename is visible to the human pin-bump audit even
  though the version does not move (notes inclusion tracks the section, not the
  bump).

**Combination across a PR:** `prBump = max(classify(c) for non-bot c in pr)`. The
fold is order-independent and idempotent (fuzz-tested) so squash order can never
change the version. `prBump == none` ⇒ no release.

**Breaking is an orthogonal, non-suppressible boolean flag**, not a rung (a single
emoji is ambiguous). Any of three triggers short-circuits to major and cannot be
dropped by a skip rule: `:boom:`, a `!` before the colon, or a
`BREAKING CHANGE:` / `BREAKING-CHANGE:` footer.

**Deliberate divergences from the spec's `semver` field** (to ratify in Phase 1):
`:wrench:`→none and `:alembic:`→none (fleet config / experiments are
non-shipping); `:thread:` / `:safety_vest:` / `:airplane:` / `:t-rex:`→patch (each
changes shipped runtime behavior the spec leaves `null`).

## 4. Squash-safe mechanism — release-time re-read (stateless)

On a release run, walk `lastPublishedTag..HEAD` over `main`'s **merge points**;
for each, resolve its PR via `GET /repos/{o}/{r}/commits/{sha}/pulls` and fetch
that PR's individual commits via `GET /pulls/{N}/commits`; classify and
max-fold. **Nothing is persisted** — recompute-from-git each run, idempotent and
self-healing. Every verdict command runs **inside a git checkout** of the
repository being released — the walk base, the version base, and the draft's
target sha all come from local git; tags are never fetched over the API.

A merge point is whatever commit GitHub named `merge_commit_sha` for the pull —
the squash commit, a rebase-merge's last commit, or (the merge-commit button)
the **merge commit itself**, whose `merge_commit_sha` is its own sha. One
equality resolves all three. The walk therefore excludes a commit before that
lookup **only by its author** (a bot's or an automation's commit is a direct
push that can never move the version, and the fleet's daily sync push must not
cost a round-trip): a merge point's subject and its parent count are how GitHub
*shapes a pointer*, not evidence about it. Judging the shape first is what let
one click on the merge button drop a whole PR out of both the version and the
notes, silently, on the 31 of 34 fleet repositories that allow the button
(t-7zt7); the message rules still apply, one step later, to any commit no pull
request explains — which is how a local `git merge` stays skipped. Because
`git log` runs without `--first-parent`, a merge-merged PR's own commits are
walked beside its merge point; they resolve as *covered* by that PR (or, if the
association lags, fold in on the fallback path), and a walk-wide SHA set counts
each exactly once either way. That set holds the **canonical commit of every
resolved pull** as well as its inner commits — a pull squash-merged *into* a
topic branch leaves its own (never gitmoji-formed) squash subject inside the
listing of the pull that later landed that branch, and re-reading it as a
message wedged the release permanently. The price is one API round-trip per
merge commit, including the local merges that resolve to nothing — the only way
to tell the two apart is to ask.

**A pull's listing is governed by the walk's range, not by the pull.** The
listing is the pull's *entire* history and knows nothing of the range, so
expanding it whole folds in whatever the pull touched, whenever it touched it:
with a version tag cut *inside* a landed pull's footprint, commits that shipped
under that tag came back for a second release — exit 0, empty stderr, a minor
manufactured out of released work (t-8xsb). Before anything is parsed, each
listed commit is therefore mapped to **where it landed on the released branch**,
and the range decides. The mapping is git's, not a guess, and there are three
shapes: the merge button leaves the commits on the branch verbatim, so a listed
SHA the repository holds and that is an ancestor of `HEAD` landed as itself; a
rebase rewrote them all, but preserved their messages and their order and named
the last of the run as `merge_commit_sha`, so the N first-parent commits ending
there align positionally — an alignment **verified message by message and
abandoned whole** unless every one matches; a squash left no footprint at all,
and the pull alone governs it exactly as before. A commit that landed outside
the range is dropped with a `::notice::` naming it, and one that landed inside
is folded **under its on-branch SHA** — which also retires a defect of its own,
since a rebase-merged pull used to put its pre-rebase SHAs, which exist on no
branch, into the notes. A **shallow** checkout cannot answer the question at all
(a commit git does not have is indistinguishable from one that never landed), so
the walk says so once and falls back to expanding whole listings.

This also restores the intuitive **wedge escape**. A lint failure inside a
resolved pull is hard (Q1, below), and it used to be escapable only by cutting a
base at or past the pull's *merge point*, because expanding re-read the whole
listing however far past the offending commit the tag sat — so clearing the wedge
threw away every good commit in between. Now a base at or past the **offending
commit** clears it, and the error says so; only a squash-merged pull, whose
commits exist nowhere but the API, still sends the operator to its merge point
(which is its one commit on `main` anyway).

Standing aside is only safe if something stands in, so the walk keeps a ledger:
a pull that some commit reported itself *covered* by, whose canonical commit is
inside the range and was nevertheless never expanded, gets a loud `::warning::`
naming it at the end of the walk. That is a merged PR the walk could only ever
see from the inside — GitHub had not indexed the merge commit yet (a release
job runs seconds after the merge), or an automation authored it and the author
gate skipped it. Without the ledger every one of its commits skips itself and
the release reports `no release: 0 commit(s) participate` with no diagnostic at
all: the original t-7zt7 silence, surviving on the new path.

The walk **warns and does not expand** such a pull. Its commits are genuinely
lost from that release, and the reason the walk will not recover them from the
API is the same one that governs the resolved arm above — a listing carries
nothing about the range — but here the walk cannot repair it. The footprint
mapping needs the pull's **canonical commit** as its anchor, and this arm is
defined by not having one: the rebase alignment has nothing to align against.
Guessing instead is wrong two ways. A rebase-merged pull lists its *pre-rebase*
SHAs, which can never equal the `main` SHAs the walk-wide set holds, so the
dedup passes them all and the same change renders twice; and a pull whose
earlier commits shipped under the previous tag has them folded straight back in,
manufacturing a minor bump out of released work. Both are the silent-wrong-verdict
class this mechanism exists to kill, so the honest move is to name the loss and
refuse to guess. (The merge-button shape *is* placeable without the anchor, since
its commits sit on the branch under their own SHAs — so this refusal is broader
than it now has to be. Narrowing it means reopening a decision t-7zt7 ratified,
and it is tracked rather than smuggled in.) The warning cannot fire on a repository
whose merge points the walk can **resolve** — one that cries on every release
would be worse than none: standing aside requires the pull's canonical commit to
be **in range**, and a canonical commit in range that resolves is expanded on the
spot. Two causes reach the warning and they behave differently. API lag clears
itself, so it warns once. But a repository whose merge button is pressed by an
**automation** is perfectly healthy and warns on *every* release: the author gate
skips a bot-authored merge commit before the API, so nothing is left to resolve
the pull. Before t-7zt7 that pull was lost silently, so this is not a regression
— but such a repository should let a human press merge, or expect a standing
warning.

*Covered* is deliberately gated on the canonical commit being **in the walked
range** — a pull merged into another base branch is associated with commits
that reached `main` another way, and neither deferring to it nor expanding it
would be right.

`glyph release` converges the repository's **rolling DRAFT release** on that
verdict: the one glyph-managed draft (draft + `vX.Y.Z` tag) is created or
updated **by release id** (retagged in place when the next version moves —
never a second draft; id, not tag name, because tag-name resolution can hit a
published release, cli/cli#9367), residual drafts are deleted on a none
verdict, and **no tag is created** — GitHub tags the target commit when a
human publishes. A next version not strictly above the latest published
release fails loud (an unpublishable draft; a deleted published release's tag
is burned forever). `--dry-run` computes everything, action included, and
writes nothing. The `--json` verdict also carries the walk's expansion
provenance (`pulls`: each resolved pull and its participating commit count) —
a squash-subject reader like git-cliff can only diverge legitimately when some
pull contributed 2+ commits, so a shadow comparison branches on exactly this
instead of re-deriving the walk's exclusion rules in shell. The count does not
distinguish the merge style, so it is an **upper bound**: a merge-merged pull's
commits are on `main` verbatim, where a text reader sees them and agrees, so 2+
there can raise a false alarm — never hide a real divergence.

Rejected alternatives: a semver **label** on the PR (note generation must re-read
the inner commits anyway, so the label adds a weaker, mutable, git-invisible
source of truth); enforcing the squash **subject** as an aggregate (lossy — one
line can't carry N grouped entries); owning the squash **body** via
`COMMIT_MESSAGES` (kept only as an optional drift alarm, never the primary).

Fallbacks never hard-fail a release: a direct-push commit or an API lag emits a
`::warning::` and classifies the squash commit's own message. On this fallback
path only, a message that does not parse or carries an unknown `:code:` also
degrades to a `::warning::` and counts **none** — never a silent patch, and
never exit 3: the hard unknown-code error stays with the lint gate (§2), and
the downgrade is owned by the walk assembly, keeping `internal/bump` pure.
One exception (Q10): a **breaking marker is never suppressed** — an unknown
`:code:` carrying `!` or a `BREAKING CHANGE:` footer counts **major** behind
the `::warning::`, normalized to `:boom:` (so it folds and hoists into
Breaking Changes); a typo can over-bump, but a breaking change is never
silently dropped.

The leniency is for the fallback path only. A lint failure **inside a
resolved merged PR** stays a hard exit 3 even on the release walk (Q1 —
"never a silent patch" at full strength; only a commit that bypassed the lint
gate can produce one). Published history is immutable, so that commit wedges
every release until the walk starts past it: cut a release tag there by hand, or
name such a tag with an explicit `--since-tag=TAG`. The error names the PR, its
merge point, the base that clears it and why that base is the one.

**Which base clears it depends on whether the offending commit is somewhere a
tag can be cut**, and the two answers are the two halves of the footprint rule
above. A merge- or rebase-merged PR put its commits on the released branch, so
the walk can drop the ones that landed outside the range, and a base **at or
past the offending commit** clears the wedge — the intuitive escape, and the
nearest one, which matters because every commit between it and the merge point
is a commit a farther base would silently drop from the release. A
**squash-merged** PR has no such commit: its individual commits exist only over
the API, its one commit on `main` *is* its merge point, and nothing short of a
base at or past that merge point stops the walk re-fetching the listing.

Since a merge commit resolves (t-7zt7), that hard gate reaches **merge-merged
PRs for the first time**: a non-conforming commit inside one now wedges the
release where the pre-fix walk released quietly. That is the contract
squash-merged PRs have always had, and the quiet release was the silent-drop
bug wearing a friendly face — but it is a real change for the 31 of 34 fleet
repositories that allow the button, and it lands as an exit 3 on their next
release. Between t-7zt7 and t-8xsb the escape had to be stated against the merge
point for *every* shape, because expanding re-fetched the pull's whole listing
whenever its merge point was in range (verified then: a tag at the offending
commit and a tag strictly past it both exited 3). Both now exit 0.

## 5. Architecture (Go, house pattern)

Binary `glyph`, module `github.com/akira-toriyama/glyph`. Subcommands:
`lint`, `bump`, `notes`, `release`, `doctor`, `rules`, `version`.

```
cmd/glyph/main.go        os.Exit(cli.Execute()) — thin process boundary only
internal/core            exit-code contract + structured Error (no I/O, no logic)
internal/version         ldflags build identity + ReadBuildInfo fallback
internal/gitmoji         //go:embed rules.json; Load() validates completeness
internal/parser          message → Commit{Gitmoji,Scope,Breaking,Subject,Body,SHA,Author}
internal/bump            Level lattice; Classify; Reduce(max); Next; stdlib semver
internal/notes           group by section; text/template render (no external tmpl dep)
internal/gitsource       local `git log BASE..HEAD` (exec.CommandContext)
internal/github          commits/{sha}/pulls, pulls/{N}/commits, release CRUD, repo object
internal/doctor          repository-precondition checks; independent, read-only (§7)
internal/cli             cobra adapter; Execute() int owns the exit-code funnel
```

**Exit-code contract** (`internal/core`): `0` ok · `1` no release · `2` usage ·
`3` convention violation · `4` API/git/IO · `130` interrupted. Errors are
classified at the source into `*core.Error`; `ExitCode` funnels everything
(unclassified ⇒ API, never usage). `3` is the *gate* code — what glyph was asked
to judge does not conform: a commit message under `lint`, a repository's own
configuration under `doctor`. Same class, different subject; no new integer.

**gitmoji table embedding:** `//go:embed internal/gitmoji/rules.json` — the
pinned binary *is* the pinned rules (lockstep, zero skew). `Load()` fails at
startup if any spec code is missing or a bump is out of enum.

**Testing** (stdlib only, no testify): table tests + a full-coverage
exhaustiveness test for the gitmoji table; golden tests for notes; fuzz for the
parser (never panics; well-formed round-trips) and the fold (order-independence).
Always `-race`.

## 6. Distribution (summary)

Rules ship inside the binary (no synced config table — that would recreate
per-repo drift). glyph ships its own reusable workflows (`lint.yml`,
`release.yml`, `pr-verdict.yml` — the merge preview: one sticky PR comment
predicting the next release from the PR's individual commits folded with what
is already pending on main; it is fleet-distributable because it names no
draft (the arithmetic holds whether the next release is a draft or a hand-cut
tag) and skips its only unbounded input, the pending walk, on a repo with no
v* tag) that install the pinned binary with checksum + attestation verify;
family repos consume them at a concrete `@vX.Y.Z` (never a moving tag — binary
and workflow ship from one repo at one tag). glyph's OWN tag-driven GoReleaser
workflow is `goreleaser.yml` — also the attestation signer identity from v0.3.0
on. Migration off git-cliff is canary-first (`chord`) and flips DIRECTLY —
ratified Q16: no shadow parallel-run (a policy-honest comparison against the
type-driven git-cliff is impossible once the gitmoji table legitimately
reclassifies single commits, and migration scaffolding is debt). The safety net
is structural: writes are draft-only, a human publishes, the published floor
guards the tag space, and `--dry-run` previews any verdict. Full rollout:
tracked in the `projects` furrow task and the approved plan.

The install itself — download the pinned tarball, verify it against the
release's `checksums.txt` AND its build provenance (`gh attestation verify`,
fail-closed, bounded retry), add it to `PATH` — lives in ONE composite action,
`.github/actions/install`, auto-detecting the runner's OS/arch (it serves the
Linux lint/preview jobs and the macOS release job from one file; the inline
copies it replaced had already drifted — two `linux_amd64` + `sha256sum`, one
`darwin_arm64` + `shasum`). glyph's own three reusables reach it by checking out
glyph's source at the commit the caller pinned (`job.workflow_sha`) and using a
relative `uses:` against that checkout — NOT the full `owner/repo/path@tag` form.
A relative `uses:` inside a reusable workflow resolves against the CALLER's
workspace, never the reusable's own repo, so a bare `./…` would look for the
action in the consumer's tree; the self-checkout puts glyph's tree there. The
binary version is derived from `job.workflow_ref` (the tag the caller pinned),
so it cannot drift from the workflow revision — replacing a hand-bumped
`glyph-version` default that did drift once (lint.yml sat at v0.4.0 through the
v0.5.0 tag while callers pinned @v0.5.0); `internal/workflows` tests guard both
the single-source install and the derived version.

A CONSUMER that just wants the glyph CLI on a laptop-in-CI (e.g. a macOS
`swift package diagnose-api-breaking-changes` gate that also reads
`glyph bump --range "$BASE..HEAD" --json | jq -r .level` — the same rules,
no gate reimplementing the convention) references the action the ordinary way,
by full path pinned to a release tag:

```yaml
- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z
  with:
    version: vX.Y.Z              # the tag you pinned above
    token: ${{ github.token }}   # for `gh attestation verify`
- run: glyph bump --range "$BASE..HEAD" --json   # exit 1 on a none verdict — handle it
```

The consuming job needs `permissions: contents: read` and a `GH_TOKEN`/`token`
for the attestation verify.

## 7. Repository preconditions (`glyph doctor`)

Everything above assumes repository configuration glyph never observes: that the
repo squash-merges, that the squash subject and body policy leave a classifiable
gitmoji on `main`, that a caller pins a concrete tag. When one of those drifts
nothing turns red — the workflows are green and the verdict is simply computed
over a repository that no longer matches the model. The 2026-07-21 fleet
measurement found 31 of 34 non-archived repos allowing merge commits and rebase
merges, and `glyph-test` sitting on `squash_merge_commit_title = PR_TITLE` /
`squash_merge_commit_message = PR_BODY`; nothing detected either until a human
ran `gh api` by hand. `doctor` is the machine-checkable half of that, and the
prevention side of t-7zt7 (a merge-commit PR vanishing from the release walk).

Shape: independent checks → one report object → an exit on the aggregate.
**Read-only, always** — a diagnostic that mutates cannot be run casually, and
this one is meant to be. Each finding carries a stable kebab-case id (branch on
that, never on the prose), the observed and expected values, what breaks, and the
`gh api` command that fixes it. Independence is structural: one unreadable input
degrades *that* check to `unknown` and no other, and `unknown` is deliberately
distinguishable from `fail` — "we could not check" is not "it is fine", so
neither exits 0.

The line between the two is drawn on what the API **said**, not on whether the
call returned an error (`github.IsRepoUnknown`). A 404 from the repository read is
an answer — there is no such repository *for this credential* — and fails at `3`.
A 403 rate limit, a 5xx that outlived the retry schedule, a dead socket or a body
that would not parse is no answer at all: nothing about the repository was
observed, so it is `unknown` at `4`, the same code every other glyph command gives
that failure. Collapsing the two the other way made a transient GitHub outage tell
the fleet's CI wrappers — which branch on `jq -e '.error.code == 3'` to hard-fail
and treat everything else as retryable infra — that the repository was
misconfigured, and never retry.

The severities are the argued part:

- **`allow_squash_merge` false ⇒ fail.** *Not* because only a squash commit
  resolves — every style does. GitHub points `merge_commit_sha` at whichever
  commit represents the merge (the squash commit, a rebase's **last** replayed
  commit, or the merge commit itself), and §4's walk expands the PR from there in
  all three cases. What squash-off removes is not a fallback — it is the guarantee
  that a pull request is resolved all-or-nothing. A squash-merged pull has exactly
  one commit on `main` and that commit is its `merge_commit_sha`, so the walk
  either expands it or falls back on it. Every multi-commit landing splits those
  two states. §4's walk runs `git log` without `--first-parent`, so a merge-merged
  pull's branch commits are in the range beside its merge point; each of them
  stands aside for that merge point (`mergedPullFor`'s `covering`), and when the
  merge point alone is unresolved — GitHub indexes a merge commit *after* the
  commits it merges, or an automation authored it and `ExcludedFromResolution`
  skipped it before the API — nothing expands the pull and the whole of it counts
  `none`. That is measured: fully dark, a merge-merged pull reproduces its live
  verdict (`minor` either way); with only the merge point at 422, the same
  repository exits `1` with two warnings. A rebase splits them the other way: it
  writes new shas that appear in no pull's listing, so a replayed commit classified
  during the lag is folded in again when the last one expands the pull. Note also
  which failures actually reach the fallback: only a 422 (`IsCommitUnknown`). A 403
  rate limit, a 5xx outliving the retry schedule (t-bjrv) and a dead socket all
  leave `walkSince` as an error and exit `4` — the outage window is an exit-code
  question, not a classification one. Squash is therefore the landing style with no
  partial state at all; the cost of a dark API under squash is that a MULTI-commit
  squash carries the PR title, which no lint gate checks, so the fallback reads one
  unlinted subject (measured `minor` → `patch`, and `minor` → `none` for a title
  with no gitmoji). One wrong level on one pull, versus a whole pull lost.
- **`allow_merge_commit` / `allow_rebase_merge` true ⇒ advice, not failure.** A
  merge commit *used* to be data loss (`bump.Excluded` drops 2+ parents, so the
  PR vanished — t-7zt7); with the walk expanding merge commits correctly it costs
  no bump while the API answers, and none at full darkness either — the branch
  commits are on `main` and classify themselves. What is left is the squash-only
  house convention plus one *loud* window per style: an unresolved merge point
  (API lag, or a bot-authored merge, where it repeats every release) drops its pull
  with two warnings and exit `1`, and a rebase whose listing the walk cannot align
  against what landed — one that dropped an already-upstream commit — can still
  fold a replayed commit in twice during the lag. Neither is the silent wrong
  verdict `fail` is reserved for.
  A rebase merge was never lenient either — the last replayed commit expands the
  whole PR through the API and an unknown `:code:` inside it hard-fails exactly as a
  squash's would, while the earlier replayed commits resolve as *covered* and are
  skipped; it costs one round-trip per replayed commit and the dedup key, not
  strictness. Failing over settings glyph handles correctly would train the fleet
  to ignore the report, which is the one failure mode a voluntary check cannot
  survive. The merge-commit severity is downstream of that fix: revert the fix and
  it must move back to `fail`. It is downstream of the *loudness* too — advice only
  holds while §4's reconciliation warning keeps naming the pull that was lost, and
  that warning fires on every release of an automation-merged repository, which is
  exactly the noise somebody eventually silences. Quiet it and this severity moves
  to `fail` with it. *Allowing* a second method leaves squash there for
  the traffic that matters — which is why this is advice while turning squash
  **off** is a failure.
- **The squash title/message policy ⇒ fail.** `PR_TITLE` hands the PR title to
  *every* squash, single-commit PRs included, so `main` fills with subjects no
  gitmoji reader can classify — and §4's documented fallback (direct push, or API
  lag right after a push) classifies exactly that message, so a release counts
  none and the bump is lost. `PR_BODY` drops the per-commit list that is the only
  offline record of a PR's pre-squash types.
- **Workflow pins ⇒ fail on any non-`vX.Y.Z` ref**, scanned in the LOCAL
  checkout. Whether the pin is the *latest* release is deliberately NOT checked:
  `glyph-pin-audit.yml` in `akira-toriyama/.github` already owns that question
  fleet-wide, and a second implementation would be a second source of truth. The
  scan's trap is that a `uses:`-shaped line need not be an executing step: every
  reusable ships a permanently-stale COMMENTED caller stub containing `uses:` and
  an old version (ignore comments and glyph reports itself as drifted forever;
  read the first match in a file and you read the comment instead of the real
  line), and a fleet-sync step *writes* stubs from a `run: |` heredoc, so the
  scan must skip block scalars whole or it fails a repository over text it emits
  rather than runs. Whole-line comments are dropped, block scalars are skipped by
  indentation, `uses:` is only recognised as the line's own YAML key, and the
  owner/repo match is case-insensitive because GitHub's resolution is —
  `Akira-Toriyama/glyph/…@main` executes, and a case-sensitive scan called that
  repository clean.

## 8. Roadmap

Phase 0 scaffold (this) → 1 gitmoji table → 2 parser+bump+lint → 3 lint reusable
+ canary (first shippable) → 4 notes → 5 GitHub squash-safe plumbing → 6
glyph-shipped `release.yml` reusable + chord direct flip → 7 hub self-adopt +
docs → 8 fleet migration.
