#!/bin/sh
# fleet-preflight.sh — what does the glyph you are about to TAG change, out there?
#
# glyph is pinned by every repo in the fleet, so a change here reaches nobody
# until a tag is cut and the pins move. That gap is the whole opportunity: the
# blast count is knowable BEFORE the tag, and until this script existed it was
# not — the rollout runbook's "verify" step audits pins and fires one live PR at
# glyph-test, neither of which answers "how many repos does this change the
# verdict for". The answer arrived at step 5, in production, one repo at a time.
#
# ─── What it measures ────────────────────────────────────────────────────────
# A DIFFERENTIAL, never an absolute. "Is repo X red?" is the wrong question: a
# repo already red for its own reasons is not this release's doing, and it is
# the noisiest possible way to hide the one repo this release breaks. So every
# probe runs TWICE over the identical range — once with the binary the fleet
# runs today, once with the candidate — and only a repo whose verdict MOVES is
# a finding. Repos that agree are counted and stay quiet.
#
# Two probes, because glyph reaches the fleet through two gates:
#
#   lint --range  the commit-lint gate (lint.yml). Read its answer as a
#                 PREDICTOR, not a verdict: CI lints a pull request's own
#                 commits, so nothing already merged is ever re-judged and a
#                 flip here reddens no existing run. What it tells you is that
#                 the authoring habit which produced those commits is still
#                 live in that repo, and the next pull request written the same
#                 way is the one that goes red. That is the useful warning, and
#                 it is the reason the range walked is recent authorship rather
#                 than all history.
#
#   bump          the release verdict (release.yml). This one IS retroactive:
#   --since-tag   it re-reads history that already exists, so a flip here
#                 changes the version number the next release cuts, in a repo
#                 that did nothing. A moved `bump` line is strictly graver than
#                 a moved `lint` line, and the report says so.
#
#                 `--since-tag` and not `--range`, which is what this script
#                 asked first and was wrong to. The release job walks merge
#                 points and expands each back into the pull it merged; a plain
#                 range walks the commits on the branch. They are not the same
#                 set and they do not give the same answer — measured on canon,
#                 where `--range v2.0.0..origin/main` refuses the whole range
#                 over one `:robot:` subject fleet-sync wrote (a code not in
#                 glyph's table) while `--since-tag` returns v2.0.1 without
#                 hesitating. Nine repos were being reported as "bump gate
#                 unanswered" on the strength of that, and the one thing a
#                 preflight may not do is describe a gate it is not running.
#
# The two are classified INDEPENDENTLY. They fail for unrelated reasons — a
# repo carrying one unparseable subject makes `bump` refuse the whole range
# (exit 3) while `lint` still answers about every other commit in it — and the
# first cut of this script discarded such a repo entirely, throwing away a good
# lint answer because the bump probe beside it had broken. Measured: that lost
# three consumers, one of them glyph-test, the repo the rollout fires live at.
#
# ─── What it refuses to do ───────────────────────────────────────────────────
# Report a number it cannot stand behind. Three ways that is enforced:
#
#   * The denominator is the FLEET, not the clones on this disk. It comes from
#     `gh repo list`, and if that cannot be read the script exits non-zero
#     rather than measuring whatever happens to be checked out — "5 repos
#     change" over an unknown denominator is not a smaller claim than the truth,
#     it is a different and false one.
#   * Every repo it does not measure is PRINTED, with the reason (no clone, no
#     tag, not a glyph consumer, probe broke). A silent skip is how a preflight
#     reports "2 repos affected" for a fleet of thirty-five it looked at four of.
#   * It asserts glyph's exit CODE and never its truthiness. `lint` exits 3 for
#     a convention violation — that is a finding — and 2 or 4 when the tool or
#     the environment broke, which is NOT one. Measured while writing this: a
#     run of `glyph lint --range` with the wrong working directory exits 4 for
#     every repo, and a script reading `if ! glyph …` would have called that
#     thirty-five findings and been believed. Same rule as scripts/check.sh's
#     smoke block, and the same incident behind it.
#
# ─── Usage ───────────────────────────────────────────────────────────────────
#     sh scripts/fleet-preflight.sh <candidate-binary> [baseline-binary]
#
# The candidate is an ARGUMENT and not `glyph` from PATH on purpose: the whole
# instrument exists to judge a binary that has no release yet, and the PATH
# wrapper builds a hardcoded clone — the primary checkout, i.e. code you did not
# write (CLAUDE.md). Build one with `sh build.sh` and pass ./bin/glyph.
#
# The baseline defaults to the highest v* tag in THIS repo, built into a
# throwaway worktree — that is the version the fleet is pinned at when the
# rollout is done correctly. A repo pinned further back sees a LARGER change
# than this script reports; the pin audit (the hub's glyph-pin-scan.sh) is what
# tells you whether any repo is in that state, and the final line here says how
# the baseline was chosen so the two can be read together.
#
# Needs network (`gh repo list`, and `--fetch` if you pass it). It is therefore
# NOT a check.sh gate and never should be: check.sh mirrors CI, this answers a
# question no CI job asks, and wiring a network call into the local pre-push
# loop is how the local pre-push loop stops being run.
set -eu

cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"
FLEET_DIR="$(dirname "$REPO_ROOT")"
OWNER=akira-toriyama

FETCH=''
CANDIDATE=''
BASELINE=''
for arg in "$@"; do
  case "$arg" in
    --fetch) FETCH=1 ;;
    -h|--help)
      # The whole header, not a hand-counted slice of it. `sed -n '2,60p'` cut
      # off mid-sentence and never reached the Usage block — the one thing
      # --help exists to print — and it was silently re-truncated every time the
      # header grew. Stop at the first non-comment line instead.
      sed -n '2,${/^[^#]/q;p;}' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*)
      echo "✗ unknown flag: $arg" >&2
      echo "  usage: sh scripts/fleet-preflight.sh [--fetch] <candidate-binary> [baseline-binary]" >&2
      exit 2
      ;;
    *)
      if [ -z "$CANDIDATE" ]; then CANDIDATE="$arg"
      elif [ -z "$BASELINE" ]; then BASELINE="$arg"
      else
        echo "✗ too many arguments (candidate, then optionally baseline): $arg" >&2
        exit 2
      fi
      ;;
  esac
done

if [ -z "$CANDIDATE" ]; then
  echo "✗ no candidate binary. This instrument judges the binary you are ABOUT to tag," >&2
  echo "  so it cannot default to one — \`glyph\` on PATH builds the primary checkout." >&2
  echo "  usage: sh scripts/fleet-preflight.sh [--fetch] <candidate-binary> [baseline-binary]" >&2
  echo "  build one first: sh build.sh && sh scripts/fleet-preflight.sh ./bin/glyph" >&2
  exit 2
fi
if [ ! -x "$CANDIDATE" ]; then
  echo "✗ candidate is not an executable file: $CANDIDATE" >&2
  exit 2
fi
CANDIDATE="$(cd "$(dirname "$CANDIDATE")" && pwd)/$(basename "$CANDIDATE")"

