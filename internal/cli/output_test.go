package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/core"
)

// TestStderrCarriesAnnotationsThenOneSievableEnvelope pins the stream contract
// stated at the top of output.go, from the consumer's side.
//
// stderr carries two shapes at once — `::`-prefixed workflow commands and the
// error envelope — and jq over the two together is a parse error (exit 5). The
// fleet's reusables sieve the envelope out with `sed -n '/^[{]/,$p'`, which is
// only correct while (a) nothing but annotations precedes the first line opening
// with `{`, and (b) everything from there to EOF is one JSON document. Neither
// is expressible in the workflow YAML, so it is asserted here.
//
// The message itself is asserted DECODED, never as raw bytes: marshal escapes a
// newline either way, so the bytes looked fine while the value a consumer
// interpolated into `::error::` was multi-line — the runner parses a workflow
// command up to the first newline, so the part the annotation existed to deliver
// was the part it dropped (t-sws7).
func TestStderrCarriesAnnotationsThenOneSievableEnvelope(t *testing.T) {
	// A proxy page is the realistic multi-line source: internal/github's distill
	// carries a non-JSON error body through verbatim, bounded only in length.
	const proxyPage = "<html>\n<head><title>403 Forbidden</title></head>\n<body>\nblocked by the proxy\n</body>\n</html>"

	cases := []struct {
		name    string
		emit    func()
		wantMsg string
	}{
		{
			name: "a warning precedes the envelope",
			emit: func() {
				warnf("no v* release tag here — previewing this PR's own verdict only")
				renderError(core.APIf("github: GET /repos/o/r/pulls/1/commits: 500 internal error"))
			},
			wantMsg: "github: GET /repos/o/r/pulls/1/commits: 500 internal error",
		},
		{
			name: "a notice precedes the envelope",
			emit: func() {
				noticef("dry run: the upsert would create the rolling draft v1.2.0")
				renderError(core.Lintf("2 commit-convention violation(s)"))
			},
			wantMsg: "2 commit-convention violation(s)",
		},
		{
			name: "a remote body arrives as a multi-line page",
			emit: func() {
				warnf("pull request #1 returned 250 commits — GitHub truncates this listing at 250")
				renderError(core.APIf("github: GET /repos/o/r: 403 %s", proxyPage))
			},
			wantMsg: "github: GET /repos/o/r: 403 <html> <head><title>403 Forbidden</title></head> <body> blocked by the proxy </body> </html>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := captureErr(t)
			tc.emit()

			lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
			cut := -1
			for i, l := range lines {
				if strings.HasPrefix(l, "{") {
					cut = i
					break
				}
			}
			if cut < 0 {
				t.Fatalf("no line opens the envelope, so `sed -n '/^[{]/,$p'` recovers nothing:\n%s", stderr.String())
			}
			for _, l := range lines[:cut] {
				if !strings.HasPrefix(l, "::") {
					t.Fatalf("stderr line %q before the envelope is not a workflow command; the sieve "+
						"would hand jq a fragment it cannot parse:\n%s", l, stderr.String())
				}
			}

			env := decodeErrorEnvelope(t, strings.Join(lines[cut:], "\n"))
			if env.Message != tc.wantMsg {
				t.Errorf("the sieved envelope's message is\n  %q\nwant\n  %q\n(it is interpolated "+
					"into a one-line ::error::, which the runner truncates at the first newline)",
					env.Message, tc.wantMsg)
			}
		})
	}
}
