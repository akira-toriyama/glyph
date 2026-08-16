#!/bin/sh
# golden-gate.sh — the golden gate: a diff that rewrites a golden file must
# state its reason as a `Golden-change: <reason>` trailer on every non-merge
# commit in the range.
#
# A golden is a format SPEC stored as bytes — TestRenderGolden, the gitmoji
# table's TestMarkdownGolden, markdown's TestExportedSurfaceGolden all compare
# output against one — and `go test -update` rewrites it from whatever the code
# currently produces. That last part is the hazard this gate closes (ratified
# with t-3f4s's surface golden): -update makes a rendering BUG look intentional.
# The diff sits right there in the pull request, but nothing forces the author
# to have READ it as the spec change it is. The trailer is that force: range-
# wide, parsed by `git interpret-trailers` exactly as go-bite's `Bite-exempt:`
# is, so a line inside a body paragraph asserts nothing.
#
# Unlike dist-gate.sh (whose shape this copies), the trailer here is not a
# waiver of some stronger demand — it IS the demand. No test can prove a golden
# diff correct; the golden is the assertion. All a gate can force is a named,
# human-stated reason in the history of every commit that rewrote the spec.
#
# What counts as a golden is GOLDEN_PATTERN below: the -update-rewritable
# goldens (testdata/*.golden.*; docs/gitmoji-table.md left with
# gitmoji.Table.Markdown(), same -update path). Deliberately absent:
# internal/bump/testdata/fleet-corpus.tsv has NO -update on purpose — its test
# header argues why, and scripts/fleet-corpus.sh refuses to bake a broken
# tree's verdicts in as truth. Listing it here would read as "a trailer makes a
# hand edit of stored verdicts acceptable", which it does not.
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

if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "golden-gate: base '$base' does not resolve — fetch it first; a stale or shallow checkout cannot form the diff CI judges" >&2
  exit 1
fi

changed="$(git diff --name-only "$base"...HEAD)"

goldens="$(printf '%s\n' "$changed" | grep -E "$GOLDEN_PATTERN" || true)"
if [ -z "$goldens" ]; then
  echo "golden-gate: no golden file in the diff — nothing to prove"
  exit 0
fi

# Range-wide claims cannot be made by one commit in the range, so — exactly as
# go-bite demands for Bite-exempt — every non-merge commit must carry the
# trailer, each with a real reason.
examined=0
missing=''
for commit in $(git rev-list --no-merges "$base"..HEAD); do
  examined=$((examined + 1))
  line="$(git show -s --format=%B "$commit" | git interpret-trailers --parse | grep '^Golden-change:' | sed -n '1p')"
  if [ -z "$line" ]; then
    missing="$commit"
    break
  fi
  reason="$(printf '%s' "${line#Golden-change:}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  case "$reason" in
    '')
      echo "golden-gate: the Golden-change trailer on $(git rev-parse --short "$commit") states no reason" >&2
      exit 1 ;;
    '<'*'>')
      echo "golden-gate: the Golden-change trailer on $(git rev-parse --short "$commit") is still the placeholder — state the real reason" >&2
      exit 1 ;;
  esac
done

if [ "$examined" -gt 0 ] && [ -z "$missing" ]; then
  echo "golden-gate: every commit in the range states its Golden-change reason for:"
  printf '%s\n' "$goldens" | sed 's/^/  /'
  exit 0
fi

printf '%s\n' "$goldens" | sed 's/^/  /' >&2
printf '::error::golden-gate: this change rewrites the golden files above and %s carries no Golden-change trailer. A golden is the format spec — `-update` regenerates it from whatever the code now produces, so the diff IS the spec change: read it as one, then put `Golden-change: <reason>` on every commit in the range (a real trailer; `git interpret-trailers` must parse it).\n' \
  "$([ -n "$missing" ] && git rev-parse --short "$missing" || echo 'an empty commit range')" >&2
exit 1
