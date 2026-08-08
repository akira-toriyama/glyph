package workflows

// Shipping invariants (t-ewre): four :bug: fixes to the distribution layer
// each shipped as a one-file YAML/shell diff with zero tests, and each can be
// reverted by a one-line change that no compiler, no Go test and no smoke can
// see. The dist-gate makes evidence mandatory for FUTURE distribution diffs;
// these tests are the evidence those four never carried, written after the
// fact. Each has a mutation-ledger row re-breaking it, which is where "a fix
// is shown by re-breaking it" is enforced for lines no Go code compiles.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReleasePublishesAtTagTime pins `draft: false` in .goreleaser.yaml
// (1015774). The same run's homebrew_casks step has ALREADY pushed the new
// version to the tap, so every draft is a window where `brew install` fetches
// a 404 asset, bounded only by how soon a human remembers to publish — cifail
// v0.1.1 sat unpublished for 17 days. Reverting is a one-word change that CI
// stays green over; the failure surfaces after the next tag, in a consumer.
func TestReleasePublishesAtTagTime(t *testing.T) {
	config := code(repoFile(t, ".goreleaser.yaml"))
	if !regexp.MustCompile(`(?m)^\s*draft:\s*false\s*$`).MatchString(config) {
		t.Error(".goreleaser.yaml no longer sets `draft: false`; a draft's assets are 404 to " +
			"anonymous clients while the tap already names the new version — every draft is a " +
			"broken-brew window bounded only by a human remembering to publish (1015774)")
	}
	if regexp.MustCompile(`(?m)^\s*draft:\s*["']?true`).MatchString(config) {
		t.Error(".goreleaser.yaml sets a truthy `draft:`; publish at tag time — see the release block's rationale")
	}
}

