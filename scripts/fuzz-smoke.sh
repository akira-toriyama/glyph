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
#
# Discovery failing is this script failing, or a go.mod go cannot parse and a
# test file that does not build read as "no targets" and exit 0 having fuzzed
# nothing. `set -e` stops on an assignment's failure and sees nothing inside
# `for x in $(cmd)` or on the left of a pipe, so each listing is assigned before
# it is read. Zero targets fails for the same reason: this repo commits targets,
# so finding none means discovery broke. The count is printed, never expected:
# a new target must need no edit here. TestFuzzSmokeFailsWhenDiscoveryFails holds
# all three.
set -eu
cd "$(dirname "$0")/.."

pkgs=$(go list ./...)
found=0
for pkg in $pkgs; do
  listing=$(go test -list '^Fuzz' "$pkg")
  targets=$(printf '%s\n' "$listing" | grep '^Fuzz' || true)
  for f in $targets; do
    go test "$pkg" -run '^$' -fuzz "^${f}\$" -fuzztime 200000x
    found=$((found + 1))
  done
done

if [ "$found" -eq 0 ]; then
  echo "✗ fuzz-smoke: no Fuzz target in any package, so nothing was fuzzed and a green here would claim otherwise" >&2
  exit 1
fi
echo "✓ fuzz-smoke: $found Fuzz target(s) ran"
