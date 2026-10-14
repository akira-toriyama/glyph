package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/config"
	"github.com/akira-toriyama/glyph/v5/internal/core"
	"github.com/akira-toriyama/glyph/v5/internal/gitsource"
	"github.com/akira-toriyama/glyph/v5/internal/notes"
	"github.com/spf13/cobra"
)

// notesResult is the machine verdict: {sections, reason} — plus packages
// when the repository declares [[packages]], and then sections is EMPTY:
// each line renders its own body (DESIGN §4.1), and the top-level sections
// describe no one line, exactly as bump's scalars describe no one line.
// reason appears only on an empty verdict — it explains why nothing rendered.
type notesResult struct {
	Sections []notes.Section `json:"sections"`
	Packages []packageNotes  `json:"packages,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

// packageNotes is one line's rendered notes inside notesResult.packages.
type packageNotes struct {
	Path     string          `json:"path"`
	Sections []notes.Section `json:"sections"`
}

// notesOptions is what one notes run was asked, snapshotted at RunE: the
// input source and --json. Nothing below RunE reads a flag — notesRun is a
// function of these alone (TestNotesRunsFromOptionsWithoutCobra).
type notesOptions struct {
	source
	json bool
}

func newNotesCmd() *cobra.Command {
	var f sourceFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "Render section-grouped release notes from a range of commits",
		Long: "notes renders the release-notes body under the repository's glyph.toml:\n" +
			"[[note.sections]] order is render order, each section filters on one axis\n" +
			"(semver or author), a commit lands in EVERY section whose filter matches\n" +
			"it, and each line renders through the note.line template ($xxx reads the\n" +
			"winning pattern's named groups; $pr / $author / $hash / $coauthors are\n" +
			"built in, and a [[note.trailers]] entry adds its own name; a\n" +
			"commit no pattern matches, or an unlandable one claims, renders its raw\n" +
			"first line as $subject).\n" +
			"skip-pattern commits appear nowhere; exclude_authors appear wherever\n" +
			"note.sections says they do — whether a commit is in the notes is the\n" +
			"sections' decision alone (under [[packages]], on the lines its files touch).\n" +
			"There are three input sources, exactly one of which is required.\n" +
			"--range reads a local git revision range; --pr reads a pull request's\n" +
			"INDIVIDUAL commits over the API, so a squash-merge cannot collapse them\n" +
			"into one line; --since-tag walks main's merge points since a tag and\n" +
			"expands each back into the pull it merged (the release-time source).\n" +
			"stdout is the Markdown body\n" +
			"(pipe it into a release step); a --since-tag body closes with the range's\n" +
			"compare link (compare/<base tag>...<HEAD sha>; none when the walk has no\n" +
			"tag base, and none from --range or --pr). --json emits {sections,reason},\n" +
			"unchanged — no link. Nothing release-worthy prints no body and exits 1\n" +
			"(soft no-release).\n\n" +
			"On a repository declaring [[packages]] the body is per line: stdout is one\n" +
			"body per line under a `# <path>` heading (bare when a tag selects one\n" +
			"line), each --since-tag body closed by its own line's compare link (none\n" +
			"for a line with no tag); --json carries\n" +
			"packages: [{path,sections}] with the top-level\n" +
			"sections EMPTY. --pr is refused there (exit 2: a pull's listing carries\n" +
			"messages and no files, so nothing can be attributed to a line).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
				return err
			}
			return notesRun(cmd.Context(), notesOptions{source: sourceOf(cmd, f), json: asJSON})
		},
	}
	cmd.Flags().StringVar(&f.rng, "range", "", "render notes for every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&f.pull, "pr", 0, "render notes from a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &f.tag, "render notes from")
	cmd.Flags().StringVar(&f.repo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine verdict {sections,reason}; with [[packages]] declared sections is empty and packages[] carries one body per line")
	markInputSourceFlags(cmd)
	return cmd
}

