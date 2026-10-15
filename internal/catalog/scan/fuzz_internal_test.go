// Fuzzing for the Gitleaks report reader.
package scan

import "testing"

// FuzzParseLeaks holds parseLeaks to its contract on any input: it
// returns or refuses without panicking, and every finding it returns has
// a rule, a file and a line, and no value: the type it decodes into has
// no field for one.
func FuzzParseLeaks(f *testing.F) {
	f.Add([]byte(`[{"RuleID":"aws-access-token","File":"/scan/conf/app.ini","StartLine":2,"Secret":"REDACTED","Match":"REDACTED"}]`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"RuleID":"x","File":"y","StartLine":0}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		leaks, err := parseLeaks(data)
		if err != nil {
			return
		}
		for _, l := range leaks {
			if l.RuleID == "" || l.File == "" || l.StartLine < 1 {
				t.Fatalf("a finding parseLeaks returned is incomplete: %+v", l)
			}
		}
	})
}
