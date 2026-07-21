package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akira-toriyama/glyph/internal/core"
)

// stdout carries the payload; stderr carries diagnostics. Everything funnels
// through these two writers (no bare fmt.Print*, no logging lib) so a caller
// piping stdout to jq/grep is never polluted. in is the one input stream
// (`glyph lint --stdin`). All three are overridable in tests.
var (
	out    io.Writer = os.Stdout
	errOut io.Writer = os.Stderr
	in     io.Reader = os.Stdin
)

// printCompact writes v as one JSON line.
func printCompact(v any) {
	fmt.Fprintln(out, string(marshal(v, false)))
}

// marshal renders v deterministically: HTML escaping off, 2-space indent when
// pretty, trailing newline trimmed so on-disk and on-wire bytes match.
func marshal(v any, indent bool) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if indent {
		e.SetIndent("", "  ")
	}
	_ = e.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// warnf emits one GitHub Actions warning annotation (::warning::) to the
// diagnostic stream. It goes to stderr so a piped stdout payload stays pure —
// the Actions runner scans stderr for workflow commands just as it does stdout,
// and outside Actions the line is an ordinary human-readable warning.
func warnf(format string, a ...any) {
	fmt.Fprintln(errOut, "::warning::glyph: "+oneLine(fmt.Sprintf(format, a...)))
}

// noticef emits one GitHub Actions notice annotation (::notice::) to the
// diagnostic stream — the informational sibling of warnf, for outcomes worth
// surfacing in a release job's log that are not warnings (a draft created, a
// stale draft discarded).
func noticef(format string, a ...any) {
	fmt.Fprintln(errOut, "::notice::glyph: "+oneLine(fmt.Sprintf(format, a...)))
}

// oneLine folds an annotation's text onto a single physical line.
//
// A workflow command IS one line: the runner parses `::warning::` up to the
// first newline and drops the rest, so a multi-line interpolation loses exactly
// the part the annotation existed to deliver. The text is not always glyph's own
// — doctor and the release path interpolate a remote error body, and a
// proxy/gateway HTML page (common behind an enterprise GITHUB_API_URL) arrives
// as a dozen lines. Folding here rather than at each call site means no future
// caller has to remember; everywhere else glyph routes arbitrary API text
// through renderError, which JSON-encodes it and has the same property.
//
// The escape GitHub understands for a genuinely multi-line annotation (%0A) is
// deliberately not used: glyph's annotations are read as plain warnings outside
// Actions just as often as inside it, and %0A there is line noise.
func oneLine(s string) string {
	var parts []string
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// renderError prints a structured error to stderr as {"error":{...}} so a caller
// piping stdout to jq is unaffected, and both agents and humans can read it.
func renderError(ce *core.Error) {
	type errBody struct {
		Code    int    `json:"code"`
		Msg     string `json:"message"`
		Details any    `json:"details,omitempty"`
	}
	env := struct {
		Error errBody `json:"error"`
	}{Error: errBody{Code: int(ce.Code), Msg: ce.Msg, Details: ce.Details}}
	fmt.Fprintln(errOut, string(marshal(env, true)))
}
