package cleanup

import (
	"strings"
)

// cutLine is git's scissors line, byte for byte: comment char, one space, then
// wt-status.c's `cut_line` (24 dashes, " >8 ", 24 dashes). git writes it above
// the diff under `commit --verbose` and truncates the message at it.
//
// Matched EXACTLY — column 0, that dash count, newline-terminated — because git
// matches it exactly (wt_status_locate_end does strstr on "\n" + this string).
// The loose regex that stood here (`^\s*#\s*-+\s*>8\s*-+\s*$`) cut on a line git
// would have KEPT: an indented one is not even a comment to git, so the text
// below it is recorded, and glyph threw it away — a `NON-BREAKING:` footer
// sitting under such a line is invisible to the hook and present in CI, which is
// `undeclared-removal` refusing a commit CI then accepts. Being exact fails in
// the other direction if a future git changes the line, so the constant is not
// trusted on faith: TestCutLineIsTheOneGitWrites drives a real `git commit -v`
// and asserts git still writes THIS string.
const cutLine = "# ------------------------ >8 ------------------------\n"

// Mode is what git will do to a commit message before recording it —
// the resolved `--cleanup` for ONE commit, not the mode name.
//
// Three fields rather than a five-valued enum because git's own cleanup is three
// orthogonal operations (sequencer.c, cleanup_message: truncate when `verbose ||
// cleanup_mode == SCISSORS`, then strbuf_stripspace with or without the comment
// prefix). A name like "strip" does not decide the truncation on its own, so an
// enum of git's mode names cannot express `--cleanup=strip` under `-v` (truncate)
// versus the same mode under `-m` (do not) — and that pair is exactly where the
// hook and CI used to disagree.
type Mode struct {
	// Space runs git's whitespace cleanup: trailing " \t\r" off every line,
	// an interior run of blank lines collapsed to one, leading and trailing
	// blank lines dropped. False only for `verbatim`.
	Space bool
	// Comments drops whole-line comments — git's `strip`. A comment is a line
	// whose FIRST byte is '#'; git does not look past leading whitespace.
	Comments bool
	// Truncate cuts the message at git's scissors line: always under
	// commit.verbose (git cuts under verbose in every mode, editor or not), in
	// scissors mode whenever an editor ran, and — because `-v` reaches no hook
	// and git writes its cut line and the diff into every verbose editor buffer
	// — whenever an editor ran for `git commit` (Verbose).
	Truncate bool
}

// Verbose is what a commit-msg hook can know of the verbose setting git will
// clean the message under — the one input to the scissors cut besides the mode
// and the editor.
type Verbose int

const (
	// VerboseUnseen is `git commit` with commit.verbose off or unset. `-v` may
	// still be on and nothing a hook reads says so, so the cut is guessed on
	// exactly when an editor runs: under -v git writes the cut line and the
	// diff into the editor's buffer itself, and a cut line in any other message
	// is one the author typed.
	VerboseUnseen Verbose = iota
	// VerboseOn is commit.verbose above 0: git cuts in every mode, with or
	// without an editor.
	VerboseOn
	// VerboseNever is `git merge`, which cleans with verbose 0 (builtin/merge.c)
	// and has no -v: only scissors mode with an editor cuts.
	VerboseNever
)

// ResolveMode answers what git will do to this message from what a commit-msg
// hook actually has: `commit.cleanup` (empty when unset), whether git is going
// to open an editor, and what it can know of verbose. git cuts iff `verbose ||
// cleanup_mode == SCISSORS`, and SCISSORS needs an editor (sequencer.c,
// cleanup_message and get_cleanup_mode — that formula held in every cell of
// internal/cli's TestHookCutMatchesGit).
//
// Measured on git 2.54, from a probe hook that dumped its environment:
//
//	git commit -m / -F / --amend --no-edit   ->  GIT_EDITOR=:
//	git commit (an editor runs)              ->  GIT_EDITOR=<the editor>, or
//	                                             UNSET when core.editor or
//	                                             $EDITOR supplied it
//
// So ":" is the only load-bearing reading — it is githooks(5)'s documented "no
// editor will run" marker — and everything else, unset included, means an editor
// may. That asymmetry is deliberate: reading unset as "no editor" would put a
// `core.editor` user on the whitespace branch, where the editor template is not
// stripped and every commit is `malformed-subject` at the hook.
//
// `known` is false for a mode name git does not have. The caller must still lint
// (with the returned fallback) and merely warn: the commits that reach a hook
// under such a mode are the ones a `--cleanup=` override let past git's own
// refusal of it, and failing would leave exactly those unlinted (DESIGN §2.1).
//
// The blind spots, unfixable from inside a hook: `--cleanup=<mode>`, `-v` and
// `--no-verbose` on git's COMMAND LINE reach neither the config nor the
// environment (measured), so a per-commit override is judged under the
// repository's settings (DESIGN §2.1 lists what that costs).
func ResolveMode(configured string, edited bool, verbose Verbose) (mode Mode, known bool) {
	cut := verbose == VerboseOn || (verbose == VerboseUnseen && edited) || (configured == "scissors" && edited)
	switch configured {
	case "verbatim":
		return Mode{Truncate: cut}, true
	case "whitespace":
		return Mode{Space: true, Truncate: cut}, true
	case "strip":
		return Mode{Space: true, Comments: true, Truncate: cut}, true
	case "scissors":
		// Without an editor this is whitespace: measured, `commit.cleanup=
		// scissors` with `-F` records the cut line AND everything below it, so
		// only verbose cuts here then. Cutting anyway would hide a footer git
		// keeps — the false-positive direction, which stops a commit.
		return Mode{Space: true, Truncate: cut}, true
	case "default", "":
		// git's default IS the editor question: strip when a message is edited,
		// whitespace when it is not.
		return Mode{Space: true, Comments: edited, Truncate: cut}, true
	default:
		mode, _ = ResolveMode("default", edited, verbose)
		return mode, false
	}
}

