# CLAUDE.md — glyph

Only what a session gets wrong *without* this file. Everything else already has a home, and
this file points at it rather than keeping a second copy.

**Blast radius, stated correctly.** Every fleet repo runs this binary as its commit-lint gate
and its merge-preview verdict — but only at **the tag it pins**. Merging to `main` here reaches
nobody; a rollout is a separate act that moves five pin sites, and no single mechanism moves all
five — `fleet-sync` byte-copies two, `glyph-pin-rewrite` opens a pull request for the other three
(they live in files each repo owns), and merging those is still a human step. So a wrong answer is
not "wrong everywhere at once"; it is wrong in this repo until a tag is cut, and then wrong
fleet-wide until the pins move back. The sequence and the five sites are
in [`akira-toriyama/.github/docs/glyph-rollout-runbook.md`](https://github.com/akira-toriyama/.github/blob/main/docs/glyph-rollout-runbook.md);
how far to verify before touching the fleet is in
[`docs/fleet-change-policy.md`](https://github.com/akira-toriyama/.github/blob/main/docs/fleet-change-policy.md).

## Where the facts live

In this repo:

- **README.md** — the surface, the flags, why glyph exists.
- **docs/DESIGN.md** — the model and the ratified decisions. Route by topic, because the
  section numbers do not read the way you would guess: **§4** is the release-time walk
  (`--since-tag`, footprints, the wedge), **§3** is classification and the fold, **§2** the
  message grammar and the lint rules, **§5** architecture and the stream contract, **§7**
  `doctor`. Read the relevant one before changing behaviour — the obvious fix was usually
  considered and rejected there, with the incident that killed it named.
- **docs/glossary.md** — the vocabulary. If a task body uses a word oddly (*merge point*,
  *footprint*, *residual* vs *stale* draft), it is defined there.
- **`glyph rules`** — the gitmoji table, embedded from `internal/gitmoji/rules.json` so the
  pinned binary *is* the pinned rules. Never retype the table, or one row of it, into prose.

Outside it: the commit convention is canonical in
[`.github/CONTRIBUTING.md`](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md)
and `docs/commit-convention.md` here is only a pointer — do not answer a convention question
locally. Doc rules (one home per fact, no stored translations) are in
[`docs/doc-consistency-policy.md`](https://github.com/akira-toriyama/.github/blob/main/docs/doc-consistency-policy.md).

## Exit codes are a machine API

Implemented once in `internal/core/errors.go`; spelled out in README.md (twice — the table and
`doctor`'s subset) and DESIGN §5. Do not add another prose copy. What matters is that the
integers are frozen: every fleet repo's lint gate, glyph's three reusable workflows, the
installed commit-msg hook and `scripts/check.sh` all branch on the exact value, so a new code
is a breaking change. **Assert the code, never truthiness** — `lint --stdin=false` once exited
`3`, telling a CI gate that a commit violated the convention when none had been submitted, and
`if glyph …; then` cannot tell `3` from `2`.

## `glyph` on PATH is not your working tree

The wrapper builds a **hardcoded** clone path (`/Volumes/workspace/github.com/akira-toriyama/glyph`).
Inside a git worktree it therefore builds and runs the *primary* checkout — code you did not
write — and reports success. To exercise your own tree use `sh build.sh` (stamps the version
from git) or `go run ./cmd/glyph`. The same wrapper is what the installed commit-msg hook calls,
and that hook blocks **only** on exit 3 and passes every other failure through by design, so a
tree that does not compile turns the local gate into a silent no-op.

`glyph lint --range origin/main..HEAD` before committing: CI lints this repo's own commits.

## Verification

- **`sh scripts/check.sh`** — the full local run, and the only thing that reproduces CI without
  pushing. Nothing in the repo invokes it, which is why it is named here.
  It declares its gates in a manifest at the top and **reconciles** at the end: every mirrored
  gate must actually have run or there is no `✓` line and the exit is non-zero, and gates that
  are deliberately *not* mirrored are listed with the reason. So read the final line, not the
  absence of errors — `✓ 11/11 mirrored gates passed — NOT mirrored: …`.
  Three things it will refuse to do rather than mislead you: it **exits 2 on a dirty tree**
  (`ALLOW_DIRTY=1` runs anyway and says so in the final line, because a green over uncommitted
  work is a claim about nothing reproducible); a **missing tool is a failure, not a skip**
  (`nix develop` supplies `golangci-lint` and `govulncheck`); and its `bite` step needs the
  `akira-toriyama/.github` clone beside this repo (or `GLYPH_FLEET_HUB`) — that step reads
  **committed** history only, so an uncommitted test is invisible to it.
  Two things that look like gaps and are not: `-count=1` is absent because `go-ci.yml` omits it
  too (adding it would move this script *away* from CI — add it by hand when chasing a flake),
  and `taplo` is unmirrored because glyph tracks zero `.toml` files.
- **`sh scripts/mutations.sh`** — the mutation ledger: each `testdata/mutations/*.patch` breaks
  one argued decision and the ledger names the test that must then fail. This is where the house
  rule "a fix is shown by re-breaking it, not by a green suite" is mechanised. Adding a decision?
  Add a row — `testdata/mutations/README.md` says how, and what to do when a patch stops
  applying (re-derive it; deleting the row is how a decision loses its only defender).
- **`sh scripts/fleet-preflight.sh <candidate-binary>`** — run this **before cutting a tag**, not
  before pushing. It answers the one question the rollout runbook cannot: of the repos that pin
  glyph, how many does this release change the verdict for? It is a **differential** — every probe
  runs twice over the same range, once with the tag the fleet is pinned at and once with the
  binary you are about to ship — so a repo that is already red for its own reasons is not a
  finding, and the one repo this release breaks cannot hide among them. The candidate is an
  argument because the point is to judge a binary that has no release yet; `sh build.sh` first.
  Two things to read in its output rather than assume: `lint` moves are a **prediction** (CI lints
  a pull request's own commits, so nothing already merged is re-judged) while `bump` moves are
  **retroactive** (that repo's next release cuts a different version, having changed nothing), and
  the `✓` line carries every weakening of the claim — unanswered gates make the headline count a
  floor. It needs `gh` and the fleet cloned as siblings, which is why it is not a `check.sh` gate:
  check.sh mirrors CI, and no CI job asks this.

## Generated and pinned data — regenerate, never hand-edit

- `docs/gitmoji-table.md` ← `gitmoji.Table.Markdown()`, guarded by `TestMarkdownGolden`;
  regenerate with `go test ./internal/gitmoji -run Golden -update`.
- `internal/notes/testdata/*.golden.md` ← `notes.Render()`, guarded by `TestRenderGolden`;
  regenerate with `go test ./internal/notes -update`.
- `-update` makes a rendering bug look intentional. Read the golden diff as the format spec it
  is before committing it.
- `internal/bump/testdata/fleet-corpus.tsv` is the exception that proves the rule: it has **no
  `-update`, on purpose**, because the failure it defends against is an author who narrows the
  parser, sees a wall of red and regenerates. It freezes 3,049 real fleet subjects against the
  verdict each produces, and the number that matters is that **75 of its 83 breaking rows are
  breaking only because a retired Conventional token carried the `!`** — nine in ten of the
  fleet's breaking commits hang on one line in `parser.go`, and losing it costs those repos a
  major they never learn they were owed. `sh scripts/fleet-corpus.sh` refreshes it by
  **appending** subjects the fleet has written since (and refuses to run if the stored verdicts
  already disagree with the tree, so a broken tree cannot bake its own output in as truth);
  changing what a stored subject *means* is a hand edit. Public repos only — six of the
  thirty-five are private and their subjects are not this repo's to publish.
- `internal/gitmoji/gitmoji.go` pins `CodeCount = 75` and `Load()` rejects a table of any other
  size — adding a code to `rules.json` without bumping it breaks **every** command at startup,
  not one test.
- `testdata/fuzz` is a regression corpus, not scratch. Seven entries are committed (six under
  `internal/markdown/`, one under `internal/parser/`) and plain `go test` replays each as a named
  subtest. A **new** file there is the engine reporting an input the code fails: the engine writes
  it under a hash name, so rename it after the defect (Go replays any filename in the target
  directory; six of the seven are named that way) and commit it. Never delete one to go green.
  A new `Fuzz` target needs no CI or check.sh edit — both loops discover targets.

## CI gates that fail for reasons the diff does not show

- **bite** — runs the tests a PR adds or changes against the pre-PR source. Its verdict is
  "**at least one** bites": a single biting test greens the job and the rest are only flagged in
  the summary, so it is weaker than it sounds (and weaker than `scripts/mutations.sh`, which
  requires every named test to fail). Two opt-outs, both demanding a reason: `bite-exempt:
  <reason>` in the test's doc comment, or a `Bite-exempt: <reason>` **git trailer** on every
  non-merge commit — a real trailer, parsed by `git interpret-trailers`, so a line sitting inside
  a body paragraph waives nothing.
  Rationale: [`docs/go-bite.md`](https://github.com/akira-toriyama/.github/blob/main/docs/go-bite.md).
- **dist-gate** — a distribution-layer file (the three reusables, `goreleaser.yml`,
  the install action, `.goreleaser.yaml`) in the diff with no `internal/workflows`
  `*_test.go` change fails the PR — bite acts on Go diffs only, and YAML `:bug:`
  fixes have shipped with zero tests before. Waiver: `Dist-gate-exempt: <reason>`
  trailer on every non-merge commit (a real trailer, like `Bite-exempt:`). The
  file set lives twice on purpose — `scripts/dist-gate.sh` and
  `internal/workflows/distgate_test.go` — and the test fails if the copies drift.
- **`internal/workflows`** — tests the workflow YAML itself: the install action's single source,
  `fetch-depth: 0` on the walking reusables, the `sed -n '/^[{]/,$p'` envelope sieve a step needs
  before `jq`. Its failure messages carry the incident; read them. The one it fails you for being
  helpful: a **commented** caller stub must keep its `@vX.Y.Z` placeholder — a tag is cut on an
  already-frozen tree, so a concrete version in a comment is stale on arrival — while an
  **executable** `uses:` must pin a real `vX.Y.Z`.
  Four of its eight guards carry a **positive control** proving the pattern still matches a real
  instance. Copy that shape for any guard that asserts an absence: a regex guard with no canary is
  how a fleet invariant dies green.
- **zizmor** — third-party actions must be full-SHA pinned (`actions/*` and `akira-toriyama/*` may
  ride tags); a new `actions/checkout` needs an explicit `persist-credentials:`.

## Files that are not yours to edit

Any file whose header says it is managed by fleet-sync is overwritten on the next sync — edit the
canonical copy in `akira-toriyama/.github/fleet/` instead. The canonical set is the Files list in
[`fleet/README.md`](https://github.com/akira-toriyama/.github/blob/main/fleet/README.md), not a
copy of it kept here. One exception worth knowing: `.github/dependabot.yml` is **assembled**, not
copied — a base plus one `dependabot.d/<ecosystem>.yml` block per manifest present — so glyph's
copy legitimately differs from the hub's, and a base-only overwrite erases a repo's real
ecosystems.

Mind the naming split: glyph **defines** `lint.yml` / `release.yml` / `pr-verdict.yml` and
**consumes** them as `commit-lint.yml` / `version-preview.yml`. A caller must never share a
filename with a reusable; that once overwrote `pr-verdict.yml`.

## One edit nothing guards

Bumping `go.mod` / `go.sum` leaves `flake.nix`'s `vendorHash` stale, and there is no nix job in
CI to catch it: reset it to `pkgs.lib.fakeHash`, run `nix build`, paste the hash it reports.