// TestInstallActionRetriesSpanARealOutage pins the retry widths in the install
// composite action (bcd93e3): `--retry 3` (~7s) measurably lost to a real
// GitHub outage — a verdict job died on 504x4 downloading the binary (t-yj1b)
// — so the curls carry `--retry 5 --retry-connrefused` and the attestation
// gate retries 5 times on a growing sleep. This action is the entry point of
// every fleet repo's lint/pr-verdict/release, so a shrunk width is invisible
// until an outage turns the whole fleet red at once.
func TestInstallActionRetriesSpanARealOutage(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "actions", "install", "action.yml")))

	// Both downloads — the tarball and its checksums — carry the widened curl.
	if got := strings.Count(body, "--retry 5 --retry-connrefused"); got < 2 {
		t.Errorf("install action carries %d curl(s) with `--retry 5 --retry-connrefused`, want 2 "+
			"(binary + checksums); --retry 3's ~7s window measurably lost to a real outage (t-yj1b)", got)
	}

	// The attestation gate keeps its five bounded attempts and still hard-fails.
	for _, want := range []string{
		"for i in 1 2 3 4 5",
		`sleep "$((i * 3))"`,
		`[ "$i" -eq 5 ]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install action's attestation retry lost %q; the Sigstore/attestations API rides "+
				"the same infrastructure that just proved able to stay down past a short retry", want)
		}
	}
}

// TestNotesSoftExitShipsAPlaceholder pins the goreleaser.yml arm that absorbs
// `glyph notes`' soft no-release exit 1 (7d8d733): under `set -e` a bare
// invocation kills the whole job when the range below the tag holds nothing
// release-worthy — no binaries, no cask, no attestation, behind a tag that
// already exists. The contract is honoured instead: 1 ships a placeholder
// body, anything else is a real failure and keeps its code.
func TestNotesSoftExitShipsAPlaceholder(t *testing.T) {
	body := code(repoFile(t, filepath.Join(".github", "workflows", "goreleaser.yml")))

	for _, want := range []string{
		"|| status=$?",                 // capture without dying under set -e
		`if [ "$status" -eq 1 ]`,       // the soft no-release arm...
		"No release-worthy changes in", // ...ships a body that says so
		`elif [ "$status" -ne 0 ]`,     // real failures keep failing
		`exit "$status"`,               // with their own code, not a laundered 1
	} {
		if !strings.Contains(body, want) {
			t.Errorf("goreleaser.yml's notes step lost %q; `glyph notes` exits 1 on an infra-only "+
				"range by contract, and without this arm `set -e` turns that verdict into a job "+
				"abort behind a tag that already exists (7d8d733)", want)
		}
	}
}

// ldflagsX matches one `-X path.Var=` injection as .goreleaser.yaml and
// build.sh write it (build.sh single-quotes the argument).
var ldflagsX = regexp.MustCompile(`-X '?([A-Za-z0-9_./~-]+)\.([A-Za-z_][A-Za-z0-9_]*)=`)

// TestLdflagsNameTheRealVersionSymbols pins the injection target the release
// and source builds write twice, as literals, in files no test compiles.
// Go's -X silently ignores an unknown symbol: move internal/version or rename
// a var and both builds go green while every released binary reports "dev" —
// measured with a doctored -X (exit 0, zero warnings, `glyph dev`). The smoke
// in build.yml runs a plain `go build`, so it cannot see it either.
func TestLdflagsNameTheRealVersionSymbols(t *testing.T) {
	const wantPkg = "github.com/akira-toriyama/glyph/internal/version"

	// The package the path names must exist where the path says — read through
	// the module layout itself, so a moved package fails here.
	versionGo, err := os.ReadFile(filepath.Join("..", "..", "internal", "version", "version.go"))
	if err != nil {
		t.Fatalf("internal/version/version.go is unreadable (%v); if the package moved, every "+
			"-X injection in .goreleaser.yaml and build.sh is now a silent no-op", err)
	}

	declared := func(file, sym string) {
		if !regexp.MustCompile(`(?m)^\t` + sym + ` `).Match(versionGo) {
			t.Errorf("%s injects %q, which internal/version/version.go no longer declares; "+
				"-X ignores an unknown symbol silently", file, sym)
		}
	}

	// .goreleaser.yaml spells the full path in every injection.
	goreleaser := ldflagsX.FindAllStringSubmatch(repoFile(t, ".goreleaser.yaml"), -1)
	// Non-vacuity: the file injects Version, Commit and Date today. A matcher
	// gone stale would report zero and the loop would prove nothing.
	if len(goreleaser) < 3 {
		t.Fatalf(".goreleaser.yaml carries %d -X injection(s), want at least 3 — either the "+
			"injections were dropped (released binaries report \"dev\") or ldflagsX no longer "+
			"matches how they are written", len(goreleaser))
	}
	for _, m := range goreleaser {
		if m[1] != wantPkg {
			t.Errorf(".goreleaser.yaml injects into %q, want %q; -X ignores an unknown symbol "+
				"silently, so a stale path ships binaries that report \"dev\"", m[1], wantPkg)
		}
		declared(".goreleaser.yaml", m[2])
	}

	// build.sh routes the same path through one PKG= variable, so the literal
	// lives once and the injections name ${PKG}.
	sh := repoFile(t, "build.sh")
	if !strings.Contains(sh, `PKG="`+wantPkg+`"`) {
		t.Errorf("build.sh's PKG no longer names %q; its -X injections are silent no-ops and "+
			"every source build reports \"dev\"", wantPkg)
	}
	shInjections := regexp.MustCompile(`-X '\$\{PKG\}\.([A-Za-z_][A-Za-z0-9_]*)=`).FindAllStringSubmatch(sh, -1)
	if len(shInjections) < 3 {
		t.Fatalf("build.sh carries %d ${PKG} -X injection(s), want at least 3 — either the "+
			"injections were dropped or the matcher no longer fits how they are written", len(shInjections))
	}
	for _, m := range shInjections {
		declared("build.sh", m[1])
	}
}
