// Tests for the advice ParamTypeRule words with each refusal, which sits
// beside the rule in package validate.
package validate

import "testing"

// TestTypeAdvice names how to write the value the rule refuses, for each of
// the two mistakes it advises on, and says nothing for the rest.
func TestTypeAdvice(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		v    any
		want string
	}{
		{"string", 4, ": quote it to pass text"},
		{"int", "600", ": write it unquoted"},
		{"bool", "true", ": write it unquoted"},
		{"list", 4, ""},
	} {
		if got := typeAdvice(tc.typ, tc.v); got != tc.want {
			t.Errorf("typeAdvice(%q, %#v) = %q, want %q", tc.typ, tc.v, got, tc.want)
		}
	}
}
