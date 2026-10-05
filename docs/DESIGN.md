# glyph — design

The canonical design for glyph, a self-built, sigil-driven release engine.
The commit grammar is **not** defined here — since v2 it lives in each
repository's own `glyph.toml` (§2), written once by `glyph init` and then
owned by the repository. This document is the *why* and the *shape*.

## 1. Problem

The fleet squash-merges everywhere. GitHub's
`squash_merge_commit_title = COMMIT_OR_PR_TITLE` rewrites the squash subject to
the **PR title** on any multi-commit PR, erasing per-commit types from `main`.
Every tool that types a release from **commit text** (git-cliff today,
semantic-release, release-please, cocogitto) is therefore fooled. glyph instead
derives the bump and notes from the **individual commits inside the PR**, and
made gitmoji the type driver. (That last half is the v1 statement: since v2
the driver is the repository's own pattern file and its `semver_sigil` (§2),
and the gitmoji prefix is visual convention the patterns may or may not
require. The problem, and the PR-resolution hop below, are unchanged.)

Two inversions from the prior house convention:

- **gitmoji drives classification/semver** *(v1 — superseded by §2's sigil)*.
  Previously the Conventional type
  decided the bump and the gitmoji was stripped before parsing. In v1 the
  leading `:code:` *was* the type; in v2 the sigil beside it is.
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
  both map textual shortcodes to semver with nearly glyph's v1 defaults, and the
  latter uses the same subject grammar. glyph's v1 table was bigger (75 codes) and
  compiled in rather than configured — a packaging choice, not an invention, and
  one v2 then dissolved entirely into the repository's own pattern file.
- **Deferring the tag to publish is NOT a differentiator** — inherent to any
  draft-based tool (release-drafter carries `tag_name` on the unpublished draft
  exactly as glyph does).
- **Two real differentiators worth defending**: glyph can resolve to *no release*
  (release-drafter falls back to `patch` whenever no category matches, so it can
  never say "nothing shipped"), and glyph's walk is self-baselining
  (release-drafter requires a previously PUBLISHED release or it warns and returns
  nothing — which is also the strongest argument for the backlogged initial-tag knob).

Only permitted external dependencies: cobra (the CLI frame, per house pattern;
its flag package pflag rides along as a direct require) and go-toml (the
`glyph.toml` decoder). The v1 gitmoji spec dataset left with
the embedded table.

## 2. Commit format

A note on the name before anything else: "v2" throughout this document is the
**engine generation** (epic e-qzpz's sigil redesign), not a tag. The generation
ships as tag **v3.0.0** — the existing `v2.0.0` / `v2.1.0` tags are v1-grammar
releases, and they are what the fleet is pinned at until the rollout moves the
pins. "Upgrade to glyph v2" must never be read as "pin a v2.x tag": a real
`pin glyph v2.0.0` commit exists in glyph-test, and it pinned the OLD grammar.

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
  from lint, bump and notes entirely. The presets skip merge commits alone,
  whose diff is never read; v1 carried them and the `fixup!`/`squash!`
  autosquash artifacts as hardcoded exemptions, and the presets skipped the
  artifacts too until t-mfny claimed them as unlandable (below). The hook
  path is where git's own subjects matter most — an author cannot rewrite a
  subject git generated, so refusing it forces `--no-verify`, which turns the
  gate off; a skip and an unlandable claim both let it through.
  - **A preset fixes, skips or claims only what git spells; a subject its
    author writes carries its own sigil** (t-h9y7, ruled 2026-09-29;
    `TestConventionalPresetTakesTheVersionOnlyFromTheSigil`, mutation row
    `conventional-preset-reads-the-type-as-the-version.patch`, the twin of
    `gemoji-preset-grows-an-acceptance-window.patch`). Beside its grammar's
    own pattern a preset carries only messages git writes — the raw revert,
    merges, the autosquash artifacts and `amend!` — and the two presets are
    one grammar over two prefix vocabularies: what `init` writes differs
    only in the header and `style` label, the vocabulary note, the
    template's form line, the first pattern's comment and regex, and the
    `amend!` body regex that mirrors it. `TestPresetsDifferOnlyInThePrefix`
    holds every other byte equal, the first pattern's other keys included,
    and `TestPresetsShareOneGrammar` holds by sample what the masked lines
    share: every sigil over each scope shape the class admits, in the
    grammar and in the `amend!` body, the whole-line subject, the
    sigil-less refusal and the footer read as prose (mutation rows
    `presets-drift-outside-the-prefix.patch` — a conventional-only revert
    change that survived every other test —
    `presets-grammar-grows-a-key-in-one-preset.patch`,
    `presets-scope-class-drifts-in-one-preset.patch`,
    `presets-amend-body-drifts-in-one-preset.patch` and
    `presets-read-the-breaking-change-footer.patch`). So a ruling that
    changes one preset lands in both, and one that changes a class inside a
    masked line adds its sample there for every preset — a sample is all
    that holds those lines. `--conventional` is therefore Conventional
    Commits with the sigil before the colon, over the presets' lowercase
    classes (a `[a-z]+` type and a `[a-z0-9-]+` scope, stricter than the
    spec, which makes its units case-insensitive and asks only that a scope
    be a noun): the spec's own `feat!:` reads as `!` unchanged, a sigil-less
    `feat:` is refused at exit 3 by both hook modes, `lint --range` and
    `bump`, and a `BREAKING CHANGE:` footer is prose — `fix~:` over one
    folds patch, silently (measured 2026-09-29; lint has no taste, below).
    That is the grammar epic e-qzpz ratified: the sigil is the only version
    input, a prefix — a conventional type included — is for the reader, and
    profiles and footer parsing are gone. `--conventional` was sketched
    with a mandatory sigil group from the start (t-w7dv), refusing a
    sigil-less `feat:` is the write-it-down half of the sigil's job
    (`TestConventionalPresetGrammar`), and the preset exists for
    repositories that cannot take gitmoji, not to align with Conventional
    Commits (epic e-b3t3: no obligation to).
  - **Plain Conventional Commits is deliberately absent** — the type
    deciding the version, the footer raising it — with the reason, so
    nobody re-files it as an oversight. glyph read it once, as the v1
    grammar's conventional profile (tag v2.1.0: an embedded table, `feat`
    minor, `fix`, `perf` and `revert` patch, the other seven types none,
    footers parsed), and v2 deleted the table with the footer parsing
    (glyph#187, tag v3.0.0). It ships neither as a third preset nor in
    `--conventional`'s place, and no copy of its patterns is kept in prose:
    a pattern block no loader reads is a copy that drifts, as the packages
    paragraph did between the presets until one embedded snippet replaced
    the copies (#228). A repository's own file may still say it — a `(?s)`
    footer pattern, a `!:` capture, fixed sigils on `feat` and `fix` and a
    none-folding catch-all read the spec's version rules (`feat` minor,
    `fix` patch, `feat!:` and a footered `fix:` major, `chore` none —
    measured 2026-09-29) — and "readable from the file alone" (§3) is no
    objection: it bars verdict inputs outside the file, and patterns are
    inside it. What keeps the block out of every file `init` writes is what
    it brings back, each measured on it the same day. Its catch-all folds a
    typo'd or unmarked type as a silent none: `feta: add the export command`
    and `refactor: rename the public Parse API` lint 0 and bump none with
    nothing said, the hole the window's retirement took out of the presets,
    where `--conventional` refuses the first at 3 and folds `feta^:` minor —
    which is why its type word may be free. Closing the catch-all means
    listing the types, v1's table moved into the file. Its footer read is
    the regex over the raw message §3 rejects as a trailer parser: a
    `BREAKING CHANGE:` line inside a body paragraph releases a major, and
    the spec's own token is no git trailer (the space — `git
    interpret-trailers --parse` answers nothing for it, git 2.54), so
    written in the trailer block it voids the block and the release line
    drops its `$coauthors` credit. It has no `%`, so a 0.x repository could
    never say 1.0 (`feat%:` refused, `feat!:` at v0.4.2 reads v0.5.0), and
    adding one sets a sigil beside the implied levels — two spellings of
    one version. Nor does it read the spec as written, the case for putting
    it in `--conventional`'s place: over the presets' classes `Feat:`,
    `FEAT:`, `feat(API):` and `feat(ui/button):` are refused, as
    `--conventional` refuses them with a sigil, where the spec makes its
    units case-insensitive and leaves a scope's spelling to the noun it
    names. A repository that keeps plain Conventional Commits writes those
    patterns into its own file and owns what they fold (a catch-all is
    legal — lint has no taste). No consumer was waiting: every fleet
    `glyph.toml` is the gemoji grammar (measured 2026-10-04 on each
    unarchived akira-toriyama repository's default branch through the
    GitHub API: 38 commit one, all `style = "gemoji"` with no type group,
    and 3 commit none), and the motive — running beside another
    repository's convention unchanged — left with the outreach path
    declined on 2026-09-15 (t-9q9h).
- A pattern may carry **`warn = '<message>'`** — a message the file's author
  wrote for a commit's author, for a pattern that is legal but undesirable.
  The verdict is untouched; the message is surfaced **wherever the pattern
  wins a verdict**: lint (`--range`, `--stdin`/`--message`, `--pr`, both
  hooks) annotates it per commit, and the fold puts it on the commit's row in
  the machine verdict (`bump --json`'s `commits[].warn`), which `bump`,
  `release` and both of `preview`'s folds each announce. The invariant is
  deliberate — a warning loud at one gate and silent at another teaches the
  reader that the loud gate is noise. Under `[[packages]]` the preview
  announces it over the pull's whole listing, once per participating commit:
  announced per touched line, a warned commit on no line was silent there
  while the single line's preview and packages `bump` said it, and one on two
  lines was said twice (measured 2026-10-05,
  `TestPreviewPackagesWarnsEveryParticipatingCommitOnce`; mutation row
  `preview-packages-warns-per-touched-line`). `warn` on a `skip` pattern is a load
  error (a skipped commit is outside every verdict, so the warning would fire
  for nobody), and so is an empty `warn`. The key was made for the
  **v1-acceptance window** (t-37xj): the migration pattern that accepted a
  sigil-less gitmoji subject as `=` none while the fleet's histories still
  held pre-sigil commits, where before the warning a forgotten sigil passed
  lint green and folded silently — measured on dotfiles as a release that
  simply stopped (v1 verdict v1.0.0, v2 verdict none, nothing said).
  - **The window is retired** (t-j4c5, ships as v4.0.0; ratified 2026-09-16,
    reaffirmed 2026-09-24: zero migration debt, breaking changes accepted).
    `init --v1-window`, the embedded snippet and glyph's own window block are
    gone — `glyph.toml` here is the bare gemoji preset, byte for byte
    (`TestGlyphOwnConfigIsTheGemojiPreset`), and a sigil-less subject is a
    violation under the shipped grammar (mutation row
    `gemoji-preset-grows-an-acceptance-window.patch`). The block's own
    trigger — every commit behind the release walk's base carries a sigil —
    was met here at v3.3.1. A repository still carrying the block deletes it
    once its own walk base clears its pre-sigil history, and until then its
    verdicts do not move: the window was config, never binary. `warn` stays —
    a grammar key any pattern may carry, published in the machine verdict,
    not the window's private hole.
- A pattern may carry **`unlandable = '<reason>'`** (t-t84a) — a message
  that may be WRITTEN but must never LAND. Every gate that judges a commit
  which already exists reads it exactly as a message no pattern claims, with
  the reason in place of the no-match sentence: `lint --range` and the
  pre-push hook report a violation, `lint --pr` refuses the title, the fold
  refuses the range (Q2, §3), the notes render the raw first line in author
  sections only, and `exclude_authors` still comes first. Only the
  commit-msg hook's modes (`--stdin`, `--message`) let it through, at 0,
  printing the reason and that the later gates refuse it — an argued gap
  between the hook and CI (§2.1). `Match` reports it in the UNMATCHED
  shape on purpose (mutation row `config-unlandable-lands-in-history.patch`):
  every history consumer already refuses that shape and already checks
  `exclude_authors` first, so one that never reads the reason still fails
  closed, where a matched shape would have handed the fold a zero sigil —
  a silent none — wherever a consumer missed the new arm. It contradicts
  `skip`, `warn` and a fixed `semver_sigil`, each of which would give the
  match a second answer, and an empty reason: all load errors.
  - The key was made for git's `amend!` subject, the first case where a
    skip's safety property failed; `fixup!` and `squash!` are the second
    (below). `git commit --fixup=amend:<c>` and `--fixup=reword:<c>` write
    `amend! <subject>`, and `rebase --autosquash` REPLACES the target's
    message with the amend! body — unlike `fixup!` and `squash!`, whose
    target keeps its subject, and with it its sigil. Skipping amend! (the
    presets did, glyph#241 until t-t84a) dropped the only commit carrying the
    new sigil: `:bug:~ fix b` plus a reword to `:boom:! fix b` folded to
    patch with every gate green, while the history autosquash writes folds
    to major. Leaving it unmatched refuses it at the hook too, which forces
    `--no-verify` for a subject git spells itself — the reason `skip`
    exists. `TestUnlandableAmendEndToEnd` asks git for every step: the
    installed hook on both `--fixup` forms, the range gates on the history
    git recorded, and the verdict after a real `rebase --autosquash`.
  - **The presets claim `amend!` as unlandable, one release after the key**
    (`TestPresetsNeverSkipAmend`, `TestShippedPresetRefusesAnUnsquashedAmend`,
    `TestInstalledHookGatesRealCommits`; mutation row
    `presets-skip-amend.patch`). A key only works where the binary reading
    the file knows it: glyph's own CI runs a pinned release, `glyph.toml`
    here is the gemoji preset byte for byte, and a release older than the
    key refuses the whole file at exit 2. So v4.2.0 shipped the key with the
    presets leaving `amend!` unmatched — every gate refusing it, the hook
    included, v4.1.0's behaviour — and the presets claimed it only once every
    pin that reads a preset-derived file read a release carrying the key.
  - **The presets claim `fixup!` and `squash!` as unlandable too** (t-mfny,
    ratified 2026-09-29; `TestPresetsNeverSkipAutosquashArtifacts`,
    `TestPackagesFixupIsUnlandableEndToEnd`,
    `TestShippedPresetRefusesAFixupAutosquashCannotFold`, `TestLintRange`,
    `TestInstalledHookGatesRealCommits`; mutation row
    `presets-skip-autosquash-artifacts.patch`). They were skipped on the
    argument the preset's own comment gave: `rebase --autosquash` folds each
    into its target with the target's subject, and so its sigil, intact — "the
    one property that makes a skip safe". But a skip answers for the commit
    before that fold, and for good when autosquash never reaches the target,
    and two cases break it (measured on git 2.54 with the presets as they
    stood at adfc5e1). Under `[[packages]]` a commit's files decide which
    lines it moves, and a skip's are never asked for (§4.1): `:sparkles:^`
    touching `a/` plus a `--fixup` touching `b/` linted green and bumped `a`
    minor and `b` none, while the autosquashed history moves both to minor —
    and a squash-merge fleet's walk folds exactly the pre-squash commits (§4),
    so `b`'s change shipped unversioned with every gate green (the same pull
    squash-merged and walked by `bump --since-tag` over a stand-in API
    answered the first line's tag alone; with the claim it refuses at 3,
    naming the wedge's escape at the merge point). On one line, a `fixup!`
    whose target is outside the commits autosquash rebases — already
    released, already on the branch a topic was cut from, or on the parent
    topic a stacked branch was cut from — survives autosquash untouched and
    was skipped: `lint --range` 0 and `bump --range` "no release: 0
    commit(s)", its change shipped as a silent none. That is the `amend!` case
    (t-t84a) over again — a skip whose safety property fails — and it gets the
    same answer. The hook still passes git's subject, with a reason that names
    both ways out: run `git rebase --autosquash` before the history is pushed
    or merged, or reword the commit with a sigil of its own when autosquash
    leaves it as it is, its target outside the commits being rebased. The
    reword's condition is what autosquash did, not where the target lives: the
    first wording, "when its target is already merged or released", named no
    escape for the stacked branch, whose target is neither (mutation row
    `presets-fixup-reword-conditioned-on-place.patch`). Measured: the
    installed hook passes the `--fixup` with the reason; on the packages
    history, the released target, the topic branch and the stacked branch
    alike, lint and bump refuse at 3; the autosquashed packages history lints
    0 with both lines at minor; and the reworded commit lints 0 and bumps
    patch. `skip` so keeps one meaning in the presets — merge commits, whose
    diff is never read — and the binary still knows no git spelling: a
    repository that skips the pair in its own file keeps that decision. The
    price is paid in the safe case too: a `fixup!` whose target sits in the
    same unreleased fold is refused until autosquash runs (the one-line
    `fixup!` in `TestLintRange` is such a commit), in glyph itself and in
    every file generated from here on, a fixture a harness generates at the
    ref it tests included (glyph-test's `e2e-v2` arm (c) commits a `fixup!`
    into an `init --gemoji` fixture built at its `glyph-ref`, `main` by
    default, and asserted it skipped, so it moves when this reaches `main`,
    not at a release) — no file already written moves (below) — and the
    hook's pass and the reason's first escape keep that price to one rebase.
    Rejected: claiming the pair only under `[[packages]]`, as a commented
    claim for the adopter to uncomment — the single-line leak is real without
    any package, and glyph's own packages fixtures (`packagesConfig`) declare
    `[[packages]]` by appending to a generated preset, never by uncommenting
    its block; placing a skipped commit by its files, as an `exclude_authors`
    commit is placed — placement moves no version (the excluded-author
    analog: a bot's `:arrow_up:^` touching `b/` leaves `b` at none, measured
    the same day); and handing a `fixup!` its target's sigil — a second
    implementation of autosquash's target search inside the fold, where
    attribution is per commit.
  - **The claim reads the body, not the subject** (mutation row
    `presets-claim-any-amend-body.patch`). The body is what autosquash lands,
    so the presets claim an `amend!` only when its body opens the way their
    first pattern's subject does, and leave every other body unmatched —
    refused at the hook too. The case that decided it:
    `--fixup=amend:<a fixup! commit>` prepares that fixup!'s own line as the
    body, and autosquash lands it as the original commit's whole message,
    `fixup! …` — which the skip of the day dropped (lint green, bump none,
    the original sigil gone; measured on git 2.54), and which the claim
    above now refuses at every gate with no autosquash left to fold it (a
    second `rebase --autosquash` leaves it as it is; measured 2026-09-29):
    only a reword gets it out. Claimed on the subject alone, the hook passed
    that commit with a warning whose remedy produced it.
    One chain no single message can show: after a `squash!` of the same
    target, git's sequencer treats the `amend!` as a squash and APPENDS its
    body, so the target's old first line lands unless the rebase's combined
    message is edited — lint green, the old sigil folded (measured on git
    2.54). glyph folds what landed, so no gate can catch it afterwards; the
    reason names the case at the hook, the one moment the author can act.
  - **Nothing rewrites a `glyph.toml` already written**, so each keeps what
    its release generated: v4.1.1's skips `amend!` (the silent fold above,
    until its repository replaces the skip with the preset's block), every
    other release's before the claim leaves it unmatched, refused at the
    hook, and every release's before t-mfny skips `fixup!`/`squash!` (both
    leaks above, until its repository replaces that skip with the preset's
    claim).
- **`exclude_authors`** removes a commit from lint and the fold before its
  message is ever matched — the key exists for bots, whose messages are
  exactly the ones the patterns do not describe. Whether such a commit
  appears in the notes is `[[note.sections]]`'s decision alone (§3), and
  under `[[packages]]` which lines' notes is its files' (§4.1). An
  **empty entry is refused at load**: the authoring path has no commit yet, so
  `lint --message` and `lint --stdin` judge under an empty author, and
  `exclude_authors = ['']` excluded every message the commit-msg hook was ever
  handed — measured, a message matching no pattern exited 0 with that one
  entry present and 3 with it removed. A stray comma turning the gate off
  silently is the shape this file refuses everywhere else. The author it is
  compared against is read from git **whole** (t-esm5): `git log`'s fields
  were framed by the unit separator, a byte git keeps inside a name, so a
  contributor named `dependabot[bot]<US>x` shifted every field by one, read
  as `dependabot[bot]` and was excluded at exit 0 — measured, and a check of
  the parents field alone still passed the same name with an empty email or
  a 40-hex one. The fields are framed by NUL, the byte no field can hold and
  the one the record framing already rested on, and a record whose SHA and
  parents are not full object names fails the read at 4 rather than reach a
  gate shifted (mutation rows
  `gitsource-log-fields-framed-by-a-byte-a-name-holds.patch`,
  `gitsource-log-believes-a-misframed-record.patch`). Full object names, not
  SHA-1's 40 digits: glyph reads SHA-256 repositories and did before the check
  (`gitsource-log-refuses-sha256-object-names.patch`). Because the check
  refuses any byte git writes outside a record, `git log` runs with
  `--no-show-signature`: under `log.showSignature` git prints each signature's
  verdict ahead of the commit's record even under `--format`, and every history
  read of a developer who signs failed at 4 — which the installed pre-push hook
  lets through (measured; `gitsource-log-shows-signatures.patch`). Two more
  display settings reached a parser the same way and are held out the same way
  (t-esm5, measured 2026-10-05 on git 2.54). `i18n.logOutputEncoding`
  re-encodes what `git log` prints and `diff-tree`'s `%P` header with it:
  under UTF-16 every history read failed at 4, and under ISO-8859-1 `notes`
  wrote a subject's Latin-1 bytes into the release body, so both reads name
  `--encoding=UTF-8`. And `column.ui=always` columns `git tag --list` even
  into a pipe: a row of tags read as one name that parses as no version, so
  the step base fell to v0.0.0 — `bump --range` printed v0.0.1 with nothing
  said where the answer is v0.1.1, and a bare `--since-tag` warned that HEAD's
  history holds no version tag and walked the whole of it — and the listing
  runs with `--no-column` (mutation rows
  `gitsource-log-reads-in-the-configured-output-encoding.patch`,
  `gitsource-diff-tree-header-reads-in-the-configured-output-encoding.patch`,
  `gitsource-tag-listing-follows-column-config.patch`).
- **Lint has no taste** (mutation row `config-lint-grows-a-taste.patch`): a
  message either matches a pattern and yields a sigil, or it violates. Which
  combinations are wise (`:memo:!`) is the author's call — glyph parses and
  computes, it does not opine. The retired v1 rule vocabulary (uppercase
  subject, trailing period, footer discipline, merge-candidate rules) is
  gone with the grammar that defined it.
- **A refusal quotes the form** (mutation row
  `lint-violation-forgets-the-form.patch`, t-s1q0): the no-pattern-matches
  violation carries the first line of `commit.template` verbatim — `write it
  as <:code:>[(scope)]<semver_sigil> <subject> with a semver_sigil of = ~ ^ !
  or %` — so the refused author is told the shape instead of re-deriving it
  from the winning regex on every failure (measured: the envelope said only
  "see glyph.toml", while the window warning then sitting next to it already
  spelled the line). The template stays unparsed: glyph quotes it, it does not interpret a
  placeholder, and a file with no `[commit]` block gets the bare pointer.
- **A captured group is the author's text, and it renders as prose — the
  scope included** (ruled 2026-09-29, t-0j9r). `notes.renderLine` hands every
  placeholder but `$author` to `markdown.Line.Prose`: the value is flattened
  as it arrives, then the prose escape and the mention fence each run once
  over the assembled line (the order is argued at `markdown.Line`). A `<` a
  pattern captured is escaped exactly as a `<` in a subject is, and an at-sign
  is fenced exactly as one there is. The incident behind the ruling is v1's:
  a scope slot that took anything but a parenthesis let `fix(<i title="x">): …`
  lint clean and put a live tag into a release body (#61). v2 imposes no shape
  on a group, so those bytes reach the renderer wherever a repository's own
  pattern captures them — the presets cannot (their scope group is
  `[a-z0-9-]+`, and `:bug:(<i title="x">)~ fix it` is refused at 3 under the
  gemoji preset). Measured 2026-09-29 through `glyph notes` on a throwaway
  repository capturing `\((?P<scope>[^)]+)\)` under
  `line = '- $[**$scope:** ]$subject'`, each line then rendered by GitHub
  (`POST /markdown`, mode gfm):

  ```
  - **\<i title="x">:** fix it      the tag renders as its own text
  - **`@x`:** fix it                the handle is code, not a mention
  - **readme`:** credit ``@alice`` and ``@bob`` for the fix
                                    the fence is sized over the line: no mention
  - **_x_:** fix it                 emphasis works: an italic x
  - **a*b_c:** fix it               opens no emphasis: renders as typed
  - **grid-1f-4:** fix it           byte-identical
  ```

  Unescaped, the first line's tag is live and leaks past the list item's
  `</li>`. (Test `TestRenderLineEscapesTheScopeAsProse`; mutation row
  `notes-placeholder-values-render-raw.patch` — with every value passed raw,
  nothing else in `go test ./...` fails.)
  - **`-` and `@` stay unescaped, deliberately**, and the prose rules test
    neither. `-` is a list, setext or thematic-break marker only at the start
    of a line, where no shipped template puts a value (each opens with a
    literal `- `), so escaping it would put a backslash into the raw source of
    every kebab-case scope for nothing (`grid\-1f\-4` renders as
    `grid-1f-4`). `@` is the fence's: a backslash does not disarm a mention
    (`credit \@octocat` renders a live one), and one in front of the at-sign
    makes the fence write a separating space so the backslash cannot eat its
    opening backtick — the reader sees `\ ` before a code-font handle
    (measured on a `(\@x)` scope, the bytes escaping `@` would write).
  - **The plain-text route is deleted, not revived** (`Line.Text` and
    `escapeText`, t-0j9r). v1 rendered the scope through a flat escaper that
    backslashed every ASCII punctuation byte but those two, on the argument
    that a scope is data and no grammar applies to it. v2's renderer (#185)
    sent every value through `Prose`, and once v1's renderer left with #187
    nothing called the route again. Reviving it would need the renderer to
    tell data from prose by a group's NAME — the pattern file's to choose, and
    `scope` means something only to attribution (§4.1) — while the presets
    carry the scope inside `$subject`, the whole first line (§3), where it is
    prose regardless. And it would add fidelity, not safety: prose already
    disarms the incident's whole class, and the two policies part only where
    prose leaves an emphasis or code-span delimiter live, as it deliberately
    does for the author's markup — `_x_` renders italic and `~x~` struck; a
    lone `*` pairs with the template's own `**` (`**x*:** fix it` rendered an
    italic `x:` and a stray `*` with no bold, where the plain-text route's
    `x\*` kept the bold — measured 2026-10-04); and a backtick pairs with one
    in the subject (`` (a`b) `` before ``fix `it` now`` put the stretch
    between them in code font and broke the bold). None of that injects
    structure, points anywhere the author did not write, or steals the fence's
    delimiter (escape.go's theorem), so the price is rendering, paid only by a
    repository whose own pattern captures such bytes — accepted.
- Unknown keys, an unknown `schema`, an uncompilable pattern, a malformed
  `note.line` and a section that does not state exactly one axis are LOAD
  errors, never repairs (mutation row `config-unknown-schema-accepted.patch`):
  this file decides CI verdicts, and a silently-misread key is a
  silently-changed verdict.

Config resolution is one file per checkout: the `glyph.toml` at the top level
of the working tree the command runs in (ratified Q1 — a config change
reinterprets past commits, accepted). The commit-msg hook, CI and a
subdirectory shell all read the same file by construction, and `init` writes
that same file: it asks git for the top level as every other command does and
writes there from any subdirectory, falling back to the current directory
only where git names no top level (outside a checkout) — never because a
signal interrupted the question, which exits 130 with nothing written.
Measured before (t-f2cb, 2026-09-29 and 2026-10-04): from a subdirectory
`init` wrote `sub/glyph.toml` at exit 0, a file no command reads, and the
missing-config refusal's remedy — run `init` — sent the author back to the
same command; with `glyph.toml` already at the top level, an `init` from a
subdirectory wrote a second one there at exit 0
(`TestInitWritesTheTopLevelFromASubdirectory`,
`TestInitInterruptDuringTopLevelReadWritesNothing`; mutation rows
`init-writes-the-current-directory`,
`init-reads-a-toplevel-interrupt-as-outside-a-checkout`).

### 2.1 The text the rules judge — git's cleanup

A rule is only as good as the text it is applied to, and at the commit-msg hook
that text is **not** the message. git runs the hook BEFORE its own cleanup, so
the file still holds whatever the editor left: the template, the status block,
and under `-v` a scissors line with the entire diff below it. `cleanup.Apply`
reduces that file to the message git will record, and `--stdin` is its only
caller. A `--range` walk reads `git log %B`, which git has already cleaned —
into git's shape, not the hook's: git records a cleaned message with a closing
newline, and `cleanup.Apply` returns the text without one. Go's `$` without
`(?m)` matches only at the very end of the text, so a pattern ending in `$` —
the natural way to say the sigil form is the whole subject line — passed a
one-line subject at the hook and refused the same commit at `lint --range` and
`bump --range` (t-3p3k, measured); and GitHub's copy of a message never carries
the newline (measured 2026-09-29 on glyph#246: af7ee18's local `%B` ends in
one, its entry in the pull's commit listing does not). `gitsource` strips
exactly that one newline where it parses a record, so every reader of local
history — `lint --range`, the pre-push hook, `bump`, `notes`, `release` and
`preview`'s walk — judges the text the hook judged, and a verbatim message
keeps trailing blank lines of its own
(`TestEndAnchoredPatternGetsOneVerdictAtTheHookAndInTheRange` asserts a
matching and a non-matching message at both gates; mutation row
`gitsource-log-keeps-the-record-newline.patch`).

**The requirement is agreement, not tidiness.** The hook and CI must reach the
SAME verdict on one commit; a gap is glyph lying in one of two directions, and
the two are not equally bad. Blessing a message CI will reject costs a round
trip. Refusing one CI would accept costs the commit — the only way past the hook
is `--no-verify`, which turns the whole gate off.

**One gap is argued rather than accidental: a message an `unlandable`
pattern claims** (§2). The hook passes it and CI refuses it, deliberately,
in the direction this section calls the cheaper one — blessing costs a round
trip, refusing costs the commit, and the message is one git spelled, so a
refusal here could only be answered with `--no-verify`. What keeps the gap
honest is that the pass is loud: the hook prints the file's reason and says
the later gates refuse the commit (`config.LintAuthoring`; mutation row
`config-unlandable-refused-at-authoring.patch`). doctor's hook probe judges
its message through the same function, since it must answer exactly as the
fired hook does. A repository whose patterns claim nothing as unlandable has
no such gap.

**Which cleanup runs is a per-commit question, and the hook can answer it from
four signals.** git has five modes and picks between two of them by whether an
editor will run; assuming the editor's cleanup is what made the hook and CI
disagree, measured on git 2.54 in both directions (`-F` with a `#` line as the
subject: hook 0, CI 3; `-F` with an indented `  # why:` line above a footer:
hook 0, CI 3 — both measured under the v1 grammar, whose footer rule the second
case tripped; the disagreement belongs to git's cleanup, not to any grammar, so
a v2 pattern file inherits it unchanged). The signals a hook actually has:

- `commit.cleanup`, read with `git config --get`;
- `commit.verbose`, read through git's own parser — `git config
  --type=bool-or-int`, on above 0 — because `yes`, a bare key and `1k` are
  git's to spell, and `--type=bool` reads `-1` as on where `git commit` treats
  it as unset and does not cut (measured; mutation row
  `cleanup-reads-commit-verbose-minus-one-as-on.patch`). `git -c
  commit.verbose=true commit` reaches the hook as config, through
  `GIT_CONFIG_PARAMETERS` (measured);
- `GIT_EDITOR`, which git sets to `:` when no editor will run. Only that side is
  load-bearing — with `core.editor` or `$EDITOR` supplying the editor git leaves
  `GIT_EDITOR` **unset** in the hook, so unset must mean "an editor may run".
  Read the other way, those developers get the whitespace branch, where the
  template is never stripped and every commit is `malformed-subject`;
- **which file** the hook was handed. `git merge` (and `git pull`) hands it
  `MERGE_MSG`, and `git commit` — concluding a conflicted merge included —
  `COMMIT_EDITMSG` (measured). The installed hook redirects the file onto stdin
  (`<"$1"`), so its identity survives into `lint --stdin`: the same file as
  `git rev-parse --git-path MERGE_MSG`, linked worktrees included. A hook that
  pipes the message in instead gets `git commit`'s reading.

Resolution, then, is `commit.cleanup` × edited → `verbatim` / `whitespace` /
`strip` / `scissors`, cut at the scissors line when `commit.verbose` is on, when
an editor ran for `git commit`, and in scissors mode with an editor — the only
cut under `git merge`, which cleans with verbose 0 whatever `commit.verbose`
says (`builtin/merge.c`). git cuts under verbose — `-v` or `commit.verbose`,
editor or not — and in scissors mode only with an editor (`cleanup_message` and
`get_cleanup_mode`, sequencer.c). That formula held in every cell of cleanup
source × message source × verbose source × where a typed cut line sits: 396
cells in the D10 design run (304 recorded, 92 aborted as empty) and 880 in its
review (593 recorded, 287 aborted), measured on git 2.54 on 2026-09-29, and the
48 cells `TestHookCutMatchesGit` asks of real git on every run.

The rule first ratified here said the cut was NOT applied without an editor,
generalising a scissors-mode measurement (`-F` records the cut line and
everything under it, still true); under `commit.verbose` git cuts an `-m`, `-F`
or `--amend --no-edit` message in every mode, and the hook judged the cut line
and all below it (t-3p3k; mutation row `cleanup-ignores-commit-verbose.patch`).
Read that way, a merge was judged as a commit: a typed cut line opening a
merge's message cut everything, and the installed hook stopped a merge git
records, under `commit.verbose` with `-F` and under `--edit` in the default
mode alike (measured; `TestInstalledHookJudgesAGitMergeByMergesCleanup`,
mutation row `cleanup-reads-commit-verbose-under-git-merge.patch`).

The editor half is a guess, and argued: `-v` reaches no hook, and under it git
writes the cut line and the whole diff into the buffer itself (every such
buffer, measured), so a hook that cut only on what it can see — git's own
formula over the config — would judge that diff as the message of every `git
commit -v`: a `$`-anchored one-line subject refused at the hook though git
records exactly that one line (measured), and whatever pattern reads past the
first line — the presets' own `amend!` claim reads the body, and so would a
`(?s)` `warn`, `unlandable` or `skip` pattern — judged against code (mutation
row `cleanup-keeps-the-diff-git-commit-v-wrote.patch`). A cut line git did not
write is one the author typed, byte for byte. The grid is asked of real git as
TEXT, not verdict (`TestHookCutMatchesGit`): under a subject-anchored grammar a
message and its cut-short form get the same answer, and
`TestHookVerdictMatchesWhatGitRecords` stayed green under the old rule and
under git's formula alike (measured).

An unrecognised mode name warns and falls back. git itself refuses one for a
plain `git commit`, `cherry-pick -e` and a rebase reword before any hook runs
(`Invalid cleanup mode`, measured), but a `--cleanup=` override on the command
line gets past it and the hook runs (measured) — and this hook forwards only the
lint gate code and waves everything else through, so failing there would leave
exactly those commits unlinted.

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

**What stays wrong, and is not claimed fixed:** a flag on git's COMMAND LINE
reaches neither the config nor the environment (measured), so the hook judges a
per-commit override under the repository's settings:

- `git commit --cleanup=<mode>` is judged under `commit.cleanup`;
- `-v` with no editor cuts a typed cut line in git and not at the hook, and
  `--no-verbose` under `commit.verbose` cuts at the hook and not in git. Both
  move verdicts the expensive way. `--no-verbose` does under the presets: a
  message opening with a typed cut line, in a mode that strips comments, is
  recorded as the subject under it while the hook judges an empty message and
  refuses — with an editor in the default mode (`commit.cleanup` unset or
  `default`) or `strip`, and with an `-m`, `-F` or `--amend --no-edit` message
  under `commit.cleanup=strip` (measured with the installed hook,
  `commit.verbose` true or 2). `-v` does under a grammar that reads past the
  subject: with the typed line below the subject, git records the subject
  alone, the hook judges the lines under it too, and a `$`-anchored pattern
  refuses at the hook what the range accepts (measured with `-m` and `-F`;
  under the presets' subject-anchored grammar both answers agree). Neither
  refusal forces `--no-verify`: the author typed the line and can delete it;
- under an editor with neither `-v` nor `commit.verbose`, in any mode but
  scissors, the hook cuts at a line the author typed and git keeps what is under
  it. Nothing the hook reads besides the message separates this from `git
  commit -v`, where git does cut: `keep`, a typed cut line and a subject record
  both lines without `-v` and `keep` alone with it, with the environment and
  both config reads identical and the file differing only below the typed line,
  by the block git writes there under `-v` (measured). Under a subject-anchored
  grammar it moves one verdict — the typed line opening the message in a mode
  that strips comments (default with an editor, or `strip`): git records the
  subject under it, the hook judges an empty message and refuses, and again the
  author can delete the line;
- an exported `GIT_EDITOR=:` (`GIT_EDITOR=: git commit --amend`) is git's
  no-editor marker to the hook while git runs its editor cleanup, so the hook
  judges the template git strips (measured).

Three ways to close the editor and `--no-verbose` cells were measured and not
taken. Judging the uncut text whenever the cut leaves nothing closes every cell
where the typed cut line opens the message — since under `-v` git aborts an
empty message anyway — but each repair needs an author to type git's 53-byte
cut line at column 0 as the message's first line, while its cost lands on every
`git commit -v` saved empty to abort: the refusal would quote the diff (`diff
--git a/… b/…`) as the subject (measured). Inferring `-v` from the block git
writes under its cut line is a content guess at a state the hook cannot see, and
it fails three ways: git translates the explanation lines (measured under
`LANG=de_DE.UTF-8`: `# Ändern oder entfernen Sie nicht die obige Zeile.`), `git
commit -v --allow-empty` writes the cut line and no diff (measured), and the
author may delete the block. And the flags themselves are visible only in the
parent process's argv (measured with `ps`: `-v`, an alias's expansion, the
abbreviation `--verb`), where reading them means re-implementing git's
parse-options — abbreviations, bundled short options, `--no-` forms — an
approximation of git of exactly the kind this section refuses. Same for
`core.commentChar`: glyph assumes `#`.

### The gemoji dictionary (`glyph emoji`, t-0c0m)

v2 dissolved the embedded gitmoji table because it made the emoji decide the
version. What went with it was the one thing the table was also doing:
telling the writer of a subject **which** code to write. Measured 2026-09-08
over 46 fleet clones, 52 distinct codes head the fleet's subjects, and the
tail is the symptom — `:green_heart:` beside `:construction_worker:`,
`:bookmark:` beside `:rocket:` beside `:package:` for the same act, `:art:`
and `:recycle:` and `:broom:` and `:bulb:` for the same tidy. Ratified
(user, 2026-09-08):

- **A dictionary, not a grammar.** `glyph emoji` prints an ordered JSON array
  — `code`, `emoji`, `name`, `description`, `absorbs` — and nothing in the
  engine reads it: lint accepts whatever the repository's pattern accepts,
  listed or not, and the sigil alone decides the version. The dictionary
  carries **no semver field** for that reason; putting one back is the v1
  mistake in a new file. The binary is the one home (each repository defines
  nothing), and the shipped bytes are served byte for byte, so
  `internal/emoji/table.json` on GitHub and the command's stdout are the same
  bytes for a session with no binary at hand.
- **One meaning, one emoji.** Every entry's `name` is the kind of change, one
  word, unique across the table (`TestOneMeaningOneEmoji`). Where gitmoji
  offers neighbours — `:art:` / `:recycle:` / `:lipstick:`, `:fire:` /
  `:coffin:` / `:wastebasket:` — the description draws the border and the
  losing codes are listed under `absorbs`, so a reader arriving with gitmoji
  habits can look their old code up. Severity is not a kind (`:ambulance:`,
  `:adhesive_bandage:` fold into `:bug:`); direction is not a kind
  (`:arrow_down:`, `:pushpin:` fold into `:arrow_up:`); scale is not a kind
  (`:building_construction:` folds into `:recycle:`).
- **Ordered, first fit wins** — the same reading rule as `[[patterns]]`, so
  the specific kinds (`:page_facing_up:` license, `:see_no_evil:` ignore)
  sit above the broad ones (`:bug:`, `:sparkles:`) and location beats kind
  for the file-shaped entries: a fix inside a workflow is `ci`, a fix inside
  check.sh is `tooling`, a fix to a test is `tests`.
- **Deliberately absent**, with the reason each time, so nobody re-adds them
  as an oversight: `:boom:` — breaking is the sigil's job, and a `:boom:`
  prefix repeats `!` while hiding the kind (a removal that breaks is
  `:fire:!`, a feature that breaks is `:sparkles:!`; README's example moved
  accordingly); `:globe_with_meridians:` — the fleet is English only with
  no stored translations (doc-consistency-policy); `:twisted_rightwards_arrows:`
  — the presets skip merge commits, so nothing is ever written with it;
  `:rotating_light:` as a kind of its own — a warning fix is polish. The
  rest of gitmoji's 75 that the fleet never used (`:poop:`, `:beers:`,
  `:egg:` …) are neither entries nor absorbed: absence is the whole
  statement.
