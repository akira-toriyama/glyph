// Package attribution answers the one question packages add to the release
// walk (DESIGN §4.1): of the version lines a repository declares, which does
// this commit move? It is pure — a commit's files, its captured scope, its
// sigil and the declared packages in; the packages it is placed on or a refusal
// out — and it reads no git, no API and no clock, so the walk, lint --range
// and preview can all ask it and get the same answer for the same commit.
//
// Prohibitions the callers rely on: no package here is ever invented (a
// commit under no package with no scope is carried nowhere, never by "every
// line", and a commit with no files is not the root package's — the root
// package is a claim on files); no `all` scope exists; the answer never
// depends on anything but the four inputs. Nothing here decides what a
// commit's message may say — exclude_authors, skip patterns and the pattern
// match run first (cli's placeOf): a commit the fold reads arrives with its
// scope and sigil, and a commit the fold does not read arrives with no scope
// and the none sigil, so its files alone place it and nothing here can refuse
// it (DESIGN §4.1, t-sr1c).
//
// A Refusal's SENTENCE reads more than the four inputs — which pattern
// claimed the message, whether the diff was a merge commit's or went unread —
// and the caller sets those on the refusal after the verdict (cli's
// attribute). They choose words, never an answer.
package attribution

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v5/internal/config"
)

// Reason says why a commit was refused: which of §4.1's two authoring errors
// it is. A consumer branches on it to name the escape.
type Reason int

const (
	// NoCarrier — the commit's files belong to no package (or it has none),
	// no scope names one, and its sigil claims a version impact. Nothing can
	// carry the claim.
	NoCarrier Reason = iota
	// Contradiction — the scope names a package that owns none of the
	// commit's files, and its sigil claims a version impact. The author said
	// where the impact lands and the tree disagrees.
	Contradiction
)

// Owned is one file with the package that owns it (the longest declared
// prefix): a Contradiction's evidence for a line the commit does move.
type Owned struct {
	File    string
	Package config.Package
}

// Refusal is the lint-class error Attribute returns (the caller maps it to
// exit 3; this package, like config, does not decide exit codes). Its fields
// are what an error message or a machine detail needs to name the escapes.
//
// Reason, Scope, Sigil, Touched and Names are the verdict's own terms. The
// rest only words the sentence, in two halves: what Attribute saw of the
// files (Owners, Unowned, Files, More), and what it cannot see and the caller
// tells it (Merge, Unread, Pattern). Left untold — Attribute asked directly,
// as its own tests do — the sentence assumes a pattern that captures every
// name and its own sigil, a scope the message can drop, and a diff read whole.
type Refusal struct {
	Reason  Reason
	Scope   string
	Sigil   config.Sigil
	Touched []config.Package // packages that own the commit's files, in config order (empty for NoCarrier)
	Names   []string         // every package name, in config order

	// Owners is a Contradiction's evidence: one owned file per touched
	// package, in config order, so the file that decides is named wherever it
	// sits in the diff — the first three files of `.github/a.yml, Makefile,
	// curry/b.go` are two no package owns before the one that matters.
	// Unowned counts the files no package owns, which are never said to
	// belong to anything.
	Owners  []Owned
	Unowned int
	// Files is a NoCarrier's evidence: the first of the commit's files, none
	// of which a package owns — nil when the commit arrived with no file.
	Files []string
	// More counts what the evidence leaves out: the owned files past Owners,
	// or the files past Files.
	More int

	// Merge: the commit is a merge commit, whose diff no caller reads.
	Merge bool
	// Unread: the caller did not read the commit's diff whole — a file
	// listing GitHub capped or cut short, a shallow clone's boundary — so the
	// files are what it did read, perhaps none, and the refusal is one the
	// caller withholds (cli's partitionLines). The sentence must not state
	// of the commit what is known only of the files read.
	Unread bool
	// Pattern is what a message can write under the pattern that claimed
	// this one, and under the file's others (config.Sayable). Nil when
	// nobody said.
	Pattern *config.Sayable
}

// quoted is how many file names a refusal spells out before counting.
const quoted = 3

