# glyph

A gitmoji-driven release engine for squash-merge repositories: one Go binary
that **lints commit messages**, computes the **semantic-version bump**, renders
**release notes**, and maintains a **rolling draft release** — all derived from
the gitmoji that leads each commit.

```sh
# Squash-safe: reads the commits INSIDE the PR, which the squash would erase.
glyph bump    --pr 7   # → v0.3.0   (a :sparkles: rides with a :white_check_mark:)
glyph notes   --pr 7   # → the Markdown release body
glyph preview --pr 7   # → the whole PR comment: what merging #7 does to the version
```

Three reusable workflows ship from this repo at each tag: `lint.yml` (commit
lint), `release.yml` (rolling-draft release) and `pr-verdict.yml` (the merge
preview — it runs anywhere, not just on rolling-draft repos). See
[`docs/DESIGN.md`](docs/DESIGN.md) for the full design.

## Why it exists

Squash-and-merge erases history you need. GitHub's
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

## What it does

| command | answer |
|---|---|
| `glyph lint` | commit-convention gate over `--range`, one `--message`, or `--stdin`; findings carry stable rule ids for machines (`--json`) |
| `glyph bump` | the next version — or **"no release"** — from `--range`, `--pr`, or the release-time walk `--since-tag` |
| `glyph notes` | the release-notes body: commits grouped under their gitmoji's section, breaking changes hoisted |
| `glyph release` | upserts one rolling **draft** release (tag, target, body); publishing — and therefore the tag — stays a human act |
| `glyph preview` | the whole merge-preview comment for a PR: what merging it does to the version, with the evidence |
| `glyph doctor` | read-only checks that the repository still matches what glyph assumes; each failing check prints the command that fixes it |
| `glyph hook install` | local `commit-msg` and `pre-push` hooks that run the same lint the CI gate runs |
| `glyph rules` | the embedded gitmoji → semver table (`--md`, or `--json` for the raw `rules.json`); `--lint` lists every lint `rule` id with `merge_candidate_only` |

`lint --range/--message/--stdin` works offline against local git alone. The
PR and release-walk inputs (`--pr`, `--since-tag`, `release`, `preview`,
`doctor`) read the GitHub API; in GitHub Actions no flags are needed —
`--repo` defaults to `$GITHUB_REPOSITORY`, the API host to `$GITHUB_API_URL`
(so a GitHub Enterprise runner just works), and the credential to
`$GITHUB_TOKEN` (else `$GH_TOKEN`).

## Install

**Homebrew**

```sh
brew install akira-toriyama/tap/glyph
```

**Prebuilt binaries** — every [release](https://github.com/akira-toriyama/glyph/releases)
ships `linux`/`darwin` × `amd64`/`arm64` tarballs with `checksums.txt` and a
build-provenance attestation you can verify:

```sh
gh attestation verify glyph_*.tar.gz --repo akira-toriyama/glyph
```

**go install**

```sh
go install github.com/akira-toriyama/glyph/cmd/glyph@latest
```

**Nix**

```sh
nix run github:akira-toriyama/glyph            # one-shot
nix profile install github:akira-toriyama/glyph
```

**GitHub Actions** — either consume a reusable workflow at a pinned release
tag:

```yaml
# .github/workflows/commit-lint.yml
on:
  pull_request:
jobs:
  lint:
    uses: akira-toriyama/glyph/.github/workflows/lint.yml@vX.Y.Z  # pin a release tag
```

or put the verified binary on a job's PATH yourself (checksum **and**
attestation are checked, fail-closed):

```yaml
- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z
  with: { version: vX.Y.Z, token: ${{ github.token }} }
```

Never pin `@main`: a moving ref changes the workflow *and* the binary under
you. `glyph doctor` flags any unpinned reference it finds in your workflows.

## Getting started in your repository

**1. Check the repository matches the model:**

```sh
glyph doctor            # read-only; --json for CI
```

