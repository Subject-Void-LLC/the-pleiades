// Package engine: Ansible's is defined test in a when: item, read as the
// has() CEL already has.
package engine

import (
	"fmt"
	"regexp"
	"strconv"
)

// definedTest is a when: item that is wholly Ansible's definedness test:
// a subject, then "is defined", "is not defined", "is undefined" or "is not
// undefined". CEL has no "is", so no item this matches is CEL already.
var definedTest = regexp.MustCompile(`^\s*(.*?)\s+is\s+(not\s+)?(defined|undefined)\s*$`)

// variableReference is what a definedness test may ask about: a run
// variable, vars and one name under it, the form CEL's has() takes and the
// one root whose answer is Ansible's. A deeper path is not one: has(vars.a.b)
// fails, where Ansible answers false, when vars.a is not a mapping.
var variableReference = regexp.MustCompile(`^vars\.[A-Za-z_][A-Za-z0-9_]*$`)

// otherRoot is a subject on one of the other condition roots, which a
// definedness test is refused on with its own reason.
var otherRoot = regexp.MustCompile(`^(result|nodes|stat|device|loop|item|error)(\.|\[|$)`)

// factReference is a stored fact a definedness test may ask about: facts and
// a name (facts.uptime), or facts indexed by a quoted key holding no quote or
// backslash (facts["os.family"]). Its answer is the store's: a key the device
// was never gathered for fails the task rather than answering (facts_view.go).
var factReference = regexp.MustCompile(`^facts(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*(?:"([^"\\]*)"|'([^'\\]*)')\s*\])$`)

// ansibleTest returns the CEL a when:, when_or:, until:, changed_when:,
// failed_when: or that: item means: an item that is wholly "vars.<name> is
// defined" (or "is not undefined") is has(vars.<name>), one that is
// "vars.<name> is not defined" (or "is undefined") is !has(vars.<name>), one
// asking it of a stored fact (facts["<key>"], facts.<name>) is
// facts[?"<key>"].hasValue() or its negation, and any other item is its own
// CEL, returned as written. when_cel: is never read here: it is CEL as written.
//
// The test is asked only of a run variable, where has() answers as Ansible
// does. Every other root is refused when the runbook is built, rather than
// given an answer Ansible would not give: a register (result, nodes, stat) is
// written only by a task that ran, where Ansible registers a skipped task's
// result too, and result holds only a register exactly one device wrote, so
// on two devices "result.probe is not defined" would run where Ansible skips
// (the playbook converter refuses the same case); item.<name> fails on a
// scalar item, where Ansible answers false; device, loop and error are the
// platform's own. Anything else that is not a run variable is refused too,
// rather than handed to CEL to fail on the word "is".
func ansibleTest(expr string) (string, error) {
	m := definedTest.FindStringSubmatch(expr)
	if m == nil {
		return expr, nil
	}
	subject, negated, word := m[1], m[2] != "", m[3]
	// A stored fact is asked as facts[?"key"].hasValue(), which is exact
	// whatever other key has no answer: "key" in facts reads the root whole,
	// so one unknown key anywhere would leave every in test unknown
	// (facts_condition.go).
	if f := factReference.FindStringSubmatch(subject); f != nil {
		test := TemplateRootFacts + "[?" + strconv.Quote(f[1]+f[2]+f[3]) + "].hasValue()"
		if (word == "defined") == negated {
			return "!(" + test + ")", nil
		}
		return test, nil
	}
	switch {
	case otherRoot.MatchString(subject):
		return "", fmt.Errorf("`%s` tests whether something is defined, which is asked here only of a run variable (vars.<name>): a registered result (result, nodes, stat) exists only for a task that ran, where Ansible registers a skipped task's too, and result holds one only a single device wrote, so the answer would not be Ansible's; an item may not be a mapping; write the condition you mean with has() in when_cel: (a stored fact may be asked too, as facts[\"<key>\"] is defined)", expr)
	case !variableReference.MatchString(subject):
		return "", fmt.Errorf("`%s` tests whether something is defined, which is asked here only of a run variable, vars and one name such as vars.region, or of a stored fact, facts[\"<key>\"]; for anything else write has() in when_cel:, testing each level of a path", expr)
	}
	// "is not undefined" asks what "is defined" does, and "is undefined"
	// what "is not defined" does.
	if (word == "defined") == negated {
		return "!has(" + subject + ")", nil
	}
	return "has(" + subject + ")", nil
}

// Sources returns c's expressions as the CEL they compile to, in order:
// each when: and when_or: item read through ansibleTest, then when_cel: as
// written. An item ansibleTest refuses is returned as written, for a reader
// that parses conditions to fail on as it would before; Compile, which every
// condition passes through when its DAG is built, is what refuses it. Empty
// expressions are left out.
func (c Conditional) Sources() []string {
	var out []string
	for _, e := range append(append([]string(nil), c.When...), c.WhenOr...) {
		if e == "" {
			continue
		}
		if cel, err := ansibleTest(e); err == nil {
			e = cel
		}
		out = append(out, e)
	}
	if c.WhenCEL != "" {
		out = append(out, c.WhenCEL)
	}
	return out
}