// declaration is the escape only files open: a declaration that owns them.
// The path is not guessed — glyph cannot tell a module from root CI or a docs
// tree, and the first path segment of the canonical shared-only commit is
// `.github`, a prefix the loader refuses (DESIGN §4.1, t-n5tw R1). Where the
// loader would refuse the root's default name the sentence says the root
// package takes a name: it holds that name to the scope grammar, and "." is
// no preset scope's word, so `path = "."` alone would send the reader to a
// file that does not load. Whether it would is the loader's to say
// (Sayable.RootNeedsName), not something to read off what the message can
// write.
func declaration(say config.Sayable) string {
	root := `path = "." declares the root package`
	if say.RootNeedsName {
		root = `path = "." and a name declare the root package`
	}
	return `declare the package these files belong to ([[packages]] path = "<its directory>"; ` + root + `, which holds every file no other package claims)`
}

func (r *Refusal) Error() string {
	if r.Reason == Contradiction {
		return r.contradiction()
	}
	return r.noCarrier()
}

// contradiction words the scope check in ownership's terms: each line the
// commit does move, shown by a file and the package that owns it, then the
// scopes that would be true. The first cut said the commit "does not touch"
// travel while naming files under travel/onsen, and printed root-owned files
// as lying "under ." — a filesystem reading of a rule that is about the
// longest prefix (t-mfny (C), t-n5tw 5).
func (r *Refusal) contradiction() string {
	say := r.sayable()
	evidence := make([]string, 0, len(r.Owners)+2)
	var moved, spellable []string
	for _, o := range r.Owners {
		// The root package has no path worth printing ("."), and its name is
		// shown only where a scope can write it.
		label := fmt.Sprintf("%s (%s)", o.Package.Name, o.Package.Path)
		writable := slices.Contains(say.Scopes, o.Package.Name)
		if o.Package.Path == "." {
			label = "the root package"
			if writable {
				label = o.Package.Name + " (the root package)"
			}
		}
		evidence = append(evidence, fmt.Sprintf("%s belongs to %s", o.File, label))
		moved = append(moved, o.Package.Name)
		if writable {
			spellable = append(spellable, o.Package.Name)
		}
	}
	if r.More > 0 {
		evidence = append(evidence, countFiles(r.More, "more ")+" likewise")
	}
	if r.Unowned > 0 {
		evidence = append(evidence, countFiles(r.Unowned, "")+" under no package")
	}
	if n := len(evidence); n > 1 {
		evidence[n-1] = "and " + evidence[n-1]
	}

	// "drop the scope" is said only of a message whose scope was dropped and
	// run through the patterns again (Sayable.ScopeDroppable) — never because
	// the pattern's scope looks optional, which is true of
	// `(?:type\((?P<scope>…)\)|release)` and no help to `type(haiku)~: …`.
	var remedy string
	switch {
	case len(spellable) > 0 && say.ScopeDroppable:
		remedy = fmt.Sprintf("write the scope of a line it moves (%s), or drop the scope", strings.Join(spellable, ", "))
	case len(spellable) > 0:
		remedy = fmt.Sprintf("write the scope of a line it moves (%s)", strings.Join(spellable, ", "))
	case say.ScopeDroppable:
		remedy = "drop the scope"
	default:
		// No instruction this message can follow under its pattern: state
		// the rule rather than name an escape that is not there. "requires a
		// scope" is said only where the tree shows it; a scope that is
		// neither required nor proven droppable is called neither.
		requires := ""
		if say.ScopeRequired {
			requires = "requires a scope and "
		}
		remedy = fmt.Sprintf("patterns[%d], which claimed this message, %sspells none of the lines it moves — the scope must name one of them (%s) or no package", say.Pattern, requires, strings.Join(moved, ", "))
	}

	whose := "this commit does"
	if r.Unread {
		whose = "the files read of this commit do"
	}
	return fmt.Sprintf("scope (%s) names a line %s not move: %s — the longest declared path owns a file: %s",
		r.Scope, whose, strings.Join(evidence, ", "), remedy)
}

func countFiles(n int, more string) string {
	if n == 1 {
		return "1 " + more + "file"
	}
	return fmt.Sprintf("%d %sfiles", n, more)
}

