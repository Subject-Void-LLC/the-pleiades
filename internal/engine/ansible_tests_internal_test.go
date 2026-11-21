// Internal tests of the is defined reading of a when: item.
package engine

import (
	"strings"
	"testing"
)

// TestAnsibleTest_EachSpellingOfARunVariable: each spelling of the test over
// a run variable reads as has() or !has(), any other item is returned as
// written, a test on any other condition root is refused naming why, and a
// test of anything else is refused naming the form that works.
func TestAnsibleTest_EachSpellingOfARunVariable(t *testing.T) {
	const otherRoot, notAVariable = "a registered result", "vars and one name"
	for _, tc := range []struct{ in, want, refused string }{
		{in: "vars.region is defined", want: "has(vars.region)"},
		{in: "  vars.region is not defined ", want: "!has(vars.region)"},
		{in: "vars.region is undefined", want: "!has(vars.region)"},
		{in: "vars.region is not undefined", want: "has(vars.region)"},
		{in: "vars.region == 'x'", want: "vars.region == 'x'"},
		{in: "has(vars.region)", want: "has(vars.region)"},
		{in: "vars.msg == 'is defined'", want: "vars.msg == 'is defined'"},
		// A register's answer is not Ansible's, nor is a scalar item's.
		{in: "result.probe is defined", refused: otherRoot},
		{in: "result.probe is not defined", refused: otherRoot},
		{in: "nodes.probe is defined", refused: otherRoot},
		{in: "stat.probe is undefined", refused: otherRoot},
		{in: "item.port is defined", refused: otherRoot},
		{in: "item is defined", refused: otherRoot},
		{in: "device.site is defined", refused: otherRoot},
		{in: "loop.index is defined", refused: otherRoot},
		{in: "error.message is defined", refused: otherRoot},
		{in: "result['probe'] is defined", refused: otherRoot},
		{in: "vars.a.b is defined", refused: notAVariable},
		// A stored fact is asked of the facts root, which answers from the
		// store and fails on a key it cannot answer (facts_view.go).
		{in: "facts.os is defined", want: `"os" in facts`},
		{in: `facts["os.family"] is not defined`, want: `!("os.family" in facts)`},
		{in: "facts['os.family'] is undefined", want: `!("os.family" in facts)`},
		{in: `facts[ "os.family" ] is not undefined`, want: `"os.family" in facts`},
		{in: "facts.os.family is defined", refused: notAVariable},
		{in: "facts[vars.k] is defined", refused: notAVariable},
		{in: `facts["a\"b"] is defined`, refused: notAVariable},
		{in: "region is defined", refused: notAVariable},
		{in: "variables.x is defined", refused: notAVariable},
		{in: "vars.x is defined && vars.y is defined", refused: notAVariable},
	} {
		got, err := ansibleTest(tc.in)
		switch {
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused) || !strings.Contains(err.Error(), strings.TrimSpace(tc.in)) || !strings.Contains(err.Error(), "has() in when_cel")):
			t.Errorf("ansibleTest(%q) = %q, %v, want it refused naming the item, %q and has()", tc.in, got, err, tc.refused)
		case tc.refused == "" && (err != nil || got != tc.want):
			t.Errorf("ansibleTest(%q) = %q, %v, want %q", tc.in, got, err, tc.want)
		}
	}
}
