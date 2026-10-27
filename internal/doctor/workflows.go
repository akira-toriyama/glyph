package doctor

// This file is the workflow-pin check: every akira-toriyama/glyph reference in
// the checkout's own workflows and action metadata files must pin a concrete
// vX.Y.Z release tag. It reads files and the action-file list git produced
// (handed in by internal/cli, which runs every subprocess doctor needs) — no network — so
// it still answers when the API side of the report is entirely dark.
//
// It asks only whether the pin is CONCRETE, never whether it is the LATEST
// release: glyph-pin-audit.yml in akira-toriyama/.github owns that question
// (DESIGN §7).
//
// It also holds what the two caller checks share with it: the one read of
// .github/workflows (readWorkflows, done once by Run) and the one YAML line
// discipline every scanner walks (contentLines).

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
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

// workflowFile is one .github/workflows file as readWorkflows read it. Err is
// that file's read failure: the checks render it (unreadableLine) and never
// drop the file, because a caller check that skipped one turned chmod 000 on a
// failing caller into "nothing to judge" (t-fdd8 (3)).
type workflowFile struct {
	Name string // the directory entry, e.g. release.yml
	Body string
	Err  error
}

// unreadableLine is the one Details line every workflow check gives a file it
// could not read. The read error already names the path.
func (f workflowFile) unreadableLine() string {
	return fmt.Sprintf("a workflow file could not be read: %v", f.Err)
}

// readWorkflows lists root/.github/workflows and reads every workflow file in
// it, once for the three checks that scan them. listErr is non-nil exactly
// when the listing observed nothing: any failure but an absent directory, and
// an absence under an unverified bare "." too. An absent directory under a
// git-named root is an observed absence, (nil, nil) — a repository with no
// workflows has nothing pinned and nothing to call. Under a bare "." both
// readings stay live — no workflows, or doctor run from the wrong directory —
// and that stays UNKNOWN, not a vacuous pass (a fresh repository could never
// get doctor to exit 0 before wiring CI, which is how this arm was found: the
// t-wzsw zero-base drill, 2026-08-26).
func readWorkflows(root string, rootVerified bool) (files []workflowFile, listErr error) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil && (!rootVerified || !errors.Is(err, fs.ErrNotExist)) {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		// Both extensions: GitHub honours .yaml as readily as .yml, and a
		// caller stub written with the other spelling would otherwise be
		// scanned by nobody at all.
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- the caller's own checkout, listed above
		files = append(files, workflowFile{Name: name, Body: string(body), Err: rerr})
	}
	return files, nil
}

// unlistableWorkflows fills c with the UNKNOWN verdict for a workflows
// directory readWorkflows could not list (listErr). Each check gives its own
// account of what an unverified read costs, once per arm: outside follows
// "…git could not name one here. " (no git-named root), and unread follows
// "the checkout's workflows were never read" with its own leading
// punctuation (under one), since a check may continue that clause rather
// than start a sentence.
func unlistableWorkflows(c *Check, root string, rootVerified bool, listErr error, outside, unread string) {
	c.Status = StatusUnknown
	c.Observed = fmt.Sprintf("the workflows directory could not be listed: %v", listErr)
	if !rootVerified {
		c.Message = "doctor reads the LOCAL checkout for this check, and git could not name one here. " + outside
		c.Fix = fixOutsideCheckout
		return
	}
	c.Message = "the checkout's workflows were never read" + unread
	c.Fix = unlistableFix(filepath.Join(root, ".github", "workflows"), listErr)
}

// unlistableFix is the remedy for a workflows directory under a git-named root
// that the filesystem would not list. Only a permission error is a permissions
// problem; anything else (a regular file at the path, a symbolic-link loop)
// needs the path made a listable directory — the split checkConfig draws for
// glyph.toml.
func unlistableFix(dir string, err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "fix the directory's permissions and re-run"
	}
	return "make " + dir + " a directory that can be listed, then re-run"
}

