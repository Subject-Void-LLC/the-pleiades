// Tests of IsTemplate, the one test of whether a parameter's text renders.
package collection_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestIsTemplate: an expression, a statement and a comment each make text a
// template; a lone brace or percent sign does not.
func TestIsTemplate(t *testing.T) {
	for s, want := range map[string]bool{
		"show {{ vars.x }}":           true,
		"{% if vars.x %}y{% endif %}": true,
		"x {# note #}":                true,
		"printf '{%'":                 true,
		"show version":                false,
		"a { b } 100%":                false,
		"":                            false,
	} {
		if got := collection.IsTemplate(s); got != want {
			t.Errorf("IsTemplate(%q) = %v, want %v", s, got, want)
		}
	}
}
