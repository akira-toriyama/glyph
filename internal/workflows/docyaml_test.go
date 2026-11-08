package workflows

// The YAML a reader copies by hand: the fenced blocks of README.md and docs/*.md,
// and the usage examples standing in the comments of the workflow and action files
// (doctor's remedy calls a reusable's stub the known-good copy). actionlint reads a
// workflow's executable text and nothing else, so a snippet here that is no YAML
// stays green and fails only whoever pastes it.
//
// This is not a YAML parser, and it does not grow into one: go.mod carries no YAML
// library, and this package and doctor both read YAML by line. It judges two
// constructs by the rules of YAML 1.2.2, and TestDocumentedYAMLParses's doc lists
// what it leaves unjudged:
//
//   - flow collections (§7.4), in a document and in a comment: brackets close and
//     match, a plain scalar holds no flow indicator — so an unquoted ${{ }} ends the
//     scalar at `$` and the `{` after it is a syntax error — and `:` right after a
//     quoted scalar or a closed collection is the value indicator JSON writes;
//   - block structure (§8.2), in a document only: a key's column is its mapping's
//     and a dash's column is its sequence's, a nested collection opens under a line
//     that carries no value, a sequence may stand at its parent key's column, and a
//     plain scalar alone continues onto deeper lines, until a comment closes it.
//
// A block scalar's lines are its content whatever they look like — shell's `{ …; }`
// and `[ … ]`, and shell's `#` comments — so both readers skip them whole.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// yamlText is YAML read out of a host file, with the host line its text starts on.
type yamlText struct {
	line int
	text string
}

// fencedYAML returns the body of every yaml fence in a Markdown document. Every
// fence is tracked, so a yaml opener quoted inside another fence is not one.
func fencedYAML(md string) []yamlText {
	var blocks []yamlText
	var body []string
	start, open, isYAML := 0, false, false
	for i, line := range strings.Split(md, "\n") {
		fence := strings.TrimSpace(line)
		switch {
		case !open && strings.HasPrefix(fence, "```"):
			info := strings.TrimPrefix(fence, "```")
			open, isYAML, start, body = true, info == "yaml" || info == "yml", i+2, nil
		case open && fence == "```":
			open = false
			if isYAML {
				blocks = append(blocks, yamlText{start, strings.Join(body, "\n") + "\n"})
			}
		case open:
			body = append(body, line)
		}
	}
	return blocks
}

// commentYAML returns a file's whole-line comments with the `#` removed and every
// other line blank, so a line of the result is the same line of the file. A `#`
// line inside a block scalar is the scalar's text — a shell comment in a `run: |`
// step — and not a YAML comment, so it stays blank with the rest of the scalar.
func commentYAML(raw string) yamlText {
	lines := strings.Split(raw, "\n")
	out := make([]string, len(lines))
	for i := 0; i < len(lines); i++ {
		text := strings.TrimLeft(lines[i], " \t")
		if after, ok := strings.CutPrefix(text, "#"); ok {
			out[i] = after
			continue
		}
		if n := readYAMLNode(lines[i]); yamlBlockScalar.MatchString(n.value) {
			i = blockScalarEnd(lines, i, n.owner())
		}
	}
	return yamlText{1, strings.Join(out, "\n")}
}

// yamlNode is one line's nodes: each `- ` opens a sequence entry, and after them
// stand a key with its value, a value alone, or nothing. A node's column is what
// its text leaves of the line's length.
type yamlNode struct {
	dashes     []int // the column of each dash
	col        int   // the column of the key or lone value
	key, value string
	isKey      bool
}

// readYAMLNode reads the nodes of line. A whole-line comment reads as a line
// with no node and an empty value; a comment after the dashes, the same.
func readYAMLNode(line string) yamlNode {
	var n yamlNode
	text := strings.TrimLeft(line, " ")
	for text == "-" || strings.HasPrefix(text, "- ") {
		n.dashes = append(n.dashes, len(line)-len(text))
		text = strings.TrimLeft(text[1:], " ")
	}
	if text != "" && text[0] == '#' {
		text = ""
	}
	n.col = len(line) - len(text)
	n.key, n.value, n.isKey = cutYAMLKey(text)
	if !n.isKey {
		n.value = text
	}
	return n
}

