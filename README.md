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
| `glyph init` | writes a starting `glyph.toml` (`--gemoji` or `--conventional`) at the checkout's top level, from any subdirectory — the file everything else reads; an existing file refuses without `--force` |
| `glyph lint` | commit-convention gate over `--range`, one `--message`, `--stdin`, or a PR title via `--pr` (the subject a multi-commit squash lands): does one of the repository's patterns claim the message, and does it yield a sigil? |
| `glyph bump` | the next version — or **"no release"** — from `--range`, `--pr`, or the release-time walk `--since-tag`; a commit no pattern claims refuses the whole range |
| `glyph notes` | the release-notes body: `[[note.sections]]` order, one line per commit through the `note.line` template — over `--since-tag`, closed by the range's compare link |
| `glyph release` | upserts one rolling **draft** release (tag, target, body closed by the range's compare link — `--footer-file` appends a per-repo Markdown footer); publishing — and therefore the tag — stays a human act |
| `glyph preview` | the whole merge-preview comment for a PR: what merging it does to the version, with the evidence; `--notes` folds the release-notes preview in |
| `glyph doctor` | read-only checks that the repository still matches what glyph assumes; each failing check prints the command or edit that fixes it |
| `glyph hook install` | local `commit-msg` and `pre-push` hooks that run the same lint the CI gate runs |
| `glyph version` | the build identity — release tag, commit, build date; like `emoji` it reaches nothing (no git, no API), so it answers anywhere |
| `glyph emoji` | the gemoji dictionary as JSON: for each kind of change the one shortcode to write, when to pick it, and the gitmoji codes it absorbed; advice only — lint never reads it, and like `version` it reaches nothing |

Every `--range` input (`lint`, `bump`, `notes`), `--message` / `--stdin`,
`init`, `hook`, `emoji` and `version` work offline against local git alone.
The PR and release-walk inputs (`--pr` — on `lint` as much as on `bump` —
`--since-tag`, `release`, `preview`, `doctor`) read the GitHub API;
in GitHub Actions no flags are needed —
`--repo` defaults to `$GITHUB_REPOSITORY`, the API host to `$GITHUB_API_URL`
(so a GitHub Enterprise runner just works), and the credential to
`$GITHUB_TOKEN` (else `$GH_TOKEN`). Outside Actions, with the variable unset,
`--repo` defaults to the clone's `origin` remote — provided it points at the
host the API client will query; an origin elsewhere is refused at exit 2
rather than asked about on the wrong host.

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
go install github.com/akira-toriyama/glyph/v6/cmd/glyph@latest
```

**From a checkout**

```sh
git clone https://github.com/akira-toriyama/glyph && cd glyph
sh install.sh          # → ~/.local/bin/glyph
```

`install.sh` runs `build.sh` — a `go build` stamped from the checkout's
`git describe`, so `glyph version` names the commit it was built from
(`-dirty` included) — and copies `bin/glyph` to `~/.local/bin/glyph`, printing
the line to add to your shell rc when that directory is not on `PATH`. The
build honours `go.mod`'s `toolchain` line: with an older Go installed, the `go`
command fetches that toolchain once. `sh build.sh` alone stops at `bin/glyph`.

**Nix**

```sh
nix run github:akira-toriyama/glyph            # one-shot
nix profile install github:akira-toriyama/glyph
```

The flake builds with the Go its locked nixpkgs ships, not with `go.mod`'s
`toolchain` (`buildGoModule` never downloads a toolchain), so a Go security
fix reaches this path when `flake.lock` moves to a nixpkgs that carries it.

**GitHub Actions** — either consume a reusable workflow at a pinned release
tag:

```yaml
# .github/workflows/commit-lint.yml
on:
  pull_request:
    # a retitle re-fires the squash-title lint only with `edited`, not a default type
    types: [opened, synchronize, reopened, edited]
  push:
    branches: [main]  # optional — annotates a direct push (exit 3 swallowed there alone; a push it cannot judge still fails)
permissions:
  contents: read
  pull-requests: read  # the squash-title lint reads GET /pulls/{n}
jobs:
  lint:
    uses: akira-toriyama/glyph/.github/workflows/lint.yml@vX.Y.Z  # pin a release tag
```

or put the verified binary on a job's PATH yourself (checksum **and**
attestation are checked, fail-closed, and the binary must report the release
tag you passed as `version`). The action runs `curl`, `tar`, `gh` and `jq`,
which every GitHub-hosted Linux and macOS runner already has; a self-hosted
runner needs them installed:

```yaml
- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z
  with:
    version: vX.Y.Z
    token: ${{ github.token }}
```

Never pin `@main`: a moving ref changes the workflow *and* the binary under
you. `glyph doctor` flags every `uses: akira-toriyama/glyph/…` in your
workflows and action files that is not pinned to a release tag; what other
actions are pinned to is not its question.

## Getting started in your repository

**0. Write the config everything reads:**

```sh
glyph init --gemoji     # or --conventional; edit the file freely afterwards
```

glyph ships no migration window: a subject no pattern claims is a violation at
the gate and refuses the release walk, so a repository whose history predates
the sigil does not teach its grammar to accept the old subjects — it baselines
the walk with a tag on the last commit before the convention ("Adopting on a
repository with deep history", below), and the walk never reads past that tag.

**1. Check the repository matches the model:**

```sh
glyph doctor            # read-only; --json for CI
```

The release verdict rides on configuration glyph cannot see from inside a run:
the `glyph.toml` every verdict command reads (present and loadable — without
it every verdict command refuses at exit 2), the squash-merge settings the
walk assumes, a credential that can read the repository (and, for `release`,
write it), workflow pins on release tags, caller workflows granting and
passing what the pinned reusables declare, and the installed hooks (if any)
written by *this* binary. `glyph doctor --help` lists every check and what
each one guards. When one of those drifts nothing goes red — the verdict is
just computed over a repository that no longer matches the model, which is
exactly what `doctor` exists to catch. Nothing is ever modified. Its exit
codes are its own subset of the contract, stated in `glyph doctor --help`: a
failing check is a convention violation, a check that could not run is no
trustworthy answer — unverified, which is not the same as fine.

**2. Move the lint to the moment you write the message:**

```sh
glyph hook install                 # both hooks (honours core.hooksPath)
glyph hook install commit-msg      # or name the ones you want
```

Two hooks, one gate. `commit-msg` runs `glyph lint --stdin` on the message being
written. `pre-push` hands git's ref list to `glyph hook pre-push`, which lints the
commits the push would add that the remote does not already have, and blocks
**only** when the ref being written is the remote's default branch — anywhere
else it warns and lets the push through (`glyph hook pre-push --help` says why).
Neither hook holds a copy of the convention: `glyph hook --help` states what they
call and what happens without glyph on `PATH`. The verdict `commit-msg` gives is
the verdict CI will give: glyph
reduces the message as git's cleanup will before linting it — from the
repository's `commit.cleanup`, `commit.verbose` and comment prefix
(`core.commentChar`; `auto` is read as `#`), whether an editor runs, and whether
the commit is a `git merge` (DESIGN §2.1). The gaps are stated rather
than hidden: a message a pattern marks `unlandable` passes the hook with a
warning that CI will refuse it; under `[[packages]]` the hook cannot see a diff
that is not yet a commit — `pre-push` judges that one step later; and some of
what git cleans by never reaches a hook — a `--cleanup=`, `-v` or
`--no-verbose` on git's command line among it — so the hook judges those
commits under the repository's settings, and DESIGN §2.1 lists the cases where
its verdict and CI's part.

**3. Wire CI** — three callers, each a few lines around one `uses:` pinned
to a release tag; `glyph doctor` checks that each grants and passes what its
pinned reusable declares. The `lint.yml` caller above is the gate.
`pr-verdict.yml` posts a merge-preview comment on every pull request — its
caller is named `version-preview.yml`, never `pr-verdict.yml`, so a caller
synced into glyph's own fleet can never overwrite the reusable it calls:

```yaml
# .github/workflows/version-preview.yml
on:
  pull_request:
    types: [opened, synchronize, reopened]  # the commits are the input; every push recomputes
concurrency:
  group: version-preview-${{ github.event.pull_request.number }}
  cancel-in-progress: true
permissions:
  contents: read
  pull-requests: write  # the one sticky comment
jobs:
  verdict:
    uses: akira-toriyama/glyph/.github/workflows/pr-verdict.yml@vX.Y.Z  # pin a release tag
```

`release.yml` keeps a rolling draft release maintained on every push to
`main`. Publishing the draft is the release: the tag is created by that human
act, never by CI.

```yaml
# .github/workflows/release.yml
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      dry_run: { description: "Preview only", type: boolean, default: false }
concurrency:
  group: release
  cancel-in-progress: false
permissions:
  contents: write  # the draft
jobs:
  release:
    uses: akira-toriyama/glyph/.github/workflows/release.yml@vX.Y.Z  # pin a release tag
    with:
      dry-run: ${{ github.event.inputs.dry_run == 'true' }}
```

Every input is optional, and that caller is complete for a repository whose
release is the notes: the draft is the generated body alone. `install-notes`
appends a per-repo Markdown block (an `## Install` section) after it;
`app` / `binary` name an artefact the reusable builds and attaches, on a
macOS runner. Each input's `description:` in `release.yml` is its contract.

The draft is always one a human can publish. Every published release sets a
**floor** (per line under `[[packages]]`), wherever its commit sits: a
computed version that is not strictly greater than the highest of them — equal included, because that
tag is taken, or burned for good if its release was deleted — is refused at
exit 4 before anything is written, `--dry-run` included, with both versions
named. DESIGN §4 has the rule and its prices.

A tag-push job — a GoReleaser step rendering the body of the tag it was just
handed — cannot walk with a bare `--since-tag`: at that moment the new tag is
itself the highest, and the range would be empty. `--since-tag=below:vX.Y.Z`
resolves the highest version tag HEAD contains strictly **below** the bound
instead, so cutting a v0.8.3 hotfix while v0.9.0 exists walks from v0.8.2,
and with nothing below the bound it walks the whole history, as a first
release does. The `release-notes` action is that step, packaged: it walks
`below:` the tag, ships the two exits that are permanent at tag time — `1`,
nothing release-worthy, and `3`, the wedge escape's own tag (DESIGN §4) — as a
placeholder body that says why, and fails the job on any other. glyph's own
`goreleaser.yml` runs it over the binary it just built; a repository that cuts
tags by hand runs it over the installed one and hands GoReleaser the file:

```yaml
# after actions/checkout with fetch-depth: 0 — the walk reads the tags and their history
- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z
  with:
    version: vX.Y.Z
    token: ${{ github.token }}
- uses: akira-toriyama/glyph/.github/actions/release-notes@vX.Y.Z
  id: notes
  with:
    token: ${{ github.token }}
# then the GoReleaser step, told its tag rather than left to infer it:
#   with: { args: "release --clean --release-notes ${{ steps.notes.outputs.file }}" }
#   env: { GORELEASER_CURRENT_TAG: "${{ github.ref_name }}" }
```

Two facts of that last step are easy to lose. Unset, `GORELEASER_CURRENT_TAG`
leaves GoReleaser to pick its tag with `git describe`, and a commit carrying
two tags — a release cut on the same tree as its last candidate — makes that a
coin toss (glyph's own v3.3.0 push built `3.3.0-rc.2` artifacts and tried to
upload them onto the rc release; `goreleaser.yml` states the tag). And
`--release-notes` is read inside GoReleaser's changelog pipe, so
`changelog.disable: true` drops the file and ships an empty body — GoReleaser's
own documentation says so, and glyph's v0.8.2 shipped that way; keep the pipe
enabled and silence the built-in generator with an exclude-everything filter,
as `.goreleaser.yaml` does.

Two wired-in behaviours worth knowing before you read them out of the YAML:
the lint caller's optional `push: branches: [main]` arm runs the same rules
over each direct push to the default branch but only **annotates** (exit 3 is
swallowed on that arm alone; DESIGN §6 says why) — a push it cannot judge, a
force push or a ref creation, still fails. And `release.yml` hands its
verdict back as `workflow_call` outputs — `level`, `next`, `current`, `action`,
and `packages` once `[[packages]]` is declared — so a caller can gate
follow-up steps without re-deriving it; gate fail-safe on `""`, which means
**not computed**, never "none". The outputs' own `description:` lines in
`release.yml` are the contract (what each carries, what `packages` strips);
the draft's URL is deliberately not among them (DESIGN §6).

The build step the reusable does run — `./package.sh` in `app` mode,
`./build.sh` in `binary` mode — is told the tag it builds for as
`RELEASE_TAG`. The draft's tag exists as a git ref only once a human
publishes, so a `git describe` inside that script names the *previous*
release (measured 2026-09-25 on facet's v7.0.2 draft: the app inside printed
7.0.1-48-g729d4a7); a script that stamps a version reads
`${RELEASE_TAG:-$(git describe --tags --dirty)}` and keeps the fallback for a
local build. On a packages repository the artefact inputs are refused right
after glyph is installed, before the verdict touches a draft.

An artefact the reusable did not build — a firmware image from a Docker job,
say — is attached by a follow-up job in the caller, not by a new input: gate
it on `needs.release.outputs.next != ''` and on the dry-run input, then
`gh release upload "$next" <files> --clobber`. gh resolves an unpublished
draft by its intended tag (DESIGN §6 measured it), the same call the
reusable's own upload step makes. It is a follow-up job rather than a
`needs:`-fed input because the reusable upserts before it builds — DESIGN §6 argues why. A monorepo
attaches the same way, per line: iterate the `packages` output
(`jq -r '.[] | select(.tag != null) | .tag'`) and upload to each `tag`;
`app` / `binary` name one artifact for one draft and are refused on a
packages repository before the verdict runs (`glyph doctor` names the pair
before the push).

Adopting on a repository with deep history? Tag the **last commit before the
convention** — the parent of the first commit written with a sigil. The walk
reads `tag..HEAD`, which leaves the tagged commit out, so a tag on the first
convention commit drops that commit from every release (measured 2026-11-08
on a scratch repository: a `^` under the tag answered `v0.0.1`; the same tag
on its parent answers `v0.1.0`). The walk baselines at the highest `v*` tag
the released branch holds, so the number must be the highest one: `v0.0.0` on
a repository with no version tags, and above the highest existing tag where
there are some (`v2.3.1` behind a `v2.3.0`; a lower tag is never the base).
Push the tag — the walk runs in CI from the checkout's tags, and the draft's
compare link needs both ends on GitHub. With no tag at all a long history is
refused past a walk cap (fail-loud, one API round-trip per commit is the cost
it refuses; the error names this exact remedy) rather than walked unbounded. A
young repository needs no tag: the first release walks the whole history.

## Commit format

Your `glyph.toml` decides. The shipped presets give you a starting grammar:

```
<:code:>[(<scope>)]<sigil> <subject>     # --gemoji
<type>[(<scope>)]<sigil>: <subject>      # --conventional
```

The sigil is the version signal, and the only thing glyph interprets:
`=` none / `~` patch / `^` minor / `!` major / `%` promote. Under the shipped
presets the prefix (a gemoji, a conventional type) is for the reader and never
decides the version; a pattern you write may give one a fixed sigil, and then
owns what it folds. Examples under the gemoji preset:

```
:sparkles:(ui)^ add a right-click window menu            → minor
:bug:(config)~ keep defaults when an unknown key present → patch
:fire:(api)! drop the --items flag for a positional arg    → major
:memo:(readme)= document the bump model                  → no release
:rocket:% call it 1.0                                    → v1.0.0
```

Which code to write is `glyph emoji`'s answer: an ordered dictionary, read
top to bottom, stop at the first description that fits. It is advice, not
grammar — `glyph emoji --help` states what it prints and what lint does not
read.

**Below 1.0, `!` does not reach 1.0.0.** While the version is `v0.y.z` a
breaking change steps the minor, and `%` is the only way across: it lands
exactly on `v1.0.0`, and from 1.x on it is an ordinary major step. A `%`
commit is still classified as breaking everywhere a level is published,
because the rule shortens the *step*, not the meaning of the commit (DESIGN §3
has the arithmetic and what it collapses).

Under the conventional preset the sigil sits before the colon, so
Conventional Commits' own `feat!:` reads as the major sigil unchanged
(`feat^:` minors, `fix~:` patches, `chore=:` moves nothing, `feat%:` promotes)
— a sigil-less `feat:` is a violation, and a `BREAKING CHANGE:` footer moves
nothing under either preset, because the sigil is the only signal: writing it
down, in the subject, is the point. DESIGN §2 records why plain Conventional
Commits ships as no preset.

Everything is the pattern file's to change: `[[patterns]]` are ordered RE2
regexes (first match wins) over the whole message, and the named group
`semver_sigil` carries the signal. The other pattern keys — a fixed
`semver_sigil`, `skip`, `warn`, `unlandable` — and `exclude_authors` are
explained where `glyph init` writes them, in the comments of the preset
itself; DESIGN §2 argues each, including why the presets claim git's
autosquash subjects (`amend!`, `fixup!`, `squash!`) as unlandable rather than
skipping them. `glyph notes --help` explains how `exclude_authors` and
`[[note.sections]]` meet in the release body.

`note.line` is the same idea for the release body: `$name` placeholders
(`glyph notes --help` lists them) and a `$[ … ]` span that renders only when
every placeholder inside it resolves. That is what lets the shipped line cite a
pull when there is one and drop the parens with it when there is not. `@$author` pages the
contributor only when GitHub knows who that is — the login the API reports for
a listed commit, or the one a `@users.noreply.github.com` author address
carries; a commit from any other address is credited by its git author name,
plain, so a display name that happens to look like a handle never pages the
stranger who owns it.

The commit author's own text — the subject, the scope, any group a pattern
captures — renders as prose that cannot reach past its line: outside the
author's own code spans, glyph puts a backslash before every `<`, every `[`,
the `:` of an `http://` / `https://` / `ftp://`, the `.` of a `www.` and an
entity-shaped `&`, and fences every `@handle` as code, while emphasis keeps
working. The head of a list item opens blocks as the head of a line does, so
the literal `- ` of a template protects nothing: a value that lands there loses
its indent, and a heading, a block quote, a list marker, a code fence, a
thematic break, an HTML block opener, a footnote definition or a table
delimiter row at its head takes a backslash too (`## x` lands as `\## x`, the
text `## x` inside the item), and a task-list marker takes one between its
brackets, the one place GitHub's checkbox filter reads (`[ ] ship it` lands as
`\[\ ] ship it`, the text `[\ ] ship it`). A code span the author wrote passes
through byte for byte — ``unwrap `Optional<Any>` and [x]`` lands as
``unwrap `Optional<Any>` and \[x]``. A subject written as

