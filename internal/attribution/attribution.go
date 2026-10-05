// Package attribution answers the one question packages add to the release
// walk (DESIGN §4.1): of the version lines a repository declares, which does
// this commit move? It is pure — a commit's files, its captured scope, its
// sigil and the declared packages in; the participating packages or a refusal
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
	"strings"

	"github.com/akira-toriyama/glyph/v4/internal/config"
)

// Reason says why a commit was refused: which of §4.1's two authoring errors
// it is. A consumer branches on it to name the escape.
type Reason int

const (
	// NoCarrier — the commit's files belong to no package (or it has none),
	// no scope names one, and its sigil claims a version impact. Nothing can
	// carry the claim.
	NoCarrier Reason = iota
	// Contradiction — the scope names a package the commit's files do not
	// touch. The author said where the impact lands and the tree disagrees.
	Contradiction
)

// Refusal is the lint-class error Attribute returns (the caller maps it to
// exit 3; this package, like config, does not decide exit codes). Its fields
// are what an error message or a machine detail needs to name the escapes.
//
// Reason, Scope, Sigil, Touched and Names are the verdict's own terms. The
// rest only words the sentence, in two halves: what Attribute saw of the
// files (Files, More), and what it cannot see and the caller tells it (Merge,
// Unread, Pattern). Left untold — Attribute asked directly, as its own tests
// do — the sentence assumes a pattern that captures every name and its own
// sigil, and a diff read whole.
type Refusal struct {
	Reason  Reason
	Scope   string
	Sigil   config.Sigil
	Touched []config.Package // packages the files lie under (empty for NoCarrier)
	Names   []string         // every package name, in config order

	// Files is a NoCarrier's evidence: the first of the commit's files, none
	// of which a package owns — nil when the commit arrived with no file.
	// More counts the files it leaves out.
	Files []string
	More  int

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

// declare is the escape only files open: a declaration that owns them. The
// path is not guessed — glyph cannot tell a module from root CI or a docs
// tree, and a first path segment gave `path = ".github"` for the canonical
// shared-only commit, a prefix no tag can carry (DESIGN §4.1, t-n5tw R1).
const declare = `declare the package these files belong to ([[packages]] path = "<its directory>"; path = "." declares the root package, which holds every file no other package claims)`

func (r *Refusal) Error() string {
	if r.Reason == Contradiction {
		touched := make([]string, 0, len(r.Touched))
		for _, p := range r.Touched {
			touched = append(touched, p.Path)
		}
		return fmt.Sprintf("scope (%s) names a package this commit does not touch (its files lie under %s): write the scope of a package it touches, or drop the scope",
			r.Scope, strings.Join(touched, ", "))
	}
	return r.noCarrier()
}

// noCarrier words rule 3's refusal: what the tree was shown, then the
// escapes THIS commit can take, in the order DESIGN gives them — a scope
// naming a line, =, a declaration. An escape the claiming pattern cannot
// write is not named: the first cut named the scope and = to every commit,
// the raw revert included, whose pattern fixes its sigil and captures no
// scope (t-mfny (A)).
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
	if say.None {
		escapes = append(escapes, "write = so it moves no line")
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
		default:
			why += ", and no other pattern captures a scope naming a line or allows ="
		}
	}
	if len(r.Files) > 0 {
		escapes = append(escapes, declare)
	}

	switch {
	case why == "":
		return opening + ": " + orList(escapes)
	case len(escapes) == 0:
		return opening + ": " + why + " — no message can carry it until glyph.toml's patterns change"
	default:
		return opening + ": " + why + " — " + orList(escapes)
	}
}

// sayable is what the sentence assumes a message can write: what the caller
// said of the claiming pattern, else every name and the message's own sigil.
func (r *Refusal) sayable() config.Sayable {
	if r.Pattern != nil {
		return *r.Pattern
	}
	return config.Sayable{Pattern: -1, ScopeGroup: true, ScopeOptional: true, Scopes: r.Names, SigilGroup: true, None: true}
}

// orList joins alternatives: "a", "a, or b", "a, b, or c". The comma before
// "or" is unconditional because an alternative may hold an "or" of its own.
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
}

// Attribute maps one participating commit to the packages it moves, in config
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
//  3. Otherwise no carrier: sigil = participates nowhere (nil, nil — shared
//     housekeeping has no line to appear on); any other sigil is a *Refusal,
//     whatever the scope says: a scope naming no package is not a third
//     state.
//
// A scope that names a package the files do not touch is a *Refusal
// (Contradiction) whatever the files say — the scope is checked only when it
// names a package, so (ci), (deps) and every free-form scope pass through
// untouched. A scope is matched against Package.Name exactly.
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
		if hasNamed && !touchedIdx[named] {
			return nil, &Refusal{Reason: Contradiction, Scope: scope, Sigil: sigil, Touched: touched, Names: names(packages)}
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
