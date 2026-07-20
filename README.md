# glyph

A gitmoji-driven release engine: one Go CLI that **lints commits**, computes the
**semantic-version bump**, and renders **release notes** — deriving all of it from
the gitmoji that leads each commit.

> **Status: engine complete, fleet migration underway.** `version`, `rules`,
> `lint` (`--range` / `--message` / `--stdin`), `bump` / `notes` over a local
> range (`--range`), a **pull request's individual, pre-squash commits**
> (`--pr`) or the release-time walk (`--since-tag`), and `release` (rolling
> DRAFT upsert), `preview` (the whole PR comment) and `hook install` (the
> commit-msg hook) all work. Three reusable
> workflows ship from this repo at each tag: `lint.yml` (commit lint),
> `release.yml` (rolling-draft release) and `pr-verdict.yml` (the merge
> preview — it runs anywhere, not just on rolling-draft repos).
> See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design.

```sh
# Squash-safe: reads the commits INSIDE the PR, which the squash would erase.
glyph bump    --pr 7   # → v0.3.0   (a :sparkles: rides with a :white_check_mark:)
glyph notes   --pr 7   # → the Markdown body, none-bump commits left out
glyph preview --pr 7   # → the whole PR comment: what merging #7 does to the version
```

In GitHub Actions no flags are needed: `--repo` defaults to `$GITHUB_REPOSITORY`,
the API host to `$GITHUB_API_URL` (so a GitHub Enterprise runner just works), and
the credential to `$GITHUB_TOKEN` (else `$GH_TOKEN`).

Locally, one command moves the lint from CI to the moment you write the message:

```sh
glyph hook install     # commit-msg → `glyph lint --stdin` (honours core.hooksPath)
```

The hook holds no copy of the convention — it calls glyph, so it cannot fall out
of lockstep when the rules move. Without glyph on `PATH` it warns and lets the
commit through; the commit-lint CI job stays the authority.

## Why it exists

The fleet uses **squash-and-merge** everywhere. GitHub's
`squash_merge_commit_title = COMMIT_OR_PR_TITLE` rewrites the squash subject to
the **PR title** on any multi-commit PR, so the per-commit types are erased from
`main`. Every tool that types a release from **commit text** (git-cliff,
semantic-release, release-please, cocogitto) is fooled by this — a PR titled
`chore: cleanup` that contained a feature silently ships no minor bump.

glyph derives the bump and notes from the **individual commits inside the PR**,
read over the API and re-read at release time, so the squash can never lose them.

**What is actually new here is that second hop.** The rest of the field either
routes around commit text or stops one step short of it:

| tool | how it dodges the squash | where it stops |
|---|---|---|
| release-drafter | never reads commit text — types from PR **labels** + paths | resolves each commit to its PR, but the PR fragment has no `commits` — it never re-expands one |
| release-please | reads the merged PR over the API — but its **body**, a human-written override | recommends squash *specifically to discard* intra-PR types |
| semantic-release | asks you to constrain the **PR title** instead | maintainers hold that pre-squash commits are disposable by design |
| python-semantic-release | un-squashes by parsing the squash **body text** | breaks exactly when GitHub drops that text |
| changesets / knope | moves the signal out of commits into **intent files** | squash-safety is a side effect, not a goal |
| tagpr | types from **PR labels** | resolves PRs, never their commits |

So the squash-commit → PR hop is prior art. Reading **that PR's own commits** to
type the release is the part nothing else does — and it is why glyph exists.

Two things glyph does **not** claim as novel: the gitmoji vocabulary
(`semantic-release-gitmoji` and python-semantic-release's `EmojiCommitParser`
ship nearly the same `:boom:`/`:sparkles:`/`:bug:` mapping), and deferring the
tag until a human publishes (any draft-based tool does that). What it does add
beyond the second hop: a verdict that can be **no release at all** (release-drafter
falls back to `patch` when nothing matches), and a walk that needs **no published
release as a baseline**.

## Commit format

```
<:code:>[(<scope>)][!] <subject>
```

The leading gitmoji (textual form, e.g. `:sparkles:`) is the type; `!` (or a
`BREAKING CHANGE:` footer, or `:boom:`) marks a breaking change. Examples:

```
:sparkles:(ui) add a right-click window menu            → minor
:bug:(config) keep defaults when an unknown key present → patch
:boom:(api)! replace --items flag with a positional arg → major
:memo:(readme) document the bump model                  → no release
```

The full gitmoji → semver mapping is the binary's embedded source of truth,
self-printed by `glyph rules` (Phase 1).

## Exit codes

`0` ok · `1` no release-worthy change · `2` usage · `3` lint violation ·
`4` API/git/IO · `130` interrupted.

## License

MIT © akira-toriyama
