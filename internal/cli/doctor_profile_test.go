package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/hook"
)

// installProfileHooks writes BOTH of one profile's hooks into the checkout,
// exactly as `glyph hook install --profile=<name>` would.
func installProfileHooks(t *testing.T, profile string) {
	t.Helper()
	if err := os.MkdirAll(".git/hooks", 0o750); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	for _, k := range hook.Kinds(profile) {
		if err := os.WriteFile(".git/hooks/"+k.Name, []byte(k.Script), 0o700); err != nil { // #nosec G306 -- a hook must be executable
			t.Fatalf("install %s: %v", k.Name, err)
		}
	}
}

// TestDoctorFollowsTheProfile pins t-ktap's conclusion in both directions.
//
// bite-exempt: deliberately pins behaviour #175 already wired (Input.Profile
// reaches the hook byte-compare); this change is the ratified §7 conclusion
// and prose neutralization, and the pin is the part that must not rot.
// Every check RUNS under both profiles — the ratified company scope includes
// the walk, so the squash preconditions are not the gitmoji profile's private
// property, and nothing is narrowed away — but the hook comparisons follow
// the run's profile: a conventional repo's hooks legitimately carry the
// --profile flag, and judging them against the default bytes would report
// every correctly installed hook as drift. The cross reading is the sharp
// half: the SAME tree that passes under its own profile must be called stale
// under the other, or doctor is not actually comparing against the profile's
// scripts.
func TestDoctorFollowsTheProfile(t *testing.T) {
	usePR(t, doctorServer(t, apiRepoObject(healthySettings)))
	useDoctorCheckout(t, pinnedCaller)
	installProfileHooks(t, "conventional")
	stubGlyphOnPATH(t, 3)

	code, stdout, stderr := runGlyph(t, "doctor", "--profile=conventional")
	if code != 0 {
		t.Fatalf("doctor --profile=conventional on a conventional repo exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "12 checks: 12 pass") {
		t.Fatalf("a conventional repo under its own profile must pass every check — the count is the claim "+
			"that nothing was narrowed away:\n%s", stdout)
	}

	// The same tree under the DEFAULT profile: the hooks carry glyph's marker
	// with different bytes, which is the stale state — a real drift verdict,
	// because a default-profile repo genuinely must not run conventional-arm
	// hooks. Exit 3: the repository's own configuration violates the model.
	code, stdout, _ = runGlyph(t, "doctor")
	if code != 3 {
		t.Fatalf("doctor (default profile) over conventional hooks exited %d, want 3 — the byte-compare "+
			"is not following the profile\nstdout: %s", code, stdout)
	}
	for _, id := range []string{"commit-msg-hook", "pre-push-hook"} {
		if !strings.Contains(stdout, id) {
			t.Errorf("the failing report does not name %s:\n%s", id, stdout)
		}
	}
}
