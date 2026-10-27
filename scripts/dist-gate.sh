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
# One change is judged before any evidence is asked for, and passes: a diff
# whose every changed line in the gated files is a SHA-pinned `uses:` step put
# back as the same line with another SHA, or another `# vX.Y.Z` comment —
# dependabot's bump, and nothing else. No text assertion can express a SHA
# swap and a bot cannot write the trailer below, so until the gate learned
# that shape every such pull went red: six of them (#88, #89, #90, #158, #287,
# #315), two re-authored by hand to carry the trailer, two merged red by
# fleet-automerge, which reads required checks alone — which is also why the
# gate could not be made required. The shape is read strictly, by pin_swaps
# below: a changed `with:` line, a step moved, added or re-indented, an action
# replaced by another, a pin that is a tag rather than 40 hex, a mode change, a
# rename, a binary — each of those is judged like any other change.
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
# arm still names a real file, asserts byte-for-byte agreement with its own
# copy — edit the pattern in BOTH places or that test fails — and runs this
# script over a fixture repository to hold the pin-swap stand-down to its shape.
set -eu
cd "$(dirname "$0")/.."

DIST_PATTERN='^(\.goreleaser\.yaml$|\.github/actions/|\.github/workflows/(lint|pr-verdict|release|goreleaser)\.yml$)'
EVIDENCE_PATTERN='^internal/workflows/.*_test\.go$'

base="${1:?usage: sh scripts/dist-gate.sh <base-ref>}"

# shellcheck source=scripts/lib/range-gate.sh
. ./scripts/lib/range-gate.sh
changed="$(range_gate_changed "$base")"

# pin_swaps prints, one per line, the actions whose pins the diff of the files
# in $dist swaps, and returns 0 only when every changed line is such a swap: a
# `uses: <owner>/<repo>[/<path>]@<40 hex>` line, with or without one
# `# <comment>` token after it, whose text before the `@` — indentation, list
# marker, action — is the i-th removal of its hunk and comes back as the i-th
# addition. Any other changed line, an unpaired removal or addition, a header
# line that is not a plain text change (a mode, a rename, a binary), or no
# changed line at all returns 1. The diff carries no context and no git
# configuration: -U0 and --inter-hunk-context=0 keep each change its own hunk,
# --text and --no-renames show a binary or a rename as the lines they are, and
# the rest holds a developer's diff.* config out of a verdict CI would not
# share.
pin_swaps() {
  old_ifs=$IFS
  IFS='
'
  set -f
  # shellcheck disable=SC2086 # one path per line: IFS is a newline alone and globbing is off
  set -- $dist
  set +f
  IFS=$old_ifs
  git diff -U0 --inter-hunk-context=0 --no-color --text --no-renames --no-ext-diff --no-textconv "$base"...HEAD -- "$@" | awk '
    function pin(line,    s, at, head, sha) {
      s = substr(line, 2)
      at = index(s, "@")
      if (at == 0) return ""
      head = substr(s, 1, at - 1)
      sha = substr(s, at + 1)
      sub(/ # [^ \t]+$/, "", sha)
      if (head !~ "^[ \t]*(- )?uses: [A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+$") return ""
      if (length(sha) != 40 || sha !~ /^[0-9a-f]+$/) return ""
      return head
    }
    function settle(    i) {
      if (nminus != nplus) bad = 1
      for (i = 1; i <= nminus; i++) if (minus[i] != plus[i]) bad = 1
      nminus = 0
      nplus = 0
    }
    /^diff --git / { settle(); inhunk = 0; next }
    /^@@ /         { settle(); inhunk = 1; next }
    !inhunk        { if ($0 !~ /^(index |--- |[+][+][+] )/) { bad = 1; exit }; next }
    /^[-+]/ {
      head = pin($0)
      if (head == "") { bad = 1; exit }
      if (substr($0, 1, 1) == "-") minus[++nminus] = head; else plus[++nplus] = head
      sub(/^[ \t]*(- )?uses: /, "", head)
      seen[head] = 1
      any = 1
    }
    END {
      settle()
      if (bad || !any) exit 1
      for (k in seen) print k
    }
  '
}

dist="$(printf '%s\n' "$changed" | grep -E "$DIST_PATTERN" || true)"
if [ -z "$dist" ]; then
  echo "dist-gate: no distribution-layer file in the diff — nothing to prove"
  exit 0
fi

if swapped="$(pin_swaps)"; then
  swapped="$(printf '%s\n' "$swapped" | sort | paste -s -d ' ' -)"
  echo "dist-gate: every changed line of the distribution layer swaps the SHA pin of the same action ($swapped) — dependabot's bump, which no workflow test can express; the gate stands down:"
  printf '%s\n' "$dist" | sed 's/^/  /'
  exit 0
fi

if printf '%s\n' "$changed" | grep -qE "$EVIDENCE_PATTERN"; then
  echo "dist-gate: distribution-layer change is accompanied by an internal/workflows test change:"
  printf '%s\n' "$dist" | sed 's/^/  /'
  exit 0
fi

if require_trailer_on_every_commit Dist-gate-exempt "$base"; then
  echo "dist-gate: waived by a Dist-gate-exempt trailer on every commit — $RANGE_GATE_REASON"
  exit 0
fi

printf '%s\n' "$dist" | sed 's/^/  /' >&2
printf '::error::dist-gate: this change touches the distribution layer (the files above) with no internal/workflows test change. YAML, action and goreleaser fixes have shipped with zero tests before — add or extend a text assertion in internal/workflows (bite will then prove it bites), or waive with a `Dist-gate-exempt: <reason>` trailer on every commit.\n' >&2
exit 1