WORK="$(mktemp -d)"
BASELINE_WT=''
# `[ -n "$X" ] && cmd` was wrong here: it is an AND-OR list whose LAST command is
# the git, so under `set -e` a worktree that will not remove aborts cleanup before
# `rm -rf` runs and replaces the script's exit status with git's. Measured: rc=128
# and a leaked temp directory. An `if` has neither property.
cleanup() {
  if [ -n "$BASELINE_WT" ]; then
    git -C "$REPO_ROOT" worktree remove --force "$BASELINE_WT" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
# A signal handler that only cleans up does not stop the script — the shell runs
# the handler and CARRIES ON. Measured: SIGTERM mid-walk deleted the evidence
# files and the run then printed a normal, fully-caveated `✓ preflight:` line with
# the entire NOT MEASURED section missing, and exited 0. An interrupted run must
# not be able to produce a verdict, so re-raise.
trap cleanup EXIT
trap 'cleanup; trap - INT;  kill -INT  $$' INT
trap 'cleanup; trap - TERM; kill -TERM $$' TERM

# The baseline. Building it from a tag rather than downloading the release asset
# keeps this runnable with no release credentials and, more to the point, makes
# the comparison one between two TREES — the same thing `go build` produces from
# both sides, so a difference in the report is a difference in glyph and not in
# how the two binaries were produced.
BASELINE_LABEL=''
BASE_TAG_NAME=''
if [ -n "$BASELINE" ]; then
  if [ ! -x "$BASELINE" ]; then
    echo "✗ baseline is not an executable file: $BASELINE" >&2
    exit 2
  fi
  BASELINE="$(cd "$(dirname "$BASELINE")" && pwd)/$(basename "$BASELINE")"
  BASELINE_LABEL="$BASELINE (given)"
else
  BASE_TAG="$(git -C "$REPO_ROOT" tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)"
  if [ -z "$BASE_TAG" ]; then
    echo "✗ no v*.*.* tag in this repo to build a baseline from, and none was given." >&2
    echo "  Pass one explicitly: sh scripts/fleet-preflight.sh <candidate> <baseline>" >&2
    exit 2
  fi
  echo "→ building the baseline the fleet is pinned at ($BASE_TAG)"
  BASELINE_WT="$WORK/baseline-src"
  git -C "$REPO_ROOT" worktree add --detach "$BASELINE_WT" "$BASE_TAG" >/dev/null 2>&1
  BASELINE="$WORK/glyph-baseline"
  ( cd "$BASELINE_WT" && go build -o "$BASELINE" ./cmd/glyph )
  BASELINE_LABEL="$BASE_TAG (built here)"
  BASE_TAG_NAME="$BASE_TAG"
fi

# The denominator. `--source` drops forks, `--no-archived` drops the dead. This
# is the fleet the rollout runbook moves pins in, so it is the only honest set
# to divide by; measuring the clones on this disk instead would silently swap
# the question for "of the repos I happen to have, how many change".
echo "→ reading the fleet from GitHub"
if ! command -v gh >/dev/null 2>&1; then
  echo "✗ gh is not installed, so the fleet's membership cannot be read." >&2
  echo "  Measuring the clones on this disk instead would report a fraction as a total." >&2
  exit 1
fi
# The bump probe runs the release job's own `--since-tag` walk, which expands
# each merge point back into the pull it merged over the API. Without a token
# every one of those returns exit 4, and the run would spend two minutes
# producing thirty-five "no answer" rows that say nothing about the candidate.
if [ -z "${GITHUB_TOKEN:-}" ]; then
  GITHUB_TOKEN="$(gh auth token 2>/dev/null || true)"
  export GITHUB_TOKEN
fi
if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "✗ no GITHUB_TOKEN and \`gh auth token\` gave none, so the release-path walk" >&2
  echo "  (\`bump --since-tag\`) cannot expand merge points and would report no verdict" >&2
  echo "  for any repo. Run \`gh auth login\`, or export GITHUB_TOKEN." >&2
  exit 1
fi
if ! gh repo list "$OWNER" --no-archived --source --limit 200 --json name \
     --jq '.[].name' > "$WORK/fleet.txt" 2>"$WORK/gh.err"; then
  echo "✗ could not list $OWNER's repositories:" >&2
  sed 's/^/    /' "$WORK/gh.err" >&2
  echo "  Refusing to report a blast count over an unknown denominator." >&2
  exit 1
fi
sort -o "$WORK/fleet.txt" "$WORK/fleet.txt"
FLEET_TOTAL="$(wc -l < "$WORK/fleet.txt" | tr -d ' ')"
if [ "$FLEET_TOTAL" -eq 0 ]; then
  echo "✗ the fleet listing came back empty, which is not a fleet of zero — it is a" >&2
  echo "  broken query or a token without visibility. Refusing to report 0 changes." >&2
  exit 1
fi

# ─── Probes ──────────────────────────────────────────────────────────────────
# Each returns a single line "CODE<TAB>STDOUT-FIRST-LINE". The CODE is the whole
# assertion: 0/3 from lint and 0/1 from bump are ANSWERS, and anything else means
# the run did not produce one. Keeping the raw integer in the record is what lets
# the classifier below tell a finding from a broken probe instead of guessing
# from a non-zero.
probe() { # probe <bin> <dir> <subcommand-and-args...>
  _bin="$1"; _dir="$2"; shift 2
  _st=0
  _out="$( cd "$_dir" && "$_bin" "$@" 2>/dev/null )" || _st=$?
  printf '%s\t%s\n' "$_st" "$(printf '%s' "$_out" | head -1)"
}

# probe_lint exists because the exit code is NOT the whole verdict, and treating
# it as one was this script's worst bug. lint has exactly two answer codes, so a
# repo already at 3 could never register a move however much worse the candidate
# made it — and nine of thirty-five were already at 3, eight of which gain
# violations under the v1.0.0 candidate while being counted as agreeing. The
# headline "10 would change" was wrong by eight.
#
# So the comparable thing is the FINDING SET: every (sha, rule) pair the run
# reported. The envelope is sieved with `sed -n '/^[{]/,$p'` rather than fed to
# jq whole, because a lint run that warns before it fails writes annotations
# ahead of the JSON and jq over the two together is a parse error — the same
# incident internal/workflows pins for lint.yml (t-sws7).
probe_lint() { # probe_lint <bin> <dir> <range>
  _st=0
  _err="$( cd "$2" && "$1" lint --range "$3" 2>&1 >/dev/null )" || _st=$?
  _sig=''
  if [ "$_st" -eq 3 ]; then
    _sig="$(printf '%s\n' "$_err" | sed -n '/^[{]/,$p' |
      jq -r '[(.error.details // [])[] | "\(.sha[0:7]):\(.rule)"] | sort | join(" ")' 2>/dev/null || true)"
    # A code-3 run with no readable detail list is not "no violations"; it is a
    # signature this script cannot compare. Say so rather than emit an empty
    # string that would compare equal to a clean run's.
    [ -n "$_sig" ] || _sig='?unreadable-envelope'
  fi
  printf '%s\t%s\n' "$_st" "$_sig"
}

