#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. A green run here means a green CI.
#
# That sentence is a CHECK, not a claim: every mirrored gate is DECLARED below
# and RECONCILED at the end — each must actually have run, or the final line is
# not printed and the exit is non-zero. A missing tool is a failure, not a skip.
# A verification script that overstates its coverage is worse than one that
# admits a gap, because the gap is what you were relying on it for.
#
# ─── MIRRORED ────────────────────────────────────────────────────────────────
#   commit-lint     commit-lint.yml `lint`       `glyph lint --range`, judged by
#                                                this tree; the final line names
#                                                the tag CI judges at
#   dist-gate       build.yml `dist-gate`        `scripts/dist-gate.sh origin/main
#                                                <the merge commit CI tests>`
#   golden-gate     build.yml `golden-gate`      `scripts/golden-gate.sh origin/main
#                                                <the merge commit CI tests>`
#   repo-policy     repo-policy.yml `repo-policy` the hub's repo-policy-check.sh at
#                                                the tag the caller pins, over
#                                                this tree
#   module-hygiene  build.yml `ci` → go-ci.yml   `go mod tidy -diff`, verify
#   build           build.yml `ci` → go-ci.yml   `go build ./...`
#   vet             build.yml `ci` → go-ci.yml   `go vet ./...`
#   race-test       build.yml `ci` → go-ci.yml   `go test -race ./...`
#   golangci-lint   build.yml `ci` → go-ci.yml   `golangci-lint run --enable=$LINT_FLOOR
#                                                ./...`, the floor read from go-ci.yml
#                                                at the tag
#   lint-floor      build.yml `ci` → go-ci.yml   the 'lint floor' step's own run:
#                                                block, read from go-ci.yml at the
#                                                tag and run as the runner runs it
#   govulncheck     govulncheck.yml `scan` → go-vuln.yml source + `-mode binary`
#   bite            build.yml `bite`             the hub's go-bite.sh at the tag, on
#                                                the merge commit CI tests
#   mutations       build.yml `mutations`        `scripts/mutations.sh`
#   fuzz-smoke      build.yml `extras`           `scripts/fuzz-smoke.sh`
#   release-history build.yml `extras`           the caller-check bounds against
#                                                glyph's release tags
#   smoke           build.yml `extras`           `scripts/smoke.sh <binary>`
#
# Reported beside them in the final line, a REPORT and never a gate, so outside
# the count: coverage, go-ci.yml's `coverage: true`, the total line off the race
# run's profile.
#
# ─── NOT MIRRORED, each with its reason ──────────────────────────────────────
# Naming these is the honest half. Reproducing every CI job would cost more to
# maintain than the code it guards, and a hand-written second implementation of
# a fleet policy is a second source of truth — the thing this repo refuses
# everywhere else.
#   commit-lint title (commit-lint.yml `lint`, the 'Lint the squash title' step) `glyph lint --pr N`:
#                                       the squash title is a pull request's, and
#                                       exists nowhere locally. Check a planned
#                                       one by hand with
#                                       `glyph lint --message "<title>"`.
#   zizmor        (zizmor.yml `zizmor`) Actions-security lint over .github/. The
#                                       policy is the fleet's; a shell copy of it
#                                       would drift against the canonical one.
#   actionlint    (actionlint.yml `actionlint`) Workflow syntax, expression types, and
#                                       `run:` blocks through shellcheck. The tool's
#                                       VERSION and its SHA256 are pinned once in
#                                       the hub's reusable and are deliberately
#                                       not caller-tunable, so a local actionlint
#                                       is whatever nixpkgs currently ships — a
#                                       green here would be a claim about a
#                                       different linter, which is the same
#                                       "green here != green CI" defect the
#                                       golangci-lint skew line below reports
#                                       rather than hides.
#   taplo         (taplo.yml `taplo`)   TOML formatting over glyph.toml and the
#                                       presets. Same argument as actionlint:
#                                       the tool version is pinned in the hub's
#                                       reusable, so a local taplo is whatever
#                                       nixpkgs ships — a green here would be a
#                                       claim about a different formatter.
#   version-preview (version-preview.yml `verdict`) posts the merge-preview COMMENT on a
#                                       pull request. There is no pull request
#                                       locally; `glyph preview --pr N` is the
#                                       command, and it needs the API.
#   task-status   (task-status.yml `sync`) acts on the furrow board, not on this
#                                       tree — nothing here can make it fail.
#   goreleaser    (goreleaser.yml)      tag-triggered release build and signing.
#                                       Never runs on a pull request.
#   codeql        (repo setting)        GitHub-hosted analysis; no local runner.
#
# `ALLOW_DIRTY=1` runs against a dirty tree and says so in the final line: the
# claim is about a COMMIT, and a claim about "HEAD plus whatever is in my editor"
# is not one anybody can act on later.
#
# GOTOOLCHAIN is deliberately left UNSET — exactly as build.yml leaves it. go.mod
# carries a `toolchain` line, so Go resolves that toolchain (fetching it once if
# absent) and the run is pinned by go.mod rather than by whichever SDK happens to
# be installed on this machine. Forcing GOTOOLCHAIN=local here would check against
# a *different* Go than CI uses, quietly breaking the "green here == green CI"
# contract this script exists to provide: a stdlib vulnerability patched in the
# pinned toolchain but present in the installed one shows up as a local govulncheck
# failure CI never sees (and the reverse can hide a real one). Only a repo with NO
# `toolchain` line — floor-only, resolving to whatever is installed by design —
# should pin GOTOOLCHAIN=local.
set -eu
cd "$(dirname "$0")/.."

