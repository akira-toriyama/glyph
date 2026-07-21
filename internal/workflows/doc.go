// Package workflows holds tests that guard glyph's CI workflow files against
// regressions the YAML itself cannot express — chiefly that the security-critical
// install logic (download + checksum + `gh attestation verify`) stays in the one
// composite action .github/actions/install and never drifts back inline into the
// reusable workflows, and that those reusables install the binary version the
// caller pinned rather than a hand-bumped default that fell out of lockstep once,
// and that the reusables' commented caller stubs carry the @vX.Y.Z PLACEHOLDER
// instead of a concrete tag — a tag is cut on a tree that is already frozen, so a
// version written into a comment is stale on arrival, and its presence is what
// makes the fleet's grep for glyph pins wrong in both directions.
//
// It has no runtime code; the invariants live in workflows_test.go. The package
// exists only so the directory holds a non-test Go file.
package workflows