# An ANSWER is any code glyph reaches by JUDGING. That includes 3 on both gates:
# lint exits 3 for a convention violation and bump exits 3 for a subject it
# refuses to classify, and both are verdicts a consumer acts on — comparing them
# across binaries is the entire point. Only 2 (bad input from this script), 4 (no
# trustworthy answer) and 130 (interrupted) mean no verdict was produced.
#
# Spelled `[ "$status" -eq N ]` rather than as a `case` on "$gate:$code", which
# is how it was first written. The shape is not cosmetic: internal/workflows'
# exit-code inventory walks every .sh under scripts/ looking for exactly this
# pattern, and the `case` form was invisible to it — so glyph's own guard against
# "a shell consumer branches on a stale integer" did not cover its newest shell
# consumer. Keep the form, and keep the rows in exitcodes_test.go beside it.
answered() { # answered <gate> <status>  → 0 when the status is an ANSWER, 1 when the probe broke
  status="$2"
  case "$1" in
    lint) [ "$status" -eq 0 ] || [ "$status" -eq 3 ] ;;
    bump) [ "$status" -eq 0 ] || [ "$status" -eq 1 ] || [ "$status" -eq 3 ] ;;
    *) return 1 ;;
  esac
}

verdict() { # verdict <gate> <code> <stdout> → the human word for a probe result
  case "$1:$2" in
    bump:0) echo "${3:-?}" ;;
    bump:1) echo "none" ;;
    bump:3) echo "refused" ;;
    *) echo "exit$2" ;;
  esac
}