// noCarrier words rule 3's refusal: what the tree was shown, then the
// escapes THIS commit can take, in the order DESIGN gives them — a scope
// naming a line, = (written, or read from a sigil left out), a declaration.
// An escape the claiming pattern cannot write is not named: the first cut
// named the scope and = to every commit, the raw revert included, whose
// pattern fixes its sigil and captures no scope (t-mfny (A)).
func (r *Refusal) noCarrier() string {
	say := r.sayable()
	var opening string
	switch {
	case len(r.Files) > 0:
		whose := "its files"
		if r.Unread {
			whose = "the files read of it"
		}
		files := strings.Join(r.Files, ", ")
		if r.More > 0 {
			files += fmt.Sprintf(" and %d more", r.More)
		}
		opening = fmt.Sprintf("%s (%s) belong to no declared package, and its sigil %s claims a version impact nothing can carry", whose, files, r.Sigil)
	case r.Merge:
		opening = fmt.Sprintf("this merge commit's own diff is never read, so no package's tree can carry its sigil %s", r.Sigil)
	case r.Unread:
		opening = fmt.Sprintf("no file of this commit was read, so no package's tree can carry its sigil %s", r.Sigil)
	default:
		opening = fmt.Sprintf("this commit touches no file, so no package's tree can carry its sigil %s", r.Sigil)
	}

	var escapes []string
	if len(say.Scopes) > 0 {
		escapes = append(escapes, fmt.Sprintf("name the line it moves in the scope (one of %s)", strings.Join(say.Scopes, ", ")))
	}
	switch {
	case say.None:
		escapes = append(escapes, "write = so it moves no line")
	case say.SigilDroppable:
		// The pattern captures no = and reads one all the same: its fixed
		// sigil, for a message that writes none.
		escapes = append(escapes, "leave the sigil out so it moves no line")
	}
	why := ""
	if len(escapes) == 0 {
		// Nothing this message can write under its pattern carries it: say
		// which pattern and why, then where a reworded message could go.
		sigil := fmt.Sprintf("fixes the sigil at %s", r.Sigil)
		if say.SigilGroup {
			sigil = "captures no = as the sigil"
		}
		scope := "captures no scope"
		if say.ScopeGroup {
			scope = "captures no scope naming a line"
		}
		why = fmt.Sprintf("patterns[%d], which claimed this message, %s and %s", say.Pattern, sigil, scope)
		switch {
		case len(say.ElsewhereScopes) > 0 && say.ElsewhereNone:
			escapes = append(escapes, fmt.Sprintf("reword it so another pattern claims it, with a scope naming the line it moves (one of %s) or as = so it moves no line", strings.Join(say.ElsewhereScopes, ", ")))
		case len(say.ElsewhereScopes) > 0:
			escapes = append(escapes, fmt.Sprintf("reword it so another pattern claims it, with a scope naming the line it moves (one of %s)", strings.Join(say.ElsewhereScopes, ", ")))
		case say.ElsewhereNone:
			escapes = append(escapes, "reword it so another pattern claims it as =, so it moves no line")
		}
	}
	if len(r.Files) > 0 {
		escapes = append(escapes, declaration(say))
	}

	switch {
	case why == "":
		return opening + ": " + orList(escapes)
	case len(escapes) == 0:
		// Nowhere to reword to and no files to declare: say what was read of
		// the claiming pattern and stop. The sentence went on "nothing a
		// message it claims can write carries this commit", which is a claim
		// about every message the pattern takes, and false wherever its fixed
		// sigil is = and some message reaches it — `chore: …` under
		// `(?:fix(?P<semver_sigil>[~^!])|chore): ` (measured 2026-10-05: lint
		// 0). Nor is anything claimed of the rest of the file, where a warn
		// pattern Sayable leaves out may still carry the commit.
		return opening + ": " + why
	default:
		return opening + ": " + why + " — " + orList(escapes)
	}
}

// sayable is what the sentence assumes a message can write: what the caller
// said of the claiming pattern, else every name, a scope it can drop and the
// message's own sigil.
func (r *Refusal) sayable() config.Sayable {
	if r.Pattern != nil {
		return *r.Pattern
	}
	return config.Sayable{Pattern: -1, ScopeGroup: true, ScopeDroppable: true, Scopes: r.Names, SigilGroup: true, None: true, RootNeedsName: true}
}

// orList joins alternatives: "a", "a, or b", "a, b, or c". The comma before
// "or" is unconditional because an alternative may hold an "or" of its own.
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
}

