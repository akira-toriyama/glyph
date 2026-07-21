package doctor

// This file is the local half of the diagnosis: every akira-toriyama/glyph
// reference in the checkout's own workflows must pin a concrete vX.Y.Z release
// tag. It reads files and nothing else — no network, no git — so it still
// answers when the API side of the report is entirely dark.
//
// What it deliberately does NOT check is whether the pin is the LATEST release.
// That audit already runs daily as glyph-pin-audit.yml in akira-toriyama/.github
// across the whole fleet, and a second implementation of "which version is
// current" would be a second source of truth for the one question the fleet
// already has an answer to. This check asks only: is the pin CONCRETE.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/internal/bump"
)

// glyphRepo is the owner/name whose references must be pinned. Matched on the
// first two path segments, never as a string prefix: "akira-toriyama/glyph-test"
// starts with it and is a different repository entirely.
const glyphRepo = "akira-toriyama/glyph"

// pinRef is one akira-toriyama/glyph reference found in a workflow file, with
// the verdict on its ref. Problem is empty when the ref is a concrete release
// tag.
type pinRef struct {
	File    string
	Line    int
	Uses    string
	Ref     string
	Problem string
}

// checkWorkflowPins scans .github/workflows in the local checkout.
//
// A missing directory is UNKNOWN, not a vacuous pass. Both readings are live —
// a repository may genuinely have no workflows, or doctor may have been run
// from the wrong directory — and the difference matters enough that the check
// says so instead of quietly reporting that everything is fine.
func checkWorkflowPins(root string) Check {
	c := Check{
		ID:       IDWorkflowPinned,
		Expected: "every `uses: " + glyphRepo + "/…` in .github/workflows pins a concrete @vX.Y.Z release tag",
	}
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("%s could not be listed: %v", dir, err)
		c.Message = "doctor reads the LOCAL checkout for this check, so it must run from the repository root. " +
			"If this repository genuinely has no workflows there is nothing pinned and nothing to drift — but that is " +
			"unverified, not verified"
		c.Fix = "re-run from the repository root (cd into the checkout), or pass --repo and accept that this check cannot run there"
		return c
	}

	var refs []pinRef
	var unreadable []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		// Both extensions: GitHub honours .yaml as readily as .yml, and a
		// caller stub written with the other spelling would otherwise be
		// scanned by nobody at all.
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		files++
		path := filepath.Join(dir, name)
		body, rerr := os.ReadFile(path) // #nosec G304 -- the caller's own checkout, listed above
		if rerr != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s could not be read: %v", path, rerr))
			continue
		}
		refs = append(refs, scanUses(filepath.Join(".github", "workflows", name), string(body))...)
	}

	var bad []pinRef
	for _, r := range refs {
		if r.Problem != "" {
			bad = append(bad, r)
		}
	}

	for _, r := range bad {
		c.Details = append(c.Details, fmt.Sprintf("%s:%d uses %s — %s", r.File, r.Line, r.Uses, r.Problem))
	}
	c.Details = append(c.Details, unreadable...)

	switch {
	case len(bad) > 0:
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d of %d %s reference(s) in %d workflow file(s) in this checkout do not pin a release tag",
			len(bad), len(refs), glyphRepo, files)
		c.Message = "a glyph reusable workflow and the glyph binary it installs ship from ONE repository at ONE tag, and the " +
			"reusable derives the binary version from the tag the caller pinned (job.workflow_ref). A moving ref makes both " +
			"change under the caller between runs; a non-tag ref has no release binary to derive from at all, so the job " +
			"hard-errors unless glyph-version is passed"
		c.Fix = "pin each reference to a released tag: uses: " + glyphRepo + "/.github/workflows/<file>@vX.Y.Z"
	case len(unreadable) > 0:
		c.Status = StatusUnknown
		c.Observed = fmt.Sprintf("%d workflow file(s) could not be read; the %d %s reference(s) that were read all pin a release tag",
			len(unreadable), len(refs), glyphRepo)
		c.Message = "the files that were read are clean, but a file doctor cannot read could hold anything"
		c.Fix = "fix the file permissions and re-run"
	case len(refs) == 0:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("no %s reference in the %d workflow file(s) in this checkout", glyphRepo, files)
		c.Message = "nothing here consumes a glyph reusable workflow or action, so there is no pin to drift"
	default:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("%d %s reference(s) in the %d workflow file(s) in this checkout, all pinned to a release tag",
			len(refs), glyphRepo, files)
		c.Message = "every glyph reference names one immutable release, so the workflow and the binary it installs cannot diverge"
	}
	return c
}

