#!/bin/sh
# dist-gate.sh — the distribution-layer gate: a change to what glyph SHIPS must
# carry evidence, exactly as a change to what glyph computes must.
#
# The gap this closes was measured, not imagined (t-5dbh): `bite` acts on Go
# diffs only, and the extras smoke exercises the built binary's CLI contract and
# reads no YAML — so a :bug: fix touching only .goreleaser.yaml (1015774) or
# only the install action (bcd93e3) reached main with zero tests and no gate
# even noticing. The one defence those files have is internal/workflows' text
# assertions, and adding one was left to each pull request's discretion. This
# script removes the discretion:
#
#   distribution-layer file in the diff AND no internal/workflows test change
#   -> exit 1.
#
# What counts as the distribution layer is DIST_PATTERN below: the three
# reusable workflows and goreleaser.yml, the composite install action, and
# .goreleaser.yaml. Deliberately absent: build.yml / govulncheck.yml (this
# repo's own CI, shipped to nobody) and the fleet-synced consumers
# (commit-lint.yml, task-status.yml, ...) whose canonical copies live in
# akira-toriyama/.github and arrive here by sync, not by pull request.
#
# The demanded evidence is a *_test.go change under internal/workflows, which
# also arms `bite`: a changed test there is exactly what bite runs against the
# pre-PR tree. The two gates compose — this one demands the test EXISTS, bite
# demands it BITES.
#
# Escape hatch, go-bite's exact shape: a `Dist-gate-exempt: <reason>` git
# trailer on EVERY non-merge commit in the range — a real trailer, parsed by
# `git interpret-trailers`, so a line sitting inside a body paragraph waives
# nothing. Use it for the change no text assertion can express (a pure comment
# reword, say), and state that as the reason.
#
# Usage: sh scripts/dist-gate.sh <base>   (CI passes the PR base sha; check.sh
# passes origin/main). The diff is three-dot, i.e. against the merge-base —
# the same diff CI judges.
#
# internal/workflows/distgate_test.go holds this pattern's canary, proves every
# arm still names a real file, and asserts byte-for-byte agreement with its own
# copy — edit the pattern in BOTH places or that test fails.
set -eu
cd "$(dirname "$0")/.."

DIST_PATTERN='^(\.goreleaser\.yaml$|\.github/actions/|\.github/workflows/(lint|pr-verdict|release|goreleaser)\.yml$)'
EVIDENCE_PATTERN='^internal/workflows/.*_test\.go$'

base="${1:?usage: sh scripts/dist-gate.sh <base-ref>}"

if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "dist-gate: base '$base' does not resolve — fetch it first; a stale or shallow checkout cannot form the diff CI judges" >&2
  exit 1
fi

changed="$(git diff --name-only "$base"...HEAD)"

dist="$(printf '%s\n' "$changed" | grep -E "$DIST_PATTERN" || true)"
if [ -z "$dist" ]; then
  echo "dist-gate: no distribution-layer file in the diff — nothing to prove"
  exit 0
fi

if printf '%s\n' "$changed" | grep -qE "$EVIDENCE_PATTERN"; then
  echo "dist-gate: distribution-layer change is accompanied by an internal/workflows test change:"
  printf '%s\n' "$dist" | sed 's/^/  /'
  exit 0
fi

# The waiver. Range-wide claims cannot be made by one commit in the range, so —
# exactly as go-bite demands for Bite-exempt — every non-merge commit must carry
# the trailer, each with a real reason.
waiver=''
waived=1
for commit in $(git rev-list --no-merges "$base"..HEAD); do
  line="$(git show -s --format=%B "$commit" | git interpret-trailers --parse | grep '^Dist-gate-exempt:' | sed -n '1p')"
  if [ -z "$line" ]; then
    waived=0
    break
  fi
  waiver="$(printf '%s' "${line#Dist-gate-exempt:}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  case "$waiver" in
    '')
      echo "dist-gate: the Dist-gate-exempt trailer on $(git rev-parse --short "$commit") states no reason" >&2
      exit 1 ;;
    '<'*'>')
      echo "dist-gate: the Dist-gate-exempt trailer on $(git rev-parse --short "$commit") is still the placeholder — state the real reason" >&2
      exit 1 ;;
  esac
done
if [ "$waived" = 1 ] && [ -n "$waiver" ]; then
  echo "dist-gate: waived by a Dist-gate-exempt trailer on every commit — $waiver"
  exit 0
fi

printf '%s\n' "$dist" | sed 's/^/  /' >&2
printf '::error::dist-gate: this change touches the distribution layer (the files above) with no internal/workflows test change. YAML, action and goreleaser fixes have shipped with zero tests before — add or extend a text assertion in internal/workflows (bite will then prove it bites), or waive with a `Dist-gate-exempt: <reason>` trailer on every commit.\n' >&2
exit 1
