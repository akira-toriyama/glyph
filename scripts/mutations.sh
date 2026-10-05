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
# Rows are independent — each gets a private copy of the snapshot — so they run
# MUTATIONS_JOBS at a time (default: the online CPU count), and so do the
# baselines. A baseline stays ONE TEST PER RUN on purpose: a test that passes
# only after another has run would pass a batched baseline and then fail alone
# under the mutation, a kill the suite never earned. The report is replayed in
# ledger order, so it reads the same at any width.
#
# It still gets its own CI job rather than riding the unit-test one, because a
# red here means something different from a failing test: the tests passed, and
# that is the problem.
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

JOBS=${MUTATIONS_JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)}
case "$JOBS" in
  '' | *[!0-9]* | 0)
    echo "mutations: MUTATIONS_JOBS must be a positive integer, got '$JOBS'" >&2
    exit 2
    ;;
esac

TMP=$(mktemp -d)
PIDS=''
cleanup() {
  # shellcheck disable=SC2086 # PIDS is a space-separated list
  [ -z "$PIDS" ] || kill $PIDS 2>/dev/null || true
  rm -rf "$TMP"
}
# An interrupted run must not be able to print a verdict. The trap used to
# delete the snapshot and RETURN, so every remaining row failed for the script's
# own reasons and the last line called a healthy branch ✗ (2026-10-04: a pkill
# aimed at another run of this script). Same re-raise as scripts/fleet-preflight.sh.
trap cleanup EXIT
trap 'cleanup; trap - HUP;  kill -HUP  $$' HUP
trap 'cleanup; trap - INT;  kill -INT  $$' INT
trap 'cleanup; trap - TERM; kill -TERM $$' TERM

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
mkdir "$TMP/list" "$TMP/b" "$TMP/out"

# The rows this run measures, numbered by their position in the whole ledger so
# a filtered run prints the same [n] as a full one.
ROWS=$TMP/rows
: >"$ROWS"
n=0
while IFS='	' read -r patch pkg tests why || [ -n "${patch:-}" ]; do
  case "$patch" in '' | '#'*) continue ;; esac
  n=$((n + 1))
  if [ -n "$FILTER" ]; then
    case "$patch$pkg$tests$why" in *"$FILTER"*) ;; *) continue ;; esac
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$n" "$patch" "$pkg" "$tests" "$why" >>"$ROWS"
done <"$LEDGER"

if [ ! -s "$ROWS" ]; then
  echo "mutations: the filter '$FILTER' matched no ledger row" >&2
  exit 1
fi

# Baselines are per (package, test) and shared across rows: the same test often
# guards more than one decision, and re-running it per row is the slowest thing
# this script could do for no added assurance. KEYS numbers each pair once.
KEYS=$TMP/keys
while IFS='	' read -r n patch pkg tests why; do
  [ -f "testdata/mutations/$patch" ] || continue
  for t in $tests; do
    printf '%s\t%s\n' "$pkg" "$t"
  done
done <"$ROWS" | sort -u | awk '{ print NR "\t" $0 }' >"$KEYS"

key_of() { # key_of <pkg> <test>
  awk -F'\t' -v p="$1" -v t="$2" '$2 == p && $3 == t { print $1; exit }' "$KEYS"
}

listfile() { # listfile <pkg> — where that package's test names are kept
  printf '%s/list/%s' "$TMP" "$(printf '%s' "$1" | tr '/' '_')"
}

# reap waits for everything started since the last reap. `wait` returns as soon
# as a trapped signal arrives, which is what lets an interrupt stop the run
# between rows rather than after the one in flight.
running=0
reap() {
  wait
  PIDS=''
  running=0
}
throttle() { # throttle <pid> — call after each `&`
  PIDS="$PIDS $1"
  running=$((running + 1))
  if [ "$running" -ge "$JOBS" ]; then
    reap
  fi
}

cut -f2 "$KEYS" | sort -u >"$TMP/pkgs"
while IFS= read -r pkg; do
  (cd "$TMP/clean" && go test -list '.*' "$pkg" >"$(listfile "$pkg")" 2>/dev/null || true) </dev/null &
  throttle $!
done <"$TMP/pkgs"
reap

# baseline leaves $TMP/b/<key>.err holding the refusal a row naming this test
# prints, and no such file when the test exists and passes on the unmutated tree.
baseline() { # baseline <key> <pkg> <test>
  if ! grep -qxF "$3" "$(listfile "$2")"; then
    {
      echo "  ✗ $2 has no test named $3 — the ledger names a test that does not exist"
      echo "    (a -run pattern matching nothing EXITS 0, so this row would pin nothing)"
    } >"$TMP/b/$1.err"
    return 0
  fi
  if ! (cd "$TMP/clean" && go test -count=1 -run "^$3\$" "$2" >"$TMP/b/$1.log" 2>&1); then
    {
      echo "  ✗ $3 already FAILS on the unmutated tree — fix that first; until then the row is vacuous"
      sed 's/^/    /' "$TMP/b/$1.log"
    } >"$TMP/b/$1.err"
  fi
}