```
:sparkles:^ [click here](https://evil.example) and @octocat and <img src=x onerror=1> and **bold**
```

reaches the notes as

```
:sparkles:^ \[click here](https\://evil.example) and `@octocat` and \<img src=x onerror=1> and **bold**
```

— no link, no mention, no live tag, the bold still bold. A raw tag is not
only a risk: GitHub deletes an unknown one outright (`Optional<Any>` renders
as `Optional`), so the escape is also what keeps real subjects whole. DESIGN
§2 and §3 hold the rulings, and `internal/markdown/escape.go` the rules with
the renderer measurements behind each.

Two more of a commit's facts live below its subject, in the **git trailer
block**, and glyph parses that block rather than grepping for the lines it
hopes are trailers. `$coauthors` is the names its `Co-authored-by:` trailers
credit, in message order — never a mention, because a co-author address
establishes no identity glyph can verify. `[[note.trailers]]` binds one more
of the repository's own vocabulary to a placeholder:

```toml
[[note.trailers]]
token = "Why"   # the git trailer this entry reads
name  = "why"   # the $placeholder note.line writes
```

A declared trailer renders as fenced prose, like any other text a commit
author typed, and an absent one resolves empty so the `$[ … ]` span carrying
it drops. What a trailer block IS follows git exactly: the last paragraph of
the message, above any `---`, every line of it trailer-shaped — **one prose
line in it and the whole block is not a trailer block**, credits included, so
write the trailers together at the very bottom and indent any wrapped line.

