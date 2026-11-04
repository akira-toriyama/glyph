package workflows

// The YAML a reader copies by hand: README.md's fenced blocks, and the usage
// examples standing in the comments of the workflow and action files (doctor's
// remedy calls a reusable's stub the known-good copy). actionlint reads a
// workflow's executable text and nothing else, so a snippet here that is no YAML
// stays green and fails only whoever pastes it.
//
// This is not a YAML parser, and it does not grow into one: go.mod carries no
// YAML library, and this package and doctor both read YAML by line. It reads flow
// collections, the one construct that has gone wrong here, by the rule of YAML
// 1.2.2 §7.3.3: a plain scalar in flow context cannot hold a flow indicator, so
// an unquoted ${{ }} ends the scalar at `$` and the `{` after it is a syntax error.

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
// other line blank, so a line of the result is the same line of the file.
func commentYAML(raw string) yamlText {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		text := strings.TrimLeft(line, " \t")
		if after, ok := strings.CutPrefix(text, "#"); ok {
			lines[i] = after
		} else {
			lines[i] = ""
		}
	}
	return yamlText{1, strings.Join(lines, "\n")}
}

var (
	// yamlKeyColumn is a block-context line up to its key: indentation and any
	// sequence dashes.
	yamlKeyColumn = regexp.MustCompile(`^ *(?:- +)*`)
	// yamlKey is a plain `key: ` at that column. A quoted key is not read: a value
	// behind one goes unjudged, and none of these files writes one.
	yamlKey = regexp.MustCompile(`^[^\s:#"'{\[][^:#]*: +`)
	// yamlBlockScalar is a value that opens a block scalar.
	yamlBlockScalar = regexp.MustCompile(`^[|>][-+0-9]*(?: +#.*)?$`)
)

// flowFaults returns one message per flow collection in y that YAML refuses, each
// naming its host line. A block scalar's lines are its content whatever they look
// like, shell's `{ …; }` and `[ … ]` above all, so they are skipped: the scalar
// ends at the first line no deeper than its key.
func flowFaults(y yamlText) []string {
	var faults []string
	lines := strings.Split(y.text, "\n")
	for i := 0; i < len(lines); i++ {
		column := len(yamlKeyColumn.FindString(lines[i]))
		value := lines[i][column:]
		value = value[len(yamlKey.FindString(value)):]
		switch {
		case yamlBlockScalar.MatchString(value):
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) != "" && len(next)-len(strings.TrimLeft(next, " ")) <= column {
					break
				}
				i++
			}
		case strings.HasPrefix(value, "{") || strings.HasPrefix(value, "["):
			rest := value + "\n" + strings.Join(lines[i+1:], "\n")
			end, fault := readFlowCollection(rest)
			if fault != "" {
				faults = append(faults, fmt.Sprintf("line %d (%s): %s", y.line+i, strings.TrimSpace(lines[i]), fault))
				continue
			}
			i += strings.Count(rest[:end], "\n")
		}
	}
	return faults
}

// readFlowCollection walks the flow collection s opens and returns the offset just
// past its close, or what YAML refuses in it.
func readFlowCollection(s string) (end int, fault string) {
	var closers []byte
	plain := false
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
			j := i + 1
			for j < len(s) && s[j] != c {
				if c == '"' && s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return len(s), "a quoted scalar never closes"
			}
			i = j
		case '{':
			if plain {
				return i, "a plain scalar in a flow collection cannot hold `{` — quote the value, or write the mapping in block style"
			}
			closers = append(closers, '}')
		case '[':
			if plain {
				return i, "a plain scalar in a flow collection cannot hold `[` — quote the value, or write the sequence in block style"
			}
			closers = append(closers, ']')
		case '}', ']':
			if closers[len(closers)-1] != c {
				return i, fmt.Sprintf("`%c` closes a collection `%c` should", c, closers[len(closers)-1])
			}
			closers = closers[:len(closers)-1]
			plain = false
			if len(closers) == 0 {
				return i + 1, ""
			}
		case ',':
			plain = false
		case ':':
			plain = i+1 < len(s) && !strings.ContainsRune(" \t\n", rune(s[i+1]))
		default:
			plain = true
		}
	}
	return len(s), "the flow collection never closes"
}

// TestDocumentedYAMLFlowCollectionsParse holds every fenced yaml block in README
// and every commented usage example to the flow-collection rule. README's install
// snippets and the install action's header wrote `with: { …, token: ${{ github.token }} }`:
// pushed as written, GitHub refuses the whole workflow file ("You have an error in
// your yaml syntax", glyph-test-2 run 41809060151, no job started).
//
// The table is the oracle, and its verdicts are the real parsers', not this
// reader's: measured 2026-11-04 with PyYAML 6.0.3 and libyaml 0.2.1 on every row,
// and on GitHub for the unquoted row above and the quoted row (run 41809639082
// ran it). A reader that drifts from them fails here before it judges a file.
func TestDocumentedYAMLFlowCollectionsParse(t *testing.T) {
	for _, c := range []struct {
		doc     string
		refused bool
	}{
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
		{"- uses: o/r/a@v1\n  with: { version: vX.Y.Z, token: \"${{ github.token }}\" }\n", false},
		{"- uses: o/r/a@v1\n  with: { version: vX.Y.Z, token: '${{ github.token }}' }\n", false},
		{"- uses: o/r/a@v1\n  with:\n    version: vX.Y.Z\n    token: ${{ github.token }}\n", false},
		{"on:\n  pull_request:\n    types: [opened, synchronize, reopened, edited]\n  push:\n    branches: [main]  # optional\n", false},
		{"a: [ { b: 1 }, { c: 2 } ]\n", false},
		{"a: {b: [1, 2], c: {d: e}}\n", false},
		{"a: { b: c#d, e: f }  # g\n", false},
		{"a: {\n  b: 1,  # comment\n  c: \"${{ d }}\"\n}\n", false},
		{"a: { b: 'it''s', c: \"x\\\"{y\" }\n", false},
		{"run: |\n  { echo a; echo b; } > f\n  [ -f f ]\n", false},
		{"run: >-\n  { echo ${{ a }}; }\n", false},
		{"token: ${{ github.token }}\n", false},
	} {
		faults := flowFaults(yamlText{1, c.doc})
		if c.refused != (len(faults) > 0) {
			t.Fatalf("the reader says %v over a document the real parsers %s — every verdict below would be "+
				"this reader's opinion:\n%s", faults, map[bool]string{true: "refuse", false: "accept"}[c.refused], c.doc)
		}
	}

	blocks := fencedYAML(repoFile(t, "README.md"))
	if len(blocks) < 3 {
		t.Fatalf("read %d fenced yaml blocks in README.md, want the lint caller and the two install snippets at "+
			"least — the check below is over nothing", len(blocks))
	}
	for _, block := range blocks {
		for _, fault := range flowFaults(block) {
			t.Errorf("README.md %s\nGitHub refuses a workflow file holding this as a syntax error, so the snippet fails "+
				"whoever pastes it", fault)
		}
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
		for _, fault := range flowFaults(commentYAML(repoFile(t, host))) {
			t.Errorf("%s %s\nGitHub refuses a workflow file holding this as a syntax error, so the usage example "+
				"fails whoever pastes it", host, fault)
		}
	}
}
