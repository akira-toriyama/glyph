// Package conventional is the conventional profile's vocabulary data
// (DESIGN §2.2/§3.1): the embedded type → semver table, its count pin, and
// its Spec for the shared table engine in internal/gitmoji. It holds no logic
// of its own on purpose — validation, lookup and the renderers are the
// engine's, so the two vocabularies cannot fork on what a well-formed table
// is; what lives here is only the data and the two facts about it worth
// pinning: the type set is closed at TypeCount, and every row is DERIVED from
// its canonical gitmoji counterpart (the derivation test in this package is
// DESIGN §3.1's table in executable form — a dispute about a conventional row
// is a dispute about the gitmoji row it derives from).
package conventional

import (
	_ "embed"
	"fmt"
	"regexp"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
)

// rawRules is the pinned conventional type → semver table. //go:embed binds it
// at compile time, so the shipped binary is the shipped rules — zero skew,
// exactly as the gitmoji table ships.
//
//go:embed rules.json
var rawRules []byte

// TypeCount is the size of the ratified conventional type set — CodeCount's
// counterpart (DESIGN §2.2). The eleven types are, measured 2026-08-16, the
// same set the gitmoji grammar's legacyTokenRE retires and commitlint's
// config-conventional enforces. Growing the set is a deliberate act: bump this
// and rules.json together so a dropped or added type can never slip in
// silently. Load fails if the embedded table is not this size.
const TypeCount = 11

// spec is the conventional vocabulary's Spec for the shared engine. The token
// shape is open beyond the ratified set on purpose — it mirrors the grammar's
// own open type slot (membership is the table's question, shape is not), and
// ParseTable applies it to TABLE entries, where the closed set is enforced by
// TypeCount plus the derivation test instead.
var spec = gitmoji.Spec{
	Name:    "conventional",
	Token:   "type",
	TokenRE: regexp.MustCompile(`^[a-z][a-z0-9-]*$`),
	Emoji:   false,
	Title:   "conventional type → semver",
	Command: "glyph rules --profile=conventional --md",
}

// Load parses and validates the embedded conventional table. It fails if the
// table is structurally invalid or not exactly TypeCount types — a
// build/embedding error, surfaced at startup rather than as a silent
// misclassification later. The returned table is the same *gitmoji.Table the
// gitmoji vocabulary loads into, which is what lets every consumer — Classify,
// Group, the renderers — take either vocabulary without knowing which it holds.
func Load() (*gitmoji.Table, error) {
	t, err := gitmoji.ParseTable(rawRules, spec)
	if err != nil {
		return nil, err
	}
	if len(t.Codes) != TypeCount {
		return nil, fmt.Errorf("conventional: embedded table has %d types, want %d", len(t.Codes), TypeCount)
	}
	return t, nil
}
