// Package gitsource runs every git subprocess the binary runs
// (exec.CommandContext — a Ctrl-C cancels the child). It is an I/O adapter: it
// moves bytes and classifies failures (all git failures are core.CodeAPI by
// contract), and holds no commit semantics — a message's meaning belongs to
// internal/config's pattern match, participation and folding to internal/bump.
package gitsource

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/core"
)

// RawCommit is one commit as git reports it, before any parsing: what its
// readers judge participation by (the author, the message) and what tells
// them a merge commit, whose diff is never read (the parent count).
type RawCommit struct {
	SHA     string
	Author  string // author name (%an) — what bot/automation matching runs on
	Email   string // author email (%ae), as git holds it — self-asserted text that names no login, a GitHub noreply address included (DESIGN §3)
	Login   string // GitHub login; always "" from git (github.Commit fills it from the API's author.login) — mirrored so the two convert
	Parents int    // parent count; >= 2 marks a merge commit
	// Message is the message as git recorded it (%B) minus the one closing
	// newline git records a cleaned message with — the shape cleanup.Apply hands
	// the authoring path, so the hook and every history reader judge one text
	// (DESIGN §2.1). Nothing else is trimmed: a verbatim message keeps its own
	// trailing blank lines.
	Message string
	// Recorded is the message as git recorded it (%B), closing newline and
	// all: the bytes git's own commit reader reads the trailer block off, and
	// the one text notes read it off (DESIGN §3). The newline decides one
	// shape — `Conflicts:\n` closing the object is git's ignored list, while
	// `Conflicts:` closing the input is an empty trailer — and the parser
	// cannot tell from Message which it was handed. Every other reader judges
	// Message.
	Recorded string
}

// logFormat renders one record per commit: SHA, author name, author email,
// parent SHAs and the raw message (%B last), each field closed by NUL and the
// record by the NUL -z writes after it. NUL frames the fields too because it is
// the one byte none of them can hold — git refuses it in a message ("a NUL byte
// in commit log message not allowed", measured) and an ident is a C string —
// and the record framing already rested on it. Not the unit separator: git
// keeps one inside an author name (DESIGN §2, `exclude_authors`;
// TestLogReadsAnAuthorNameHoldingTheUnitSeparator).
const logFormat = "%H%x00%an%x00%ae%x00%P%x00%B"

// logFields is how many NUL-closed fields logFormat writes per record.
const logFields = 5

// logCmd is the head of every `git log` whose output parseLog reads. A display
// setting in the user's config must never reach that parser, and one does:
// under log.showSignature git prints each signature's verdict — and a merged
// signed tag's — on stdout ahead of the commit's record, --format or not, and
// parseLog refuses any byte that is not a record, so every history read of a
// developer who signs failed at 4 (measured on git 2.54; the installed pre-push
// hook lets 4 through). --no-show-signature turns it off whatever the config
// says. i18n.logOutputEncoding is the second: git re-encodes what it prints to
// it, so under ISO-8859-1 an author name and a message arrived as bytes that are
// not UTF-8, and under UTF-16 the whole record did, which parseLog refuses, so
// every history read failed at 4 (measured on git 2.54). --encoding=UTF-8 names
// the one encoding every reader here assumes. log.decorate, log.abbrevCommit,
// log.date, log.mailmap and color.ui move nothing logFormat prints (measured).
func logCmd(args ...string) []string {
	return append([]string{"log", "-z", "--no-show-signature", "--encoding=UTF-8"}, args...)
}

// log returns the commits in revRange (e.g. "BASE..HEAD"), oldest first. An
// empty range is a successful empty result. --end-of-options pins revRange as
// a revision, so an option-shaped argument is a git error, never a flag.
func log(ctx context.Context, dir, revRange string) ([]RawCommit, error) {
	return LogRevs(ctx, dir, []string{revRange})
}

// LogRevs is log over several revisions at once — the union of what each
// includes, minus what each `^`-prefixed one excludes.
//
// Exclusions are spelled `^<object>` rather than passed as `--not`, and that is
// forced by --end-of-options rather than chosen: everything after it is a
// revision, so `git log --end-of-options HEAD --not --remotes=origin` is
// `fatal: bad revision '--not'` (measured). Giving up --end-of-options to spell
// it the readable way would give up this package's property that no caller
// string can be read as an option, on the one caller whose input comes off a
// git hook's stdin.
func LogRevs(ctx context.Context, dir string, revs []string) ([]RawCommit, error) {
	if len(revs) == 0 {
		return nil, nil
	}
	args := logCmd(append([]string{"--reverse", "--format=" + logFormat, "--end-of-options"}, revs...)...)
	out, err := run(ctx, dir, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	return parseLog(out)
}

// RemoteTips returns the object names of every remote-tracking ref under
// remote — what this clone last saw that remote holding.
//
// The pattern's trailing slash states the intent; it does not guard anything.
// for-each-ref matches a literal pattern "completely or from the beginning up
// to a slash" (git-for-each-ref(1)), so `refs/remotes/origin` alone already
// stops short of a remote named `originmirror` — measured 2026-09-29 on git
// 2.54: both spellings list origin's refs and none of originmirror's.
//
// Measured equal to `--not --remotes=<remote>` on the case that rules out the
// cheaper spellings (re-measured 2026-09-29, git 2.54): a branch pushed to a
// second remote first makes bare `--remotes` report ZERO outgoing commits, i.e.
// a silent green over unlinted work, while both the qualified form and this
// one report the two real ones.
func RemoteTips(ctx context.Context, dir, remote string) ([]string, error) {
	out, err := run(ctx, dir, "for-each-ref", "--format=%(objectname)", "--end-of-options", "refs/remotes/"+remote+"/")
	if err != nil {
		return nil, err
	}
	var tips []string
	for l := range strings.SplitSeq(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tips = append(tips, l)
		}
	}
	return tips, nil
}