## Packages — several version lines in one repository

A repository that versions more than one thing declares each as a
`[[packages]]` subtree; each gets its own tag line, its own walk base and
its own verdict, and the mode is **independent** only — every package moves
for its own reasons (a repository that wants everything to move together is
the single line it already is):

```toml
[[packages]]
path = "haiku"          # tags are haiku/vX.Y.Z — the Go multi-module rule, not configurable
                        # (a major version subdirectory folds into the major: pubsub/v2 tags as pubsub/v2.x.y)
# name = "haiku"        # what a commit scope may call it; default: the last path segment
                        # (a major version subdirectory keeps its parent: pubsub/v2) — a name
                        # no pattern's scope can spell does not load

[[packages]]
path = "."              # optional: the root package keeps the bare vX.Y.Z line
name = "core"           # its default "." is no preset scope's word: under the presets name is required
```

A path must also be one git can tag: a segment `git check-ref-format`
refuses (`a b`, `x.lock`, `.hidden`, `c~d`, …), or a leading `-`, which
`git tag` refuses, does not load.

Which line a commit moves is read from its **own diff**, per commit — never
from a pull's net diff, which cannot tell a `^` under one module from a `~`
under another: files under a package move that package (the longest path
wins), a commit under no package moves the package its scope names
(`:sparkles:(haiku)^ …` on a shared README), and a version claim nothing can
carry — a shared-only `~`, `^`, `!` or `%` with no such scope, or a scope
naming a package that owns none of the diff's files — is a lint-class refusal
(exit 3) wherever a diff is read: `lint --range`, `bump` / `notes --range`,
the release walk, and the pre-push hook on the default branch. DESIGN §4.1
rules 1–3 are the full rule, with the escapes the refusal names. Files come
from local git for every commit the branch holds and from the API for a
squash-merged pull's inner commits, one request each.