# lintword renders a lint result as its finding COUNT, so a row shows how much
# worse the candidate is rather than merely that both sides were non-zero.
lintword() { # lintword <code> <signature>
  case "$1" in
    0) echo "clean" ;;
    3)
      case "$2" in
        '?unreadable-envelope') echo "3/unreadable" ;;
        *) echo "$(printf '%s' "$2" | wc -w | tr -d ' ')v" ;;
      esac
      ;;
    *) echo "exit$1" ;;
  esac
}

# All report cells are ASCII. printf's %-Ns pads by BYTES, so a multibyte arrow
# or em-dash in a cell silently narrows that row against the header — measured
# at 2 columns per row before this.


# ─── Walk ────────────────────────────────────────────────────────────────────
# Counters are updated in THIS shell, never inside a pipeline: `cmd | while read`
# runs its body in a subshell in POSIX sh, so every increment would be discarded
# and the script would end by printing zeros over real findings. The fleet list
# is read with a redirect on the loop for exactly that reason.
CONSUMERS=0
CHANGED=0
LINT_OK=0
BUMP_OK=0
UNPROBED=0
LOCAL_HEAD=0
FETCH_FAILED=0
BEHIND_PIN=0
PIN_UNVERIFIED=0
LOST_ANSWER=0
: > "$WORK/rows"
: > "$WORK/skips"

# RECENT_CAP bounds the walk in a repo that has never released. Such a repo has
# no release floor, and dropping it would drop a real consumer — sixteen of
# them, measured — so the range becomes its last commits instead. The number is
# a proxy for "recent authorship" and the report PRINTS the range it used, so
# nobody has to infer which question a row answers.
RECENT_CAP=50

