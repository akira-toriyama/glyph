package doctor

// This check is the caller-side half of release.yml's packages refusal
// (DESIGN §4.1, "The reusables"): a repository that declares [[packages]]
// keeps one rolling draft per line, and `app` / `binary` name one artifact
// for one draft — there is no line it would attach to. The reusable refuses
// the pair right after it installs glyph, before any draft is touched, so the
// run is red on every push until the input goes; this check reads the same
// two facts — the caller's `with:` keys and the loaded glyph.toml — and names
// the pair before the push. No pin rule applies: one artifact has no line at
// any release, so the pair is wrong at every pin (DESIGN §7).

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// artifactInputs are the release.yml inputs that name one artifact for one
// draft, as the caller's `with:` spells them.
var artifactInputs = []string{"app", "binary"}

// checkCallerArtifact scans the local checkout's workflow files for callers
// of release.yml and, when the loaded glyph.toml declares [[packages]],
// reports each caller whose `with:` names app or binary. cfg is nil when the
// file was not loaded — the config check carries that failure, this one says
// it could not run.
func checkCallerArtifact(root string, rootVerified bool, workflows []workflowFile, workflowsErr error, cfg *config.Config) Check {
	c := Check{
		ID: idCallerArtifact,
		Expected: "no caller of release.yml names `app` or `binary` while glyph.toml declares [[packages]]: each line keeps its own " +
			"rolling draft, and one artifact has no line to attach to",
	}
	if configUnread(&c, cfg) {
		return c
	}
	if len(cfg.Packages) == 0 {
		c.Status = StatusPass
		c.Observed = "no [[packages]] declared: one version line (vX.Y.Z), whose draft is the one an artifact attaches to"
		c.Message = "the repository versions as a single line, and `app` / `binary` are the single line's inputs"
		return c
	}
	if workflowsErr != nil {
		unlistableWorkflows(&c, root, rootVerified, workflowsErr,
			"A release.yml caller naming an artifact input is refused by the reusable before its verdict — unverified "+
				"here, not verified",
			", so whether a release.yml caller names an artifact input is unverified here, not verified")
		return c
	}

	callers := 0
	var findings, unreadable []string
	for _, f := range workflows {
		if f.Err != nil {
			unreadable = append(unreadable, f.unreadableLine())
			continue
		}
		lines := strings.Split(f.Body, "\n")
		for _, call := range reusablesCalled(f.Name, f.Body) {
			if !strings.EqualFold(call.Reusable, "release.yml") {
				continue
			}
			callers++
			keys := jobWithKeys(lines, call.Line)
			for _, input := range artifactInputs {
				if keys[input] {
					findings = append(findings, fmt.Sprintf(
						".github/workflows/%s calls %s@%s with %s, and this repository declares %d [[packages]] line(s)",
						f.Name, call.Reusable, call.Ref, input, len(cfg.Packages)))
				}
			}
		}
	}

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d artifact input(s) across the %d workflow file(s) that call release.yml", len(findings), callers)
		c.Details = append(findings, unreadable...)
		c.Message = "a repository that declares [[packages]] keeps one rolling draft per line, and `app` / `binary` name one " +
			"artifact for one draft — there is no line it would attach to, so release.yml refuses the run right after it " +
			"installs glyph, before any draft is touched, and stays red on every push until the input goes"
		c.Fix = "drop `app` / `binary` from the caller's with: block, and attach per line in a follow-up job over the " +
			"reusable's `packages` output (README, \"Packages\")"
		return c
	}
	if unverifiedCallers(&c, nil, nil, unreadable, "whether a release.yml caller names an artifact input") {
		return c
	}
	c.Status = StatusPass
	if callers == 0 {
		c.Observed = "no workflow in this checkout calls release.yml (nothing names an artifact)"
		c.Message = "nothing to judge, observed — not assumed"
		return c
	}
	c.Observed = fmt.Sprintf("%d release.yml caller(s), none naming app or binary; %d [[packages]] line(s) declared", callers, len(cfg.Packages))
	c.Message = "every line's draft is attached to, if at all, by a follow-up job over `packages`"
	return c
}