// owner is the node a block scalar's indentation is measured from: the key that
// carries it, or the last dash when the value stands alone in an entry.
func (n yamlNode) owner() int {
	if !n.isKey && len(n.dashes) > 0 {
		return n.dashes[len(n.dashes)-1]
	}
	return n.col
}

// blockScalarEnd returns the index of the last line of the block scalar whose
// header is lines[i] and whose owner sits at column owner. The body is every line
// at least as deep as its first non-blank one, which must itself be deeper than
// the owner, blank lines included; a shallower line ends the scalar.
func blockScalarEnd(lines []string, i, owner int) int {
	content := -1
	for i+1 < len(lines) {
		next := lines[i+1]
		if strings.TrimSpace(next) != "" {
			n := len(next) - len(strings.TrimLeft(next, " "))
			if n <= owner || (content >= 0 && n < content) {
				break
			}
			if content < 0 {
				content = n
			}
		}
		i++
	}
	return i
}

var (
	// yamlKey is a plain `key:` opening a line's node, with its value after one or
	// more spaces or nothing at all. The key ends at the first `:` a space or the
	// line's end follows, so `a:b: 1` keys `a:b`. A quoted key is not read, and a
	// `:` followed by a tab is not a value indicator here: PyYAML refuses that tab
	// and libyaml takes it (measured 2026-11-04), so the reader judges neither.
	yamlKey = regexp.MustCompile("^([^\\s\\-?,\\[\\]{}#&*!|>'\"%@`:][^#]*?):(?: +(.*))?$")
	// yamlBlockScalar is a value that opens a block scalar.
	yamlBlockScalar = regexp.MustCompile(`^[|>][-+0-9]*(?: +#.*)?$`)
)

// yamlLevel is an open block collection: the column of its entries and its kind,
// 'm' for a mapping and 's' for a sequence. shared marks a sequence standing at its
// parent key's own column (`a:\n- b`), which that mapping's next key closes.
type yamlLevel struct {
	column int
	kind   byte
	shared bool
}