// checkWorkflowPins scans the workflow files (readWorkflows, workflowsErr its
// listing's failure) and every action.yml / action.yaml git lists for the
// local checkout (gitsource.ActionFiles, paths relative to root; actionErr its
// failure). An absent workflows directory under a git-named root leaves the
// action files still read, so a composite is not blessed by the workflows
// directory being gone.
func checkWorkflowPins(root string, rootVerified bool, workflows []workflowFile, workflowsErr error, actionFiles []gitsource.ActionFile, actionErr error) Check {
	c := Check{
		ID: idWorkflowPinned,
		Expected: "every `uses: " + glyphRepo + "/…` in .github/workflows or in any action.yml / action.yaml in the checkout " +
			"(submodules included) pins a concrete @vX.Y.Z release tag",
	}
	if workflowsErr != nil {
		unlistableWorkflows(&c, root, rootVerified, workflowsErr,
			"If this repository genuinely has no workflows there is nothing pinned and nothing to drift — but that is "+
				"unverified, not verified",
			", so whatever they pin is unverified, not verified")
		return c
	}

	var refs []pinRef
	var unreadable []string
	files := 0
	for _, f := range workflows {
		files++
		if f.Err != nil {
			unreadable = append(unreadable, f.unreadableLine())
			continue
		}
		refs = append(refs, scanUses(filepath.Join(".github", "workflows", f.Name), f.Body)...)
	}

	// The workflows directory is not the only place a checkout EXECUTES a glyph
	// reference from: GitHub runs a composite from wherever it sits, so every
	// action metadata file git lists is read, at any path (DESIGN §7 has the
	// misses that drew this boundary). Only the two action.yml spellings:
	// GitHub takes nothing else as action metadata.
	var listErr string
	if actionErr != nil {
		listErr = fmt.Sprintf("the checkout's action files could not be listed: %v", actionErr)
	}
	for _, f := range actionFiles {
		// A file directly in the workflows directory was read above.
		if rest, ok := strings.CutPrefix(f.Path, ".github/workflows/"); ok && !strings.Contains(rest, "/") {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(f.Path))
		if f.SkipWorktree {
			unreadable = append(unreadable, fmt.Sprintf("%s is outside the sparse checkout, so its bytes are not on disk to read", abs))
			continue
		}
		body, rerr := os.ReadFile(abs) // #nosec G304 -- a path git listed for the caller's own checkout
		switch {
		case errors.Is(rerr, fs.ErrNotExist):
			// Deleted in the working tree: an observed absence, exactly as a
			// missing workflows directory is under a git-named root. Only a
			// read the filesystem refused is unknown (DESIGN §7).
			continue
		case rerr != nil:
			unreadable = append(unreadable, fmt.Sprintf("an action file could not be read: %v", rerr))
			continue
		}
		files++
		refs = append(refs, scanUses(f.Path, string(body))...)
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
	if listErr != "" {
		c.Details = append(c.Details, listErr)
	}

	switch {
	case len(bad) > 0:
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d of %d %s reference(s) in %d workflow/action file(s) in this checkout do not pin a release tag",
			len(bad), len(refs), glyphRepo, files)
		c.Message = "a glyph reusable workflow and the glyph binary it installs ship from ONE repository at ONE tag, and the " +
			"reusable derives the binary version from the tag the caller pinned (job.workflow_ref). A moving ref makes both " +
			"change under the caller between runs; a non-tag ref has no release binary to derive from at all, so the job " +
			"hard-errors unless glyph-version is passed"
		c.Fix = "pin each reference to a released tag: uses: " + glyphRepo + "/.github/workflows/<file>@vX.Y.Z"
	case len(unreadable) > 0 || listErr != "":
		c.Status = StatusUnknown
		var gaps []string
		if listErr != "" {
			gaps = append(gaps, "the checkout's action files could not be listed")
		}
		if len(unreadable) > 0 {
			gaps = append(gaps, fmt.Sprintf("%d workflow/action file(s) could not be read", len(unreadable)))
		}
		c.Observed = fmt.Sprintf("%s; the %d %s reference(s) that were read all pin a release tag",
			strings.Join(gaps, ", and "), len(refs), glyphRepo)
		c.Message = "the files that were read are clean, but a file doctor cannot read — or one git never listed — could hold anything"
		c.Fix = "re-run from inside the git checkout, with read access to the named files (a sparse checkout must include them)"
	case len(refs) == 0:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("no %s reference in the %d workflow/action file(s) in this checkout", glyphRepo, files)
		c.Message = "nothing here consumes a glyph reusable workflow or action, so there is no pin to drift"
	default:
		c.Status = StatusPass
		c.Observed = fmt.Sprintf("%d %s reference(s) in the %d workflow/action file(s) in this checkout, all pinned to a release tag",
			len(refs), glyphRepo, files)
		c.Message = "every glyph reference names one immutable release, so the workflow and the binary it installs cannot diverge"
	}
	return c
}

