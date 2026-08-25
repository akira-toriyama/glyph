# glyph — design

The canonical design for glyph, a self-built, sigil-driven release engine.
The commit grammar is **not** defined here — since v2 it lives in each
repository's own `glyph.toml` (§2), written once by `glyph init` and printed
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

v2 (epic e-qzpz, ratified 2026-08-16) removed glyph's own grammars. The
repository's `glyph.toml` — written by `glyph init --gemoji` or
`--conventional` and then owned by the repository — carries an ordered list of
RE2 `[[patterns]]`, and a commit message means whatever the first matching
pattern says it means:

- The named group **`semver_sigil`** must capture one of the five sigils —
  `=` none / `~` patch / `^` minor / `!` major / `%` promote to 1.0.0 — the
  only input to version calculation (§3). The alphabet is fixed in the binary;
  everything else (where the sigil sits, what a prefix looks like, whether a
  scope exists) is the pattern file's decision. RE2 means no lookahead and no
  backreferences, stated in the presets' comments and here because it is the
  first thing a regex author reaches for.
  - v2 ratified an alphabet of exactly four, "fixed and unchangeable". **That
    decision is superseded** (2026-08-17, task t-c01z): `%` is the fifth, and
    it is fixed and unchangeable in the same way — a repository still cannot
    invent a sigil or remap one. What changed is the count, and it changed
    because the 0.x rule below closed the only exit those four had. Widening
    the alphabet is a breaking change to every distributed `glyph.toml`, whose
    patterns spell the class themselves: a config still reading `[=~^!]` does
    not reject a `%` commit, it fails to MATCH it, which refuses the whole
    range (exit 3). The presets ship `[=~^!%]`; existing files are a rollout
    step, not an upgrade glyph performs.
- A pattern may carry a fixed **`semver_sigil`** key — the sigil a match
  yields when the message captures none (the presets use it to make a raw
  `git revert` a patch) — or **`skip = true`**, which drops a matching commit
  from lint, bump and notes entirely (the presets skip merge commits and
  autosquash artifacts; v1 carried both as hardcoded exemptions, and the hook
  path is where they matter most — an author cannot rewrite a subject git
  generated, so judging it forces `--no-verify`, which turns the gate off).
