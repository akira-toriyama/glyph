package version

import "testing"

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
