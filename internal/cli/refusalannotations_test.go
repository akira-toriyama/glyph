package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/bump"
	"github.com/akira-toriyama/glyph/v6/internal/testutil"
)

// TestVerdictRefusalsAnnotateEachRefusedCommit holds the producer half of the
// stream contract for the verdict commands: a refusal of bump, notes, preview
// or release writes one ::error:: line per row in its details, in order, before
// the envelope — the line lint --range writes for a finding. The envelope's
// message names the first row only and pr-verdict.yml frames that message
// alone, so before this every refused commit past the first reached no
// workflow log. stdout is asserted empty: a refusal's stdout, --json included,
// is what it was.
func TestVerdictRefusalsAnnotateEachRefusedCommit(t *testing.T) {
	dir, base := testutil.NewTaggedRepo(t)
	testutil.Commit(t, dir, "akira-toriyama", "first without any pattern shape")
	first := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	testutil.Commit(t, dir, "akira-toriyama", "no gitmoji in this one")
	second := testutil.Git(t, dir, "akira-toriyama", "rev-parse", "HEAD")
	t.Chdir(dir)
	usePR(t, dryServer(t, map[string]string{
		commitPullsPath(first):  `[]`,
		commitPullsPath(second): `[]`,
		pullCommitsPath(7): `[` + apiCommit("aaa1111", "akira-toriyama", "no sigil one") + `,` +
			apiCommit("bbb2222", "akira-toriyama", "no sigil two") + `]`,
	}))

	rng := base + "..HEAD"
	for _, args := range [][]string{
		{"bump", "--range", rng},
		{"bump", "--range", rng, "--json"},
		{"notes", "--range", rng},
		{"bump", "--pr", "7"},
		{"notes", "--pr", "7", "--json"},
		{"bump", "--since-tag"},
		{"notes", "--since-tag"},
		{"preview", "--pr", "7"},
		{"preview", "--pr", "7", "--json"},
		{"release", "--dry-run"},
		{"release", "--dry-run", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runGlyph(t, args...)
			if code != 3 {
				t.Fatalf("exited %d, want 3\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal wrote to stdout:\n%s", stdout)
			}
			cut := strings.Index(stderr, "\n{")
			if cut < 0 {
				t.Fatalf("no line opens the envelope:\n%s", stderr)
			}
			var env struct {
				Error struct {
					Code    int            `json:"code"`
					Details []bump.Refusal `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(stderr[cut+1:]), &env); err != nil {
				t.Fatalf("the envelope does not parse: %v\n%s", err, stderr)
			}
			if env.Error.Code != 3 || len(env.Error.Details) != 2 {
				t.Fatalf("envelope code %d with %d details, want 3 with 2:\n%s", env.Error.Code, len(env.Error.Details), stderr)
			}
			var got []string
			for _, l := range strings.Split(stderr[:cut], "\n") {
				if strings.HasPrefix(l, "::error::") {
					got = append(got, l)
				}
			}
			var want []string
			for _, r := range env.Error.Details {
				want = append(want, "::error::glyph: commit "+r.SHA[:7]+": "+r.Detail)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("want one ::error:: per detail row, in order, before the envelope:\nwant:\n%s\ngot:\n%s\nstderr:\n%s",
					strings.Join(want, "\n"), strings.Join(got, "\n"), stderr)
			}
		})
	}
}
