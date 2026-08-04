#!/bin/sh
# fleet-corpus.sh — refresh internal/bump/testdata/fleet-corpus.tsv from the
# fleet's real commit subjects.
#
# The corpus is what stops a change to the parser's acceptance range from
# quietly re-versioning thirty-five repositories; internal/bump's
# fleetcorpus_test.go carries the argument. This script owns only the half a Go
# test should not: reading the fleet, and deciding which repositories may be
# quoted in a public file.
#
# Split of responsibility, on purpose. The GATE is hermetic — it reads the
# committed corpus and nothing else, so it gives the same verdict on a laptop
# with the fleet cloned and on a CI runner with nothing but this repository. The
# COLLECTOR needs `gh`, the network and sibling clones, which is why it lives
# out here and runs when a human asks.
#
# ─── Public repositories only ────────────────────────────────────────────────
# glyph is public and six of the fleet's thirty-five repositories are not.
# Commit subjects are prose their authors wrote about private work, so quoting
# them into this repository's testdata would publish them — permanently, and in
# a file whose whole purpose is never to be rewritten. `gh repo list --json
# isPrivate` is the filter, and it is applied to the LISTING rather than to the
# clones on disk: a private repository is excluded because GitHub says it is
# private, not because someone remembered to leave it out.
#
# ─── Append-only ─────────────────────────────────────────────────────────────
# This script cannot change a verdict. It hands the subjects to a Go test that
# appends unseen ones and refuses to run at all if the existing corpus does not
# already verify against the working tree. Keeping up with the fleet is
# therefore cheap; changing what a subject MEANS stays a hand edit, which is the
# whole point — see the header of internal/bump/fleetcorpus_test.go.
#
#     sh scripts/fleet-corpus.sh
set -eu

cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"
FLEET_DIR="$(dirname "$REPO_ROOT")"
OWNER=akira-toriyama
CORPUS=internal/bump/testdata/fleet-corpus.tsv

if ! command -v gh >/dev/null 2>&1; then
  echo "✗ gh is not installed, so the public/private split cannot be read." >&2
  echo "  Collecting from the clones on this disk instead would risk publishing a" >&2
  echo "  private repository's commit subjects into a public file." >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT INT TERM

echo "→ listing $OWNER's PUBLIC, non-archived, non-fork repositories"
if ! gh repo list "$OWNER" --no-archived --source --limit 200 \
     --json name,isPrivate --jq '.[] | select(.isPrivate | not) | .name' \
     > "$WORK/public.txt" 2>"$WORK/gh.err"; then
  echo "✗ could not list repositories:" >&2
  sed 's/^/    /' "$WORK/gh.err" >&2
  exit 1
fi
PUBLIC="$(wc -l < "$WORK/public.txt" | tr -d ' ')"
if [ "$PUBLIC" -eq 0 ]; then
  echo "✗ the listing came back empty, which is a broken query or an unscoped token," >&2
  echo "  not a fleet of zero. Refusing to 'refresh' the corpus with nothing." >&2
  exit 1
fi

# Counters are updated in THIS shell: `cmd | while read` bodies run in a
# subshell in POSIX sh, so every increment would be discarded and the summary
# would report zeros over a real collection.
CLONED=0
MISSING=''
: > "$WORK/subjects.txt"
while IFS= read -r name; do
  [ -n "$name" ] || continue
  dir="$FLEET_DIR/$name"
  if [ ! -d "$dir/.git" ]; then
    MISSING="$MISSING $name"
    continue
  fi
  # --all so a subject that only ever lived on a released tag still counts;
  # history is the corpus, not the current branch. A repo whose log cannot be
  # read contributed nothing, so it is MISSING and not CLONED — counting it as
  # collected is how "29/29 public repositories" comes to mean twenty-eight.
  if git -C "$dir" log --all --format='%s' >> "$WORK/subjects.txt" 2>/dev/null; then
    CLONED=$((CLONED + 1))
  else
    MISSING="$MISSING $name(unreadable)"
  fi
done < "$WORK/public.txt"

# A subject containing a newline cannot survive a line-oriented handoff, and git
# gives none here — %s is the first line by construction. Sort and dedupe so the
# appender sees each distinct subject once.
sort -u "$WORK/subjects.txt" -o "$WORK/subjects.txt"
DISTINCT="$(wc -l < "$WORK/subjects.txt" | tr -d ' ')"
echo "→ $DISTINCT distinct subject(s) from $CLONED/$PUBLIC public repositories"
if [ -n "$MISSING" ]; then
  echo "  not cloned here, so not represented:$MISSING"
fi
if [ "$CLONED" -eq 0 ]; then
  echo "✗ none of the public repositories is cloned beside this one, so there is nothing" >&2
  echo "  to collect. Clone them under $FLEET_DIR first." >&2
  exit 1
fi

rows() { grep -cvE '^(#|$)' "$1" || true; }

if [ ! -f "$CORPUS" ]; then
  echo "✗ $CORPUS is missing. This script APPENDS; it does not create the corpus," >&2
  echo "  because a file created here would carry no header explaining why it may not be" >&2
  echo "  regenerated — which is the only thing keeping it from being regenerated." >&2
  exit 1
fi
BEFORE="$(rows "$CORPUS")"

# The append test's status is the whole verdict, so it is captured rather than
# piped. A pipeline's status is the LAST command's — grep's — and `|| true` then
# swallowed even that, so a hard FAIL (the refusal that fires when the stored
# verdicts already disagree with the tree, i.e. exactly the case this script must
# not paper over) was followed by the ✓ line and exit 0. Measured.
echo "→ appending unseen subjects (existing verdicts are never touched)"
st=0
GLYPH_FLEET_SUBJECTS="$WORK/subjects.txt" go test ./internal/bump \
  -run TestFleetCorpusAppend -v > "$WORK/append.log" 2>&1 || st=$?
grep -E 'appended|nothing to append|refusing to append|FAIL|ok ' "$WORK/append.log" || true
if [ "$st" -ne 0 ]; then
  echo "✗ the append step failed (exit $st) — the corpus was NOT refreshed." >&2
  sed 's/^/    /' "$WORK/append.log" >&2
  exit "$st"
fi

AFTER="$(rows "$CORPUS")"
echo "✓ corpus: $BEFORE → $AFTER rows. Read the diff as the specification it is, then run"
echo "  \`go test ./internal/bump -run TestFleetCorpus\` to confirm the gate still holds."
