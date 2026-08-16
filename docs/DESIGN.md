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

glyph speaks two commit grammars, called **profiles**: **`gitmoji`** — the
default, the fleet's own, and the subject of the rest of this section — and
**`conventional`** (§2.2), ratified 2026-08-16 for company repositories where a
gitmoji vocabulary cannot be imposed. A profile bundles the three things a
vocabulary owns — the subject grammar, the token → bump table (§3), and the
lint rules that only make sense inside that vocabulary — and nothing else:
footer semantics, body rules, git's cleanup (§2.1), the walk, the fold and the
exit codes are one implementation both profiles share. How a run selects its
profile is a distribution question and lives in §6.

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
  Japanese translation (house rule). Footer may carry `BREAKING CHANGE:` (or
  `BREAKING-CHANGE:`), `NON-BREAKING: <why>` (next bullet), `Closes #N`,
  `Co-Authored-By:`. A footer counts only where a trailer can legally sit —
  opening a block (after a blank line, or as the very FIRST body line) or
  stacked under another trailer — and a block is read as one: git trailers
  (`token: value`) and issue references in GitHub's colon-less closing-keyword
  form (`Closes #12`, `Fixes owner/repo#12`) may stack with no blank line
  between them, and only prose ends the block. That the colon-less form counts
  is load-bearing rather than cosmetic — reading it as prose closed the block
  and discarded a `BREAKING CHANGE:` footer stacked beneath it, shipping a major
  as a minor out of a shape this very list blesses. A line that merely OPENS
  with a closing keyword ("fixes the crash reported in #12 by …") is prose and
  still ends the block.
- **`NON-BREAKING: <why>`** — the removal codes' counterpart to
  `BREAKING CHANGE:`, and what satisfies `undeclared-removal` (below) when `!` is
  not the answer: it records that a `:fire:`/`:coffin:`/`:truck:` commit takes
  nothing public away. Alone among these footers it exists for one lint rule and
  nothing else in glyph reads it, and it never moves the bump — so it cannot hide
  a break, only record a claim the author is making. Uppercase and
  case-SENSITIVE, for the reason `BREAKING CHANGE:` is: a body may legitimately
  read "this is non-breaking: the API is untouched", and a footer that switches a
  rule off must not be spellable by accident in prose. The reason is mandatory —
  a bare `NON-BREAKING:` leaves the rule unsatisfied, because the magic word
  typed by reflex answers nothing the rule exists to ask.

The redundant Conventional `<type>` word is dropped — the gitmoji's own trailing
`:` plays the type-colon role. The retired token is handled by SURFACE, not by
one blanket policy. The release walk's parser stays **lenient**: it
accepts-and-ignores a legacy `<type>(scope)!:` token (scope salvaged when the
new slot has none, its `!` still meaning breaking) so the immutable pre-glyph
history keeps walking and bumping exactly as it always has. The authoring lint
makes the same token a **hard error** (`legacy-token`, in the list below):
since v1.0.0 the convention is one grammar and new history carries zero
migration debt — ratified 2026-07-21, shipped only after the machines that
wrote the retired form fleet-wide were silenced first (t-271n), so the rule
never fires on a commit a bot is still producing. Re-ratified 2026-08-16,
narrower and unchanged in force: "one grammar" means one grammar **per
profile**. The rule's intent was always the detection of vocabulary bleed, not
a judgement on the Conventional form as such — the same token this rule
hard-errors on is the conventional profile's canonical grammar, and that
profile carries the mirror-image guard pointing the other way (§2.2).

Linter shape check (membership is checked in code against the embedded table):

```
^(:[a-z0-9][a-z0-9_+-]*:)(\([a-z0-9][a-z0-9-]*\))?(!)? (\S.*)$
```

An unknown `:code:` is a **hard lint error (exit 3)**, never a silent patch.

