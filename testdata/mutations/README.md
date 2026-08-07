# testdata/mutations — the mutation ledger

Each `*.patch` here breaks **one argued decision** in the tree on purpose.
`scripts/mutations.sh` applies each to a private snapshot of the working tree and
requires the test the ledger *names* to fail. A row that stops failing is a test
that stopped biting.

## Why this exists

Coverage answers "was this line executed". That is not the question. A line can
be executed by every test in the suite and still have nobody assert its result —
delete the guard and the suite stays green. Measured at `52236c5`, all three of
these survived `go test ./...`:

- `gitsource.IsShallow` returning a constant,
- `gitsource.IsAncestor` returning a constant,
- `gitsource.Have` claiming every sha the repository was asked about.

Those are the shallow-clone and did-it-land paths, i.e. the three the fleet is
most likely to hit. (All three are killed today — `#69` added their tests. The
ledger is what keeps them killed.)

The house rule this mechanises is "a fix is not shown by a green suite; it is
shown by re-breaking it and watching the suite go red." That check used to exist
only as a habit, performed by hand, remembered in prose. Here it is a gate.

`go-bite` (the `bite` job in `build.yml`) asks a different and weaker question:
of the tests a pull request adds, did **at least one** bite? It cannot notice a
decision whose test rotted three releases ago, and one biting test satisfies it
for the whole diff.

## The ledger format

`ledger.tsv` is tab-separated, `#` starts a comment line:

    patch <TAB> package <TAB> tests <TAB> why this decision is worth a row

- **patch** — a file in this directory.
- **package** — one Go package path, e.g. `./internal/gitsource`.
- **tests** — one or more test names, space-separated, all in that package. Each
  must fail under the mutation. Name the test that *asserts the decision*, not
  every test that happens to break; an incidental failure is not evidence that
  anything is still being checked deliberately.
- **why** — one line, in the imperative present, saying what breaks in the world
  if this decision is lost. It is printed while the row runs, so it is what a
  reader sees when the row goes red.

One row per (patch, package). A mutation whose decision is asserted in two
packages gets two rows.

## Adding a row

1. Break the decision in your working tree — the smallest edit that removes it.
2. Run the suite. **If it stays green you have found a gap**: write the test
   first, then come back. That is the ledger doing its job before it has a row.
3. Turn the edit into a patch and revert your tree:

       git diff -- <file> > testdata/mutations/<name>.patch && git checkout -- <file>

4. Add the row, then `scripts/mutations.sh <substring>` to run just that row.

Name the file after **the defect the decision prevents**, not after the code —
`release-incomplete-walk-still-hands-down-a-verdict.patch`, not
`cmd-release-line-133.patch`. The filename is the sentence the gate prints.

Keep every line the patch **touches or quotes** — deleted lines and context
alike — out of dependabot's hands. A patch is bytes frozen against a moving
file, and a pinned-SHA `uses:` line inside one re-breaks on every bump: row 18
deleted a whole CI job, checkout SHA included, and went red on three
consecutive dependabot PRs without any decision changing (t-7zy2). Mutate the
smallest glyph-owned span that still kills the named test.

Two properties the runner enforces so a row cannot rot green:

- the named tests must **pass** on the unmutated tree (a misspelled or missing
  test name would otherwise "kill" every mutation — `go test -run` matching
  nothing exits 0);
- the patch must **apply**, and the mutated tree must **compile**. A patch that
  no longer applies pins nothing, and a mutation that does not build fails the
  tests for the compiler's reasons rather than the suite's. That second one is
  not hypothetical: Go rejects an unused variable, so deleting a guard's only use
  of one does not compile, and an earlier by-hand measurement of this repo read
  that build error as a kill.

## When a patch stops applying

The code it mutates moved. **Re-derive the mutation; do not delete the row.**
Deleting it is how the decision loses its only mechanical defender, and it will
look like a tidy-up in the diff.
