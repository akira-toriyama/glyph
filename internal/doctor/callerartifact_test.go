package doctor

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// artifactAt runs the caller-artifact check over root's workflows, read as
// Run reads them, against the loaded cfg.
func artifactAt(root string, verified bool, cfg *config.Config) Check {
	workflows, err := readWorkflows(root, verified)
	return checkCallerArtifact(root, verified, workflows, err, cfg)
}

// TestCallerArtifactOutcomes walks the verdicts over real files: the measured
// incident — a release caller passing `binary` on a repository that declares
// [[packages]] (glyph-monorepo-test-2 run 41805408808, 2026-11-04: three
// drafts rewritten before the refusal) — in both input spellings and both
// with: forms, the healthy shapes, and the not-a-caller cases.
//
// The check judges the VALUE, because the reusable does (its refusal runs
// for a non-empty input): judged by the key alone, a caller the reusable
// runs green failed here at exit 3 — `binary: ""` on glyph-monorepo-test-2
// run 42450511719 and a bare `binary:` on run 43678383110 both skipped the
// refusal step and succeeded. A value the checkout does not hold is unknown,
// never a verdict either way. The opposite error is the worse one: a value on
// the line under a commented key read as empty passed a caller the reusable
// refused (run 43689423925).
func TestCallerArtifactOutcomes(t *testing.T) {
	packages := loaded(t, packagesConfig("camp", "travel"))
	single := loaded(t, minimalConfig)
	tests := []struct {
		name       string
		files      map[string]string
		cfg        *config.Config
		want       Status
		wantDetail string
	}{
		{
			name: "the measured incident: binary on a packages repository",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: probe\n      dry-run: false\n")},
			cfg: packages, want: StatusFail, wantDetail: "with binary, and this repository declares 2 [[packages]]",
		},
		{
			name: "app on a packages repository",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      app: MyApp\n")},
			cfg: packages, want: StatusFail, wantDetail: "with app",
		},
		{
			name: "flow-form with: { app: MyApp }",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: { app: MyApp, dry-run: false }")},
			cfg: packages, want: StatusFail, wantDetail: "with app",
		},
		{
			name: "a packages caller passing neither",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      dry-run: false\n      install-notes: |\n        ## Install\n        app: not a key, a heredoc line\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name:  "a packages caller with no with: at all",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "")},
			cfg:   packages, want: StatusPass,
		},
		{
			name: "a single-line repository may name an artifact",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: mytool\n")},
			cfg: single, want: StatusPass,
		},
		{
			name: "another reusable's caller is not judged",
			files: map[string]string{".github/workflows/commit-lint.yml": callerWith("lint.yml",
				"    with:\n      app: irrelevant\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name:  "no glyph caller at all",
			files: map[string]string{".github/workflows/ci.yml": "name: ci\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"},
			cfg:   packages, want: StatusPass,
		},
		{
			name: "a literal behind a trailing comment is still named",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: probe  # the tool\n")},
			cfg: packages, want: StatusFail, wantDetail: "with binary",
		},
		{
			name: "a plain scalar on the line under its key is still named",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary:\n        probe\n      dry-run: false\n")},
			cfg: packages, want: StatusFail, wantDetail: "with binary",
		},
		{
			name: "a value under a key line that carries a comment is still named (run 43689423925)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: # the tool build.sh produces\n        probe\n")},
			cfg: packages, want: StatusFail, wantDetail: "with binary",
		},
		{
			name: "a quoted null spelling is a string, and named (run 43690585767)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: \"null\"\n")},
			cfg: packages, want: StatusFail, wantDetail: "with binary",
		},
		{
			name: "~ and null spell no value (runs 43689418881, 43689421291)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: ~\n      app: null  # nothing to build\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "Null and NULL spell no value (run 43690575673)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      app: Null\n      binary: NULL\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "flow-form with: { binary: \"\" } behind a comment",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: { binary: \"\" }  # nothing to build")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "an empty quoted value names no artifact (run 42450511719)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: \"\"\n      app: ''  # nothing here\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "an input with no value names no artifact (run 43678383110)",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      dry-run: false\n      binary:\n")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "flow-form with: { binary: \"\" }",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with: { binary: \"\", dry-run: false }")},
			cfg: packages, want: StatusPass,
		},
		{
			name: "an expression is a value the checkout does not hold",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: ${{ vars.RELEASE_BINARY }}\n")},
			cfg: packages, want: StatusUnknown, wantDetail: "with binary: ${{ vars.RELEASE_BINARY }}",
		},
		{
			name: "a quoted expression is one too",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      app: \"${{ inputs.app }}\"\n")},
			cfg: packages, want: StatusUnknown, wantDetail: "with app",
		},
		{
			name: "a block scalar is text this reader does not read",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: >-\n        ${{ vars.RELEASE_BINARY }}\n")},
			cfg: packages, want: StatusUnknown, wantDetail: "with binary: >-",
		},
		{
			name: "a named artifact beside an expression still fails",
			files: map[string]string{
				".github/workflows/release.yml": callerWith("release.yml", "    with:\n      binary: ${{ vars.RELEASE_BINARY }}\n"),
				".github/workflows/second.yml":  callerWith("release.yml", "    with:\n      app: MyApp\n"),
			},
			cfg: packages, want: StatusFail, wantDetail: "with app, and this repository",
		},
		{
			name:  "a double-quoted key is the key it quotes: binary",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      \"binary\": probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a single-quoted key is the key it quotes: binary",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      'binary': x\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a quoted key in the flow form is the key it quotes",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: { \"binary\": probe }")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a flow mapping that closes on a later line is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: {\n      binary: probe\n    }\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "does not read: job j, line 7 `with: {`",
		},
		{
			name:  "an alias for with: is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    env: &w { binary: probe }\n    with: *w\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "does not read: job j, line 8 `with: *w`",
		},
		{
			name:  "an anchored flow with: is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: &w { binary: probe }\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 7 `with: &w { binary: probe }`",
		},
		{
			name:  "a tagged flow with: is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: !!map { binary: probe }\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 7 `with: !!map { binary: probe }`",
		},
		{
			name:  "a merge key is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      <<: *x\n      dry-run: false\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 8 `<<: *x`",
		},
		{
			name:  "a ? key is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      ? binary\n      : probe\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 8 `? binary`",
		},
		{
			name:  "a key carrying an anchor is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      &k binary: probe\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 8 `&k binary: probe`",
		},
		{
			name:  "a nested collection in the flow form is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: { tags: [a, b], binary: probe }\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "line 7",
		},
		{
			name:  "an alias value is a value the checkout does not hold",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    env:\n      E: &e \"\"\n    with:\n      binary: *e\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "with binary: *e",
		},
		{
			name:  "a comma inside quotes splits no flow entry",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: { install-notes: \"a, binary: b\" }")},
			cfg:   packages, want: StatusPass,
		},
		{
			name:  "an apostrophe in a plain flow value opens no quote",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: { install-notes: don't, binary: probe }")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a literal block scalar is unread",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      binary: |\n        probe\n")},
			cfg:   packages, want: StatusUnknown, wantDetail: "with binary: |",
		},
		{
			name:  "an empty quoted value behind a comment",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      binary: \"\" # c\n      app: '' # don't\n")},
			cfg:   packages, want: StatusPass,
		},
		{
			name:  "a # with no space before it is the value",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      binary: probe#1\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "an anchored literal is named",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      binary: &b probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a comment line between the key and its value",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n      # the tool\n      binary:\n        # build.sh makes it\n        probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a comment on the with: line opens the block form",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with: # inputs\n      binary: probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "keys at a deeper indent",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    with:\n          binary: probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a with: on a step is not the caller's",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "    steps:\n      - uses: actions/checkout@v4\n        with:\n          binary: probe\n")},
			cfg:   packages, want: StatusPass,
		},
		{
			name:  "a with: on another job is not the caller's",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml", "  other:\n    runs-on: ubuntu-latest\n    with:\n      binary: probe\n")},
			cfg:   packages, want: StatusPass,
		},
		{
			name:  "a CRLF file",
			files: map[string]string{".github/workflows/release.yml": crlf(callerWith("release.yml", "    with:\n      binary: probe\n"))},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name:  "a leading document marker",
			files: map[string]string{".github/workflows/release.yml": "---\n" + callerWith("release.yml", "    with:\n      binary: probe\n")},
			cfg:   packages, want: StatusFail, wantDetail: "with binary,",
		},
		{
			name: "a named artifact beside an unread with: still fails",
			files: map[string]string{
				".github/workflows/release.yml": callerWith("release.yml", "    with: *w\n"),
				".github/workflows/second.yml":  callerWith("release.yml", "    with:\n      app: MyApp\n"),
			},
			cfg: packages, want: StatusFail, wantDetail: "with a with: doctor does not read: job j, line 7 `with: *w`",
		},
		{
			name: "glyph.toml not loaded is could-not-run, never a verdict",
			files: map[string]string{".github/workflows/release.yml": callerWith("release.yml",
				"    with:\n      binary: probe\n")},
			cfg: nil, want: StatusUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := artifactAt(checkoutAt(t, tt.files), true, tt.cfg)
			if c.Status != tt.want {
				t.Fatalf("status = %s, want %s (observed %q, details %v)", c.Status, tt.want, c.Observed, c.Details)
			}
			if tt.wantDetail != "" {
				joined := strings.Join(c.Details, "\n")
				if !strings.Contains(joined, tt.wantDetail) {
					t.Errorf("details %v never say %q", c.Details, tt.wantDetail)
				}
			}
			switch tt.want {
			case StatusFail:
				// README's per-line recipe sits under Getting started, step 3;
				// its "Packages" section names the output and no recipe.
				if !strings.Contains(c.Fix, "`packages` output") || !strings.Contains(c.Fix, "README (Getting started, step 3)") {
					t.Errorf("the fix must send the caller to the packages output and to the recipe's home, got %q", c.Fix)
				}
				// The check has no pin rule, and where the refusal lands in the
				// run differs by release: through v6.1.0 it follows the upsert.
				for _, position := range []string{"before any draft", "right after", "before its verdict"} {
					if strings.Contains(c.Message+c.Expected, position) {
						t.Errorf("the check says where the refusal lands (%q), which is false of a caller pinned at "+
							"v6.1.0 or below: %q", position, c.Message)
					}
				}
			case StatusUnknown:
				if tt.cfg != nil && !strings.Contains(c.Message, "unverified here, not verified") {
					t.Errorf("an unknown must say it verified nothing, got %q", c.Message)
				}
			case StatusPass, StatusAdvice:
			}
		})
	}
}

