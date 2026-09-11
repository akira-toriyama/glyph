package cli

import (
	"fmt"

	"github.com/akira-toriyama/glyph/v3/internal/emoji"
	"github.com/spf13/cobra"
)

func newEmojiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "emoji",
		Short: "Print the gemoji dictionary: which shortcode each kind of change is written as",
		Long: "emoji prints glyph's dictionary of GitHub emoji shortcodes as a JSON array —\n" +
			"for each kind of change, the one code to write at the head of the subject,\n" +
			"the emoji GitHub renders it as, the kind's name, when to pick it over its\n" +
			"neighbours, and the gitmoji codes whose meaning it absorbed. The array is\n" +
			"ORDERED: read top to bottom and stop at the first description that fits,\n" +
			"exactly as the repository's [[patterns]] resolve a message.\n\n" +
			"It is advice, not grammar. lint never reads it — any `:name:` the\n" +
			"repository's pattern accepts is legal, listed or not — and the sigil beside\n" +
			"the code is what decides the version; the dictionary carries no semver\n" +
			"field on purpose. Like `version`, it reaches nothing: no git, no API, no\n" +
			"glyph.toml, so it answers anywhere.\n\n" +
			"The output is the shipped file byte for byte, so the same bytes are at\n" +
			"internal/emoji/table.json in the glyph repository for a session with no\n" +
			"binary at hand. Search it with jq or fzf rather than with flags — there are\n" +
			"none.",
		Example: "  glyph emoji | jq -r '.[] | \"\\(.code)\\t\\(.emoji)  \\(.name) — \\(.description)\"' | fzf\n" +
			"  glyph emoji | jq -r '.[] | select(.absorbs[]? == \":green_heart:\") | .code'   # what replaced a gitmoji code",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(out, string(emoji.JSON()))
			return nil
		},
	}
}