The release verdict rides on configuration glyph cannot see from inside a run:
squash merging enabled, `squash_merge_commit_title` / `squash_merge_commit_message`
still landing a classifiable gitmoji subject and the per-commit body on `main`,
a credential that can read the repository, workflow pins on release tags, and
the installed hooks (if any) written by *this* binary. When one of those drifts
nothing goes red — the verdict is just computed over a repository that no
longer matches the model, which is exactly what `doctor` exists to catch.
Nothing is ever modified: each failing check prints the command that fixes it.
Exit `0` all clear · `3` a check failed · `4` a check could not run
(unverified, which is not the same as fine) — an API that never answered is
always the second, never the first.

**2. Move the lint to the moment you write the message:**

```sh
glyph hook install                 # both hooks (honours core.hooksPath)
glyph hook install commit-msg      # or name the ones you want
```

Two hooks, one gate. `commit-msg` runs `glyph lint --stdin` on the message being
written. `pre-push` hands git's ref list to `glyph hook pre-push`, which lints the
commits the push would add that the remote does not already have — the only place
the merge-candidate rules can fire before CI, since a `:construction:` commit is
legal mid-branch and illegal at the merge. It blocks **only** when the ref being
written is the remote's default branch; anywhere else it warns and lets the push
through, because refusing a legal mid-branch commit makes the branch unpushable
and the only escape is `--no-verify`, which turns the gate off entirely.

Neither hook holds a copy of the convention, and neither computes a range — both
call glyph, so they cannot fall out of lockstep when the rules move. Without glyph
on `PATH` they warn and let you through; the commit-lint CI job stays the
authority. The verdict `commit-msg` gives is the verdict CI will give: glyph
reduces the message exactly as git's cleanup mode will before linting it
(DESIGN §2.1).

**3. Wire CI** — the `lint.yml` caller above for the gate, `pr-verdict.yml`
for a merge-preview comment on every PR, and `release.yml` to keep a rolling
draft release maintained on every push to `main`. Publishing the draft is the
release: the tag is created by that human act, never by CI.

Adopting on a repository with deep history? Cut a version tag at the commit
where the convention starts — the walk baselines at the highest `v*` tag, and
with no tag at all a long history is refused past a walk cap (fail-loud, one
API round-trip per commit is the cost it refuses; the error names this exact
remedy) rather than walked unbounded. A young repository needs no tag: the
first release walks the whole history.

## Commit format

```
<:code:>[(<scope>)][!] <subject>
```

The leading gitmoji (textual form, e.g. `:sparkles:`) is the type; `!` (or a
`BREAKING CHANGE:` footer, or `:boom:`) marks a breaking change. A removal code
(`:fire:`/`:coffin:`/`:truck:`) must either carry `!` or a
`NON-BREAKING: <why>` footer saying why nothing public goes away. Examples:

```
:sparkles:(ui) add a right-click window menu            → minor
:bug:(config) keep defaults when an unknown key present → patch
:boom:(api)! replace --items flag with a positional arg → major
:memo:(readme) document the bump model                  → no release
```

The full gitmoji → semver mapping is the binary's embedded source of truth,
self-printed by `glyph rules` — `--md` (the default) renders the same table as
[`docs/gitmoji-table.md`](docs/gitmoji-table.md), and `--json` emits the
embedded `rules.json` verbatim. Both name the gitmoji-spec snapshot they were
pinned from.

## Exit codes

`0` ok · `1` no release-worthy change · `2` usage · `3` convention violation
(a commit under `lint`, the repository's own configuration under `doctor`) ·
`4` API/git/IO, including a `release` walk that could not read its range ·
`130` interrupted.

The integers are a frozen machine API — CI gates branch on the exact value, so
assert the code, never truthiness (`if glyph …` cannot tell `3` from `2`).

## Working on glyph

```sh
sh scripts/check.sh     # everything CI gates on, before pushing
```

It declares which CI gates it mirrors and which it does not, and reconciles the
two at the end — a gate that did not run means no success line, and a missing
tool is a failure rather than a skip. `nix develop` supplies the tools;
[`CLAUDE.md`](CLAUDE.md) has the rest of the working assumptions, and
[`docs/DESIGN.md`](docs/DESIGN.md) / [`docs/glossary.md`](docs/glossary.md)
hold the ratified decisions and the vocabulary.

## License

MIT © akira-toriyama