- **Renderability has an oracle**, because the one failure that matters —
  a listed code GitHub draws as literal text — is invisible to every local
  check. `internal/emoji/testdata/gemoji.tsv` is `GET /emojis` as GitHub
  answered it on the date in its header (1936 shortcodes), and
  `TestEveryCodeRendersOnGitHub` holds every entry and every absorbed code
  to it, emoji field included: the `emoji` value is exactly the code points
  the API maps the key to, with no variation selector added, because the API
  is the oracle and a font is not. Refresh the snapshot with the command in
  its header; never hand-edit it. `doctor` was considered for this and
  rejected — doctor diagnoses a repository, and the dictionary is a property
  of the binary.

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
  a fix and a break are the two a reader most needs to tell apart. The
  preview's headline says so rather than drawing it: a `!` pull over a
  pending `^` reads *raises **major** — the next release stays **v0.4.0***,
  never *escalates **v0.4.0 → v0.4.0*** (t-d0d9, measured 2026-09-26 on
  glyph-monorepo-test #31; mutation row
  `preview-headline-escalates-to-the-same-version.patch`). The level still
  rises in the sentence because classification is version-blind — the
  break stays visible — and only the arrow, which the arithmetic would have
  drawn from a version to itself, is withheld.
- **A version field is at most 2^31−1** (`bump.MaxField`; t-f2cb, mutation
  row `semver-field-past-the-cap-accepted`). `Next` adds one to a field, and
  from the `int` ceiling it wrapped — measured 2026-09-29: `bump --current
  v9223372036854775807.0.0` over a `!` printed `v-9223372036854775808.0.0` at
  exit 0, and a tag of that version did the same from the walk base.
  `ParseVersion` refuses a larger field, so a `--current` past the cap is
  usage (exit 2), and a tag past it is no version on its line: it leaves the
  walk base's, the published floor's and the managed drafts' candidates like
  any tag that is not version-shaped. The cap sits at parse, not in `Next`: a
  step from a field at the cap lands one past it, which `int` holds on every
  target glyph builds for, so `Next` stays total — and a version past the cap
  that glyph writes is one it never reads back once tagged, so the line's next
  step answers from below it: backwards after a stepped field (measured
  2026-10-04: `--current v2147483647.0.0` over a `!` answers
  `v2147483648.0.0`, and with that tagged a later `~` answers
  `v2147483647.0.1`) and the same tag again on a major version subdirectory
  past the cap (`pkg/v2147483648` re-proposes `pkg/v2147483648.0.0` after it
  is tagged). Rejected: detecting the overflow in `Next`, which gives every
  step site an error arm for a state no stepping reaches.

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
regex misses it — the silent hole v2 exists to close. A commit an
`unlandable` pattern claims is refused the same way (§2), with that
pattern's reason as the refusal's detail. The refusal is the lint
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
order silently decides which section owns a commit). An unmatched commit —
one an `unlandable` pattern claims included — has no level, so it can only
surface through an author section, rendered through
the same `note.line` template with `$subject` bound to its raw first line —
the ratified bot fallback. `skip` is total: no section at all, which is what
separates it from `exclude_authors`.