`bump` and `notes` then answer per line. **A tag names a line**:
`--since-tag=haiku/v0.1.0` or `--since-tag=below:haiku/v0.2.0` selects haiku
alone (what a tag-time notes step needs; a release candidate on the line,
`haiku/v0.2.0-rc.1`, selects it the same way and steps from its highest plain
tag), a bare `--since-tag` walks every line from its own highest tag the
released branch holds, and `--range` attributes from local git with every
commit pending on every line.
A package with no tag yet is baselined
by cutting `<prefix>v0.0.0` (`haiku/v0.0.0`) at the commit before its first
change — `<prefix>vN.0.0` on a line whose path ends in a major-version
subdirectory (`pubsub/v2` → `pubsub/v2.0.0`); the refusal names the tag. On
stdout `bump` prints the next **tag** of every line that moves, one per line
(`haiku/v0.2.0`), and exits `1` only when every line folds to none; `--json`
carries `packages: [{path, current, level, next, commits, reason}]` with the
scalar `current` / `level` **empty** and `next` absent, as on a none verdict —
there is no one line for them to describe, so a consumer reading only the
scalars fails safe provided it reads an absent key as empty
(`jq -r '.next // empty'`; a plain `.next` prints `null`). `notes`
mirrors it: `packages: [{path, sections}]`, and on stdout one body per line
under a `# <path>` heading (bare when one line is selected). `--pr` is
refused on both — a pull's listing carries messages and no files.

