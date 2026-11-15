package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// This file holds glyph.toml's keys to the schema's spelling byte for byte.
// go-toml binds a key to a struct field by its tag and, failing that, by a
// case fold (unmarshaler.go's structPlan.lookupFold at v2.4.3, with no option
// to turn it off), and DisallowUnknownFields reports only a key NO field
// claims — so `Skip = true` read as skip, `[[Patterns]]` as patterns, and a
// file spelling every key in capitals loaded and judged (measured 2026-11-04
// at 2f9428a: `skip = false` followed by `Skip = true` skipped the commit,
// the reverse did not). A key read by a spelling the author did not write is
// the silently-misread key the package head refuses.
//
// The check runs beside the strict decode, over the generic map the same
// bytes decode to: there every key keeps its bytes, and `skip` and `Skip` are
// two entries. The walk is driven by the raw structs' tags — the schema's one
// spelling — so a key added to the schema is held to it with no second list.
// No fold decides whether a key passes: a key is a tag byte for byte or it is
// refused, whatever go-toml would bind it to. The fold is read only to name
// the spelling to write and to descend a miscased table, and it is go-toml's
// own, not strings.EqualFold — the two part at U+0130 `İ`, which
// strings.ToLower maps to `i` and EqualFold does not match to it, so a walker
// that found fields through EqualFold let `"skİp" = true` bind skip and
// walked on (measured 2026-11-08, mutation row
// config-key-walker-skips-a-key-its-own-fold-cannot-bind; go1.26.10: of every
// rune at or above U+0080 whose lower case is ASCII, U+0130 is the one
// EqualFold misses; U+212A KELVIN SIGN it matches).

// checkKeyCase refuses every key whose bytes are not the tag of a field at
// its table, naming the key as written and the spelling the schema gives it.
// Asked after strictUnmarshal, so the bytes parse and every key binds to some
// field: a parse failure or an unknown key is the strict decode's to report,
// in its own words.
func checkKeyCase(data []byte) error {
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil
	}
	var miscased []string
	walkKeys(m, reflect.TypeFor[raw](), "", &miscased)
	switch len(miscased) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("miscased key %s: keys are matched byte for byte, so a key in another case binds nothing — it is refused rather than folded onto the key it resembles", miscased[0])
	}
	return fmt.Errorf("miscased keys %s: keys are matched byte for byte, so a key in another case binds nothing — it is refused rather than folded onto the key it resembles", strings.Join(miscased, ", "))
}

// walkKeys descends v, a decoded TOML value, beside t, the raw type it binds
// to, and appends to miscased each key whose bytes are not a field's tag, as
// `path.Key (write key)`. A miscased table is descended too, by the field
// go-toml's fold binds, so every miscased key under it is named in the same
// refusal. Pointers are looked through; a slice of tables walks each table
// under `path[i]`; scalars and slices of scalars end the walk.
func walkKeys(v any, t reflect.Type, path string, miscased *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		table, ok := v.(map[string]any)
		if !ok {
			return
		}
		for _, key := range slices.Sorted(maps.Keys(table)) {
			at := key
			if path != "" {
				at = path + "." + key
			}
			field, exact := fieldByTag(t, key)
			if !exact {
				*miscased = append(*miscased, spelled(at, field))
			}
			if field == nil {
				continue
			}
			walkKeys(table[key], field.Type, at, miscased)
		}
	case reflect.Slice:
		items, ok := v.([]any)
		if !ok {
			return
		}
		for i, item := range items {
			walkKeys(item, t.Elem(), path+"["+strconv.Itoa(i)+"]", miscased)
		}
	default:
	}
}

// spelled is one refused key for the error: the key as written at its path,
// and the tag to write when a field binds it under go-toml's fold. After
// strictUnmarshal one always does; the bare path is what a key no field
// binds would read as, so the refusal never rests on the fold.
func spelled(at string, field *reflect.StructField) string {
	if field == nil {
		return at
	}
	return fmt.Sprintf("%s (write %s)", at, field.Tag.Get("toml"))
}

// fieldByTag finds the field of t whose toml tag is key byte for byte, exact
// true; failing that, the field go-toml binds the key to, exact false — the
// spelling to write, never a field the key is held to have matched; nil,
// false when no field binds the key at all. The fold is go-toml's
// (structPlan.lookupFold, v2.4.3): an ASCII key lowered byte by byte, any
// other through strings.ToLower, which is strings.ToLower in both cases.
func fieldByTag(t reflect.Type, key string) (field *reflect.StructField, exact bool) {
	var folded *reflect.StructField
	lowered := strings.ToLower(key)
	for f := range t.Fields() {
		tag := f.Tag.Get("toml")
		switch {
		case tag == key:
			return &f, true
		case folded == nil && tag == lowered:
			folded = &f
		}
	}
	return folded, false
}
