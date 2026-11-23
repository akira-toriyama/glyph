package doctor

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The second member of the failure class checkCallerPermissions exists for: a
// caller omitting an input its reusable marks `required: true` also dies as
// startup_failure before any job, and GitHub surfaces NO error anywhere for it
// (DESIGN §7, measured on glyph-test3). This check reads the caller's `with:`
// block against what the reusable requires at the caller's pin (callerpin.go).

// reusableRequiredInputs is every input a glyph reusable marks required — the
// minimum a caller's `with:` must name — with the newest release whose
// reusable required otherwise (After; callerpin.go). Required mirrors the
// workflow_call.inputs blocks in this repo's own workflow files;
// TestReusableRequiredInputsMatchTheShippedWorkflows holds the two in
// lockstep, so a reusable growing a required input without a row here fails a
// test instead of shipping a check that blesses callers GitHub will kill, and
// TestCallerDeclarationBoundsMatchReleasedTags holds After to glyph's tags.
// No reusable requires an input today — release.yml required install-notes
// through v6.1.0 — so the judging below is exercised by a fixture table in
// its tests, never by a live row.
var reusableRequiredInputs = map[string]inputRow{
	"lint.yml":       {After: "v0.1.0"},
	"release.yml":    {After: "v6.1.0"},
	"pr-verdict.yml": {After: "v0.3.0"},
}

// inputRow is one reusable's required inputs and the bound below which this
// binary cannot speak for them.
type inputRow struct {
	After    string
	Required []string
}

// inputsFloor is the release workflow-caller-inputs first shipped in — the
// oldest glyph whose own doctor can judge its own reusables' inputs.
const inputsFloor = "v3.1.0"

// checkCallerInputs scans the local checkout's workflow files for callers of
// glyph's reusable workflows and verifies that each caller's `with:` names
// every input the reusable requires at the release the caller pins. glyph is
// the running binary's version (judgeAt).
func checkCallerInputs(root string, rootVerified bool, workflows []workflowFile, workflowsErr error, glyph string) Check {
	return judgeCallers(root, rootVerified, workflows, workflowsErr, callerSpec[inputRow]{
		id:          idCallerInputs,
		expected:    inputsExpected(glyph),
		consequence: "A caller omitting a required input dies as startup_failure before any job — unverified here, not verified",
		glyph:       glyph,
		floor:       inputsFloor,
		rows:        reusableRequiredInputs,
		file: func(f workflowFile) func(reusableCall, inputRow) ([]string, string) {
			lines := strings.Split(f.Body, "\n")
			return func(call reusableCall, row inputRow) ([]string, string) {
				if len(row.Required) == 0 {
					// Nothing to ask of with:, so nothing it holds is unread: a
					// caller at a pin that requires no input is never
					// could-not-run over its with: (exit 4 where 0 is right).
					return nil, ""
				}
				var missing []string
				keys, unread := jobWithKeys(lines, call.Line)
				if unread != "" {
					return nil, withUnreadLine(f.Name, call, unread)
				}
				for _, want := range row.Required {
					if !keys[want] {
						missing = append(missing, fmt.Sprintf(
							".github/workflows/%s calls %s@%s but its with: never names %s", f.Name, call.Reusable, call.Ref, want))
					}
				}
				return missing, ""
			}
		},
		failObserved: "%d missing required input(s) across the %d workflow file(s) that call a glyph reusable",
		failMessage: "GitHub kills a reusable call that omits a required input as startup_failure before any job — and " +
			"it surfaces no error anywhere: not in the run, not in the check suite, not in the API (measured, " +
			"glyph-test3 2026-08-26). This static read is the only check that can see it coming",
		failFix:      "add the missing input to the caller's with: block — " + readmeCallers,
		death:        "whether every caller gets past GitHub's silent startup input gate",
		noCaller:     "no workflow in this checkout calls a glyph reusable (binary-only consumers have no caller to starve)",
		passObserved: "%d workflow file(s) call glyph reusables; every judged caller passes what its pinned release requires",
		passMessage:  "these runs get past GitHub's startup input gate",
	})
}

// inputsExpected renders the check's Expected from the table itself, so the
// text cannot drift from the rows it describes.
func inputsExpected(glyph string) string {
	names := make([]string, 0, len(reusableRequiredInputs))
	for name := range reusableRequiredInputs {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]string, 0, len(names))
	for _, name := range names {
		row := reusableRequiredInputs[name]
		required := "none"
		if len(row.Required) > 0 {
			required = strings.Join(row.Required, ", ")
		}
		rows = append(rows, fmt.Sprintf("%s after %s: %s", strings.TrimSuffix(name, ".yml"), row.After, required))
	}
	return "every workflow calling a glyph reusable passes each input that reusable marks required at the release it pins, " +
		"judged for " + judgedRange(glyph) + " (" + strings.Join(rows, "; ") + ") — any other pin is could-not-run"
}