`release` keeps **one rolling draft per line** — `haiku/v0.2.0` and
`curry/v0.1.1` side by side, each converged on its own tag prefix (never a
second draft on a line, retagged in place, a line that folds to none loses
its residual draft — or keeps a `<path>/Unreleased` placeholder with
`draft_on_none` on), every line's upsert written before any stray is deleted
(a write that fails on the second line leaves the first standing and exits
`4`), each line's draft — and each line's `notes --since-tag` body — closing
its notes with a compare link from that line's own base tag (none for a line
with no tag), `--footer-file` appended to every draft, and `--json` carrying
`packages: [{path, current, level, next, tag, body, action, url, commits,
reason}]` with the scalar `current` / `level` / `action` empty, `next` /
`tag` / `body` / `url` absent, and `target` shared — omitted, as the single
line omits it, when no line has a draft to write. A bare `vX.Y.Z` draft in a
repository that declares packages but no root package is the single
line's residue and is deleted with a notice on the first packages run —
`release --dry-run` names it without deleting it, so move any hand-written
prose it carried into the line's own draft first. GitHub's
Latest badge lands on whichever line a human published last; glyph never
sets `make_latest`. A tag that selects one line converges that line alone.

`preview --pr` posts one headline per line the pull touches — `**haiku** —
🔼 Merging this PR raises **minor** — the next release becomes
**haiku/v0.1.0 → haiku/v0.2.0**.` — with one commit table per line and the
notes preview per line; a line the pull does not touch is not mentioned, and
a shared-only `^` is refused at exit 3 here first, while the branch can
still be fixed. The reusables (`pr-verdict.yml`, `release.yml`) render what
the binary hands them: `release.yml` passes the per-line verdicts through as
its `packages` output and refuses `app` / `binary` on a packages repository,
and every `pr-verdict.yml` output — `breaking` included — is `""` there, not
computed, so a gate fails safe.
A repository without `[[packages]]` is untouched by all of this, byte for
byte.

## Exit codes

`0` ok · `1` no release-worthy change · `2` usage · `3` convention violation
(a commit under `lint`, the repository's own configuration under `doctor`) ·
`4` no trustworthy answer — API/git/IO, or a refusal to hand down a verdict
glyph will not stand behind (an incomplete walk, the published floor, the
whole-history walk cap, a `release` body over GitHub's cap, a `release` write
off the default branch) · `130` interrupted.

The integers are a frozen machine API — CI gates branch on the exact value, so
assert the code, never truthiness (`if glyph …` cannot tell `3` from `2`).

glyph's stderr has a shape too — `::` workflow annotations, then, on a failure
that is not silent, one JSON error envelope, written last — so read the
envelope the way the *error envelope* entry in
[`docs/glossary.md`](docs/glossary.md#6-exit-codes-and-streams) says, never by
handing stderr whole to `jq`.

## Working on glyph

Maintaining glyph: see [`CLAUDE.md`](CLAUDE.md).

## License

MIT © akira-toriyama