// RemoteURLs maps each configured remote's name to its URL.
//
// For the pre-push hook the map is how a remote's name is found: git hands
// the hook a remote NAME or a bare URL in the same argument (measured:
// `git push git@host:o/r.git HEAD` passes the URL as both `$1` and `$2`), so
// the name has to be recovered from the URL before anything can be asked about
// the remote's tracking refs.
//
// No remotes at all is an empty map, not an error: git spells it exit 1, the
// same shape ConfigGet documents for an unset key.
//
// Read NUL-framed (`key\nvalue\0`): the line form separates key and value with
// a space, and a remote name may hold one (`[remote "my fork"]`), which the
// line form would cut to `my` (TestRemoteURLsKeepsASpacedRemoteName).
func RemoteURLs(ctx context.Context, dir string) (map[string]string, error) {
	out, found, err := runAnswer(ctx, dir, "config --get-regexp", "config", "-z", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return nil, err
	}
	urls := map[string]string{}
	if !found {
		return urls, nil
	}
	for rec := range strings.SplitSeq(string(out), "\x00") {
		key, url, ok := strings.Cut(rec, "\n")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name != "" && name != key {
			urls[name] = url
		}
	}
	return urls, nil
}

// DefaultBranch returns remote's default branch as this clone records it, or
// "" when nothing records it. Unresolved is deliberately NOT an error: the
// caller has a correct behaviour for it and only that caller can decide what
// not knowing costs.
//
// It reads a LOCAL ref and never the network. A git hook that needed a token
// would fail on every machine that has none, and refuse a push for a reason
// that has nothing to do with the commits being pushed. The ref is written by
// clone and refreshed by `git remote set-head <remote> -a`; a clone made before
// that ref existed simply has no answer, which is why the caller degrades
// rather than guesses (`main` is wrong on a `master` or `develop` repository,
// and guessing it there blocks a legal commit whose only escape is
// --no-verify).
//
// A recorded name whose ref is gone records nothing either. A default branch
// renamed on the remote leaves this ref dangling after `git fetch --prune`: git
// warns once and leaves it, and symbolic-ref still answers exit 0 with the old
// name. Believed, that name makes the new default branch read as off the
// default branch, so a violating push lands on it with no remedy named while
// doctor passes (measured on git 2.54;
// TestPrePushReadsADanglingDefaultBranchAsUnrecorded). A recorded name whose
// commit this clone holds and cannot read is the failure it is, not
// unrecorded: set-head repairs no object store
// (TestDefaultBranchFailsOnATargetGitCannotRead).
func DefaultBranch(ctx context.Context, dir, remote string) (string, error) {
	prefix := "refs/remotes/" + remote + "/"
	out, recorded, err := runAnswer(ctx, dir, "symbolic-ref", "symbolic-ref", "--quiet", "--end-of-options", prefix+"HEAD")
	switch {
	case err != nil:
		// A remote name config accepts and refs refuse (`remote.mirror..x.url`,
		// hand-edited: `git remote add` refuses it) can record nothing, and
		// symbolic-ref answers that 128 "No such ref", not 1. Asked of git
		// rather than matched in that message: its text is not an interface.
		// The interrupt is asked first, as mergedTags asks it of its second
		// read: a cancel landing in this probe kills the child, and read as
		// "git could not say" it would hand back the symbolic-ref failure in
		// the interrupt's place.
		can, perr := RemoteCanRecordHead(ctx, dir, remote)
		switch {
		case perr != nil && core.IsInterrupted(perr):
			return "", perr
		case perr == nil && !can:
			return "", nil
		}
		return "", err
	case !recorded:
		return "", nil
	}
	target := strings.TrimSpace(string(out))
	exists, err := namesCommit(ctx, dir, target)
	if err != nil || !exists {
		return "", err
	}
	return strings.TrimPrefix(target, prefix), nil
}

// tags lists all tags in git's `--sort=-v:refname` order, unfiltered — the
// policy of what counts as a version stays with internal/bump.
//
// That order is NOT a version ordering and no caller may read it as one. It is
// a REFNAME sort that compares digit runs numerically, so it still orders on
// the leading byte first: every `refs/tags/v…` lands above every
// `refs/tags/1…`, and on {v0.0.1, v0.0.2, 9.9.9, 100.0.0} git reports v0.0.2
// ahead of the highest version there is. A caller that wants the highest
// VERSION must parse every entry and compare (internal/walk's
// LatestVersionTag). What the order IS good for is being STABLE, which is what
// lets that caller break a tie between two spellings of one version without
// inventing a preference.
func tags(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, tagListCmd()...)
	if err != nil {
		return nil, err
	}
	return tagLines(out), nil
}

// mergedTags is tags narrowed to the tags whose commit rev's history holds
// (`git tag --merged`): the releases rev's own history carries, in the same
// order under the same contract. --merged peels an annotated tag to its commit
// and leaves out a tag on a non-commit object (measured, git 2.54). The rev is
// passed in the attached form (--merged=REV), so it is the option's value and
// can never be read as another option.
//
// A rev that names no commit holds no tags. The unborn HEAD of a repository
// before its first commit is the case that reaches here: git answers it with
// "malformed object name HEAD" at exit 128 (measured), tags answers empty
// there, and a verdict that only reads a step base — `bump --pr`, `preview
// --pr` — moved from exit 0 to exit 4 on git's failure. So the failure is
// asked about, and only when the listing fails: the ordinary path stays one
// git call.
func mergedTags(ctx context.Context, dir, rev string) ([]string, error) {
	out, err := run(ctx, dir, tagListCmd("--merged="+rev)...)
	if err != nil {
		if core.IsInterrupted(err) {
			return nil, err
		}
		named, nerr := namesCommit(ctx, dir, rev)
		switch {
		case nerr != nil && core.IsInterrupted(nerr):
			return nil, nerr
		case nerr == nil && !named:
			return nil, nil
		}
		return nil, err
	}
	return tagLines(out), nil
}

// tagListCmd is the head of every `git tag --list` tagLines reads. --no-column
// because column.ui (or column.tag) set to `always` lays the listing out in
// columns even into a pipe: two tags came back as one line, tagLines read it as
// one name no version parses, and the step base fell to v0.0.0 — `bump --range`
// printed v0.0.1 at exit 0 with nothing said where the answer is v0.1.1, and a
// bare --since-tag warned that HEAD's history holds no version tag and walked
// the whole of it (measured on git 2.54).
func tagListCmd(args ...string) []string {
	return append(append([]string{"tag", "--list", "--no-column"}, args...), "--sort=-v:refname")
}

// tagLines splits git's one-name-per-line tag listing. Shared by tags and
// mergedTags so the two readers cannot come to disagree about what a listing
// holds, as runIn is the one exec site behind run.
func tagLines(out []byte) []string {
	var tags []string
	for l := range strings.SplitSeq(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags = append(tags, l)
		}
	}
	return tags
}

