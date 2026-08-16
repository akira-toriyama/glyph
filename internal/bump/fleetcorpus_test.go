package bump

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/internal/gitmoji"
	"github.com/akira-toriyama/glyph/internal/parser"
)

// The fleet corpus gate.
//
// Every repository in the fleet runs the pinned glyph over its own history to
// decide what version to cut. That makes the PARSER's acceptance range a
// versioning contract, and the contract has no other defender: a change that
// narrows what Parse recognises does not fail — it succeeds differently, and
// the repository ships a smaller version than it owed. `lint` cannot catch it
// (the release walk calls Parse, never Lint, and bot commits are lint-exempt),
// and the unit tests cannot either, because they assert against subjects this
// repository invented.
//
// Measured while writing this: removing the one line in parser.go that lifts
// `!` out of a retired Conventional token (`c.Breaking = c.Breaking || lm[3]
// == "!"`) turns a major release into a patch, and — with only that commit in
// the range — into no release at all. Three tests fail, all of them in
// internal/parser, all of them about the STRUCT. internal/bump, internal/cli
// and internal/notes stay green: the consequence the fleet actually feels, a
// version number silently falling, is asserted nowhere. There is no mutation
// ledger row for that line either.
//
// So this gate freezes the fleet's REAL subjects — 3,053 distinct ones, from
// the public repositories — against the verdict each one produces today.
//
// The measurement that justifies the file, stated exactly, because the loose
// version of it was wrong by 2.3x: 75 of the 83 breaking rows take their
// breaking-ness ONLY from a legacy `!`, but 42 of those are `:boom:`, which the
// table already levels major on the gitmoji alone — their version does not move.
// The commits that actually FALL A LEVEL when the lift is removed number 33.
// Thirty-three real releases, each one shipping smaller than it owed, is the
// thing being defended; "nine in ten" was a count of exposure and read as a
// count of harm.
//
// ─── Why there is no -update ─────────────────────────────────────────────────
// A golden with a rewrite flag defends nothing here. The failure mode is not a
// typo, it is an author who narrows the parser, sees a wall of red, and
// regenerates. That is one keystroke, it looks like maintenance, and it is
// exactly the event this file exists to make impossible to perform absently.
//
// The corpus is APPEND-ONLY instead. TestFleetCorpusAppend adds subjects the
// fleet has written since the last refresh and never touches a stored verdict,
// so keeping up with the fleet stays cheap while changing what a subject MEANS
// stays a hand edit — one that shows up in review as a changed verdict column
// rather than as a regenerated file. The appender refuses to run at all unless
// the existing corpus already verifies, so a broken tree cannot bake its own
// output in as the new truth.
//
// ─── Privacy ────────────────────────────────────────────────────────────────
// Public repositories only. glyph is public, six of the fleet's thirty-five are
// not, and committing a private repository's commit subjects here would publish
// them. scripts/fleet-corpus.sh filters on `gh repo list --json isPrivate`, and
// the loss is nothing the gate needs: the shapes that matter appear in the
// public twenty-nine.

// corpusPath is the frozen corpus, relative to this package.
var corpusPath = filepath.Join("testdata", "fleet-corpus.tsv")

// Positive controls. Anything asserting an absence needs one, or the gate
// greens on an empty file and reports it as coverage (CLAUDE.md; the same rule
// that put canaries in internal/workflows' guards).
//
// These are FLOORS, not exact counts, because the corpus grows by design — the
// exact assertion is the row-by-row comparison below, which pins every verdict
// individually. Their job is the coarser one: catch a refresh that silently
// emptied a category. Measured at the corpus committed alongside this file.
const (
	minCorpusRows = 3000 // 3053 present
	minBreaking   = 80   // 83 present
	minLegacy     = 2100 // 2202 present
	// The sharpest one: rows breaking ONLY because a retired Conventional
	// token carried the `!`, i.e. the exact path parser.go's lift keeps alive.
	// Without one of these the gate is vacuous for the defect it exists for.
	// (Exposure is 75; the subset whose LEVEL actually falls when the lift goes
	// is 33 — the header says why the difference matters.)
	minLegacyBangOnly = 70 // 75 present
)