// scanUses extracts every akira-toriyama/glyph `uses:` reference from one
// workflow file, with its verdict.
//
// A `uses:`-shaped line is not necessarily an executing step (DESIGN §7 has
// the trap): every glyph reusable ships a COMMENTED caller stub in its header,
// and a fleet-sync step WRITES caller stubs from a `run: |` heredoc — data it
// emits into another repository. So only contentLines are read (block scalars
// skipped whole, whole-line comments dropped), and `uses:` is only recognised
// as the YAML key at the start of the (optionally list-dashed) line — never as
// text found anywhere in it, which also keeps a trailing `# uses: …` inline
// comment from being read as a second reference.
func scanUses(file, body string) []pinRef {
	var refs []pinRef
	for i, line := range contentLines(strings.Split(body, "\n"), 0) {
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

// contentLines yields (index, line) for every line of lines from from on that
// is YAML structure: not blank, not a whole-line comment, not block-scalar
// content. It is the one line discipline of every workflow scanner here
// (scanUses, callerGrants, jobWithKeys), so they cannot read one file two ways.
//
// The order is the point. Inside a block scalar a blank line or one indented
// deeper than its key is content and is skipped; the first line at the key's
// column or further left closes the scalar — a comment line included, since
// YAML reads a comment that far left as the scalar's end, never as its text —
// and is then judged like any other line, so it may itself be dropped as a
// comment or open the next scalar. A key line that opens a scalar is yielded:
// it is structure, and only what follows it is text. Blank means whitespace
// only, inside a scalar and out of one alike, so a CRLF file's lone `\r` is
// blank wherever it sits rather than a line at column 0 that ends a mapping.
func contentLines(lines []string, from int) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		// block is the indent column of the key that opened a block scalar, or
		// -1 outside one.
		block := -1
		for i := from; i < len(lines); i++ {
			line := lines[i]
			blank := strings.TrimSpace(line) == ""
			if block >= 0 {
				if blank || indentOf(line) > block {
					continue
				}
				block = -1
			}
			if blank || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
				continue
			}
			if indent, opens := blockScalarKey(line); opens {
				block = indent
			}
			if !yield(i, line) {
				return
			}
		}
	}
}

// blockScalarKey reports whether a line opens a block scalar (`run: |`,
// `script: >`, and the `|-` / `|+` / `>-` / `|2` chomping and indentation
// variants), and at which column its key sits — the column everything deeper
// belongs to the scalar rather than to the workflow.
//
// Only the header is recognised here, never the content: the content is skipped
// by indentation in contentLines, which is how YAML itself delimits it. A
// trailing comment after the indicator (`run: | # write the stub`) is legal and
// does not change the answer, so only the first field is inspected.
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
// The comparison is case-INSENSITIVE because GitHub's resolution is
// (DESIGN §7).
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

// isReleaseTag reports whether ref is a house release tag on glyph's own bare
// line: GoReleaser cuts glyph's tags as vX.Y.Z, so a v-less tag simply does
// not exist to pin.
func isReleaseTag(ref string) bool {
	_, ok := bump.HouseTagOn(config.Line{}, ref)
	return ok
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
