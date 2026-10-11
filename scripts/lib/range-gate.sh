# shellcheck shell=sh
# range-gate.sh — the half dist-gate.sh and golden-gate.sh share: form the diff
# CI judges, and demand a trailer on every commit in the range. Sourced, never
# run; each gate keeps its own file-set PATTERN, because internal/workflows
# holds those lines byte-equal to its copies in the gate scripts themselves.
#
# Messages carry the sourcing script's name ($0 is not rebound by `.`).
RANGE_GATE="$(basename "$0" .sh)"

# range_gate_changed <base> prints the paths of the three-dot diff — against
# the merge-base, the diff a pull request is judged on. A base that does not
# resolve exits 1: a stale or shallow checkout cannot form that diff, and an
# empty answer would read as "nothing to prove".
range_gate_changed() {
  if ! git rev-parse --verify --quiet "$1^{commit}" >/dev/null; then
    echo "$RANGE_GATE: base '$1' does not resolve — fetch it first; a stale or shallow checkout cannot form the diff CI judges" >&2
    exit 1
  fi
  git diff --name-only "$1"...HEAD
}

# require_trailer_on_every_commit <Key> <base> returns 0 only when base..HEAD
# holds at least one non-merge commit and every one carries a `<Key>:` trailer
# with a real reason. A range-wide claim cannot be made by one commit in the
# range — go-bite's rule for Bite-exempt. The trailer is parsed by `git
# interpret-trailers`, so a matching line inside a body paragraph asserts
# nothing.
#
# A trailer with no reason, or still the `<placeholder>`, exits 1 on the spot.
# Otherwise the answer is the return code, with RANGE_GATE_MISSING set to the
# first commit lacking the trailer (empty when the range held no non-merge
# commit) and RANGE_GATE_REASON to the last reason read.
# shellcheck disable=SC2034 # RANGE_GATE_MISSING is read by golden-gate.sh
require_trailer_on_every_commit() {
  RANGE_GATE_MISSING=''
  RANGE_GATE_REASON=''
  examined=0
  for commit in $(git rev-list --no-merges "$2"..HEAD); do
    examined=$((examined + 1))
    line="$(git show -s --format=%B "$commit" | git interpret-trailers --parse | grep "^$1:" | sed -n '1p')"
    if [ -z "$line" ]; then
      RANGE_GATE_MISSING="$commit"
      return 1
    fi
    RANGE_GATE_REASON="$(printf '%s' "${line#"$1":}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    case "$RANGE_GATE_REASON" in
      '')
        echo "$RANGE_GATE: the $1 trailer on $(git rev-parse --short "$commit") states no reason" >&2
        exit 1 ;;
      '<'*'>')
        echo "$RANGE_GATE: the $1 trailer on $(git rev-parse --short "$commit") is still the placeholder — state the real reason" >&2
        exit 1 ;;
    esac
  done
  [ "$examined" -gt 0 ]
}
