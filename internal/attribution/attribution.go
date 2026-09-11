// Package attribution answers the one question packages add to the release
// walk (DESIGN §4.1): of the version lines a repository declares, which does
// this commit move? It is pure — a commit's files, its captured scope, its
// sigil and the declared packages in; the participating packages or a refusal
// out — and it reads no git, no API and no clock, so the walk, lint --range
// and preview can all ask it and get the same answer for the same commit.
//
// Prohibitions the callers rely on: no package here is ever invented (a
// commit under no package with no scope is carried nowhere, never by "every
// line"); no `all` scope exists; the answer never depends on anything but the
// four inputs. Nothing here decides whether a commit participates at all —
// exclude_authors, skip patterns and the pattern match run first, and a commit
// the fold would not read is never handed to Attribute.
package attribution

import (
	"fmt"
	"path"
	"strings"

	"github.com/akira-toriyama/glyph/v3/internal/config"
)

// Reason says why a commit was refused: which of §4.1's two authoring errors
// it is. A consumer branches on it to name the escape.
type Reason int

const (
	// NoCarrier — the commit's files lie under no package, no scope names one,
	// and its sigil claims a version impact. Nothing can carry the claim.
	NoCarrier Reason = iota
	// Contradiction — the scope names a package the commit's files do not
	// touch. The author said where the impact lands and the tree disagrees.
	Contradiction
)

// Refusal is the lint-class error Attribute returns (the caller maps it to
// exit 3; this package, like config, does not decide exit codes). Its fields
// are what an error message or a machine detail needs to name the escapes:
// the packages the files touched, the scope written, the names a scope could
// have used.
type Refusal struct {
	Reason  Reason
	Scope   string
	Sigil   config.Sigil
	Touched []config.Package // packages the files lie under (empty for NoCarrier)
	Names   []string         // every package name, in config order
}

func (r *Refusal) Error() string {
	if r.Reason == Contradiction {
		touched := make([]string, 0, len(r.Touched))
		for _, p := range r.Touched {
			touched = append(touched, p.Path)
		}
		return fmt.Sprintf("scope (%s) names a package this commit does not touch (its files lie under %s): write the scope of a package it touches, or drop the scope",
			r.Scope, strings.Join(touched, ", "))
	}
	return fmt.Sprintf("this commit touches no declared package and its sigil %s claims a version impact nothing can carry: name the package in the scope (one of %s) or write = so it moves no line",
		r.Sigil, strings.Join(r.Names, ", "))
}

// Attribute maps one participating commit to the packages it moves, in config
// order, applying §4.1's rules in their order:
//
//  1. Its files lie under one or more packages → each of them. A file belongs
//     to the package with the LONGEST path prefix, so a nested package takes
//     its files out of its parent and the root package (".") holds only what
//     no other package claims.
//  2. Its files lie under no package and scope names one → that package.
//  3. Otherwise no carrier: sigil = participates nowhere (nil, nil — shared
//     housekeeping has no line to appear on); any other sigil is a *Refusal.
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
	return nil, &Refusal{Reason: NoCarrier, Scope: scope, Sigil: sigil, Names: names(packages)}
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
