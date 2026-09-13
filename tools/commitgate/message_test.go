// Package main: tests for the commit message rules.
//
// Table-driven throughout, and every case names the rule it is about, so
// a failure says which rule broke rather than which row of a table did.
package main

import (
	"strings"
	"testing"
)

// hasRule reports whether findings contain one at the given severity
// whose Rule names substr. The tests match on the rule text rather than
// on a count, so adding an unrelated rule never breaks an existing case.
func hasRule(findings []Finding, severity Severity, substr string) bool {
	for _, f := range findings {
		if f.Severity == severity && strings.Contains(f.Rule, substr) {
			return true
		}
	}
	return false
}

func TestCheckMessageAcceptsEveryConventionalType(t *testing.T) {
	for _, subject := range []string{
		"feat: add a thing",
		"fix(engine): stop the leak",
		"docs: explain the gate",
		"refactor(internal/journal): split the store",
		"test(archtest): add the rule",
		"chore: bump a pin",
		"feat(api)!: change the response shape",
	} {
		t.Run(subject, func(t *testing.T) {
			if findings := checkMessage(subject); hasRule(findings, SeverityFail, "conventional") {
				t.Errorf("checkMessage(%q) refused a valid subject", subject)
			}
		})
	}
}

func TestCheckMessageRefusesANonConventionalSubject(t *testing.T) {
	for _, subject := range []string{
		"Add a thing",
		"feature: add a thing",
		"feat add a thing",
		"feat:",
		"feat: ",
		"(engine): add a thing",
		"wip",
	} {
		t.Run(subject, func(t *testing.T) {
			if findings := checkMessage(subject); !hasRule(findings, SeverityFail, "conventional") {
				t.Errorf("checkMessage(%q) accepted a subject that is not a conventional commit", subject)
			}
		})
	}
}

func TestCheckMessageExemptsWhatGitItselfWrites(t *testing.T) {
	for _, subject := range []string{
		"Merge branch 'main' into feature/x",
		"Revert \"feat: add a thing\"",
		"fixup! feat: add a thing",
		"squash! feat: add a thing",
	} {
		t.Run(subject, func(t *testing.T) {
			if findings := checkMessage(subject); hasRule(findings, SeverityFail, "conventional") {
				t.Errorf("checkMessage(%q) refused a subject git or a rebase wrote", subject)
			}
		})
	}
}

func TestCheckMessageRefusesAnEmDash(t *testing.T) {
	msg := "feat: add a thing\n\nThe body explains it " + string(emDash) + " at length.\n"
	if !hasRule(checkMessage(msg), SeverityFail, "em dash") {
		t.Error("checkMessage accepted a message containing an em dash")
	}
}

func TestCheckMessageAllowsAnEnDashWithAWarning(t *testing.T) {
	// The en dash is not forbidden, so the message is accepted. It is not
	// warned about either: the warning lives in the content rules, where
	// there is a file and a line to point at.
	msg := "feat: cover 2020" + string(rune(0x2013)) + "2024\n"
	findings := checkMessage(msg)
	if hasRule(findings, SeverityFail, "em dash") {
		t.Error("checkMessage refused an en dash, which AGENTS.md does not forbid")
	}
}

func TestCheckMessageRefusesAnAIAttributionTrailer(t *testing.T) {
	for name, trailer := range map[string]string{
		"co-authored claude":  "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>",
		"co-authored copilot": "Co-Authored-By: GitHub Copilot <copilot@github.com>",
		"generated with":      "\U0001F916 Generated with [Claude Code](https://claude.com/claude-code)",
		"signed off gemini":   "Signed-off-by: Gemini <noreply@example.com>",
	} {
		t.Run(name, func(t *testing.T) {
			msg := "feat: add a thing\n\nA real body.\n\n" + trailer + "\n"
			if !hasRule(checkMessage(msg), SeverityFail, "co-author") {
				t.Errorf("checkMessage accepted a message trailing %q", trailer)
			}
		})
	}
}

func TestCheckMessageAllowsTalkingAboutAModelInTheBody(t *testing.T) {
	// The Licensing rule is about authorship credit, not about the word.
	// A commit that explains it changed the Claude API client has to be
	// able to say so.
	msg := "feat(api): pin the claude model id used by the agent path\n\n" +
		"The anthropic client now names claude-opus-5 explicitly rather than\n" +
		"tracking whatever the default happens to be.\n"
	if hasRule(checkMessage(msg), SeverityFail, "co-author") {
		t.Error("checkMessage refused a message that merely discusses a model")
	}
}

func TestCheckMessageWarnsOnALongSubject(t *testing.T) {
	long := "feat(engine): "
	for len(long) <= subjectSoftLimit {
		long += "x"
	}
	if !hasRule(checkMessage(long), SeverityWarn, "one line") {
		t.Errorf("checkMessage did not warn about a %d character subject", len(long))
	}
}

func TestCheckMessageWarnsOnASubjectEndingInAPeriod(t *testing.T) {
	if !hasRule(checkMessage("feat: add a thing."), SeverityWarn, "title") {
		t.Error("checkMessage did not warn about a subject ending in a period")
	}
}

func TestParseMessageDropsWhatGitThrowsAway(t *testing.T) {
	raw := "feat: add a thing\n" +
		"# Please enter the commit message for your changes.\n" +
		"\n" +
		"A real body.\n" +
		"# ------------------------ >8 ------------------------\n" +
		"diff --git a/x b/x\n" +
		"+a line with an em dash " + string(emDash) + " inside the diff\n"
	got := parseMessage(raw)
	if strings.ContainsRune(got, emDash) {
		t.Errorf("parseMessage kept text below the scissors line: %q", got)
	}
	if strings.Contains(got, "Please enter") {
		t.Errorf("parseMessage kept a comment line: %q", got)
	}
	if !strings.Contains(got, "A real body.") {
		t.Errorf("parseMessage dropped the real body: %q", got)
	}
}

func TestCheckMessageIsSilentOnAnEmptyMessage(t *testing.T) {
	// git aborts an empty-message commit itself, so adding a finding here
	// would only add noise to something already refused.
	for _, raw := range []string{"", "\n\n", "# only a comment\n"} {
		if findings := checkMessage(raw); len(findings) != 0 {
			t.Errorf("checkMessage(%q) reported %d findings, want 0", raw, len(findings))
		}
	}
}