// namesCommit reports whether rev names a commit this repository can read. A
// NO is an answer about the name — git knows no such name (an unborn HEAD, a
// dangling ref, a word that is no revision), holds no object under it, or
// holds an object that is no commit (a blob, a tree, a tag on either) — and
// never a failure to read one: an empty or unopenable loose object and a
// commit whose body does not parse are the git/IO error they always were, as
// is anything else (not a repository, no git). The interrupt is asked first at
// each step, as everywhere in this package (see isAncestor for why the order
// is the whole point).
//
// Asked of `cat-file -t rev^{}`: the peel parses the object, so exit 0 names a
// readable object of the printed type — a tag peeled to its target — and one
// call answers the ordinary path. Its 128 is the same for a name git does not
// know, an object it does not hold, a held object it cannot read and a git that
// cannot run here at all, and is taken apart in three steps, each a process a
// healthy repository never pays. `rev-parse --verify --quiet rev` answers 0 or
// 1 wherever git can resolve names, so its failure is git unable to run here
// (not a repository, a config it cannot parse), returned as such: a fatal with
// no error: line is then not about the name. Past that, errorLine reads the
// first call's stderr: an error: line is git saying it could not read what it
// was told to, and the failure stands whatever the name — a held object git
// cannot read, or any name at all over a store whose alternates directory is
// gone (git ≤ 2.54 writes that line for an absent sha, 2.56 for a dangling
// ref, a word that is no revision and an unborn HEAD as well; measured). Then,
// for a name rev-parse resolved, `cat-file -e rev`: its existence check is a
// stat, so exit 0 against a 128 from the peel is an object the store holds and
// git could not read without saying so — the loose object the user cannot open
// on git ≤ 2.39, which writes no error: line for it (measured on 2.39.5; 2.40.1
// and later write one, and never reach this step). A stat cannot see through a
// fan-out directory git cannot open (exit 1, as for an absent sha), so below
// 2.40 that shape — git running as another user under umask 077 — still reads
// as nothing in the store: a known limit, stated in DESIGN §4. Not `rev-parse
// --verify --quiet rev^{commit}`: its exit 1 answers a dangling ref, an absent
// sha, a blob and a `hash-object --literally` commit alike, and which of the
// unreadable shapes answer 1 and which 128 moves with the git version (an
// empty object file is 1 on 2.54 and 128 on 2.39.5 and 2.56.0; measured)
// (TestIsAncestorFailsWhereGitCouldNotAnswer,
// TestDefaultBranchFailsOnATargetGitCannotRead, TestMergedTagsFailsOnARevGitCannotRead).
func namesCommit(ctx context.Context, dir, rev string) (bool, error) {
	typ, stderr, _, terr := runRead(ctx, dir, nil, "cat-file", "-t", "--end-of-options", rev+"^{}")
	if terr == nil {
		return strings.TrimSpace(string(typ)) == "commit", nil
	}
	if core.IsInterrupted(terr) {
		return false, terr
	}
	_, known, err := runAnswer(ctx, dir, "rev-parse", "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	if err != nil {
		return false, err
	}
	if _, unreadable := errorLine(stderr); unreadable {
		return false, terr
	}
	if !known {
		return false, nil
	}
	_, held, err := runAnswer(ctx, dir, "cat-file", "cat-file", "-e", "--end-of-options", rev)
	switch {
	case err != nil:
		return false, err
	case held:
		return false, core.APIf("git cat-file: %s names an object this repository holds and cannot read", rev)
	}
	return false, nil
}

// isTag reports whether refs/tags/<name> exists — an exact ref, never a
// revision: rev-parse would resolve `v1.0.0~1` through the real tag and call
// it one. A NO is git's exit 1 under show-ref --verify --quiet and is an
// answer; anything else is the git/IO error it always was, the interrupt
// asked first (see isAncestor for why the order is the whole point).
func isTag(ctx context.Context, dir, name string) (bool, error) {
	_, exists, err := runAnswer(ctx, dir, "show-ref", "show-ref", "--verify", "--quiet", "refs/tags/"+name)
	return exists, err
}

// Head returns the checkout's HEAD commit sha — the target_commitish a
// rolling draft records so the eventual Publish tags the commit the verdict
// was computed at.
func Head(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TopLevel returns the absolute path of the working tree's top level — where
// glyph.toml lives, whatever subdirectory the command runs from. Asked of git
// rather than walked by hand so worktrees and submodules get git's own
// answer.
func TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", core.APIf("git rev-parse --show-toplevel: empty result")
	}
	return path, nil
}

// RemoteCanRecordHead reports whether git can record remote's default branch
// at all: refs/remotes/<remote>/HEAD must be a refname git accepts. Config
// accepts names refs refuse — `my fork`, `mirror..x` — and for those
// `git remote set-head` cannot succeed (unquoted it exits 129, quoted 1:
// "Not a valid ref"); the remedy is renaming the remote. Asked of
// `git check-ref-format` rather than reimplemented: its rules are git's.
func RemoteCanRecordHead(ctx context.Context, dir, remote string) (bool, error) {
	_, valid, err := runAnswer(ctx, dir, "check-ref-format", "check-ref-format", "refs/remotes/"+remote+"/HEAD")
	return valid, err
}

// ErrNoRepository is InsideGitDir's answer where git itself says dir is in no
// repository — the one failure that means "outside a checkout".
var ErrNoRepository = errors.New("not a git repository")

// InsideGitDir reports whether dir is inside a git directory — a checkout's
// .git or a bare repository — the places TopLevel fails although a repository
// is there. Where git answers "not a git repository" it returns
// ErrNoRepository; every other failure (dubious ownership, a broken config, no
// git) is a core.APIf carrying git's message, because a repository git will
// not read is not the absence of one (TestInitCannotRunWhereGitWillNotReadTheRepository).
//
// Both kinds exit 128, so the message is the only discriminator, and it is
// read under LC_ALL=C: git translates it through gettext.
func InsideGitDir(ctx context.Context, dir string) (bool, error) {
	// #nosec G204 -- the binary is the fixed literal "git" and every argument
	// here is a constant of this package.
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-git-dir")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ierr := interrupted(ctx); ierr != nil {
			return false, ierr
		}
		// distill's first fatal: line, not the stream's start: GIT_TRACE and
		// friends write lines ahead of it (TestInitOutsideARepositoryReadsGitsAnswerPastItsTrace).
		why := distill(stderr.Bytes(), err)
		if strings.HasPrefix(why, "fatal: not a git repository") {
			return false, ErrNoRepository
		}
		return false, core.APIf("git rev-parse --is-inside-git-dir: %s", why)
	}
	return strings.TrimSpace(stdout.String()) == "true", nil
}