// newSlotBangRE matches the CURRENT grammar's breaking marker, the `!` that
// sits before the mandatory space. A row that is breaking without one, and
// without a footer, took its breaking-ness from the legacy token. "Without a
// footer" was an assumption about the collector until loadCorpus started
// REJECTING a multi-line raw — a body carrying `BREAKING CHANGE:` would
// otherwise be representable, loadable, and counted toward minLegacyBangOnly
// while having no legacy token at all.
var newSlotBangRE = regexp.MustCompile(`^:[a-z0-9][a-z0-9_+-]*:(\([^()]*\))?! `)

// hasLegacyToken asks the shipped lint rule rather than re-deriving the
// pattern. A second copy of legacyTokenRE here would drift from the real one
// exactly when it mattered — the day someone edits it — and this gate would
// keep counting the category it was no longer measuring. nil Known skips the
// gitmoji membership rule only, which is the one rule irrelevant here.
func hasLegacyToken(raw string) bool {
	for _, v := range parser.Lint(raw, parser.LintOptions{}) {
		if v.Rule == parser.RuleLegacyToken {
			return true
		}
	}
	return false
}

// corpusRow is one frozen (subject → verdict) pair.
type corpusRow struct {
	line     int
	raw      string // the real commit subject, as written
	level    string // the bump level, or a !-prefixed refusal
	breaking string // "t", "f", or "-" when there is no verdict
	gitmoji  string
	scope    string // "-" when absent
	subject  string // Parse's subject: markers and any legacy token stripped
}

// verdict quotes the SCOPE too. The header used to claim a tab could never split
// a row because "the two subject fields are quoted", and that was false: a scope
// salvaged from a retired token comes out of legacyTokenRE's `\([^()]+\)` slot,
// which admits a tab, so `:bug: fix(a<TAB>b): thing` emitted a seven-field row
// the loader then rejected. Anything that can carry an arbitrary byte is quoted;
// level, breaking and gitmoji are drawn from closed vocabularies and cannot.
func (r corpusRow) verdict() string {
	return strings.Join([]string{
		r.level, r.breaking, r.gitmoji, strconv.Quote(r.scope), strconv.Quote(r.subject),
	}, "\t")
}

func (r corpusRow) String() string {
	return strconv.Quote(r.raw) + "\t" + r.verdict()
}

// classify is the whole chain a release walk runs, for one subject. It never
// returns an error: a refusal IS a verdict here, and freezing it is the point —
// a parser that starts ACCEPTING something it used to reject has widened the
// acceptance range just as consequentially as one that narrows it.
func classifyCorpusSubject(raw string, table *gitmoji.Table) corpusRow {
	row := corpusRow{raw: raw, breaking: "-", gitmoji: "-", scope: "-"}
	c, err := parser.Parse(raw, parser.GrammarGitmoji)
	if err != nil {
		row.level = "!parse"
		return row
	}
	row.gitmoji = c.Token
	row.subject = c.Subject
	if c.Scope != "" {
		row.scope = c.Scope
	}
	if c.Breaking {
		row.breaking = "t"
	} else {
		row.breaking = "f"
	}
	level, err := Classify(c, table)
	if err != nil {
		row.level = "!unknown-gitmoji"
		return row
	}
	row.level = string(level)
	return row
}