// Cleanup reduces a raw commit-message FILE — what git hands a commit-msg hook —
// to the message git will actually record, under the cleanup git is about to
// apply to it.
//
// It exists because the hook runs BEFORE git's own cleanup, so the file may
// still hold the editor template ("# Please enter the commit message..."), the
// status block, and, under commit.verbose, a scissors line with the whole diff
// beneath it. Linting that raw text made the first template comment the
// "subject" and a leading blank line an "empty commit message" — rejecting
// messages git was perfectly happy to record.
//
// It takes a mode because git's cleanup is not one behaviour, and assuming the
// editor one cost both directions of the disagreement this function exists to
// prevent (measured on git 2.54, reproduced as tests):
//
//	git commit -F msg   with "# reminder" as line 1
//	    git records it verbatim (no editor ⇒ whitespace ⇒ comments are content)
//	    glyph dropped it as a comment and linted line 2 ⇒ hook 0, CI 3
//	git commit -F msg   with an indented "  # why:" line above NON-BREAKING:
//	    git records the line; glyph dropped it, closing the gap that made the
//	    footer a trailer ⇒ hook 0, CI 3 (undeclared-removal)
//
// Everything below the mode is a port of git's strbuf_stripspace (strbuf.c) and
// wt_status_locate_end (wt-status.c) rather than an approximation of them,
// because an approximation is what the two rows above are. `git stripspace` is
// the same function on the command line, so the port is held to it by a
// differential test over generated messages (TestCleanupMatchesGitStripspace).
//
// The comment character is assumed to be '#'. A repo that sets core.commentChar
// to something else keeps its comments in the linted text — the same behaviour
// as before this function existed, never worse.
//
// Only the authoring path (`--stdin`) calls this. A --range walk reads messages
// from `git log %B`, which git has already cleaned — internal/gitsource strips
// the one closing newline git records a message with, the only thing that
// separated it from this function's output shape (DESIGN §2.1); running this
// there would silently swallow a genuinely empty message and any body line a
// project chose to start with '#'.
func Apply(message string, mode Mode) string {
	if mode.Truncate {
		message = truncateAtCutLine(message)
	}
	if !mode.Space {
		// verbatim: git records the bytes as they are, so glyph judges them as
		// they are. The trailing newline is dropped only to keep this function's
		// output shape constant for its caller.
		return strings.TrimSuffix(message, "\n")
	}
	return stripspace(message, mode.Comments)
}

// truncateAtCutLine is git's wt_status_locate_end: the cut is the FIRST cut line
// that opens a line, and a message that starts with one cleans to nothing.
func truncateAtCutLine(message string) string {
	if strings.HasPrefix(message, cutLine) {
		return ""
	}
	if i := strings.Index(message, "\n"+cutLine); i >= 0 {
		return message[:i+1]
	}
	return message
}

// stripspace is git's strbuf_stripspace, line for line: comments (when asked)
// vanish without counting as blank lines, every other line loses its trailing
// whitespace, and a run of blank lines survives as at most one — never at the
// start or the end.
//
// The trim set is " \t\r" and not " \t". git trims what ITS isspace says is
// space, and git's sane_ctype gives that answer for space, tab, CR and LF only —
// a \v or \f is content. A differential run of 882 generated messages against
// `git stripspace` disagreed on 82 of them before the CR went in, every one of
// them a line ending in CR followed by spaces.
//
// git's output ends with a newline whenever it is non-empty; this returns the
// same text without it, so a caller can compare against a message rather than a
// file.
func stripspace(message string, dropComments bool) string {
	var b strings.Builder
	blank, wrote := false, false
	for line := range strings.SplitSeq(message, "\n") {
		// A comment is invisible: it is neither kept nor counted as the blank
		// line that would separate what is around it (git `continue`s before
		// touching its `empties` counter). "a\n# c\nb" is therefore "a\nb", not
		// "a\n\nb" — and a trailer stacked under a commented-out line stays a
		// trailer.
		if dropComments && strings.HasPrefix(line, "#") {
			continue
		}
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			blank = true
			continue
		}
		if blank && wrote {
			b.WriteString("\n")
		}
		blank = false
		if wrote {
			b.WriteString("\n")
		}
		b.WriteString(trimmed)
		wrote = true
	}
	return b.String()
}
