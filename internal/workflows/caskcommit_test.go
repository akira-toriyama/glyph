package workflows

import (
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v4/internal/config"
)

// goreleaserDefaultCaskSubject is the commit subject GoReleaser writes to the
// tap when homebrew_casks carries no commit_msg_template.
const goreleaserDefaultCaskSubject = "Brew cask update for {{ .ProjectName }} version {{ .Tag }}"

var caskCommitTemplate = regexp.MustCompile(`(?m)^\s+commit_msg_template:\s*"([^"]*)"\s*$`)

// TestCaskCommitSubjectLintsInTheTap: the commit GoReleaser pushes to
// akira-toriyama/homebrew-tap on every release is judged by the tap's own
// commit-lint, under the gemoji preset. With no template GoReleaser writes its
// default subject, which matches no pattern — measured on the tap: 5bb48cd
// "Brew cask update for glyph version v4.1.0" exits 3. The rendered template
// must lint clean; the default subject failing under the same preset is the
// positive control that the preset would catch the regression.
func TestCaskCommitSubjectLintsInTheTap(t *testing.T) {
	body := code(repoFile(t, ".goreleaser.yaml"))
	i := strings.Index(body, "\nhomebrew_casks:\n")
	if i < 0 {
		t.Fatal(".goreleaser.yaml has no homebrew_casks block — the tap push moved and this guard asserts nothing")
	}
	block := body[i+1:]
	if j := regexp.MustCompile(`(?m)^[a-z_]+:`).FindAllStringIndex(block, 2); len(j) == 2 {
		block = block[:j[1][0]]
	}

	data, ok := config.Preset("gemoji")
	if !ok {
		t.Fatal("no gemoji preset")
	}
	cfg, err := config.Load(data)
	if err != nil {
		t.Fatalf("loading the gemoji preset: %v", err)
	}
	render := func(tmpl string) string {
		s := strings.NewReplacer("{{ .ProjectName }}", "glyph", "{{ .Tag }}", "v4.1.0").Replace(tmpl)
		if strings.Contains(s, "{{") {
			t.Fatalf("template %q uses a field this guard does not render — extend the replacer", tmpl)
		}
		return s
	}

	if v := cfg.Lint(render(goreleaserDefaultCaskSubject), ""); v.OK {
		t.Fatal("GoReleaser's default cask subject lints clean under the gemoji preset — the positive control is dead, so this guard can no longer tell a missing template")
	}

	m := caskCommitTemplate.FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("homebrew_casks sets no commit_msg_template, so GoReleaser writes %q to the tap, which the tap's lint rejects", render(goreleaserDefaultCaskSubject))
	}
	subject := render(m[1])
	if v := cfg.Lint(subject, ""); !v.OK {
		t.Errorf("the tap commit subject %q does not lint under the gemoji preset: %s", subject, v.Reason)
	}
}