# Every file this run writes lives in WORK, apart from the objects of the merge
# it synthesizes below for CI's view, which git writes into this repository's
# store as it writes any object. bin/ is build.sh's, and the path
# fleet-preflight.sh is handed its candidate at: a smoke build there replaced a
# candidate mid-preflight with an unstamped binary (2026-09-10). A signal
# cleans up and re-raises (scripts/lib/trap.sh says why).
WORK="$(mktemp -d)"
cleanup() {
  rm -rf "$WORK"
}
# shellcheck source=scripts/lib/trap.sh
. ./scripts/lib/trap.sh
reraise_on_signals cleanup

# MIRRORS is the reconciliation set: the run refuses its ✓ line unless every name
# here was recorded by `ran`. Keeping it as data rather than as a comment is what
# makes "a gate silently stopped running" a failure instead of a nicer-looking log.
MIRRORS='commit-lint dist-gate golden-gate repo-policy module-hygiene build vet race-test golangci-lint lint-floor govulncheck bite mutations fuzz-smoke release-history smoke'
NOT_MIRRORED='commit-lint title, zizmor, actionlint, taplo, version-preview, task-status, goreleaser, codeql'
RAN=''
ran() { RAN="$RAN $1 "; }

# The fleet's gates live in the hub (akira-toriyama/.github), beside this repo in
# the house layout. --git-common-dir resolves to the PRIMARY checkout even from a
# worktree, so the sibling lookup works in both; GLYPH_FLEET_HUB overrides it.
HUB="${GLYPH_FLEET_HUB:-}"
if [ -z "$HUB" ]; then
  HUB="$(dirname "$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")")/.github"
fi

