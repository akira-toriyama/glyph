# glyph

A gitmoji-driven release engine: one Go CLI that **lints commits**, computes the
**semantic-version bump**, and renders **release notes** — deriving all of it from
the gitmoji that leads each commit.

> **Status: engine complete, fleet migration underway.** `version`, `rules`,
> `lint` (`--range` / `--message` / `--stdin`), `bump` / `notes` over a local
> range (`--range`), a **pull request's individual, pre-squash commits**
> (`--pr`) or the release-time walk (`--since-tag`), and `release` (rolling
> DRAFT upsert) all work. Two reusable workflows ship from this repo at each
> tag: `lint.yml` (commit lint) and `release.yml` (rolling-draft release).
> See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design.

```sh
# Squash-safe: reads the commits INSIDE the PR, which the squash would erase.
glyph bump  --pr 7   # → v0.3.0   (a :sparkles: rides with a :white_check_mark:)
glyph notes --pr 7   # → the Markdown body, none-bump commits left out
```

In GitHub Actions no flags are needed: `--repo` defaults to `$GITHUB_REPOSITORY`,
the API host to `$GITHUB_API_URL` (so a GitHub Enterprise runner just works), and
the credential to `$GITHUB_TOKEN` (else `$GH_TOKEN`).

## Why it exists

The fleet uses **squash-and-merge** everywhere. GitHub's
`squash_merge_commit_title = COMMIT_OR_PR_TITLE` rewrites the squash subject to
the **PR title** on any multi-commit PR, so the per-commit types are erased from
`main`. Any tool that reads `main`'s history (git-cliff, semantic-release,
cocogitto) is fooled — a PR titled `chore: cleanup` that contained a feature
silently ships no minor bump.

glyph derives the bump and notes from the **individual commits inside the PR**
(re-read at release time), so squash-merge can never lose them. And it makes the
**gitmoji first-class**: the leading `:code:` *is* the change type.

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