// HeadTrees lists every directory HEAD's tree records, at any depth, as
// top-level-relative slash paths exactly as git stores them — the subtrees
// attribution can find a changed path under. Byte-exact on purpose: the
// filesystem answers a case-different spelling on a case-insensitive volume
// and follows a symlink git records as a 120000 blob, so it called both
// subtrees while attribution found no file under either (t-fdd8 (1)).
//
// `-d` lists a submodule's gitlink (160000) beside the trees, and that is
// kept: attribution's owner matches a file EQUAL to a package path as well as
// one under it, so a declared path naming a submodule claims that submodule's
// bumps. Filtering to trees would fail a path attribution still carries.
//
// --full-tree because ls-tree otherwise lists only the part of the tree under
// the working directory, relative to it. An unborn HEAD fails (git's "Not a
// valid object name HEAD"): no tree was recorded to answer from, and the caller
// renders that as could-not-run rather than as an empty answer.
func HeadTrees(ctx context.Context, dir string) ([]string, error) {
	out, err := run(ctx, dir, "ls-tree", "-r", "-d", "-z", "--name-only", "--full-tree", "HEAD")
	if err != nil {
		return nil, err
	}
	var trees []string
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			trees = append(trees, p)
		}
	}
	return trees, nil
}

// ActionFile is one action metadata file git counts as part of the checkout,
// as a path relative to the directory ActionFiles was asked at. SkipWorktree
// marks an entry sparse checkout keeps out of the working tree: git tracks
// it, but no bytes were put on disk to read.
type ActionFile struct {
	Path         string
	SkipWorktree bool
}

// actionSpecs selects the two names GitHub reads as action metadata, at any
// depth (`**/` matches zero directories too, so a root action.yml is in).
var actionSpecs = []string{"--", ":(glob)**/action.yml", ":(glob)**/action.yaml"}

// ActionFiles lists every action.yml / action.yaml git counts as part of the
// checkout — tracked, an initialized submodule's tracked files included, plus
// untracked files no ignore rule excludes — for the pin scan (DESIGN §7). git
// lists rather than a filesystem walk because "any path" on disk includes what
// is not the repository (dependency checkouts under ignored build directories
// can carry action files whose pins are someone else's), and because a walk
// is far slower (DESIGN §7 holds the measurement).
//
// Two listings, because `--recurse-submodules` refuses to combine with
// --others ("fatal: ls-files --recurse-submodules unsupported mode", measured
// on git 2.54) and a submodule's composite is code the checkout runs. -t tags
// each tracked entry: H for an ordinary one — a working-tree deletion stays H,
// the caller reads its absence — and S for a skip-worktree (sparse) one.
// Ask it at the top level: ls-files reports paths relative to the directory
// it runs in, and lists only that directory's part of the tree.
func ActionFiles(ctx context.Context, dir string) ([]ActionFile, error) {
	tracked, err := run(ctx, dir, append([]string{"ls-files", "-z", "-t", "--cached", "--recurse-submodules"}, actionSpecs...)...)
	if err != nil {
		return nil, err
	}
	untracked, err := run(ctx, dir, append([]string{"ls-files", "-z", "--others", "--exclude-standard"}, actionSpecs...)...)
	if err != nil {
		return nil, err
	}
	// Seen once per path: an unmerged entry is listed once per stage.
	seen := map[string]bool{}
	var files []ActionFile
	for rec := range strings.SplitSeq(string(tracked), "\x00") {
		if rec == "" {
			continue
		}
		tag, path, ok := strings.Cut(rec, " ")
		if !ok || path == "" {
			return nil, core.APIf("git ls-files -t: malformed entry %q", rec)
		}
		if !seen[path] {
			seen[path] = true
			files = append(files, ActionFile{Path: path, SkipWorktree: tag == "S"})
		}
	}
	for path := range strings.SplitSeq(string(untracked), "\x00") {
		if path != "" && !seen[path] {
			seen[path] = true
			files = append(files, ActionFile{Path: path})
		}
	}
	return files, nil
}

// HooksDir returns the directory git will look in for hooks, as a path relative
// to dir (or absolute, when git reports one). It asks git rather than assuming
// .git/hooks because core.hooksPath relocates them — the family's older repos
// set it to scripts/hooks, and writing to .git/hooks there would install a hook
// git never runs. Worktrees are handled by the same delegation.
func HooksDir(ctx context.Context, dir string) (string, error) {
	return GitPath(ctx, dir, "hooks")
}

// GitPath is `git rev-parse --git-path <name>`: where git keeps one of its own
// files for this checkout, relative to dir or absolute. Asked of git because the
// answer moves — a linked worktree's MERGE_MSG is under that worktree's own git
// directory, and core.hooksPath relocates the hooks.
func GitPath(ctx context.Context, dir, name string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", core.APIf("git rev-parse --git-path %s: empty result", name)
	}
	return path, nil
}

// ConfigGet returns one git config value and whether it is set at all.
//
// Unset is (", false, nil) and not an error: git spells "no such key" as exit 1,
// the same shape as a real failure, and the one caller — the commit-msg hook
// asking for `commit.cleanup` — has a correct answer for the unset case and
// nothing to say about the difference. Any OTHER failure (not a repository, a
// malformed config file) reaches the caller as an error, which that caller then
// chooses to treat as unset; the choice belongs there, not here.
//
// `--get` reports the LAST value for a multiply-set key, which is git's own
// precedence for the single-valued keys this asks about.
func ConfigGet(ctx context.Context, dir, key string) (string, bool, error) {
	return configGet(ctx, dir, key)
}