# hub_at <reusable> <caller.yml> leaves HUB_AT naming a private copy of the hub at
# the tag .github/workflows/<caller.yml> pins <reusable> at, the revision CI's run
# resolves on the remote and checks out (the self-checkout at job.workflow_sha in
# the hub's repo-policy.yml). Never the clone's working tree: that holds whichever
# branch someone has out. Never the clone's copy of the tag on its own word either:
# these tags move, and a plain `git fetch` or `git pull` exits 0 and leaves a moved
# tag where it was (measured 2026-11-04, git 2.54). So the tag is fetched by name
# from the clone's origin into a repository of this run's own under WORK — the
# clone is read for its origin's URL and, when that origin cannot be reached, for
# its copy of the tag, and is written to by nothing here: a tag someone moved in
# it and has not pushed yet is theirs, and a forced fetch into it would reset the
# move without a trace (tags keep no reflog). A refresh that fails weakens the
# claim and does not end the run, on the golangci-lint skew's principle below: a
# GitHub outage must not make this script unusable, and the ✓ line is where a
# weakened claim has to appear.
HUB_SKEW=""
hub_at() {
  hub_pin="$(sed -n "s|^[[:space:]]*uses: akira-toriyama/\.github/\.github/workflows/$1@\([^[:space:]]*\).*|\1|p" ".github/workflows/$2" | head -1)"
  if [ -z "$hub_pin" ]; then
    echo "  ✗ $2 calls no $1 at a tag, so nothing says which revision of it CI runs." >&2
    exit 1
  fi
  HUB_AT="$WORK/hub-$hub_pin"
  [ ! -d "$HUB_AT" ] || return 0
  hub_pin_repo="$WORK/hub-$hub_pin.git"
  git init -q --bare "$hub_pin_repo"
  hub_url="$(git -C "$HUB" remote get-url origin 2>/dev/null || true)"
  if [ -z "$hub_url" ] || ! git -C "$hub_pin_repo" fetch --quiet --no-tags --depth 1 "$hub_url" "refs/tags/$hub_pin:refs/tags/$hub_pin"; then
    HUB_SKEW="${HUB_SKEW:+$HUB_SKEW; }hub $hub_pin read at the clone's copy of the tag, which could not be refreshed from origin and CI may have moved past"
    if ! git -C "$hub_pin_repo" fetch --quiet --no-tags --depth 1 "$HUB" "refs/tags/$hub_pin:refs/tags/$hub_pin"; then
      echo "  ✗ cannot read the hub at $hub_pin, the tag $2 pins $1 at, from its origin or from $HUB" >&2
      echo "    It lives in akira-toriyama/.github, which the house layout keeps beside this repo:" >&2
      echo "      git clone git@github.com:akira-toriyama/.github.git \"$HUB\"" >&2
      echo "    or point GLYPH_FLEET_HUB at an existing clone. Skipping the gates it holds would" >&2
      echo "    put this script back to overstating its coverage." >&2
      exit 1
    fi
  fi
  mkdir "$HUB_AT"
  git -C "$hub_pin_repo" archive "refs/tags/$hub_pin" | tar -xf - -C "$HUB_AT"
}

# A claim about a dirty tree is a claim about nothing reproducible, so the default
# is to refuse. The escape hatch exists because the edit/verify loop needs it, and
# it changes the final line rather than staying quiet — the whole failure this
# script is being fixed for was a run that looked like a verdict and was not.
DIRTY=''
if [ -n "$(git status --porcelain)" ]; then
  if [ "${ALLOW_DIRTY:-}" = 1 ]; then
    DIRTY=1
  else
    echo "✗ the working tree is dirty, so a green run here would not be a claim about any commit." >&2
    echo "  Commit or stash first, or re-run with ALLOW_DIRTY=1 (the final line then says so)." >&2
    git status --short >&2
    exit 2
  fi
fi

# What the hub's gates are, read up front: a run that cannot read them will not
# reach its ✓ line, and that takes a second to find out here and twenty minutes
# at the gate.
echo "→ the hub at the tags the callers pin"
hub_at go-bite.yml build.yml
BITE="$HUB_AT/actions/go-bite/go-bite.sh"
hub_at go-ci.yml build.yml
GO_CI="$HUB_AT/.github/workflows/go-ci.yml"
hub_at repo-policy.yml repo-policy.yml
REPO_POLICY="$HUB_AT/scripts/repo-policy-check.sh"