// yamlFaults returns one message per construct in y that YAML refuses, each naming
// its host line. A document is judged for block structure and flow collections; a
// fragment — the comments of a file, whose lines stand in no structure — for flow
// collections alone. A block scalar's lines are skipped whole: the scalar ends at
// the first line no deeper than the node that owns it.
func yamlFaults(y yamlText, document bool) []string {
	var faults []string
	lines := strings.Split(y.text, "\n")
	fault := func(i int, format string, args ...any) {
		faults = append(faults, fmt.Sprintf("line %d (%s): %s", y.line+i, strings.TrimSpace(lines[i]), fmt.Sprintf(format, args...)))
	}

	var levels []yamlLevel
	top := func() *yamlLevel {
		if len(levels) == 0 {
			return nil
		}
		return &levels[len(levels)-1]
	}
	pending := -1      // column of a key or dash whose node is still to come; -1 after a value
	plain := false     // the last value was a plain scalar still open, which deeper lines continue
	commented := false // the last value was a plain scalar a comment closed, which nothing continues
	floor := -1        // column of the collection holding that scalar; a continuation sits deeper

	for i := 0; i < len(lines); i++ {
		text := strings.TrimLeft(lines[i], " ")
		if text == "" || text[0] == '#' {
			continue
		}
		indent := len(lines[i]) - len(text)
		if document && indent == 0 && (text == "---" || text == "...") {
			levels, pending, plain, commented = nil, -1, false, false
			continue
		}

		n := readYAMLNode(lines[i])
		dashes, col, key, value, isKey := n.dashes, n.col, n.key, n.value, n.isKey
		text = lines[i][col:]

		if document {
			for top() != nil && top().column > indent {
				levels = levels[:len(levels)-1]
			}
			t := top()
			placed := t == nil || t.column == indent || (pending >= 0 && indent > pending)
			if !placed {
				switch {
				case plain && indent > floor && !holdsValueIndicator(text):
					// A plain scalar's continuation line (`a: 1\n  - b` reads "1 - b"); a
					// comment on it ends the scalar (`a: 1\n  b  # c\n  d` is refused).
					pending = -1
					if commentCloses(text) {
						plain, commented = false, true
					}
					continue
				case plain && indent > floor:
					fault(i, "this line continues the plain scalar above it and holds `: `, which YAML reads as a "+
						"mapping value where none is allowed — a key one column off its mapping, or a scalar that "+
						"wants quoting")
				case isKey:
					fault(i, "the key `%s` sits at column %d, where no mapping is open — a block mapping's keys "+
						"share one column, and a nested one begins under a key with no value on its line", key, col)
				case len(dashes) > 0:
					fault(i, "a sequence entry at column %d, where no sequence is open — a nested sequence begins "+
						"under a key or dash with no value on its line", dashes[0])
				case commented && indent > floor:
					fault(i, "a line deeper than the plain scalar above it, which the comment on its line closed — "+
						"a plain scalar ends at ` #`, and nothing continues it past that line")
				default:
					fault(i, "a line deeper than the value above it, which closed on its own line — only a plain "+
						"scalar continues onto deeper lines")
				}
				continue
			}
			misplaced := false
			for _, c := range dashes {
				switch t := top(); {
				case t != nil && t.column == c && t.kind == 's':
				case t != nil && t.column == c && pending != c:
					fault(i, "a sequence entry at column %d, the column of a mapping's keys — a sequence stands "+
						"at its parent key's column only right under a key with no value on its line", c)
					misplaced = true
				case t != nil && t.column == c:
					levels = append(levels, yamlLevel{c, 's', true})
				default:
					levels = append(levels, yamlLevel{c, 's', false})
				}
				if misplaced {
					break
				}
			}
			if misplaced {
				continue
			}
			if isKey {
				if t := top(); t != nil && t.column == col && t.kind == 's' && t.shared {
					levels = levels[:len(levels)-1]
				}
				switch t := top(); {
				case t != nil && t.column == col && t.kind == 's':
					fault(i, "the key `%s` sits at column %d, the column of a sequence's entries — a mapping "+
						"inside an entry begins after `- `", key, col)
					continue
				case t != nil && t.column == col:
				default:
					levels = append(levels, yamlLevel{col, 'm', false})
				}
			}
		}

		pending, plain, commented = -1, false, false
		switch {
		case value == "":
			pending = n.owner()
		case yamlBlockScalar.MatchString(value):
			i = blockScalarEnd(lines, i, n.owner())
		case value[0] == '{' || value[0] == '[':
			rest := value + "\n" + strings.Join(lines[i+1:], "\n")
			end, f := readFlowCollection(rest)
			if f != "" {
				fault(i, "%s", f)
				continue
			}
			tail, _, _ := strings.Cut(rest[end:], "\n")
			if tail = strings.TrimSpace(tail); document && tail != "" && tail[0] != '#' {
				fault(i, "`%s` follows the flow collection on its closing line — YAML takes nothing after the close "+
					"but a comment", tail)
			}
			i += strings.Count(rest[:end], "\n")
		case document && (value[0] == '"' || value[0] == '\''):
			rest := value + "\n" + strings.Join(lines[i+1:], "\n")
			end := readQuotedScalar(rest)
			if end < 0 {
				fault(i, "a quoted scalar never closes")
				continue
			}
			i += strings.Count(rest[:end], "\n")
		default:
			// A plain scalar continues onto deeper lines until a comment on its line
			// closes it (`a: 1  # c\n  b` is refused, `a: 1\n  b` reads "1 b").
			commented = commentCloses(value)
			plain, floor = !commented, -1
			if t := top(); t != nil {
				floor = t.column
			}
		}
	}
	return faults
}

// commentCloses reports whether a plain scalar's line carries a comment, white
// space then `#`, which ends the scalar there: no deeper line continues it.
func commentCloses(text string) bool {
	return strings.Contains(text, " #") || strings.Contains(text, "\t#")
}

// cutYAMLKey splits a node's text into a plain key and its value, which is empty
// for a key with nothing or a comment after the colon.
func cutYAMLKey(text string) (key, value string, ok bool) {
	m := yamlKey.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	value = strings.TrimRight(m[2], " ")
	if value != "" && value[0] == '#' {
		value = ""
	}
	return m[1], value, true
}

// holdsValueIndicator reports whether a line's text, less a trailing comment,
// holds a `:` a space or the line's end follows — what makes a plain scalar's
// continuation line a syntax error.
func holdsValueIndicator(text string) bool {
	if i := strings.Index(text, " #"); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimRight(text, " ")
	return strings.Contains(text, ": ") || strings.HasSuffix(text, ":")
}

