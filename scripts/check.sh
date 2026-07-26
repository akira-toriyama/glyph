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
#   module-hygiene  go-ci.yml (via build.yml's `ci`) `go mod tidy -diff`, verify
#   build           go-ci.yml                    `go build ./...`
#   vet             go-ci.yml                    `go vet ./...`
#   race-test       go-ci.yml                    `go test -race ./...`
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
#   taplo         (taplo.yml)           TOML formatting. glyph tracks 0 .toml
#                                       files (measured), so it is a structural
#                                       no-op here, not a gap.
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
MIRRORS='commit-lint module-hygiene build vet race-test golangci-lint govulncheck bite mutations fuzz-smoke smoke'
NOT_MIRRORED='zizmor, taplo, version-preview, task-status, goreleaser, codeql'
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
echo "→ go test -race"
go test -race ./...
ran race-test

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
BASE_SHA="$(git rev-parse origin/main)" HEAD_SHA="$(git rev-parse HEAD)" sh "$BITE"
ran bite

# Both linters are CI gates, so a missing one is a FAILURE and not a skip. The old
# `command -v` guard printed "skipped" and let the run finish with its success
# line, which on this machine meant govulncheck never ran and nothing said so.
if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
  ran golangci-lint
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

echo "→ smoke: version / rules / help / usage errors"
"$BIN" version >/dev/null
"$BIN" version --ndjson >/dev/null # subcommand must own --ndjson (else exit 2)
"$BIN" --version >/dev/null
"$BIN" --help >/dev/null
"$BIN" rules --json >/dev/null     # embedded table self-prints as JSON
"$BIN" rules --md >/dev/null       # ...and as the Markdown docs table
# mutually-exclusive formats must exit 2 (usage), not crash
if "$BIN" rules --json --md >/dev/null 2>&1; then
  echo "  expected a usage error for rules --json --md" >&2
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
"$BIN" lint --message ':bug: fix a crash'   # clean → 0
if "$BIN" lint --message 'no gitmoji' >/dev/null 2>&1; then
  echo "  expected exit 3 for a malformed message" >&2
  exit 1
fi
# The CODE is the assertion, not merely non-zero: `--stdin=false` used to exit 3,
# telling a CI gate that a commit violated the convention when none was
# submitted, and a plain `if "$BIN" …; then` cannot tell 3 from 2.
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
if "$BIN" bump --range HEAD..HEAD >/dev/null 2>&1; then
  echo "  expected exit 1 for an empty bump range" >&2
  exit 1
fi

echo "→ smoke: doctor's input guard (no network — the repo never resolves)"
"$BIN" doctor --help >/dev/null
# A malformed --repo is the caller's input and is rejected BEFORE any request
# goes out, so this smoke stays hermetic: a doctor run that reached the network
# here would hang or flake on an offline machine.
if "$BIN" doctor --repo notaslash >/dev/null 2>&1; then
  echo "  expected exit 2 for a malformed doctor --repo" >&2
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

if [ -n "$DIRTY" ]; then
  echo "✓ passed (DIRTY TREE — not a claim about HEAD): $total/$total mirrored gates — NOT mirrored: $NOT_MIRRORED"
else
  echo "✓ $total/$total mirrored gates passed — NOT mirrored: $NOT_MIRRORED"
fi
