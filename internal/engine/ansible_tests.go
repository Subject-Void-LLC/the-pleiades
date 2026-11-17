// Package engine: Ansible's is defined test in a when: item, read as the
// has() CEL already has.
package engine

import (
	"fmt"
	"regexp"
)

// definedTest is a when: item that is wholly Ansible's definedness test:
// a subject, then "is defined", "is not defined", "is undefined" or "is not
// undefined". CEL has no "is", so no item this matches is CEL already.
var definedTest = regexp.MustCompile(`^\s*(.*?)\s+is\s+(not\s+)?(defined|undefined)\s*$`)

// variableReference is what a definedness test may ask about: one of the
// condition roots (CELVariableOptions) and one name under it, the form CEL's
// has() takes. A deeper path is not one: has(vars.a.b) fails, where Ansible
// answers false, when vars.a is not a mapping.
var variableReference = regexp.MustCompile(`^(vars|result|nodes|stat|device|loop|item|error)\.[A-Za-z_][A-Za-z0-9_]*$`)

// ansibleTest returns the CEL a when:, when_or:, until:, changed_when:,
// failed_when: or that: item means: an item that is wholly "<ref> is
// defined" (or "is not undefined") is has(<ref>), one that is "<ref> is not
// defined" (or "is undefined") is !has(<ref>), and any other item is its own
// CEL, returned as written. A definedness test of anything but a variable
// reference (vars.region, result.probe) is refused, naming the form that
// works, rather than handed to CEL to fail on the word "is". when_cel: is
// never read here: it is CEL as written.
func ansibleTest(expr string) (string, error) {
	m := definedTest.FindStringSubmatch(expr)
	if m == nil {
		return expr, nil
	}
	subject, negated, word := m[1], m[2] != "", m[3]
	if !variableReference.MatchString(subject) {
		return "", fmt.Errorf("`%s` tests whether something is defined, which is asked only of a variable reference, a root and one name such as vars.region or result.probe; for anything else write has() in when_cel:, testing each level of a path", expr)
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