echo "→ probing $FLEET_TOTAL repos (baseline: $BASELINE_LABEL)"
while IFS= read -r name; do
  [ -n "$name" ] || continue
  dir="$FLEET_DIR/$name"

  if [ ! -d "$dir/.git" ]; then
    printf '%s\tno local clone at %s\n' "$name" "$dir" >> "$WORK/skips"
    UNPROBED=$((UNPROBED + 1))
    continue
  fi

  # Refresh FIRST: everything below — consumership, the pin, the walk — is read
  # from the remote ref, and an unfetched remote ref answers as of the clone's
  # last visit. A discarded fetch failure is worse than no fetch: passing
  # --fetch removed the "clones NOT refreshed" caveat from the ✓ line whether or
  # not a single fetch had actually succeeded, so an offline run read as the
  # freshest possible one.
  refreshed=''
  if [ -n "$FETCH" ]; then
    if git -C "$dir" fetch --quiet --tags origin >/dev/null 2>&1; then
      refreshed=1
    else
      FETCH_FAILED=$((FETCH_FAILED + 1))
      printf '%s\tfetch failed, so this row is as stale as the clone was\n' "$name" >> "$WORK/skips"
    fi
  fi

  # Walk the REMOTE's head, not the local one. The question is what the fleet
  # looks like, and a local branch can carry unpushed work that is in no repo
  # but this disk — or sit on a topic branch, which would make the row answer
  # about a branch nobody releases from. Falling back to HEAD is allowed but
  # noted, because then the row IS about this disk.
  local_note=''
  if wh="$(git -C "$dir" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null)"; then
    :
  elif br="$(git -C "$dir" symbolic-ref --quiet --short HEAD 2>/dev/null)" &&
       git -C "$dir" rev-parse --verify --quiet "origin/$br" >/dev/null 2>&1; then
    wh="origin/$br"
  else
    wh=HEAD
    local_note='local-HEAD'
  fi

  # A repo that does not run glyph cannot be reddened by glyph. Excluding it is
  # right; excluding it QUIETLY is not, because "not a consumer" and "I forgot to
  # look" produce the same silence. Judged at the ref the walk uses, never the
  # working tree — a clone sits at whatever checkout its last visit left.
  if ! git -C "$dir" grep -q -e "$OWNER/glyph" "$wh" -- .github/workflows .github/actions 2>/dev/null; then
    printf '%s\tno glyph pin site at %s — not a consumer\n' "$name" "$wh" >> "$WORK/skips"
    UNPROBED=$((UNPROBED + 1))
    continue
  fi

  # What is this repo ACTUALLY pinned at? The baseline is one tag for the whole
  # fleet, which is only true when the last rollout finished; a repo genuinely
  # pinned further back sees a LARGER change than any row here — the baseline it
  # would move from is older than the one being compared against. Read from the
  # remote ref, never the working tree: `git fetch` moves origin/* and leaves
  # the tree behind, and the tree-read version of this block reported 29 repos
  # "pinned BELOW the baseline" out of checkouts the fleet had long moved past,
  # while every fetched origin/HEAD carried the true pin (the hub's
  # glyph-pin-audit.yml, which reads the API, agreed). `sort -V`, because
  # v0.12.0 sorts lexically below v0.8.0 and `tail -1` would report the LOWER
  # pin of such a pair. Comments are filtered because every glyph reusable ships
  # a permanently stale commented caller stub (the rollout runbook's trap).
  pin="$(git -C "$dir" grep -hoE "^[^#]*$OWNER/glyph[^@]*@v[0-9]+\.[0-9]+\.[0-9]+" \
           "$wh" -- .github/workflows .github/actions 2>/dev/null |
         grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sort -uV | tail -1 || true)"
  if [ -n "$pin" ] && [ -n "$BASE_TAG_NAME" ] && [ "$pin" != "$BASE_TAG_NAME" ]; then
    if [ -n "$refreshed" ] && [ "$wh" != HEAD ]; then
      BEHIND_PIN=$((BEHIND_PIN + 1))
      printf '%s\tpinned at %s, not the %s baseline — its real change is larger than its row\n' \
        "$name" "$pin" "$BASE_TAG_NAME" >> "$WORK/skips"
    else
      # This run did not refresh the ref it read (or read local HEAD), so the
      # version is an observation from the clone's last fetch — never asserted.
      PIN_UNVERIFIED=$((PIN_UNVERIFIED + 1))
      printf '%s\tpin read %s at %s as of the last fetch — unverified (--fetch to assert)\n' \
        "$name" "$pin" "$wh" >> "$WORK/skips"
    fi
  fi

  tag="$(git -C "$dir" tag --sort=-v:refname 2>/dev/null | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)"
  if [ -n "$tag" ]; then
    rng="$tag..$wh"
  else
    # The floor is an actual COMMIT, taken from the walk, never `<ref>~N`.
    # Arithmetic on `~` is wrong twice over: `rev-list --count` counts every
    # reachable commit while `~N` walks first parents only, so any merge in the
    # history makes N overshoot the chain, and even without merges `~<count>`
    # names the root's parent. Both produce `fatal: bad revision`, which glyph
    # correctly reports as exit 4 — "no trustworthy answer" — and this script
    # then filed as an unanswered gate. Measured before the fix: nine repos
    # blamed on glyph for a bug that was here.
    floor="$(git -C "$dir" rev-list --abbrev-commit --max-count=$((RECENT_CAP + 1)) "$wh" 2>/dev/null | tail -1)"
    if [ -z "$floor" ] || [ "$floor" = "$(git -C "$dir" rev-parse --short "$wh" 2>/dev/null)" ]; then
      printf '%s\tno tag and no history to walk from\n' "$name" >> "$WORK/skips"
      UNPROBED=$((UNPROBED + 1))
      continue
    fi
    rng="$floor..$wh"
  fi

  CONSUMERS=$((CONSUMERS + 1))
  [ -n "$local_note" ] && LOCAL_HEAD=$((LOCAL_HEAD + 1))

  o_lint="$(probe_lint "$BASELINE"  "$dir" "$rng")"
  n_lint="$(probe_lint "$CANDIDATE" "$dir" "$rng")"
  # The release job's own invocation, aimed at the repo it belongs to. It reads
  # the API, which is why a token is required up front rather than discovered
  # here as thirty-five identical exit 4s.
  o_bump="$(probe "$BASELINE"  "$dir" bump --since-tag --repo "$OWNER/$name")"
  n_bump="$(probe "$CANDIDATE" "$dir" bump --since-tag --repo "$OWNER/$name")"

  ol_code="${o_lint%%	*}"; nl_code="${n_lint%%	*}"
  ol_sig="${o_lint#*	}";  nl_sig="${n_lint#*	}"
  ob_code="${o_bump%%	*}"; nb_code="${n_bump%%	*}"
  ob_out="${o_bump#*	}";  nb_out="${n_bump#*	}"

  # Per GATE, because the two break independently and a broken one must not
  # take its neighbour's answer down with it.
  # Three cases per gate, not two. The third — the BASELINE answered and the
  # CANDIDATE cannot — is the gravest verdict this instrument can reach: the
  # release makes glyph stop computing an answer for a repo that has one today.
  # It was being filed as a skip, next to "no local clone", while CHANGED stayed
  # where it was. A regression cannot be routed into the list of things that
  # were never measured.
  lint_cell='-'
  if answered lint "$ol_code" && answered lint "$nl_code"; then
    LINT_OK=$((LINT_OK + 1))
    # The finding SET, not the exit code. See probe_lint.
    if [ "$ol_code" != "$nl_code" ] || [ "$ol_sig" != "$nl_sig" ]; then moved_lint=1; else moved_lint=''; fi
    lint_cell="$(lintword "$ol_code" "$ol_sig") -> $(lintword "$nl_code" "$nl_sig")"
  elif answered lint "$ol_code"; then
    moved_lint=1
    LOST_ANSWER=$((LOST_ANSWER + 1))
    lint_cell="$(lintword "$ol_code" "$ol_sig") -> NO ANSWER(exit $nl_code)"
  else
    moved_lint=''
    printf '%s\tlint gate unanswered over %s (baseline exit %s, candidate exit %s)\n' \
      "$name" "$rng" "$ol_code" "$nl_code" >> "$WORK/skips"
  fi

  bump_cell='-'
  if answered bump "$ob_code" && answered bump "$nb_code"; then
    BUMP_OK=$((BUMP_OK + 1))
    if [ "$ob_code" != "$nb_code" ] || [ "$ob_out" != "$nb_out" ]; then moved_bump=1; else moved_bump=''; fi
    bump_cell="$(verdict bump "$ob_code" "$ob_out") -> $(verdict bump "$nb_code" "$nb_out")"
  elif answered bump "$ob_code"; then
    moved_bump=1
    LOST_ANSWER=$((LOST_ANSWER + 1))
    bump_cell="$(verdict bump "$ob_code" "$ob_out") -> NO ANSWER(exit $nb_code)"
  else
    moved_bump=''
    printf '%s\tbump gate unanswered over %s (baseline exit %s, candidate exit %s)\n' \
      "$name" "$rng" "$ob_code" "$nb_code" >> "$WORK/skips"
  fi

  moved=''
  [ -n "$moved_lint" ] && moved="lint"
  [ -n "$moved_bump" ] && moved="${moved:+$moved+}bump"
  [ -z "$moved" ] && continue

  CHANGED=$((CHANGED + 1))
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$name" "$moved" "$rng" "$lint_cell" "$bump_cell" \
    "$local_note" >> "$WORK/rows"