// scanUses extracts every akira-toriyama/glyph `uses:` reference from one
// workflow file, with its verdict.
//
// THE TRAP, which has been got wrong in every direction: a `uses:`-shaped line
// is not necessarily an executing step.
//
//   - Every glyph reusable workflow ships a permanently-stale COMMENTED caller
//     stub in its header — a line whose first non-space character is # that
//     contains `uses:` and a version frozen at whenever the prose was last
//     edited. glyph's own lint.yml carries
//     `#       uses: akira-toriyama/glyph/.github/workflows/lint.yml@v0.10.0`
//     while the repository's real caller (commit-lint.yml) is pinned to a later
//     tag. A scan that ignores comments reports glyph itself as drifted forever;
//     a scan that takes the FIRST match in a file (a `head -1` shape) reads the
//     comment instead of the real line and reports the wrong version everywhere.
//   - One shape further out, and the reason this loop carries state: a fleet-sync
//     step WRITES caller stubs, so a `run: |` block holds a heredoc whose text is
//     `uses: akira-toriyama/glyph/.github/workflows/lint.yml@main`. That is data
//     the step emits into another repository, not a pin this repository executes
//     — reading it as one failed doctor (exit 3) on a repository whose every
//     executing `uses:` was correctly pinned.
//
// So: block scalars are skipped whole, whole-line comments are dropped, and
// `uses:` is only recognised as the YAML key at the start of the (optionally
// list-dashed) line — never as text found anywhere in it, which also keeps a
// trailing `# uses: …` inline comment from being read as a second reference.
func scanUses(file, body string) []pinRef {
	var refs []pinRef
	// block is the indent column of the key that opened a block scalar, or -1
	// outside one. Everything indented deeper than that key is the scalar's
	// content — text, whatever it looks like — until a line comes back to that
	// column or further left.
	block := -1
	for i, line := range strings.Split(body, "\n") {
		if block >= 0 {
			if strings.TrimSpace(line) == "" || indentOf(line) > block {
				continue
			}
			block = -1
		}
		if indent, opens := blockScalarKey(line); opens {
			block = indent
			continue
		}
		value, ok := usesValue(line)
		if !ok {
			continue
		}
		spec, ref, hasRef := strings.Cut(value, "@")
		if !isGlyphRef(spec) {
			continue
		}
		refs = append(refs, pinRef{File: file, Line: i + 1, Uses: value, Ref: ref, Problem: pinProblem(ref, hasRef)})
	}
	return refs
}

// indentOf counts the leading whitespace of a line. YAML forbids tabs in
// indentation, so a character count is the same measure the parser uses.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// blockScalarKey reports whether a line opens a block scalar (`run: |`,
// `script: >`, and the `|-` / `|+` / `>-` / `|2` chomping and indentation
// variants), and at which column its key sits — the column everything deeper
// belongs to the scalar rather than to the workflow.
//
// Only the header is recognised here, never the content: the content is skipped
// by indentation in scanUses, which is how YAML itself delimits it. A trailing
// comment after the indicator (`run: | # write the stub`) is legal and does not
// change the answer, so only the first field is inspected.
func blockScalarKey(line string) (int, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return 0, false
	}
	indent := indentOf(line)
	// A block scalar can open on a step's first key (`- run: |`); the mapping —
	// and therefore the column its content must beat — starts after the dash.
	if after, dashed := strings.CutPrefix(trimmed, "-"); dashed {
		rest := strings.TrimLeft(after, " \t")
		indent += len(trimmed) - len(rest)
		trimmed = rest
	}
	key, value, found := strings.Cut(trimmed, ":")
	if !found || key == "" || strings.ContainsAny(key, " \t") {
		return 0, false
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, false
	}
	head := fields[0]
	if head[0] != '|' && head[0] != '>' {
		return 0, false
	}
	// Everything after the indicator must be a chomping or explicit-indentation
	// modifier; anything else means this is a plain scalar that merely starts
	// with the character (`if: >0.5` is not a block).
	return indent, strings.Trim(head[1:], "+-0123456789") == ""
}

