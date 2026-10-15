// Fuzzing the manifest parser: any input either parses into a manifest
// that validates and plans, or is refused with an error, never a panic.
package gate

import "testing"

// FuzzParse feeds arbitrary bytes to Parse.
func FuzzParse(f *testing.F) {
	f.Add([]byte(goodManifest))
	f.Add([]byte("version: 1\ndefaults: {timeout: 1m}\nchecks: [{name: a, kind: pure, shell: x}]\n"))
	f.Add([]byte("version: 1\nchecks:\n  - &a {name: a, kind: pure, shell: x, timeout: 1s}\n  - *a\n"))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			return
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("a parsed manifest does not validate: %v", err)
		}
		plan, err := m.Plan(Selection{})
		if err != nil {
			t.Fatalf("a parsed manifest does not plan: %v", err)
		}
		for _, p := range plan {
			if p.Timeout <= 0 {
				t.Fatalf("check %s planned with no timeout", p.Check.Name)
			}
		}
	})
}
