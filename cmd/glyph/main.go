// Command glyph is a gitmoji-driven release engine: it lints commit messages,
// computes the semantic-version bump, and renders release notes from the
// individual commits inside a pull request — so squash-merge (which rewrites the
// squash subject to the PR title) never erases the per-commit types.
//
// All logic lives in internal/{cli,parser,bump,notes,gitmoji,github,core,version};
// main only maps the CLI's resolved exit code to the process. See README.md.
package main

import (
	"os"

	"github.com/akira-toriyama/glyph/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
