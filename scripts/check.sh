#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. A green run here means a green CI.
#
# That sentence used to be a claim and is now a CHECK. It was false in both
# directions: this script never mirrored build.yml's `bite` job, and it ran
# govulncheck, which build.yml does not contain (that gate is its own caller,
# .github/workflows/govulncheck.yml). Worse, the two linters were guarded by
# `command -v` and a run that skipped them both still ended `✓ all checks
# passed` — measured on a machine where govulncheck is not installed, so the
# skip was not hypothetical but the normal case (t-7tj3). A verification script
# that overstates its coverage is worse than one that admits a gap, because the
# gap is what you were relying on it for.
#
# So the gates are now DECLARED below and RECONCILED at the end: every mirrored
# gate must actually have run, or the final line is not printed and the exit is
# non-zero. A missing tool is a failure, not a skip.
#
# ─── MIRRORED ────────────────────────────────────────────────────────────────
#   commit-lint     commit-lint.yml → lint.yml   `glyph lint --range`
#   dist-gate       build.yml `dist-gate`        `scripts/dist-gate.sh origin/main`
#   golden-gate     build.yml `golden-gate`      `scripts/golden-gate.sh origin/main`
#   module-hygiene  go-ci.yml (via build.yml's `ci`) `go mod tidy -diff`, verify
#   build           go-ci.yml                    `go build ./...`
#   vet             go-ci.yml                    `go vet ./...`
#   race-test       go-ci.yml                    `go test -race ./...`
#   coverage        build.yml `coverage`         total line off the race run's
#                                                profile — a REPORT, never a gate
#   golangci-lint   go-ci.yml                    `golangci-lint run ./...`
#   govulncheck     govulncheck.yml → go-vuln.yml `govulncheck ./...`
#   bite            build.yml `bite`             the hub's go-bite.sh
#   mutations       build.yml `mutations`        `scripts/mutations.sh`
#   fuzz-smoke      build.yml `extras`           the bounded fuzz loop
#   smoke           build.yml `extras`           the shipped binary's contract
#
# ─── NOT MIRRORED, each with its reason ──────────────────────────────────────
# Naming these is the honest half. Reproducing every CI job would cost more to
# maintain than the code it guards, and a hand-written second implementation of
# a fleet policy is a second source of truth — the thing this repo refuses
# everywhere else.
#   zizmor        (zizmor.yml)          Actions-security lint over .github/. The
#                                       policy is the fleet's; a shell copy of it
#                                       would drift against the canonical one.
#   actionlint    (actionlint.yml)      Workflow syntax, expression types and
#                                       shellcheck over `run:` blocks. The tool's
#                                       VERSION and its SHA256 are pinned once in
#                                       the hub's reusable and are deliberately
#                                       not caller-tunable, so a local actionlint
#                                       is whatever nixpkgs currently ships — a
#                                       green here would be a claim about a
#                                       different linter, which is the same
#                                       "green here != green CI" defect the
#                                       golangci-lint skew line below reports
#                                       rather than hides.
#   taplo         (taplo.yml)           TOML formatting over glyph.toml and the
#                                       presets. Same argument as actionlint:
#                                       the tool version is pinned in the hub's
#                                       reusable, so a local taplo is whatever
#                                       nixpkgs ships — a green here would be a
#                                       claim about a different formatter.
#   version-preview (version-preview.yml) posts the merge-preview COMMENT on a
#                                       pull request. There is no pull request
#                                       locally; `glyph preview --pr N` is the
#                                       command, and it needs the API.
#   task-status   (task-status.yml)     acts on the furrow board, not on this
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

# MIRRORS is the reconciliation set: the run refuses its ✓ line unless every name
# here was recorded by `ran`. Keeping it as data rather than as a comment is what
# makes "a gate silently stopped running" a failure instead of a nicer-looking log.
MIRRORS='commit-lint dist-gate golden-gate module-hygiene build vet race-test coverage golangci-lint govulncheck bite mutations fuzz-smoke smoke'
NOT_MIRRORED='zizmor, actionlint, taplo, version-preview, task-status, goreleaser, codeql'
RAN=''
ran() { RAN="$RAN $1 "; }

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