# The convention gate the fleet runs on every pull request (commit-lint.yml calls
# glyph's own lint.yml). Mirrored here because the alternative — push, wait, read
# a red CI — is a round trip for a mistake that is one character wide, and it has
# actually happened. A missing origin/main is a stale checkout, not a pass.
echo "→ glyph lint --range origin/main..HEAD"
if ! git rev-parse --verify --quiet origin/main >/dev/null; then
  echo "  ✗ no origin/main in this checkout, so the commit range CI lints cannot be formed." >&2
  echo "    Run \`git fetch origin\` first." >&2
  exit 1
fi
if [ -n "$(git rev-list origin/main..HEAD)" ]; then
  go run ./cmd/glyph lint --range origin/main..HEAD
else
  echo "  (no commits ahead of origin/main — nothing to lint)"
fi
ran commit-lint
# Judged by this tree's lint, where CI judges with the binary of the tag
# commit-lint.yml pins: the two can split on a rule this tree changed. The final
# line says so on every run; running the pinned binary as well is a second build
# for a split the pull request shows anyway.
lint_pin="$(sed -n 's|^[[:space:]]*uses: akira-toriyama/glyph/\.github/workflows/lint\.yml@\([^[:space:]]*\).*|\1|p' .github/workflows/commit-lint.yml | head -1)"
LINT_JUDGE="commit-lint judged by this tree, CI judges at ${lint_pin:-a tag commit-lint.yml no longer names}"

# The commit CI tests. A pull request's jobs check out refs/pull/N/merge, the
# merge of the branch into its base as the base is now, so every gate that reads
# a diff or a tree — dist-gate, golden-gate and bite — is handed that commit, built
# here once the way GitHub builds it. The branch head is not it: the head's diff
# runs from the fork point, and on a branch stacked on a pull request main has
# since squashed that diff carries the parent's files too, so dist-gate counted the
# parent's test as this branch's evidence (exit 0 here, exit 1 in CI) and
# golden-gate demanded a trailer for the parent's golden (exit 1 here, exit 0 in
# CI), both measured 2026-11-08. A stale origin/main gives a stale merge — hence
# the fetch check above. With nothing ahead of origin/main there is no pull
# request and no merge, and the gates judge HEAD, an empty range. A conflict is no
# merge either, and GitHub runs no CI on one, so it ends the run here.
CI_HEAD="$(git rev-parse HEAD)"
if [ -n "$(git rev-list origin/main..HEAD)" ]; then
  if ! ci_tree="$(git merge-tree --write-tree origin/main HEAD)"; then
    echo "  ✗ HEAD does not merge cleanly into origin/main, so CI has no merge commit to run on:" >&2
    printf '%s\n' "$ci_tree" | sed 's/^/    /' >&2
    exit 1
  fi
  CI_HEAD="$(git -c user.name=check.sh -c user.email=check.sh@localhost commit-tree -p origin/main -p HEAD -m 'the merge commit CI tests' "$ci_tree")"
fi

# The distribution-layer gate build.yml runs on every pull request: a change to
# a reusable workflow, goreleaser.yml, the install action or .goreleaser.yaml
# must carry an internal/workflows test added or modified (or a Dist-gate-exempt
# trailer on every commit; a same-action SHA pin swap alone needs neither). Same
# script, same base, same head — origin/main is what a pull request from here
# would be judged against, and CI_HEAD is the commit CI's checkout holds, so the
# diff is the one CI judges. Like `bite` below it reads COMMITTED history: an
# uncommitted test does not satisfy it, and that is the same claim-about-a-commit
# stance as the dirty check at the top.
echo "→ dist-gate (does a distribution-layer change carry evidence?)"
sh scripts/dist-gate.sh origin/main "$CI_HEAD"
ran dist-gate