// readQuotedScalar returns the offset just past the close of the quoted scalar s
// opens, across lines, or -1 when it never closes. `\"` is an escape inside double
// quotes, and a doubled quote inside single.
func readQuotedScalar(s string) int {
	for j := 1; j < len(s); j++ {
		switch {
		case s[0] == '"' && s[j] == '\\':
			j++
		case s[j] == s[0]:
			if s[0] == '\'' && j+1 < len(s) && s[j+1] == '\'' {
				j++
				continue
			}
			return j + 1
		}
	}
	return -1
}

// readFlowCollection walks the flow collection s opens and returns the offset just
// past its close, or what YAML refuses in it. json marks the position after a
// quoted scalar or a closed collection, where `:` is the value indicator with
// nothing between (§7.4.2: `{"a":{"b":1}}`); anywhere else a `:` followed by a
// plain character continues the plain scalar (`a:1`).
func readFlowCollection(s string) (end int, fault string) {
	var closers []byte
	plain, json := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ' ', '\t', '\n':
			if i+1 < len(s) && s[i+1] == '#' {
				newline := strings.IndexByte(s[i+1:], '\n')
				if newline < 0 {
					return len(s), "the flow collection never closes"
				}
				i += newline
			}
		case '"', '\'':
			if plain {
				continue
			}
			n := readQuotedScalar(s[i:])
			if n < 0 {
				return len(s), "a quoted scalar never closes"
			}
			i += n - 1
			json = true
		case '{':
			if plain {
				return i, "a plain scalar in a flow collection cannot hold `{` — quote the value, or write the mapping in block style"
			}
			closers = append(closers, '}')
			json = false
		case '[':
			if plain {
				return i, "a plain scalar in a flow collection cannot hold `[` — quote the value, or write the sequence in block style"
			}
			closers = append(closers, ']')
			json = false
		case '}', ']':
			if closers[len(closers)-1] != c {
				return i, fmt.Sprintf("`%c` closes a collection `%c` should", c, closers[len(closers)-1])
			}
			closers = closers[:len(closers)-1]
			plain, json = false, true
			if len(closers) == 0 {
				return i + 1, ""
			}
		case ',':
			plain, json = false, false
		case ':':
			plain = !json && i+1 < len(s) && strings.IndexByte(" \t\n,[]{}", s[i+1]) < 0
			json = false
		default:
			plain, json = true, false
		}
	}
	return len(s), "the flow collection never closes"
}