# The distribution-layer gate build.yml runs on every pull request: a change to
# a reusable workflow, goreleaser.yml, the install action or .goreleaser.yaml
# must carry an internal/workflows test change (or a Dist-gate-exempt trailer on
# every commit). Same script, same base — origin/main is what a pull request
# from here would be judged against, and the origin/main check above has already
# run. Like `bite` below it reads COMMITTED history: an uncommitted test does
# not satisfy it, and that is the same claim-about-a-commit stance as the dirty
# check at the top.
echo "→ dist-gate (does a distribution-layer change carry evidence?)"
sh scripts/dist-gate.sh origin/main
ran dist-gate

# The golden gate build.yml runs on every pull request: a diff that rewrites a
# golden (testdata/*.golden.*) must carry a
# `Golden-change: <reason>` trailer on every non-merge commit — `-update`
# regenerates a golden from whatever the code now produces, so the trailer is
# what forces the diff to be read as the spec change it is. Same script, same
# base, same committed-history stance as dist-gate above.
echo "→ golden-gate (does a golden rewrite state its reason?)"
sh scripts/golden-gate.sh origin/main
ran golden-gate

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
COVER="$(mktemp)"
go test -race -covermode=atomic -coverprofile="$COVER" ./...
ran race-test
# The total line off the profile the race run just wrote — a REPORT, never a
# gate (t-37mg): coverage is read for risk (an unexecuted branch is a cheap
# candidate finder for the mutation ledger), and a threshold would turn the
# number into a target, which buys the tests that assert nothing. The ledger
# below stays the authority on whether tests bite — 93% coverage has been
# measured beside three surviving mutations.
go tool cover -func="$COVER" | tail -n 1
rm -f "$COVER"
ran coverage

# Mirrors build.yml's Linux-only "fuzz smoke (bounded)" step: discover every
# Fuzz target and run each briefly so a new target needs no edit here either.
# The bound is an execution COUNT, not a duration: a wall-clock -fuzztime makes
# the fuzz engine race its own deadline at shutdown and FAIL green runs with a
# spurious "context deadline exceeded" under machine load (Go's own builders
# flake on this), whereas a count bound never creates that deadline. Real hangs
# are still caught by the engine's per-input 10s limit.
echo "→ fuzz smoke (bounded)"
for pkg in $(go list ./...); do
  targets=$(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true)
  for f in $targets; do
    go test "$pkg" -run '^$' -fuzz "^${f}\$" -fuzztime 200000x
  done
done
ran fuzz-smoke

# Does the suite BITE? The step above proves the tests PASS, which is a weaker
# claim than it reads as: a line every test executes but nobody asserts is
# invisible to both `go test` and coverage. Each testdata/mutations/*.patch
# breaks one argued decision and the ledger names the test that must then fail.
# One snapshot, patch and build per row: 21s for 7 rows with a warm build cache,
# so it sits after the fast gates but ahead of the binary smoke.
echo "→ mutation ledger (does the suite bite?)"
sh scripts/mutations.sh
ran mutations

# Do the tests this branch ADDS bite? The ledger above asks it of a standing list
# of decisions; this asks it of the diff, which is the half CI gates on and the
# half no local run used to reproduce (t-7tj3). The script is the hub's, not a
# reimplementation — a second copy of a fleet gate would drift against the
# canonical one, which is exactly the mistake the NOT-MIRRORED list refuses.
#
# --git-common-dir resolves to the PRIMARY checkout even from a worktree, so the
# sibling lookup works in both; GLYPH_FLEET_HUB overrides it. BASE_SHA is the
# commit CI would merge into (go-bite takes the merge-base itself), so a stale
# origin/main gives a stale verdict — hence the fetch check at the top.
#
# This gate reads COMMITTED history and nothing else. A test sitting uncommitted
# in the working tree is invisible to it, and it stands down quietly rather than
# failing — so a mid-edit run tells you nothing about the test you just wrote.
# Commit first, then re-run. (ALLOW_DIRTY's final line makes the same point about
# the run as a whole.)
echo "→ bite (do this branch's new tests fail without it?)"
HUB="${GLYPH_FLEET_HUB:-}"
if [ -z "$HUB" ]; then
  HUB="$(dirname "$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")")/.github"