- A pattern may carry **`warn = '<message>'`** — a message the file's author
  wrote for a commit's author, for a pattern that is legal but undesirable.
  The verdict is untouched; the message is surfaced **wherever the pattern
  wins a verdict**: lint (`--range`, `--stdin`/`--message`, `--pr`, both
  hooks) annotates it per commit, and the fold puts it on the commit's row in
  the machine verdict (`bump --json`'s `commits[].warn`), which `bump`,
  `release` and both of `preview`'s folds each announce. The invariant is
  deliberate — a warning loud at one gate and silent at another teaches the
  reader that the loud gate is noise. `warn` on a `skip` pattern is a load
  error (a skipped commit is outside every verdict, so the warning would fire
  for nobody), and so is an empty `warn`. The key exists for the
  **v1-acceptance window** (t-37xj): the migration pattern that accepts a
  sigil-less gitmoji subject as `=` none, where before the warning a
  forgotten sigil passed lint green and folded silently — measured on
  dotfiles as a release that simply stopped (v1 verdict v1.0.0, v2 verdict
  none, nothing said). `glyph init --gemoji --v1-window` generates the
  window with the warning in place; the block's own comment says when to
  remove it.
- **`exclude_authors`** removes a commit from lint and the fold before its
  message is ever matched — the key exists for bots, whose messages are
  exactly the ones the patterns do not describe. Whether such a commit
  appears in the notes is `[[note.sections]]`'s decision alone (§3). An
  **empty entry is refused at load**: the authoring path has no commit yet, so
  `lint --message` and `lint --stdin` judge under an empty author, and
  `exclude_authors = ['']` excluded every message the commit-msg hook was ever
  handed — measured, a message matching no pattern exited 0 with that one
  entry present and 3 with it removed. A stray comma turning the gate off
  silently is the shape this file refuses everywhere else.
- **Lint has no taste** (mutation row `config-lint-grows-a-taste.patch`): a
  message either matches a pattern and yields a sigil, or it violates. Which
  combinations are wise (`:memo:!`) is the author's call — glyph parses and
  computes, it does not opine. The retired v1 rule vocabulary (uppercase
  subject, trailing period, footer discipline, merge-candidate rules) is
  gone with the grammar that defined it.
- Unknown keys, an unknown `schema`, an uncompilable pattern, a malformed
  `note.line` and a section that does not state exactly one axis are LOAD
  errors, never repairs (mutation row `config-unknown-schema-accepted.patch`):
  this file decides CI verdicts, and a silently-misread key is a
  silently-changed verdict.

Config resolution is one file per checkout: the `glyph.toml` at the top level
of the working tree the command runs in (ratified Q1 — a config change
reinterprets past commits, accepted). The commit-msg hook, CI and a
subdirectory shell all read the same file by construction.

### 2.1 The text the rules judge — git's cleanup

A rule is only as good as the text it is applied to, and at the commit-msg hook
that text is **not** the message. git runs the hook BEFORE its own cleanup, so
the file still holds whatever the editor left: the template, the status block,
and under `-v` a scissors line with the entire diff below it. `parser.Cleanup`
reduces that file to the message git will record, and `--stdin` is its only
caller (a `--range` walk reads `git log %B`, which git has already cleaned).

**The requirement is agreement, not tidiness.** The hook and CI must reach the
SAME verdict on one commit; a gap is glyph lying in one of two directions, and
the two are not equally bad. Blessing a message CI will reject costs a round
trip. Refusing one CI would accept costs the commit — the only way past the hook
is `--no-verify`, which turns the whole gate off.

**Which cleanup runs is a per-commit question, and the hook can answer it.** git
has five modes and picks between two of them by whether an editor will run;
assuming the editor's cleanup is what made the hook and CI disagree, measured on
git 2.54 in both directions (`-F` with a `#` line as the subject: hook 0, CI 3;
`-F` with an indented `  # why:` above a `NON-BREAKING:` footer: hook 0, CI 3 for
`undeclared-removal`). The two signals a hook actually has:

- `commit.cleanup`, read with `git config --get`;
- `GIT_EDITOR`, which git sets to `:` when no editor will run. Only that side is
  load-bearing — with `core.editor` or `$EDITOR` supplying the editor git leaves
  `GIT_EDITOR` **unset** in the hook, so unset must mean "an editor may run".
  Read the other way, those developers get the whitespace branch, where the
  template is never stripped and every commit is `malformed-subject`.

Resolution, then, is `commit.cleanup` × edited → `verbatim` / `whitespace` /
`strip` / `scissors`, with the scissors cut applied whenever an editor ran (git
truncates under `-v` in every mode) and NOT applied without one (measured:
`commit.cleanup=scissors` with `-F` records the cut line and everything under
it). An unrecognised mode name warns and falls back — this hook forwards only the
lint gate code and waves everything else through, so failing there would trade a
typo in `commit.cleanup` for a repository whose commits are not linted at all.

**Two decisions ratified by measurement, against the shape a reader expects:**

- the cleanup is a **port** of git's `strbuf_stripspace`, not an approximation of
  it — trailing `" \t\r"` per line (git's own `isspace`: a `\v` is content),
  interior blank runs collapsed, comments recognised at **column 0 only**. Held
  to `git stripspace` by a differential test over generated messages, because
  every earlier approximation passed its hand-written cases;
- the scissors line is matched **exactly** — git's `wt_status_locate_end` does a
  `strstr` for one literal string, so a loose match cuts messages git records,
  and everything below a stray cut line (footers included) vanishes from the
  hook's view. A test drives a real `commit -v` and asserts git still writes that
  literal, because exactness fails the other way if git ever changes it.

**Deriving this inside the binary rather than in the hook script is a rollout
decision, and the pre-push hook is the same decision applied to a bigger
quantity** — it computes no range at all, because a range computed by a script
nobody refreshes is a wrong verdict rather than a loud failure. The script is a file installed once into ~34 repositories; had it
been taught to compute the mode, every already-installed copy would go on
computing nothing until someone re-ran `glyph hook install` there. It also keeps
the hook's founding property (§5): the hook holds no knowledge, it asks glyph.

**What stays wrong, and is not claimed fixed:** `git commit --cleanup=<mode>` on
the COMMAND LINE reaches neither the config nor the environment (measured), so a
per-commit override is invisible to the hook and the message is judged under the
repository's mode. Same for `core.commentChar`: glyph assumes `#`.

## 3. Sigils → semver

Lattice: `none(0) < patch(1) < minor(2) < major(3)`, owned by `internal/bump`
(`Level`, `Reduce`). The sigil IS the classification: `=` none, `~` patch,
`^` minor, `!` major, `%` major — fixed in the binary beside the alphabet
(`bump.SigilLevel`), not configurable, because a repository that could remap
`!` to patch would make every pinned verdict unreadable from the file alone.

**Combination across a range:** `Reduce` folds with max — order-independent
and idempotent (fuzz-pinned), so squash order can never change the version.
All-none ⇒ no release (exit 1).

**Classification is version-blind; only the arithmetic is not.** This is the
one split the 0.x rule rests on, and it is why the rule lives in
`bump.Version.Next` and nowhere else:

- While the current major is **0**, a major decision steps the **minor**
  (`v0.5.3` + `!` ⇒ `v0.6.0`; mutation row
  `semver-0x-major-still-jumps-to-1.0.0.patch`). Plain semver would answer
  `v1.0.0` — the behaviour through v2 — which let a repository still finding
  its shape claim a stable major by writing one `!`, and charged it a whole
  major for every break after that. There is no config opt-out: a flag here
  would make the same commit range mean two versions depending on a file that
  nothing in the verdict shows.
- **`%` promotes**: from any 0.x it lands exactly on `v1.0.0`, and from 1.x up
  it is a plain major step (mutation row
  `semver-promote-steps-instead-of-landing.patch`). It is the only door out of
  0.x, which is the point — reaching 1.0 becomes something an author says,
  not an accident of the third breaking change. The 1.x arm is not symmetry
  for its own sake: a constant `v1.0.0` would sit at or below every published
  1.x release and `checkPublishedFloor` would refuse the release outright
  (exit 4).
- In 0.x, `!` and `^` therefore produce the same version. That collapse is
  accepted: `v0.y.z` has two moving digits and the lattice has three moving
  rungs, so some pair must collapse, and the pair chosen keeps `~` distinct —
  a fix and a break are the two a reader most needs to tell apart.

**Promote is not a fifth rung.** `bump.Decision` carries `{Level, Promote}`,
and a `%` commit classifies as **major** like any other breaking change; the
promotion rides beside the lattice, OR-folded so it stays order-independent
(mutation row `sigilfold-promote-is-dropped.patch`). A fifth `Level` word was
rejected because `Level` is a closed four-word vocabulary that three consumers
read as data and all three answer an unknown word by silently doing nothing:
a `[[note.sections]]` `semver` filter (the promoting commit vanishes from the
release body), `internal/preview`'s `rank`/`icon` (the pull request is told it
"moves nothing" while the version moves), and `pr-verdict.yml`'s
`[ "$level" = "major" ]` (`breaking` goes false fleet-wide). None of those is
a failure anyone would see.

**A non-excluded commit no pattern claims refuses the WHOLE range** (ratified
Q2; mutation row `bump-unmatched-commit-folds-as-silent-none.patch`): folded
as none instead, a commit stops existing for versioning the moment someone's
regex misses it — the silent hole v2 exists to close. The refusal is the lint
class (exit 3), walks the whole range before it goes out (one red run carries
every finding — the v1 three-red-runs incident, kept fixed), and is exempted
exactly twice: `exclude_authors` (checked BEFORE matching — mutation row
`bump-author-exclusion-waits-for-a-match.patch`) and the release walk's
API-lag fallback (§4 — a squash subject during lag is not a message anyone
wrote, so it is dropped and recorded in the walk facts, never a refusal).

**Notes follow `[[note.sections]]` alone**: config order is render order, each
section filters on one axis (`semver` or `author`), and a commit lands in
EVERY section whose filter matches it (mutation row
`notes-first-section-wins.patch` — dedupe on first placement and section
order silently decides which section owns a commit). An unmatched commit has
no level, so it can only surface through an author section, rendered through
the same `note.line` template with `$subject` bound to its raw first line —
the ratified bot fallback. `skip` is total: no section at all, which is what
separates it from `exclude_authors`.

**`note.line` and the optional span.** The template substitutes `$name`
placeholders — the winning pattern's named groups, plus the built-ins `$pr` /
`$author` / `$hash`, which outrank a group of the same name — and literal
text passes through as the author's own Markdown, with the mention fence run
over the assembled line. A **`$[ … ]` span renders only when EVERY placeholder
inside it resolves non-empty** (mutation row
`notes-optional-span-always-renders.patch`), taking its own punctuation with
it when one does not. Without it, punctuation written around a placeholder
rendered around nothing: `$pr` is empty for every commit the `--range` walk
sees (that walk resolves no pulls at all) and for every direct push under
`--since-tag`, so both shipped presets emitted
`- add the demo feature () @akira-toriyama` — measured live. A malformed span
is a CONFIG error, refused at load with the file's path (exit 3) rather than
by the release that would have rendered it: unterminated, nested, or holding
no placeholder — the last because a span with nothing to resolve renders
unconditionally, which says optional and means always. The marker was chosen
over `${ … }` and `[? … ]` after measuring that all three parse as literal
text under the existing placeholder grammar (so every template already in the
fleet loads unchanged): only `$[` carries both of the file's own signals — `$`
marks what glyph interprets, and the bracket already means optional in the
same file's `[commit]` template block (`[(scope)]`, `[<body>]`). A bare `[`
stays literal, which is what leaves the `- [$scope] $subject` idiom and
Markdown links untouched, and brackets *inside* a span nest — the closing `]`
is the first at depth zero, so `$[ [$scope]]` renders `[cli]` or leaves whole,
and an unpaired `[` is refused as unterminated instead of closing the span one
character early (measured: closing at the first `]` of any kind left a stray
`]` on every rendered line).

**`draft_on_none`** (§4's flag, `internal/draftplan`): with it on, a none
verdict maintains an `Unreleased` placeholder draft instead of deleting the
rolling draft, and the next real verdict retags that same draft to the real
version through the ordinary keep-selection path (mutation row
`draftplan-none-forgets-the-placeholder.patch`). The placeholder tag is
deliberately not house-shaped — publishing it by hand cannot burn a version
tag or wedge the published floor — and it is claimed as glyph-managed even
with the flag off, so flipping the flag off converges the artifact away.

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
and the range decides. The mapping is git's, not a guess, and it is two questions
rather than three shapes. First, did this commit land under its **own SHA**? The
merge button leaves the pull's commits on the branch verbatim, so a listed SHA the
repository holds and that is an ancestor of `HEAD` landed as itself. Then, of
**whatever is left**: does it align against the run a rebase would have written?
A rebase rewrites what it replays but preserves the messages and the order, and
GitHub names the last of the run as `merge_commit_sha`, so the first-parent
commits ending there align positionally with the unplaced entries — an alignment
**verified message by message and abandoned whole** unless every one matches. A
squash left no footprint at all, reaches that alignment with the whole listing and
fails it, and the pull alone governs it exactly as before. A commit that landed outside
the range is dropped with a `::notice::` naming it, and one that landed inside
is folded **under its on-branch SHA** — which also retires a defect of its own,
since a rebase-merged pull used to put its pre-rebase SHAs, which exist on no
branch, into the notes. The fold then establishes what the notes may cite: the
pull **beside** the landed sha wherever one resolved, and for a footprint-less
commit — the squash arm, whose listed shas exist on no branch and were published
anyway (t-xxhj: a live release body cited two shas `git branch -r --contains`
answers nothing for) — the pull **alone**, the one address that outlives the
squash. Beside, never instead: within one pull the pull number is the same for
every entry, so the sha is the only thing that could tell their citations apart,
and a template placing it *instead* of the pull would address a squash's entries
by shas no branch holds. What actually reaches a body is `note.line`'s decision,
not glyph's — the fold binds `$pr` and `$hash`, and both shipped presets place
`$pr` alone inside an optional span, so a line reads `- subject (#123)` and
drops the parens with the pull (§3). v1's hardcoded `(#123, abc1234)` is
`$[ ($pr, $hash)]` today. A **shallow** checkout cannot answer the question at all
(a commit git does not have is indistinguishable from one that never landed), so
the walk says so once and falls back to expanding whole listings — and, since it
knows the answer it gave was a guess, records the checkout as one it could not
read (below). That probe is taken once per walk, before any expansion, rather
than lazily on the first pull that expands: a walk where nothing resolves is
still a walk over a truncated history, and it was the one that never asked.

The order of those two questions matters, and so does the fact that the second is
asked of **what the first did not place** rather than only when it placed nothing.
Reading a listing as all-verbatim *or* all-rewritten was wrong in both directions,
and both were measured against the live API rather than argued (`glyph-test`,
t-7h15). A listing can be **mixed**: GitHub computes it against a *stored* base
SHA instead of re-deriving one, so a stacked pull keeps listing its base pull's
commits after those land verbatim through the merge button — and short-circuiting
there left every *rebased* entry of that pull with no landing site, which is to say
ungoverned by the range. That is t-8xsb reappearing inside the fix for t-8xsb:
`minor` out of work released a tag earlier, exit 0, no warning, notes citing
pre-rebase SHAs. And a rebase does **not** replay everything it is handed: it drops
the merge commit that "merge `main` into the branch" leaves behind, GitHub permits
rebase-merge over one, and that entry — listed, landing nowhere — made the count
wrong and abandoned a mapping that was otherwise exact. It is kept out of the
alignment on its **parent count**, the same fact `bump.ExcludedFromClassification`
already reads, so nothing downstream needs it placed. What can still fail to align
is a rebase that dropped a commit it was asked to replay — one already upstream, or
one that rebased empty — which stays indistinguishable from a squash and stays quiet
(below). *Can*, not *must*: the commonest drop is a change whose duplicate sits on
`main` directly under the replayed run, and there the window reaches one commit
deeper, the messages verify, and the entry maps to the landing that made its replay
redundant — the ratified double-landing answer, next.

**A change that landed twice keeps the landing git states** (ratified, t-nsww).
Both doubles occur in the wild: a rebase-merged pull's *original* commit later
reaches `main` verbatim through another pull — the stored-base listing above is
how the entry outlives its own landing — and a branch carries a commit, its own
SHA or a cherry-pick, whose change is already on `main`, so the rebase drops it.
Either way one listed entry has two defensible landing sites: where git says the
change sits on the branch, and the copy this pull's own rebase wrote. The mapping
keeps git's answer — an ancestor SHA landed *as itself* whoever landed it, and a
dropped replay aligns to the commit that made it redundant — because the first is
a fact and the second a message-verified alignment, while "this pull's copy is
the real landing" is an intent git nowhere records: precisely the guess this
mechanism exists to remove. The two readings reach different verdicts only when
the landings straddle the walk's base — the pull's copy in range, the other under
an earlier tag — and there git's answer maps the entry to the released landing,
which the range drops with its notice instead of counting the change again
through the in-range copy. The pull's-copy reading re-releases it: t-8xsb's
silence, one shape over.

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
human publishes.

Convergence is on the verdict, and a verdict is a claim about the range only
when the walk **read** the range. A walk that came back short — every commit
unknown to the queried repository, a merged pull whose merge point nothing
resolved, a commit GitHub had not indexed, a pull whose commit listing GitHub
**truncated** at its 250 cap, or a **shallow** checkout in which git cannot place
a listed commit at all — hands down the same empty fold as a range that genuinely
holds nothing, and glyph acted on both alike: it deleted
the rolling draft on the very reading it had just told the operator to re-run
(t-441z), and, where one commit *did* classify, it retagged an existing v1.0.0
draft down to v0.1.1 out of a fold missing the `:boom:` pull — exit 0, green,
and a human publishing that draft burns the tag forever. So an incomplete walk
**fails loud (4)** before the releases listing is even fetched: no delete, no
retag, no draft, no verdict — the same refusal to judge an unread range that
the wedge, the covered pull and the published floor have always made (ratified
t-pysg, replacing #66's warn-and-refuse-to-destroy, which still built and
raised drafts, banner and all, out of folds it had just called short). The exit
that #66 rejected was rejected for a repository whose merge button an
automation presses, which then warned on every release structurally; merge
points resolve now (t-7zt7), so what remains of that shape is a bot-authored
merge commit — a walk blind to a whole pull, which is exactly what the exit is
for, and the escape is the wedge escape: cut a tag at or past what the walk
cannot read, or fix the checkout (`fetch-depth: 0`) when the shortfall names
it. Ordinary API lag clears on the re-run the error asks for. The same family,
reached from the output side: a composed body over GitHub's release cap —
**125000 characters, not bytes**, measured live against the API with the
over-by-one and the multibyte case both probed (`internal/cli/bodylimit.go`
keeps the method beside the number) — **fails loud (4) before anything is
written**, dry run included. The walk read the whole range, so a truncated
body would publish a wrong document as the release; and without the guard the
run computes its verdict, spends the write's retry schedule on a POST GitHub
always 422s, and exits 4 anyway — one draft later. The escape is the wedge
escape again: cut an intermediate tag so the next walk, and its body, is
smaller. (The preview's sticky comment takes the OPPOSITE degradation —
truncate at its 65536-char comment cap, marked in the comment and warned on
stderr — because that surface is advisory and refreshed on every push, and a
refusal there would take the whole verdict comment down with it.) A next version
not strictly above the latest published
release fails loud (an unpublishable draft; a deleted published release's tag
is burned forever).

The refusals above are about evidence — a range glyph could not read. One is
about **authority**, and it is the only release-time refusal a dry run does not
reproduce. A release run started from a ref that is not the repository's
**default branch** **fails loud (4) before the walk**, because everything
downstream of that ref is silently wrong and nothing catches it: the range is
`<tag>..HEAD` over the branch, so its unmerged commits enter the notes; each
resolves to no merged pull and takes the direct-push arm, classified from its
own subject; only an API lag is recorded as dropped, so the walk still calls
itself complete and the refusal above never fires. Green run, a draft
describing work the default branch never held, and Publish cutting the tag
there. A caller cannot close this itself — GitHub offers no way to restrict
which ref a `workflow_dispatch` runs from — and it is not hypothetical
plumbing: four repositories (`canon`, `dotfiles`, `sill`, `swift-toml-edit`)
hand-copy the release step instead of calling glyph's reusable, so the binary
is the only layer that reaches them. The escapes are `--dry-run` and re-running
from the default branch, and both are named in the refusal.

That the ref is exempt from the dry-run rule stated for the body cap and
`--target` (a dry run previews the real run) is the argued exception, not an
oversight: those are properties of the **work** a run would publish, so hiding
them would preview a lie, while the ref is a property of the **write**.
Exempting it can never make a preview more permissive than the run it previews,
since the real run from the same ref refuses unconditionally — and judging it
would red the sanctioned preview path every consumer exposes as a dispatch
input, plus `scripts/fleet-preflight.sh`, whose probes all pass `--dry-run`.
The boundary is read from the event payload at `$GITHUB_EVENT_PATH`, then from
the repository object; **two** sources because one would mean a payload
reshuffle refuses every pinned repository at once, with eleven pin reverts as
the only recovery. `gitsource.DefaultBranch` is not among them: it reads
`refs/remotes/<remote>/HEAD`, which `actions/checkout` never writes, so in CI
it is permanently unresolved. The guard arms when **either** `$GITHUB_REF` or
`$GITHUB_EVENT_PATH` is present — one witness missing is a refusal, not a
shrug, so a step that blanks one cannot disarm the boundary green — and with
both absent (a laptop) the run proceeds and says on stderr that its ref went
unjudged. That last case is a deliberate hole: no documented caller writes a
release from outside Actions, and closing it would refuse a class that does not
exist. `release.yml`'s own step stays as the cheaper half — it refuses before
the checkout and the Xcode setup, which the binary cannot do because it has not
been installed yet — and the two must never drift on polarity. Note that
`fleet-preflight` is structurally blind to this refusal (its probes carry no
ref and pass `--dry-run`), so glyph-test's live-fire range is its only oracle.

A delete whose answer is LOST counts as done when its retry
finds the release already gone: DELETE is idempotent and the id is what glyph
asked to remove, so failing there aborted the upsert over work that had
succeeded (t-yq7m). t-yq7m fixed one shape of that; **the order is the general
fix**, so as of v1.0.0 the rolling draft is WRITTEN FIRST and the stale drafts
are converged after it. Measured before the reorder, against an API answering
every `DELETE` with 503: the run burned the whole 1s→4s→16s schedule, exited 4,
and sent the `PATCH` **zero** times — a delete spent the run's only chance to
land the notes. The mirror image was lost too: with the delete first, a failed
write left the strays already gone, so the run destroyed state and landed
nothing. A stray that will not go is now a **warning on a green run**, because
the exit code of `release` answers whether the *verdict* landed and after the
write it did; what remains is bookkeeping over a draft, where no tag exists,
nothing is published, and no new draft is created while one exists — so the
stray set is self-limiting and the same failure simply repeats next run. Failing
there instead would red the release job at its `status -ne 0` gate before it
reads the verdict, i.e. a stray GitHub will not delete would stop the repository
shipping artefacts while its notes were correct (t-rncn). The leniency is
subordinate to a write that succeeded: on a **none** verdict the delete is the
entire action, so it still fails loud, and an interrupt is never absorbed on
either path. The price is named rather than hidden — a 404 is also how
GitHub answers for a repository the credential can no longer see, and on a none
verdict there is no following write to catch that, so such a run reports the
draft as *found already gone* instead of *deleted* and says the claim is
unconfirmed. A 404 on the FIRST attempt is untouched: that one is the id
vanishing under the run. The create has the same lost-answer problem with the
opposite polarity, and gets the mirrored fix: a draft has no tag for GitHub to
collide on, so a `POST` whose answer was lost used to be replayed blind and
every replay minted another identical draft (measured: two from one lost
answer, four from a spent schedule — t-ph6p). Before each re-send the client
now probes the release listing for what the earlier copy would have made —
same intended tag, same draft state — and adopts it as the create's answer;
the probe is one round trip, best-effort, and a probe that finds nothing or
cannot look falls back to the replay, with the upsert's convergence still
deleting any duplicate on the next run. `--dry-run` computes everything, action included, and
writes nothing. The `--json` verdict also carries the walk's expansion
provenance (`pulls`: each resolved pull and its participating commit count), so
how a verdict was assembled can be read back afterwards — by a human reviewing a
draft, or by a CI step — without re-deriving the walk's exclusion rules in
shell. It reports what the walk DID and never why a count is what it is: 0 has
two innocent causes (a stacked pull whose commits rode in with its base, a
merge-merged pull the fallback already folded), so it never means "this pull
changed nothing".

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

Binary `glyph`, module `github.com/akira-toriyama/glyph`. Subcommands: `lint`,
`fmt`, `bump`, `notes`, `preview`, `release`, `doctor`, `rules`, `hook`, `version` —
everything `glyph --help` prints except cobra's own `completion` and `help`.
This line and the tree below are the two places in this document a new command
or package has to be added, and both had gone quietly out of date: before t-0cqs
the list was two commands behind (`preview`, `hook`) and the tree four packages
behind (`markdown`, `preview`, `hook`, `workflows`). Read them against
`glyph --help` and `ls internal/` rather than trusting them.

```
cmd/glyph/main.go        os.Exit(cli.Execute()) — thin process boundary only
internal/core            exit-code contract + structured Error (no I/O, no logic)
internal/version         ldflags build identity + ReadBuildInfo fallback
internal/cleanup         git's message cleanup, modelled exactly (comment strip, scissors cut) — what --stdin judges is what git records
internal/bump            Level lattice; Classify; Reduce(max); Next; stdlib semver
internal/config          v2 glyph.toml loader — user RE2 patterns, first match wins, semver_sigil extraction (epic e-qzpz; not yet wired to any command)
internal/draftplan       v2 draft convergence — pure; which draft a verdict keeps, retags or deletes (the Unreleased placeholder lives here; not yet wired)
internal/markdown        Line: per-field escape, then the mention fence over the assembled line
internal/notes           group by section; text/template render (no external tmpl dep)
internal/preview         merge-preview comment body — pure; no git, no API, no clock
internal/gitsource       local `git log BASE..HEAD` (exec.CommandContext)
internal/github          commits/{sha}/pulls, pulls/{N}/commits, release CRUD, repo object
internal/doctor          repository-precondition checks; independent, read-only (§7)
internal/hook            commit-msg hook contents + overwrite policy (no rules of its own)
internal/cli             cobra adapter; Execute() int owns the exit-code funnel
internal/testutil        the hermetic git fixture shared by tests (test-only, ships nothing)
internal/workflows       no runtime code — tests pinning CI-YAML invariants
```

**Why the five newest boundaries exist** — the tree says what each package
holds, and each package's doc comment argues its own internals; what belongs
here is only why it is a package at all, and what depends on it:

- `internal/markdown` — one owner for the escaping ORDER (per field, then over
  the assembled line), because both renderers, `notes` and `preview`, have to run
  it the same way round and a copy in each is a copy that drifts.
- `internal/preview` — the merge fold is version arithmetic, so it sits above
  `internal/bump` rather than in `pr-verdict.yml`'s jq, where it was a second
  rank table living on the fleet's side of the pin.
- `internal/hook` — the generated hook is a consumer of the exit-code contract
  that glyph WRITES, so its gate code is interpolated from `core.CodeLint`
  (below) instead of typed as a shell literal. `internal/cli` imports it to
  install, and `internal/doctor` imports the same embedded bytes to
  byte-compare what a repository actually has (#81).
- `internal/workflows` — the one package whose subject is a directory rather
  than a type (`.github/`), hence no runtime code, no importers, and nothing in
  it that ships.
- `internal/testutil` — one home for the hermetic git fixture, because its
  environment pin is an incident-bearing block and verbatim copies quietly
  lose incidents (a partial copy in the hook tests had already lost the
  maintenance pin). Imported only by `internal/cli` and `internal/gitsource`
  tests; nothing shipping depends on it.

**Exit-code contract** (`internal/core`): `0` ok · `1` no release · `2` usage ·
`3` convention violation · `4` no trustworthy answer — API/git/IO, a refusal
to judge what it could not read, or a refusal to write from a ref it has no
authority over (a release run off the default branch, §4) · `130` interrupted. Errors are
classified at the source into `*core.Error`; `ExitCode` funnels everything
(unclassified ⇒ API, never usage). `3` is the *gate* code — what glyph was asked
to judge does not conform: a commit message under `lint`, a repository's own
configuration under `doctor`. Same class, different subject; no new integer.

One command sits deliberately off the `1` rung: for `preview`, `none` is a real
answer to the question asked — *what would merging this do?* — so a none verdict
exits `0` there. `core.CodeNoRelease` is constructed in `cmd_bump.go`,
`cmd_notes.go` and `cmd_release.go`, and nowhere else. Outside Go the integers
are branched on in several places — `lint.yml` on `0` and on
`jq -e '.error.code == 3'`, `release.yml` and `goreleaser.yml` on `1` — but the
generated commit-msg hook is the one such consumer glyph WRITES, so its gate code
is interpolated from `core.CodeLint` rather than typed as a shell literal; it
forwards `3` and only `3` and exits `0` on every other failure (`internal/hook`,
above).

**Stream contract:** stdout carries the payload, stderr the diagnostics — and
stderr has a *shape*, because two machine-readable things share it. Every line
is either a `::`-prefixed GitHub workflow command or part of the one
`{"error":{…}}` envelope, which is written **last** (from the CLI's exit funnel,
after the command returned; cobra is silenced and every git subprocess writes
into a buffer, so nothing follows it). A consumer therefore sieves the envelope
out — `sed -n '/^[{]/,$p'` — before handing it to `jq`: jq over the two shapes
together is a parse error, and both shipped reusables buried that failure under
`|| true`, so a run that warned before it failed printed **no** `::error::` at
all (t-sws7). The envelope's `message` is folded onto one line at that single
boundary for the same reason the annotations are: a consumer interpolates it
straight into a `::error::`, and the runner parses a workflow command up to the
first newline — JSON-escaping the newlines keeps the *bytes* valid while the
*value* still loses everything past the first line, which is how this stayed
invisible.

The same incident settled who *renders* a finding: the binary that computed it.
`lint --range` writes one `::error::` per violation onto the stream before the
envelope, so a consumer's whole job is `cat` — replay the stream verbatim and
frame only the summary (`.error.message`). Rebuilding the per-finding lines out
of `.error.details` in a caller's jq is exactly the reconstruction that vanished
under `|| true`, on the fleet's side of the pin where no test here could see it,
so `internal/workflows` bans the read itself
(`TestNoWorkflowRebuildsPerFindingAnnotations`) and the mutation ledger holds the
producer half (`lint-findings-lose-their-annotations`).

**Machine-output flag:** one spelling, `--json`, on every command that has one —
`bump`, `notes`, `preview`, `release`, `doctor`, `rules`, `version` and
`hook install` (`lint` speaks only in exit codes, and `fmt`'s stdout IS the
payload — the corrected message, pipeable into `git commit -F -` — so neither
has one). It was
`--ndjson` on the last two until v1.0.0, which was wrong twice: the flag named a
format glyph has never emitted (`printCompact` writes ONE object, not a stream)
and it split the surface, so a caller had to remember which subcommand took
which — measured before the rename, `version --json` and `bump --ndjson` *both*
exited 2 with `unknown flag`. The flag is read by VALUE and not by `Changed`, so
an explicit `--json=false` selects the human line at exit `0`; unlike
`rules --md`, the machine flag is not the default-bearing member of its group,
so declining it selects a real output rather than nothing and it carries no
`checkDefaultModeOff`. Enumerated from the command tree at run time by
`internal/cli`'s `TestMachineOutputFlagHasOneSpelling`, so a new command that
invents a third spelling fails there rather than in a caller's shell.

**Preset embedding:** `//go:embed presets/*.toml` inside `internal/config` —
the preset files are the single source: `glyph init` writes them byte for
byte and the config package's own tests load them, so the generated artifact
and the loader cannot drift apart silently (`TestEveryPresetLoads`).

**Testing** (stdlib only, no testify): table tests; a golden for the
dry-run release body (`internal/cli/testdata/release_dry_run.golden.md`);
`internal/workflows` pins what the CI YAML cannot state about itself; fuzz
over the pattern match (never panics; every outcome one of the three legal
shapes), the fold (order-independence), version parse/step, the `Link:`
header parser (it extracts, never fabricates) and both Markdown escapers. One fuzz target is not a parse
test at all: `FuzzNextPageOrigin` machine-checks a SECURITY invariant — no
`Link:` header a server sends can move a token-bearing request off the configured
origin — and spells the expected origin out literally rather than calling
`sameOrigin`, so a bug inside the comparison cannot make the property agree with
itself. `rg '^func Fuzz'` is the current list, not this sentence. Always `-race`.

**Anything that models an external system carries one test that asks the real
system**, because a closed loop of glyph-against-glyph proves nothing about the
thing being modelled. `internal/parser` shells to `git stripspace` — git's own
`strbuf_stripspace`, the authority on what git will actually record — with
`GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` pinned to `/dev/null` so a personal
`core.commentChar` cannot move the answer. `internal/markdown`'s rules were
measured against GitHub's own renderer (`gh api -X POST /markdown`, mode=gfm),
with the probes, their observed output and the date of the run recorded in
`markdown_test.go`; re-run them before changing a rule, because the one rule
changed from reasoning alone was wrong (an at-sign written as `&#64;` renders
back to `@` and GitHub's mention post-processor linked it anyway, long after
CommonMark had decoded the entity).

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
guards the tag space, and `--dry-run` previews any verdict. Full rollout — and
everything else still open — is tracked in the `projects` furrow board, which is
the single home for it (§8 keeps no copy).

Two behaviours of the shipped workflows are contract, not implementation
detail. `lint.yml` carries a second, annotate-only arm (#140): a caller may add
`push: branches: [main]` and the same rules run over each direct push to the
default branch, but exit 3 is swallowed on that arm alone — that history is
immutable, so a red verdict there could never be made green again, and a
permanently red check trains a fleet to stop reading its own gate. And
`release.yml` hands its verdict back as `workflow_call` outputs — `level`,
`next`, `current`, `action`, where empty means NOT COMPUTED, never "none", so a
caller gates fail-safe on `""` (#155). The draft's URL is deliberately
withheld: with the API handle in hand, auto-publishing the draft is a two-line
caller step, and the human act of publishing — the safety net everything above
rests on — stays structurally out of a caller's reach.

**The grammar is the repository's file (v2, superseding the 2026-08-16
flag-not-file ratification):** v1 refused per-repo config because a synced
TABLE could drift from the pinned binary. v2's config is a different object —
not a copy of anything glyph owns, but the repository's OWN grammar, versioned
in its own history and read by whatever binary its pins name. The drift the
v1 stance guarded against (two copies of one truth) has no second copy left
to drift. The reusables therefore take no grammar input at all: the binary
finds `glyph.toml` at the checkout root, the same file the hook and a
developer's shell read.

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

The checks follow no vocabulary, and they guard two layers. One check guards
the CONFIG: `glyph.toml` exists at the checkout's top level and loads — v2's
config-first invariant, which until this check had no machine verification
anywhere (the fleet reality it exists for: a repository whose pin moves before
its config exists fails every gate at exit 2, with nothing having said so in
advance). The rest guard the WALK (squash policy, pins, hooks), which is
grammar-free since v2: every walk precondition holds whatever the repository's
`glyph.toml` says.

Shape: independent checks → one report object → an exit on the aggregate.
**Read-only, always** — a diagnostic that mutates cannot be run casually, and
this one is meant to be. Each finding carries a stable kebab-case id (branch on
that, never on the prose), the observed and expected values, what breaks, and the
concrete command or edit that resolves it — a `gh api -X PATCH` only for the
repository-settings checks. Independence is structural: one unreadable input
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

- **`glyph.toml` missing or unloadable ⇒ fail.** The verdict commands exit `2`
  on a missing config because for *them* the invocation was the mistake — the
  caller assumed a v2 repository. `doctor` was asked whether the repository
  satisfies glyph's preconditions, and an observed absence is the honest answer
  no: the whole gate is down, at `3` like every other violated precondition. A
  file that exists but does not load is the same failure carrying the loader's
  own error (the loader rejects rather than repairs — no silent none). Only
  content that was never *observed* — a read the filesystem refused, no top
  level to resolve the path against — is `unknown` at `4`.
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
  `none`. That is measured (`TestSinceTagMergeCommitReproducesVerdictWhenFullyDark`):
  fully dark, a merge-merged pull reproduces its live verdict (`minor` either way); with only the merge point at 422, the same
  repository is a lost pull — an incomplete walk, which `release` refuses at
  exit `4` (§4) and `bump`/`notes` report at two warnings. A rebase splits them
  the other way: it
  writes new shas that appear in no pull's listing, so a replayed commit classified
  during the lag is folded in again when the last one expands the pull. Note also
  which failures actually reach the fallback: only a 422 (`IsCommitUnknown`). A 403
  rate limit, a 5xx outliving the retry schedule (t-bjrv) and a dead socket all
  leave `walkSince` as an error and exit `4` — the outage window is an exit-code
  question, not a classification one. Squash is therefore the landing style with no
  partial state at all; the cost of a dark API under squash is that a MULTI-commit
  squash carries the PR title, so the fallback reads one subject the range walk
  never saw. `lint --pr` is that subject's own gate — CONTRIBUTING ratifies the
  title as a commit subject and lint.yml runs it beside the range — so the
  fallback now reads a *linted* subject, though only as reliably as the gate's
  trigger re-fires on a retitle
  (measured `minor` → `patch`, and `minor` → `none` for a title
  with no gitmoji — `TestSinceTagSquashMultiCommitDivergesWhenAPIDark`,
  `TestSinceTagNonGitmojiPRTitleCountsNoneWhenDark`). One wrong level on one pull, versus a whole pull lost.
- **`allow_merge_commit` / `allow_rebase_merge` true ⇒ advice, not failure.** A
  merge commit *used* to be data loss (`bump.Excluded` drops 2+ parents, so the
  PR vanished — t-7zt7); with the walk expanding merge commits correctly it costs
  no bump while the API answers, and none at full darkness either — the branch
  commits are on `main` and classify themselves. What is left is the squash-only
  house convention plus one *loud* window per style: an unresolved merge point
  (API lag, or a bot-authored merge, where it repeats every release) is a lost
  pull that stops `release` at exit `4` until a tag clears it (§4), and a rebase
  whose listing the walk cannot align
  against what landed — one that dropped a commit it was asked to replay, already
  upstream or rebased empty — can still
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
- **A stale glyph-written hook ⇒ fail; no hook at all ⇒ pass.** One check per
  kind (`commit-msg-hook`, `pre-push-hook`), because a `Check` carries ONE
  observed/expected pair and folding the two would collapse "commit-msg current,
  pre-push stale" into prose a CI gate has to parse. The
  question is drift, not adoption. `internal/hook` interpolates the lint gate
  code from `core.CodeLint` so a renumbered constant cannot leave behind a hook
  comparing against a code glyph no longer emits — one that waves every
  violation through. That holds for the hook glyph *writes*; it holds for
  nothing already on disk. Hooks are untracked, so no pull, no fleet-sync and no
  CI job refreshes one, and everything it decides — the gate code, the arguments
  it hands `lint` — was frozen by whichever glyph was on PATH that day. It fails
  in the quiet direction: a stale hook still exits 0, which is what a clean
  message looks like. Hence the one artefact here that glyph itself wrote is
  also the one nothing else can notice going wrong.
  Absence passing is the argued half. A hook is opt-in, CI is the authority, and
  an Actions checkout cannot have one *by construction* — grading that as advice
  would post a notice on every run in every repository, which is the noise that
  teaches a fleet to stop reading the report. A hook glyph did not write is
  advice: `hook.Install` refuses to overwrite one, so it is a standing choice
  rather than drift, and rare enough to say so once. The hooks directory is
  git's answer (`core.hooksPath` relocates it; the family's older repos point it
  at a tracked `scripts/hooks`), which makes this the one check that needs a
  subprocess — resolved in `internal/cli` beside every other one, since
  `internal/doctor` runs no git exactly as it makes no request.
- **The byte-identical hook is also FIRED (`commit-msg-hook-fires`).** Current
  bytes prove the script; they prove nothing about the glyph the script resolves
  on PATH at run time, and the script blocks only on the gate code and waves
  every other failure through *by design* — so a PATH wrapper building a
  different checkout (the documented worktree trap) or a tree that no longer
  compiles is a local gate answering 0 to everything while the byte-compare
  calls it healthy. `internal/cli` executes the hook with a violating scratch
  message (a subprocess, like the hooks-dir read; `internal/doctor` only renders
  the outcome) and the check fails when the probe passes through — the split
  "bytes current, gate dead" is the finding. It fires the byte-identical arm and
  nothing else: someone else's hook is theirs to run, a stale glyph hook already
  fails the drift check, and a read-only diagnosis must not execute code it does
  not vouch for. Executing the hook glyph itself wrote is still read-only in the
  sense the report claims — the hook lints a scratch file and changes nothing.
  A probe that cannot run, or an exit outside the script's own two-code
  vocabulary, is unknown, never a verdict.

## 8. Where we are

No phase list: a numbered plan has to be re-edited on every release, and the one
that stood here was not — it still called the gitmoji table "Phase 1" long after
it shipped, the same rot that left §5's subcommand list and package tree behind
the binary (t-0cqs).

**Shipped.** Every engine deliverable is something you can run or read in this
tree, and it is inventoried where it is already maintained rather than a third
time here: the commands are §5's list, each with its own `--help`; the three
reusable workflows and the one install action are §6; `git tag` is the release
history and the only answer to "which version". README's status line is the
one-sentence state, and it is the only prose that should need touching when that
changes.

**Self-adopted, partly — know which half.** glyph consumes its own reusables at
a pinned tag: `.github/workflows/commit-lint.yml` calls `lint.yml`,
`version-preview.yml` calls `pr-verdict.yml`. It does NOT consume its own
`release.yml`. glyph's tags are cut by `goreleaser.yml`, which renders the
release body with `glyph notes --since-tag=below:TAG` run from the tagged
commit (the predecessor resolved by the binary — the workflow once re-derived
it in shell over git's refname sort and inherited the defect that sort has,
t-s5n4) — so the
notes renderer is dogfooded while the rolling-draft path is not exercised
end to end by anything in this repository. What holds `release.yml` here is
`internal/workflows` (the install action stays single-source, the binary version
is derived from the caller's pin, the commented caller stub keeps its `@vX.Y.Z`
placeholder, the checkout stays `fetch-depth: 0`) plus the fake-API tests behind
`glyph release`. Worth knowing before trusting "we dogfood it" about a
`release.yml` change.

**Next.** Everything still open lives in the `projects` furrow board — the
pointer §6 already carries, not a second copy of it here: a doc-side task list
is a second source of truth, and the two go stale against each other. (The
fleet migration this paragraph used to name completed: every consumer pins
v1.0.0 or later, audited daily.) Two design decisions this document names in place, so that
dropping the phase list does not lose them: the initial-tag knob (§1), and
narrowing the reconciliation refusal to the merge-button shape, which is
placeable without a canonical commit (§4).
