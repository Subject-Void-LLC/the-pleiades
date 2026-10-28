// Tests of ArgvSummary and CommandLineSummary: a command line is reported
// as its program and argument count, never an argument.
package containerengine_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

func TestArgvSummary_KeepsTheProgramAndCountsTheRest(t *testing.T) {
	cases := map[string]struct {
		argv []string
		line string
		want map[string]any
	}{
		"none":     {nil, "", map[string]any{"program": "", "arg_count": 0}},
		"program":  {[]string{"nginx"}, "nginx", map[string]any{"program": "nginx", "arg_count": 0}},
		"a secret": {[]string{"redis-server", "--requirepass", "s3cret"}, "redis-server --requirepass s3cret", map[string]any{"program": "redis-server", "arg_count": 2}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := containerengine.ArgvSummary(tc.argv); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ArgvSummary(%q) = %#v, want %#v", tc.argv, got, tc.want)
			}
			if got := containerengine.CommandLineSummary(tc.line); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CommandLineSummary(%q) = %#v, want %#v", tc.line, got, tc.want)
			}
		})
	}
}