# The golden gate build.yml runs on every pull request: a diff that rewrites a
# golden (testdata/*.golden.*) must carry a
# `Golden-change: <reason>` trailer on every non-merge commit — `-update`
# regenerates a golden from whatever the code now produces, so the trailer is
# what forces the diff to be read as the spec change it is. Same script, same
# base, same head, same committed-history stance as dist-gate above.
echo "→ golden-gate (does a golden rewrite state its reason?)"
sh scripts/golden-gate.sh origin/main "$CI_HEAD"
ran golden-gate

# The house-policy gate repo-policy.yml runs on every pull request: the hub's
# repo-policy-check.sh — the *.ja.* translation ban, and the available(macOS) half
# that stands down with no root Package.swift, i.e. here — at the tag the caller
# pins, as the reusable checks it out at job.workflow_sha. It judges this checkout
# where CI judges the merge, so the two verdicts can part on what origin/main has
# changed since the fork: a translation main gained is red in CI and green here,
# one main deleted is red here and green in CI (both measured 2026-11-09, the
# checker at v2 over a branch checkout and over its merge). bash, as the reusable's
# `run:` has it.
echo "→ repo-policy (the hub's house policy over this tree)"
bash "$REPO_POLICY"
ran repo-policy

# Module hygiene: fail if go.mod/go.sum are not tidy, and verify the downloaded
# dependencies match go.sum. `-diff` prints the needed changes and exits non-zero
# without touching the files (Go 1.23+), so this is a pure gate under `set -e`.
echo "→ go mod tidy -diff && go mod verify"
go mod tidy -diff
go mod verify
ran module-hygiene

echo "→ go build"
go build ./...
ran build

echo "→ go vet"
go vet ./...
ran vet

# No -count=1, deliberately, and this is the one place the mirror is exact rather
# than stricter: go-ci.yml runs the same command without it. Adding it here would
# move this script AWAY from CI, not towards it. Add it by hand when chasing a
# flake — a cached green is a real risk, it is just not a mirror defect.
echo "→ go test -race (coverage rides the same run)"
COVER="$WORK/cover.out"
go test -race -covermode=atomic -coverprofile="$COVER" ./...
ran race-test
# The total line off the profile the race run just wrote — a REPORT, never a
# gate: coverage is read for risk (an unexecuted branch is a cheap
# candidate finder for the mutation ledger), and a threshold would turn the
# number into a target, which buys the tests that assert nothing. The ledger
# below stays the authority on whether tests bite — 93% coverage has been
# measured beside three surviving mutations. So the pipeline's status decides
# nothing, and the final line says what came of it.
cover_total="$(go tool cover -func="$COVER" | tail -n 1)"
case "$cover_total" in
  total:*)
    echo "$cover_total"
    COVERAGE="coverage reported: $(printf '%s\n' "$cover_total" | awk '{ print $NF }')"
    ;;
  *) COVERAGE="coverage NOT reported: go tool cover could not read the race run's profile" ;;
esac

echo "→ fuzz smoke (bounded)"
sh scripts/fuzz-smoke.sh
ran fuzz-smoke

# Mirrors build.yml's "release-history bounds" step: every caller-check table
# row's After must be the newest release tag whose reusable declared otherwise
# (DESIGN §7). The race run above ran the same test, but it skips without
# glyph's tags; GLYPH_RELEASE_HISTORY=required makes a clone with no tags (or
# a shallow one) a failure here instead. -count=1 because the answer depends
# on the tags, which Go's test cache does not track.
echo "→ release-history (caller-check bounds against glyph's release tags)"
GLYPH_RELEASE_HISTORY=required go test ./internal/doctor -run '^TestCallerDeclarationBoundsMatchReleasedTags$' -count=1
ran release-history

