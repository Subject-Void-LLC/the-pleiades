// Internal tests of the is defined reading of a when: item.
package engine

import (
	"strings"
	"testing"
)

// TestAnsibleTest_EachSpellingAndRoot: each spelling of the test over each
// condition root reads as has() or !has(), any other item is returned as
// written, and a test of anything but a variable reference is refused.
func TestAnsibleTest_EachSpellingAndRoot(t *testing.T) {
	for _, tc := range []struct{ in, want, refused string }{
		{in: "vars.region is defined", want: "has(vars.region)"},
		{in: "  vars.region is not defined ", want: "!has(vars.region)"},
		{in: "vars.region is undefined", want: "!has(vars.region)"},
		{in: "vars.region is not undefined", want: "has(vars.region)"},
		{in: "result.probe is defined", want: "has(result.probe)"},
		{in: "nodes.probe is defined", want: "has(nodes.probe)"},
		{in: "stat.probe is defined", want: "has(stat.probe)"},
		{in: "device.site is defined", want: "has(device.site)"},
		{in: "item.port is defined", want: "has(item.port)"},
		{in: "loop.index is defined", want: "has(loop.index)"},
		{in: "error.message is defined", want: "has(error.message)"},
		{in: "vars.region == 'x'", want: "vars.region == 'x'"},
		{in: "has(vars.region)", want: "has(vars.region)"},
		{in: "vars.msg == 'is defined'", want: "vars.msg == 'is defined'"},
		{in: "vars.a.b is defined", refused: "vars.a.b is defined"},
		{in: "facts.os is defined", refused: "facts.os is defined"},
		{in: "region is defined", refused: "region is defined"},
		{in: "vars.x is defined && vars.y is defined", refused: "vars.x is defined && vars.y is defined"},
	} {
		got, err := ansibleTest(tc.in)
		switch {
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused) || !strings.Contains(err.Error(), "has() in when_cel")):
			t.Errorf("ansibleTest(%q) = %q, %v, want it refused naming the item and has()", tc.in, got, err)
		case tc.refused == "" && (err != nil || got != tc.want):
			t.Errorf("ansibleTest(%q) = %q, %v, want %q", tc.in, got, err, tc.want)
		}
	}
}
