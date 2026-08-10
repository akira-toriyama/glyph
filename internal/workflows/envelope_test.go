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

// detailsRead matches a workflow reading the envelope's details array — the
// signature of a caller rebuilding per-finding annotations out of the machine
// half of the stream.
var detailsRead = regexp.MustCompile(`\.error\.details`)

// TestNoWorkflowRebuildsPerFindingAnnotations guards the division of labour the
// stream contract rests on: the binary that computed a finding is the one that
// renders it. glyph writes one `::error::` per finding onto the diagnostic
// stream itself, and a consumer's whole job is `cat` — replay that stream into
// the log and frame the SUMMARY (`.error.message`, which stays legitimate and
// present in three workflows today).
//
// The reconstruction this forbids was real and its failure was silent: lint.yml
// iterated `.error.details` with jq to mint one annotation per finding, over a
// stream that carries two shapes, behind an `|| true` — and a run that warned
// before it failed emitted NO annotations at all (t-sws7). The verdict was
// computed correctly and then lost in shell on the caller's side of the pin,
// where no test of glyph's could see it. Reading `.error.details` in YAML is
// that defect's first move, whatever it is rebuilt into — so the guard bans the
// read, not the output shape.
func TestNoWorkflowRebuildsPerFindingAnnotations(t *testing.T) {
	// Positive control: the exact line this guard exists to keep out — the jq
	// program lint.yml shipped with until the binary took the annotations over.
	// A regex guard that asserts an absence proves nothing unless the pattern
	// still bites the real instance it was written against.
	const canary = `jq -r '(.error.details // [])[]` + "\n" +
		`         | "::error::\((.sha // "")[0:7]) \(.rule // ""): \(.detail // "")"'`
	if !detailsRead.MatchString(canary) {
		t.Fatalf("detailsRead no longer matches the reconstruction it was written to ban (%q) — "+
			"every assertion below is vacuous", canary)
	}

	for _, name := range workflowFiles(t) {
		t.Run(name, func(t *testing.T) {
			body := code(repoFile(t, filepath.Join(".github", "workflows", name)))
			if m := detailsRead.FindString(body); m != "" {
				t.Errorf("%s reads the envelope's details array (%q) — rebuilding per-finding "+
					"output in YAML is how a whole run's annotations vanished in silence (t-sws7). "+
					"The binary already writes one ::error:: per finding onto the stream this step "+
					"replays with `cat`; frame only .error.message here.", name, m)
			}
		})
	}
}

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
	// Positive control, for the same reason TestNaiveGrepSeesNoPinInTheReusables
	// carries one. Every assertion below is nested inside "for each sink this
	// file has", so a file with no sink asserts NOTHING — and most files have
	// none, which is normal (they never run glyph). If stderrSink stopped
	// matching real redirects, every subtest would go quiet in exactly the way a
	// clean run looks, and this guard on a fleet-distributed contract would be
	// gone with nothing to show for it. So: prove the pattern still bites, and
	// prove the corpus still contains something for it to bite on.
	const canary = `          glyph release --json >"$RESULT" 2>"$ERR" || status=$?`
	if m := stderrSink.FindStringSubmatch(canary); m == nil || strings.Trim(m[1], `"`) != "$ERR" {
		t.Fatalf("stderrSink no longer finds the sink in a real redirect (%q) — every subtest below "+
			"would pass vacuously", canary)
	}

	found := 0
	for _, name := range workflowFiles(t) {
		t.Run(name, func(t *testing.T) {
			body := code(repoFile(t, filepath.Join(".github", "workflows", name)))

			var sinks []string
			for _, m := range stderrSink.FindAllStringSubmatch(body, -1) {
				if sink := strings.Trim(m[1], `"`); sink != "&1" && sink != "/dev/null" {
					sinks = append(sinks, sink)
				}
			}
			found += len(sinks)

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

	// The corpus half of the control. Only the workflows that actually run glyph
	// park its stderr, so most files contributing nothing is expected — but ZERO
	// across all of them means either the redirects were removed (and this guard
	// now protects nothing) or the scan stopped finding them (and it protects
	// nothing while looking healthy). Both are worth a red test.
	if found == 0 {
		t.Errorf("no glyph stderr sink was found in any of .github/workflows — every subtest above "+
			"asserted nothing. Either the reusables stopped parking stderr in a file, or %q stopped "+
			"matching how they do it.", stderrSink)
	}
}
