#!/bin/sh
# build.sh — build glyph into bin/glyph with the version/commit/date stamped from
# git. Used by install.sh, and by anyone building from a checkout. NOT part of
# the cask path: .goreleaser.yaml ships `homebrew_casks`, which installs the
# prebuilt release binary and has no from-source stanza to fall back to.
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
# The commit's committer date in UTC, the form and the meaning of GoReleaser's
# {{.CommitDate}} and of the toolchain's vcs.time — never the time of this
# run, so a rebuild of one commit prints one date. Empty outside a checkout:
# `glyph version` then prints no date.
DATE="$(TZ=UTC0 git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)"
PKG="github.com/akira-toriyama/glyph/v6/internal/version"

mkdir -p bin
go build -trimpath \
  -ldflags "-s -w -X '${PKG}.Version=${VERSION}' -X '${PKG}.Commit=${COMMIT}' -X '${PKG}.Date=${DATE}'" \
  -o bin/glyph ./cmd/glyph

echo "built: $DIR/bin/glyph  (${VERSION})"