echo "→ baselining $(wc -l <"$KEYS" | tr -d ' ') test(s) on the unmutated tree, $JOBS at a time"
while IFS='	' read -r key pkg t; do
  baseline "$key" "$pkg" "$t" </dev/null &
  throttle $!
done <"$KEYS"
reap

# row measures one ledger row. Its stdout and stderr are files the main loop
# replays in ledger order; its tally goes to $TMP/out/<n>.res as "<kills> <bad>".
row() { # row <n> <patch> <pkg> <tests> <why>
  res=$TMP/out/$1.res
  printf '\n→ [%s] %s\n  %s\n' "$1" "$2" "$5"

  if [ ! -f "testdata/mutations/$2" ]; then
    echo "  ✗ no such patch: testdata/mutations/$2" >&2
    echo "0 1" >"$res"
    return 0
  fi

  ok=yes
  for t in $4; do
    key=$(key_of "$3" "$t")
    if [ -z "$key" ]; then
      echo "  ✗ $3 $t was never baselined — refusing to count a kill nothing vouches for" >&2
      ok=no
    elif [ -f "$TMP/b/$key.err" ]; then
      cat "$TMP/b/$key.err" >&2
      ok=no
    fi
  done
  if [ "$ok" = no ]; then
    echo "0 1" >"$res"
    return 0
  fi

  mdir=$TMP/mut-$1
  cp -R "$TMP/clean" "$mdir"
  if ! (cd "$mdir" && git apply --whitespace=nowarn "$ROOT/testdata/mutations/$2" 2>"$mdir.log"); then
    echo "  ✗ the patch no longer applies — the code it mutates moved, so this row now pins NOTHING." >&2
    echo "    Re-derive the mutation against the current tree; do not delete the row." >&2
    sed 's/^/    /' "$mdir.log" >&2
    rm -rf "$mdir" "$mdir.log"
    echo "0 1" >"$res"
    return 0
  fi
  if ! (cd "$mdir" && go build ./... >"$mdir.log" 2>&1); then
    echo "  ✗ the mutated tree does not COMPILE, so a failing test would prove nothing about the suite." >&2
    echo "    Rewrite the mutation so it builds (Go rejects an unused variable — a deleted" >&2
    echo "    guard often needs a \`_ = x\` to keep its inputs used)." >&2
    sed 's/^/    /' "$mdir.log" >&2
    rm -rf "$mdir" "$mdir.log"
    echo "0 1" >"$res"
    return 0
  fi

  survived=0
  for t in $4; do
    if (cd "$mdir" && go test -count=1 -run "^$t\$" "$3" >"$mdir.log" 2>&1); then
      echo "  ✗ MUTATION SURVIVED: $3 $t still passes with the decision broken." >&2
      echo "    Either the test does not assert what the row claims, or the decision moved." >&2
      survived=$((survived + 1))
    else
      echo "  ✓ killed by $3 $t"
    fi
  done
  rm -rf "$mdir" "$mdir.log"
  if [ "$survived" -eq 0 ]; then
    echo "1 0" >"$res"
  else
    echo "0 $survived" >"$res"
  fi
}

rows=0
kills=0
bad=0
batch=''

# replay prints the rows of the batch that just finished, in ledger order, and
# adds up their tallies. A row with no tally was killed mid-flight: counted as
# bad, so a run that lost a worker cannot read as green.
replay() {
  reap
  for i in $batch; do
    cat "$TMP/out/$i.out"
    cat "$TMP/out/$i.err" >&2
    if [ -f "$TMP/out/$i.res" ]; then
      read -r k b <"$TMP/out/$i.res"
      kills=$((kills + k))
      bad=$((bad + b))
    else
      echo "  ✗ row [$i] produced no result — its worker died" >&2
      bad=$((bad + 1))
    fi
  done
  batch=''
}

while IFS='	' read -r n patch pkg tests why; do
  rows=$((rows + 1))
  row "$n" "$patch" "$pkg" "$tests" "$why" >"$TMP/out/$n.out" 2>"$TMP/out/$n.err" </dev/null &
  PIDS="$PIDS $!"
  batch="$batch $n"
  running=$((running + 1))
  if [ "$running" -ge "$JOBS" ]; then
    replay
  fi
done <"$ROWS"
replay

echo
if [ "$bad" -ne 0 ]; then
  echo "✗ $kills/$rows ledger rows bite — $bad row(s) above need a look" >&2
  exit 1
fi
echo "✓ $kills/$rows ledger rows bite"
