// Package notes renders release notes from the commits a caller hands it:
// GroupSigils files each commit under every [[note.sections]] entry whose
// filter it meets and renders its line through note.line's template, and
// RenderSigils draws those sections as Markdown. trailers.go reads a message's
// trailer block for the $coauthors built-in and for [[note.trailers]].
//
// It is pure — no git, no API, no clock — and renders what it is given. It
// holds no walk knowledge: which commits a range holds, the pull each came
// from, the login to credit and whether a sha may be cited are the caller's
// (cli's walkedNoteCommits blanks the sha of a commit no branch holds before
// it arrives here). It decides no version and no release: a commit's level is
// bump.SigilLevel over config's match, and whether a release happens, the
// version and the compare link are the caller's. Commit-derived text is
// escaped only through internal/markdown's Line; nothing here keeps an
// escaping rule of its own.
package notes
