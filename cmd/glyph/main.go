// Command glyph is a sigil-driven release engine: it lints commit messages
// against the repository's own glyph.toml patterns, folds their version
// sigils into the next semantic version, and renders release notes from the
// individual commits inside a pull request — so squash-merge (which rewrites
// the squash subject to the PR title) never erases the per-commit sigils.
//
// All logic lives under internal/; the package tree and each package's
// contract are docs/DESIGN.md §5, which a test reconciles against the
// directory. main only maps the CLI's resolved exit code to the process.
package main

import (
	"os"

	"github.com/akira-toriyama/glyph/v4/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