fi
BITE="$HUB/actions/go-bite/go-bite.sh"
if [ ! -f "$BITE" ]; then
  echo "  ✗ cannot mirror CI's \`bite\` job: no go-bite.sh at $BITE" >&2
  echo "    It lives in akira-toriyama/.github, which the house layout keeps beside this repo:" >&2
  echo "      git clone git@github.com:akira-toriyama/.github.git \"$HUB\"" >&2
  echo "    or point GLYPH_FLEET_HUB at an existing clone. Skipping it would put this" >&2
  echo "    script back to overstating its coverage, which is the defect being fixed." >&2
  exit 1
fi
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
BASE_SHA="$(git rev-parse origin/main)" HEAD_SHA="$(git rev-parse HEAD)" \
  GOROOT="$BITE_GOROOT" PATH="$BITE_GOROOT/bin:$PATH" sh "$BITE"
ran bite

# Both linters are CI gates, so a missing one is a FAILURE and not a skip. The old
# `command -v` guard printed "skipped" and let the run finish with its success
# line, which on this machine meant govulncheck never ran and nothing said so.
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
  want_lint="$(sed -n "s/.*golangci-lint-version:.*default:[[:space:]]*'v\{0,1\}\([0-9][0-9.]*\)'.*/\1/p" "$HUB/.github/workflows/go-ci.yml" 2>/dev/null | head -1)"
  if [ -z "$want_lint" ]; then
    want_lint="$(sed -n "s/.*default:[[:space:]]*'v\([0-9][0-9.]*\)'.*/\1/p" "$HUB/.github/workflows/go-ci.yml" 2>/dev/null | head -1)"
  fi
  got_lint="$(golangci-lint --version 2>/dev/null | sed -n 's/.*version[[:space:]]*v\{0,1\}\([0-9][0-9.]*\).*/\1/p' | head -1)"
  if [ -z "$want_lint" ]; then
    LINT_SKEW="golangci-lint: could not read the version go-ci.yml pins, so this run cannot say it matched CI"
  elif [ -z "$got_lint" ]; then
    LINT_SKEW="golangci-lint: could not read the LOCAL version, so this run cannot say it matched CI"
  elif [ "$want_lint" != "$got_lint" ]; then
    LINT_SKEW="golangci-lint v$got_lint here vs v$want_lint in CI — findings only the newer one has are invisible to this run"
  fi
  golangci-lint run ./...
  ran golangci-lint
  [ -n "$LINT_SKEW" ] && echo "  ! $LINT_SKEW"
else
  echo "✗ golangci-lint is not installed, and go-ci.yml runs it — so this run cannot" >&2
  echo "  claim a green CI. \`nix develop\` supplies it (flake.nix's devShell), or install it." >&2
  exit 1
fi

if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck"
  govulncheck ./...
  ran govulncheck
else
  echo "✗ govulncheck is not installed, and .github/workflows/govulncheck.yml runs it — so" >&2
  echo "  this run cannot claim a green CI. \`nix develop\` supplies it (flake.nix's devShell)," >&2
  echo "  or \`go install golang.org/x/vuln/cmd/govulncheck@latest\`." >&2
  exit 1
fi

echo "→ build binary for live checks"
go build -o bin/glyph ./cmd/glyph
BIN="$(pwd)/bin/glyph"

echo "→ smoke: version / init / help / usage errors"
"$BIN" version >/dev/null
"$BIN" version --json >/dev/null   # the machine flag is spelled --json on every command that has one
"$BIN" --version >/dev/null
"$BIN" --help >/dev/null
# init refuses to clobber this repository's own glyph.toml (usage, 2)
status=0
"$BIN" init --gemoji >/dev/null 2>&1 || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for init over an existing glyph.toml, got $status" >&2
  exit 1