func loadCorpus(t *testing.T) []corpusRow {
	t.Helper()
	f, err := os.Open(corpusPath)
	if err != nil {
		t.Fatalf("reading the fleet corpus %s: %v\n"+
			"This gate is the only thing pinning the parser's acceptance range against the\n"+
			"fleet's real history. Regenerate it with `sh scripts/fleet-corpus.sh`.", corpusPath, err)
	}
	defer f.Close()

	var rows []corpusRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 6 {
			t.Fatalf("%s:%d: want 6 tab-separated fields, got %d.\n"+
				"The two subject fields are strconv.Quote'd precisely so a tab inside a real\n"+
				"commit subject cannot split a row; a raw tab here means the file was hand-edited\n"+
				"without quoting.\nline: %s", corpusPath, n, len(parts), line)
		}
		raw, err := strconv.Unquote(parts[0])
		if err != nil {
			t.Fatalf("%s:%d: field 1 is not a quoted Go string: %v", corpusPath, n, err)
		}
		scope, err := strconv.Unquote(parts[4])
		if err != nil {
			t.Fatalf("%s:%d: field 5 is not a quoted Go string: %v", corpusPath, n, err)
		}
		subject, err := strconv.Unquote(parts[5])
		if err != nil {
			t.Fatalf("%s:%d: field 6 is not a quoted Go string: %v", corpusPath, n, err)
		}
		// A stored subject must be ONE line. The legacy-bang-only control below
		// reasons that a breaking row with no new-slot `!` took its breaking-ness
		// from a legacy token, and that only holds while no row can carry a
		// BREAKING CHANGE footer. git's %s gives one line by construction, so
		// this can only fail on a hand edit — which is exactly when the control
		// would otherwise start counting the wrong thing, silently.
		if strings.ContainsAny(raw, "\n\r") {
			t.Fatalf("%s:%d: the stored subject spans lines. A corpus row is one commit SUBJECT;\n"+
				"a body can carry a BREAKING CHANGE footer, which would make a row breaking\n"+
				"without a legacy token and corrupt the minLegacyBangOnly control.\n  %s",
				corpusPath, n, strconv.Quote(raw))
		}
		rows = append(rows, corpusRow{
			line: n, raw: raw,
			level: parts[1], breaking: parts[2], gitmoji: parts[3], scope: scope,
			subject: subject,
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning %s: %v", corpusPath, err)
	}
	return rows
}

// TestFleetCorpus is the gate. It recomputes every stored subject and compares
// the whole verdict, then separates out the one drift that may never be waived
// by editing a column.
//
// bite-exempt: ratifies the tree's current behaviour by construction — it
// accompanies no source change and therefore cannot fail without one. Its whole
// purpose is to make a FUTURE narrowing of the parser fail, and that it does so
// is proven where this repo proves such things: ledger rows 32 and 33, which
// break parser.go's legacy-`!` lift and require this test to go red.
func TestFleetCorpus(t *testing.T) {
	rows := loadCorpus(t)
	table := loadTable(t)

	// The one control that must run FIRST and must be fatal: it is about the
	// FILE, and every assertion after it is about rows agreeing — on an empty or
	// gutted corpus those all agree vacuously and the run is green.
	if len(rows) < minCorpusRows {
		t.Fatalf("the corpus holds %d rows, below the floor of %d.\n"+
			"Rows are only ever appended, so a shrunken corpus means rows were deleted —\n"+
			"which is how this gate is silenced without a single assertion changing.",
			len(rows), minCorpusRows)
	}

	// The category counts are DEFERRED to the end of this function, and are
	// t.Errorf rather than t.Fatalf, because they are derived from the tree
	// under test — hasLegacyToken asks the shipped lint rule. Run first and
	// fatal, they pre-empted the report that matters: deleting legacyTokenRE
	// outright drops `legacy` to zero, which killed the test before the
	// row-by-row comparison ever ran, so the narrowing most likely to happen
	// was reported as "the corpus lost a category" instead of as the 33 real
	// releases it silently shrinks.
	var breaking, legacy, legacyBangOnly int
	for _, r := range rows {
		isLegacy := hasLegacyToken(r.raw)
		if isLegacy {
			legacy++
		}
		if r.breaking != "t" {
			continue
		}
		breaking++
		// "Breaking ONLY via a legacy `!`" is BOTH halves: no `!` in the current
		// grammar's slot AND a retired token actually present. Counting the
		// first half alone was a proxy that happens to agree on today's corpus
		// and would stop agreeing the moment a row acquired breaking-ness some
		// other way — which is precisely when a positive control must not
		// quietly keep counting.
		if isLegacy && !newSlotBangRE.MatchString(r.raw) {
			legacyBangOnly++
		}
	}
	// Reported after the comparison below — see the note where they are counted.
	defer func() {
		for _, c := range []struct {
			got, want int
			what, why string
		}{
			{breaking, minBreaking, "breaking rows",
				"with none, `breaking: true -> false` can never be observed and the report above is decoration"},
			{legacy, minLegacy, "rows carrying a retired Conventional token",
				"the legacy path is what the fleet's pre-glyph history is made of; a corpus without it pins nothing about that history"},
			{legacyBangOnly, minLegacyBangOnly, "rows breaking ONLY via a legacy `!`",
				"this is the exact path parser.go's `c.Breaking = c.Breaking || lm[3] == \"!\"` keeps alive — without one of these the gate is vacuous for the defect it exists for"},
		} {
			if c.got < c.want {
				t.Errorf("the corpus holds %d %s, below the floor of %d — %s.\n"+
					"This count is derived from the TREE, not the file, so it can also mean the code that\n"+
					"recognises the category was removed. Read any report above this one first.",
					c.got, c.what, c.want, c.why)
			}
		}
	}()

	// The exact assertion: every stored verdict, recomputed.
	var lost []corpusRow
	var lostLevel []int
	membership := 0
	drifted := 0
	for _, r := range rows {
		got := classifyCorpusSubject(r.raw, table)
		if got.verdict() == r.verdict() {
			continue
		}
		// A commit that WAS breaking and no longer is gets its own report,
		// because its consequence is not "a golden moved" but "every repo
		// carrying this shape now cuts a smaller version than it owes", in
		// silence, having changed nothing itself.
		if r.breaking == "t" && got.breaking != "t" {
			lost = append(lost, r)
			if got.level != r.level {
				lostLevel = append(lostLevel, r.line)
			}
			continue
		}
		// The level column comes from Classify, so the corpus freezes gitmoji
		// TABLE MEMBERSHIP as well as the parser's acceptance range. That is
		// deliberate — adding a code changes the version verdict of every fleet
		// commit already carrying it, which is exactly the kind of change
		// CodeCount exists to keep deliberate — but the drift must say so.
		// Reported generically it sends the reader to the parser, and the fleet
		// has 77 such rows waiting (74 of them `:robot:`, which fleet-sync
		// writes today), so the wrong-subsystem message was the likely one.
		if r.level == "!unknown-gitmoji" || got.level == "!unknown-gitmoji" {
			membership++
			if membership <= 5 {
				t.Errorf("%s:%d: the GITMOJI TABLE changed, not the parser — %s is %s now and was %s\n"+
					"  subject: %s\n"+
					"  Adding or removing a code in internal/gitmoji/rules.json re-levels every fleet\n"+
					"  commit already carrying it. That is a real fleet-visible change, so it belongs in\n"+
					"  the diff: update these rows in the same commit that moves CodeCount.",
					corpusPath, r.line, r.gitmoji, got.level, r.level, strconv.Quote(r.raw))
			}
			continue
		}
		drifted++
		if drifted <= 20 {
			t.Errorf("%s:%d: verdict drifted for a real fleet commit subject\n  subject: %s\n  want:    %s\n  got:     %s",
				corpusPath, r.line, strconv.Quote(r.raw), r.verdict(), got.verdict())
		}
	}
	if membership > 5 {
		t.Errorf("%s: %d rows re-levelled by a gitmoji-table change in total (first 5 shown).", corpusPath, membership)
	}
	if drifted > 20 {
		t.Errorf("%s: %d rows drifted in total (first 20 shown).\n"+
			"A whole-file drift is a change to what the parser ACCEPTS, not a typo. There is no\n"+
			"-update: decide what the new verdicts should be and edit the affected rows by hand,\n"+
			"so review sees changed verdicts rather than a regenerated file.", corpusPath, drifted)
	}

	if len(lost) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d commit subject(s) STOPPED being breaking, and %d of them now take a LOWER LEVEL.\n\n",
			len(lost), len(lostLevel))
		b.WriteString("This is the one drift that is never a formatting change. Every fleet repository\n" +
			"whose unreleased range holds one of the level-losing subjects now cuts a SMALLER\n" +
			"version than it owes — a major landing as a patch, or as no release at all — with no\n" +
			"error, no warning, and no change of its own. It is the failure this gate was written\n" +
			"for.\n\n" +
			"The two counts differ and the difference is the point: a subject whose gitmoji already\n" +
			"levels major (`:boom:`) loses its `breaking` flag without losing its version, so the\n" +
			"larger number is EXPOSURE and the smaller one is HARM. Quote the smaller one.\n\n")
		for i, r := range lost {
			if i == 10 {
				fmt.Fprintf(&b, "  … and %d more\n", len(lost)-10)
				break
			}
			now := classifyCorpusSubject(r.raw, table)
			mark := "  "
			if now.level != r.level {
				mark = "! "
			}
			fmt.Fprintf(&b, "  %s%s:%d  %s -> %s  %s\n",
				mark, corpusPath, r.line, r.level, now.level, strconv.Quote(r.raw))
		}
		b.WriteString("\n  (rows marked ! lose a level; the rest lose only the flag)\n")
		b.WriteString("\nIf the loss is genuinely intended, say so where it can be argued: change the\n" +
			"minBreaking / minLegacyBangOnly floors in this file, in the same commit, with the\n" +
			"reason in the message. Editing the corpus rows alone is not enough and is not meant\n" +
			"to be — the floors are the part a reviewer reads.")
		t.Fatal(b.String())
	}
}

