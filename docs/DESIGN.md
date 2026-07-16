# glyph — design

The canonical design for glyph, a self-built, gitmoji-driven release engine.
The per-gitmoji semver table is **not** duplicated here — it lives in
`internal/gitmoji/rules.json` (the machine source of truth) and is self-printed
by `glyph rules`. This document is the *why* and the *shape*.

## 1. Problem

The fleet squash-merges everywhere. GitHub's
`squash_merge_commit_title = COMMIT_OR_PR_TITLE` rewrites the squash subject to
the **PR title** on any multi-commit PR, erasing per-commit types from `main`.
Every "read `main`'s linear history" tool (git-cliff today, semantic-release,
cocogitto) is therefore fooled. glyph instead derives the bump and notes from the
**individual commits inside the PR**, and makes gitmoji the type driver.

Two inversions from the prior house convention:

- **gitmoji drives classification/semver.** Previously the Conventional type
  decided the bump and the gitmoji was stripped before parsing. Now the leading
  `:code:` *is* the type.
- **The bump is computed from the PR's individual commits at merge time**, not
  from `main`'s post-squash history — the one thing git-cliff structurally
  cannot do, and the reason a self-built tool is justified.

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
  `Closes #N`, `Co-Authored-By:`.

The redundant Conventional `<type>` word is dropped — the gitmoji's own trailing
`:` plays the type-colon role. The parser is **lenient**: it accepts-and-ignores
a legacy `<type>(scope)!:` token so existing history keeps linting and bumping —
no flag-day.

Linter shape check (membership is checked in code against the embedded table):

```
^(:[a-z0-9][a-z0-9_+-]*:)(\([a-z0-9][a-z0-9-]*\))?(!)? (.+)$
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
- **none:** internal / non-shipping / meta — kept in history, excluded from notes,
  never moves the version.

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

On a release run, walk `lastPublishedTag..HEAD` over `main`'s squash commits;
for each, resolve its PR via `GET /repos/{o}/{r}/commits/{sha}/pulls` and fetch
that PR's individual commits via `GET /pulls/{N}/commits`; classify and
max-fold. **Nothing is persisted** — recompute-from-git each run, idempotent and
self-healing. Every verdict command runs **inside a git checkout** of the
repository being released — the walk base, the version base, and the draft's
target sha all come from local git; tags are never fetched over the API.

`glyph release` converges the repository's **rolling DRAFT release** on that
verdict: the one glyph-managed draft (draft + `vX.Y.Z` tag) is created or
updated **by release id** (retagged in place when the next version moves —
never a second draft; id, not tag name, because tag-name resolution can hit a
published release, cli/cli#9367), residual drafts are deleted on a none
verdict, and **no tag is created** — GitHub tags the target commit when a
human publishes. A next version not strictly above the latest published
release fails loud (an unpublishable draft; a deleted published release's tag
is burned forever). `--dry-run` computes everything, action included, and
writes nothing.

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
gate can produce one). Squash history is immutable, so that commit wedges
every release until the walk range moves past it: cut a release tag past it
by hand, or name a later base with an explicit `--since-tag=TAG`. The error
itself names the PR and both escapes.

## 5. Architecture (Go, house pattern)

Binary `glyph`, module `github.com/akira-toriyama/glyph`. Subcommands:
`lint`, `bump`, `notes`, `release`, `rules`, `version`.

```
cmd/glyph/main.go        os.Exit(cli.Execute()) — thin process boundary only
internal/core            exit-code contract + structured Error (no I/O, no logic)
internal/version         ldflags build identity + ReadBuildInfo fallback
internal/gitmoji         //go:embed rules.json; Load() validates completeness
internal/parser          message → Commit{Gitmoji,Scope,Breaking,Subject,Body,SHA,Author}
internal/bump            Level lattice; Classify; Reduce(max); Next; stdlib semver
internal/notes           group by section; text/template render (no external tmpl dep)
internal/gitsource       local `git log BASE..HEAD` (exec.CommandContext)
internal/github          commits/{sha}/pulls, pulls/{N}/commits, release CRUD
internal/cli             cobra adapter; Execute() int owns the exit-code funnel
```

**Exit-code contract** (`internal/core`): `0` ok · `1` no release · `2` usage ·
`3` lint · `4` API/git/IO · `130` interrupted. Errors are classified at the
source into `*core.Error`; `ExitCode` funnels everything (unclassified ⇒ API,
never usage).

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
`release.yml`) that install the pinned binary with checksum + attestation verify;
family repos consume them at a concrete `@vX.Y.Z` (never a moving tag — binary
and workflow ship from one repo at one tag). Migration off git-cliff is
canary-first (`chord`), shadow-mode-compared before any publish. Full rollout and
effort estimate: tracked in the `projects` furrow task and the approved plan.

## 7. Roadmap

Phase 0 scaffold (this) → 1 gitmoji table → 2 parser+bump+lint → 3 lint reusable
+ canary (first shippable) → 4 notes → 5 GitHub squash-safe plumbing → 6 hub
`release.yml@v2` + shadow → 7 hub self-adopt + docs → 8 fleet migration.
