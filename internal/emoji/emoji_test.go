package emoji

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestTableIsCanonical holds table.json to the bytes the JSON encoder would
// write for it (2-space indent, HTML escaping off, one trailing newline), so
// the file on GitHub and `glyph emoji`'s stdout are the same bytes without
// the command re-encoding anything. Edit the file, then run this: it names
// the first line that differs.
func TestTableIsCanonical(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(Table()); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got, want := JSON(), b.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("table.json is not in canonical form — re-encode it (2-space indent, no HTML escaping, one trailing newline); first differing line: %q", firstDiffLine(string(got), string(want)))
	}
}

// TestOneMeaningOneEmoji is the rule the dictionary exists for, made
// mechanical: a Name appears once, a Code appears once, and a code an entry
// absorbed is neither an entry itself nor absorbed twice. Two entries with
// the same name are two spellings of one meaning, which is exactly the
// hesitation the dictionary was built to remove.
func TestOneMeaningOneEmoji(t *testing.T) {
	names, codes := map[string]string{}, map[string]bool{}
	for _, en := range Table() {
		if en.Code == "" || en.Emoji == "" || en.Name == "" || en.Description == "" {
			t.Errorf("%+v: every field but absorbs is required", en)
		}
		if prev, dup := names[en.Name]; dup {
			t.Errorf("name %q is claimed by both %s and %s — one meaning, one emoji", en.Name, prev, en.Code)
		}
		names[en.Name] = en.Code
		if codes[en.Code] {
			t.Errorf("code %s appears twice", en.Code)
		}
		codes[en.Code] = true
	}
	for _, en := range Table() {
		for _, a := range en.Absorbs {
			if codes[a] {
				t.Errorf("%s absorbs %s, which is also an entry — a code cannot be both a kind and an alias of one", en.Code, a)
			}
			codes[a] = true
		}
	}
}

// TestEveryCodeRendersOnGitHub asks the one question the dictionary cannot
// answer about itself: does GitHub draw this shortcode? The oracle is
// testdata/gemoji.tsv, GET https://api.github.com/emojis as it answered on
// the date in the file's header — every entry's code and every absorbed code
// must be a key there, and the emoji field must be exactly the code points
// the API maps the key to. Refresh the snapshot with the command in its
// header when GitHub's vocabulary moves; never edit it by hand.
func TestEveryCodeRendersOnGitHub(t *testing.T) {
	gemoji := loadGemoji(t)
	for _, en := range Table() {
		cps, ok := gemoji[strings.Trim(en.Code, ":")]
		if !ok {
			t.Errorf("%s is not a GitHub shortcode — GitHub would render it as the literal text", en.Code)
			continue
		}
		if cps == "" {
			t.Errorf("%s is a GitHub-only image with no Unicode; the emoji field cannot carry it", en.Code)
			continue
		}
		if want := decodeCodePoints(t, cps); en.Emoji != want {
			t.Errorf("%s: emoji field is %q, GitHub maps the shortcode to %q (%s)", en.Code, en.Emoji, want, cps)
		}
		for _, a := range en.Absorbs {
			if _, ok := gemoji[strings.Trim(a, ":")]; !ok {
				t.Errorf("%s absorbs %s, which is not a GitHub shortcode", en.Code, a)
			}
		}
	}
}

// loadGemoji reads the snapshot: name<TAB>hex code points joined by "-", or
// "-" alone for a GitHub-only image (returned as the empty string).
func loadGemoji(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("testdata/gemoji.tsv")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, cps, found := strings.Cut(line, "\t")
		if !found {
			t.Fatalf("snapshot line without a tab: %q", line)
		}
		if cps == "-" {
			cps = ""
		}
		m[name] = cps
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if len(m) < 1000 {
		t.Fatalf("snapshot holds %d shortcodes; GitHub reports well over a thousand — the file is truncated", len(m))
	}
	return m
}

func decodeCodePoints(t *testing.T, cps string) string {
	t.Helper()
	var sb strings.Builder
	for h := range strings.SplitSeq(cps, "-") {
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			t.Fatalf("snapshot code point %q: %v", h, err)
		}
		sb.WriteRune(rune(n))
	}
	return sb.String()
}

func firstDiffLine(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return al[i]
		}
	}
	if len(al) < len(bl) {
		return bl[len(al)]
	}
	if len(bl) < len(al) {
		return al[len(bl)]
	}
	return ""
}