// Attribute maps one commit to the packages it is placed on, in config
// order, applying §4.1's rules in their order:
//
//  1. Its files lie under one or more packages → each of them. A file belongs
//     to the package with the LONGEST path prefix, so a nested package takes
//     its files out of its parent and the root package (".") holds only what
//     no other package claims.
//  2. Its files lie under no package — or it has none: an empty commit, a
//     merge commit whose diff the caller never reads — and scope names one →
//     that package. The root package is a claim on files, so it does not
//     carry a commit that shows none.
//  3. Otherwise no carrier: sigil = is placed nowhere (nil, nil — shared
//     housekeeping has no line to appear on); any other sigil is a *Refusal,
//     whatever the scope says: a scope naming no package is not a third
//     state.
//
// A scope that names a package owning none of the files is a *Refusal
// (Contradiction) when the sigil claims a version impact — owning in rule 1's
// sense, so a parent's name over a nested package's file is one, and so is
// the root's name over a declared package's. A = is placed by its files: it
// claims no impact, so there is nothing for the tree to contradict. The scope
// is checked only when it names a package, so (ci), (deps) and every
// free-form scope pass through untouched. A scope is matched against
// Package.Name exactly.
//
// files are repository-relative, slash-separated, as git diff-tree and the
// commits API list them; they are cleaned (a leading "./" cannot defeat the
// prefix match) and never otherwise interpreted. With no packages declared
// the question does not arise — the repository is one line — and the answer
// is nil, nil: the caller must not ask.
func Attribute(files []string, scope string, sigil config.Sigil, packages []config.Package) ([]config.Package, error) {
	if len(packages) == 0 {
		return nil, nil
	}
	touchedIdx := make(map[int]bool)
	for _, f := range files {
		if i, ok := owner(path.Clean(f), packages); ok {
			touchedIdx[i] = true
		}
	}
	var touched []config.Package
	for i, p := range packages {
		if touchedIdx[i] {
			touched = append(touched, p)
		}
	}

	named, hasNamed := -1, false
	if scope != "" {
		for i, p := range packages {
			if p.Name == scope {
				named, hasNamed = i, true
				break
			}
		}
	}

	if len(touched) > 0 {
		if hasNamed && !touchedIdx[named] && sigil != config.SigilNone {
			return nil, contradiction(files, scope, sigil, touched, packages)
		}
		return touched, nil
	}
	if hasNamed {
		return []config.Package{packages[named]}, nil
	}
	if sigil == config.SigilNone {
		return nil, nil
	}
	return nil, noCarrier(files, scope, sigil, packages)
}

// contradiction builds the scope check's refusal with its evidence: the first
// file each touched package owns, the other owned files counted, and the
// files no package owns counted apart.
func contradiction(files []string, scope string, sigil config.Sigil, touched, packages []config.Package) *Refusal {
	r := &Refusal{Reason: Contradiction, Scope: scope, Sigil: sigil, Touched: touched, Names: names(packages)}
	first := make(map[int]string, len(touched))
	for _, f := range files {
		f = path.Clean(f)
		i, owned := owner(f, packages)
		_, seen := first[i]
		switch {
		case !owned:
			r.Unowned++
		case seen:
			r.More++
		default:
			first[i] = f
		}
	}
	for i, p := range packages {
		if f, ok := first[i]; ok {
			r.Owners = append(r.Owners, Owned{File: f, Package: p})
		}
	}
	return r
}

// noCarrier builds rule 3's refusal with its evidence: no package owns any
// of files, so the first few stand for all of them.
func noCarrier(files []string, scope string, sigil config.Sigil, packages []config.Package) *Refusal {
	r := &Refusal{Reason: NoCarrier, Scope: scope, Sigil: sigil, Names: names(packages)}
	for i, f := range files {
		if i == quoted {
			r.More = len(files) - quoted
			break
		}
		r.Files = append(r.Files, path.Clean(f))
	}
	return r
}

// owner returns the index of the package whose path is the longest prefix of
// file, and false when no package claims it. The root package "." claims
// every file and is the weakest prefix, so it wins only when nothing else
// does.
func owner(file string, packages []config.Package) (int, bool) {
	best, bestLen, found := -1, -1, false
	for i, p := range packages {
		var l int
		switch {
		case p.Path == ".":
			l = 0
		case file == p.Path || strings.HasPrefix(file, p.Path+"/"):
			l = len(p.Path)
		default:
			continue
		}
		if l > bestLen {
			best, bestLen, found = i, l, true
		}
	}
	return best, found
}

func names(packages []config.Package) []string {
	out := make([]string, 0, len(packages))
	for _, p := range packages {
		out = append(out, p.Name)
	}
	return out
}
