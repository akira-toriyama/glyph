package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/internal/config"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitsource"
	"github.com/akira-toriyama/glyph/internal/notes"
	"github.com/spf13/cobra"
)

var (
	notesRange    string
	notesPR       int
	notesSinceTag string
	notesRepo     string
	notesJSON     bool
)

// notesResult is the machine verdict: {sections, reason} — plus packages
// when the repository declares [[packages]], and then sections is EMPTY:
// each line renders its own body (DESIGN §4.1), and the top-level sections
// describe no one line, exactly as bump's scalars describe no one line.
// reason appears only on an empty verdict — it explains why nothing rendered.
type notesResult struct {
	Sections []notes.SigilSection `json:"sections"`
	Packages []packageNotes       `json:"packages,omitempty"`
	Reason   string               `json:"reason,omitempty"`
}

// packageNotes is one line's rendered notes inside notesResult.packages.
type packageNotes struct {
	Path     string               `json:"path"`
	Sections []notes.SigilSection `json:"sections"`
}

func newNotesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "Render section-grouped release notes from a range of commits",
		Long: "notes renders the release-notes body under the repository's glyph.toml:\n" +
			"[[note.sections]] order is render order, each section filters on one axis\n" +
			"(semver or author), a commit lands in EVERY section whose filter matches\n" +
			"it, and each line renders through the note.line template ($xxx reads the\n" +
			"winning pattern's named groups; $pr / $author / $hash are built in; a\n" +
			"commit no pattern matches renders its raw first line as $subject).\n" +
			"skip-pattern commits appear nowhere; exclude_authors appear wherever\n" +
			"note.sections says they do — whether a commit is in the notes is the\n" +
			"sections' decision alone.\n" +
			"There are three input sources, exactly one of which is required.\n" +
			"--range reads a local git revision range; --pr reads a pull request's\n" +
			"INDIVIDUAL commits over the API, so a squash-merge cannot collapse them\n" +
			"into one line; --since-tag walks main's merge points since a tag and\n" +
			"expands each back into the pull it merged (the release-time source).\n" +
			"stdout is the Markdown body\n" +
			"(pipe it into a release step); --json emits {sections,reason}. Nothing\n" +
			"release-worthy prints no body and exits 1 (soft no-release).",
		Args: sinceTagArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notesRun(cmd)
		},
	}
	cmd.Flags().StringVar(&notesRange, "range", "", "render notes for every commit in a git revision range (BASE..HEAD)")
	cmd.Flags().IntVar(&notesPR, "pr", 0, "render notes from a pull request's individual (pre-squash) commits, read over the API")
	addSinceTagFlag(cmd, &notesSinceTag, "render notes from")
	cmd.Flags().StringVar(&notesRepo, "repo", "", "owner/name to query for --pr and --since-tag (default: $GITHUB_REPOSITORY, else the origin remote)")
	cmd.Flags().BoolVar(&notesJSON, "json", false, "emit the machine verdict {sections,reason}")
	markInputSourceFlags(cmd)
	return cmd
}

// notesInput reads the commits the notes are rendered from — with the citation
// each source can attest: the walk's per-commit pull, the --pr flag's own
// number, the bare sha for a local range — and names the source for the reason
// line. The notes twin of bumpInput, dispatching on whether a flag was set
// rather than on its value.
func notesInput(cmd *cobra.Command, cfg *config.Config) ([]notes.SigilCommit, string, error) {
	ctx := cmd.Context()
	if cmd.Flags().Changed("pr") {
		raws, source, err := pullInput(ctx, notesPR, notesRepo)
		return noteCommits(raws, notesPR), source, err
	}
	if cmd.Flags().Changed("since-tag") {
		// The version base is bump's concern; the walk's facts are discarded for
		// the reason spelled out at bump's own call site — notes REPORTS, and an
		// incomplete walk already warns per cause on stderr. Nothing here writes
		// back, so there is no irreversible act to gate.
		w, err := sinceTagInput(ctx, cfg, notesSinceTag, notesRepo)
		return walkedNoteCommits(w.All), w.Source, err
	}
	if err := checkRangeFlag(notesRange); err != nil {
		return nil, "", err
	}
	raws, err := gitsource.Log(ctx, ".", notesRange)
	return noteCommits(raws, 0), notesRange, err
}

// notesLines is notes for a repository that declares [[packages]]: one body
// per line, each grouped over the commits that participate on that line.
// --pr is refused for the reason bump refuses it. stdout is the one line's
// body when one line is selected (the tag-time rendering goreleaser.yml
// performs, where a heading would be noise); with several lines each body
// sits under a `# <path>` heading, the sections keeping their `##` below it,
// in config order, lines with nothing to say omitted. Exit 1 only when no
// line lands anything in a section.
func notesLines(cmd *cobra.Command, cfg *config.Config) error {
	ctx := cmd.Context()
	var w sinceTagWalk
	var err error
	switch {
	case cmd.Flags().Changed("pr"):
		return refusePullSource(cfg)
	case cmd.Flags().Changed("since-tag"):
		w, err = sinceTagInput(ctx, cfg, notesSinceTag, notesRepo)
	default:
		if err = checkRangeFlag(notesRange); err != nil {
			return err
		}
		w, err = rangeLines(ctx, cfg, notesRange)
	}
	if err != nil {
		return err
	}
	var pkgs []packageNotes
	var bodies []string
	for _, lw := range w.Lines {
		sections, gerr := notes.GroupSigils(walkedNoteCommits(lw.Commits), cfg)
		if gerr != nil {
			return gerr
		}
		if sections == nil {
			sections = []notes.SigilSection{}
		}
		pkgs = append(pkgs, packageNotes{Path: lw.Package.Path, Sections: sections})
		if len(sections) == 0 {
			continue
		}
		body := notes.RenderSigils(sections)
		if len(w.Lines) > 1 {
			body = "# " + lw.Package.Path + "\n\n" + body
		}
		bodies = append(bodies, body)
	}
	if len(bodies) == 0 {
		reason := fmt.Sprintf("no release notes: %d commit(s) participate in %s and none lands in a section on any line", len(w.All), w.Source)
		if notesJSON {
			printCompact(notesResult{Sections: []notes.SigilSection{}, Packages: pkgs, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}
	if notesJSON {
		printCompact(notesResult{Sections: []notes.SigilSection{}, Packages: pkgs})
		return nil
	}
	fmt.Fprint(out, strings.Join(bodies, "\n"))
	return nil
}

func notesRun(cmd *cobra.Command) error {
	if err := checkNamingFlags(cmd, [][3]string{{"repo", "repository", repoHint}}); err != nil {
		return err
	}
	cfg, err := loadConfig(cmd.Context())
	if err != nil {
		return err
	}
	if len(cfg.Packages) > 0 {
		return notesLines(cmd, cfg)
	}
	commits, source, perr := notesInput(cmd, cfg)
	if perr != nil {
		return perr
	}

	sections, nerr := notes.GroupSigils(commits, cfg)
	if nerr != nil {
		return nerr
	}

	if len(sections) == 0 {
		reason := fmt.Sprintf("no release notes: %d commit(s) participate in %s and none lands in a section", len(commits), source)
		if notesJSON {
			printCompact(notesResult{Sections: []notes.SigilSection{}, Reason: reason})
			return &core.Error{Code: core.CodeNoRelease, Msg: reason, Silent: true}
		}
		return core.NoReleasef("%s", reason)
	}

	if notesJSON {
		printCompact(notesResult{Sections: sections})
		return nil
	}
	fmt.Fprint(out, notes.RenderSigils(sections))
	return nil
}
