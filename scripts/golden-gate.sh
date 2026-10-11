#!/bin/sh
# golden-gate.sh — the golden gate: a diff that rewrites a golden file must
# state its reason as a `Golden-change: <reason>` trailer on every non-merge
# commit in the range.
#
# A golden is a format SPEC stored as bytes — the dry-run release body's
# golden and markdown's TestExportedSurfaceGolden both compare
# output against one — and `go test -update` rewrites it from whatever the code
# currently produces. That last part is the hazard this gate closes (ratified
# with t-3f4s's surface golden): -update makes a rendering BUG look intentional.
# The diff sits right there in the pull request, but nothing forces the author
# to have READ it as the spec change it is. The trailer is that force: range-
# wide, parsed by `git interpret-trailers` exactly as go-bite's `Bite-exempt:`
# is, so a line inside a body paragraph asserts nothing.
#
# Unlike dist-gate.sh (whose trailer rule this shares through
# scripts/lib/range-gate.sh), the trailer here is not a waiver of some stronger
# demand — it IS the demand. No test can prove a golden diff correct; the
# golden is the assertion. All a gate can force is a named, human-stated reason
# in the history of every commit that rewrote the spec.
#
# What counts as a golden is GOLDEN_PATTERN below: the -update-rewritable
# goldens (testdata/*.golden.*). Deliberately absent: the fuzz corpus, which is
# regression INPUTS rather than a format spec — a trailer demanded there would
# read as "a hand edit of a crash input is acceptable with a reason", and the
# only legitimate writes are the engine's own.
#
# Usage: sh scripts/golden-gate.sh <base>   (CI passes the PR base sha;
# check.sh passes origin/main). The diff is three-dot, i.e. against the
# merge-base — the same diff CI judges.
#
# internal/workflows/goldengate_test.go holds this pattern's canary, proves it
# still matches the real goldens and nothing else, and asserts byte-for-byte
# agreement with its own copy — edit the pattern in BOTH places or that test
# fails.
set -eu
cd "$(dirname "$0")/.."

GOLDEN_PATTERN='^.*/testdata/.*\.golden\..*$'

base="${1:?usage: sh scripts/golden-gate.sh <base-ref>}"

# shellcheck source=scripts/lib/range-gate.sh
. ./scripts/lib/range-gate.sh
changed="$(range_gate_changed "$base")"

goldens="$(printf '%s\n' "$changed" | grep -E "$GOLDEN_PATTERN" || true)"
if [ -z "$goldens" ]; then
  echo "golden-gate: no golden file in the diff — nothing to prove"
  exit 0
fi

if require_trailer_on_every_commit Golden-change "$base"; then
  echo "golden-gate: every commit in the range states its Golden-change reason for:"
  printf '%s\n' "$goldens" | sed 's/^/  /'
  exit 0
fi

printf '%s\n' "$goldens" | sed 's/^/  /' >&2
printf '::error::golden-gate: this change rewrites the golden files above and %s carries no Golden-change trailer. A golden is the format spec — `-update` regenerates it from whatever the code now produces, so the diff IS the spec change: read it as one, then put `Golden-change: <reason>` on every commit in the range (a real trailer; `git interpret-trailers` must parse it).\n' \
  "$([ -n "$RANGE_GATE_MISSING" ] && git rev-parse --short "$RANGE_GATE_MISSING" || echo 'an empty commit range')" >&2
exit 1
