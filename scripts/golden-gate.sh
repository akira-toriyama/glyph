#!/bin/sh
# golden-gate.sh — the golden gate: a diff that rewrites a golden file must
# state its reason as a `Golden-change: <reason>` trailer on every non-merge
# commit in the range.
#
# A golden is a format SPEC stored as bytes — the dry-run release body's
# golden and markdown's TestExportedSurfaceGolden both compare
# output against one — and `go test -update` rewrites it from whatever the code
# currently produces. That last part is the hazard this gate closes: -update
# makes a rendering BUG look intentional.
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
# goldens (testdata/*.golden.*), under a testdata/ at any depth, the repo root's
# included. Deliberately absent: the fuzz corpus, which is
# regression INPUTS rather than a format spec — a trailer demanded there would
# read as "a hand edit of a crash input is acceptable with a reason", and the
# only legitimate writes are the engine's own.
#
# Usage: sh scripts/golden-gate.sh <base> [<head>]   (CI passes the PR base
# sha with HEAD at the merge commit it checked out; check.sh passes origin/main
# and the merge commit it built the way CI does). The diff is three-dot,
# base...head, i.e. against the merge-base — the same diff CI judges, as long as
# head is the merge: from a branch head the diff runs from the fork point, and on
# a branch stacked on a pull request main has since squashed it carries the
# parent's golden, so this gate demanded a trailer CI never asked for (measured
# 2026-11-08: exit 1 from the head, exit 0 from the merge).
#
# internal/workflows/goldengate_test.go holds this pattern's canary, proves it
# still matches the real goldens and nothing else, and asserts byte-for-byte
# agreement with its own copy — edit the pattern in BOTH places or that test
# fails.
set -eu
cd "$(dirname "$0")/.."

GOLDEN_PATTERN='(^|/)testdata/.*\.golden\..*$'

base="${1:?usage: sh scripts/golden-gate.sh <base-ref> [<head-ref>]}"
head="${2:-HEAD}"

# shellcheck source=scripts/lib/range-gate.sh
. ./scripts/lib/range-gate.sh
changed="$(range_gate_changed "$base" "$head")"

goldens="$(printf '%s\n' "$changed" | grep -E "$GOLDEN_PATTERN" || true)"
if [ -z "$goldens" ]; then
  echo "golden-gate: no golden file in the diff — nothing to prove"
  exit 0
fi

if require_trailer_on_every_commit Golden-change "$base" "$head"; then
  echo "golden-gate: every commit in the range states its Golden-change reason for:"
  printf '%s\n' "$goldens" | sed 's/^/  /'
  exit 0
fi

printf '%s\n' "$goldens" | sed 's/^/  /' >&2
printf '::error::golden-gate: this change rewrites the golden files above and %s carries no Golden-change trailer. A golden is the format spec — `-update` regenerates it from whatever the code now produces, so the diff IS the spec change: read it as one, then put `Golden-change: <reason>` on every commit in the range (a real trailer; `git interpret-trailers` must parse it).\n' \
  "$([ -n "$RANGE_GATE_MISSING" ] && git rev-parse --short "$RANGE_GATE_MISSING" || echo 'an empty commit range')" >&2
exit 1
