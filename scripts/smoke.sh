#!/bin/sh
# smoke.sh <binary> — the built binary's CLI contract, asserted on the real
# process. The one implementation: build.yml's extras job runs it on every
# released platform shape (linux amd64 and arm64, darwin) and scripts/check.sh
# runs it locally, so an assertion added here runs in both.
#
# Runs from the repository root: the init assertion needs this repository's own
# glyph.toml, and the bump assertions need its history (HEAD~1 must exist).
set -eu
BIN="${1:?usage: sh scripts/smoke.sh <binary>}"
case "$BIN" in
  /*) ;;
  *) BIN="$(pwd)/$BIN" ;;
esac
cd "$(dirname "$0")/.."

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
status=0
"$BIN" init --gemoji --conventional >/dev/null 2>&1 || status=$?
if [ "$status" -ne 2 ]; then
  echo "  expected exit 2 (usage) for init --gemoji --conventional, got $status" >&2
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
