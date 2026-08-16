package workflows

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// glyphInvocationRE finds an executable line that runs the glyph binary
// against the caller's repository — the verdict and lint surfaces a profile
// must reach. Anchored to `glyph` as the line's first token: every real
// invocation in the reusables writes it that way, and a looser prefix matched
// input-description PROSE ("Override the glyph release whose…"), which
// code() does not strip because a YAML description is not a comment. It
// deliberately matches only the judgement subcommands: `glyph rules` /
// `version` read no commits, and the composite install action is not a
// binary invocation at all.
var glyphInvocationRE = regexp.MustCompile(`(?m)^\s*glyph (?:lint|bump|notes|preview|release|"\$\{args\[@\]\}")`)

// profileInputRE is the input declaration each reusable must carry: a
// `profile:` key followed (within the same inputs block, loosely: within a few
// lines) by `default: gitmoji`. The two are matched together so a declared
// input whose default silently changed — flipping every caller that says
// nothing onto another vocabulary — fails here, not fleet-wide.
var profileInputRE = regexp.MustCompile(`(?ms)^ {6}profile:\n(?: {8}.*\n)*? {8}default: gitmoji$`)

// TestReusablesThreadTheProfile pins the distribution half of DESIGN §6's
// profile decision: every reusable declares the `profile` input defaulting to
// gitmoji, and every glyph invocation that judges commits threads it. The
// second half is the one a green run cannot show: a new glyph call added
// without the flag would judge a conventional caller's commits under the
// gitmoji grammar — exit 3 on every commit, or worse, a verdict computed from
// a vocabulary the repository does not write. The fleet never trips this (its
// callers say nothing and the default holds); only the conventional caller
// would, which is exactly the dogfood gap §2.2 names.
func TestReusablesThreadTheProfile(t *testing.T) {
	// Positive control: the invocation matcher must still recognise the shapes
	// the workflows actually write, or every per-file loop below would pass
	// over an empty match set.
	for _, canary := range []string{
		`          glyph lint --range "$BASE..$HEAD" --profile "$PROFILE" 2>/tmp/x || status=$?`,
		`          glyph preview --pr "$PR_NUMBER" --notes --json --profile "$PROFILE" \`,
		`          glyph "${args[@]}" > "$RUNNER_TEMP/verdict.json" 2> "$RUNNER_TEMP/verdict.err" || status=$?`,
	} {
		if !glyphInvocationRE.MatchString(canary) {
			t.Fatalf("glyphInvocationRE no longer matches a real invocation line (%q); re-derive it "+
				"from the reusables before trusting anything below", canary)
		}
	}

	for _, name := range reusables {
		t.Run(name, func(t *testing.T) {
			raw := repoFile(t, filepath.Join(".github", "workflows", name))
			body := code(raw)

			if !profileInputRE.MatchString(raw) {
				t.Errorf("%s does not declare the `profile` input with `default: gitmoji` — a caller "+
					"that says nothing must keep meaning the fleet's own vocabulary (DESIGN §6)", name)
			}

			for line := range strings.SplitSeq(body, "\n") {
				if !glyphInvocationRE.MatchString(line) {
					continue
				}
				// release.yml builds its argv as an array; the flag must be in
				// the array literal, which the invocation line itself cannot
				// show — assert it on the file instead, right below.
				if strings.Contains(line, `"${args[@]}"`) {
					continue
				}
				if !strings.Contains(line, `--profile "$PROFILE"`) {
					t.Errorf("%s runs glyph without threading the profile:\n  %s\n"+
						"every judging invocation must carry `--profile \"$PROFILE\"`, or a conventional "+
						"caller's commits are judged under the gitmoji grammar", name, strings.TrimSpace(line))
				}
				// The env mapping is the other half of the thread: the flag
				// reads $PROFILE, which exists only if the step maps it.
				if !strings.Contains(body, "PROFILE: ${{ inputs.profile }}") {
					t.Errorf("%s uses $PROFILE but never maps `PROFILE: ${{ inputs.profile }}` into a step env", name)
				}
			}

			if name == "release.yml" {
				for _, want := range []string{
					`args=(release --json --footer-file "$RUNNER_TEMP/install-notes.md" --profile "$PROFILE")`,
					`args=(release --dry-run --json --footer-file "$RUNNER_TEMP/install-notes.md" --profile "$PROFILE")`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("release.yml's argv array is missing the profile flag (want %q) — the "+
							"invocation line only shows \"${args[@]}\", so the array is where the thread lives", want)
					}
				}
			}
		})
	}
}