// TestFleetCorpusAppend refreshes the corpus with subjects the fleet has
// written since the last run. It is the ONLY writer, it only ever appends, and
// it does nothing unless GLYPH_FLEET_SUBJECTS names a file of raw subjects —
// so the gate above stays hermetic and this stays a deliberate act.
// scripts/fleet-corpus.sh produces that file; it owns the part that needs the
// network and a clone of the fleet, which is not a thing a test should own.
//
// bite-exempt: a generator, not an assertion. It skips unless asked to write,
// so it can neither pass nor fail against any tree.
func TestFleetCorpusAppend(t *testing.T) {
	src := os.Getenv("GLYPH_FLEET_SUBJECTS")
	if src == "" {
		t.Skip("set GLYPH_FLEET_SUBJECTS to a file of raw subjects to append; `sh scripts/fleet-corpus.sh` does it")
	}

	rows := loadCorpus(t)
	table := loadTable(t)

	// Refuse to extend a corpus that does not already verify. Otherwise a tree
	// mid-change appends ITS verdicts as the new truth, and the drift this gate
	// exists to catch is committed as data rather than reported as a failure.
	for _, r := range rows {
		if got := classifyCorpusSubject(r.raw, table); got.verdict() != r.verdict() {
			t.Fatalf("refusing to append: %s:%d already disagrees with this tree.\n"+
				"Run TestFleetCorpus and settle that first — appending now would bake this tree's\n"+
				"verdicts in as the frozen ones.\n  want: %s\n  got:  %s",
				corpusPath, r.line, r.verdict(), got.verdict())
		}
	}

	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		seen[r.raw] = true
	}

	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading GLYPH_FLEET_SUBJECTS=%s: %v", src, err)
	}
	var added []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		added = append(added, line)
	}
	if len(added) == 0 {
		t.Logf("nothing to append: every subject in %s is already frozen", src)
		return
	}
	sort.Strings(added)

	f, err := os.OpenFile(corpusPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening %s for append: %v", corpusPath, err)
	}
	defer f.Close()
	for _, s := range added {
		if _, err := fmt.Fprintln(f, classifyCorpusSubject(s, table).String()); err != nil {
			t.Fatalf("appending to %s: %v", corpusPath, err)
		}
	}
	t.Logf("appended %d subject(s) to %s — read the diff as the specification it is, then\n"+
		"raise the floors in this file if a category grew in a way worth pinning", len(added), corpusPath)
}