// TestDocumentedYAMLParses holds every fenced yaml block in README.md and docs/*.md,
// and every commented usage example in the workflow and action files, to the two
// rules the reader knows. README's install snippets and the install action's header
// wrote `with: { …, token: ${{ github.token }} }`: pushed as written, GitHub refuses
// the whole workflow file ("You have an error in your yaml syntax", glyph-test-2
// run 41809060151, no job started).
//
// What is judged: flow collections, everywhere — closing, matching, no flow
// indicator inside a plain scalar, `:` adjacent after a quoted scalar or a closed
// collection, nothing but a comment after the close in a document; block structure,
// in a document — a key at a column where no mapping is open, a dash at a column
// where no sequence is, a key at a sequence's column or a dash at a mapping's, a
// continuation line of a plain scalar holding `: `, any line deeper than a value
// that closed on its own line — a flow collection, a quoted scalar, or a plain
// scalar whose line carries a comment, since ` #` ends a plain scalar — a quoted
// scalar that never closes. The content of a block scalar is judged nowhere: in a
// document the block reader skips it, and in a workflow file the comment scan
// leaves a `#` line inside one blank (TestDocumentedYAMLCommentScanLeavesShellAlone).
//
// What is not judged, and stays green here: a quoted key and the value behind it;
// a `:` followed by a tab, and a line indented with one; the content of scalars,
// so a plain scalar's continuation lines, text after a quoted scalar, anchors, tags
// and aliases; a scalar line standing at a collection's column; a `?` explicit key;
// a document whose later lines sit left of its first; duplicate keys; and what the
// text means to GitHub. None of these files writes one of those shapes.
//
// The table is the oracle, and its verdicts are the real parsers', not this
// reader's: measured 2026-11-04 and 2026-11-08 with PyYAML 6.0.3 (its pure parser
// and its libyaml binding) and Psych 3.1.0 on libyaml 0.2.1, the three agreeing on
// every row, and on GitHub for the unquoted install row and the quoted one (run
// 41809639082 ran it). A shape the three parsers split on has no row: `with:\t{ a: 1 }`
// and `{[a]:1}`, and a `:` right after a plain key with a flow indicator next —
// `{a:[b]}`, `{a:{b: 1}}`, `{a:}` — which libyaml refuses and PyYAML's pure parser
// takes, so the indicator the reader grants there is pinned by no row. A reader
// that drifts from the table fails here before it judges a file.
func TestDocumentedYAMLParses(t *testing.T) {
	for _, c := range []struct {
		doc     string
		refused bool
	}{
		// flow collections
		{"- uses: o/r/a@v1\n  with: { version: vX.Y.Z, token: ${{ github.token }} }\n", true},
		{"- uses: o/r/a@v1\n  with: { token: ${{ github.token }} }\n", true},
		{"matrix: [ ${{ github.token }} ]\n", true},
		{"a: { b: [ ${{ c }} ] }\n", true},
		{"a: {\n  b: 1,\n  c: ${{ d }}\n}\n", true},
		{"a: { b: c${{ d }} }\n", true},
		{"a: { b: c d {e} }\n", true},
		{"- { a: ${{ b }} }\n", true},
		{"- - [ ${{ b }} ]\n", true},
		{"a: { b: 1\n", true},
		{"a: [ b, c }\n", true},
		{"{\"a\":{\"b\":${{ c }}}}\n", true},
		{"{\"a\":[${{ b }}]}\n", true},
		{"a: [1] b\n", true},
		{"- uses: o/r/a@v1\n  with: { version: vX.Y.Z, token: \"${{ github.token }}\" }\n", false},
		{"- uses: o/r/a@v1\n  with: { version: vX.Y.Z, token: '${{ github.token }}' }\n", false},
		{"on:\n  pull_request:\n    types: [opened, synchronize, reopened, edited]\n  push:\n    branches: [main]  # optional\n", false},
		{"a: [ { b: 1 }, { c: 2 } ]\n", false},
		{"a: {b: [1, 2], c: {d: e}}\n", false},
		{"a: { b: c#d, e: f }  # g\n", false},
		{"a: {\n  b: 1,  # comment\n  c: \"${{ d }}\"\n}\n", false},
		{"a: { b: 'it''s', c: \"x\\\"{y\" }\n", false},
		{"{\"a\":{\"b\":1}}\n", false},
		{"{\"a\":[\"b\"]}\n", false},
		{"a: {\"b\":{\"c\":1}}\n", false},
		{"a: {\"b\":[\"c\"]}\n", false},
		{"- {\"a\":[\"b\"]}\n", false},
		{"{\"error\":{\"code\":3,\"message\":\"m\"}}\n", false},
		{"matrix: {\"a\":[\"b\"],\"c\":[\"d\"]}\n", false},
		{"{ \"a\" : 1 }\n", false},
		{"{\"a\": {\"b\": 1}}\n", false},
		{"{'a':{'b':1}}\n", false},
		{"[\"a\":1]\n", false},
		{"a: {\n  b: 1\n}\nc: 2\n", false},
		{"- a: [\n    1,\n    2\n  ]\n  b: 3\n", false},
		{"a:\n  [1, 2]\nb: 3\n", false},
		{"a: [1]\n  # c\nb: 2\n", false},
		{"- [1]\n- b\n", false},
		{"a:{\"b\":1}\n", false},
		// the `:` after a quoted scalar is the value indicator even when a quoted
		// scalar holding a flow indicator follows it with nothing between
		{"{\"a\":\"x{y\"}\n", false},
		{"{\"a\":\"[b]\"}\n", false},
		{"[\"a\":\"{b}\"]\n", false},
		{"{\"a\":'x{y'}\n", false},
		{"{\"a\" :\"x{y\"}\n", false},
		{"{\"a\":{\"b\":\"c{d\"}}\n", false},
		{"{\"a\":[\"x{y\"]}\n", false},
		{"{\"error\":{\"code\":3,\"message\":\"glyph.toml: {x}\"}}\n", false},
		{"a: {\"error\":{\"code\":3,\"message\":\"glyph.toml: {x}\"}}\n", false},
		{"- {\"a\":\"x{y\"}\n", false},
		// block scalars and quoted scalars
		{"run: |\n  { echo a; echo b; } > f\n  [ -f f ]\n", false},
		{"run: >-\n  { echo ${{ a }}; }\n", false},
		{"- run: |\n    { echo; }\n  if: x\n", false},
		{"a: >\n  x\n   y\n", false},
		{"a: |\n  x\nb: 1\n", false},
		{"a: |\n  x\n b: 1\n", true},
		{"a: \"x\n  y\"\n", false},
		{"a: \"x\n  b: y\"\n", false},
		{"a: 'x\n  b: y'\n", false},
		{"a: \"x\n  y\"\nb: 1\n", false},
		{"a: \"x\"\nb: 1\n", false},
		{"a: \"x\n", true},
		{"a: 'x\n", true},
		// block structure: the README and DESIGN shapes
		{"- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z\n  with:\n    version: vX.Y.Z\n    token: ${{ github.token }}\n", false},
		{"- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z\n  with:\n    version: vX.Y.Z\n   token: ${{ github.token }}\n", true},
		{"- uses: o/r/a@v1\n  with:\n    version: vX.Y.Z\n     token: ${{ github.token }}\n", true},
		{"- uses: akira-toriyama/glyph/.github/actions/install@vX.Y.Z\n  with:\n    version: vX.Y.Z              # the tag you pinned above\n    token: ${{ github.token }}   # for `gh attestation verify`\n- run: glyph bump --range \"$BASE..HEAD\" --json   # exit 1 on a none verdict — handle it\n", false},
		{"# .github/workflows/commit-lint.yml\non:\n  pull_request:\n    # a retitle re-fires the squash-title lint only with `edited`, not a default type\n    types: [opened, synchronize, reopened, edited]\n  push:\n    branches: [main]  # optional — annotates direct pushes, never fails them\npermissions:\n  contents: read\n  pull-requests: read  # the squash-title lint reads GET /pulls/{n}\njobs:\n  lint:\n    uses: akira-toriyama/glyph/.github/workflows/lint.yml@vX.Y.Z  # pin a release tag\n", false},
		{"- uses: x\n  with:\n    a: 1\n- uses: y\n  id: z\n", false},
		{"token: ${{ github.token }}\n", false},
		// block structure: keys and dashes off their collection's column
		{"a: 1\n  b: 2\n", true},
		{"a:\n  b: 1\n c: 2\n", true},
		{"a:\n  b: 1\n   c: 2\n", true},
		{"a: 1\n b: 2\n", true},
		{"a:\n    - b\n  c: 1\n", true},
		{"a: {\n  b: 1\n}\n c: 2\n", true},
		{"- a: 1\n    b: 2\n", true},
		{"a: 1\n- b\n", true},
		{"a:\n  b: 1\n  - c\n", true},
		{"- a\nb: c\n", true},
		{"a:\n  - b\n  c: 1\n", true},
		{"- a\n  b: 1\n", true},
		{"- \"a\"\n  b: 1\n", true},
		{"a:\n  - b\n   c: 1\n", true},
		{"a:\n  text\n  b: 1\n", true},
		{"a: long\n  b: c\n", true},
		{"a: 1\n  b\n  c: 2\n", true},
		{"a: 1\n  \"b: c\"\n", true},
		{"a: [1]\n  text\n", true},
		{"a: [1]\n  b: 2\n", true},
		{"a: {b: 1}\n  c: 2\n", true},
		{"a:\n  [1, 2]\n  b: 3\n", true},
		{"a: \"x\"\n  text\n", true},
		{"a: \"x\"\n  - b\n", true},
		{"a: 'x'\n  - b\n", true},
		{"- [1]\n  - b\n", true},
		// block structure: a comment ends the plain scalar on its line, so nothing
		// deeper continues it (README's lint caller, its `branches:` line continued)
		{"on:\n  push:\n    branches: main  # c\n      - release\n", true},
		{"a: 1  # c\n  b\n", true},
		{"a: 1 #c\n  b\n", true},
		{"a: 1\t# c\n  b\n", true},
		{"a: 1  # c\n  - b\n", true},
		{"a: 1  # c\n  b: 2\n", true},
		{"a: 1  # c\n  [b]\n", true},
		{"a: 1  # c\n  \"b\"\n", true},
		{"a: 1 # c\n\n  b\n", true},
		{"a: x \"y # z\"\n  w\n", true},
		{"- a  # c\n  b\n", true},
		{"a: long\n  continued  # c\n  more\n", true},
		// block structure: what YAML takes
		{"steps:\n- uses: x\n- run: y\n", false},
		{"a:\n- b\n- c\nd: 1\n", false},
		{"a:\n- b\nc: 1\n", false},
		{"a:\n  b:\n  - c\n  d: 1\n", false},
		{"- a:\n  - b\n  c: 2\n", false},
		{"- a:\n    b: 1\n  c: 2\n", false},
		{"a:\n  b:\n    c: 1\n  d: 2\ne: 3\n", false},
		{"a:\n  b:\nc: 1\n", false},
		{"a:\nb: 1\n", false},
		{"a:\nb:\n", false},
		{"a:\n    b: 1\n    c: 2\n", false},
		{"a:\n    - b\n    - c\nd: 1\n", false},
		{"a:\n  - b: 1\n    c: 2\n  - d\n", false},
		{"- - a\n  - b\n- c\n", false},
		{"- - a: 1\n    b: 2\n  - c\n", false},
		{"- a: 1\n  b: 2\n- c: 3\n", false},
		{"-\n  a: 1\n", false},
		{"-\n  - b\n", false},
		{"-\n- b\n", false},
		{"- a\n-\n- b\n", false},
		{"a:  # c\n  b: 1\n", false},
		{"a: # c\n  - b\n", false},
		{"-  # c\n  a: 1\n", false},
		{"a:   \n  b: 1\n", false},
		{"a:\n  # comment\n  b: 1\n", false},
		{"key: value\n\n\nother: 1\n", false},
		{"---\na: 1\n", false},
		{"a: long\n  continued\n", false},
		{"on:\n  push:\n    branches: main\n      - release\n", false},
		{"a: 1\n  b\n", false},
		{"a: 1  # c\nb: 2\n", false},
		{"a: c#d\n  e\n", false},
		{"a:  # c\n  b\n", false},
		{"- a  # c\n- b\n", false},
		{"a: long\n  continued  # c\nb: 1\n", false},
		{"a: long\n  continued\n  more\n", false},
		{"a: 1\n  - b\n", false},
		{"a: 1\n  \"b\"\n", false},
		{"a: 1\n  [b]\n", false},
		{"- a\n  \"b\"\n", false},
		{"- a\n  - b\n", false},
		{"- a\n - b\n", false},
		{"- - a\n   - b\n", false},
		{"- a\n  b\n- c\n", false},
		{"a:\n  text\n", false},
		{"a:\n  text\nb: 1\n", false},
		{"a: b:c\n", false},
		{"a:b: 1\n", false},
		{"a: https://x/y\n", false},
		{"a:   b\n", false},
		{"pull-requests: read\nfoo bar: 1\n", false},
		{"a: &x 1\nb: *x\n", false},
		{"? a\n: b\n", false},
		{"\"a\": 1\nb: 2\n", false},
	} {
		faults := yamlFaults(yamlText{1, c.doc}, true)
		if c.refused != (len(faults) > 0) {
			t.Fatalf("the reader says %v over a document the real parsers %s — every verdict below would be "+
				"this reader's opinion:\n%s", faults, map[bool]string{true: "refuse", false: "accept"}[c.refused], c.doc)
		}
	}
	// A comment is a fragment: its flow collections are judged, its lines' columns
	// are not, and prose after a `{…}` is prose.
	for _, c := range []struct {
		doc     string
		refused bool
	}{
		{"  - uses: o/r/a@v1\n    with: { version: vX.Y.Z, token: ${{ github.token }} }\n", true},
		{"  - uses: o/r/a@v1\n    with:\n      version: vX.Y.Z\n     token: ${{ github.token }}\n", false},
		{"{owner}/{repo} is the slug\n", false},
		{"{\"error\":{\"code\":3,\"message\":\"m\"}} is the shape of the envelope\n", false},
	} {
		faults := yamlFaults(yamlText{1, c.doc}, false)
		if c.refused != (len(faults) > 0) {
			t.Fatalf("the reader says %v over a fragment it should %s:\n%s",
				faults, map[bool]string{true: "refuse", false: "accept"}[c.refused], c.doc)
		}
	}

	docs, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil || len(docs) == 0 {
		t.Fatalf("listing docs/*.md: %v (%d found)", err, len(docs))
	}
	fenced := map[string]int{}
	for _, md := range append([]string{"README.md"}, docs...) {
		rel := strings.TrimPrefix(md, filepath.Join("..", "..")+string(filepath.Separator))
		for _, block := range fencedYAML(repoFile(t, rel)) {
			fenced[rel]++
			for _, fault := range yamlFaults(block, true) {
				t.Errorf("%s %s\nYAML refuses this, and GitHub refuses a workflow file that is not YAML with no job "+
					"started (glyph-test-2 run 41809060151), so the snippet fails whoever pastes it", rel, fault)
			}
		}
	}
	if fenced["README.md"] < 3 || fenced[filepath.Join("docs", "DESIGN.md")] < 1 {
		t.Fatalf("read %d fenced yaml blocks in README.md and %d in docs/DESIGN.md, want the lint caller and the two "+
			"install snippets, and §6's consumer snippet, at least — the check above was over too little",
			fenced["README.md"], fenced[filepath.Join("docs", "DESIGN.md")])
	}

	var hosts []string
	for _, name := range workflowFiles(t) {
		hosts = append(hosts, filepath.Join(".github", "workflows", name))
	}
	actions, err := filepath.Glob(filepath.Join("..", "..", ".github", "actions", "*", "action.yml"))
	if err != nil || len(actions) < 2 {
		t.Fatalf("listing the composite actions: %v (%d found, want the install and release-notes actions at least)", err, len(actions))
	}
	for _, path := range actions {
		hosts = append(hosts, filepath.Join(".github", "actions", filepath.Base(filepath.Dir(path)), "action.yml"))
	}
	for _, host := range hosts {
		for _, fault := range yamlFaults(commentYAML(repoFile(t, host)), false) {
			t.Errorf("%s %s\nYAML refuses this, and GitHub refuses a workflow file that is not YAML with no job "+
				"started (glyph-test-2 run 41809060151), so the usage example fails whoever pastes it", host, fault)
		}
	}
}

