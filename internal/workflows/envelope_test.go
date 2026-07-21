package workflows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// glyph's stderr carries TWO shapes: `::`-prefixed workflow commands and the
// error envelope. jq over the two together is a parse error (exit 5), so a
// consumer must sieve the envelope out first — everything from the first line
// opening with `{`, which is where renderError's document starts and, since it
// is written last, where it runs to EOF.
//
// The sieve is this literal. Both shipped consumers hid jq's failure behind
// `|| true` / `>/dev/null 2>&1`, so the broken form was not merely wrong, it was
// SILENT: the ::error:: heading a human needed simply never appeared (t-sws7).
const envelopeSieve = `sed -n '/^[{]/,$p'`

// stderrSink captures where a step parked glyph's stderr — `2>FILE` in any
// spacing, quoted or not. `2>&1` and `2>/dev/null` are not files a later command
// can read, and are dropped.
var stderrSink = regexp.MustCompile(`2>\s*("?[^"\s|;&]+"?)`)

// mergedIntoJQ matches the other way that stream reaches jq: merged into stdout
// and piped straight in (`glyph … 2>&1 | jq`), with no file to name. A bare
// `2>&1` is not enough to flag — `jq … file >/dev/null 2>&1` merely silences
// jq's own diagnostics, which these steps do deliberately.
var mergedIntoJQ = regexp.MustCompile(`2>&1[^|]*\|[^|]*jq`)

// TestReusablesSieveTheEnvelopeBeforeJQ guards the consumer half of the stream
// contract.
//
// The rule is deliberately blunt: once a step has parked glyph's stderr in a
// file, that file may appear in exactly three places — the `2>` that creates it,
// a `cat … >&2` that replays it into the log, and the sieve. Anywhere else, some
// command is reading a stream that is not JSON.
//
// Blunt beats clever here, and that is measured rather than assumed: two
// earlier versions of this guard tried to identify the jq command itself
// (matching `jq …*.err`, then splitting the body into shell commands), and each
// missed real defects — a jq whose PROGRAM spans two lines, a `cat … | jq`, a
// `jq … < FILE`, an `ERR=FILE` indirection. This rule catches all of those
// because it never has to work out which command is reading the file, only that
// something other than the three sanctioned forms names it. Its failure mode is
// a false positive on a legitimate fourth use — a conversation in review, not a
// silent hole in a fleet-distributed gate.
func TestReusablesSieveTheEnvelopeBeforeJQ(t *testing.T) {
	for _, name := range workflowFiles(t) {
		t.Run(name, func(t *testing.T) {
			body := code(repoFile(t, filepath.Join(".github", "workflows", name)))

			var sinks []string
			for _, m := range stderrSink.FindAllStringSubmatch(body, -1) {
				if sink := strings.Trim(m[1], `"`); sink != "&1" && sink != "/dev/null" {
					sinks = append(sinks, sink)
				}
			}

			for line := range strings.SplitSeq(body, "\n") {
				if strings.Contains(line, envelopeSieve) {
					continue
				}
				for _, sink := range sinks {
					if !strings.Contains(line, sink) {
						continue
					}
					// The redirect that creates it, and the replay into the log.
					if regexp.MustCompile(`2>\s*"?`+regexp.QuoteMeta(sink)).MatchString(line) ||
						(strings.Contains(line, "cat ") && strings.Contains(line, ">&2")) {
						continue
					}
					t.Errorf("%s reads glyph's raw stderr (%s) outside the sanctioned three uses:\n  %s\n"+
						"That stream carries ::warning:: / ::notice:: annotations as well as the error "+
						"envelope, so jq over it exits 5 and — behind the `|| true` these steps use — the "+
						"annotation it was meant to print vanishes in silence. Sieve the envelope out with "+
						"%s first and read the result.",
						name, sink, strings.TrimSpace(line), envelopeSieve)
				}
			}

			if m := mergedIntoJQ.FindString(body); m != "" {
				t.Errorf("%s pipes a 2>&1-merged stream straight into jq:\n  %s\n"+
					"glyph's stderr is annotations plus the envelope; pipe through %s first.",
					name, strings.TrimSpace(m), envelopeSieve)
			}
		})
	}
}