// jobWithKeys returns the top-level keys of the `with:` mapping belonging to
// the job whose `uses:` sits on usesLine (1-indexed): jobWith's keys and its
// unread, for a check that asks only whether an input is named.
func jobWithKeys(lines []string, usesLine int) (map[string]bool, string) {
	keys := map[string]bool{}
	with, unread := jobWith(lines, usesLine)
	for k := range with {
		keys[k] = true
	}
	return keys, unread
}

// What both caller checks say of a caller whose with: jobWith does not read:
// could-not-run, never a verdict either way (DESIGN §7).
const (
	withUnreadWhy = "doctor reads a with: written as a block of plain `key: value` lines or as a one-line `{ … }`, " +
		"and no other form"
	withUnreadFix = "write the caller's with: as a block of plain `key: value` lines or a one-line `{ … }`"
)

// withUnreadLine is the Details line of a call whose with: jobWith does not
// read; unread is jobWith's account of it.
func withUnreadLine(file string, call reusableCall, unread string) string {
	return fmt.Sprintf(".github/workflows/%s calls %s@%s with a with: doctor does not read: %s",
		file, call.Reusable, call.Ref, unread)
}

// jobWith returns the `with:` mapping belonging to the job whose `uses:` sits
// on usesLine (1-indexed): each top-level key and its value as the file
// spells it — the text after the key's colon with any deeper content line
// folded onto it behind one space, each line's trailing comment dropped
// BEFORE the fold: a comment ends at its line's end, so the line under
// `binary: # which tool` is the value, and folded behind the comment it read
// as part of it — an artifact input GitHub refuses graded as empty
// (glyph-monorepo-test-2 run 43689423925, 2026-11-15). A block scalar's value
// is its header alone (`|`): contentLines yields none of its text. The job
// block's bounds are walked out from the uses line itself — up
// to the job-name line (the first shallower-indented content line), then
// forward until the block closes — so a `with:` written above its `uses:` is
// still seen; YAML orders siblings freely and a miss in either direction
// would cry wolf over a healthy caller. The forward walk reads only
// contentLines: an install-notes heredoc's content must not read as keys.
//
// The reader names the forms it reads, and a form it does not positively
// recognise is unread, never a key set either way in a file that is valid
// YAML (a tab-indented key line, or one between the job's indent and the
// keys', reads as no key — GitHub refuses either file outright): unread, when
// not "", names the job, the line and its text, and the map is nil. The job's
// own key lines are withEntry's too, so `with  :` and `"with":` are with, a
// job-level line it does not read could be with and is unread, and a `-` line
// there is a sequence entry under the key above. A job key whose value opens a
// flow collection that does not close on its line owns every line up to the
// bracket that closes it, whatever their indent: a `]` at the job's indent
// continues `needs: [`, and read as a job-level line it made a healthy caller
// could-not-run (exit 4). The forms read: the
// `with:` line's rest, its comment cut, is empty (block form) or a flow
// mapping closing on that line, split by flowEntries; each block-form line at
// the key indent is one withEntry. An alias, an anchor or a tag on `with:`, a
// `{` closing on a later line, a merge key, a `?` key, a key carrying an
// anchor or a tag — PyYAML reads each as setting keys this reader cannot see
// (a quoted `"binary": probe` read as the key `"binary"` passed a caller the
// reusable refuses).
func jobWith(lines []string, usesLine int) (map[string]string, string) {
	with := map[string]string{}
	if usesLine < 1 || usesLine > len(lines) {
		return with, ""
	}
	jobIndent := indentOf(lines[usesLine-1])
	start := usesLine - 1
	for i := usesLine - 2; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(strings.TrimLeft(lines[i], " \t"), "#") {
			continue // blank as contentLines means it: a CRLF file's lone `\r` included
		}
		if indentOf(lines[i]) < jobIndent {
			start = i + 1
			break
		}
		start = i
	}
	notRead := func(i int, line string) (map[string]string, string) {
		job := "?"
		if start > 0 {
			job, _, _ = strings.Cut(strings.TrimSpace(lines[start-1]), ":")
		}
		return nil, fmt.Sprintf("job %s, line %d `%s`", job, i+1, withoutComment(strings.TrimSpace(line)))
	}
	inWith := false
	open := 0 // the bracket depth a job key's flow collection leaves open
	withKeyIndent := -1 // indent of the keys directly inside with:, learned from the first one
	last := ""          // the with: key a deeper line continues
	for i, line := range contentLines(lines, start) {
		if open > 0 {
			var closed bool
			if open, closed = flowOpen(withoutComment(strings.TrimSpace(line)), open); !closed {
				return notRead(i, line)
			}
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		ind := indentOf(line)
		if ind < jobIndent {
			break // the job block closed
		}
		if ind == jobIndent {
			k, rest, ok := withEntry(trimmed)
			if !ok && !strings.HasPrefix(trimmed, "-") {
				return notRead(i, line)
			}
			isWith := ok && k == "with"
			inWith = isWith
			if isWith {
				// Flow form: with: { a: 1, b: 2 } — the keys sit on this line.
				if rest = withoutComment(strings.TrimSpace(rest)); rest != "" {
					entries, flow := flowEntries(rest)
					if !flow {
						return notRead(i, line)
					}
					for _, e := range entries {
						k, v, ok := withEntry(e)
						if !ok {
							return notRead(i, line)
						}
						with[k] = v
					}
					inWith = false
				}
			} else if v := withoutComment(strings.TrimSpace(rest)); strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
				var closed bool
				if open, closed = flowOpen(v, 0); !closed {
					return notRead(i, line)
				}
			}
			continue
		}
		if !inWith {
			continue
		}
		if withKeyIndent < 0 {
			withKeyIndent = ind
		}
		if ind != withKeyIndent {
			// Deeper content: a nested value or a plain scalar's next line,
			// never a with: key.
			if last != "" {
				with[last] = strings.TrimSpace(with[last] + " " + withoutComment(strings.TrimSpace(trimmed)))
			}
			continue
		}
		k, v, ok := withEntry(trimmed)
		if !ok {
			return notRead(i, line)
		}
		last = k
		with[last] = withoutComment(v)
	}
	return with, ""
}

