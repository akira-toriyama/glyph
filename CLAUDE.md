# CLAUDE.md — glyph

Only what a session gets wrong *without* this file. Everything else already has a home, and
this file points at it rather than keeping a second copy. Each section leads with its orders;
the reasons and the incidents that ratified them follow.

## Blast radius

- Do not treat a merge to `main` as a release: nobody runs it until a tag is cut **and** the
  six pin sites move.
- Before any change meant to reach the fleet, read
  [`glyph-rollout-runbook.md`](https://github.com/akira-toriyama/.github/blob/main/docs/glyph-rollout-runbook.md)
  for the sequence and
  [`fleet-change-policy.md`](https://github.com/akira-toriyama/.github/blob/main/docs/fleet-change-policy.md)
  for how far to verify first.

Why: every fleet repo runs this binary as its commit-lint gate and merge-preview verdict, but
only at **the tag it pins**, and no single mechanism moves all six pin sites (the runbook names
what moves each, and that a major's pin pulls are a human merge). So a wrong
answer is wrong in this repo until a tag is cut, then wrong fleet-wide until the pins move
back.

## Where the facts live

- Route the question to its home and answer from the home, never from memory:
  - surface, flags, why glyph exists → **README.md**
  - the model and the ratified decisions → **docs/DESIGN.md**
  - which gemoji a commit is written with → `go run ./cmd/glyph emoji` (the ordered dictionary;
    its rationale and the codes it deliberately leaves out → **DESIGN §2**, "The gemoji dictionary")
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
  entry says (since v3.0.0 — the v2 engine generation, DESIGN §2 — there is no embedded
  table and no `glyph rules`). Never retype a pattern into prose.

## Exit codes are a machine API

- Treat a new exit code as a breaking change; the integers are frozen.
- **Assert the exact code, never truthiness** — `if glyph …; then` cannot tell `3` from `2`.
- Do not add another prose copy: implemented once in `internal/core/errors.go`; the copies
  `internal/workflows/exitcodes_test.go` holds lockstep are listed in
  `internal/workflows/doc.go`'s header. `glyph doctor --help`'s Exit paragraph is doctor's own
  subset (its `4` is a check that could not run), and no test holds it — renumbering a code
  means editing it by hand.

Why: every out-of-Go consumer `TestOutOfGoConsumersBranchOnTheContractIntegers` inventories
branches on the exact value. `lint --stdin=false` once exited `3`, telling a CI gate that a
commit violated the convention when none had been submitted.

## `glyph` on PATH is not your working tree

- To exercise your own tree, use `sh build.sh` (stamps the version from git) or
  `go run ./cmd/glyph` — never `glyph` on PATH, in the primary checkout or in a worktree.
- Run `glyph lint --range origin/main..HEAD` before committing: CI lints this repo's own
  commits.

Why: the wrapper `git archive`s the **hardcoded** clone's `origin/main`
(`/Volumes/workspace/github.com/akira-toriyama/glyph`; its header has the mechanism) and
builds that, never any working tree — so in every checkout, your unpushed work is not what
runs, and it reports success. The installed hooks call the same wrapper and block **only** on
exit 3, waving every other failure through by design (DESIGN §7, `commit-msg-hook-fires`), so
a broken local gate never looks like a verdict.

## Verification

- Run **`sh scripts/check.sh`** for the full local run — the only thing that reproduces CI
  without pushing (nothing in the repo invokes it, which is why it is named here). Read the
  final line, not the absence of errors: `✓ N/N mirrored gates passed — NOT mirrored: …`
  (check.sh's header says why a gate that did not run withholds it).
  - Missing tools and govulncheck: check.sh's golangci-lint refusal and its go-vuln comment
    say what supplies each; govulncheck is fetched `@latest`, so the run needs the network.
  - Concurrent runs each need their own `TMPDIR` as well as their own `GOLANGCI_LINT_CACHE`:
    golangci-lint's run lock lives in `TMPDIR`, so two runs sharing it fail one with
    `parallel golangci-lint is running`.
  - It reads the hub at the tag the callers pin for three gates: bite, the lint floor and
    repo-policy (where it finds the hub and how it fetches the tag: check.sh's `hub_at`
    comment). When the hub's origin cannot be reached, the clone's copy of the tag judges
    and the final line says so. bite reads **committed** history only — an uncommitted
    test is invisible to it.
  - Do not "fix" its two deliberate gaps, `-count=1` and `taplo`: check.sh says why at its
    `go test` line and in its NOT-mirrored manifest.
- Run **`sh scripts/mutations.sh`** to prove fixes by re-breaking them — each
  `testdata/mutations/*.patch` breaks one argued decision and the ledger names the test that
  must then fail. Adding a decision? Add a row (`testdata/mutations/README.md` says how).
  When a patch stops applying, re-derive it — deleting the row is how a decision loses its
  only defender.
- Run **`sh scripts/fleet-preflight.sh <candidate-binary>`** before cutting a tag, not before
  pushing (`sh build.sh` first; its header and refusals say what it needs, and the header why
  it is not a `check.sh` gate).
  It answers what the runbook cannot: of the repos that pin glyph, how many does this release
  change the verdict for? Read its output as the runbook's step 0 says.
  - Two corrections not to re-make: the lint differential compares the **finding set**, not
    the exit code (fleet-preflight.sh's `probe_lint` comment says why), and the bump probe runs `--since-tag`, the walk
    `release.yml` actually performs, not `--range` (the script's header, at its `bump` probe,
    says where they disagree).
- Fire live ammunition in **`glyph-test`** — it is this repo's permanent live-fire harness
  (user-ratified 2026-08-27), so real PRs, releases and force-pushed branches there are fair
  game. Do not wipe its history or tags: `e2e-v2.yml` and `livefire.yml` reference old tags
  as frozen coordinates, and the rollout runbook names it as the canary.
- Fire **packages** ammunition in **`glyph-monorepo-test`** (created 2026-09-10, the same
  standing as glyph-test). Its README lists its lines, what each workflow does and the pulls
  and merge commit that are frozen; do not rewrite them.
- Patch both harnesses first when a change narrows the `glyph.toml` schema — a frozen
  coordinate carries the `glyph.toml` of its day, so the change reds both walk tiers until
  each harness edits its frozen copy (glyph-test#130, glyph-monorepo-test#43) — and dispatch
  e2e from main, because the drafts tiers write.

## Generated and pinned data — regenerate, never hand-edit

- `glyph.toml` ← `glyph init --gemoji`, guarded by `TestGlyphOwnConfigIsTheGemojiPreset` (byte
  equality). Edit the source — `internal/config/presets/gemoji.toml`, or
  `internal/config/presets/packages.snippet` for the commented `[[packages]]` paragraph every
  preset carries — then regenerate with `go run ./cmd/glyph init --gemoji --force`.
- Treat `testdata/fuzz` as a regression corpus, not scratch: a **new** file there is the
  engine reporting an input the code fails. Rename it after the defect (Go replays any
  filename in the target directory) and commit it; never delete one to go green.
  `git ls-files '*testdata/fuzz/*'` lists the committed entries, and plain `go test` replays
  each as a named subtest. A new `Fuzz` target needs no CI or check.sh edit
  (`scripts/fuzz-smoke.sh`'s header says why).

## `internal/emoji/table.json` is hand-edited, in order

- Insert a new kind where its specificity puts it — DESIGN §2, "The gemoji dictionary", says
  the order and what each field carries — and leave `emoji` as `"?"`:
  `go test ./internal/emoji` then prints the code points GitHub maps the shortcode to.
- The oracle is `internal/emoji/testdata/gemoji.tsv`, a dated `GET /emojis` snapshot — refresh
  it with the command in its header, never by hand. Nothing regenerates the table and nothing
  else reads it; the argued decisions (what is deliberately absent, and why) are DESIGN §2,
  "The gemoji dictionary".

## CI gates that fail for reasons the diff does not show

- **bite** — runs the tests a PR adds or changes against the pre-PR source. Its two waivers,
  and the exact form each must take, are in
  [`docs/go-bite.md`](https://github.com/akira-toriyama/.github/blob/main/docs/go-bite.md).
  Its verdict is "**at least one** bites", so it is weaker than it sounds — and weaker than
  `scripts/mutations.sh`, which requires every named test to fail.
- **`internal/workflows`** — tests the workflow YAML itself; `internal/workflows/doc.go` names
  its chief guards, the `@vX.Y.Z` placeholder in commented stubs among them; its failure
  messages name the rest and carry the incident. When you add a guard that asserts an absence, copy the
  **positive control** shape of `TestNaiveGrepSeesNoPinInTheReusables`: a regex guard with no
  canary proving the pattern still matches a real instance is how a fleet invariant dies
  green.
- **zizmor** — full-SHA pin third-party actions (`.github/zizmor.yml`'s `unpinned-uses`
  policy names the ones that may ride tags); give any new `actions/checkout` an explicit
  `persist-credentials:`.

## Files that are not yours to edit

- If a file's header says fleet-sync manages it, edit the canonical copy in
  `akira-toriyama/.github/fleet/` instead — the local copy is overwritten on the next sync.
  The canonical set is the Files list in
  [`fleet/README.md`](https://github.com/akira-toriyama/.github/blob/main/fleet/README.md),
  not a copy kept here.
- Exception: `.github/dependabot.yml` is **assembled**, not copied — its header says how, and
  why a base-only overwrite erases a repo's real ecosystems — so glyph's copy legitimately
  differs from the hub's.
- Never let a caller share a filename with a reusable. glyph **defines** `lint.yml` /
  `release.yml` / `pr-verdict.yml` and **consumes** them as `commit-lint.yml` /
  `version-preview.yml`; ignoring the split once overwrote `pr-verdict.yml`.

## One edit nothing guards

- After changing `go.mod` / `go.sum` by hand, run `sh scripts/repin-vendorhash.sh` (needs
  nix) before pushing — dependabot's gomod pulls are re-pinned by the hub's fleet-repin. No
  CI job here catches a stale `vendorHash`.
