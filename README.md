# glyph

A sigil-driven release engine for squash-merge repositories: one Go binary
that **lints commit messages**, computes the **semantic-version bump**, renders
**release notes**, and maintains a **rolling draft release** — all derived from
the version sigil each commit carries under the repository's own `glyph.toml`
(`=` none / `~` patch / `^` minor / `!` major / `%` promote to 1.0.0).

```sh
glyph init --gemoji    # write the starting glyph.toml (or --conventional)
# Squash-safe: reads the commits INSIDE the PR, which the squash would erase.
glyph bump    --pr 7   # → v0.3.0   (a ^ rides with a =)
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

Two things glyph does **not** claim as novel: pattern-driven commit parsing
(git-cliff's `commit_parsers` are the direct ancestor of `[[patterns]]`), and
deferring the tag until a human publishes (any draft-based tool does that).
What it does add beyond the second hop: a verdict that can be **no release at
all** — including refusing a range holding a commit no pattern claims, never
folding it as a silent none — and a walk that needs **no published release as
a baseline**.

## What it does

| command | answer |
|---|---|
| `glyph init` | writes a starting `glyph.toml` (`--gemoji` or `--conventional`) — the file everything else reads; `--v1-window` (gemoji only) appends the migration pattern that accepts sigil-less v1 subjects as none, with a warning on every one; an existing file refuses without `--force` |
| `glyph lint` | commit-convention gate over `--range`, one `--message`, `--stdin`, or a PR title via `--pr` (the subject a squash merge lands): does one of the repository's patterns claim the message, and does it yield a sigil? |
| `glyph bump` | the next version — or **"no release"** — from `--range`, `--pr`, or the release-time walk `--since-tag`; a commit no pattern claims refuses the whole range |
| `glyph notes` | the release-notes body: `[[note.sections]]` order, one line per commit through the `note.line` template |
| `glyph release` | upserts one rolling **draft** release (tag, target, body — `--footer-file` appends a per-repo Markdown footer); publishing — and therefore the tag — stays a human act |
| `glyph preview` | the whole merge-preview comment for a PR: what merging it does to the version, with the evidence; `--notes` folds the release-notes preview in |
| `glyph doctor` | read-only checks that the repository still matches what glyph assumes; each failing check prints the command that fixes it |
| `glyph hook install` | local `commit-msg` and `pre-push` hooks that run the same lint the CI gate runs |
| `glyph version` | the build identity — release tag, commit, build date; the one command that reaches nothing (no git, no API), so it answers anywhere |

`lint --range/--message/--stdin` and `init` work offline against local git
alone. The PR and release-walk inputs (`--pr` — on `lint` as much as on
`bump` — `--since-tag`, `release`, `preview`, `doctor`) read the GitHub API;
in GitHub Actions no flags are needed —
`--repo` defaults to `$GITHUB_REPOSITORY`, the API host to `$GITHUB_API_URL`
(so a GitHub Enterprise runner just works), and the credential to
`$GITHUB_TOKEN` (else `$GH_TOKEN`).

A **writing** `release` reads two more, and refuses rather than guesses:
`$GITHUB_REF` and `$GITHUB_EVENT_PATH`. When either is set, glyph is in a run
that has an identity, and it will only upsert the draft if the ref is the
repository's default branch — the draft is one per repository, and a release
walked over another ref folds that ref's unmerged commits into it. The
boundary comes from the event payload, or from the repository object if the
payload does not name it. `--dry-run` is never judged, and with neither
variable set (a laptop) nothing is judged and the run says so.

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
  push:
    branches: [main]  # optional — annotates direct pushes, never fails them
permissions:
  contents: read
  pull-requests: read  # the squash-title lint reads GET /pulls/{n}
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

**0. Write the config everything reads:**

```sh
glyph init --gemoji     # or --conventional; edit the file freely afterwards
```

A repository with pre-sigil gitmoji history adds `--v1-window`: sigil-less
subjects then lint clean **with a warning** and fold as `=` none instead of
failing the range. The window is meant to be deleted once every commit behind
the release walk's base carries a sigil — its comment in the generated file
says so, and the per-commit warning is what keeps it from quietly becoming
permanent.

**1. Check the repository matches the model:**

```sh
glyph doctor            # read-only; --json for CI
```

The release verdict rides on configuration glyph cannot see from inside a run:
the `glyph.toml` every verdict command reads (present and loadable — without
it every command refuses to run), squash merging enabled,
`squash_merge_commit_title` / `squash_merge_commit_message`
still landing the pull-request title as the squash subject on `main`,
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
commits the push would add that the remote does not already have. It blocks **only** when the ref being
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

Two wired-in behaviours worth knowing before you read them out of the YAML:
the lint caller's optional `push: branches: [main]` arm runs the same rules
over each direct push to the default branch but only **annotates** — that
history is immutable, so a red verdict there could never be made green again,
and exit 3 is swallowed on that arm alone. And `release.yml` hands its verdict
back as `workflow_call` outputs (`level`, `next`, `current`, `action`) so a
caller can gate follow-up steps without re-deriving it; empty means **not
computed**, never "none", so gate fail-safe on `""`. The draft's URL is
deliberately not among them — with the handle in hand, auto-publishing would
be a two-line caller step, and publishing staying human is the safety net.

Adopting on a repository with deep history? Cut a version tag at the commit
where the convention starts — the walk baselines at the highest `v*` tag, and
with no tag at all a long history is refused past a walk cap (fail-loud, one
API round-trip per commit is the cost it refuses; the error names this exact
remedy) rather than walked unbounded. A young repository needs no tag: the
first release walks the whole history.

## Commit format

Your `glyph.toml` decides. The shipped presets give you a starting grammar:

```
<:code:>[(<scope>)]<sigil> <subject>     # --gemoji
<type>[(<scope>)]<sigil>: <subject>      # --conventional
```

The sigil is the version signal, and the only thing glyph interprets:
`=` none / `~` patch / `^` minor / `!` major / `%` promote. The prefix (a
gemoji, a conventional type, anything your pattern accepts) is for the reader
and never decides the version. Examples under the gemoji preset:

```
:sparkles:(ui)^ add a right-click window menu            → minor
:bug:(config)~ keep defaults when an unknown key present → patch
:boom:(api)! replace --items flag with a positional arg  → major
:memo:(readme)= document the bump model                  → no release
:rocket:% call it 1.0                                    → v1.0.0
```

**Below 1.0, `!` does not reach 1.0.0.** While the version is `v0.y.z` a
breaking change steps the minor (`v0.5.3` + `!` → `v0.6.0`), so a repository
still finding its shape can break things without claiming a stable major.
`%` is the only way across: it lands exactly on `v1.0.0`, and from 1.x on it
is an ordinary major step (`v1.4.2` + `%` → `v2.0.0`). A `%` commit is still
classified as breaking everywhere a level is published — the release notes'
Breaking Changes section, the PR verdict, the JSON — because the rule shortens
the *step*, not the meaning of the commit. One consequence worth expecting: in
0.x, `!` and `^` land on the same version.

Under the conventional preset the sigil sits before the colon, so
Conventional Commits' own `feat!:` reads as the major sigil unchanged
(`feat^:` minors, `fix~:` patches, `chore=:` moves nothing, `feat%:` promotes)
— and a sigil-less `feat:` is a violation: writing the version signal down is
the point.

Everything is the pattern file's to change: `[[patterns]]` are ordered RE2
regexes (first match wins) over the whole message, the named group
`semver_sigil` carries the signal, a pattern-level `semver_sigil` key
supplies one for messages that carry none (the presets make a raw
`git revert` a patch), and `skip = true` drops a matching commit from every
check (merge commits, autosquash artifacts). `exclude_authors` keeps bots
out of lint and the fold; whether they appear in the notes is
`[[note.sections]]`'s decision alone.

`note.line` is the same idea for the release body: `$name` reads the winning
pattern's named groups, `$pr` / `$author` / `$hash` are built in, and a
`$[ … ]` span renders only when every placeholder inside it resolves. That is
what lets the shipped `- $subject$[ ($pr)] @$author` cite a pull when there is
one and drop the parens with it when there is not, instead of writing `()` for
every commit that reached main without a merged pull.

## Exit codes

`0` ok · `1` no release-worthy change · `2` usage · `3` convention violation
(a commit under `lint`, the repository's own configuration under `doctor`) ·
`4` API/git/IO, a `release` walk that could not read its range, or a `release`
write refused off the default branch · `130` interrupted.

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