done < "$WORK/fleet.txt"

# ─── Report ──────────────────────────────────────────────────────────────────
echo
if [ "$CHANGED" -gt 0 ]; then
  echo "REPOS WHOSE VERDICT MOVES"
  printf '  %-18s %-9s %-22s %-22s %-24s %s\n' REPO GATE RANGE LINT BUMP NOTE
  while IFS='	' read -r name moved rng lint bump note; do
    printf '  %-18s %-9s %-22s %-22s %-24s %s\n' "$name" "$moved" "$rng" "$lint" "$bump" "$note"
  done < "$WORK/rows"
  echo
  if [ "$LOST_ANSWER" -gt 0 ]; then
    echo "  !! $LOST_ANSWER gate(s) show NO ANSWER under the candidate where the baseline had one."
    echo "     That is not a changed verdict, it is a LOST one: the release makes glyph stop"
    echo "     computing a result for a repo that has one today. Read those rows first."
    echo
  fi
  echo "  lint moves are a PREDICTION: CI lints a pull request's own commits, so"
  echo "  nothing already merged is re-judged and no existing run turns red. It"
  echo "  says the habit that wrote those commits is still live in that repo."
  echo "  bump moves are RETROACTIVE: that repo's next release cuts a different"
  echo "  version than it would today, having changed nothing itself."
  echo