The complete rule set of the gitmoji profile, in the order `parser.Lint`
evaluates it — §2.2 states the conventional profile's vocabulary as a delta
over this list. Every id is
**machine API** — branch on the id, never on the prose — and so is the finding's
`fix` field: where a repair is mechanical (the retired token, casing, trailing
periods, a lowercasable scope), the violation carries the corrected subject
line, and pasting it as the message's first line lints green
(`TestLintFixIsPasteable`). Every fixable violation on one message carries the
SAME fully-corrected line — per-rule fixes applied in sequence un-did each
other — and rules whose repair needs a human decision (an unknown code, a WIP
marker, an undeclared removal) carry none: a guessed fix lint would bless
anyway is a wrong answer pasted with confidence. Agents were regexing `detail`
for the suggestion, and that prose has been reworded before (#78) — the `fix`
key is the stable home the id discipline already promised. This list has to stay
in step with the `Rule*` constants in `internal/parser/parser.go`. It did not:
the prose summary that stood here was written in the scaffold commit, before the
parser existed. It named four of the seven, left `malformed-subject` implicit in
the shape block above, and had no word at all for `invalid-scope` or
`undeclared-removal` — both added later. Enumerating by id is what makes that
drift checkable at all, and `TestDesignDocNamesEveryRuleID` in `internal/parser`
is what checks it: an id here that is no longer a constant, or a constant not
named here, fails the suite. The binary self-prints the same vocabulary —
`glyph rules --lint`, each id with `merge_candidate_only` and nothing else —
held to the constants by `TestLintRulesMatchTheConstants` and to `Lint`'s real
mode behaviour by `TestLintRulesModeGating`. The semantics stay here on
purpose: a prose field beside a printed id would be this list's second home.

- `malformed-subject` — the subject line does not match the shape above.
  Short-circuits: with the subject unparsed, nothing else is checkable.
- `invalid-scope` — the same parse failure, sharpened for a subject whose scope
  is outside lowercase kebab: it names the offending scope and suggests the
  lowercased form when lowercasing alone would make it legal, where
  `malformed-subject` quotes the whole line and sends an author who wrote
  `(Palette)` hunting the gitmoji or the separating space. Being a parse failure
  it short-circuits exactly as `malformed-subject` does, which is the part worth
  knowing at the terminal: `:bug:(Palette) Do a thing.` reports the scope ALONE
  and says nothing about the capital or the period until the scope is fixed. The
  defect it prevents was authors obeying `undeclared-removal`'s own instruction —
  `:fire:(Palette)! prune catppuccin-latte` answered with `malformed-subject`
  (t-edan), in the PascalCase-scoping Swift repos (sill, wand, facet, halo,
  perch) that removal rule most protects. The legacy token's scope slot is
  `[^()]+` and still accepts `(Palette)`, so the retired syntax is the more
  permissive of the two — which is exactly why the canonical form owes the author
  the sharper message.
- `legacy-token` — the subject carries the retired Conventional
  `<type>[(scope)][!]:` token after the gitmoji. First of the accumulating
  rules because the rewrite it offers is the line the style rules should be
  judging: the detail hands the author the canonical spelling — salvaged scope
  and `!` preserved — whenever one exists that the linter itself accepts
  (kebabSuggestion's contract, one level up; a salvaged scope even lowercasing
  cannot make kebab gets the plain grammar reminder, because a suggestion that
  drops the scope misrepresents the commit). Fires in every authoring mode and
  never on the walk — Parse still eats the token, so history is untouched.
- `unknown-gitmoji` — the code is not in the embedded table (`glyph rules`).
- `wip-merge-candidate` — a `:construction:` commit reaching a merge candidate.
  The ONE rule gated on merge-candidate mode: `:construction:` is legal
  mid-branch and illegal only at the merge, so its verdict genuinely changes
  with time.
- `uppercase-subject` — the subject's first rune is uppercase.
- `trailing-period` — the subject ends with `.`, judged after the same trailing
  space/tab/CR trim git itself applies before recording the message. Reading the
  untrimmed line let a trailing space hide the period behind it, in every mode,
  `--range` and CI included.
- `cjk-subject` — the subject carries a rune in a CJK script (Han, Hiragana,
  Katakana, Hangul, or their punctuation/fullwidth blocks). The convention's
  subjects have been English since the shape block above was written, and this
  is the first rule to hold any of it — measured before it existed, 592 fleet
  subjects carried CJK text and 585 of them linted clean. The id names what is
  checked, not the policy: a CJK scan is no English detector (a French subject
  sails through), so `non-english-subject` would promise a judgement the rule
  cannot make. Subject only — bodies in pre-retirement history legitimately
  carry `---（和訳）` sections — and no `fix`: the mechanical repair is a
  translation, exactly the guess `fix` refuses to bless.
- `rendered-gitmoji` — the subject opens with the GLYPH form of a known code
  (`✨ feat(tree): x`) instead of the textual `:sparkles:`. The same argument
  that made `invalid-scope`: it is a parse failure, and `malformed-subject`
  quoting the whole line sends the author hunting when the one wrong thing is
  the emoji's spelling. Measured 8 subjects across 4 PRs, every one a PR title
  — the surface where an emoji picker is one keystroke away — and five of the
  eight carried a retired Conventional token too, so the finding's `fix` is
  composed through `Format` on the code-substituted message: one corrected
  line that clears both, rather than two findings each proposing half the
  repair. The reverse lookup is injected (`LintOptions.CodeForEmoji`, U+FE0F
  and ZWJ normalized away) so the parser stays table-blind, and detection sits
  beside `laxSubjectRE`, never inside `Parse` — the walk must keep refusing
  the glyph form.
- `undeclared-removal` — a `:fire:`, `:coffin:` or `:truck:` commit that says
  nothing about whether it breaks anyone: no `!`, no `BREAKING CHANGE:` footer,
  no `NON-BREAKING: <why>` footer. Those three codes are the removals, and the
  only three; all three are `none`, which is right for dead code, docs and
  fixtures and silently wrong for the rare one. sill pruned the public preset
  `catppuccin-latte` under `:fire:` inside a `:sparkles:` PR, shipped it as a
  MINOR, and broke downstream wand (t-n158) — `:truck:` is the worst of the three
  there, because a rename resolves at runtime, so
  `paletteFor("catppuccin-latte")` fell back to another theme instead of failing.
  glyph cannot know whether the removed element was public — that is the
  consuming repo's knowledge, and an API-diff tool's job — so the rule only
  refuses to let the question go UNANSWERED. Deliberately NOT gated on
  merge-candidate, unlike `wip-merge-candidate`: whether a removal breaks anyone
  is settled the moment the commit is written, so there is nothing to wait for,
  and waiting is what costs — at the hook the fix is one line in an editor the
  author already has open, in CI it is a rewrite of pushed history.

These are glyph's implementation of the convention, not its normative statement.
Which elements count as *public* for `undeclared-removal` is settled by the fleet
CONTRIBUTING.md that `docs/commit-convention.md` points at — do not restate it
here, or one question will have two answers.

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

### 2.2 The conventional profile (ratified 2026-08-16)

```
<type>[(<scope>)][!]: <subject>
```

Conventional Commits' own form, chosen over a second in-house token set because
the profile exists for company repositories whose authors should have to learn
nothing: the motive is zero imposed vocabulary, and an invented one would
rebuild exactly the cost being removed. The type vocabulary is closed —
`feat fix perf revert docs style refactor test build ci chore`, eleven types,
pinned by a `TypeCount = 11` the way `CodeCount = 75` pins the gitmoji table —
and it is, measured 2026-08-16, identical to the retired-token vocabulary
`legacyTokenRE` already recognises and to commitlint's config-conventional
type-enum, so an author arriving from either recognises every token.

What differs from the gitmoji grammar, and everything that does not:

- the type plays the code's role and the type-colon replaces the gitmoji's own
  trailing colon (`feat(cli)!: x`), with `!` before the colon as the
  Conventional spec places it. Type membership is checked the way code
  membership is — injected, the shape check open — so the parser stays
  table-blind in both profiles and an unknown type is the same hard error an
  unknown gitmoji is, never a silent fallback.
- the scope rule is the same lowercase kebab. The Conventional spec does not
  regulate scope shape, so this is a house rule carried across on purpose: an
  author moving between a fleet repo and a company repo keeps one habit, and
  `invalid-scope` keeps firing with the same sharpened message.
- subject style is shared: imperative, lowercase start, no trailing period,
  English — `uppercase-subject`, `trailing-period` and `cjk-subject` fire
  unchanged, and the `fmt` fix machinery composes the same corrected line.
- **footer semantics are shared verbatim** — `BREAKING CHANGE:` /
  `BREAKING-CHANGE:`, `NON-BREAKING:`, the trailer-block placement rules, the
  colon-less closing-keyword forms. This was nearly ratified the other way
  ("subject-only, `!` alone means breaking") on the belief that the current
  parser model closes its decision in the subject line; measured 2026-08-16,
  that belief was false — the footer walk in `Parse` is independent of the
  subject grammar and already classifies footers under the gitmoji profile.
  Dropping footers from one profile is therefore what would SPLIT the two
  profiles' verdicts on an identical body — the opposite of the parity this
  profile exists to keep. `NON-BREAKING:` still parses here and changes no
  verdict (the one rule that reads it is gitmoji-only, below); the asymmetry
  is in the rules that consume the record, not in what is recorded.

The lint vocabulary, as a delta over the list above. Shared and firing
identically: `malformed-subject`, `invalid-scope`, `uppercase-subject`,
`trailing-period`, `cjk-subject`. Two ids exist only here:

- `unknown-type` — the type is not in the embedded conventional table (`glyph
  rules --profile=conventional`). The membership rule read across, under a new
  id rather than a reused one: `unknown-gitmoji` naming a type would lie to
  the machine that branches on it. Like its counterpart, membership is the
  injected oracle's answer and the shape check stays open, so `readme: fix
  typo` is diagnosed as an unknown TYPE rather than a shapeless line.
- `gitmoji-token` — the mirror image of `legacy-token`: a conventional-profile
  subject that opens with the gitmoji grammar's own well-formed shape
  (`:sparkles: add x`) sharpens `malformed-subject` into the name of what the
  author actually did — wrote the other profile's vocabulary. Shape-checked
  only, deliberately: deciding whether the code is a KNOWN gitmoji would make
  this profile load the other profile's table to lint its own commits. And no
  `fix`, for `Fix`'s standing reason — which type a gitmoji maps to is a
  cross-vocabulary guess.

Absent, each with its reason:

| gitmoji-profile rule | why it has no conventional counterpart |
|---|---|
| `legacy-token` | the token it retires IS this profile's grammar; bleed detection here is `gitmoji-token` |
| `rendered-gitmoji` | diagnoses a misspelt gitmoji; there is no gitmoji to misspell |
| `wip-merge-candidate` | no WIP type exists — Conventional has nothing playing `:construction:` |
| `undeclared-removal` | no removal types exist, so the question cannot be asked — next paragraph |

The absence of `undeclared-removal` is the one to respect rather than admire:
the Conventional vocabulary cannot mark a removal, so the sill incident class —
a public symbol pruned inside a feature PR, shipped minor, breaking a
downstream consumer (t-n158) — is unguardable in this profile. A `refactor:`
that deletes public API and says nothing ships a none. Accepted with eyes
open: the profile's ratified scope is lint + bump for repositories that never
had the guard either, and the honest reading is an argument FOR the gitmoji
profile, not a defect to fix by inventing a `removal` type no Conventional
author would ever write unprompted.

One dogfood fact, named because §8 names the smaller version of it: the fleet
stays on the gitmoji profile, so no commit in this repository's own CI ever
exercises the conventional grammar end-to-end. The compensations are
structural — a parity suite and mutation-ledger rows on the glyph side, a
live-fire repository on the fleet side — and the epic's closing condition is a
company repository actually running the profile, not a green suite here.

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

**Deliberate divergences from the spec's `semver` field** — ratified and
shipped; each is pinned by name in `TestLoadBearingAndRatifiedDeviations`, so
reverting one fails the suite rather than quietly changing every repo's bump:
`:wrench:`→none and `:alembic:`→none (fleet config / experiments are
non-shipping); `:thread:` / `:safety_vest:` / `:airplane:` / `:t-rex:`→patch (each
changes shipped runtime behavior the spec leaves `null`).

### 3.1 The conventional table (ratified 2026-08-16)

Same lattice, same default-none, same fold, same non-suppressible breaking flag
— with one less trigger: the conventional vocabulary has no `:boom:`, so
breaking is `!` or the footer, exactly the two the Conventional spec defines.
Every type is explicitly enumerated, and the table is **derived, not
designed**: each type takes the bump and section of its canonical gitmoji
counterpart, so the two tables cannot quietly embody two philosophies — a
dispute about a conventional row is a dispute about the gitmoji row it derives
from, and the arguments above settle both.

| type | bump | section | counterpart |
|------|------|---------|-------------|
| `feat` | minor | Features | `:sparkles:` |
| `fix` | patch | Fixes | `:bug:` |
| `perf` | patch | Performance | `:zap:` |
| `revert` | patch | Reverts | `:rewind:` |
| `docs` | none | — | `:memo:` |
| `style` | none | — | `:art:` |
| `refactor` | none | — | `:recycle:` |
| `test` | none | — | `:white_check_mark:` |
| `build` | none | — | `:construction_worker:` |
| `ci` | none | — | `:green_heart:` |
| `chore` | none | — | `:hammer:` |

Where the industry splits, the derivation decides: `perf` → patch sides with
semantic-release and `:zap:` against convco's default none; `revert` → patch
sides with everyone and `:rewind:`. The row that costs something is **`build`
→ none**. The gitmoji table classifies dependency changes patch — six codes
and a Dependencies section, because a vendored dependency changes the shipped
binary — while the Conventional vocabulary folds dependency bumps under
`build`/`chore` (Dependabot's own spelling), and this table maps both none. A
dependency upgrade that changes shipped behaviour must therefore say so itself
— `fix`, `feat`, or `!` — or ride along until the next version-moving commit.
Ratified as accepted coarseness rather than patched, because the repair would
be a bump keyed on the scope slot (`build(deps)` → patch), which makes a
free-form label semantic in exactly one cell of one profile's table — and a
scope suddenly load-bearing is drift no author would predict.

`sections[]` is shared: conventional rows draw from the gitmoji section list,
no new names. The ratified company scope is lint + bump — notes and the
rolling draft are not required — but a row that already carries its section
means turning notes on later is a decision, not a data migration. Version
stepping, 0.x included, is shared and makes no new decision here.

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
branch, into the notes. The notes then cite what the fold established: the pull
**beside** the landed sha (`(#123, abc1234)`) wherever one resolved, and for a
footprint-less commit — the squash arm, whose listed shas exist on no branch and
were published anyway (t-xxhj: a live release body cited two shas
`git branch -r --contains` answers nothing for) — the pull **alone** (`(#123)`),
the one address that outlives the squash. Beside, never instead: within one pull
the sha is what keeps N entries N distinct lines, so replacing it would collapse
them into N copies of the same pointer. A **shallow** checkout cannot answer the question at all
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
internal/gitmoji         //go:embed rules.json; Load() validates completeness
internal/parser          Commit{Gitmoji,Scope,Breaking,NonBreaking,Subject,Body,SHA,Author}
internal/bump            Level lattice; Classify; Reduce(max); Next; stdlib semver
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

**gitmoji table embedding:** `//go:embed rules.json` inside `internal/gitmoji`
(an embed pattern is package-relative, so it cannot be written as a path from the
repository root) — the pinned binary *is* the pinned rules (lockstep, zero skew).
`Load()` fails at startup if any spec code is missing or a bump is out of enum.

**Testing** (stdlib only, no testify): table tests + a full-coverage
exhaustiveness test for the gitmoji table — the two halves buy different things.
`Load()` (above) only catches `rules.json` disagreeing with `CodeCount`, which an
edit to both would satisfy, so `TestCodeCount` pins the literal 75 as well: a
code added or dropped cannot reach a release without a diff that says so. Neither
half is a build error — the binary still compiles; what breaks is every command
that reads the table, at startup. Golden files for notes
(`internal/notes/testdata/*.golden.md`) and for the docs table
(`docs/gitmoji-table.md` is `glyph rules --md`, held by a golden test);
`internal/workflows` pins what the CI YAML cannot state about itself; fuzz over
the parser (never panics; well-formed round-trips), the fold
(order-independence), version parse/step, the `Link:` header parser (it extracts,
never fabricates) and both Markdown escapers. One fuzz target is not a parse
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

**Profile selection (ratified 2026-08-16) is a flag, not a file in the repo:**
`--profile={gitmoji|conventional}`, default `gitmoji`, on every command that
reads a rules table, both tables embedded in the one binary — "rules ship
inside the binary" (this section's opening sentence) now covers two tables
instead of one and is re-ratified unchanged. Surveyed before ratifying
(shallow clones, 2026-08-16): all six comparable tools — commitlint,
semantic-release, cocogitto, convco, git-cliff, release-please — select their
vocabulary through a per-repository config file, which is exactly the
synced-table drift that opening sentence refuses; the survey made the
no-config stance a deliberate differentiation rather than an omission. glyph's
callers already state a pinned tag at every use site, and the profile rides
the same sites: the three reusables take a `profile` input (default
`gitmoji`) a company caller sets once beside its pin, and
`hook install --profile=…` interpolates the flag into the hook it writes, the
way the hook already interpolates its gate exit code. A repository therefore
cannot drift into a profile nobody chose — the choice sits where the version
already sits, in the caller's own pinned file, reviewed like any pin move.

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
