package cli

import (
	"github.com/akira-toriyama/glyph/internal/conventional"
	"github.com/akira-toriyama/glyph/internal/core"
	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/hook"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// profileFlag is the root's persistent --profile value. A flag and not a repo
// config file, ratified in DESIGN §6: the callers that pin glyph's version
// state the profile at the same sites, so a repository cannot drift into a
// vocabulary nobody chose.
var profileFlag string

// profileSpec binds one profile's name to the two things the CLI assembles
// per run: the grammar the parser judges under, and the loader for the
// vocabulary's embedded table. The list below is the whole registry — the
// flag's valid values, the hook interpolation and the grammar mapping all
// derive from it, so a third profile cannot be added by halves.
type profileSpec struct {
	name    string
	grammar parser.Grammar
	load    func() (*gitmoji.Table, error)
}

// profiles is the registry, default first — DESIGN §2's two profiles.
var profiles = []profileSpec{
	{name: "gitmoji", grammar: parser.GrammarGitmoji, load: gitmoji.Load},
	{name: "conventional", grammar: parser.GrammarConventional, load: conventional.Load},
}

// defaultProfile is what --profile defaults to and what an empty value means.
const defaultProfile = "gitmoji"

// resolveProfile validates --profile and returns its spec. An unknown value
// is usage (2) — the caller named a vocabulary glyph does not ship, which is
// an invocation defect, not a convention violation.
func resolveProfile() (profileSpec, error) {
	name := profileFlag
	if name == "" {
		name = defaultProfile
	}
	for _, p := range profiles {
		if p.name == name {
			return p, nil
		}
	}
	return profileSpec{}, core.Usagef("unknown profile %q: the profiles are gitmoji (the default) and conventional", profileFlag)
}

// profileKinds is the hook set for the run's validated profile. Every caller
// sits after loadRules validated the flag, so the resolve cannot fail there;
// the defensive default keeps these advisory paths advisory rather than
// panicking on a state the command dispatch already rejects.
func profileKinds() []hook.Kind {
	p, err := resolveProfile()
	if err != nil {
		p = profiles[0]
	}
	return hook.Kinds(p.name)
}

// grammarFor maps a loaded table back to its grammar via the spec name the
// engine stamped on it. Deriving it from the table rather than threading a
// second value keeps the pair inseparable: a call site holding a conventional
// table cannot accidentally lint under the gitmoji grammar.
func grammarFor(table *gitmoji.Table) parser.Grammar {
	if table.Spec().Name == "conventional" {
		return parser.GrammarConventional
	}
	return parser.GrammarGitmoji
}

// profileName is the run's validated profile name — profileKinds' sibling for
// callers that need the name itself, with the same post-validation contract.
func profileName() string {
	p, err := resolveProfile()
	if err != nil {
		p = profiles[0]
	}
	return p.name
}