fi

if [ -s "$WORK/skips" ]; then
  echo "NOT MEASURED — each with its reason"
  while IFS='	' read -r name why; do
    printf '  %-18s %s\n' "$name" "$why"
  done < "$WORK/skips"
  echo
fi

# The ✓ line carries every weakening of the claim, because the ✓ line is what
# gets read — the same stance scripts/check.sh takes with ALLOW_DIRTY and the
# golangci-lint skew.
# A run that probed nothing is not a run that found nothing. UNPROBED used to be
# incremented at every skip and then never read, so with no clones on disk the
# script printed a ✓, a flat "0 would change" and exit 0 over a fleet it had
# measured none of — the exact vacuous green everything else here refuses.
if [ "$CONSUMERS" -eq 0 ]; then
  echo "✗ none of the $FLEET_TOTAL fleet repos could be probed ($UNPROBED skipped, each listed above)." >&2
  echo "  \"0 would change\" over a fleet nothing was measured of is not a result." >&2
  exit 1
fi

NOTE=''
if [ -z "$FETCH" ]; then
  NOTE="$NOTE — clones NOT refreshed, so this is the fleet as of each clone's last fetch (--fetch)"
elif [ "$FETCH_FAILED" -gt 0 ]; then
  NOTE="$NOTE — $FETCH_FAILED fetch(es) FAILED, so those rows are as stale as their clone"
fi
[ "$UNPROBED" -gt 0 ] && NOTE="$NOTE — $UNPROBED of $FLEET_TOTAL never probed"
[ "$LOCAL_HEAD" -gt 0 ] && NOTE="$NOTE — $LOCAL_HEAD repo(s) walked at local HEAD (no remote-tracking ref)"
[ "$BEHIND_PIN" -gt 0 ] && NOTE="$NOTE — $BEHIND_PIN repo(s) pinned BELOW the baseline, so their real change exceeds their row"
[ "$PIN_UNVERIFIED" -gt 0 ] && NOTE="$NOTE — $PIN_UNVERIFIED repo(s) showed a non-baseline pin at last fetch (unverified; --fetch to assert)"
[ "$LOST_ANSWER" -gt 0 ] && NOTE="$NOTE — $LOST_ANSWER gate(s) LOST their answer under the candidate"
if [ "$LINT_OK" -lt "$CONSUMERS" ] || [ "$BUMP_OK" -lt "$CONSUMERS" ]; then
  NOTE="$NOTE — gates unanswered (lint $LINT_OK/$CONSUMERS, bump $BUMP_OK/$CONSUMERS)"
fi
case "$NOTE" in *?*) NOTE="$NOTE — so $CHANGED is a FLOOR" ;; esac
# "N/35 consume glyph and were probed" asserted a fact about the FLEET — that the
# other 33 do not consume glyph — when the real reduction was "I do not have 33 of
# the clones". Say what was probed and leave the reasons to the list above.
echo "✓ preflight: probed $CONSUMERS of $FLEET_TOTAL fleet repos; $CHANGED would change — baseline $BASELINE_LABEL$NOTE"
