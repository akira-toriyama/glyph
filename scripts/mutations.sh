#!/bin/sh
# mutations.sh — the mutation ledger gate: does this suite BITE?
#
# Coverage answers "was this line executed", which is not the question. A line
# can be executed by every test in the tree and still have nobody assert its
# result: delete the guard, and the suite stays green. That is not hypothetical
# here — `gitsource.IsShallow` returning a constant, `IsAncestor` returning a
# constant and `Have` claiming every sha were all measured surviving the whole
# suite at 52236c5, on the three paths the fleet is most likely to hit.
#
# So this gate asks the inverse question, per named decision: break the decision
# on purpose and require the tests the ledger NAMES to fail. A row that stops
# failing is a test that stopped biting, and it says so before the release does.
#
# The ledger is testdata/mutations/ledger.tsv (tab-separated, `#` comments):
#
#   patch <TAB> package <TAB> tests <TAB> why this decision is worth a row
#
# Every row is checked in BOTH directions, because half a check is what lets a
# ledger rot green:
#
#   - the named tests must PASS on the unmutated tree (a test that fails, is
#     misspelled, or does not exist would otherwise "kill" every mutation and
#     the whole ledger would be vacuous — the failure mode a hand-picked
#     non-vacuity canary only samples, done here for every row);
#   - the patch must APPLY and the mutated tree must COMPILE. A patch that no
#     longer applies means the code moved and the row now pins nothing; a
#     mutation that does not build "fails" the tests for the compiler's reasons,
#     not the suite's, which is the exact trap that made an earlier by-hand
#     measurement of this repo read as a kill (Go rejects an unused variable, so
#     deleting a guard's only use of one does not compile).
#
# Nothing runs in the working tree: each row is applied to a private snapshot of
# it (tracked + untracked-not-ignored, so a test you have not committed yet is
# measured). Interrupting this script cannot leave a mutation behind.
#
# Measured 21s wall for 7 rows on a warm build cache (macOS, 2026-07-26); a cold
# cache pays for one `go build ./...` per row on top. It still gets its own CI job
# rather than riding the unit-test one, because a red here means something
# different from a failing test: the tests passed, and that is the problem.
set -eu
cd "$(dirname "$0")/.."

LEDGER=testdata/mutations/ledger.tsv
ROOT=$(pwd)

if [ ! -f "$LEDGER" ]; then
  echo "mutations: no ledger at $LEDGER" >&2
  exit 1
fi

# One optional argument filters rows by substring, for the edit/measure loop:
#   scripts/mutations.sh footprint
FILTER=${1:-}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

# snapshot copies the WORKING TREE (not HEAD) so a test being written right now
# is what gets measured. Untracked-but-not-ignored files are included for the
# same reason; ignored ones (bin/, caches) are not, so the copy stays small.
snapshot() {
  mkdir -p "$1"
  git ls-files -z --cached --others --exclude-standard |
    tar --null -T - -cf - |
    tar -xf - -C "$1"
}

echo "→ snapshotting the working tree"
snapshot "$TMP/clean"

# Baselines are per (package, test) and shared across rows: the same test often
# guards more than one decision, and re-running it per row is the slowest thing
# this script could do for no added assurance.
BASELINED=$TMP/baselined
: >"$BASELINED"

baseline() { # baseline <pkg> <test>
  key="$1 $2"
  if grep -qxF "$key" "$BASELINED"; then
    return 0
  fi
  listed=$(cd "$TMP/clean" && go test -list "^$2\$" "$1" 2>/dev/null | grep -xF "$2" || true)
  if [ -z "$listed" ]; then
    echo "  ✗ $1 has no test named $2 — the ledger names a test that does not exist" >&2
    echo "    (a -run pattern matching nothing EXITS 0, so this row would pin nothing)" >&2
    return 1
  fi
  if ! (cd "$TMP/clean" && go test -count=1 -run "^$2\$" "$1" >"$TMP/base.log" 2>&1); then
    echo "  ✗ $2 already FAILS on the unmutated tree — fix that first; until then the row is vacuous" >&2
    sed 's/^/    /' "$TMP/base.log" >&2
    return 1
  fi
  echo "$key" >>"$BASELINED"
}

rows=0
kills=0
bad=0
n=0

while IFS='	' read -r patch pkg tests why || [ -n "${patch:-}" ]; do
  case "$patch" in '' | '#'*) continue ;; esac
  n=$((n + 1))
  if [ -n "$FILTER" ]; then
    case "$patch$pkg$tests$why" in *"$FILTER"*) ;; *) continue ;; esac
  fi
  rows=$((rows + 1))
  printf '\n→ [%s] %s\n  %s\n' "$n" "$patch" "$why"

  if [ ! -f "testdata/mutations/$patch" ]; then
    echo "  ✗ no such patch: testdata/mutations/$patch" >&2
    bad=$((bad + 1))
    continue
  fi

  ok=yes
  for t in $tests; do
    baseline "$pkg" "$t" || ok=no
  done
  if [ "$ok" = no ]; then
    bad=$((bad + 1))
    continue
  fi

  mdir=$TMP/mut-$n
  cp -R "$TMP/clean" "$mdir"
  if ! (cd "$mdir" && git apply --whitespace=nowarn "$ROOT/testdata/mutations/$patch" 2>"$TMP/apply.log"); then
    echo "  ✗ the patch no longer applies — the code it mutates moved, so this row now pins NOTHING." >&2
    echo "    Re-derive the mutation against the current tree; do not delete the row." >&2
    sed 's/^/    /' "$TMP/apply.log" >&2
    bad=$((bad + 1))
    continue
  fi
  if ! (cd "$mdir" && go build ./... >"$TMP/build.log" 2>&1); then
    echo "  ✗ the mutated tree does not COMPILE, so a failing test would prove nothing about the suite." >&2
    echo "    Rewrite the mutation so it builds (Go rejects an unused variable — a deleted" >&2
    echo "    guard often needs a \`_ = x\` to keep its inputs used)." >&2
    sed 's/^/    /' "$TMP/build.log" >&2
    bad=$((bad + 1))
    continue
  fi

  for t in $tests; do
    if (cd "$mdir" && go test -count=1 -run "^$t\$" "$pkg" >"$TMP/mut.log" 2>&1); then
      echo "  ✗ MUTATION SURVIVED: $pkg $t still passes with the decision broken." >&2
      echo "    Either the test does not assert what the row claims, or the decision moved." >&2
      bad=$((bad + 1))
      ok=no
    else
      echo "  ✓ killed by $pkg $t"
    fi
  done
  [ "$ok" = yes ] && kills=$((kills + 1))
done <"$LEDGER"

if [ "$rows" -eq 0 ]; then
  echo "mutations: the filter '$FILTER' matched no ledger row" >&2
  exit 1
fi

echo
if [ "$bad" -ne 0 ]; then
  echo "✗ $kills/$rows ledger rows bite — $bad row(s) above need a look" >&2
  exit 1
fi
echo "✓ $kills/$rows ledger rows bite"