# Does the suite BITE? The step above proves the tests PASS, which is a weaker
# claim than it reads as: a line every test executes but nobody asserts is
# invisible to both `go test` and coverage. Each testdata/mutations/*.patch
# breaks one argued decision and the ledger names the test that must then fail.
# One snapshot copy, patch and build per row, so it sits after the fast gates
# but ahead of the binary smoke.
echo "→ mutation ledger (does the suite bite?)"
sh scripts/mutations.sh
ran mutations

# Do the tests this branch ADDS bite? The ledger above asks it of a standing list
# of decisions; this asks it of the diff, which is the half CI gates on. The
# script is the hub's, not a reimplementation — a second copy of a fleet gate
# would drift against the canonical one, which is exactly the mistake the
# NOT-MIRRORED list refuses.
#
# BASE_SHA is the commit CI merges into and HEAD_SHA the merge commit CI tests
# (go-bite.yml: base.sha, github.sha), so the pre-change tree is origin/main as it
# is now and not the fork point: handed the branch head, go-bite credits a test
# pinning what origin/main has shipped since the fork as a bite CI then refuses.
# CI_HEAD above is that merge, or HEAD itself with nothing ahead of origin/main,
# where go-bite stands down on the empty range.
#
# This gate reads COMMITTED history and nothing else. A test sitting uncommitted
# in the working tree is invisible to it, and it stands down quietly rather than
# failing — so a mid-edit run tells you nothing about the test you just wrote.
# Commit first, then re-run. (ALLOW_DIRTY's final line makes the same point about
# the run as a whole.)
echo "→ bite (do this branch's new tests fail without it?)"
# go-bite exports GOTOOLCHAIN=local on the premise that setup-go has already
# installed the toolchain go.mod names. In CI that premise holds. Locally it fails
# twice over: `nix develop` supplies whatever nixpkgs' go currently is (measured:
# 1.26.4, against go.mod's `toolchain go1.26.5`), and a mise install leaks
# GOROOT=…/go/1.26.5 into the shell — so the 1.26.4 driver reaches into a 1.26.5
# GOROOT and every package dies with `compile: version "go1.26.5" does not match go
# tool version "go1.26.4"`. The gate then reports `cannot build bitescan` and no
# verdict at all, and the run never reaches its ✓ line (t-wmwb).
#
# `unset GOROOT` clears the error and is the WRONG fix: it leaves this gate running
# under nixpkgs' go while CI runs go.mod's, trading a loud failure for a silent
# "green here != green CI" — the defect the header above exists to refuse. Make
# go-bite's premise true instead. Every other gate here resolves the `toolchain`
# line through GOTOOLCHAIN=auto, so `go env GOROOT` already names that toolchain's
# root; handing it over as GOROOT plus PATH is what setup-go does in CI. On a
# machine whose go IS go.mod's toolchain this resolves to that same go, so it costs
# nothing outside the devshell.
BITE_GOROOT="$(go env GOROOT)"
# bash, as the action's `shell: bash` runs it: the script is bash (pipefail,
# process substitution), and `sh` is bash only on some machines.
BASE_SHA="$(git rev-parse origin/main)" HEAD_SHA="$CI_HEAD" \
  GOROOT="$BITE_GOROOT" PATH="$BITE_GOROOT/bin:$PATH" bash "$BITE"
ran bite

