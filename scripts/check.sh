#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what .github/workflows/build.yml enforces in CI, so a green run
# here means a green CI.
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

# Module hygiene: fail if go.mod/go.sum are not tidy, and verify the downloaded
# dependencies match go.sum. `-diff` prints the needed changes and exits non-zero
# without touching the files (Go 1.23+), so this is a pure gate under `set -e`.
echo "→ go mod tidy -diff && go mod verify"
go mod tidy -diff
go mod verify

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race"
go test -race ./...

# Mirrors build.yml's Linux-only "fuzz smoke (bounded)" step: discover every
# Fuzz target and run each briefly so a new target needs no edit here either.
echo "→ fuzz smoke (bounded)"
for pkg in $(go list ./...); do
  targets=$(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true)
  for f in $targets; do
    go test "$pkg" -run '^$' -fuzz "^${f}\$" -fuzztime 15s
  done
done

if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi

if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck"
  govulncheck ./...
else
  echo "→ govulncheck (skipped — not installed; CI runs it)"
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

echo "→ smoke: lint / bump exit-code contract"
"$BIN" lint --message ':bug: fix a crash'   # clean → 0
if "$BIN" lint --message 'no gitmoji' >/dev/null 2>&1; then
  echo "  expected exit 3 for a malformed message" >&2
  exit 1
fi
# this checkout is a repo: an empty range is the soft no-release exit (1)
if "$BIN" bump --range HEAD..HEAD >/dev/null 2>&1; then
  echo "  expected exit 1 for an empty bump range" >&2
  exit 1
fi
echo "✓ all checks passed"