// ConfigBoolOrInt returns a key git reads as a bool-or-int — commit.verbose —
// as the integer git acts on, and whether it is set at all; unset is
// (0, false, nil), as ConfigGet's. git normalises the value itself
// (`--type=bool-or-int` prints true for yes, on or a bare key, and 1024 for
// 1k), so no copy of git's parser lives here. Not `--type=bool`: it prints true
// for -1, which git commit reads as unset and does not cut at (measured).
//
// A value git cannot parse is an API error. `git commit` dies on it before any
// hook runs; `git merge`, which never reads the key, runs its hook anyway, and
// there this read exits 128 (both measured on git 2.54) — which is why the one
// caller treats an error as unset.
func ConfigBoolOrInt(ctx context.Context, dir, key string) (int, bool, error) {
	raw, set, err := configGet(ctx, dir, key, "--type=bool-or-int")
	if err != nil || !set {
		return 0, set, err
	}
	switch raw {
	case "true":
		return 1, true, nil
	case "false":
		return 0, true, nil
	}
	n, aerr := strconv.Atoi(raw)
	if aerr != nil {
		return 0, true, core.APIf("git config --type=bool-or-int %s printed %q, which is neither a bool nor an int", key, raw)
	}
	return n, true, nil
}

// ConfigLastOf returns the value of whichever of keys git defines LAST, and
// whether any of them is set — for a setting git spells under several names and
// reads through one handler, where the last definition under any name wins
// (core.commentChar and core.commentString, measured on git 2.54). Asking each
// name with ConfigGet cannot answer that: it loses the order between names.
// `--get-regexp` lists definitions in git's own read order, `-c` last.
//
// keys are `section.name` without a subsection: they are matched lowercased,
// as git canonicalises them. The value is raw — `-z` keeps a quoted trailing
// space that ConfigGet's trim would drop, and for a comment prefix that space
// is part of the value. Unset is ("", false, nil), as ConfigGet's.
func ConfigLastOf(ctx context.Context, dir string, keys ...string) (string, bool, error) {
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = regexp.QuoteMeta(strings.ToLower(k))
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	out, set, err := runAnswer(ctx, dir, "config", "config", "-z", "--get-regexp", "--end-of-options", pattern)
	if err != nil || !set {
		return "", false, err
	}
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	// Each record is key, newline, value; a bare key (no `=`) has no newline
	// and reads as the empty value.
	_, value, _ := strings.Cut(records[len(records)-1], "\n")
	return value, true, nil
}