// TestDocumentedYAMLCommentScanLeavesShellAlone adds a shell comment to a copy of
// lint.yml's title step and reads what the comment scan makes of it. A `#` line
// inside a `run: |` block is the script's text, never a YAML comment, so the scan
// leaves it blank where it once read it as a usage-example fragment and refused
// `[ -n "${VAR}" ]`, shell's commonest test, as a flow sequence holding `{` — a
// false diagnosis sending a reader to fix YAML that GitHub, actionlint and PyYAML
// all take (PyYAML reads the fixture's line as the step's run script, measured
// 2026-11-08). The canary writes the same text as a real whole-line comment, at
// `jobs:`'s column, and the scan refuses it there: the fixture's green is the
// block scalar's doing, not the text's.
func TestDocumentedYAMLCommentScanLeavesShellAlone(t *testing.T) {
	raw := repoFile(t, filepath.FromSlash(".github/workflows/lint.yml"))
	const (
		anchor = "          sed -n '/^[{]/,$p' \"$RUNNER_TEMP/glyph-title.err\" > \"$RUNNER_TEMP/glyph-title.json\"\n"
		shell  = "          # [ -n \"${PR_NUMBER:-}\" ] needs no test here: the event condition keeps a push out\n"
	)
	if strings.Count(raw, anchor) != 1 {
		t.Fatalf("lint.yml holds the title step's sieve line %d times, want once — the fixture below has nowhere to stand", strings.Count(raw, anchor))
	}
	if faults := yamlFaults(commentYAML(raw), false); len(faults) > 0 {
		t.Fatalf("lint.yml's comments are refused before the fixture is added, so the verdict below is not the fixture's:\n%s", strings.Join(faults, "\n"))
	}

	fixture := strings.Replace(raw, anchor, shell+anchor, 1)
	for _, fault := range yamlFaults(commentYAML(fixture), false) {
		t.Errorf("a shell comment inside the title step's run block is judged as YAML: %s", fault)
	}

	canary := strings.Replace(raw, "\njobs:\n", "\n"+strings.TrimLeft(shell, " ")+"jobs:\n", 1)
	if canary == raw {
		t.Fatal("lint.yml has no top-level `jobs:` line to stand the canary on")
	}
	faults := yamlFaults(commentYAML(canary), false)
	if len(faults) == 0 || !strings.Contains(faults[0], "PR_NUMBER") {
		t.Fatalf("canary: the same text as a whole-line comment outside every block scalar is not refused (%v), so "+
			"the fixture above proves nothing about where the line stands", faults)
	}
}
