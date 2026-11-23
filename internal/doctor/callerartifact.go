package doctor

// This check is the caller-side half of release.yml's packages refusal
// (DESIGN §4.1, "The reusables"): a repository that declares [[packages]]
// keeps one rolling draft per line, and `app` / `binary` name one artifact
// for one draft — there is no line it would attach to. The reusable refuses
// the pair at every release that reads [[packages]], so the run is red on
// every push until the input goes; this check reads the same two facts — the
// value the caller's `with:` gives each input and the loaded glyph.toml —
// and names the pair before the push. No pin rule applies: one artifact has
// no line at any release, so the pair is wrong at every pin (DESIGN §7).
// Where in the run the refusal lands differs by the release the caller pins
// — after the drafts are rewritten through v6.1.0, before the verdict from
// the next release on — so no text this check prints states it.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akira-toriyama/glyph/v6/internal/config"
)

// artifactInputs are the release.yml inputs that name one artifact for one
// draft, as the caller's `with:` spells them.
var artifactInputs = []string{"app", "binary"}

// inputValue is what a checkout says about one `with:` value, graded against
// the question release.yml's refusal asks of the input: is it non-empty at
// run time.
type inputValue int

const (
	// valueEmpty is "" at run time — the input is not named: YAML null, which
	// GitHub hands a string input as "" (measured 2026-11-15 on
	// glyph-monorepo-test-2, the refusal step skipped in each run: `binary:`
	// with nothing after it, run 43678383110; `~`, 43689418881; `null`,
	// 43689421291; `Null` and `NULL`, 43690575673), or an empty quoted scalar
	// (run 42450511719, `binary: ""`). Quoted, a null spelling is a non-empty
	// string (run 43690585767: `'~'` and `"null"` reached the job as `~` and
	// `null`).
	valueEmpty inputValue = iota
	// valueNamed is a non-empty literal: the reusable refuses it.
	valueNamed
	// valueUnread is a value a checkout does not hold: an expression GitHub
	// evaluates at run time, a block scalar, whose text this reader does not
	// read, an alias, whose anchor it does not follow (`*e` over `&e ""` is
	// no value; PyYAML), or a value carrying an anchor or a tag, which this
	// reader does not resolve (`&b ""` and `!!null` are no value).
	valueUnread
)

// nullSpellings are the plain scalars GitHub's workflow parser reads as YAML
// null, besides the absent value.
var nullSpellings = []string{"~", "null", "Null", "NULL"}

// gradeInput grades one value as jobWith returns it.
func gradeInput(v string) inputValue {
	switch {
	case strings.Contains(v, "${{"):
		return valueUnread
	case v == "", v == `""`, v == `''`, slices.Contains(nullSpellings, v):
		return valueEmpty
	case v[0] == '|', v[0] == '>', v[0] == '*', v[0] == '&', v[0] == '!':
		return valueUnread
	}
	return valueNamed
}

// checkCallerArtifact scans the local checkout's workflow files for callers
// of release.yml and, when the loaded glyph.toml declares [[packages]],
// reports each caller whose `with:` gives app or binary a non-empty value.
// A value the checkout does not hold is could-not-run, the grade every caller
// check gives what it cannot read (DESIGN §7), and a finding beside one still
// fails. cfg is nil when the file was not loaded — the config check carries
// that failure, this one says it could not run.
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
			"A release.yml caller naming an artifact input is refused by the reusable — unverified here, not verified",
			", so whether a release.yml caller names an artifact input is unverified here, not verified")
		return c
	}

	callers := 0
	var findings, unevaluated, unreadWith, unreadable []string
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
			with, unread := jobWith(lines, call.Line)
			if unread != "" {
				unreadWith = append(unreadWith, withUnreadLine(f.Name, call, unread))
				continue
			}
			for _, input := range artifactInputs {
				value, named := with[input]
				if !named {
					continue
				}
				switch gradeInput(value) {
				case valueNamed:
					findings = append(findings, fmt.Sprintf(
						".github/workflows/%s calls %s@%s with %s, and this repository declares %d [[packages]] line(s)",
						f.Name, call.Reusable, call.Ref, input, len(cfg.Packages)))
				case valueUnread:
					unevaluated = append(unevaluated, fmt.Sprintf(
						".github/workflows/%s calls %s@%s with %s: %s — a value this checkout does not hold",
						f.Name, call.Reusable, call.Ref, input, value))
				case valueEmpty:
					// "" at run time: the reusable's condition is false for it.
				}
			}
		}
	}

	if len(findings) > 0 {
		c.Status = StatusFail
		c.Observed = fmt.Sprintf("%d artifact input(s) across the %d workflow file(s) that call release.yml", len(findings), callers)
		c.Details = slices.Concat(findings, unevaluated, unreadWith, unreadable)
		c.Message = "a repository that declares [[packages]] keeps one rolling draft per line, and `app` / `binary` name one " +
			"artifact for one draft — there is no line it would attach to, so release.yml refuses the run, and it stays " +
			"red on every push until the input goes"
		c.Fix = "drop `app` / `binary` from the caller's with: block, and attach per line in a follow-up job over the " +
			"reusable's `packages` output — glyph's README (Getting started, step 3) has the recipe"
		return c
	}
	const death = "whether a release.yml caller names an artifact input"
	if len(unevaluated) > 0 {
		c.Status = StatusUnknown
		gaps := []string{fmt.Sprintf("%d artifact input(s) set from a value this checkout does not hold", len(unevaluated))}
		why := []string{"release.yml refuses an artifact input by its value at run time, and an expression, a block " +
			"scalar or an alias is not a value doctor reads"}
		fixes := []string{"write the input as a literal, or drop it — a value that is non-empty at run time is refused " +
			"on this repository"}
		if len(unreadWith) > 0 {
			gaps = append(gaps, fmt.Sprintf("%d caller(s) whose with: doctor does not read", len(unreadWith)))
			why = append(why, withUnreadWhy)
			fixes = append(fixes, withUnreadFix)
		}
		if len(unreadable) > 0 {
			gaps = append(gaps, fmt.Sprintf("%d workflow file(s) could not be read", len(unreadable)))
			why = append(why, "a file doctor cannot read could hold any caller")
			fixes = append(fixes, "fix the file permissions and re-run")
		}
		c.Observed = strings.Join(gaps, "; ") + "; no caller names an artifact outright"
		c.Message = strings.Join(why, ", and ") + " — " + death + " is unverified here, not verified"
		c.Fix = strings.Join(fixes, "; ")
		c.Details = slices.Concat(unevaluated, unreadWith, unreadable)
		return c
	}
	if unverifiedCallers(&c, nil, nil, unreadWith, unreadable, death, "no judged caller names an artifact input") {
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
