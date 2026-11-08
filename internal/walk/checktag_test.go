package walk

import (
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