fi
# two presets at once must exit 2 (usage), not crash
if "$BIN" init --gemoji --conventional >/dev/null 2>&1; then
  echo "  expected a usage error for init --gemoji --conventional" >&2
  exit 1
fi

# `completion` is cobra's command, and its output is redirected into a file the
# shell SOURCES — so an unknown shell reporting success is not a cosmetic exit
# code. `glyph completion zshh > _glyph` used to exit 0 and write the parent's
# English help where a completion script belongs. Both halves are asserted here
# rather than in a Go test because cobra binds the completion writer once, when
# the command tree is built, so an in-process test cannot observe which stream
# the script went to (see internal/cli/root_test.go).
echo "→ smoke: completion writes a script, or refuses"
"$BIN" completion zsh | head -1 | grep -q '^#compdef' || {
  echo "  expected 'completion zsh' to write a zsh script to stdout" >&2
  exit 1
}
status=0
out=$("$BIN" completion bogus 2>/dev/null) || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for an unknown completion shell, got $status" >&2
  exit 1
fi
if [ -n "$out" ]; then
  echo "  'completion bogus' wrote to stdout, which is what a caller redirects into a sourced file" >&2
  exit 1
fi

echo "→ smoke: lint / bump exit-code contract"
# The CODE is the assertion, not merely non-zero: `--stdin=false` used to exit 3,
# telling a CI gate that a commit violated the convention when none was
# submitted, and a plain `if "$BIN" …; then` cannot tell 3 from 2. That rule
# governs EVERY assertion in this block. Three of them used to state an integer
# in the failure message and assert only non-zero — a smoke that would have
# stayed green through the very renumbering it was written to catch.
"$BIN" lint --message ':bug:~ fix a crash'   # clean → 0
status=0
"$BIN" lint --message 'no gitmoji' >/dev/null 2>&1 || status=$?
if [ "$status" -ne 3 ]; then
  echo "  expected exit 3 (convention violation) for a malformed message, got $status" >&2
  exit 1
fi
status=0
"$BIN" lint --stdin=false </dev/null >/dev/null 2>&1 || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for lint --stdin=false, got $status" >&2
  exit 1
fi
status=0
"$BIN" bump --range HEAD~1..HEAD --current= >/dev/null 2>&1 || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for an empty --current, got $status" >&2
  exit 1
fi
# this checkout is a repo: an empty range is the soft no-release exit (1)
status=0
"$BIN" bump --range HEAD..HEAD >/dev/null 2>&1 || status=$?
if [ "$status" -ne 1 ]; then
  echo "  expected exit 1 (no release-worthy change) for an empty bump range, got $status" >&2
  exit 1
fi

echo "→ smoke: doctor's input guard (no network — the repo never resolves)"
"$BIN" doctor --help >/dev/null
# A malformed --repo is the caller's input and is rejected BEFORE any request
# goes out, so this smoke stays hermetic: a doctor run that reached the network
# here would hang or flake on an offline machine.
status=0
"$BIN" doctor --repo notaslash >/dev/null 2>&1 || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for a malformed doctor --repo, got $status" >&2
  exit 1
fi
ran smoke

# Reconciliation. Every gate that got this far exited 0 under `set -e`, so what is
# left to establish is that each one RAN — the failure mode that made the old
# header a lie was a gate quietly not running, which no exit code reports.
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
[ -n "${LINT_SKEW:-}" ] && SKEW=" — SKEWED FROM CI: $LINT_SKEW"
if [ -n "$DIRTY" ]; then
  echo "✓ passed (DIRTY TREE — not a claim about HEAD): $total/$total mirrored gates — NOT mirrored: $NOT_MIRRORED$SKEW"
else
  echo "✓ $total/$total mirrored gates passed — NOT mirrored: $NOT_MIRRORED$SKEW"
fi
