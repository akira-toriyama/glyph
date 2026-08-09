package version

import (
	"runtime/debug"
	"testing"
)

func TestInfoString(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{"version only", Info{Version: "dev"}, "dev"},
		{"commit is shortened to 7", Info{Version: "v1.0.0", Commit: "abc1234def", Date: "2026-07-11"}, "v1.0.0 (abc1234, 2026-07-11)"},
		{"commit without date", Info{Version: "v1.2.0", Commit: "abcdef1"}, "v1.2.0 (abcdef1)"},
		{"short commit not truncated", Info{Version: "v1", Commit: "abcd"}, "v1 (abcd)"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%s: String() = %q, want %q", c.name, got, c.want)
		}
	}
}

// Resolve defaults to the "dev" sentinel when the linker did not stamp a
// release version (a plain `go build`/`go test`).
func TestResolveDefaultsToDev(t *testing.T) {
	if got := Resolve().Version; got != "dev" {
		t.Errorf("Resolve().Version = %q, want %q (unstamped build)", got, "dev")
	}
}

// resolve is exercised through its seam: ReadBuildInfo answers for the TEST
// binary, so the go-install shape — a module version recorded, no VCS stamps —
// cannot be arranged for Resolve itself. Measured before the fix: a
// `go install …/cmd/glyph@latest` binary answered `glyph dev`, indistinguishable
// from a source build, so the one thing a fresh install would ask ("did I get
// the release I meant?") had no answer.
func TestResolveSurfacesTheModuleVersion(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		bi   *debug.BuildInfo
		want Info
	}{
		{
			"go install: the module version becomes the answer",
			Info{Version: "dev"},
			&debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}},
			Info{Version: "v1.0.0"},
		},
		{
			"directory build stays dev; VCS stamps still fill in",
			Info{Version: "dev"},
			&debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc1234def"},
					{Key: "vcs.time", Value: "2026-08-09T00:00:00Z"},
				},
			},
			Info{Version: "dev", Commit: "abc1234def", Date: "2026-08-09T00:00:00Z"},
		},
		{
			"a linker-stamped version is never overwritten",
			Info{Version: "v9.9.9", Commit: "stamped"},
			&debug.BuildInfo{
				Main:     debug.Module{Version: "v1.0.0"},
				Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "t"}},
			},
			Info{Version: "v9.9.9", Commit: "stamped", Date: "t"},
		},
	}
	for _, c := range cases {
		if got := resolve(c.in, c.bi); got != c.want {
			t.Errorf("%s: resolve() = %+v, want %+v", c.name, got, c.want)
		}
	}
}