// notesLinkEnds is what a notes compare link needs beside its base: the
// repository the walk queried — resolveRepo again, the answer the walk got —
// and HEAD, the commit the walk read up to.
func notesLinkEnds(ctx context.Context, repoFlag string) (owner, repo, head string, err error) {
	if owner, repo, err = resolveRepo(ctx, repoFlag); err != nil {
		return "", "", "", err
	}
	head, err = gitsource.Head(ctx, ".")
	return owner, repo, head, err
}

// notesRun is notes below its flags: the one walk result whatever the source
// (readSource), one body per line, and the render. A repository without
// [[packages]] walks as one bare line, so the per-line loop is the only loop
// and the render alone tells the shapes apart: the bare line's sections are
// --json's top level and its body stands alone on stdout, closed by the
// compare link when the walk has a tag base — the body goreleaser.yml
// publishes through --release-notes verbatim, so it closes with the link
// release's drafts carry. With packages declared (DESIGN §4.1) --json's
// sections is EMPTY and packages[] carries one body per line; stdout is the
// one line's body when one line is selected (the tag-time rendering
// goreleaser.yml performs, where a heading would be noise), and with several
// lines each body sits under a `# <path>` heading, the sections keeping
// their `##` below it, in config order, lines with nothing to say omitted.
// Exit 1 only when no line lands anything in a section.
func notesRun(ctx context.Context, o notesOptions) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	w, err := readSource(ctx, cfg, o.source)
	if err != nil {
		return err
	}
	// lineBody is one line that has something to say: its path, its compare
	// link's base, and its rendered notes.
	type lineBody struct{ path, base, notes string }
	var pkgs []packageNotes
	var said []lineBody
	for _, lw := range w.Lines {
		sections, gerr := notes.Group(walkedNoteCommits(lw.Commits), cfg)
		if gerr != nil {
			return gerr
		}
		if sections == nil {
			sections = []notes.Section{}
		}
		pkgs = append(pkgs, packageNotes{Path: lw.Package.Path, Sections: sections})
		if len(sections) == 0 {
			continue
		}
		said = append(said, lineBody{path: lw.Package.Path, base: lw.BaseTag, notes: notes.Render(sections)})
	}
	if len(said) == 0 {
		reason := noNotesReason(cfg, participating(cfg, walkedNoteCommits(w.All)), w.Source)
		if o.json {
			printCompact(notesVerdict(cfg, pkgs, reason))
		}
		return core.NoReleasef("%s", reason).Quiet(o.json)
	}
	if o.json {
		printCompact(notesVerdict(cfg, pkgs, ""))
		return nil
	}
	var owner, repo, head string
	if slices.ContainsFunc(said, func(b lineBody) bool { return b.base != "" }) {
		if owner, repo, head, err = notesLinkEnds(ctx, o.repo); err != nil {
			return err
		}
	}
	bodies := make([]string, 0, len(said))
	for _, b := range said {
		// Each line's notes close with that line's own link (§4.1), under its
		// heading when there is more than one line to head.
		body := compareLink(b.notes, owner, repo, b.base, head)
		if len(w.Lines) > 1 {
			body = "# " + b.path + "\n\n" + body
		}
		bodies = append(bodies, body)
	}
	fmt.Fprint(out, strings.Join(bodies, "\n"))
	return nil
}

// notesVerdict is the --json shape (notesResult): with [[packages]] declared
// sections is EMPTY and packages[] carries one body per line; without, the
// bare line's sections are the top level and packages is absent. reason
// rides only on an empty verdict.
func notesVerdict(cfg *config.Config, pkgs []packageNotes, reason string) notesResult {
	if len(cfg.Packages) == 0 {
		return notesResult{Sections: pkgs[0].Sections, Reason: reason}
	}
	return notesResult{Sections: []notes.Section{}, Packages: pkgs, Reason: reason}
}

// noNotesReason is the empty verdict's reason line: the count is bump's
// participating set in bump's words (participating), the source the walk's
// own name, and "on any line" is said exactly when there are lines to say
// it of.
func noNotesReason(cfg *config.Config, participating int, source string) string {
	s := fmt.Sprintf("no release notes: %d commit(s) participate in %s and none lands in a section", participating, source)
	if len(cfg.Packages) > 0 {
		s += " on any line"
	}
	return s
}
