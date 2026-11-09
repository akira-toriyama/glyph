package cli

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/akira-toriyama/glyph/v6/internal/config"
	"github.com/akira-toriyama/glyph/v6/internal/core"
	"github.com/akira-toriyama/glyph/v6/internal/gitsource"
)

// configPathAt is where a checkout keeps its glyph.toml: its top level (git's
// answer; the current directory only where init finds no repository at all)
// plus the one file name. It is the single spelling of that location: the
// verdict commands read it, init writes it, doctor loads it.
func configPathAt(top string) string {
	return filepath.Join(top, "glyph.toml")
}

// errInsideGitDir is checkoutTop's answer inside a git directory — a
// checkout's .git or a bare repository: a repository is there, and it has no
// work tree to stand in.
var errInsideGitDir = errors.New("inside a git directory, which has no work tree")

// checkoutTop is git's top level for the working directory, or why there is
// none, told apart for the callers that answer each differently (loadConfig,
// initPath): gitsource.ErrNoRepository where git itself says the directory is
// in no repository, errInsideGitDir where it is inside a git directory, an
// interrupt as the user's own abort, and any other failure — a repository git
// will not read (dubious ownership, a broken config), no git at all — as
// git's own error (4), because a repository git will not read is not the
// absence of one (TestInitCannotRunWhereGitWillNotReadTheRepository). The
// interrupt is asked first: read as "no repository", a Ctrl-C during the
// top-level read sent init's write to the current directory
// (TestInitInterruptDuringTopLevelReadWritesNothing).
func checkoutTop(ctx context.Context) (string, error) {
	top, err := gitsource.TopLevel(ctx, ".")
	if err == nil || core.IsInterrupted(err) {
		return top, err
	}
	inGitDir, gerr := gitsource.InsideGitDir(ctx, ".")
	switch {
	case gerr != nil:
		return "", gerr
	case inGitDir:
		return "", errInsideGitDir
	}
	// git reads a repository here and calls this its work tree, yet named no
	// top level: its own failure is the answer.
	return "", err
}

// noRepositoryUsage reclassifies a failed git read where git itself says the
// directory is in no repository: the caller ran glyph outside a checkout,
// which is the caller's input (usage, 2), the answer the verdict commands
// give it through checkoutTop (t-ckvh, ruled 2026-11-08; measured at
// 2f9428a: `lint --message=x`, `bump --range a..b`, `release --dry-run
// --repo o/r` and `hook install` outside a repository answered git's 4;
// `init --gemoji` writes ./glyph.toml there at 0, because it creates what
// the others read). An interrupt is the user's abort and every other failure
// is handed back as it came: git was reached and answered, so the 2/4 line
// is drawn on what it said (DESIGN §5).
func noRepositoryUsage(ctx context.Context, err error) error {
	if core.IsInterrupted(err) {
		return err
	}
	if _, gerr := gitsource.InsideGitDir(ctx, "."); errors.Is(gerr, gitsource.ErrNoRepository) {
		return noRepository()
	}
	return err
}

// noRepository is the one wording of the refusal every command gives outside
// a repository.
func noRepository() *core.Error {
	return core.Usagef("not a git repository — glyph reads the checkout it runs in (its commits, its glyph.toml, its hooks directory), so run it from inside one")
}

// loadConfig reads the ONE glyph.toml every verdict is judged under: the one
// at the root of the checkout glyph runs in (ratified Q1 — the currently
// checked-out tree decides, accepting that a config change reinterprets past
// commits). Resolution is by git's own answer to "where is this working
// tree's top level", so a command run from a subdirectory reads the same
// file the hook and CI read.
//
// None of the four failures here is the gate code. CodeLint is "what glyph
// was asked to judge violates the convention", and on these commands the
// subject is a COMMIT — the config is the yardstick, not the thing judged. A
// broken yardstick means no verdict was reached, so reporting 3 tells a CI
// gate a commit was rejected when none was even read (t-c6r5). `doctor` is
// the one command whose subject IS the repository's configuration, and it
// keeps answering 3 there; that asymmetry is the contract, not a lapse.
//
//   - missing (2): the invocation assumed an initialized repository and this
//     one is not yet — the fix, `glyph init`, is the caller's.
//   - not a regular file (2): a directory, a named pipe, a socket, a device
//     or a symbolic link that resolves to no file at the path, which
//     config.StatFile classifies and nothing opens. What is there was
//     observed, so it takes the missing case's code, with removal ahead of
//     the same fix (DESIGN §5).
//   - unreadable (4): permission denied, an I/O fault, a vanishing mount.
//     glyph could not reach an answer at all, which is what 4 means; `doctor`
//     and the hook installer already classify the identical event that way.
//   - unparseable (2): the file is there and readable but says something
//     invalid. Same position as a missing config — an unusable configuration
//     a human fixes by editing a file — so it takes the same code.
//
// Keeping these off 3 is also what lets a broken glyph.toml be FIXED: the
// installed commit-msg and pre-push hooks block on 3 alone, so classifying a
// typo as 3 blocks the very commit that would repair it.
//
// Before the file, the checkout (checkoutTop): outside a repository, or
// inside a git directory, there is no glyph.toml to read because the caller
// is standing in no work tree — the caller's input (2), never git's 4
// (t-ckvh); `init` refuses the git directory the same way and writes the
// current directory outside a repository, the one command for which that
// place has a use. A repository git will not read (dubious ownership, a
// broken .git/config) stays git's 4: git was reached and did not say "no
// repository" (TestOutsideARepositoryIsUsage). The hooks block on 3 alone,
// so either answer passes a commit through, as it must.
func loadConfig(ctx context.Context) (*config.Config, error) {
	top, err := checkoutTop(ctx)
	switch {
	case errors.Is(err, gitsource.ErrNoRepository):
		return nil, noRepository()
	case errors.Is(err, errInsideGitDir):
		return nil, core.Usagef("this directory is inside a git directory, which has no work tree — run glyph from the checkout's work tree, where its glyph.toml and its commits are")
	case err != nil:
		return nil, err
	}
	cfg, lerr := config.LoadFile(configPathAt(top))
	if lerr != nil {
		// Order matters: a missing file is itself a *fs.PathError, so the
		// not-exist arm has to be asked first.
		_, isNotAFile := errors.AsType[*config.NotAFileError](lerr)
		_, isPathErr := errors.AsType[*fs.PathError](lerr)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			return nil, core.Usagef("no glyph.toml at %s — this repository is not initialized for glyph; `glyph init --gemoji` (or --conventional) writes one there from anywhere in the work tree, or start from either preset and edit", top)
		case isNotAFile:
			return nil, core.Usagef("%v — glyph reads its configuration from a regular file there and nothing else, so no commit was judged; remove it, then `glyph init --gemoji` (or --conventional) writes one", lerr)
		case isPathErr:
			return nil, core.APIf("%v — glyph could not read this repository's configuration, so no commit was judged", lerr)
		default:
			return nil, core.Usagef("%v", lerr)
		}
	}
	return cfg, nil
}