// configGet is ConfigGet with git config's own type options (`--type=…`)
// placed before `--get`.
func configGet(ctx context.Context, dir, key string, typ ...string) (string, bool, error) {
	args := append(append([]string{"config"}, typ...), "--get", "--end-of-options", key)
	out, set, err := runAnswer(ctx, dir, "config", args...)
	if err != nil || !set {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// have reports which of shas this repository actually holds, as a set. It is the
// cheap first half of the footprint question the release walk asks about a pull
// request's commit listing (internal/walk: mainFootprint): most listed shas — a
// squash's pre-squash commits, a rebase-merge's pre-rebase ones — exist in no
// clone of the released branch, and answering that for the whole listing in ONE
// call keeps the expensive per-sha ancestry test off them.
//
// A malformed or unknown sha is simply absent from the result, never an error:
// the listing is GitHub's, and this asks a question about the local repository.
//
// A held object git cannot read is not absent where git says it could not
// read it. --batch-check answers `missing` at exit 0 for a loose object that
// is empty, not zlib, truncated, or under a file or fan-out directory it
// cannot open, exactly as for a sha the store does not hold, and tells the two
// apart only by the error: line it writes for the first (measured on git
// 2.39.5, 2.54.0 and 2.56.0 — on 2.39.5 for the first three shapes only; see
// errorLine for what each version writes). Read as absent, a released commit
// the clone holds broken has no landing site, the in-range pull's merge point
// governs it, and it is counted again under a later tag at exit 0 — the shape
// isAncestor refuses, reached one step earlier. So where any sha came back
// `missing` and git wrote an error: line, the read fails with that line, and
// the healthy path stays one call with nothing more read than before
// (TestHaveFailsOnAHeldObjectGitCannotRead, TestSinceTagFailsOnAReleasedCommitGitCannotRead).
// Both halves of that condition carry: a store whose alternates directory is
// gone writes the line on every read, and where every listed sha is held
// there is no absence to vouch for, so the answer stands
// (TestHaveUnderGoneAlternatesAnswersWhereNothingIsMissing). No second signal
// exists here as it does in namesCommit: `%(objectsize:disk)` is a stat too,
// and asking it costs a process per walk step that has a missing sha, which is
// every squash — so below git 2.40 a loose object the user cannot open reads
// as absent to this reader, a known limit stated in DESIGN §4.
func have(ctx context.Context, dir string, shas []string) (map[string]bool, error) {
	have := map[string]bool{}
	if len(shas) == 0 {
		return have, nil
	}
	// --batch-check reads the shas on stdin and answers one line each; a missing
	// object answers "<sha> missing" rather than failing the command.
	out, stderr, _, err := runRead(ctx, dir, strings.NewReader(strings.Join(shas, "\n")+"\n"), "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	missing := false
	for line := range strings.SplitSeq(string(out), "\n") {
		name, typ, ok := strings.Cut(strings.TrimSpace(line), " ")
		switch {
		case ok && typ == "commit":
			have[name] = true
		case ok && typ == "missing":
			missing = true
		}
	}
	if line, unreadable := errorLine(stderr); missing && unreadable {
		return nil, core.APIf("git cat-file: %s", line)
	}
	return have, nil
}

// isAncestor reports whether sha is an ancestor of rev, or is rev itself.
//
// A NO is (false, nil) in the two spellings that answer the question the walk
// asks — "did this listed commit land on the released branch under its own
// identity?": exit 1 with no error: line, the honest no, and exit 128 for a
// name that holds no commit here. Every other failure is an error, exit 4 at
// the top: a held object git cannot read, a commit gone from the middle of the
// history and a directory that is no repository say nothing about landing, and
// read as NO each is "this commit never landed" returned with a nil error
// (TestIsAncestorFailsWhereGitCouldNotAnswer).
//
// git exits 128 for most of those alike, so the failure is asked about, as
// mergedTags asks about its own, rather than matched in a message whose text is
// not an interface: namesCommit answers NO for a name that holds no commit and
// git's failure for an object this repository holds and cannot read. The one
// failure that does not exit 128 is read off stderr the way have reads its
// batch: for a commit gone from the middle of the history, git ≤ 2.40 exits 1
// — the honest no's status — beside "error: Could not read <sha>" (measured on
// 2.39.5 and 2.40.1; 2.54.0 and 2.56.0 exit 128 there), so an exit 1 that
// carries an error: line is git failing, quoted, not a NO. That reads the
// gone-alternates line too, as have does: a NO over a store git says is not
// all there cannot be vouched for, while a YES stands, since no absence was
// claimed (the store in TestIsAncestorFailsWhereGitCouldNotAnswer's
// gone-alternates row).
//
// The interrupt is classified FIRST (runRead), and of each probe after it
// (TestIsAncestorInterruptDuringTheProbeCarriesOut: the probe's interrupt is
// neither nil nor a NO, so a loop that does not return it moves on and hands
// back merge-base's own failure in the abort's place). A cancelled context does
// not stop git politely: exec.CommandContext KILLS the child, and a killed child
// is an *exec.ExitError like any other (measured on this toolchain: err
// "signal: killed", errors.As true, ctx.Err() context.Canceled). Read as an
// answer, a Ctrl-C during the footprint walk is swallowed into "this commit
// never landed", which governs which commits a release counts.
//
// sha and rev are object names the caller took from git or from a GitHub sha
// field, and --end-of-options pins both as revisions rather than options.
func isAncestor(ctx context.Context, dir, sha, rev string) (bool, error) {
	_, stderr, status, err := runRead(ctx, dir, nil, "merge-base", "--is-ancestor", "--end-of-options", sha, rev)
	switch {
	case err == nil:
		return true, nil
	case core.IsInterrupted(err):
		return false, err
	case status == 1:
		if line, unreadable := errorLine(stderr); unreadable {
			return false, core.APIf("git merge-base: %s", line)
		}
		return false, nil
	}
	for _, name := range []string{sha, rev} {
		named, nerr := namesCommit(ctx, dir, name)
		switch {
		case nerr != nil && core.IsInterrupted(nerr):
			return false, nerr
		case nerr == nil && !named:
			return false, nil
		}
	}
	return false, err
}

// firstParentLog returns the n commits ending at rev along the FIRST-PARENT
// chain, oldest first — the shape a rebase-merge leaves on the released branch,
// and the only reading under which a rebased pull's commits are consecutive.
// Fewer than n exist near the root of a history, and the caller decides what a
// short answer means.
func firstParentLog(ctx context.Context, dir, rev string, n int) ([]RawCommit, error) {
	out, err := run(ctx, dir, logCmd("--first-parent", "-n", strconv.Itoa(n),
		"--format="+logFormat, "--end-of-options", rev, "--")...)
	if err != nil {
		return nil, err
	}
	commits, err := parseLog(out)
	if err != nil {
		return nil, err
	}
	slices.Reverse(commits)
	return commits, nil
}

// diffTreeFiles returns the paths a commit's own diff touches — repository-
// relative, slash-separated, in git's order, directories never listed. Rename
// detection is OFF on purpose: a move across two subtrees then names both the
// old path and the new, which is what attribution needs to move both lines
// (DESIGN §4.1, rule 1); with detection on, git would report the pair as one
// entry under the new name and the line the file left would never hear of it.
// A root commit is diffed against the empty tree (--root). A merge commit
// answers EMPTY (git shows no diff for a merge without -m/-c), and callers do
// not ask about one: a merge commit is attributed to nothing.
//
// A shallow clone's boundary commit has no diff this checkout can compute, and
// answers an error IsShallowBoundary recognises (API class, so a caller that
// does not ask fails at 4 rather than read a wrong diff). git reads the
// boundary as a root — its parents were cut off — and `--root` then reported
// its whole tree as its own diff (measured: `curry/b haiku/a` where the full
// clone says `curry/b`; t-esm5). The boundary is told apart from a true root by
// asking the object itself: its header still names the parent git no longer
// traverses to. Not by `.git/shallow`, which lists a true root inside the depth
// too, and not by "parentless in a shallow checkout", which would refuse that
// root's diff although its tree is exactly its diff (both measured). The extra
// read is paid only by a commit git reads as parentless: %P comes back in the
// same diff-tree call.
//
// Asked only of a commit this checkout holds — the walk's landed identities
// and every commit a --range fold reads. A squash-merged pull's inner commits
// exist on no branch, and the walk asks the API about those instead.
func diffTreeFiles(ctx context.Context, dir, sha string) ([]string, error) {
	// --always prints the %P header even for an empty diff, so the header is
	// the output up to the first NUL, and the name list — when there is one —
	// follows it after one "\n" (measured on git 2.54). --encoding for logCmd's
	// reason: i18n.logOutputEncoding re-encodes a non-empty header too, and a
	// UTF-16 one holds a NUL after its byte-order mark, so the cut fell inside it
	// and the parent's hex digits came back as the file list (measured).
	out, err := run(ctx, dir, "diff-tree", "-z", "--always", "--encoding=UTF-8", "--format=%P", "--name-only", "-r", "--root", "--no-renames", "--end-of-options", sha, "--")
	if err != nil {
		return nil, err
	}
	parents, list, ok := bytes.Cut(out, []byte{0})
	if !ok {
		return nil, core.APIf("git diff-tree %s: no header in %q", sha, tail(out))
	}
	if len(parents) == 0 {
		boundary, berr := isGraftBoundary(ctx, dir, sha)
		if berr != nil {
			return nil, berr
		}
		if boundary {
			return nil, &shallowBoundary{err: core.APIf("commit %.7s is a shallow clone's boundary: its parents are not in this checkout, so its own diff cannot be read (fetch the full history, e.g. actions/checkout with fetch-depth: 0)", sha)}
		}
	}
	var files []string
	for entry := range bytes.SplitSeq(bytes.TrimPrefix(list, []byte("\n")), []byte{0}) {
		if len(entry) > 0 {
			files = append(files, string(entry))
		}
	}
	return files, nil
}

// isGraftBoundary reports whether the commit object names a parent although git
// traverses it as a root: a shallow clone's boundary. The caller has already
// seen git read it as parentless.
func isGraftBoundary(ctx context.Context, dir, sha string) (bool, error) {
	out, err := run(ctx, dir, "cat-file", "commit", "--end-of-options", sha)
	if err != nil {
		return false, err
	}
	header, _, _ := bytes.Cut(out, []byte("\n\n"))
	for line := range bytes.SplitSeq(header, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("parent ")) {
			return true, nil
		}
	}
	return false, nil
}

// shallowBoundary carries diffTreeFiles' unreadable answer: an ordinary
// *core.Error of the API class to everything that does not ask, so a caller
// that forgets the case fails loud instead of reading a wrong diff.
type shallowBoundary struct{ err *core.Error }

func (e *shallowBoundary) Error() string { return e.err.Error() }
func (e *shallowBoundary) Unwrap() error { return e.err }

// IsShallowBoundary reports whether err is DiffTreeFiles saying the commit is a
// shallow clone's boundary, whose own diff this checkout cannot compute. The
// callers that can do better than exit 4 — lint judges the message and warns
// that it could not ask attribution, the walk places the commit by its scope
// and sigil alone — branch here.
func IsShallowBoundary(err error) bool {
	_, ok := errors.AsType[*shallowBoundary](err)
	return ok
}

// mergeBase returns the best common ancestor of revs (git merge-base
// --octopus) — the commit the packages walk starts from when the lines' bases
// differ, so that one walk's range contains every line's range (DESIGN
// §4.1, "the walk is one walk"). One rev answers itself. Every rev must be
// an object this checkout holds; an unknown one is a git failure (API), and
// the caller took them from git's own tag list.
func mergeBase(ctx context.Context, dir string, revs []string) (string, error) {
	if len(revs) == 1 {
		return revs[0], nil
	}
	out, err := run(ctx, dir, append([]string{"merge-base", "--octopus", "--end-of-options"}, revs...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isShallow reports whether this checkout is a shallow clone. The release walk
// asks because a truncated history makes every ancestry answer a maybe: a commit
// git cannot see is indistinguishable from one that never landed, and the walk
// would rather say so than quietly grade itself on a partial repository.
// Every `--range` read asks for the same reason about the range (internal/cli's
// logRange): git lists only the commits the clone holds.
func isShallow(ctx context.Context, dir string) (bool, error) {
	out, err := run(ctx, dir, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// parseLog reads the NUL-closed fields logFormat produces, logFields to a
// record. Shared by every reader of that format so a field added to logFormat
// cannot be decoded two ways.
//
// A record is checked before it is believed: its SHA must be a full object
// name and its parents field object names of the same length, space-separated,
// or the read fails like any other git read (API). Those are the two fields git
// alone writes, so a record whose fields did not land where logFormat put them
// fails there instead of reaching the gates as a commit by the wrong author.
// Full object names, not SHA-1's 40 digits: glyph reads SHA-256 repositories
// (TestLogReadsSHA256Repositories).
func parseLog(out []byte) ([]RawCommit, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, core.APIf("git log: the output does not close its last record: %q", tail(out))
	}
	fields := strings.Split(string(out[:len(out)-1]), "\x00")
	if len(fields)%logFields != 0 {
		return nil, core.APIf("git log: %d fields do not make whole records of %d: %q", len(fields), logFields, tail(out))
	}
	commits := make([]RawCommit, 0, len(fields)/logFields)
	for i := 0; i < len(fields); i += logFields {
		f := fields[i : i+logFields]
		parents, ok := objectNames(f[3], len(f[0]))
		if !isObjectName(f[0]) || !ok {
			return nil, core.APIf("git log: malformed record %q", strings.Join(f, "\x00"))
		}
		commits = append(commits, RawCommit{
			SHA:      f[0],
			Author:   f[1],
			Email:    f[2],
			Parents:  parents,
			Message:  strings.TrimSuffix(f[4], "\n"),
			Recorded: f[4],
		})
	}
	return commits, nil
}

// isObjectName reports whether s is a full object name as git prints one:
// lowercase hex, 40 digits for SHA-1 or 64 for SHA-256.
func isObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// objectNames counts the space-separated object names in a %P field, each the
// length of the commit's own name, and reports false for anything else. Empty
// is a root commit's answer.
func objectNames(field string, length int) (int, bool) {
	if field == "" {
		return 0, true
	}
	names := strings.Split(field, " ")
	for _, n := range names {
		if len(n) != length || !isObjectName(n) {
			return 0, false
		}
	}
	return len(names), true
}

// tail is the end of a malformed output, for the error: enough to see where
// the framing went wrong without quoting a whole history.
func tail(out []byte) string {
	const keep = 200
	if len(out) > keep {
		out = out[len(out)-keep:]
	}
	return string(out)
}

// interrupted returns the user's own abort, or nil when the run failed for any
// other reason. It is the ONE place this package decides that question, because
// getting it wrong is silent in both directions and every git call has to get it
// right.
//
// Only a *canceled* context is the user's abort — that is what CodeInterrupted
// means (SIGINT/SIGTERM). A deadline, which only a caller-imposed timeout
// produces, is a wedged or unreachable git: an ordinary I/O failure. Classifying
// that as an interrupt would exit 130 silently and tell a release job the
// operator stopped it. Mirrors internal/github's split of the same two causes.
//
// Callers ask this BEFORE reading the child's exit status, never after. A
// cancelled context kills the child, so the abort arrives wearing an
// *exec.ExitError and any status-first reading absorbs it — see isAncestor,
// where that ordering turned a Ctrl-C into a wrong answer about the repository.
func interrupted(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return core.Interrupted()
	}
	return nil
}

// run executes one git command against dir with stdout and stderr captured
// separately, and classifies any failure as a git/IO error carrying git's most
// diagnostic stderr line.
func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return runIn(ctx, dir, nil, args...)
}

// runIn is the exec site behind run, so a fix to it lands once for every
// reader that carries stderr only as the envelope's one distilled line.
//
// stdin is an io.Reader rather than a string precisely so run can pass NIL and
// keep os/exec's own behaviour (the child gets /dev/null); a string parameter
// would swap that for a pipe this package then has to write to and close,
// which is a behaviour change smuggled inside a deduplication.
//
// #nosec G204 -- the binary is the fixed literal "git". Every argument is
// structured by this package's own funcs; the caller-supplied values
// among them (a revision range, an object name) are pinned as revisions by
// --end-of-options at their call sites, so none can be read as an option.
func runIn(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ierr := interrupted(ctx); ierr != nil {
			return nil, ierr
		}
		return nil, core.APIf("git %s: %s", args[0], distill(stderr.Bytes(), err))
	}
	return stdout.Bytes(), nil
}

// runRead is runIn for the three readers that READ the child's stderr
// (namesCommit, have, isAncestor) rather than carry its one distilled line:
// stderr comes back whatever the exit, for errorLine, and so does git's exit
// status, because what a non-zero status means is the reader's question
// (isAncestor's 1 is an answer; namesCommit's 128 is taken apart). status is -1
// where git never ran to one (not on PATH, killed by a signal), and err is then
// the failure as runIn would carry it; the interrupt is asked first, as
// everywhere (TestHaveCancelIsInterrupted, TestIsAncestorCancelIsInterrupted).
// The child runs under LC_ALL=C because what errorLine reads is the severity
// prefix, and git translates the prefix with the message — `Fehler:` under
// de_DE, `erreur :` under fr_FR, measured on git 2.54 — as InsideGitDir pins
// the locale for the one line it reads; appended last, so it wins over any
// LC_ALL the parent carries (os/exec keeps the last value of a duplicated key;
// TestHeldObjectSignalIsReadUnderThePinnedLocale). Not a property of runIn: no
// other reader looks at stderr, and the envelope's line keeps the user's
// language there.
//
// #nosec G204 -- as runIn: the fixed literal "git", and every caller-supplied
// value pinned by --end-of-options at its call site.
func runRead(ctx context.Context, dir string, stdin io.Reader, args ...string) (stdout, stderr []byte, status int, err error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stdin = stdin
	var out, serr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &serr
	if rerr := cmd.Run(); rerr != nil {
		if ierr := interrupted(ctx); ierr != nil {
			return nil, nil, -1, ierr
		}
		status = -1
		if ee, ok := errors.AsType[*exec.ExitError](rerr); ok {
			status = ee.ExitCode()
		}
		return nil, serr.Bytes(), status, core.APIf("git %s: %s", args[0], distill(serr.Bytes(), rerr))
	}
	return out.Bytes(), serr.Bytes(), 0, nil
}

// errorLine returns git's first error:-severity line, and whether stderr
// carries one — anywhere in stderr, not on its first line, since trace output
// comes first under GIT_TRACE (TestHeldObjectSignalIsFoundBehindStderrNoise).
// It is the signal git gives on stderr that it hit an object it holds and
// could not read, whether it then answered (--batch-check: `missing` at exit
// 0; `merge-base --is-ancestor`: exit 1 for a commit gone from the middle of
// the history on git ≤ 2.40) or died for the generic reason (`cat-file -t`,
// `merge-base`: 128, "Not a valid object name"): for a loose object that is
// empty, not zlib, truncated, or under a file or fan-out directory it cannot
// open, and for a commit whose body does not parse, exit and stdout are those
// of a sha the store does not hold, and this line is the difference
// (TestHaveFailsOnAHeldObjectGitCannotRead). Where git writes no such line it
// gives no signal at all, and this package then cannot see: git 2.39 and
// earlier write nothing for the two permission shapes (object-file.c gained
// its `error_errno` on an open failure in 2.40.0; measured on 2.39.5 against
// 2.40.1, 2.54.0 and 2.56.0), and no version writes one for an object in a
// pack whose .idx or .pack cannot be opened. namesCommit pairs this with
// `cat-file -e` for the file shape; have has no second signal.
//
// It is read as severity, not as message: the text after the prefix is not an
// interface and is never matched. Not warning: (an ambiguous refname, a broken
// loose ref), not hint:, not trace output, not the fatal: line every 128
// carries (TestHeldObjectSignalIgnoresStderrNoise). An unreadable global
// config, attributes or excludes file and a relative safe.directory write
// nothing to stderr on these commands (measured). A store with every object
// readable writes an error: line in three measured cases, each at exit 0 with
// the answer intact: an alternates entry — objects/info/alternates or
// GIT_ALTERNATE_OBJECT_DIRECTORIES — naming a directory that is gone, on every
// object read (and on git 2.56 on an unknown name too); a broken info/grafts
// and a truncated commit-graph, where a commit is parsed — merge-base, tag
// --merged and namesCommit's own peel, `cat-file -t rev^{}` — and not on
// --batch-check or `cat-file -e`; the failed peel of a sha the store does not
// hold writes none (measured on 2.39.5, 2.54.0 and 2.56.0). The readers fail on
// each where the answer was an absence or a NO, and quote the line: git has said
// the store it was told to read is not all there, so an absence there cannot
// be vouched for, and the line names what to fix.
func errorLine(stderr []byte) (string, bool) {
	for line := range strings.SplitSeq(string(stderr), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "error:") {
			return line, true
		}
	}
	return "", false
}

// runAnswer is runIn for a command whose exit 1 is an answer, not a failure: a
// yes/no question, or a config read whose NO is "unset". yes is false exactly
// when git exited 1, and stdout is what git printed either way. verb is the
// command as its caller's refusal names it ("git <verb>: …"), and any failure
// but exit 1 is that core.APIf. The interrupt is asked first, as runIn asks it.
// A child the cancel killed exits -1, never 1: the order shows only in a race no test pins.
//
// #nosec G204 -- the binary is the fixed literal "git". As at runIn's call
// sites, the caller-supplied values among args are pinned by --end-of-options
// or open with a literal refs/ prefix, so none can be read as an option.
func runAnswer(ctx context.Context, dir, verb string, args ...string) (stdout []byte, yes bool, err error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if rerr := cmd.Run(); rerr != nil {
		if ierr := interrupted(ctx); ierr != nil {
			return nil, false, ierr
		}
		if ee, ok := errors.AsType[*exec.ExitError](rerr); ok && ee.ExitCode() == 1 {
			return out.Bytes(), false, nil
		}
		return nil, false, core.APIf("git %s: %s", verb, distill(stderr.Bytes(), rerr))
	}
	return out.Bytes(), true, nil
}

// distill exists because the error envelope carries ONE line and git's stderr
// is many: hint: noise first, the fatal: buried under it. Preference order
// over paraphrasing the body: git prefixes its diagnosis, and the exec error
// ("exit status 128") is the least informative string available — it is the
// fallback, never the answer while stderr has anything at all.
func distill(stderr []byte, err error) string {
	lines := strings.Split(string(stderr), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			return l
		}
	}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
