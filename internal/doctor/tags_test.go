package doctor

import (
	"errors"
	"strings"
	"testing"
)

const noRootPackages = minimalConfig + "\n[[packages]]\npath = 'haiku'\n\n[[packages]]\npath = 'curry'\n"

// TestRootLineTagsOrphanedIsAdvice pins the severity: bare tags with no root
// package declared are ADVICE naming the adopting declaration — never a
// failure (leaving history alone is legitimate) and never a pass (the author
// should learn, once, that those tags baseline nothing now).
func TestRootLineTagsOrphanedIsAdvice(t *testing.T) {
	c := checkRootLineTags(loaded(t, noRootPackages), []string{"v0.1.0", "haiku/v1.0.0", "v0.2.0", "not-a-version"}, nil)
	if c.Status != StatusAdvice {
		t.Fatalf("%s = %s (%s), want %s", IDRootLineTags, c.Status, c.Observed, StatusAdvice)
	}
	if !strings.Contains(c.Observed, "2 bare vX.Y.Z tag(s)") || !strings.Contains(c.Observed, "highest v0.2.0") {
		t.Errorf("the observation must count the bare tags and name the highest, got %q", c.Observed)
	}
	if !strings.Contains(c.Fix, `path = "."`) {
		t.Errorf("the fix must hand over the root package declaration, got %q", c.Fix)
	}
}

func TestRootLineTagsPasses(t *testing.T) {
	cases := map[string]Check{
		"no packages":        checkRootLineTags(loaded(t, minimalConfig), []string{"v0.1.0"}, nil),
		"root declared":      checkRootLineTags(loaded(t, noRootPackages+"\n[[packages]]\npath = '.'\nname = 'core'\n"), []string{"v0.1.0"}, nil),
		"no bare tag":        checkRootLineTags(loaded(t, noRootPackages), []string{"haiku/v1.0.0", "curry/v0.3.0"}, nil),
		"no tags at all":     checkRootLineTags(loaded(t, noRootPackages), nil, nil),
		"root declared, err": checkRootLineTags(loaded(t, noRootPackages+"\n[[packages]]\npath = '.'\n"), nil, errors.New("git tag: boom")),
		// A root-level major version subdirectory holds the bare v2.* tags
		// (DESIGN §4.1): they are its line, not residue.
		"root v2 declared": checkRootLineTags(loaded(t, noRootPackages+"\n[[packages]]\npath = 'v2'\n"), []string{"v2.0.0", "v2.1.0"}, nil),
	}
	for name, c := range cases {
		if c.Status != StatusPass {
			t.Errorf("%s: %s = %s (%s), want pass", name, IDRootLineTags, c.Status, c.Observed)
		}
	}
}

// TestRootLineTagsDegradeWithUnobservedInputs: an unloaded config or an
// unlistable tag set is unknown — never a pass over evidence never collected.
// The config is loaded once, by the caller, so "not loaded" is the one state
// this check sees whatever the reason (no top level, a missing or invalid
// file), and its observation points at the check that owns the reason.
func TestRootLineTagsDegradeWithUnobservedInputs(t *testing.T) {
	unloaded := checkRootLineTags(nil, []string{"v0.1.0"}, nil)
	if unloaded.Status != StatusUnknown || !strings.Contains(unloaded.Observed, IDConfigLoads) {
		t.Errorf("config not loaded: %s = %s (%s), want unknown pointing at %s", IDRootLineTags, unloaded.Status, unloaded.Observed, IDConfigLoads)
	}
	unlisted := checkRootLineTags(loaded(t, noRootPackages), nil, errors.New("git tag: boom"))
	if unlisted.Status != StatusUnknown || !strings.Contains(unlisted.Observed, "git tag: boom") {
		t.Errorf("tags unlisted: %s = %s (%s), want unknown carrying git's reason", IDRootLineTags, unlisted.Status, unlisted.Observed)
	}
}

// TestRootLineTagsRootMajorSubdirectoryHoldsItsMajorOnly: with only a
// root-level v2 declared, bare v1.* tags are still nobody's — counted, and
// the v2.* ones are not.
func TestRootLineTagsRootMajorSubdirectoryHoldsItsMajorOnly(t *testing.T) {
	c := checkRootLineTags(loaded(t, noRootPackages+"\n[[packages]]\npath = 'v2'\n"), []string{"v1.0.0", "v2.0.0", "v1.1.0"}, nil)
	if c.Status != StatusAdvice || !strings.Contains(c.Observed, "2 bare vX.Y.Z tag(s)") || !strings.Contains(c.Observed, "highest v1.1.0") {
		t.Fatalf("%s = %s (%s), want advice over the two v1 tags", IDRootLineTags, c.Status, c.Observed)
	}
}
