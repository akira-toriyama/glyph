# CLAUDE.md — glyph

Only what a session gets wrong *without* this file. Everything else already has a home, and
this file points at it rather than keeping a second copy. Each section leads with its orders;
the reasons and the incidents that ratified them follow.

## Blast radius

- Do not treat a merge to `main` as a release: nobody runs it until a tag is cut **and** the
  five pin sites move.
- Before any change meant to reach the fleet, read
  [`glyph-rollout-runbook.md`](https://github.com/akira-toriyama/.github/blob/main/docs/glyph-rollout-runbook.md)
  for the sequence and
  [`fleet-change-policy.md`](https://github.com/akira-toriyama/.github/blob/main/docs/fleet-change-policy.md)
  for how far to verify first.

Why: every fleet repo runs this binary as its commit-lint gate and merge-preview verdict, but
only at **the tag it pins**. No single mechanism moves all five pin sites — `fleet-sync`
byte-copies two, `glyph-pin-rewrite` opens a pull request for the other three (they live in
files each repo owns), and merging those is still a human step. So a wrong answer is wrong in
this repo until a tag is cut, then wrong fleet-wide until the pins move back.

## Where the facts live

- Route the question to its home and answer from the home, never from memory:
  - surface, flags, why glyph exists → **README.md**
  - the model and the ratified decisions → **docs/DESIGN.md**
  - vocabulary a task body may use oddly (*merge point*, *footprint*, *residual* vs *stale*
    draft) → **docs/glossary.md**
  - commit convention →
    [`.github/CONTRIBUTING.md`](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md)
    (canonical; `docs/commit-convention.md` here is only a pointer — do not answer a
    convention question locally)
  - doc rules (one home per fact, no stored translations) →
    [`doc-consistency-policy.md`](https://github.com/akira-toriyama/.github/blob/main/docs/doc-consistency-policy.md)
- Read the relevant DESIGN section **before** changing behaviour — the obvious fix was usually
  considered and rejected there, with the incident that killed it named. Route by topic; the
  numbers do not read the way you would guess: **§4** release-time walk (`--since-tag`,
  footprints, the wedge), **§3** classification and the fold, **§2** message grammar and lint
  rules, **§5** architecture and the stream contract, **§7** `doctor`.
- Treat `glyph.toml` as the grammar: a commit means what its first matching `[[patterns]]`
  entry says (since v2 there is no embedded table and no `glyph rules`). Never retype a
  pattern into prose.

## Exit codes are a machine API

- Treat a new exit code as a breaking change; the integers are frozen.
- **Assert the exact code, never truthiness** — `if glyph …; then` cannot tell `3` from `2`.
- Do not add another prose copy: implemented once in `internal/core/errors.go`, spelled out in
  README.md (twice — the table and `doctor`'s subset), DESIGN §5 and glossary §6, and
  `internal/workflows/exitcodes_test.go` holds every copy lockstep.

Why: every fleet repo's lint gate, glyph's three reusable workflows, the installed commit-msg
hook and `scripts/check.sh` all branch on the exact value. `lint --stdin=false` once exited
`3`, telling a CI gate that a commit violated the convention when none had been submitted.

## `glyph` on PATH is not your working tree

- To exercise your own tree, use `sh build.sh` (stamps the version from git) or
  `go run ./cmd/glyph` — never `glyph` on PATH from inside a worktree.
- Run `glyph lint --range origin/main..HEAD` before committing: CI lints this repo's own
  commits.

Why: the wrapper builds a **hardcoded** clone path
(`/Volumes/workspace/github.com/akira-toriyama/glyph`), so inside a git worktree it builds and
runs the *primary* checkout — code you did not write — and reports success. The installed hooks
call the same wrapper and block **only** on exit 3, passing every other failure through by
design, so a tree that does not compile turns the local gate into a silent no-op.

## Verification

- Run **`sh scripts/check.sh`** for the full local run — the only thing that reproduces CI
  without pushing (nothing in the repo invokes it, which is why it is named here). Read the
  final line, not the absence of errors: `✓ N/N mirrored gates passed — NOT mirrored: …`
  (it declares its gates in a manifest at the top and **reconciles** at the end — a mirrored
  gate that did not run means no `✓` line and a non-zero exit).
  - It exits 2 on a dirty tree; `ALLOW_DIRTY=1` runs anyway and says so in the final line
    (a green over uncommitted work is a claim about nothing reproducible).
  - A missing tool is a failure, not a skip — `nix develop` supplies `golangci-lint` and
    `govulncheck`.
  - Its `bite` step needs the `akira-toriyama/.github` clone beside this repo (or
    `GLYPH_FLEET_HUB`) and reads **committed** history only — an uncommitted test is
    invisible to it.
  - Do not "fix" these two gaps: `-count=1` is absent because `go-ci.yml` omits it too (add
    it by hand only when chasing a flake), and `taplo` is unmirrored for the same reason
    actionlint is — the hub's reusable pins the tool version, so a local run would judge with
    a different formatter.
- Run **`sh scripts/mutations.sh`** to prove fixes by re-breaking them — each
  `testdata/mutations/*.patch` breaks one argued decision and the ledger names the test that
  must then fail. Adding a decision? Add a row (`testdata/mutations/README.md` says how).
  When a patch stops applying, re-derive it — deleting the row is how a decision loses its
  only defender.
- Run **`sh scripts/fleet-preflight.sh <candidate-binary>`** before cutting a tag, not before
  pushing (`sh build.sh` first; needs `gh`, `jq`, a token and the fleet cloned as siblings —
  which is why it is not a `check.sh` gate: check.sh mirrors CI, and no CI job asks this).
  It answers what the runbook cannot: of the repos that pin glyph, how many does this release
  change the verdict for? It is a **differential** — every probe runs twice, once with the
  pinned tag and once with the candidate — so a repo that is already red for its own reasons
  is not a finding.
  - Read `lint` moves as a **prediction** (CI lints a pull request's own commits; nothing
    already merged is re-judged) and `bump` moves as **retroactive** (that repo's next
    release cuts a different version, having changed nothing).
  - Read the `✓` line: it carries every weakening of the claim, and any weakening stamps the
    headline count as a **FLOOR**.
  - Two corrections not to re-make: the lint differential compares the **finding set**, not
    the exit code (lint has only two answer codes, so a repo already at `3` could never
    register a move — that hid eight repos), and the bump probe runs `--since-tag`, the walk
    `release.yml` actually performs, not `--range` (they disagree: on canon, `--range`
    refuses the whole range over one `:robot:` subject while `--since-tag` returns v2.0.1).
- Fire live ammunition in **`glyph-test`** — it is this repo's permanent live-fire harness
  (user-ratified 2026-08-27), so real PRs, releases and force-pushed branches there are fair
  game. Do not wipe its history or tags: `e2e-v2.yml` and `livefire.yml` reference old tags
  as frozen coordinates, and the rollout runbook names it as the canary.

## Generated and pinned data — regenerate, never hand-edit

- `glyph.toml` ← `glyph init --gemoji --v1-window`, guarded by
  `TestGlyphOwnConfigIsTheComposedV1WindowPreset` (byte equality). Edit the sources —
  `internal/config/presets/gemoji.toml` and `presets/v1window.snippet` — then regenerate with
  `go run ./cmd/glyph init --gemoji --v1-window --force`.
- Rewriting an `-update` golden (`internal/cli/testdata/release_dry_run.golden.md`,
  `internal/markdown/testdata/exported-surface.golden.txt`) requires a
  `Golden-change: <reason>` trailer on every non-merge commit (golden-gate). Read the golden
  diff as the format spec it is before committing it — `-update` makes a rendering bug look
  intentional.
- Treat `testdata/fuzz` as a regression corpus, not scratch: a **new** file there is the
  engine reporting an input the code fails. Rename it after the defect (Go replays any
  filename in the target directory) and commit it; never delete one to go green. Six entries
  are committed, all under `internal/markdown/`, and plain `go test` replays each as a named
  subtest. A new `Fuzz` target needs no CI or check.sh edit — both loops discover targets.

## CI gates that fail for reasons the diff does not show

- **bite** — runs the tests a PR adds or changes against the pre-PR source. To waive it, give
  a reason in one of the two accepted places: `bite-exempt: <reason>` in the test's doc
  comment, or a `Bite-exempt: <reason>` **git trailer** on every non-merge commit (a real
  trailer, parsed by `git interpret-trailers` — a line inside a body paragraph waives
  nothing). Its verdict is "**at least one** bites", so it is weaker than it sounds — and
  weaker than `scripts/mutations.sh`, which requires every named test to fail.
  Rationale: [`docs/go-bite.md`](https://github.com/akira-toriyama/.github/blob/main/docs/go-bite.md).
- **dist-gate** — touching a distribution-layer file (the three reusables, `goreleaser.yml`,
  the install action, `.goreleaser.yaml`) with no `internal/workflows` `*_test.go` change
  fails the PR; waive with a `Dist-gate-exempt: <reason>` trailer on every non-merge commit.
  Why: bite acts on Go diffs only, and YAML `:bug:` fixes have shipped with zero tests
  before. The file set lives twice on purpose — `scripts/dist-gate.sh` and
  `internal/workflows/distgate_test.go` — and the test fails if the copies drift.
- **`internal/workflows`** — tests the workflow YAML itself (the install action's single
  source, `fetch-depth: 0` on the walking reusables, the `sed -n '/^[{]/,$p'` envelope sieve
  a step needs before `jq`). Read its failure messages; they carry the incident. Keep the
  `@vX.Y.Z` placeholder in a **commented** caller stub (a tag is cut on an already-frozen
  tree, so a concrete version in a comment is stale on arrival) and pin a real `vX.Y.Z` in
  every **executable** `uses:` — it fails you for fixing the placeholder. When you add a
  guard that asserts an absence, copy the **positive control** shape four of its eight guards
  carry: a regex guard with no canary proving the pattern still matches a real instance is
  how a fleet invariant dies green.
- **zizmor** — full-SHA pin third-party actions (`actions/*` and `akira-toriyama/*` may ride
  tags); give any new `actions/checkout` an explicit `persist-credentials:`.

## Files that are not yours to edit

- If a file's header says fleet-sync manages it, edit the canonical copy in
  `akira-toriyama/.github/fleet/` instead — the local copy is overwritten on the next sync.
  The canonical set is the Files list in
  [`fleet/README.md`](https://github.com/akira-toriyama/.github/blob/main/fleet/README.md),
  not a copy kept here.
- Exception: `.github/dependabot.yml` is **assembled**, not copied — a base plus one
  `dependabot.d/<ecosystem>.yml` block per manifest present — so glyph's copy legitimately
  differs from the hub's, and a base-only overwrite erases a repo's real ecosystems.
- Never let a caller share a filename with a reusable. glyph **defines** `lint.yml` /
  `release.yml` / `pr-verdict.yml` and **consumes** them as `commit-lint.yml` /
  `version-preview.yml`; ignoring the split once overwrote `pr-verdict.yml`.

## One edit nothing guards

- After bumping `go.mod` / `go.sum`: reset `flake.nix`'s `vendorHash` to
  `pkgs.lib.fakeHash`, run `nix build`, paste the hash it reports. No CI job catches a stale
  one.
