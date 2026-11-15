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
// `git log` at 4 as `^v0.1.0..HEAD` (measured 2026-11-08; mutation row
// since-tag-exclusion-reaches-git); the shape check refuses it as not a
// single revision, with the range shape's remedy.
func TestCheckTagRefusesANegatedRevision(t *testing.T) {
	for _, tag := range []string{"^v0.1.0", " ^v0.1.0", "^HEAD"} {
		err := CheckTag(tag)
		ce := core.AsError(err)
		if ce == nil || ce.Code != core.CodeUsage || !strings.Contains(ce.Msg, "not a single revision") {
			t.Errorf("CheckTag(%q) = %v, want CodeUsage naming the value as not a single revision", tag, err)
		}
	}
}

// TestCheckRangeOperatorsRefusesAnOperatorOnASide: git reads its set
// operators — a leading ^, the ^!, ^@ and ^-<n> suffixes — on a single
// revision only; on a side of `..` or `...` each died in `git log` at 4
// (every combination measured on git 2.39.5, 2.54 and 2.56, 2026-11-15), so
// the shape is the caller's input and the refusal names the side. Everything
// git reads is let through: the operators on a single revision, a parent or
// peel suffix on a side, an omitted side.
func TestCheckRangeOperatorsRefusesAnOperatorOnASide(t *testing.T) {
	for rng, side := range map[string]string{
		"^v0.1.0..HEAD":     "^v0.1.0",
		"v0.1.0..^HEAD":     "^HEAD",
		"v0.1.0..HEAD^!":    "HEAD^!",
		"v0.1.0^!..HEAD":    "v0.1.0^!",
		"HEAD^@..HEAD":      "HEAD^@",
		"v0.1.0^-..HEAD":    "v0.1.0^-",
		"v0.1.0..HEAD^-1":   "HEAD^-1",
		"v0.1.0...HEAD^!":   "HEAD^!",
		" ^v0.1.0...HEAD\t": "^v0.1.0",
		"..HEAD^!":          "HEAD^!",
	} {
		err := CheckRangeOperators(rng)
		ce := core.AsError(err)
		if ce == nil || ce.Code != core.CodeUsage || !strings.Contains(ce.Msg, `"`+side+`"`) {
			t.Errorf("CheckRangeOperators(%q) = %v, want CodeUsage naming the side %q", rng, err, side)
		}
	}
	for _, rng := range []string{"v0.1.0..HEAD", "v0.1.0...HEAD", "v0.1.0..", "..HEAD", "HEAD^!", "^v0.1.0", "HEAD^@", "HEAD^-", "HEAD^-2", "v0.1.0^..HEAD^", "v0.1.0^2..HEAD~1", "v0.1.0^{commit}..HEAD^{}", "HEAD", ":/fix a ^! crash"} {
		if err := CheckRangeOperators(rng); err != nil {
			t.Errorf("CheckRangeOperators(%q) = %v, want nil — git reads this range", rng, err)
		}
	}
}