// flowSyntax walks s as flow text and calls at with each flow indicator
// outside quotes — , : { } [ ] — stopping when at returns false. It reports
// whether the walk ran to the end with every quote closed. A quote opens only
// where a scalar begins, so the apostrophe in a plain `don't` is text.
func flowSyntax(s string, at func(i int, c byte) bool) bool {
	var quote byte
	begins := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			if begins {
				quote = c
			}
			begins = false
		case strings.IndexByte(",:{}[]", c) >= 0:
			if !at(i, c) {
				return false
			}
			begins = c != '}' && c != ']'
		case c != ' ' && c != '\t':
			begins = false
		}
	}
	return quote == 0
}

// flowOpen returns the bracket depth s leaves open when the flow text before
// it left depth open; closed is false when s leaves a quote open, which this
// reader does not carry to the next line.
func flowOpen(s string, depth int) (open int, closed bool) {
	closed = flowSyntax(s, func(_ int, c byte) bool {
		switch c {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
		return true
	})
	return depth, closed
}

// flowEntries splits a flow mapping written whole on one line, `{ … }`, into
// its entries, on the commas outside quotes (flowSyntax). flow is false for
// anything else: no `{`, a `{` that does not close at the line's end, a
// nested `{` or `[`, a quote left open.
func flowEntries(s string) (entries []string, flow bool) {
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, false
	}
	s = s[1 : len(s)-1]
	from := 0
	if !flowSyntax(s, func(i int, c byte) bool {
		switch c {
		case ',':
			entries = append(entries, s[from:i])
			from = i + 1
		case ':':
		default:
			return false // a nested collection
		}
		return true
	}) {
		return nil, false
	}
	entries = append(entries, s[from:])
	return slices.DeleteFunc(entries, func(e string) bool { return strings.TrimSpace(e) == "" }), true
}

// withEntry reads one `with:` entry, `key: value`, whose key is a plain
// scalar or a quoted one without escapes — unquoted here, as YAML reads
// `"binary"` as binary. value is trimmed, its comment not cut. ok is false
// for any other entry: a merge key `<<`, a `?` key, a key carrying an anchor,
// an alias or a tag, a quoted key with an escape, no `: ` at all.
func withEntry(s string) (key, value string, ok bool) {
	s = strings.TrimSpace(s)
	var rest string
	switch {
	case s == "":
		return "", "", false
	case s[0] == '"' || s[0] == '\'':
		end := strings.IndexByte(s[1:], s[0])
		if end < 0 {
			return "", "", false
		}
		key, rest = s[1:1+end], s[2+end:]
		if strings.ContainsRune(key, '\\') || !strings.HasPrefix(rest, ":") {
			return "", "", false // an escape, `''` included, is not a simple key
		}
		rest = rest[1:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			return "", "", false
		}
	case strings.IndexByte("-?:,[]{}#&*!|>%@`", s[0]) >= 0:
		return "", "", false
	default:
		at := -1
		for i := 0; i < len(s) && at < 0; i++ {
			if s[i] == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
				at = i
			}
		}
		if at < 0 {
			return "", "", false
		}
		key, rest = strings.TrimSpace(s[:at]), s[at+1:]
		if key == "<<" {
			return "", "", false
		}
	}
	return key, strings.TrimSpace(rest), true
}

// withoutComment drops a trailing YAML comment from one line of a value: a
// `#` at the start or behind whitespace, outside quotes.
func withoutComment(v string) string {
	var quote rune
	for i, r := range v {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}