// usesValue returns the reference a workflow line's `uses:` key names, or false
// when the line is a comment, is not a uses: key, or names nothing.
func usesValue(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "#") {
		return "", false // the commented caller stub — documentation, not a pin
	}
	// The key anchoring below would reject a comment line on its own; the
	// explicit check above stays because it is the trap being stated rather
	// than merely survived, and because a future relaxation of the anchoring
	// must not quietly reopen it.
	// A step may be the first item of a list (`- uses: …`) or a continuation
	// key (`  uses: …`); nothing else puts uses: at the head of a line.
	if after, found := strings.CutPrefix(trimmed, "-"); found {
		trimmed = strings.TrimLeft(after, " \t")
	}
	rest, found := strings.CutPrefix(trimmed, "uses:")
	if !found {
		return "", false
	}
	// The value ends at whitespace — everything after it is a trailing inline
	// comment (`@v0.10.0  # pin a release tag`), which is prose.
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	return strings.Trim(fields[0], `"'`), true
}

// isGlyphRef reports whether a uses: spec (the part before @) names the glyph
// repository — its first two path segments, so a sibling repo whose name merely
// starts with "glyph" is not swept in, and a local `./…` action is not either.
//
// The comparison is case-INSENSITIVE because GitHub's is: it resolves
// `Akira-Toriyama/glyph/.github/workflows/lint.yml@main` to this repository and
// runs it. A case-sensitive scan reported "no akira-toriyama/glyph reference"
// and exited 0 on a repository running a moving ref — the check's own blind
// spot, and the one failure mode it cannot afford.
func isGlyphRef(spec string) bool {
	parts := strings.SplitN(spec, "/", 3)
	return len(parts) >= 2 && strings.EqualFold(parts[0]+"/"+parts[1], glyphRepo)
}

// pinProblem judges one ref, returning "" when it is a concrete release tag.
// The two failing shapes are worth distinguishing in the report because the fix
// differs: a moving ref must be replaced, while a SHA pin is already immutable
// and only breaks glyph's version derivation.
func pinProblem(ref string, hasRef bool) string {
	if !hasRef || ref == "" {
		return "it names no ref at all, so GitHub resolves the repository's default branch — the workflow changes under you"
	}
	if isReleaseTag(ref) {
		return ""
	}
	if isCommitSHA(ref) {
		return "@" + ref + " is a commit SHA: immutable, but a glyph reusable derives the binary version from the TAG it was " +
			"pinned to (job.workflow_ref) and hard-errors on a non-tag ref unless glyph-version is passed"
	}
	return "@" + ref + " is a moving ref (a branch, or a major/minor alias): it can change under the caller between two runs, " +
		"and glyph's tag space holds only full vX.Y.Z releases — no v1 or v0.10 alias exists to resolve"
}

// isReleaseTag reports whether ref is a house release tag. bump.ParseVersion is
// the one authority on the version shape (it accepts a bare X.Y.Z too), and the
// leading v is required on top of it: GoReleaser cuts glyph's tags as vX.Y.Z,
// so a v-less tag simply does not exist to pin.
func isReleaseTag(ref string) bool {
	if !strings.HasPrefix(ref, "v") {
		return false
	}
	_, err := bump.ParseVersion(ref)
	return err == nil
}

// isCommitSHA reports whether ref looks like a git object name: hex, and long
// enough that GitHub would resolve it as one (7 characters is git's own
// conventional floor).
func isCommitSHA(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, r := range ref {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
