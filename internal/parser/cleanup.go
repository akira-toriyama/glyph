package parser

import (
	"regexp"
	"strings"
)

// scissorsRE matches the line git writes above the verbose diff:
//
//	# ------------------------ >8 ------------------------
//
// Everything at and below it is git's own scratch output, never the message.
var scissorsRE = regexp.MustCompile(`^\s*#\s*-+\s*>8\s*-+\s*$`)

// Cleanup reduces a raw commit-message FILE — what git hands a commit-msg hook —
// to the message git will actually record.
//
// It exists because the hook runs BEFORE git's own cleanup, so the file still
// holds the editor template ("# Please enter the commit message..."), the status
// block, and, under commit.verbose, a scissors line with the whole diff beneath
// it. Linting that raw text made the first template comment the "subject" and a
// leading blank line an "empty commit message" — rejecting messages git was
// perfectly happy to record.
//
// The three steps mirror git's --cleanup=default, in git's order:
//
//  1. cut at the scissors line (before comments are stripped, since the scissors
//     line IS a comment and removing it first would strand the diff),
//  2. drop whole-line comments,
//  3. drop leading blank lines, so the subject is the first line that carries text.
//
// Only the authoring path (`--stdin`) calls this. A --range walk reads messages
// from `git log %B`, which git has already cleaned; running this there would
// silently swallow a genuinely empty message and any body line a project chose
// to start with '#'.
//
// The comment character is assumed to be '#'. A repo that sets core.commentChar
// to something else keeps its comments in the linted text — the same behaviour
// as before this function existed, never worse.
func Cleanup(message string) string {
	lines := splitLines(message)

	// 1. scissors: everything from that line down is git's, not the author's.
	for i, l := range lines {
		if scissorsRE.MatchString(l) {
			lines = lines[:i]
			break
		}
	}

	// 2. whole-line comments.
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") {
			continue
		}
		kept = append(kept, l)
	}

	// 3. leading blanks, so lines[0] is the subject rather than the gap git
	//    leaves above the template — and trailing blanks, which are all that is
	//    left where the template used to be.
	for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
		kept = kept[1:]
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	return strings.Join(kept, "\n")
}