**The presets' `subject` group is the whole first line** (ratified 2026-09-07,
fleet-wide the same day): `:sparkles:(cli)^ add the thing` renders as exactly
that in the release body — GitHub draws the gemoji — rather than as the bare
`add the thing` the group used to stop at. The two shipped presets and every
fleet `glyph.toml` moved together, so a `glyph init` of today writes what the
fleet already carries. The group's NAME is what makes the change one line:
`note.line` cites `$subject`, the `Revert "…"` pattern and the unmatched-commit
fallback bind `subject` alone, and nothing else reads the group — bump reads
`semver_sigil`, lint reads the match, and the verdict's `subject` field is git's
first line, not the group. Rejected alternative: a second `$raw` group cited
from the template, which would have emptied every revert and bot line (a
placeholder neither built-in nor a group of the winning pattern renders empty).
Measured before it went out: glyph-test #80 and the sill canary #211 both
rewrote their draft to the whole-subject form, and `POST /markdown` renders the
shortcode as the emoji. (Mutation row `presets-subject-stops-at-the-sigil.patch`.)

**`note.line` and the optional span.** The template substitutes `$name`
placeholders — the winning pattern's named groups, plus the built-ins `$pr` /
`$author` / `$hash` / `$coauthors`, which outrank a group of the same name,
plus any name `[[note.trailers]]` declares — and literal
text passes through as the author's own Markdown while every substituted value
renders as prose, the scope included (§2), with the mention fence run
over the assembled line. The fence has exactly one ratified exemption
(2026-08-17): the built-in `$author`, so the preset's `@$author` renders as a
live mention — crediting the contributor is intended behaviour, and every
peer tool (git-cliff, GitHub's own generated notes, release-drafter,
changesets) pages the author the same way. The exemption is gated by
**identity**, and by shape second (t-39fy, 2026-09-14, replacing the
shape-only gate): what goes live is the contributor's **login** — the
`author.login` GitHub itself attaches to a commit the API described (the
squash arm, `--pr`), else the login a GitHub noreply author address carries
(`<id>+<login>@users.noreply.github.com`, which every fleet commit carries
and the fallback arm and `--range` can read from git alone), else nothing —
and only when that login is one GitHub-handle-shaped token. The name is
never promoted to stand in for a missing login, because a name is not a
login and **no shape tells them apart**: measured 2026-09-13 on golang/tools,
`%an` is `Saleh` for github.com/larrasket, and the shape gate rendered
`@Saleh` — one handle-shaped token, and a stranger paged the moment the
release is published. A credit with no login renders the name as plain
prose with the template's at-sign dropped (`Robert Pająk`, not
`` `@Robert `` `Pająk`, which fencing the would-be mention produced on
opentelemetry-go): a credit that pages nobody is not written as a mention.
`dependabot[bot]` fails the shape gate on its own login and renders the same
way. Group-derived values (`$subject` above all) keep the fence
unconditionally: who a subject mentions is not the author credit.

**The trailer block, and why the exemption does not extend to it**
(2026-09-16). Two facts worth a release line live below the subject — who
co-authored the commit, and why it was made — and both are git trailers, so
glyph parses the block rather than grepping. `$coauthors` is a built-in
because `Co-authored-by` is a git-wide token with a structured value glyph
knows how to read; `[[note.trailers]]` names the words a repository decided
on itself (`Why`, `Ref`, `Escape`), whose values glyph cannot interpret.
**Config names the token, glyph alone names the identity.** So the `$author`
exemption stays the only one: a co-author address is self-asserted text that
establishes no login glyph can verify, and a declared trailer is prose, so
both keep the fence and neither can ever page a stranger — whether glyph
mentions somebody is not a key a repository sets. Measured 2026-09-16 across
glyph, glyph-test and glyph-monorepo-test: every `Co-authored-by` address in
the fleet either sits off GitHub's noreply host (`noreply@anthropic.com`) or
carries a `[bot]` login the handle gate rejects, so no credit could resolve
to a live mention even if the exemption were extended.

The parser is held to `git interpret-trailers --parse` by a differential
asserting a **subset**: glyph may report fewer trailers than git, never more,
because over-reporting is what names a person on a public page as an author
of code the commit never claimed. Four shapes answer nothing, each silently
(measured, git 2.54.0): a trailer in its own paragraph above the footer, a
line below a `---`, a block holding one prose line, and a block that is the
message's only paragraph. The third is the sharp one — an unindented wrapped
line, or the fleet's own `Generated with …` footer placed inside the block,
voids the whole block and takes the co-author credit with it. (Mutation row
`notes-coauthor-is-grepped-not-parsed.patch`.) Rejected alternative: carrying
the same two values in a `(?s)` `[[patterns]]` group. It truncates a wrapped
value, lets trailer order decide silently whether the value binds at all,
credits a trailer-shaped line sitting in a body paragraph, and would have to
be hand-copied into every repository's config and re-derived per pattern
shape — a regex over the raw message is not a trailer parser. Rejected
alternatives: rendering the raw first line (machine notation in prose, and
Breaking Changes already says what `!` says); dropping the fence entirely
(subjects carry arbitrary text); asking `GET /commits/{sha}` for every landed
commit's login (one request per commit on the free arm, for an identity the
noreply address already carries on every commit GitHub's own UI or a
privacy-on account writes — a personal address is then credited by name,
which is honest); fencing the name with its at-sign (`` `@Saleh` `` reads as a
broken mention and still shows a stranger's handle). (Mutation rows
`line-returns-its-bytes-unfenced.patch`, `author-credit-goes-live-by-shape`,
`noreply-address-carries-no-identity`, `unpaged-credit-keeps-its-at-sign`;
the gate lives in `markdown.Line.Mention`, the identity in `cli.identity`.) A **`$[ … ]` span renders only when EVERY placeholder
inside it resolves non-empty** (mutation row
`notes-optional-span-always-renders.patch`), taking its own punctuation with
it when one does not. Without it, punctuation written around a placeholder
rendered around nothing: `$pr` is empty for every commit the `--range` walk
sees (that walk resolves no pulls at all) and for every direct push under
`--since-tag`, so both shipped presets emitted
`- add the demo feature () @akira-toriyama` — measured live. A malformed span
is a CONFIG error, refused at load with the file's path (exit 2 — §5: the
config is the yardstick on a verdict command, never the judged subject) rather
than by the release that would have rendered it: unterminated, nested, or holding
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

**A placeholder no commit binds does not load** (mutation rows
`config-line-placeholder-unbound`, `config-note-line-cites-an-unlandable-group`,
`config-note-line-cites-a-skip-group`,
`config-note-line-refuses-the-fallback-subject`,
`config-note-line-cites-the-fallback-subject-alone`): a name that is not a
built-in, not a declared trailer and not a group some rendered commit's
winning pattern captures resolves empty on every line, and inside a span it
takes the span with it — `$[ ($pull)]` for `$pr` rendered every line without
its citation, at exit 0. The legal set is the union over the patterns whose
groups a commit binds (`config.Pattern.bindsGroups`, the predicate §4.1's
scope-word check reads): a `skip` pattern's commit is in no section and an
`unlandable` one renders through the fallback, so neither counts. **The
fallback's `$subject` completes a template; it never carries one.** The
fallback binds `$subject` for every commit no pattern claims, whatever the
patterns name their groups (`config.FallbackGroup`, the name the renderer
keys the fallback off), and for no other commit. So where no pattern binds
`subject`, `$subject` is legal beside a group a pattern binds —
`- $title$subject`, each line filling the one its commit binds
(`TestGroupSigilsFallbackBindsSubjectUnderAnyGrammar`) — and refused when
it is the template's only name beyond the built-ins and trailers, the
refusal naming the names the file can bind. Measured (t-f2cb): before it,
a `$branch` only a skip pattern captured loaded and rendered empty at exit 0,
and a file whose subject group is `title` was refused `$subject` outright
while the fallback bound it for every bot line — so its `- $title @$author`
rendered each such line as `-  dependabot\[bot]`, the text gone, with no
spelling that kept it (2026-09-29). Making `$subject` legal unconditionally
fixed that and loosened the loader (2026-10-04): the gemoji preset with its
group renamed `subject` → `title` and `note.line` left as written, which
adfc5e1 refused at exit 2, loaded and printed every matched commit's line
with its text gone (`-  akira`) at exit 0, and with sections on the semver
axis only — where the fallback never renders — `$subject` filled no line at
all. The rule reads the template and the patterns, not `note.sections`:
`- $title$subject` under semver sections only loads, its `$subject` empty
beside the `$title` that carries each line's text. **A `[[note.trailers]]`
name is held to the same set from the other side.** A trailer outranks a
group at render, so it may not take a name a commit binds — a group of a
pattern whose groups a commit binds, or `subject` whatever the patterns call
their groups — and a group only a `skip` or `unlandable` pattern captures,
which binds nothing, leaves its name free (mutation rows
`trailers-may-shadow-the-fallback-subject`, `trailers-count-a-skip-group`).
Measured 2026-10-04, identically before t-f2cb: a title-grammar file with a
trailer named `subject` loaded and rendered each bot line as `- `, its text
replaced by an absent trailer, at exit 0, and a trailer named after a
skip-only `branch` group was refused as "$branch would mean two things".
Rejected: refusing a template that cites a name the fallback cannot bind —
`- [$scope] $subject`, the idiom above, renders a bot line with an empty
scope and its text (measured: `- [] Bump foo from 1 to 2`), the per-winner
rendering every pattern already gets (the `Revert` pattern captures no scope
either), so that refusal would ban citing any group but `$subject`; it is
rejected on that ground alone, since no fleet file would have moved (42
measured, every `note.line` citing only built-ins and `$subject`). Also
rejected: refusing a template that does not cite `$subject` — whether a bot
line shows its text is the template author's call, not a name nothing binds
— and deriving the fallback's name from the patterns, which is undefined
when two of them name the text differently. The `$subject` rule above is
neither refusal: `- $title$subject` cites a name the fallback cannot bind
and loads, and a template citing no `$subject` is never asked about it.

**`draft_on_none`** (a `glyph.toml` key; the mechanism is `internal/draftplan`): with it on, a none
verdict maintains an `Unreleased` placeholder draft instead of deleting the
rolling draft, and the next real verdict retags that same draft to the real
version through the ordinary keep-selection path (mutation row
`draftplan-none-forgets-the-placeholder.patch`). The placeholder tag is
deliberately not house-shaped — publishing it by hand cannot burn a version
tag or wedge the published floor — and it is claimed as glyph-managed even
with the flag off, so flipping the flag off converges the artifact away.

## 4. Squash-safe mechanism — release-time re-read (stateless)

On a release run, walk `<walk base>..HEAD` over `main`'s **merge points**;
for each, resolve its PR via `GET /repos/{o}/{r}/commits/{sha}/pulls` and fetch
that PR's individual commits via `GET /pulls/{N}/commits`; classify and
max-fold. **Nothing is persisted** — recompute-from-git each run, idempotent and
self-healing. Every verdict command runs **inside a git checkout** of the
repository being released — the walk base, the version base, and the draft's
target sha all come from local git; tags are never fetched over the API.

**The walk base is a release HEAD contains** (t-n5tw, ratified 2026-09-29).
Every resolution that reads a base out of the tags takes the highest parseable
tag on the line among the tags **HEAD's history holds** (`git tag --merged
HEAD`), never among every tag the clone carries. That covers a bare
`--since-tag`, `below:`, and the step base a source without one falls back to
(`bump --range` and `--pr`, `preview`'s current). A tag outside that history is
another history's release — or, on an unmerged topic branch, the base branch's
release, which the branch has yet to follow — and baselining on it was silent
both ways it happens.

One cut later on a descendant — a checkout at an older commit — contains
everything HEAD holds, so the walk folds none. glyph-monorepo-test's
frozen-coordinate tier collapsed to `none` on every line at v3.3.0 (measured
there 2026-09-11, line tags planted on a descendant of the coordinate), and that
harness has pruned `git tag --no-merged "$FROZEN"` by hand ever since (#15
there) — this rule, written in shell because the binary lacked it. A release
run at an older commit (a re-run, which GitHub documents as running at the
original event's sha) whose own range folds above none folded none instead and
**deleted the rolling draft**, hand region included (measured 2026-09-29 at
adfc5e1 on a stand-in API: v0.2.0 published on a descendant, the v0.3.0 draft
standing, one `DELETE`); over what HEAD holds it steps to the version v0.2.0
already published and is refused at the floor with nothing written
(`TestReleaseAtAnOlderCommitRefusesAtTheFloor`; the fold at an older
coordinate is `TestSinceTagFrozenCoordinateSeesItsOwnTags` and, per line,
`TestPackagesFrozenCoordinateSeesItsOwnTags`).

One on a maintenance or side branch re-admits what that branch's releases
shipped and steps from a number this line never released. On `release/v1`,
forked at v1.9.0 with v1.9.1 cut there after main's v2.0.0, `bump --since-tag`
answered v2.0.1 out of the released backport; a backport pull's preview said
the same, and `release --dry-run` drafted it. A side branch's v9.0.0, forked
before v1.1.0, stepped main to v9.1.0 over a commit v1.1.0 had shipped (both
measured the same day). Over what HEAD holds the maintenance line answers
v1.9.2 and main answers v1.1.1 (`TestSinceTagMaintenanceBranchStepsItsOwnLine`,
`TestSinceTagBaseIsAReleaseHEADContains`). The shape is not confined to
fixtures: google-cloud-go carries 32 tags its main does not hold — maintenance
releases such as `storage/v1.61.5` on `release-storage-1.61.5` — and on that
branch the old rule resolves main's `storage/v1.68.0` (measured 2026-09-29).
This is git's answer, not a heuristic: the ancestry fact the footprint mapping
below takes as *landed*. It is also the field's: `git describe`, GoReleaser's
previous tag (`describe tags/<tag>^`), semantic-release (`git tag --merged
<branch>`, highest version, under a `before` bound that is `below:`'s), and
release-please (a release whose sha the branch's history lacks is skipped).

Refusing instead — take the highest tag anywhere, exit 4 when HEAD does not
contain it — was the other candidate, and it loses where the answer is known.
On a maintenance branch that refusal is permanent: every bare walk, every `bump
--range` step base and every backport pull's preview refuse there (measured
2026-09-29 on a prototype of it against a stand-in API). That is a structural
refusal, the shape #66 declined an exit for, and on the preview it is the
refusal this section declines for an advisory surface, since it takes the
whole verdict comment down. It also kills the first tag-time notes of a new
major whenever the previous major took a maintenance release above the fork:
`notes --since-tag=below:v2.0.0` exited 4 where both other resolutions render
the same body (the same prototype) — goreleaser's step dying behind a tag that
already exists, t-s5n4, the failure `below:` exists to end.

The published floor reads the **opposite** set on purpose: the releases
listing, every published release on the line whether HEAD holds it or not. It
answers a namespace question — what can still be published — and a tag name
is taken wherever its commit sits (measured 2026-09-29: a published v0.3.0 the
checkout lacks refuses v0.2.1 at 4). The two meet where they must. A published
release outside HEAD's history at or above the next version is refused at the
floor: the side branch's published v9.0.0 refuses main's v1.1.1, where the old
rule drafted v9.1.0 (measured 2026-09-29) — and a hotfix published off the
default branch is refused the same way, where the refusal is a price, not a
catch (below). That is also why `release` never drafts a maintenance version:
it writes only from the default branch (below), and even its dry run on
`release/v1` is refused at the floor (v1.9.2 under a published v2.0.0,
measured 2026-09-29). And a run at an older commit whose range folds above
none is refused there whenever the release it runs behind was published from
glyph's own verdict: the max-fold is monotone, and that release's range held
this one.

The rule has three prices, each measured 2026-09-29 on a stand-in API:

- **A topic branch answers as of its fork point.** Forked between v1.0.0 and
  v1.1.0, `bump --since-tag` answered v1.0.0 → v1.1.0 over a commit v1.1.0
  shipped, where the old rule answered v1.1.1; forked at v1.0.0, `bump --range`
  stepped v1.0.0 → v1.0.1 where the old rule stepped v1.1.0 → v1.1.1. No fleet
  verdict runs there: `pr-verdict.yml` checks out the pull's base, sill's
  `api-guard` runs `bump --range` on the pull's merge ref and reads `.level`
  alone, and `fleet-preflight` walks `origin/HEAD` — and the authority rule
  below already calls a walk from a non-default ref silently wrong. Ask for a
  topic branch's verdict on its merge ref or on its base.
- **A hotfix published off the default branch refuses main's patches at the
  floor.** With v1.2.1 cut and published on a branch off v1.2.0, main's
  history lacks the tagged commit whether main never takes the hotfix's change
  or takes it by squash — to this rule the two are one input. A patch on main
  then computes v1.2.1 from v1.2.0 and is refused at the floor (exit 4), on
  every patch-level push until a minor lands, where the old rule drafted
  v1.2.2. The escape is to land the tagged commit itself with a merge commit:
  main then holds v1.2.1, and the next patch drafts v1.2.2. That merge brings
  the hotfix's change with it; where main must not take it, `git merge -s
  ours` records the tagged commit and keeps main's tree, with the same v1.2.2
  (measured 2026-10-04 with `release --dry-run`: on both shapes two patches
  were refused and a minor then drafted v1.3.0; after either merge the next
  patch drafted v1.2.2).
- **A stale writer is narrowed, not closed.** A release run behind its ref's
  tip still writes from its older range, under either rule: at a docs-only `=`
  commit behind a published v0.2.0 the range folds none, and the standing
  v0.3.0 draft is deleted, hand region included; with no release between the
  run and the tip, the v0.2.0 draft is retagged down to v0.1.1 at the older
  sha. This rule closes only the case above — a range folding above none,
  behind a release published from glyph's verdict. Closing the rest needs a
  guard on the run's sha against its ref's tip, and no fleet incident asks for
  one — recorded here, on the precedent of the laptop hole the authority rule
  names.

The hole is named rather than closed. A later tag cut **by hand** below
glyph's verdict escapes the monotone argument. And a tag outside HEAD's history
that was never published — a bare `git tag` — at exactly the next version is
invisible to the floor: the draft takes a name the tag already holds, and a
publish binds the release to that tag's commit. This rule opens that collision
where the old one stepped over the tag (measured 2026-09-29: an unmerged bare
v1.2.1 hotfix tag, main drafting v1.2.1 where it drafted v1.2.2). Measured
2026-09-29 on clones of 22 tagged fleet repositories: none holds a tag outside
its default branch, and glyph-monorepo-test's 24 line tags are all on main, so
closing it would guard a class the fleet does not have.

A **shallow** checkout cannot say what its history holds: `--merged` stops at
the depth, so a `--depth 1` clone with its tags fetched held none, and v2.0.0 →
v2.1.0 read as v0.0.0 → v0.1.0 (measured 2026-09-29). A shallow checkout therefore reads
every tag, the set this resolver read before, and the walk records it as unread
(below), which `release` refuses. `actions/checkout` fetches no tags at a
non-zero depth (glyph-test's `livefire.yml` records it), and every fleet release
and preview step checks out with `fetch-depth: 0`. An unborn HEAD — a
repository before its first commit — holds no tags and answers empty rather
than failing, as the unfiltered listing did
(`TestMergedTagsOnAnUnbornHEADIsEmpty`). An explicit `--since-tag=TAG` is
untouched: naming a tag names the release being redone, and it is the escape
on any branch. The rule's cost is two git calls per resolution — the shallow
probe and `git tag --merged`, the dear one — once per declared line on a
packages walk: `--merged` took 0.14 s over google-cloud-go's 7,935 tags without
a commit-graph, which a fresh clone lacks, against 0.01 s for the plain listing
(measured 2026-09-29). (Mutation rows
`walk-base-reads-a-tag-head-does-not-contain`, `merged-tags-lists-every-tag`,
`merged-tags-fails-on-an-unborn-head`,
`shallow-checkout-resolves-from-a-truncated-history`.)

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
Under `[[packages]]` the truncation reaches one more question — which files a
commit's own diff touches — and §4.1 answers it for the boundary commit, the
one whose parents the clone does not hold.

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

What that publish *starts* is GitHub's rule, not glyph's, and it was measured
rather than assumed (glyph-test's `tag-probe.yml`, 2026-07-22 to 2026-09-25,
listening on `push: tags`, `create` and `release`): a publish made by a person
— the UI, or `gh` under a personal token — fires all three, so a tag-driven
pipeline such as glyph's own GoReleaser runs (v0.2.0, v0.3.0, v0.4.0, v0.4.1,
v0.5.0 and v1.0.0 there: three runs each); a publish made with a workflow's
`GITHUB_TOKEN` creates the same tag and fires **nothing** (v0.2.1 and v2.0.0
there, published by `token-publish.yml`: zero runs). A caller that automates
the publish from inside Actions therefore also has to start whatever the tag
was meant to start, or publish under a token that is not `GITHUB_TOKEN`.

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
refusal there would take the whole verdict comment down with it. The mark is
only a mark where it is read, so the cut **closes every `<details>` block it
leaves open** before the notice goes on. The notes preview is the body's last
section and sits in one, so a comment whose tables fit the cap and whose notes
do not is cut inside it; cut at a line boundary and no more, the comment went
out with one `<details>` and no `</details>` (t-rrw0 (5); measured 2026-10-05
on the unfixed cut, over a body `preview.Render` wrote and over a 200-commit
pull through `preview --notes`: `TestCommentTruncationClosesTheFoldItCutsInside`,
`TestPreviewCutInsideTheNotesStillClosesTheFold`). GitHub renders such a body
with the rule and the notice *inside* the block, folded away under "Release
notes preview" with the notes, and the closed one with both after it — asked
of GitHub's own renderer the same day (`POST /markdown`, `gfm` mode, one body
of each shape: `…</ul><hr><p>… truncated…</p></details>` against
`…</ul></details><hr><p>… truncated…</p>`), and of the real surface: both
bodies posted as comments on a closed glyph-test pull and read back as
`body_html`, the notice inside the open block on the unfixed cut and after
`</details>` on the fixed one (glyph-test#118, posted and deleted). Mutation row
`preview-truncation-leaves-the-notes-fold-open`. The closers are characters
of the comment too, so the cut and what it must close are settled together
against the cap: appended to a head that already spent the budget, a closer
posts a comment GitHub answers 422, and the verdict is missing on exactly the
oversized pulls. The test sweeps the cut's line end across every distance
from the budget, a closer's dozen among them (mutation row
`preview-truncation-closers-overflow-the-cap`). The cut keeps whole lines, and
a line that ends exactly at the budget is one — it is kept
(`TestCutAtLineKeepsEveryLineThatFits`, row
`comment-cut-drops-a-line-that-fit`). The footer is past the cut
either way: a truncated comment carries none. Moving the notes block behind
the footer would keep it, at the price of a new layout for every comment that
carries notes, and the cut would still fall inside the block with the notice
folded away — so the block is closed, and the layout stays.) A next version
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
the probe is one read of that listing, best-effort, and a probe that finds
nothing or cannot look falls back to the replay, with the upsert's convergence
still deleting any duplicate on the next run.

That listing is read whole on every run, dry run included, and it prices a
release in releases rather than commits (t-n5tw R4). The managed drafts and the
published floor are each a question about every release on the line. GitHub's
listing takes only `per_page` and `page`: it documents no order a partial read
could stop on, and nothing filters drafts or a tag prefix; `releases/latest`
names one release per repository, the newest non-draft, non-prerelease by
`created_at` (REST docs, read 2026-09-29). So the listing costs one request per
100 releases before anything is planned, and a whole listing again per create
probe. Measured 2026-09-29 on a stand-in API: 7,272 releases took 73 pages
before planning, and a create answered 503 throughout took 219 more over its
three probes (292 pages and four `POST`s, exit 4). google-cloud-go's listing
ran to 7,496 releases on 2026-10-04 — 75 pages (REST `releases?per_page=1`,
its `Link` rel=last) — while GraphQL's `releases.totalCount` reported 1,000 for
it the same day, so that count is no substitute. Recorded, not optimised: the
largest listing in the fleet is glyph's own 37, one page (GraphQL's count over
the 38 non-archived repositories carrying a `glyph.toml`, 2026-10-04, which
glyph's REST listing matches at that size). `--dry-run` computes everything,
action included, and writes nothing.

**The hand region** (t-qgps): the rolling draft is the only place release
prose can be written ("the exit codes changed, fix your gates"), and the
upsert used to rewrite the whole body on every push — the one channel for
hand-written notes was also the one place work was silently destroyed. Every
draft body glyph writes now opens with the hand marker (an HTML comment, so a
published release renders no trace of it): everything ABOVE it survives every
upsert byte-for-byte (CRLF from a web edit included), everything below is
glyph's and is rewritten every time. The contract is printed where the human
is typing, instead of being an unwritten rule learned by losing work. A body
with no marker contributes no hand region — glyph cannot tell such a body's
hand-written lines from its own stale output, and guessing would resurrect
old machine text as if a human had written it. `checkReleaseBody` sizes the
FINAL body, hand region included. Rejected: a `RELEASE-NOTE:` commit footer
(v2 removed footer parsing, and prose does not belong in commit messages
addressed to the version fold); diffing the body against a re-render to
detect edits (the base moves between pushes, so staleness and hand edits are
indistinguishable). (Mutation row `draft-hand-region-discarded.patch`.)

**The compare link** (t-v7f7): per-commit shas and `#N` citations give every
line an address, and nothing gave the range one — glyph's own v4.2.0 page ended
at its last bullet with no URL in it (t-v7f7's measurement). A body glyph
renders from a walk that resolved a tag base therefore closes a non-empty notes
body with `**Full Changelog**: https://<host>/<owner>/<repo>/compare/<base>...<end>`,
GitHub's own generate-notes spelling of the line; an empty one gets none, as
`notes` prints none, so a `draft_on_none` placeholder over an empty fold carries
the marker alone. `<host>` is the API base's (`apiHost`), ports dropped — which
links GHE.com data residency, whose API host is `api.<sub>.ghe.com`, under that
API host: a hole named rather than closed, since no fleet repository sits in it.
`<base>` is the tag the walk resolved, in its own spelling (never re-spelled from
the parsed version), and only when the base is a tag. Re-spelled, a bare `1.0.0`
tag becomes `v1.0.0`, a ref the repository does not have, and GitHub answers
such a ref 404 (t-v7f7 measured cli/cli's `compare/2.0.0...<sha>` 404 beside
`v2.0.0`'s 200; on glyph 2026-10-05, `4.3.0...<sha>` 404 beside `v4.3.0`'s 200).
A typed `--since-tag` that is no tag — a branch, a remote-tracking name, a sha —
is resolved by GitHub against its own refs, not the checkout's (`main...<sha>`
200 and identical, `origin/main...<sha>` 404, measured on glyph the same day), so
the explicit arm takes the value only when `refs/tags/<value>` exists
(`gitsource.IsTag`). Each `/`-separated segment is path-escaped: git accepts
`#`, `%` and `)` in a tag name, where pasted verbatim `#` starts a fragment and
`%41` decodes to another ref, and GitHub decodes an escaped segment once, back to
the tag (measured 2026-10-05 on glyph-test, the REST endpoint and the page alike,
with a throwaway tag `rel(1)%41#x` on `v2.0.1`'s commit:
`compare/rel%281%29%2541%23x...<sha>` 200 with `v2.0.1`'s four commits; `%41`
left bare, 404 as `rel(1)A#x`; pasted verbatim, 404 as `main...rel(1)A`).
`<end>` is a sha — the draft's target, or HEAD for `notes` — because a draft's
tag does not exist until a human publishes, and `compare/<base>...<tag>` 404s for
the draft's whole life (measured 2026-09-29 on glyph-monorepo-test by the t-v7f7
ruling: `curry/v1.1.0...<target sha>` 200, `curry/v1.1.0...curry/v1.2.0` 404; on
glyph 2026-10-05, `v4.3.0...v4.4.0` 404). And a **full** sha, or no link:
`--target` reaches the API verbatim as `target_commitish`, which takes a branch
too (a draft posted to glyph-test with `main` read back `main`, the same day),
and GitHub resolves whatever stands on the right when the page is opened
(`v2.0.1...main` 200 at the branch's head, likewise), so a published body
linking `...main` would list every later push — commits the release never
shipped. An abbreviation is held to the same rule (resolved down to
four digits that day, which lasts only while no second object shares them), and
the target is not resolved through the checkout instead, because `--target` has
never had to name a commit the checkout holds. The draft's target itself is
untouched; only the link is withheld (`fullSha`). A walk with no tag base renders none:
the whole-history arm has no left side, and both stand-ins are wrong — `v0.0.0`
404s, and `HEAD` answers 200 with an empty diff, a link that lies
(`compare/HEAD...<sha>` "identical", 0 commits; on glyph 2026-10-05). The link is
rendered by **both** commands that render a body from the walk, `release` and
`notes --since-tag`, through one helper (`compareLink`), and it closes the notes
before `--footer-file`'s block, so a draft's machine region is still exactly
`notes`' output for the same walk (at the default target) followed by the
caller's footer. `notes --since-tag` is not an incidental caller: it is the body
glyph's own `goreleaser.yml` publishes through `--release-notes`, which
GoReleaser loads byte for byte and wraps in nothing (`.goreleaser.yaml` sets no
header or footer), and the body six Go CLIs publish the same way (the ruling's
census of the fleet clones, 2026-09-29) — a link composed by `release` alone
would have skipped glyph's own page, the page it was measured missing from.
`--range` and `--pr` render none, by the rule that has each source render only
the citation it can attest (`notesInput`): a local range names no repository and
a pull no release base. `notes --json` is unchanged — sections only, no link
key; the link lives in composed bodies, `notes`' stdout and `release`'s `body`.
Rejected: a GoReleaser `release.footer` over `{{ .PreviousTag }}`. GoReleaser's
previous tag is `git describe --tags --abbrev=0 <tag>^`, a second predecessor
resolver beside `below:` — the class t-s5n4 retired from this same workflow —
and on this repository it disagrees with the walk exactly where release
candidates sit: `v3.3.0-rc.2` for v3.3.0 (1 commit) where `below:v3.3.0` walks
from `v3.2.0` (13), and `v3.0.0-rc.3` for v3.0.0 (9 against 22; `git describe`
and `git rev-list --count`, 2026-10-05), so the link would cite a different
range than the notes above it; it is markdown composed by the caller, which Q11
keeps inside glyph; and it would put GoReleaser's template on glyph's page and
call it dogfooding. (`TestNotesSinceTagEndsWithTheCompareLink`,
`TestReleaseBodyClosesTheNotesWithTheCompareLink`,
`TestCompareLinkKeepsTheBaseTagsSpelling`, `TestCompareLinkNeedsATagBase`,
`TestCompareLinkNeedsNotes`, `TestCompareLinkEscapesTheBase`,
`TestCompareLinkNeedsAFullShaEnd`, `TestReleaseBodyCapCountsTheCompareLink`;
mutation rows
`tag-time-body-loses-the-compare-link`, `compare-link-after-the-footer`,
`compare-link-base-respelled-from-the-version`,
`compare-link-on-a-walk-with-no-base`, `compare-link-from-a-ref-that-is-not-a-tag`,
`compare-link-base-pasted-unescaped`, `compare-link-without-notes`,
`compare-link-ends-at-the-draft-tag`, `compare-link-ends-at-a-moving-ref`,
`release-body-sized-before-the-compare-link`.)

The `--json` verdict also carries the walk's expansion
provenance (`pulls`: each resolved pull and how many of its listed commits the
walk took in — a bot's and a skipped merge commit among them, so it is no
count of *participating* commits, §4.1), so
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
`::warning::` and classifies the squash commit's own message under the pattern
file. On this fallback path only, a message no pattern claims also degrades to
a `::warning::` and counts **none** — never a silent patch, and never exit 3:
the hard refusal stays with the lint gate and the range fold (§2, §3), and the
downgrade is owned by the walk assembly, keeping `internal/bump` pure. The v1
exception here (Q10: an unknown `:code:` carrying `!` or a `BREAKING CHANGE:`
footer normalized to a major) is **superseded with the grammar that defined
it**: v2 normalizes nothing, and no text reaches the version except through a
pattern the file carries. The fallback matches the commit's whole message like
any other, so a breaking marker survives the dark path exactly when a pattern
yields a major sigil (`!` or `%`) for it — the marker's home under §2, not a
special case of the walk. Under the presets that is the subject's sigil
alone: their one pattern that reads past the first line, the `amend!` claim,
yields no sigil, and a `BREAKING CHANGE:` footer moves nothing (t-h9y7, §2).
The body is not unread — the notes parse its trailer block for `$coauthors`
and `[[note.trailers]]` (§3) — but that renders prose, never a level.

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

### 4.1 Packages — independently versioned lines in one repository

**Status: shipped.** Designed 2026-09-10 (t-99rf); what landed, task by task —
the `[[packages]]` schema in `internal/config`, the attribution rule as
`internal/attribution`,
and `doctor`'s path-exists check (t-exs0); the line primitives — a tag is
parsed *on* a line (`bump.ParseVersionOn`, `config.Package.TagPrefix`), and
the walk base, the published floor and the managed drafts are each resolved
per line — plus `doctor`'s root-line advice (t-q047); and the walk itself
(t-ws0s): `internal/cli/lines.go` resolves the lines a `--since-tag` names,
walks the union once, fetches the files of each commit it places by its diff
from local git or the API, and partitions the walk per line, so `bump` and `notes`
answer per line over `--since-tag` and `--range` (the `packages` array
below, scalars empty), `lint --range` and the pre-push hook apply rules 2–3
and the contradiction check; and the drafts (t-qecb): `release` converges
one rolling draft per line (`internal/cli/release_lines.go` — every line's
upsert before any stray, the single line's bare draft deleted as residue,
the footer on every draft, `packages[]` in the verdict with the scalars
empty); and the preview (t-npye): `preview --pr` attributes the pull's
commits over the API and renders one headline and one table per touched
line (`internal/cli/preview_lines.go`, `preview.Package`), the pending
side from one walk; and the reusable (t-dc9e): `release.yml` hands the
per-line verdicts through as its `packages` output, the scalars `""`, and
refuses `app` / `binary` once the verdict says packages. Every `e-7hat` task
was done by 2026-09-15: the line is complete, the fleet pins a tag that
carries it (v3.3.0, the first), and its live fire is `glyph-monorepo-test`
(below — the permanent packages harness, named beside `glyph-test` in
CLAUDE.md). A binary older than the
schema refuses the file (§2's strict
decoder — an old pinned binary must refuse a grammar it cannot read, never
ignore it). The decisions below are ratified so that each task inherits them
instead of re-deriving them; the paragraphs that describe measured behaviour
say so, and the rest is the design.

**The model.** A *package* is a declared subtree of the repository with its
own version line: its own tags, its own walk base, its own rolling draft.
Everything §4 says about the walk — footprints, the covered ledger, the
incomplete-walk refusal, the wedge — is about *which commits are unreleased*,
and none of it changes; what packages add is one question asked of every
walked commit before the fold, *which line does this commit move?*,
and then the fold, the version step and the draft convergence run once per
line. A repository with no `[[packages]]` is one line with no name, and every
verdict it gets today is unchanged byte for byte (golden-pinned; mutation row
`packages-absent-changes-the-single-line.patch`). So the mode is
**independent** versioning only: *fixed* versioning — every package moving
together under one number — is what the single line already is, and a second
spelling of it would make one repository mean two things (rejected: lerna's
`fixed`, changesets' `fixed` groups).

```toml
[[packages]]
path = "haiku"              # the subtree; the tag line is haiku/vX.Y.Z
# name = "haiku"            # what a commit scope may call it; default: the last path segment

[[packages]]
path = "."                  # the root package: the bare vX.Y.Z line, and every file no other package claims
name = "core"               # its default "." is no preset scope's word: under the presets name is required
```

- **The tag line is `<path>/vX.Y.Z`, derived, not configurable — with the
  path's major version subdirectory folded into the major.** That is the Go
  multi-module rule (go.dev/ref/mod, "Module paths": the tag prefix is the
  module subdirectory *not including the major version suffix*, and a module
  at major N ≥ 2 may live in a `/vN` subdirectory), the one ecosystem that
  *mandates* a shape, and it is the shape the largest monorepos in the wild
  carry (measured 2026-09-10: google-cloud-go `storage/v1.67.1`,
  opentelemetry-go `exporters/prometheus/v0.59.1`, aws-sdk-go-v2
  `config/vX.Y.Z` beside a bare root line; measured 2026-09-13:
  google-cloud-go `pubsub/v2/` tags `pubsub/v2.7.0` beside `pubsub/v1.9.1`,
  etcd `client/v3/` tags `client/v3.6.0`). So `path = "pubsub/v2"` is the
  **v2 line on the `pubsub/` prefix**, and a `path = "pubsub"` beside it is the
  free line holding every other major; a root-level `path = "v2"` is the bare
  v2.\* line the same way (`config.Package.TagPrefix`, `Config.LineOf`;
  mutation rows `major-subdirectory-kept-in-the-tag-prefix`,
  `line-reads-tags-of-every-major`). Every reader of tags — the walk base,
  the published floor, the managed drafts, `--since-tag`'s line selection —
  asks its question on a *line*, prefix and major together, so two lines on
  one prefix never read each other's tags, and a locked line's placeholder
  draft is `<path>/Unreleased` (under the path, the one name that is
  unambiguously its). The first cut derived `pubsub/v2/` and gave the module
  a tag line nothing in the Go ecosystem reads: zero tags, a current of
  v0.0.0 (or the deprecated v1 line's, with `pubsub` declared alone), and a
  `^` that drafted `pubsub/v2/v2.3.0` — two minors *behind* the published
  `pubsub/v2.3.0` of the day, under a name `go get` never resolves; the
  published floor could not catch it because it read the same dead prefix
  (t-z9d3). Two steps follow from a locked line holding one major: its
  **first release is `vN.0.0`** whatever the level (nothing below the major
  is on it), and a step that would leave the major — a `!` on `pubsub/v2`
  stepping to `pubsub/v3.0.0`, which the module path `/v2` disowns, or a `!`
  on the free `pubsub/` line stepping onto the declared v2 line's own floor —
  is **refused at 3**, the wedge's class and the wedge's escape (the breaking
  change belongs in `pubsub/v3` as its own package; a tag cut by hand past
  the commit releases the wedge), never a tag no module claims (mutation row
  `locked-line-steps-past-its-major`). A free line with no locked sibling
  steps its major exactly as the single line always has: which module path
  the tag needs is Go's business and the author's, as before. Rejected:
  reading `go.mod` to learn the major (glyph reads no manifest — §4.1's
  rejected list — and the directory already says it), and a `major` key on
  the package (the path carries it, and a knob could disagree with the
  directory). A `tag_prefix` knob
  was rejected for the reason §3 gives for the fixed sigil alphabet: a verdict
  must be readable from the file alone, and a line whose tags do not say which
  directory they version is what the Go rule exists to prevent. `pkg@X.Y.Z`
  (npm) and `pkg-vX.Y.Z` (release-please, cocogitto) are therefore not
  producible; glyph writes no manifest versions for those ecosystems either,
  so that door was never open. `path = "."` is the one exception by
  definition: the root module's line is bare `vX.Y.Z`, which is why a
  repository that declares it keeps every tag and draft it has. Because the
  path is the tag's prefix, **a path git cannot tag does not load**: every
  segment must be a refname component `git check-ref-format` accepts — no
  leading `.`, no `.lock` suffix, no `..`, no `@{`, no space, control
  character or any of `~ ^ : ? * [ \` (the rules that bind a whole name's
  end — a trailing dot, a lone `@` — never reach a prefix, since every tag
  ends `vX.Y.Z` and every placeholder `Unreleased`) — and the path may not
  begin with `-`, which `git tag` refuses as a tag name's first character
  though `check-ref-format` accepts it (the first character only: `a/-b`
  tags). Measured before the rule (t-f2cb, 2026-09-29): `path = "a b"`
  loaded, `bump` printed `a b/v0.0.1` at exit 0 (as it did for `x.lock`,
  `c~d` and `.hid`), `git tag` refused every one, and `doctor` passed both
  `glyph-toml-loads` and `package-paths-exist` on `a b`; and before its `-`
  half (2026-10-04, git 2.54.0), `path = "-dash"` loaded and `bump` printed
  `-dash/v0.0.1` at exit 0, a name `git tag` refuses. Of the 32 paths
  `TestLoadRefusesAPathGitCannotTag` asks git about, the 21 git refuses all
  loaded at adfc5e1. The test asks git — `check-ref-format` and `git tag`
  both — for each path's stepped tag and placeholder rather than restating
  the rules — a model checked against itself proves nothing (mutation rows
  `packages-path-git-cannot-tag-accepted`,
  `packages-path-leading-dash-accepted`).
- **`name` is the scope's word for the package**, defaulting to the last path
  segment — for a major version subdirectory the segment before it with the
  suffix kept, `pubsub/v2`, which is what such monorepos write in the scope
  (google-cloud-go: `feat(pubsub/v2): …`) — and must be unique across packages
  (a load error names the two; the remedy is to set one). It exists because
  the presets' scope group is `[a-z0-9-]+` and cannot spell
  `exporters/prometheus` or `pubsub/v2`, and the loader holds every name to
  that: **a name no scope can spell does not load** (exit 2 on every verdict
  command — §5, the config is the yardstick; `doctor` fails
  `glyph-toml-loads`, exit 3). Each name, set or defaulted, is asked of the
  `scope` group's own sub-expression, anchored whole, in the patterns whose
  groups a commit binds — not `skip` (placed nowhere, its message never read)
  and not `unlandable` (reported unmatched, with no groups) — and, in a
  pattern that names two groups `scope`, only the last: the one `Match`
  keeps, since a later capture overwrites an earlier one even when its
  alternative did not take part. One such group accepting the name is
  enough: which pattern wins is a property of each commit, the union
  `config.validateLineNames` already takes for `note.line`'s names. The
  refusal names the package, the rule that produced the name, each pattern it
  asked, as written, and both remedies — set `name` to a word one of them
  spells, or change a scope group so it spells this one (with
  `(?P<scope>api|web)` and packages `api`, `web`, `db`, setting a name alone
  lands on the duplicate-name refusal) — and suggests no word: glyph quotes
  the grammar, it does not invent the author's vocabulary. The root package
  is no exception: its default is `.`, which no preset's scope spells, so a
  root declared under the presets sets `name` — the `name = "core"` of init's
  snippet and the `set name` doctor's root-line advice already gives. Its
  name is read like any other: a zero-file commit scoped `(core)` lands on
  the root line (`bump` steps `.` from v0.0.0 to v0.0.1), and a `(core)` on a
  commit touching only `haiku/` is a contradiction (exit 3; both measured
  2026-09-29). A file whose patterns capture no scope at all is exempt, not
  refused — whether a scope exists is the pattern file's decision (§2), and
  its packages are placed by their paths alone. The group's own alphabet is
  necessary, not sufficient — context around the group (a lazy quantifier,
  an overlapping neighbour) can still keep a word it spells from being
  captured whole; the loader refuses what no message can spell and claims
  nothing more. Measured before the rule (t-mfny, t-f2cb; 2026-09-29 at
  adfc5e1): `path = "my_lib"` loaded, a shared-only `~` was refused "name the
  package in the scope (one of my_lib, other)", and `:memo:(my_lib)~` then
  failed `lint --range` and `lint --message` alike as matching none of the
  patterns — the refusal's one scope escape could not be written, so a
  shared-only change meant for `my_lib` had no carrier but `=`, and no scope
  could ever trip the contradiction check on it; `MyLib`, `v2.0`, the root's
  `.` and the `pubsub/v2` default were offered the same way. Rejected:
  widening the presets' group to spell `/` (it changes only what `init`
  writes — nothing rewrites a `glyph.toml` already written (§2) — so the
  fleet's files (42 in its local clones, 75 scope groups, all `[a-z0-9-]+`,
  measured 2026-09-29; every one loads under the rule) keep the old alphabet
  unless every one is rewritten, the amend! body pattern moving in step —
  for a spelling no declared package uses, and it leaves `my_lib`,
  `MyLib`, `v2.0` and the root where they were); a spellable default for a
  major version subdirectory (`pubsub-v2` is a word glyph would invent where
  the ecosystem's own scope is `pubsub/v2` (google-cloud-go), and the bare
  `v2` collides across modules); every scope group having to spell every
  name (a group narrowed on purpose — a literal `(?P<scope>deps)` — would
  refuse every package in the file); a warning or a doctor check instead (a
  warning leaves `bump` and `release` running on a line no commit can name,
  and a load error is what `glyph-toml-loads` already carries). Mutation rows
  `packages-name-no-scope-can-spell-accepted`,
  `packages-name-union-becomes-intersection`,
  `packages-name-counts-an-unread-scope`,
  `packages-name-counts-a-shadowed-scope-group`, `packages-root-name-exempt`,
  `packages-scopeless-grammar-refuses-names`. It names nothing else: the
  draft is named by its tag, the notes by their section titles.
- **One `glyph.toml`, at the top level, as today.** A per-package file was
  rejected: a commit is one message judged under one pattern file, and N files
  would be N grammars for the same message, with the winning one decided by a
  path the message does not carry.

**Attribution — path decides, scope carries what path cannot.** A commit
that is not a merge commit is placed by the packages
whose subtree its own diff touches; a file belongs to the package with the
**longest** path prefix, so a nested package takes its files out of its
parent and the root package holds the remainder. What attribution may read of
the *message* is decided first, by what the fold reads: a commit the fold
reads (a *participating* commit: not an `exclude_authors` author, not a skip
pattern, matched — "The fold, the step, the exit", below) is placed
by the rules below, scope and sigil included; a commit the fold does not read
is placed by rule 1 alone — its files, never its message, because the message
is exactly what glyph declared it would not judge. So an `exclude_authors`
commit moves no version and appears in the notes of the lines its files
touch, and on no line when they touch none (the shape rule 3 gives a
shared-only `=`) — a bump of root CI in a repository with no root package is
in no line's notes; a skip-pattern commit appears nowhere and is placed
nowhere, its files never asked for — which is why the presets skip only
merge commits, whose diff is never read, and claim the `fixup!`/`squash!`
autosquash artifacts as unlandable instead (§2; skipped, a `--fixup` whose
files lie on another line than its target's left that line at none, where the
autosquashed history moves it — t-mfny, measured 2026-09-29, mutation row
`presets-skip-autosquash-artifacts`); and a message no pattern claims (one an
`unlandable` pattern claims included) joins every line it is unreleased on,
so the fold refuses it there (§3). The first
cut placed everything the fold would not read on every line (measured
2026-09-11 on the live-fire harness: a dependabot commit touching only
`haiku/poem.go` rendered under all five line headings, lines with no commit of
their own grew a Dependencies section for it, `release --dry-run` wrote it
into every other line's draft, and `preview` dropped the same commit from
every line — two answers in one run; ratified 2026-09-26, t-sr1c; mutation
rows `packages-excluded-author-placed-on-every-line`,
`preview-packages-excluded-author-dropped`). The rules, in the order they are
asked:

1. Its files lie under one or more packages → it is placed on **each** of
   them, with its one sigil. A commit that renames across two modules moves
   both lines; that is what it did.
2. Its files lie under no package (a *shared-only* commit: root CI, the
   workspace file, a README, in a repository with no root package) — or it
   has no files at all, whatever is declared (below) — and the winning
   pattern captured a `scope` naming a package → it is placed on **that**
   package. The author said where the impact lands and the tree could not.
3. Otherwise it has **no carrier**. With sigil `=` that is the expected shape
   of shared housekeeping and it participates in no line (it appears in no
   draft — there is no line for it to appear on). With any other sigil it is a
   **refusal of the lint class (exit 3)**: the author claimed a version impact
   and nothing can carry it. The error names the escapes the commit can
   actually take — name a line in the scope when the winning pattern captures
   one that spells it, write `=` when that pattern's sigil group can capture
   it (or leave the sigil out, where that alone makes the message a `=`),
   declare the package its files belong to when it has files (below) — and,
   in the walk, the wedge escape per line.

A commit that shows the tree **no file** — `git commit --allow-empty`, or a
merge commit some pattern other than a skip claims, whose diff is never read
(below) — is placed by rules 2–3 whatever is declared, a root package included
(t-n5tw 3, t-f2cb (3); ratified 2026-09-29). The root package is a claim on
files, "every file no other package claims", not on commits. A commit with
none has told the tree nothing, and rule 2 exists for exactly that: the scope
says where the impact lands. Measured 2026-10-05 at 135eead with `haiku` and
`.` (`core`) declared: an empty `~` exits 3 naming `(core)` among the scopes,
`(core)~` moves the root line alone, an empty `=` passes on no line, and a `~`
merge of a haiku-only branch exits 3 while `(haiku)~` puts it on haiku
(`TestNoFilesHaveNoCarrierUnderARootPackage`,
`TestLintRangePackagesNoFileCommitUnderARootPackage`). Letting the root
package carry such a commit was rejected. For a merge it steps a line the
merge's content need not touch — the haiku-only merge would move the root line
beside haiku's. For an empty commit it guesses which line the author meant,
where one word of scope says it — a word only a spellable name gives, which is
why the loader holds the root's name to the scope grammar like any other
(above), and why under a grammar that captures no scope the refusal offers
none (`TestLintRangePackagesScopelessGrammarOffersNoScope`). The single line
steps on an empty `~` only because it asks no attribution; declaring packages
is what turns the question on, the root package alone included. Measured the
same day: an empty `~` and a claimed `~` merge each exit 0 and step v0.1.0 to
v0.1.1 with no packages, and exit 3 with `path = "."` (`core`) the one
declaration. The root package keeps the single line's tags and drafts, not its
verdict on a commit without files (mutation row
`attribution-root-carries-a-commit-with-no-files`).

And a file has two states: a declared package's — with a root package
declared, every file no other package claims is the root package's — or
nobody's. A subtree nobody declared is not a third (t-n5tw R1; ratified
2026-09-29). Measured 2026-10-05 at 135eead on a fixture declaring `storage`
alone: `(spanner)^` under an undeclared `spanner/` exits 3, `(spanner)=`
passes, declaring `spanner` moves that line alone, and `(ci)^` on
`.github/workflows/ci.yml` exits 3 by the same path. An *undeclared module*
state was rejected. glyph reads no manifest (above), so it has no other way to
tell a module from root CI or a docs tree. The one signal the message carries
— a scope naming no package — cannot be it either: it would let `(ci)^` on
root CI through as the silent none this rule refuses (mutation row
`attribution-unknown-scope-carried-nowhere`; before the row no test caught
that mutation — the whole suite stayed green under it at 135eead, measured the
same day). One `glyph.toml` judges every commit in the repository, as the
single line always has. Partial adoption is not a mode: a repository declares
every module whose version a commit may claim, and no gate can enforce it.
With `storage` and `.` declared, the same `(spanner)^` exits 0 and steps the
root line v0.1.0 to v0.2.0 with nothing on stderr (measured the same day) —
the omission is refused without a root package and silent under one, because
nothing distinguishes the root package's own files from an undeclared
module's, the reason no third state exists. So the refusal names the
declaration beside the scope, and does not guess its path: glyph cannot tell
which directory is the module, and the first path segment would have offered
`path = ".github"` for root CI, a prefix the loader refuses (above). Where
the loader would refuse the root's default name it says the root declaration
takes a name, since `path = "."` alone does not load there (above). Whether
it would is asked of the loader's own check, put to the default `.` over
every pattern whose groups a commit binds, a `warn` pattern included. It is
not read off where the message could be reworded to (below), which leaves
`warn` patterns out: read that way, a file whose only scope group sits in a
`warn` pattern was told `path = "."` declares the root package, and the file
so written exits 2 at `lint --range`, `bump` and `preview` alike (measured
2026-10-05). `TestSayableRootNeedsNameIsTheLoadersAnswer` puts each grammar
to the loader itself;
`TestLintRangePackagesRootDeclarationTakesANameWhereTheLoaderAsksOne` takes
the declaration as worded under that grammar and under one that captures no
scope, and `TestLintRangePackagesRawRevertNamesTheEscapesThatWork` under the
presets (mutation row `refusal-declares-a-root-package-the-loader-refuses`).

The escapes are asked of **this commit**, not of the file (t-mfny (A)). The
loader holds the file to "some scope group spells every name" (above); which
pattern wins is a property of each commit, so the refusal asks the one that
claimed the message (`config.Sayable`): the scope escape lists the package
names that pattern's scope group can capture, `=` is offered when its sigil
group can capture it, and the declaration when the commit has files. The
shipped raw-revert pattern fixes its sigil at `~` and captures no scope, so
reverting a shared-only commit in a repository with no root package was
refused with "name the package in the scope … or write =", neither of which
that message can do. Measured 2026-10-05 at 135eead: the raw revert, `Revert
":memo:(haiku)= …"` and a `=` written after the quotes all exit 3, while
`:rewind:= Revert "…"`, `:rewind:(haiku)~ Revert "…"` and declaring the root
package (`path = "."`, named) each pass. Its refusal now says which pattern
claimed the message and that it fixes the sigil and captures no scope, then
names what works:
rewording so another pattern claims it — with what the file's other patterns
can capture, a skip, an `unlandable` and a `warn` pattern never counted,
since a message reworded for one of those is placed nowhere, never lands, or
takes a form the file's author would rather not see — or the declaration
(`TestLintRangePackagesRawRevertNamesTheEscapesThatWork` takes each escape in
turn; `TestNoCarrierNamesOnlyTheEscapesTheCommitCanTake` is the sentence's
format spec; mutation rows
`attribution-refusal-names-an-escape-its-pattern-cannot-write`,
`sayable-rewords-under-a-pattern-no-landed-message-may-take`). What a group
can capture — the names, a `=` — it reads off the group's own
sub-expression, like the loader's check: necessary and not sufficient. What a
message can go without it does not read off the pattern at all (below). The
sentence reads more than attribution's four inputs — the
claiming pattern, that a merge commit's diff was never read, that a diff was
not read whole — and the answer does not: one helper (`attribute`,
`internal/cli/lines.go`) sets them on a refusal already returned, for the
walk, `lint --range` and
`preview` alike, each with a test that reads its own refusal (mutation rows
`walk-`, `lint-` and `preview-refuses-a-merge-commit-as-touching-no-file`).
A refusal over a diff not read whole is one the walk withholds and quotes
inside its warning (below), so it speaks of the files read — "no file of this
commit was read", "the files read of it (…)" — where the same words said of
the commit, "touches no file", would contradict the warning around them,
which says a package it touched is missing (mutation row
`withheld-refusal-says-of-the-commit-what-is-known-of-the-files-read`).

An escape that **takes something out** of the message is proven against the
message, never read off the pattern (found in review, 2026-10-05). There are
two: "drop the scope", which the contradiction offers (below), and "leave the
sigil out", for a pattern whose sigil group cannot capture `=` but whose fixed
`semver_sigil` is `=` — `[a-z]+(?P<semver_sigil>[~^!])?: ` reads `fix: …` as a
`=`. What a group can capture is a question about the group. What is left
once the scope is gone, and which pattern takes it, is a question about the
whole message and the whole file, so glyph asks it of them: the message is
rewritten and run through `Match` — all that lint judges a message by — and
the escape is named only when the same pattern claims the result, with no
scope and its sigil unchanged, or as `=`. The pattern's tree only proposes
what to take out: the span the scope group's innermost optional ancestor
matched (a `?`, a `*` or a `{0,n}` — `(haiku)` under the presets), where what
goes with the scope is punctuation or space alone; and the sigil's own
capture. A wrong proposal costs an escape left unnamed, never one that fails
when taken. The first cut answered from the tree: a scope under a `?`, a `*`,
a `{0,n}` *or one branch of an alternation*, or a group able to capture
nothing, was droppable. Measured 2026-10-05, before this rule, on
`lint --range`, `bump --range`, `bump --since-tag` and `preview` alike: under
`(?:[a-z]+\((?P<scope>…)\)|release)` the refusal told `fix(haiku)~: …` to
"drop the scope", and `fix~: …` then matched no pattern (exit 3); and under
the fixed-`=` pattern an empty `fix~: …` was told that nothing a message the
pattern claims can write carries it, while `fix: …` passed (lint 0, bump 1).
`Match` also sees what no tree can: an earlier pattern that claims the message
once its scope is gone — first match wins, so an unlandable `^wip…` above the
grammar takes `wip~: …` — which is why the question is put to the whole file
and the answer must be the *same* pattern's. Where nothing is proven the
sentence says less, not more. A scope neither proven droppable nor required
by the pattern's tree (no `?`, `*`, `{0,n}` or alternation lets a match pass
it by, and it cannot capture nothing — exact only as a yes) is called neither.
And a refusal with no escape to name stops at what it read of the claiming
pattern: the absolute it went on to state was false wherever another message
of the pattern reaches the fixed `=` — `chore: …` under
`(?:fix(?P<semver_sigil>[~^!])|chore): ` passes (measured the same day).
`TestSayableProvesARemovalAgainstTheMessage` is the table, each row naming
the rewritten message or why the tree alone would have said yes;
`TestLintRangePackagesDropTheScopeIsProvenAgainstTheMessage` and
`TestLintRangePackagesFixedNoneIsReachedByLeavingTheSigilOut` take each escape
their findings name and assert the exit code, then write the one a finding
withholds and watch it fail (mutation rows
`refusal-drops-a-scope-the-pattern-tree-calls-optional`,
`refusal-names-a-removal-match-never-proved`,
`refusal-drops-a-scope-with-the-words-around-it`,
`refusal-leaves-a-fixed-none-unsaid`,
`refusal-says-no-message-of-the-pattern-can-pass`).

Two things the rules deliberately do not do. A scope that **contradicts** the
tree — files only under `curry/`, scope `haiku`, a version sigil — is refused
the same way, on the same reasoning as rule 3: the scope is checked only when
it names a package, so `(ci)`, `(deps)` and every free-form scope in the fleet
stay untouched, and a scope naming a package that owns none of the commit's
files is an authoring error the gate can see. *Owns* is rule 1's word — the
longest prefix — and the check asks nothing else (t-mfny (C), t-n5tw 4;
ratified 2026-09-29). With `travel` and `travel/onsen` declared, `(travel)~`
on a commit touching only `travel/onsen/o.md` is a contradiction, and so is
the root package's name on a file a declared package owns. Measured
2026-10-05 at 135eead: each is refused at exit 3, while `(onsen)~` or no
scope moves `travel/onsen` alone and leaves `travel` at none
(`TestLintRangePackagesScopeIsCheckedByOwnership`). Containment — refuse only
a package that neither owns nor contains the files — was rejected on three
counts. A nested package takes its files out of its parent (Go's rule for a
nested module, the one the tag line follows), so the parent's line does not
move, and the accepted message would name a line the commit leaves where it
is — the silence this check exists to break. Every file lies under `.`, so
the root package's name would be the one scope no commit can contradict. And
a parent's scope over its nested package's files asks for the cascade
rejected below: refused, the author is told; accepted, the request is dropped
with nothing said. What was wrong was the sentence. It said the commit "does
not touch" `travel` while naming files under `travel/onsen`, and it printed
root-owned files as lying "under ." (t-n5tw 5). The refusal speaks ownership:
one file per line the commit does move, named with the package that owns it
(`travel/onsen/o.md belongs to onsen (travel/onsen)`; the root package is
called that), the files no package owns counted and never said to belong to
anything, then the scopes of those lines that the claiming pattern can write
— and "drop the scope" only where this message, its scope dropped, was run
through the patterns and came back that pattern's (above)
(`TestContradictionNamesTheOwnersAndTheScopesThatWork`,
`TestLintRangePackagesContradictionOffersNoDropWhereTheScopeIsRequired`;
mutation row `attribution-scope-checked-by-containment`).

The check takes rule 3's exemption with its reasoning (t-n5tw R2; ratified
2026-09-29). What it guards is a message that claims one line while the diff
moves another, and a `=` moves none: a `=` whose scope names another package
is placed by its files like any other `=`. Declaring a root package is what
showed it. `:wrench:(haiku)= update CODEOWNERS` on a root file is carried by
haiku under rule 2 while no root package is declared, and became a
contradiction — exit 3, and a wedge once merged — the moment `.` was;
`:bug:(haiku)~ fix CODEOWNERS` flips the same way (measured 2026-10-05 at
135eead: without a root package lint passes both, haiku at none and at patch;
with `.` declared lint and bump exit 3 for both). For the version sigil the
flip is right: the declaration changes where the claim lands, so the refusal
protects which line moves. For `=` every line folds to none whichever package
carries it, so the refusal protected nothing a version can show, and wedged
the walk. A scope on a `=` is the author's taste, and lint has none (§2)
(`TestContradictingNoneIsPlacedByItsFiles`,
`TestLintRangePackagesJudgesTheDiff`; mutation row
`attribution-contradiction-refuses-a-none`).

And — the second thing — there is no `all` scope and no "shared moves
everything" arm (knope has the first, and the second was the obvious
default): a shared-only `~` that steps every line is fixed versioning
entering through the back door, and the independent mode exists so that a
line moves for its own reasons. A change that really does alter every
package's behaviour touches every package's files, and rule 1 already answers
it.

**Where the files come from.** For a commit the released branch holds — the
fallback arm, a merge-merged pull's landed commits, and every commit a
`--range` fold reads — local git answers (`git diff-tree`), free. One such
commit local git cannot answer for: a shallow clone's **boundary**, whose
parents the clone does not hold. git reads it as a root, and `--root` answered
its whole tree as its own diff (measured: `curry/b haiku/a` where the full
clone says `curry/b`), so a `--depth 1` clone's `lint --range HEAD` found a
carrier for a shared-only `^` and exited 0 where the full clone refuses it at
3, and the walk moved every line the tree touched (t-esm5). `DiffTreeFiles`
answers the boundary **unreadable** — told from a true root by the commit
object's own header, which still names the parent git no longer reaches, since
`.git/shallow` lists a true root inside the depth as well (measured; mutation
row `gitsource-shallow-boundary-refuses-a-true-root.patch`) — and the
walk places it by **rules 2–3 over no file**, the rule for a file listing
GitHub will not give at all (below): a scope naming a package carries the
commit there, a refusal is withheld and the commit carried nowhere, and one
warning says which happened and that no file was read to confirm it; lint
judges its message and warns that its attribution went unchecked
(`TestDiffTreeFilesAtAShallowBoundaryIsUnreadable`,
`TestPackagesWalkPlacesAShallowBoundaryByScopeAndSigil`,
`TestLintRangePackagesAtAShallowBoundaryIsNotReadAsTheWholeTree`; mutation
rows `gitsource-shallow-boundary-diffs-its-whole-tree.patch`,
`packages-walk-carries-a-scoped-shallow-boundary-on-no-line.patch`,
`packages-walk-refuses-a-shallow-boundary-over-no-file.patch`,
`lint-attribution-exits-at-a-shallow-boundary.patch`). A caller that does not
ask gets the answer as a git failure, 4, never as a diff.

That placement **overturns the first cut** (#253), which carried the boundary
on no line whatever its scope and called it the capped listing's answer with
nothing read. It was not: #250 had already ruled that a listing with nothing
read still lets a scope carry (below), so one shallow walk placed an unlisted
`:bug:(curry)~` on curry and a boundary `:bug:(curry)~` on no line, where the
full clone carries both (found by #253's integration review, 2026-10-04;
measured again before this rule, curry patch over one commit of the two —
`TestSinceTagPackagesPlacesABoundaryAndAnUnlistedCommitAlike`). No incident
stood behind "no line" itself; the incident was the whole-tree read, and that
stays closed. One arm of the walk now withholds for a truncated listing, a
422 and a boundary alike, so the three cannot come to place a commit two ways
again. The rule was put to an independent refutation before it was taken
(t-esm5, 2026-10-05) and held: replaying glyph-monorepo-test's history with
each version-claiming commit in turn as the boundary, scope and sigil placed
4 of 11 as the full clone does and "no line" none, and moved no line the full
clone would not. Its price is named, and pinned by the first test's last
two cases: a scope the unread files **contradict** moves the line it names —
`:bug:(haiku)~` over a file under `curry/`, or over one the root package
owns, steps haiku where the full clone refuses at 3 — which is what a claimed
merge commit already does in a full clone (below; measured in the same run
against the files its branch touched) and a scoped 422 since #250. Carrying
nothing was the other consistent rule, and it would have taken #250's scope
arm with it. What moves is a report, never a write: under `[[packages]]`, a
`bump` or `notes` over a shallow checkout whose boundary names a package
answers at 0 with that line where it answered 1 with every line none — no new
code — and steps from v0.0.0 where the clone fetched no tag, which a `--depth
1` clone does not (v0.0.1 where the full clone says v0.1.1; the same test).
A since-tag walk over a shallow checkout is an incomplete walk already (§4),
so `release` refuses it at 4 and `bump`/`notes` warn. That shortfall is the
checkout's and stays the checkout's: a boundary is recorded as no unread
listing, whose remedy — a tag past the commit — is the wrong one where the
full history is the right one. Recorded as one, `release` refused it in a
422's words, naming a pull request that does not exist and a tag to cut past
the boundary (`TestReleasePackagesShallowBoundaryIsTheCheckoutsShortfall`;
mutation row
`packages-walk-records-a-shallow-boundary-as-an-unread-listing.patch`).

For the squash arm, whose listed shas exist on no branch, the API does:
`GET /repos/{o}/{r}/commits/{sha}` returns the commit's own files for a sha no
branch holds — **measured** 2026-09-10 on glyph-test #83 (inner `2aff743`,
unknown to local git, answered with one file where the pull's net diff had
two). So the price of attribution is one round trip per squash-arm inner
commit that is placed by its files — every one the fold reads and, since
t-sr1c, every one an `exclude_authors` author wrote, a bot's bump inside a
human's pull being placed by its diff like any other's; a skip is placed
nowhere and never asked for — on top of §4's one per merge point and one per
pull: a squash-merged pull of *k* such commits costs 2 + *k* requests where
it cost 2 (measured 2026-09-10 on glyph-monorepo-test, pulls #1 and #2 of one
and two commits: seven requests, four of them the merge points and the
listings; and 2026-09-26 in the fixture, a pull of one human and one
dependabot commit: four requests where the first cut paid three). A pull
whose merge point an excluded author wrote is never expanded at all (§4's
author gate), so the routine dependabot squash — authored by `dependabot[bot]`,
as GitHub writes it — is placed by its landed net diff from local git, free;
nothing else in the walk changes price, and a repository without
`[[packages]]` pays nothing new because no file is ever asked for. The
whole-history cap (`sinceTagWalkCap`) still counts commits, not requests: a
capped first walk of a packages repository may cost up to twice the cap. The
endpoint pages its file list at 300 per page (`Link: rel="next"`, followed)
and stops at 3000 — **measured** 2026-09-10 on torvalds/linux's 17k-file root
commit: `rel="last"` at page 10, page 11 empty; a commit whose listing reaches
that cap is an **incomplete walk** in the sense §4 already defines (a package
it touched past the cap is unreachable, not absent), recorded in `walkFacts`
as `FilesCapped` beside `Truncated`, so `complete()` is false and a writing
command refuses (exit 4). Two corollaries, both ratified with t-c6r5 and
t-ft7p after the first cut got them wrong. A refusal attribution would hand
down over a truncated listing — no carrier, or a scope naming a package that
owns none of the *visible* files — is **withheld**: both are claims about files
the walk could not read (the package past the cap may be exactly the one
named), so the commit is carried nowhere and the walk's own incompleteness
answers, never the gate code; measured before the fix, a capped commit under
no package exited **3** with the wedge remedy, which would have cut a tag past
a commit whose true attribution the cap had hidden, and `lint.yml`'s push arm
would have swallowed it as a merged violation. And the cap counts the
**entries** GitHub listed, not the names returned: a rename is one entry under
two names, so a whole listing of 1500 renames is whole — counted by name it was
refused at 4 with a remedy a re-run could never satisfy. A listing GitHub will
not give at all is the same shortfall with nothing read (t-esm5).
`GET /commits/{sha}` answers a sha it does not know with 422 `No commit found
for SHA: <sha>` — the same status and message `commits/{sha}/pulls` gives, the
bodies differing only in `documentation_url` (both measured 2026-09-29) — so
`IsCommitUnknown` reads both, and `listFiles`, the one reader the walk and
`preview` share, takes the answer as a capped listing with nothing listed:
`walkFacts` records it as `FilesUnknown` beside `FilesCapped`, and a refusal
attribution would hand down over the empty listing is withheld as over a
truncated one. An unscoped commit claiming a version impact is therefore
carried nowhere, while a scope naming a package still carries the commit
there — rule 2, the non-refusal answer a truncated listing already lets stand.
A 422 on a later page keeps the files the pages before it listed, by the
capped listing's own rule — attribution runs over what GitHub did list, the
listing is recorded as unread, and only the refusal that needs the whole
listing is withheld — though no live trigger for a later-page 422 is known,
so that arm is measured with a stand-in server only
(`TestSinceTagPackagesLaterPage422KeepsTheListedFiles`; mutation row
`later-page-422-discards-the-listed-files`).
`release` refuses at 4 naming it, `bump` and `notes` warn, and `preview`
carries it in the PR-side caveat, or in the pending one when the pending walk
meets it. It is not §4's lag fallback. That arm reads a lagging merge point's
own message — a weaker copy of the input it lost — and stays complete; here
the sha came from GitHub's own pull listing, its message was read, and the
files are the one input attribution has no weaker source for. Before this,
the same 422 died as a raw `github: GET …/commits/<sha>: 422` at exit 4 on
every command that asked — `release`, `bump`, `notes`, and `preview`'s whole
comment (measured on the unfixed source by the tests below) — the one unread
input the walk handed back unclassified, against `CommitFiles`' own contract.
A re-run clears neither shortfall for certain — a capped listing is capped
again, and a 422 clears only once GitHub lists the commit — so each clause
carries its own remedy: the wedge escape per line whose range holds the
commit (a `<line>` tag at or past the pull's merge point), after a re-run for
the 422 (`TestSinceTagPackagesUnlistedFilesAreAnIncompleteWalk`,
`TestPreviewPackagesUnlistedFilesAreCaveated`,
`TestReleasePackagesCappedRefusalIsNotTheGateCode`; mutation rows
`unlisted-commit-files-die-as-a-raw-api-error`,
`commit-files-status-flattened`, `unread-listing-remedy-is-a-rerun`).
That escape is the act the t-c6r5 sentence above calls the harm, and it is
offered knowingly: whoever cuts the tag settles the commit's version impact
on each line by hand, because glyph never reads its files. What t-c6r5
rejected was the escape handed down as the gate code — exit 3, blaming the
message, over a conclusion the cap had made uncertain; here it comes at 4,
beside a walk that says it is incomplete, as the one remedy left when no input
glyph can read supplies the files.
Attribution is per commit and never per pull.
`GET /pulls/{N}/files` — one call, the pull's net diff — was rejected twice
over: a pull touching two packages with a `^` in one and a `~` in the other
would bump both lines by the higher sigil, which discards exactly the
per-commit typing §1 exists for; and the net diff is not the sum of the
commits (a change made and undone inside the pull is absent from it), so it
cannot even attribute the commits it would replace. The version follows the
commits, not the diff — the stance the single line has always taken. Merge
commits are attributed to nothing and their diff is never asked for: under
the presets a skip pattern already drops them, and a merge commit some other
pattern claims is judged on its scope and sigil alone (rules 2–3), the same
as any commit whose diff touches no package — with a root package declared
too, and its refusal says it is a merge commit's (above). A file is asked
about under
both names of a rename (`git diff-tree --no-renames`; the API's
`previous_filename`), which is what rule 1's "a rename across two modules
moves both lines" needs — with detection on, the line the file left would
never hear of it.

**The walk is one walk.** Each package's range is `<its base>..HEAD`, its
base resolved exactly as §4 resolves the single line's — the highest parseable
tag carrying the package's prefix that HEAD contains (`latestVersionTag` per
prefix; a `curry/` tag never baselines `haiku`, mutation row
`packages-tag-of-one-line-baselines-another.patch`), else no tag and the
whole history. The walk runs once over the **union** of those ranges (the
range from the bases' common ancestor to `HEAD`, which contains the union),
resolving each merge point once, and the range question becomes *in which
lines* a sha is unreleased rather than a boolean (each line's own
`base..HEAD` set, read from local git, free); a listed commit **joins**
package p's line when it is placed on p **and** its governing on-branch
commit (its landing site, else its pull's merge point) is unreleased on p's
line. A commit the union holds that is released on every line — possible
where the common ancestor sits before both bases — is walked, because the
union had to contain it, and dropped with a notice. N walks over overlapping
ranges would resolve the same pulls N times for the same answers, and were
rejected on cost alone. The whole-history cap applies to
the union walk as it does today, and its remedy gains a package form the
error names: a package with no tag of its own — the common case of a package
added to an old repository — is baselined by cutting **`<path>/v0.0.0` at the
commit before its first change**, a tag that says "nothing of this line was
released before here" and steps to `<path>/v0.1.0` on the first `^`.

**Only the untagged arm is capped**, and the tagged arm's missing cap is a
decision (t-n5tw R3). A tagged range is the unreleased work a release exists
to read, so it is walked whatever its length. That holds for the single line's
range (measured 2026-09-29 on a stand-in API: 300 commits past the tag, 300
lookups, exit 0) and for the union's, whose length is the **oldest** base's
range. So one line whose last tag is old prices every bare walk, and every
`release`, by everything behind that tag: a lookup per merge point, a listing
per resolved pull, and under packages a file read per squash-arm inner commit
placed by its files — about two requests per commit where the commits are
squash-merged pulls. Measured the same day: on the stand-in API a line tagged
251 commits back took a bare `bump --since-tag` to 251 lookups for one
commit on any line, and google-cloud-go's union over the lines a 2026-09-13
survey declared started at one line's `auth/oauth2adapt/v0.2.8`, cut
2025-03-20 — 1,843 commits to main as it stood on 2026-09-13 (`git rev-list
--count`, re-run 2026-09-29; 1,888 to main as of 2026-09-29), 1,797 of them
pull-shaped, some 3,600 requests by that structure before any file read (an
estimate).

A cap there refuses releases with no honest remedy, because a tagged line's
base moves only by releasing it. The saving on offer — skipping a pull whose
landed diff misses a line — is closed twice over: the net diff is not the sum
of the commits (the pull-level attribution rejected above), and rule 2 lets a
shared-only inner commit's scope name a line its diff never touches, so no
local prefilter can prove a pull quiet. So the price is named, not capped.
Past the token's hourly budget the walk exits 4 on the rate limit, retried and
then surfaced (`github.retryable`), never answered short. A tag that names a
line walks that line's range alone (1 lookup for the fresh line beside the
251, measured the same day), and `preview` resolves over the touched lines
only (t-60dc, below).

The fleet's exposure, measured 2026-09-29: the longest tagged walk is
dotfiles', 192 commits past v0.2.0 (cut 2026-07-20) — 188 lookups once its 4
dependabot commits are excluded, plus up to 148 listings for its pull-shaped
subjects, about 340 requests per release run (an estimate from local git).
That job runs on `github.token`, and every preview on a dotfiles pull walks the
same range for its pending side, all against the repository's 1,000 requests
an hour (the `GITHUB_TOKEN` limit GitHub documents): about three verdict runs
in one hour reach it. It has not been hit — dotfiles' last eight release runs
and six preview runs are green, and canon's latest failed release (run
36527828384) was a 403 on a `PATCH`, not the rate limit — but it grows, 37
commits in the last 30 days. A line whose draft is never published adds every
commit to every walk, which is t-354v's untagged-arm cost, delayed; the remedy
is publishing. glyph-monorepo-test's union is 23 of its 42 commits.

No new flag: `--since-tag=TAG` and `below:TAG` keep their meanings and gain
one reading — **a tag names a line.** A prefixed TAG selects that package alone
(the verdict, the notes and the draft are that line's, and the other lines are
not converged), which is what tag-time note rendering needs (`goreleaser.yml`
already runs `notes --since-tag=below:TAG` from the tagged commit); a bare
`--since-tag` walks every line; a tag on a line no `[[packages]]` entry
declares — `fish/v1.0.0`, or a bare `v1.0.0` with no root package — is usage
(exit 2), never a walk of some other line; a tag that is not version-shaped on
any line names no line, so every line walks from it and steps from its own
highest tag, as the single line does. **Version-shaped** is `ParseBaseVersion`'s
question, the one `below:` has always asked of its bound: a release candidate
or a build-metadata tag on a declared line (`haiku/v3.0.0-rc.1`) names that
line, and the line steps from its highest *plain* tag — a candidate is a
question, never an answer (t-s5n4) — exactly as the single line falls back for
a tag that names no base. The first cut asked the plain form `ParseVersion`'s
question instead, so `haiku/v3.0.0-rc.1` was "not a version on any line": every
line of the live-fire harness walked `haiku/v3.0.0-rc.1..HEAD`, each sibling
re-folding what it had released and stepping past its own highest tag with
nothing on stderr — the accident the per-line base exists to prevent, reached
through the flag — and `fish/v1.0.0-rc.1` died in git at exit 4 instead of at
the usage guard (t-gt9n, measured 2026-09-11; mutation row
`packages-candidate-tag-walks-every-line`). `--current` is accepted only when one
line is selected, refused at exit 2 otherwise: with two lines it would name a
version for a verdict that has two. `--pr` on `bump` and `notes` is refused
under packages for the reason the lint paragraph gives — a pull's listing
carries messages and no files — and `--range` answers per line from local
git, every commit unreleased on every line (a `--range` fold names no
release base) and each line stepping from its own highest tag.

**The fold, the step, the exit.** `FoldSigils` runs once per package over
the commits that joined that package's line, so §3 is unchanged per line: max-fold,
`%` promotes that line, the 0.x rule reads that line's current version, and a
commit no pattern claims still refuses the whole walk (it is refused before
attribution, so a wrong grammar cannot be hidden by a wrong tree). The
published floor is per prefix. Exit `1` (no release) is answered **only when
every line folds to none**; one line moving is a release. No new integer:
the exit-code contract is frozen (§5), and every mixed outcome is legible in
the machine verdict, which gains `packages: [{path, current, level, next,
action, commits, reason}]` while the scalar `current` / `level` / `next` /
`action` stay what they are for the single line and are **empty when
packages are declared** — a repository with packages has no one line for them
to describe, and a consumer that reads only the scalars is exactly the
consumer that must not act (mutation row
`packages-scalar-verdict-describes-one-line.patch`). The top-level `commits`
then lists every **participating** commit — a shared-only `=` among them, in
no line but read — and `notes`' verdict mirrors the shape:
`packages: [{path, sections}]` with the top-level `sections` empty. On
stdout, `bump` prints the next **tag** of every line that moves, one per
line in config order (`haiku/v0.2.0` — the prefix is what a tag step needs,
and the single line's bare `vX.Y.Z` is the root package's spelling of the
same thing); `notes` prints one line's body bare when a tag selects it and,
over several lines, each body under a `# <path>` heading, the sections nested below it.

**Participating** means one set, wherever the word stands (t-rrw0, t-mfny;
ratified 2026-09-29). A commit *participates* when the fold reads it: not an
`exclude_authors` author, matched, not claimed by a skip pattern — exactly the
rows `FoldSigils` returns, on the single line and under packages alike. Under
the presets that leaves out the bots and the merge commits. A `fixup!`,
`squash!` or `amend!` is not left out but refused (§2: an unlandable claim
reads as unmatched, and an unmatched message refuses the fold wherever one
runs), so no count ever holds one. A commit *joins* package p's line when it
is placed on p and unreleased on p's line (above), and it *participates in*
p's line when it does both: it participates, and it joins. So a shared-only
`=` participates, in no line; an `exclude_authors` commit joins the lines its
files touch — which is how it reaches their notes — and participates in none;
and `packages[].commits` is the commits participating in that line, the
top-level `commits` every participating commit, each once.

The definition had to be written down because the word had come to name three
sets. The sentence above said the top-level `commits` list every commit that
"participates on any line" of a set holding the shared-only `=` rule 3 said
"participates nowhere", and the counts printed beside the word followed three
readings. Every sentence that counts participating commits now counts the one
set, each sha once. `bump`'s and `release`'s no-release reasons and the single
line's preview footer always did. The preview's footer under packages did
not, on either body — measured on b42ca92, each listing previewed on a single
line too (`TestPreviewPackagesCountsWhatTheFoldReads`): the body of a pull
touching no line counted the raw listing, 3 for a pull of a shared `=`, a bot
and a merge commit where the single line says 1; the per-line footer counted
each table's distinct sigil-and-subject pairs, 2 for a pull the single line
counts 4, under a haiku table of three rows — two commits sharing a subject
were one (a row carries no sha; t-rrw0 (1)), and the shared `=` was in no
table. Both bodies are handed the count now (`preview.Input.Participating`,
the rows of the one fold of the whole listing), and say what follows from it
(Preview, below). Mutation rows `preview-packages-counts-the-raw-listing`,
`preview-footer-counts-the-tables-distinct-subjects`.

`notes`' no-release reason did not either: "N commit(s) participate" in
`bump`'s words, over the listing `notes` was handed. Measured on b42ca92
(`TestNotesNoReleaseCountsParticipatingCommits`, which asks `bump` over the
same input and holds `notes` to its answer): over one single-line range of
two `=`, a bot and a merge commit, `bump` said 2 and `notes` 4; beside a
message no pattern claims, `notes` said 2 of a range the fold refuses at 3;
over a `--since-tag` walk of one `=` and a bot, 2 where `bump` says 1; and
under packages 4 where `bump`'s top-level `commits` lists 1. `notes` renders
excluded authors and unmatched messages, so it has no fold of its own to
count. It asks the fold, one commit at a time (`participating`,
`internal/cli/range.go`): the three conditions restated there would be a
second copy free to drift from the rows `bump` publishes, and over the whole
listing the fold refuses at the first unmatched message, where `notes` still
owes a count of the rest. The reason is prose: `goreleaser.yml` branches on
`notes`' exit code and reads no reason.
Mutation rows `notes-reason-counts-the-raw-listing`,
`notes-packages-reason-counts-the-raw-walk`.

The walk's `pulls[].commits` is not such a count and was never meant as one:
it is how many of a pull's listed commits the walk took in, bots and skipped
merge commits included (§4; measured 2026-10-05 with `release --dry-run
--json`, a squash-merged pull listing one `~`, a bot and a merge commit
reports `"commits":3` beside the one row of the verdict's `commits`).

**Drafts, one per line.** Convergence runs `draftplan` once per package over
the drafts carrying that package's prefix, so the founding invariants hold
per line: never a second draft on a line, retagged in place, everything glyph
did not manage untouched. The placeholder `draft_on_none` maintains becomes
`<path>/Unreleased` — still not house-shaped, still publishable into nothing
that wedges a floor. Two drafts glyph *did* write are claimed and converged
away, on the precedent of the placeholder being claimed with the flag off: a
bare `vX.Y.Z` draft in a repository that declares packages but no root package
is the single line's residue and is deleted on the first packages run. A hand
region it carried goes with it, and the migration is the one moment to move
that prose, so `release --dry-run` is how to learn it in time: its notice
names the residue and says it would be deleted, and a real run's speaks only
once the `DELETE` went. The first cut printed one notice at plan time, above
the dry-run fork, so a dry run that wrote nothing and a run that died at an
upsert with the residue untouched both said the draft "is deleted" (t-xz1z;
`TestReleasePackagesBareResidueNoticeWaitsForTheDelete`, mutation row
`release-packages-residue-notice-speaks-before-the-delete.patch`). With a root
package declared it is that package's draft and simply converges. The write
order is §4's write-first, extended: every line's upsert lands before any
draft is deleted, so a write that fails on the second line leaves the first
line's notes standing and exits 4 — the next run heals it. The deletes after
the upserts are of two kinds, and §4's two severities go with them per line. A
line that folds to none with `draft_on_none` off has one action, deleting its
residual drafts, so a delete that will not go fails the run (4) exactly as the
single line's none verdict does, whatever its siblings wrote; the strays
beside a draft a line wrote are bookkeeping after that write and stay a
warning. The bare residue is a stray when any line wrote a draft and the whole
action when none did (mutation row
`release-packages-residue-absorbed-when-no-line-drafts.patch`). The first cut
sent every delete through the lenient pass as soon as one line had written, so
the same line, verdict and failing `DELETE` exited 4 when every line was none
and 0 beside a moving sibling, the verdict reporting `delete` over a draft
still standing (t-xz1z;
`TestReleasePackagesNoneDeleteFailureStillFailsLoud` fails on that source, and
mutation row `release-packages-none-lines-absorb-a-failed-delete-too.patch`
restores it). A residual that will not go is answered only once every other
delete has been tried — another line's residual, the strays, the bare residue:
one line's failed action is no reason to leave another line's undone or a
stray standing unwarned. Returning at the failure was tried and rejected for
exactly that: beside a moving line it deleted neither that line's stray nor
the residue, both of which the first cut deleted (measured 2026-10-04 with the
`DELETE` of curry's residual answered 422 — returning sent `PATCH 53,
DELETE 51` and exited 4, the first cut `PATCH 53, DELETE 52, DELETE 51,
DELETE 61` and 0, the run now `PATCH 53, DELETE 51, DELETE 52, DELETE 61` and
4). The residuals still go before the strays, write-first one step on: they are
the verdict of the lines that fold to none and the strays are bookkeeping, so
a run that an interrupt cuts short — an interrupt is never absorbed — has
spent itself on the verdict first
(`TestReleasePackagesFailedResidualStrandsNoOtherDelete`; mutation rows
`release-packages-failed-residual-strands-the-other-deletes.patch` and
`release-packages-strays-go-before-the-residuals.patch`).
`--footer-file` appends to every draft (one install block per repository is
what every caller passes today; a per-package footer is a knob nobody has
asked for and is recorded here so its absence is a decision).
The compare link (§4) is per line the same way: each line's draft, and each
line's body under `notes --since-tag`, links `compare/<that line's base
tag>...<end>` (the draft's target; HEAD under `notes`) — the tag the line's own
range starts from (`haiku/v0.1.0`), never the union's merge base and never a
sibling's tag — and a line whose walk has no tag base, or whose placeholder has
no notes, carries none, as no line does under a `--target` that is no full sha
(§4). A typed `--since-tag` that names no line is every line's
walk base, so it is every line's link base exactly when it is a tag. The link is
the line's range, not its attribution: GitHub's compare filters by no path, so
`curry/v1.1.0...<target>` lists every commit and file between the two points
(measured 2026-09-29 on glyph-monorepo-test by the t-v7f7 ruling: 23 commits,
42 files across the lines), which is exactly what `<base>..HEAD` is before
attribution partitions it, and the shape google-cloud-go publishes for every
package release (`compare/bigtable/v1.57.0...bigtable/v1.58.0`, 6 of 6 sampled
the same day). A prefixed ref resolves, a nested line's too:
`travel/onsen/v2.0.0...<sha>` answered 200 over the API and on the web (the
ruling's review, 2026-09-29). No link under packages was the alternative, and
nothing argues for a line's draft being the one glyph body without its range's
address (`TestReleasePackagesCompareLinkPerLine`,
`TestNotesPackagesCompareLinkPerLine`, `TestPackagesCompareLinkNeedsATagBase`;
mutation row `compare-link-cites-another-lines-base`).
`checkReleaseBody` sizes each draft on its own. Of the scalars that describe a
draft — `tag`, `target`, `body`, `url` — `target` is the one packages leave
filled (one checkout, one HEAD, every line's draft points at it), and it keeps
the single line's rule rather than acquiring one of its own: it is present
exactly when a draft is upserted (on a dry run, would be), a line's
`<path>/Unreleased` placeholder included, and absent when no line has a draft
to write, as the single line's none verdict without the placeholder carries
none. Absent is also the fail-safe answer the scalars give: a consumer written
for the single line that reads `.target` alone must not act on a run that
drafts nothing. The first cut resolved it above that question and reported a
sha no draft would ever point at, beside a `packages[]` in which no line
carried a tag (t-xz1z; measured 2026-09-29 on an all-none fixture, the real
run and the dry run, where `TestReleasePackagesAllNoneExitsOne` fails on that
source, and on a selected line folding to none,
`release --dry-run --json --since-tag=curry/v0.1.0` over a haiku-only `^`). No
workflow or script in the fleet reads `.target` (the 55 local clones, the hub
and glyph's own reusables grepped the same day), so dropping it moved no
consumer. The gate is the draft count, never the moving-line count:
`draft_on_none`'s placeholders are drafts, and they point at the target too
(mutation rows `release-packages-target-without-a-draft.patch` and
`release-packages-placeholder-loses-its-target.patch`). GitHub's **Latest**
badge is one per repository and is assigned when a human *publishes*, by
creation date unless the publisher says otherwise; glyph writes drafts, which
cannot be latest, so it never sets `make_latest` and the badge lands on
whichever line was published last — the shape google-cloud-go's releases page
has lived with for years. Not a knob.
**Measured** 2026-09-10 on glyph-monorepo-test: `releases/latest` was 404
with two drafts standing, `haiku/v0.1.0` after haiku was published,
`curry/v0.0.1` after curry was — the last publish, whichever line. A tag
that selects one line converges that line **alone**: the other lines'
drafts are not that run's to touch (goreleaser's tag-time run must not
rewrite a sibling's draft from a range it did not ask about). The walk base
is asked of the **checkout's** tags and the published floor of the releases
listing, so a release run on a checkout that has not fetched a sibling line's
freshly published tag refuses at the floor (exit 4, measured) rather than
re-drafting a version that is already out — the same fail-loud the single
line has, and the reason `release.yml` checks out with tags.

**Preview.** `preview --pr` renders one verdict per package the pull's commits
are attributed to, in config order, each with that line's current version and
next; a package the pull does not touch is not mentioned, and a pull whose
commits carry nothing (`=` everywhere, or shared-only `=`) says it moves
nothing, as today. `pr-verdict.yml` renders what the binary hands it; its one
packages change is `breaking` (below). The pull's commits exist on its branch only, so their files
come from `GET /commits/{sha}` — one request per commit placed by its files, the
squash arm's price paid before the merge — and a commit attribution refuses
is refused here at exit 3, the same lint-class answer the walk will give
once it is merged, while the branch can still be fixed. Over a listing GitHub
truncated, or one a 422 cut short (above), the refusal is withheld exactly as
the walk withholds it, the commit is attributed to no line, and the body
carries a PR-side INCOMPLETE caveat beside the pending one — a line a commit
touches in files GitHub did not list may be missing from every figure, and
this comment is read by someone who never opens the log
(`preview.Input.PRShort`). The caveat's own sentence names no cause, because
the shortfall it quotes does: it said "only past the cap" of a 422 until
t-esm5. When no line is touched, the "moves nothing" sentence claims no
declared package only in the files GitHub listed, and the caveat makes a floor
of that sentence rather than of figures the body does not carry. That
sentence carries **no count**: "no commit participating in it touches a
declared package". It read "its N commit(s) touch no declared package" with N
the raw listing — said, then, of a skipped merge commit whose diff nothing
reads — and with the participating count in N's place it would read "its 1
commit(s)" in a pull of three and "its 0 commit(s)" in a pull of two, a count
that reads as a miscount of the pull. The count is the footer's alone
(`preview.RenderNoLine`, the body moved beside the others so its prose is
unit-tested with theirs; `TestRenderNoLine`, mutation row
`preview-moves-nothing-counts-the-pulls-commits`). The pending
side is the one walk, run when any touched line has a release tag (the
release-floor guard per line), and its RANGE is resolved over the TOUCHED
lines alone —
attribution still runs over every declared line, because a nested package
must keep taking its files out of its parent, but a line the pull does not
touch never decides how far back the walk reaches. Resolved over every
declared line instead, one line with no tag took the union to the whole
history: past the cap that refused the whole command for a pull touching only
released lines, and under it one API round-trip per commit of the history for
a line nobody asked about (t-60dc, measured 2026-09-15 — exit 4 for a
haiku-only pull in a 211-commit fixture, and 9 round-trips where the touched
line's own range held 1). A touched line with no tag reports its PR verdict
alone **only when its pending side was not walked**; when a tagged sibling in
the same pull takes the walk to the whole history, that line's pending IS
computed, and the body then reports it exactly as the machine verdict does.
The earlier rule here said the untagged line always reports its PR verdict
alone, and that was wrong in the one case it mattered: the same run answered
`curry/v0.0.1` in prose and `v0.1.0` in `packages[]`, and `bump` on the same
checkout answers `curry/v0.1.0` — preview predicts the walk, so the prose is
the half that has to move. "No tag" and "pending uncomputed" are therefore two
states, not one flag. The body is the single line's sentences per line: the
marker, one headline per touched line led by the line's name with versions
spelled as tags (`haiku/v0.1.0 → haiku/v0.2.0`, so two lines can never be
confused), one commit table per line (a commit moving two lines sits in
both), the notes preview under the notes' own
`# <path>` headings, and the incomplete-walk warning once — one walk read
every line. The footer counts every participating commit once ("The fold,
the step, the exit", above) and, in a sentence of its own, says how many of
them sit on no line — `1 of them sits on no line.` — because a shared-only
`=`, or a commit whose refusal was withheld, is in no table, and a reader who
counts the rows would otherwise find fewer than the footer claims. The
sentence follows the one that names the bases: spliced in ahead of
"squash-safe, a squash-merge cannot erase them", that clause would read as
said of the commits on no line alone (`TestRenderPackagesSaysHowManySitOnNoLine`,
`TestRenderPackagesCountsTwoCommitsWithOneSubjectAsTwo`; mutation row
`preview-footer-hides-the-commits-on-no-line`). The machine verdict gains `packages: [{path, current, untagged,
level, next, pr, pending}]` with **every scalar at its zero value** — the
strings empty, `untagged` false, whatever the pull touches — so
`pr-verdict.yml`'s `level` output is `""` — not computed — exactly as its
callers already read it, and `breaking`, `level`'s readable alias, follows it
into `""`. Both halves were first cut wrong in the same direction (t-xbk0,
measured 2026-09-26 with the reusable's own step and a real binary on
glyph-monorepo-test #30, where haiku folds major): the envelope's `pr` and
`pending` read `none` — a claim that the pull moves nothing, beside a
`packages[]` whose haiku line moves major; #219's class, a scalar answering
where it should say not computed — and the reusable derived `breaking` as
`[ "$level" = "major" ]` over the empty level and published `false`, the
definite "no" §3 names as the failure that rejected a fifth `Level` word. An
any-line-major `breaking` was rejected with it: it is a scalar describing
lines, and a consumer written for the single line reads it as the one line's
answer, which is the reading every scalar here exists to refuse (mutation
rows `preview-packages-scalar-claims-the-pull-moves-nothing`,
`pr-verdict-breaking-reads-not-computed-as-false`).

**Lint.** Attribution needs files, so it belongs to the inputs that have them:
`lint --range` (local git) applies rules 2–3 and the contradiction check when
packages are declared, and `hook pre-push` inherits it, which is where a
shared-only `^` is caught before it is pushed. `--message`, `--stdin` and
`--pr` judge a message alone, as today: the commit-msg hook cannot see a diff
that is not yet a commit, and a pull's title is not attributed to anything.
Here the commit-msg hook's verdict is weaker than CI's by construction —
beside the gaps §2.1 names, an `unlandable` pattern it argues and the
per-commit overrides it does not claim fixed — and it is stated here rather
than left to be discovered: the pre-push hook closes it on the same machine,
one step later. Every `--range` read — `lint`'s, `bump`'s and `notes`',
through one function, `logRange` — asks once whether the checkout is
**shallow** and warns when it is: git lists only the commits a shallow clone
holds, so a range reaching past its boundary is read in part. Measured on
`--depth 2` clones: lint judged 2 of 6 commits and exited 0 with nothing said
where the full clone exits 3 (t-h7w2's refutation run, 2026-09-27), and bump
printed a version and notes rendered 2 of 5 commits, both at 0 in silence,
where the full clone's bump refuses at 3 — under `[[packages]]` only the
boundary commit was named, never the range (2026-10-04;
`TestLintRangeOnAShallowCheckoutSaysSo`,
`TestBumpAndNotesRangeOnAShallowCheckoutSaySo`, mutation rows
`lint-range-is-silent-on-a-shallow-checkout.patch`,
`lint-range-refuses-a-shallow-checkout.patch`, and one per reader:
`bump-range-is-silent-on-a-shallow-checkout.patch`,
`notes-range-is-silent-on-a-shallow-checkout.patch`,
`packages-range-is-silent-on-a-shallow-checkout.patch`). Each warns and keeps its
verdict about what it read instead of refusing: a refusal would be a new
lint semantics, and the walk already gives a shallow checkout to the
reporting commands as a warning and to `release` alone as exit 4 (§4, §7).

**Doctor** gains three checks: every declared `path` is a directory HEAD
records (`package-paths-exist`, shipped — a path with no subtree claims no
file, so a typo silently moves the verdict: fail; unknown while the file
itself has not loaded, since its packages were never read, and unknown when
git cannot list HEAD's trees). It asks git, byte for byte, and never the
filesystem, because attribution matches git's path strings: on APFS a
case-different `Haiku` and a symlink `currylink` (a `120000` blob to git)
both opened as directories and passed, while `lint --range` under the same
config refused a commit under `haiku/` at `3` (t-fdd8, measured 2026-09-11
on glyph-monorepo-test; re-measured 2026-10-05 — the stat-based check passed
both in `TestPackagePathsAskGitNotTheFilesystem`, and
`TestDoctorPackagePathsAgreeWithAttribution` holds the two answers together;
mutation row `doctor-package-paths-ask-the-filesystem`). A submodule's gitlink
counts as such a directory, on purpose: attribution's `owner` matches a file
equal to a package path as well as one under it, so a path naming a submodule
claims that submodule's bumps (`TestHeadTreesListsWhatHEADRecordsAsADirectory`).
`internal/cli` lists the trees
(`gitsource.HeadTrees`) beside the hooks directory, as every doctor
subprocess is; `name`s are
unique and each is a word the file's scope grammar can spell, and every
`path` can prefix a tag git can create (load errors all three, so
`glyph-toml-loads` already carries each with the loader's own remedy — no
second check repeats them; before the path rule `package-paths-exist` passed
`path = "a b"` green, a line no tag could ever be cut on); and a bare `v*`
tag exists while no root package is declared (`root-line-tags`, shipped) —
advice, not a defect: those tags baseline nothing now, and the note says
which package declaration would adopt them.

**The reusables.** `release.yml` gains a `packages` output (the JSON array
above, as a string) and keeps its four scalars with the empty-in-packages-mode
rule, so a caller written for the single line fails safe on `""` exactly as
its contract already tells it to. The array goes through minus each line's
`body` and `url`: the body already lives in the draft and N of them could
meet the 1 MB output cap, and the url is withheld per line for the reason the
single line's url is withheld (the handle makes auto-publish a two-line
caller step; publishing stays human). Its artifact inputs (`app` / `binary`)
describe one artifact for one draft, and are refused when the repository
declares packages: a monorepo attaches per line in its own job, reading
`packages`. The refusal is asked of the verdict envelope — after the drafts
are upserted, before the build — rather than of the input-validation step,
because the declaration lives in the caller's `glyph.toml`, which that step
has not checked out and which glyph alone reads (a grep for `[[packages]]` in
the workflow would fork the grammar, the defect class the outputs exist to
avoid); the drafts written first are correct, and the misconfigured run stays
red until the input is dropped. `lint.yml` is unchanged, and
`pr-verdict.yml`'s one change is `breaking` following `level` into `""`
(Preview, above). The rollout is the runbook's: the binary change is additive, so
`fleet-preflight` must report zero verdict moves and zero body re-renders on
every repository without `[[packages]]`, and the live fire is
`glyph-monorepo-test` (created 2026-09-10 with two Go modules, `haiku/` and
`curry/`; five declared lines today, `travel/onsen` nested in `travel`), whose
defining probe is a pull that touches both modules with a
`^` in one and a `~` in the other and must move the two lines differently.

**Architecture.** The attribution rule is pure — files, scope, config in;
package set or refusal out — and gets a package of its own beside `draftplan`
and `preview` (§5's tree is updated by the task that creates it, because
`TestDesignTreeNamesEveryInternalPackage` reconciles the tree with the
filesystem and a row for a package that does not exist yet fails it). The
`Commit` struct §5 sketches gains nothing: attribution consumes files the
walk fetches beside the listing, and the fold reads `SigilCommit` as today.

**Rejected, in one place**, so the next reader does not re-argue them:
intent files (changesets — the signal leaves the commits, README says why);
pull-level attribution (above); a `tag_prefix` knob (above); an `all` scope or
a shared-moves-all arm (above); per-package `glyph.toml` (above); N walks
(above); a `--path` flag selecting a package on the command line instead of
the file (a verdict must be readable from `glyph.toml` alone, §2); cross-package
dependency cascades (multi-semantic-release bumps a dependent when its
dependency moves — glyph reads no manifest and would have to start, and a
consumer that wants the cascade expresses it by touching the dependent, which
is a commit the rules already carry); the root package carrying a commit with
no files (above); an undeclared-module state between a declared package's
files and nobody's (above); containment for the contradiction check, and
refusing a `=` whose scope names another package (above).

## 5. Architecture (Go, house pattern)

Binary `glyph`, module `github.com/akira-toriyama/glyph/v5` — the major suffix is
part of the path from v2 on, and a tag alone does not supply it: without it the
proxy answers `go install …@latest` with the last unsuffixed version (v1.0.0)
forever. Cutting v6 means editing this path. Subcommands: `lint`,
`init`, `bump`, `notes`, `preview`, `release`, `doctor`, `hook`, `version`, `emoji` —
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
internal/config          glyph.toml loader — user RE2 patterns, first match wins, semver_sigil extraction, the [[packages]] schema; embeds the init presets
internal/attribution     which declared package(s) a commit moves — pure; files + scope + sigil + packages in, package set or lint-class refusal out (§4.1)
internal/emoji           the gemoji dictionary `glyph emoji` prints — embedded table.json, advisory data nothing else reads (§2)
internal/draftplan       draft convergence — pure; which draft a verdict keeps, retags or deletes (the Unreleased placeholder lives here)
internal/markdown        Line: per-field flatten, then the prose escape and the mention fence over the assembled line
internal/notes           group by section; note.line rendered by hand over config.LineSpan / LinePart (the span grammar is parsed in internal/config; the optional-span drop rule lives here)
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

- `internal/markdown` — one owner for the escaping ORDER (flatten per field,
  then escape and fence over the assembled line), because both renderers,
  `notes` and `preview`, have to run it the same way round and a copy in each is
  a copy that drifts.
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

The converse is the part that had to be repaired rather than merely written
down (t-c6r5): on a verdict command the config is the **yardstick**, so a
`glyph.toml` that will not load is never `3` there — no commit was judged at
all. Unreadable (permission, EISDIR) is `4`, the code `doctor` and the hook
installer already gave that same event; unparseable is `2`, the code a
**missing** config already carries, since both leave a human editing a file.
Two mechanisms depend on it and both failed silently while the loader answered
`3`: the installed commit-msg and pre-push hooks block on `3` alone, so a typo
in `glyph.toml` rejected the very commit that would repair it, and `lint.yml`'s
default-branch push arm swallows `3` alone, so an I/O failure returned a green
gate having judged nothing.

One command sits deliberately off the `1` rung: for `preview`, `none` is a real
answer to the question asked — *what would merging this do?* — so a none verdict
exits `0` there. `core.CodeNoRelease` is constructed in `cmd_bump.go`,
`cmd_notes.go`, `cmd_release.go` and `release_lines.go` (the packages release,
§4.1: `1` only when every line folds to none), and nowhere else. Outside Go the integers
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

**One channel, sieved** (t-sa7p). The obvious cure for a sieve copied into
every consumer — a native hand-off: a root `--error-file <path>` that
`renderError` also writes, the envelope on fd 3, a path in the environment —
was weighed and rejected, because it could only ever be added, never swapped
in. The stderr copy is permanent on its own account: both installed hooks hand
glyph's stderr straight to the committing developer (`internal/hook`), and when
`lint --stdin` refuses a message the envelope is the only diagnostic that
reaches them (measured). And a consumer whose one probe is fired at more than
one revision — side by side in the preflight, one dispatch per ref in the
live-fire harnesses of glyph-test and glyph-monorepo-test, which keep older
releases as controls — can speak only the channel every such revision shares.
No revision has ever carried the flag (`git log --all -S error-file -- '*.go'`
finds no commit): each refuses it as unknown at exit `2`, and the sieve
recovers exactly one envelope from each (measured 2026-09-29 on v0.12.0,
v2.0.0, v3.0.0-rc.3 and v4.2.0; the sieve has been the contract since
v0.11.1). So the preflight would file every lint gate as unanswered on the
baseline side of the release that introduced the flag, the harnesses' sharp
controls would go red for the flag instead of for the defect each exists to
name, and the sieve would outlive the flag in every harness — two machine
channels for one document. The quieter shapes fail worse: a binary with no
fd-3 or environment surface ignores it, exit code unchanged and the file empty
or never created (measured), which is t-sws7's silent loss of the `::error::`
heading, reached by version skew instead of by `jq`; moving the annotations
off stderr meets the same skew, with stdout already taken by the payload.

Against that, the sieve has no failing input. glyph's own code writes stderr
in four places — `warnf`, `errorf`, `noticef`, and `renderError`, whose one
caller is `finish` — and the first three prefix `::` and fold through
`oneLine`; git's stderr lands in a buffer and the doctor's hook probe's in the
null device. Short of a crash, the one other writer is cobra's hidden
`__complete` command — its directive, and a `[Debug] [Error]` line when the
command line being completed names a flag it cannot parse — which prints at
exit `0` and never beside an envelope (measured). A subject, a `warn` string
and an `unlandable` reason each spelled `{"error":{"code":0,…}}` (the last two
across embedded newlines) still leave one `{`-opening line, the envelope's
(measured). The reusables' half is `internal/workflows`'
`TestReusablesSieveTheEnvelopeBeforeJQ`, and mutation row
`lint-summary-jq-reads-the-unsieved-stream` re-breaks it the way t-sws7
shipped it: `jq` over the annotated stream exits 5 behind the step's
`>/dev/null 2>&1`, and every convention failure the range step reports loses
its summary heading. The preflight's two sieves sit outside that guard — they
take the stream through a positional parameter and a captured variable, which
its sink rule cannot follow; pointed at the script with both sieves deleted,
it reports neither (measured). The preflight answers for `probe_lint`'s
itself: two lint signatures it cannot read are an unanswered gate on its ✓
line, never agreement. They used to compare equal — hiding a move exactly as
the exit-code comparison #103 replaced had — and a machine without `jq`
produced them with no edit at all (measured 2026-09-29: one finding against
two, reported as no move). Nothing answers for `why`'s: it only words the
reasons on skip and lost-answer lines, and broken, every reason whose stream
carries an annotation ahead of the envelope reads `no error envelope`, the ✓
line and the exit code unchanged (measured 2026-10-04). The script refuses to
start without `jq` for the gate that has no such answer: without it both sides
of every release body read as empty, so a re-render reports as none under an
answered body gate (measured 2026-10-04, the refusal removed).

**Repository resolution** (`resolveRepo`, one function for every API-side
command — `lint --pr`, `bump`/`notes` `--pr`/`--since-tag`, `preview`,
`release`, `doctor`): an explicit `--repo` wins, else `$GITHUB_REPOSITORY`,
else the clone's `origin` remote (t-ygmv, mutation row
`origin-fallback-ignores-the-host.patch`). The environment sits above origin
because in Actions the variable is the authority and origin is whatever
`actions/checkout` wrote; outside Actions the variable is unset, so origin
answers only where nobody else did — measured before the fallback: `glyph
doctor` inside a clone exited 2 asking for `--repo`, the one input the clone
already held. Only the remote named `origin` is read (a second remote is a
choice the caller makes with `--repo`), and its URL must be on the host the
API client will query — `github.com`, or `$GITHUB_API_URL`'s hostname — or the
entrance refuses at exit 2 naming both hosts: silently asking `api.github.com`
about a GitLab clone would come back as a 404 wearing the API code, telling the
caller to retry an input no retry can fix. An origin with no owner/name to give
(a local path) is the same usage error as no origin at all, never a guess.

**Machine-output flag:** one spelling, `--json`, on every command that has one —
`bump`, `notes`, `preview`, `release`, `doctor`, `version` and
`hook install` (`lint` speaks only in exit codes and the error envelope, so it
has none). It was
`--ndjson` on two of them until v1.0.0, which was wrong twice: the flag named a
format glyph has never emitted (`printCompact` writes ONE object, not a stream)
and it split the surface, so a caller had to remember which subcommand took
which — measured before the rename, `version --json` and `bump --ndjson` *both*
exited 2 with `unknown flag`. The flag is read by VALUE and not by `Changed`, so
an explicit `--json=false` selects a real output — the human line at exit `0` —
rather than nothing. Enumerated from the command tree at run time by
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
thing being modelled. `internal/cleanup` ports git's `strbuf_stripspace` and
`wt_status_locate_end` line for line, and three oracles hold the port to the
real git: `TestCutLineIsTheOneGitWrites` drives a real `git commit -v` and
asserts git still writes the exact scissors line the cut matches on,
`internal/cli`'s `TestHookVerdictMatchesWhatGitRecords` commits through a real
git and asserts the hook's verdict matches what git recorded, and
`TestHookCutMatchesGit` asserts the hook's text is git's in the 32 of its 48
cells the hook can tell apart and pins the other 16, §2.1's editor and `-v`
residuals, in the direction §2.1 states them — all with
`GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` pinned to `/dev/null` so a personal
config cannot move the answer, the
in-process git calls included (`internal/cli`'s and `internal/gitsource`'s
`TestMain`): with only the fixtures' own git commands pinned, a personal
`commit.cleanup=strip` failed the verdict oracle and `TestConfigGet`
(measured). `internal/markdown`'s rules were
measured against GitHub's own renderer (`gh api -X POST /markdown`, mode=gfm),
with the probes, their observed output and the date of the run recorded in
`markdown_test.go`; re-run them before changing a rule, because the one rule
changed from reasoning alone was wrong (an at-sign written as `&#64;` renders
back to `@` and GitHub's mention post-processor linked it anyway, long after
CommonMark had decoded the entity).

## 6. Distribution (summary)

The engine ships inside the binary; the grammar is the repository's own
`glyph.toml` (see below — v2 dissolved the embedded table). glyph ships its own reusable workflows (`lint.yml`,
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
ratified Q16 (v1 — the reasoning predates the table's removal, the stance
stands): no shadow parallel-run (a policy-honest comparison against the
type-driven git-cliff is impossible once glyph legitimately
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

An `assets` input — files a caller built elsewhere in the run, attached by the
reusable — was requested (zmk-hid-host, 2026-09-10: a Zephyr firmware build
that fits neither `app` nor `binary`) and declined; the shape for it is a
follow-up job in the caller over the `next` output, `gh release upload
"$next" … --clobber`, gated on `next != ""` and on the dry-run input. Three
reasons. The reusable upserts BEFORE it builds, on purpose: a red build leaves
the draft's asset one merge stale and its notes current, and nothing ships
without a human anyway; an input fed by a `needs:` build job inverts that — a
red build means the release job never runs, so the notes go stale with the
asset. An asset name that cites the version (`<shield>-vX.Y.Z.uf2`) exists
only after the verdict, which is exactly what `next` hands the caller and what
a pre-verdict artefact cannot carry. And the premise the request rested on —
that an unpublished draft cannot be found by tag, so a caller needs the
release id — is false in gh: the REST `releases/tags/<tag>` lookup 404s on a
draft, and gh falls back to the listing. Measured on glyph-test's `v2.0.0`
draft, 2026-09-10: upload, `--clobber` replace and `gh release delete-asset`
each resolved the draft by tag. A `release-id` output is the withheld URL by
another name and is refused for the same reason
(`TestReleaseOutputsNeverExposeTheDraftURL`).

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
repo squash-merges, that the squash subject and body policy leave a subject the
repository's patterns can classify on `main`, that a caller pins a concrete tag. When one of those drifts
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
  *every* squash, single-commit PRs included, so `main` fills with subjects the
  repository's patterns cannot classify — and §4's documented fallback (direct push, or API
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
  The scan reads the two places a glyph reference executes from, and nothing
  else: the workflow files (`.github/workflows/*.y[a]ml` — GitHub reads
  workflows from that directory alone, so a published reusable is there too)
  and every action metadata file (`action.yml` / `action.yaml`) at any path.
  GitHub runs a composite from wherever it sits — `uses: ./tools/installer` in
  this repository's own runs, `owner/repo/<path>@ref` (a root `action.yml`:
  `owner/repo@ref`) in a consumer's — and a moving ref inside a published
  action changes under every consumer at once while no consumer's doctor can
  see it: the ref is not in the consumer's tree. The scan once stopped at
  `.github/actions` because that is where the first measured miss sat; nothing
  makes an author keep a composite there either, and a checkout whose
  `tools/installer/action.yml` and root `action.yml` pinned `@main` passed
  (measured at adfc5e1 by the D2b ruling). Following the workflows' local
  `uses: ./…` references instead was rejected: it never reaches a published
  action nothing in the repository calls (the hub's `actions/*`), and the
  references it would follow include runtime self-checkouts that are not in
  the tree at all (`./.glyph-action/…`, `./.go-bite-hub/…`). The action files
  are listed by git — tracked, the tracked files of an initialized submodule
  included, plus untracked files no ignore rule excludes — resolved in
  `internal/cli` beside the hooks directory, never by walking the filesystem,
  whose "any path" includes what is not the repository: dependency checkouts
  under ignored build directories can carry action files whose pins are
  someone else's, and sill's ignored `.build` held 280,803 entries, a 2.9 s
  walk where git answered in 20 ms (the ruling's measurement, 2026-09-29). It
  takes two listings because git refuses `--recurse-submodules` beside
  `--others` ("unsupported mode"), and a submodule's composite is code the
  checkout runs — the filesystem walk caught one under `.github/actions` that a
  single listing missed (the D2b review's measurement; the
  `action-files-skip-submodules` row re-breaks it). When git cannot list them
  the check is `unknown`,
  like any unread input; a sparse-checkout entry, tracked but never put on
  disk, is unread too, while a tracked file the working tree deleted is an
  observed absence, as the workflows directory's is under a git-named root.
  The price of "any path": an action file kept as a test fixture is judged
  like a published one — which it is, since GitHub runs it for anyone who
  names its path. `TestCheckWorkflowPinsScansCompositeActions`,
  `TestActionFilesListsWhatGitCounts` and
  `TestDoctorPinScanReadsEveryActionFileGitLists` hold it; mutation rows
  `doctor-pin-scan-blind-to-composite-actions` (re-derived onto the listed
  files), `doctor-pin-scan-reads-only-github-actions`,
  `doctor-pin-scan-reads-a-deleted-action-as-unread`,
  `doctor-pin-scan-reads-a-sparse-entry-as-deleted`,
  `action-files-skip-submodules`, and `action-files-miss-the-yaml-spelling`,
  which holds the pathspec.
- **A credential that cannot write releases ⇒ advice (`token-repo-write`).**
  Only `glyph release` writes; every read command is unaffected, and doctor must
  not red the fleet's read-side wiring over a command a repository does not use.
  The check answers from the same repository read as `token-repo-read` — glyph
  never infers a scope the API did not report — and its value is timing: without
  it the 403 lands only after the release walk has already spent its API budget.
- **A caller granting less than its reusable declares ⇒ fail
  (`workflow-caller-permissions`).** The one failure class NO runtime diagnosis
  can see, glyph's included: the run dies as `startup_failure` before any job —
  no step, no exit code, nothing red in the caller's own YAML (measured,
  `.github#186`). Reading the checkout's workflow files is the only vantage
  point that works, which is doctor's. Two boundaries, both against crying wolf:
  a caller with no `permissions:` block anywhere is not judged (the repository
  default may be fine, and doctor cannot see it), and grants are unioned across
  workflow and job level (a sibling-job grant could in principle false-pass —
  the pin check's own documented trade, made for the same reason).
- **A caller omitting an input its reusable marks required ⇒ fail
  (`workflow-caller-inputs`).** The other member of the same startup-death
  class, and the quieter one: GitHub surfaces NO error anywhere for it — not
  in the run, not in the check suite, not in the API (measured, glyph-test3
  2026-08-26: three pushes, three silent `startup_failure`s). The required-set
  is a table mirroring the shipped reusables' `workflow_call.inputs`, held
  lockstep by test exactly as the permissions table is. Both caller checks read
  every workflow file, and one they cannot read leaves them `unknown`, never
  skipped: skipping it once turned `chmod 000` on a failing caller into a pass
  whose observation claimed no caller existed — the pin check's own unknown
  did not cover it, because that one answers whether a ref is concrete, not
  whether a caller starts. A defect observed in a file that was read still
  fails (`TestCallerChecksNeverPassOverAnUnreadableFile`, mutation row
  `doctor-caller-checks-skip-an-unreadable-file`).
- **Both caller checks judge a caller at the release it pins, and only where
  this binary can speak for that release — any other caller is `unknown`,
  never a verdict.** GitHub starts a caller against the reusable at the
  caller's `@ref`; the two tables mirror the reusables of the tree this binary
  was built from. They are one object only while the declarations hold still,
  and they have moved: read with the checks' own parsers at every release tag
  (33 on 2026-09-29, and confirmed with an independent YAML parser by the
  ruling's review), `lint.yml` gained `pull-requests: read` at v2.0.0 and
  `release.yml` stopped requiring `app` at v0.8.0. Judging every pin by the
  running binary's tables was wrong both ways at adfc5e1: a `lint.yml@v1.0.0`
  caller granting exactly what v1.0.0 declares failed at `3` — crying wolf, on
  nine of the 57 local checkouts measured (stale branches and worktrees, each
  granting `contents: read` at a lint tag that declares nothing more) — and a
  `release.yml@v0.4.0` caller omitting the `app` v0.4.0 requires passed, the
  silent startup death this check exists for. So each table row also names the
  newest release whose reusable declared otherwise (`After`, verified against
  glyph's own tags by `TestCallerDeclarationBoundsMatchReleasedTags` — all 34
  plain tags through v4.3.0 on 2026-10-05), and a caller is judged when it
  pins a release tag above its row's bound and — for a stamped build, which
  knows its own release — not above that release. Everything else is
  could-not-run: at or below the bound the binary knows the declarations
  differ but not what they were; above its own release it cannot know; a
  branch moves, and a sha names a commit doctor cannot map to a release
  offline (`workflow-glyph-pins` already fails both); and a call into a
  reusable this binary has no row for — one added after it was built — is
  unjudged too, never skipped (skipped, a `notes.yml@v5.0.0` caller passed as
  "no workflow calls a glyph reusable … observed — not assumed", measured by
  the review). A defect observed in any judged caller still fails, and the
  no-`permissions:`-block exemption runs before the pin rule, so a block-less
  caller at an old pin is the exemption's pass. An unreleased build — `dev`, a
  Go pseudo-version, or `build.sh`'s git-describe stamp (`v4.2.0-3-g…`) — has
  no upper end and trusts its own tree, which misjudges only a release cut
  after a declaration change the build predates. Those stamps are read with
  `bump.ParseVersion`, never `ParseBaseVersion`, which would read the describe
  stamp as its base triple v4.2.0 and cap the build there: every pin above
  v4.2.0 would be `unknown` to a tree built past it (the pseudo-version case of
  `TestCallerChecksJudgeTheCallerAtItsPin`; mutation row
  `doctor-caller-checks-cap-an-unreleased-build-at-its-base-tag`).
  This is not a latest-ness check — the pin check's stance holds: a pin
  several releases old whose reusable declares what this tree declares is
  judged exactly, offline. The price, said once: when a newer glyph moves a
  bound, a caller pinned at or below it is could-not-run for that glyph until
  its pin moves. Two alternatives lost. A per-tag history table, judging every
  pin at its own release: for every era today's bounds retire no release
  binary carries those facts either — the checks arrived at v2.1.0 and v3.1.0,
  after every bound (glyph v0.4.0 has no doctor at all; v1.0.0's has no caller
  checks) — and no live pin sits there (every live pin was v4.2.0 on
  2026-09-29, above every bound); any future bound is v4.2.0 or later, and the doctor of a pin from
  v3.1.0 on judges its own reusables exactly, so a history table would serve no
  caller. The `Fix` therefore offers "run the doctor of the release you pin"
  only for a pin at or above the check's first release (mutation row
  `doctor-caller-fix-offers-a-doctor-below-its-floor`). Rewording to "what
  this glyph declares" and degrading pins older than the binary lost too: the
  source-build wrapper doctor actually runs as reports `dev` (measured
  2026-09-29), where "older than the binary" is undefined, and a release that
  left the declarations alone would stop judging the fleet's pins for
  nothing. It is the hooks' rule seen from the other side: each artefact is
  judged against what executes with it — a hook against the binary on
  `PATH`, a caller against the reusable at its pin. The bound moves only when
  a reusable's grants or required inputs change, and the lockstep tests fail
  at exactly that commit; its own test needs glyph's tags, so `build.yml`'s
  `extras` job checks out full history to run it under
  `GLYPH_RELEASE_HISTORY=required`, and `scripts/check.sh` mirrors it as the
  `release-history` gate (a depth-1 checkout, or the mutation ledger's
  `.git`-less snapshot, skips it). Mutation rows
  `doctor-caller-checks-judge-every-pin-by-this-tree`,
  `doctor-caller-checks-trust-a-binary-older-than-the-pin`,
  `doctor-caller-checks-skip-a-reusable-they-do-not-know` and
  `doctor-caller-perms-judges-the-pin-before-the-exemption`.
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
  vocabulary, is unknown, never a verdict. So is a pass-through on a checkout
  whose `glyph.toml` does not load: the fired lint exits `2` there (`4` when
  the file cannot be read) before judging anything and the hook waves both
  through as `0`, so that `0` is the
  config's absence answering and `glyph-toml-loads` owns the finding. Failing
  it sent the reader to repair a PATH wrapper nothing had observed broken
  (`TestHookFiresDefersAPassThroughToAnUnloadedConfig`, mutation row
  `doctor-hook-fires-blames-the-path-for-an-unloaded-config`).
- **A byte-identical pre-push hook nobody fired ⇒ advice (`pre-push-hook`).**
  Only commit-msg is fired, and its answer covers pre-push because both hooks
  resolve one `PATH` — which holds while a byte-identical commit-msg hook sits
  beside the pre-push one, as the default `glyph hook install` writes them.
  Two states break it with every check green: pre-push installed alone by name,
  and a commit-msg hook deleted or replaced after install (the default install
  cannot get there by itself: it refuses a foreign commit-msg at `2` and,
  planning every kind before writing any, writes neither). Nothing is fired
  in either, and a pre-push hook over a glyph that cannot answer lets every
  push through. So the pre-push check says the glyph on `PATH` was not
  executed, at the severity of a hook glyph did not write: a standing choice,
  rare (none of 52 clones in t-2etd's census, 2026-09-27), and no reason to
  move `ok` (`TestPrePushHookSaysWhenNothingWasFired`, mutation row
  `doctor-pre-push-pass-vouches-for-an-unfired-path`). The same check carries
  one more advice line for the same reason: the hook refuses a violation only
  on the remote's default branch, read from `refs/remotes/<remote>/HEAD` — a
  local ref, never the network — and a clone that does not record it warns
  and exits `0` on every push (t-2etd's triage measured `0`, then `3` once
  `git remote set-head origin -a` had run). `internal/cli` reads each remote's
  recorded head through `gitsource.DefaultBranch` only when the pre-push hook
  is byte-identical, so a checkout with no hook — every CI runner — pays
  nothing; a read that fails is `unknown`, like any unread input
  (`TestPrePushHookSaysWhenNoRemoteHeadIsRecorded`, mutation row
  `doctor-pre-push-ignores-an-unrecorded-remote-head`). Firing pre-push itself
  — a `PrePushProbe` beside the commit-msg one — stays unbuilt. It would carry
  a fabricated push into a read-only diagnosis: a scratch repository, a bare
  remote, a pushed base, a recorded remote HEAD and a violating commit (without
  the recorded HEAD the same hook warned and exited 0, measured by t-2etd's
  triage), for a question the commit-msg probe already answers on every
  default install; and a new check id that can turn `ok` false on an unchanged
  machine is a breaking change to the report. The residual states are named
  instead of fired; the probe returns to the table if one is ever met.

## 8. Where we are

No phase list: a numbered plan has to be re-edited on every release, and the one
that stood here was not — it still called the gitmoji table "Phase 1" long after
it shipped, the same rot that left §5's subcommand list and package tree behind
the binary (t-0cqs).

**Shipped.** Every engine deliverable is something you can run or read in this
tree, and it is inventoried where it is already maintained rather than a third
time here: the commands are §5's list, each with its own `--help`; the three
reusable workflows and the one install action are §6; `git tag` is the release
history and the only answer to "which version". README's opening lede is the
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
notes renderer is dogfooded — the compare link included, since `notes
--since-tag` renders the line `release` composes (§4) — while the
rolling-draft path (the upsert, the hand region, the footer) is not exercised
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