# A CI gate whose tool is missing is a FAILURE and not a skip: a `command -v`
# guard that prints "skipped" and lets the run finish with its success line
# reports a gate that never ran, and nothing says so.
if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  # Running the gate is not the same as running CI'S gate. go-ci.yml pins the
  # version it installs; `nix develop` supplies whatever nixpkgs currently has,
  # and the two drift apart silently because a linter that gains an analyzer
  # gains it in a minor release. Measured 2026-08-02: local v2.6.2 against CI's
  # v2.12.2, six minors apart — `modernize`'s stringscut exists in one and not
  # the other, so this script printed 12/12 and the pull request went red on a
  # finding it could not have produced.
  #
  # Reported, not fatal: failing here would make the script unusable until the
  # flake is bumped, and an unusable check is how people stop running it. It
  # goes in the FINAL LINE instead, next to ALLOW_DIRTY, on the same principle —
  # the ✓ line is what gets read, so the ✓ line is where a weakened claim has to
  # appear.
  LINT_SKEW=""
  want_lint="$(sed -n "/golangci-lint-version:/,/default:/s/.*default:[[:space:]]*'v\{0,1\}\([0-9][0-9.]*\)'.*/\1/p" "$GO_CI" 2>/dev/null | head -1)"
  got_lint="$(golangci-lint --version 2>/dev/null | sed -n 's/.*version[[:space:]]*v\{0,1\}\([0-9][0-9.]*\).*/\1/p' | head -1)"
  if [ -z "$want_lint" ]; then
    LINT_SKEW="golangci-lint: could not read the version go-ci.yml pins, so this run cannot say it matched CI"
  elif [ -z "$got_lint" ]; then
    LINT_SKEW="golangci-lint: could not read the LOCAL version, so this run cannot say it matched CI"
  elif [ "$want_lint" != "$got_lint" ]; then
    LINT_SKEW="golangci-lint v$got_lint here vs v$want_lint in CI — findings only the newer one has are invisible to this run"
  fi
  # The fleet's floor, as go-ci.yml at the pin hands it to the action: defined once
  # as the job's LINT_FLOOR, passed as `args: --enable=${{ env.LINT_FLOOR }}`.
  # `--enable` adds its linters to whatever .golangci.yml lists, so a bare `run`
  # judges with the repo's list alone and a linter the config leaves out is a
  # finding only CI sees (measured with misspell, 2026-11-04). Args of any other
  # shape end the run: this mirrors one command, and a guess at another is the
  # green-here-red-there this script exists to refuse.
  lint_floor="$(sed -n 's/^[[:space:]]*LINT_FLOOR:[[:space:]]*//p' "$GO_CI" | head -1)"
  lint_args="$(sed -n '/golangci-lint-action@/,/args:/s/^[[:space:]]*args:[[:space:]]*//p' "$GO_CI")"
  # shellcheck disable=SC2016 # the expression is go-ci.yml's text, not this shell's
  if [ -z "$lint_floor" ] || [ "$lint_args" != '--enable=${{ env.LINT_FLOOR }}' ]; then
    echo "  ✗ go-ci.yml at the hub's pin no longer lints with --enable=\$LINT_FLOOR (args: ${lint_args:-none}; LINT_FLOOR: ${lint_floor:-none}), the one command this gate mirrors — re-derive it from go-ci.yml's golangci-lint step." >&2
    exit 1
  fi
  # The floor check go-ci.yml runs after the lint: `--enable` cannot hold a linter
  # the config drops — a `linters.disable` entry beats the flag (golangci-lint
  # v2.12.2, measured 2026-11-04), and the run above then exits 0 with that linter
  # never run — so the step asks `golangci-lint linters` what came out enabled. It
  # runs here as its own gate, off the step's own run: block read out of go-ci.yml
  # at the pin, under the shell the runner gives `shell: bash` and with the job's
  # LINT_FLOOR in its environment. Not retyped: a copy here would be a second home
  # for the fleet's floor check, and the one it has is the step. A go-ci.yml with
  # no such step, or a step the runner would substitute into, is one this script
  # cannot mirror and ends the run before the lint, as the args check does.
  lint_floor_step="$(awk '
    /^[[:space:]]*- name: lint floor/ { instep = 1; next }
    instep && /^[[:space:]]*- name:/ { exit }
    instep && /^[[:space:]]*run: \|[[:space:]]*$/ { inrun = 1; next }
    inrun {
      if ($0 ~ /^[[:space:]]*$/) { print ""; next }
      match($0, /^[[:space:]]*/)
      if (ind == 0) ind = RLENGTH
      if (RLENGTH < ind) exit
      print substr($0, ind + 1)
    }
  ' "$GO_CI")"
  if [ -z "$lint_floor_step" ]; then
    echo "  ✗ go-ci.yml at the hub's pin has no 'lint floor' step with a run: block, the floor check this gate mirrors — re-derive it from go-ci.yml's step after golangci-lint." >&2
    exit 1
  fi
  # shellcheck disable=SC2016 # the expression is go-ci.yml's text, not this shell's
  case "$lint_floor_step" in *'${{'*)
    echo "  ✗ go-ci.yml's 'lint floor' step carries a \${{ }} expression, which the runner substitutes before the shell starts and this script does not — it reads the job's LINT_FLOOR from the environment, so move the value there." >&2
    exit 1 ;;
  esac
  printf '%s\n' "$lint_floor_step" >"$WORK/lint-floor.sh"
  golangci-lint run --enable="$lint_floor" ./...
  ran golangci-lint
  echo "→ lint floor (every floor linter was in the set)"
  LINT_FLOOR="$lint_floor" bash --noprofile --norc -eo pipefail "$WORK/lint-floor.sh"
  ran lint-floor
  [ -n "$LINT_SKEW" ] && echo "  ! $LINT_SKEW"
