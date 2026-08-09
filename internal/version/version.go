// Package version carries the build version, overridden at release time via
// -ldflags "-X github.com/akira-toriyama/glyph/internal/version.Version=...".
// Default "dev" means a local/source build (see .goreleaser.yaml). It holds no
// logic beyond rendering the build identity.
package version

import "runtime/debug"

// These are injected by the linker at release/build time. Do not set them here.
// "dev" is the only value a plain `go build` (no ldflags) should ever show.
var (
	Version = "dev" // release tag, e.g. v0.1.0
	Commit  = ""    // git SHA (goreleaser {{.Commit}})
	Date    = ""    // build timestamp (goreleaser {{.Date}})
)

// Info is the resolved build identity, with debug.ReadBuildInfo() filling the
// commit/date when a bare `go build`/`go install` left the ldflags unset.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

// Resolve returns the build identity. When the linker stamped nothing (a bare
// `go install`/`go build`), it falls back to what the Go toolchain embedded in
// the binary — so even un-stamped builds are identifiable.
func Resolve() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date}
	if info.Commit != "" && info.Date != "" {
		return info
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	return resolve(info, bi)
}

// resolve merges the toolchain's embedded identity into the linker's. Split
// from Resolve because ReadBuildInfo answers for the running binary — the
// go-install shape (a module version, no VCS stamps) can only be exercised
// through a seam.
func resolve(info Info, bi *debug.BuildInfo) Info {
	// A module-mode `go install pkg@version` builds from the module zip: no
	// VCS stamps exist, but the toolchain records the module version itself.
	// Surface it, so the binary a user just installed can say which release
	// it is — it answered only "dev" before, indistinguishable from a source
	// build. "(devel)" is a directory build and keeps the "dev" sentinel.
	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		}
	}
	return info
}

// String renders the build identity as a single human line, e.g.
// "v0.1.0 (abc1234, 2026-07-03T00:00:00Z)".
func (i Info) String() string {
	s := i.Version
	switch {
	case i.Commit != "" && i.Date != "":
		s += " (" + short(i.Commit) + ", " + i.Date + ")"
	case i.Commit != "":
		s += " (" + short(i.Commit) + ")"
	}
	return s
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
