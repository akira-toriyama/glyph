#!/bin/sh
# fuzz-smoke.sh — every Fuzz target, run briefly. The one implementation:
# build.yml's extras job runs it on its Linux legs and scripts/check.sh runs it
# locally. Targets are discovered per package, so a new one needs no edit here
# or in either caller.
#
# The bound is an execution COUNT, not a duration: a wall-clock -fuzztime makes
# the fuzz engine race its own deadline at shutdown and FAIL green runs with a
# spurious "context deadline exceeded" under machine load (Go's own builders
# flake on this), whereas a count bound never creates that deadline. Real hangs
# are still caught by the engine's per-input 10s limit.
set -eu
cd "$(dirname "$0")/.."

for pkg in $(go list ./...); do
  targets=$(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true)
  for f in $targets; do
    go test "$pkg" -run '^$' -fuzz "^${f}\$" -fuzztime 200000x
  done
done