// TestCallerArtifactNeverPassesOverAnUnreadableFile is the rule the other
// caller checks already follow (t-fdd8 (3)): a workflow file the filesystem
// refused could hold the caller, so a clean read of the rest is unknown, and
// a finding in a file that was read still fails.
func TestCallerArtifactNeverPassesOverAnUnreadableFile(t *testing.T) {
	packages := loaded(t, packagesConfig("camp"))
	hidden := callerWith("release.yml", "    with:\n      app: MyApp\n")
	// Positive control: the hidden caller is a finding when it can be read.
	if c := artifactAt(checkoutAt(t, map[string]string{".github/workflows/hide.yml": hidden}), true, packages); c.Status != StatusFail {
		t.Fatalf("readable fixture: status = %s, want %s — the unknown below would not be earned by the unread bytes", c.Status, StatusFail)
	}
	root := checkoutAt(t, map[string]string{
		".github/workflows/ci.yml":   "name: ci\non:\n  push:\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n",
		".github/workflows/hide.yml": hidden,
	})
	lockFile(t, filepath.Join(root, ".github", "workflows", "hide.yml"))
	c := artifactAt(root, true, packages)
	if c.Status != StatusUnknown {
		t.Fatalf("status = %s over an unreadable workflow file, want %s (observed %q)", c.Status, StatusUnknown, c.Observed)
	}

	// An unreadable file beside a finding: the finding wins.
	root2 := checkoutAt(t, map[string]string{
		".github/workflows/release.yml": hidden,
		".github/workflows/hide.yml":    "name: x\n",
	})
	lockFile(t, filepath.Join(root2, ".github", "workflows", "hide.yml"))
	c = artifactAt(root2, true, packages)
	if c.Status != StatusFail {
		t.Fatalf("status = %s with a finding beside an unreadable file, want %s", c.Status, StatusFail)
	}
	if len(c.Details) != 2 {
		t.Errorf("details = %v, want the finding and the unreadable file", c.Details)
	}
}

// TestCallerArtifactUnlistableWorkflowsIsUnknown: a workflows directory that
// could not be listed observed nothing, on a packages repository.
func TestCallerArtifactUnlistableWorkflowsIsUnknown(t *testing.T) {
	packages := loaded(t, packagesConfig("camp"))
	c := checkCallerArtifact("/nowhere", true, nil, errors.New("listing refused"), packages)
	if c.Status != StatusUnknown {
		t.Fatalf("status = %s, want %s", c.Status, StatusUnknown)
	}
	if !strings.Contains(c.Observed, "listing refused") {
		t.Errorf("observed %q does not carry the listing error", c.Observed)
	}
}
