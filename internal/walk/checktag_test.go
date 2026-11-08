package walk

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// TestCheckTagJudgesTheTrimmedValue (t-3dfg): the resolvers read the flag
// trimmed (sinceTagRange, resolveLinesScoped), so CheckTag judges the same
// value — ` -x` and a tab-led `-x` are the option shape, ` a..b` the range
// shape, and ` v1.0.0` a tag, exactly as their trimmed spellings are.
// Judged untrimmed, ` -x` passed here and reached `git log` as `-x..HEAD`,
// exit 4 (measured at 2f9428a).
func TestCheckTagJudgesTheTrimmedValue(t *testing.T) {
	for _, tag := range []string{" -x", "\t-x", "-x ", " a..b", "a..b\t", " "} {
		if err := CheckTag(tag); err == nil {
			t.Errorf("CheckTag(%q) = nil, want the usage refusal its trimmed spelling gets", tag)
		} else if ce := core.AsError(err); ce == nil || ce.Code != core.CodeUsage {
			t.Errorf("CheckTag(%q) = %v, want CodeUsage", tag, err)
		}
	}
	for _, tag := range []string{" v1.0.0", "v1.0.0\t", " auto", " below:v1.0.0"} {
		if err := CheckTag(tag); err != nil {
			t.Errorf("CheckTag(%q) = %v, want nil — the trimmed value is well-formed", tag, err)
		}
	}
}

// TestCheckTagRefusesANegatedRevision: a leading ^ is git's exclusion
// operator, not part of a name. `rev-parse --verify` accepts `^v0.1.0` and
// prints `^<sha>`, so the existence question passed it and the walk died in
// `git log` at 4 as `^v0.1.0..HEAD` (measured at 0c177a3); the shape check
// refuses it as not a single revision, with the range shape's remedy.
func TestCheckTagRefusesANegatedRevision(t *testing.T) {
	for _, tag := range []string{"^v0.1.0", " ^v0.1.0", "^HEAD"} {
		err := CheckTag(tag)
		ce := core.AsError(err)
		if ce == nil || ce.Code != core.CodeUsage || !strings.Contains(ce.Msg, "not a single revision") {
			t.Errorf("CheckTag(%q) = %v, want CodeUsage naming the value as not a single revision", tag, err)
		}
	}
}
