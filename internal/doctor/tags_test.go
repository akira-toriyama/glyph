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
	c := checkRootLineTags(configPathWith(t, noRootPackages), nil, []string{"v0.1.0", "haiku/v1.0.0", "v0.2.0", "not-a-version"}, nil)
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
		"no packages":        checkRootLineTags(configPathWith(t, minimalConfig), nil, []string{"v0.1.0"}, nil),
		"root declared":      checkRootLineTags(configPathWith(t, noRootPackages+"\n[[packages]]\npath = '.'\nname = 'core'\n"), nil, []string{"v0.1.0"}, nil),
		"no bare tag":        checkRootLineTags(configPathWith(t, noRootPackages), nil, []string{"haiku/v1.0.0", "curry/v0.3.0"}, nil),
		"no tags at all":     checkRootLineTags(configPathWith(t, noRootPackages), nil, nil, nil),
		"root declared, err": checkRootLineTags(configPathWith(t, noRootPackages+"\n[[packages]]\npath = '.'\n"), nil, nil, errors.New("git tag: boom")),
	}
	for name, c := range cases {
		if c.Status != StatusPass {
			t.Errorf("%s: %s = %s (%s), want pass", name, IDRootLineTags, c.Status, c.Observed)
		}
	}
}

// TestRootLineTagsDegradeWithUnobservedInputs: an unloaded config or an
// unlistable tag set is unknown — never a pass over evidence never collected.
func TestRootLineTagsDegradeWithUnobservedInputs(t *testing.T) {
	cases := map[string]Check{
		"no top level":  checkRootLineTags("", errors.New("not a git repository"), nil, nil),
		"unloadable":    checkRootLineTags(configPathWith(t, "schema = 999\n"), nil, nil, nil),
		"tags unlisted": checkRootLineTags(configPathWith(t, noRootPackages), nil, nil, errors.New("git tag: boom")),
	}
	for name, c := range cases {
		if c.Status != StatusUnknown {
			t.Errorf("%s: %s = %s (%s), want unknown", name, IDRootLineTags, c.Status, c.Observed)
		}
	}
}
