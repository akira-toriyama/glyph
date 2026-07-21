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
//
// stderr has a SHAPE, and it is part of the contract: every line is either a
// `::`-prefixed workflow command or part of the one error envelope, which
// renderError writes LAST (from finish, once the command has returned — cobra
// is silenced in root.go and every git subprocess writes into a buffer, so
// nothing can print after it). A consumer therefore sieves the envelope out of
// the stream with `sed -n '/^[{]/,$p'` before handing it to jq; jq over the two
// shapes together is a parse error, which is how a shipped workflow's
// `::error::` heading went missing in silence (t-sws7).
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
//
// renderError folds through this same helper, for the same reason one step
// later: a consumer interpolates .error.message straight into `::error::`.
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
// piping stdout to jq is unaffected, and both agents and humans can read it. It
// is the LAST thing written to the stream (see the stream contract above), so a
// consumer recovers it with `sed -n '/^[{]/,$p'` even after annotations.
//
// The message folds through oneLine because a consumer interpolates it straight
// into a `::error::`, and a workflow command is ONE physical line: the runner
// parses up to the first newline and drops the rest. The text is not always
// glyph's own — internal/github's distill carries a non-JSON error body through
// verbatim, and a proxy page behind an enterprise GITHUB_API_URL arrives as a
// dozen lines — so the fold belongs here, at the ONE boundary every arbitrary
// remote string crosses, rather than at each source. JSON-encoding the newlines
// (which marshal does anyway) is not enough and is what made this invisible: the
// bytes on the wire were fine, the DECODED message a consumer used was not.
func renderError(ce *core.Error) {
	type errBody struct {
		Code    int    `json:"code"`
		Msg     string `json:"message"`
		Details any    `json:"details,omitempty"`
	}
	env := struct {
		Error errBody `json:"error"`
	}{Error: errBody{Code: int(ce.Code), Msg: oneLine(ce.Msg), Details: ce.Details}}
	fmt.Fprintln(errOut, string(marshal(env, true)))
}