else
  echo "✗ golangci-lint is not installed, and go-ci.yml runs it — so this run cannot" >&2
  echo "  claim a green CI. \`nix develop\` supplies it (flake.nix's devShell), or install it." >&2
  exit 1
fi

echo "→ build binary for live checks"
go build -o "$WORK/glyph" ./cmd/glyph

# go-vuln.yml's two halves, with its scanner: govulncheck@latest (the hub
# refuses a pinned one — a frozen scanner reports less and stays green) over
# the source, then `-mode binary` over the built binary, which catches stdlib
# vulnerabilities reachable in the shipped artifact that source mode can miss.
# A govulncheck from PATH or the devShell is whatever version was installed,
# a different scanner from CI's. GOTOOLCHAIN=auto lets the scanner build under
# an installed Go older than x/vuln's own `go` line; the scans still resolve
# go.mod's toolchain, as every gate here does.
echo "→ govulncheck (source + -mode binary)"
GOTOOLCHAIN=auto go run golang.org/x/vuln/cmd/govulncheck@latest ./...
GOTOOLCHAIN=auto go run golang.org/x/vuln/cmd/govulncheck@latest -mode binary "$WORK/glyph"
ran govulncheck

sh scripts/smoke.sh "$WORK/glyph"
ran smoke

# Reconciliation. Every gate that got this far exited 0 under `set -e`, so what is
# left to establish is that each one RAN — a gate quietly not running is the
# failure no exit code reports.
missing=''
total=0
for g in $MIRRORS; do
  total=$((total + 1))
  case "$RAN" in *" $g "*) ;; *) missing="$missing $g" ;; esac
done
if [ -n "$missing" ]; then
  echo "✗ these declared gates never ran:$missing" >&2
  echo "  The header promises a green run here means a green CI, so there is no ✓ line." >&2
  echo "  Either the gate was removed without leaving MIRRORS, or a \`ran\` call was lost." >&2
  exit 1
fi

SKEW=""
[ -n "$HUB_SKEW" ] && SKEW=" — SKEWED FROM CI: $HUB_SKEW"
[ -n "${LINT_SKEW:-}" ] && SKEW="$SKEW — SKEWED FROM CI: $LINT_SKEW"
CLAIMS="NOT mirrored: $NOT_MIRRORED — $LINT_JUDGE — $COVERAGE$SKEW"
if [ -n "$DIRTY" ]; then
  echo "✓ passed (DIRTY TREE — not a claim about HEAD): $total/$total mirrored gates — $CLAIMS"
else
  echo "✓ $total/$total mirrored gates passed — $CLAIMS"
fi
