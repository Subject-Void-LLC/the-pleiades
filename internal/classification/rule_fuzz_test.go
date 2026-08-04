package classification_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
)

// FuzzClassify drives Classify against the real DefaultRuleSet with
// arbitrary path input, proving it never panics regardless of segment
// content, count, casing, or embedded delimiters -- the exact "malformed
// or malicious input at a new deserialization boundary" concern this
// phase's Schema/Injection Hardening item requires evidence for, since
// path ultimately comes from a HostSpec.Classify field a user can write
// directly into inventory.yaml or pass via `add-host --classify`.
func FuzzClassify(f *testing.F) {
	f.Add("linux_server")
	f.Add("linux_server,debian_family,ubuntu")
	f.Add("network_device,cisco,ios")
	f.Add("")
	f.Add(",")
	f.Add("a.b,c")
	f.Add("UPPER_CASE")
	f.Add("../../etc/passwd")
	f.Add(strings.Repeat("a,", 200))

	rs := classification.DefaultRuleSet()

	f.Fuzz(func(t *testing.T, raw string) {
		var path []string
		if raw != "" {
			path = strings.Split(raw, ",")
		}

		// Classify must only ever return a nil error with a fully populated
		// Type, or a non-nil error with a zero Result -- never panic, and
		// never a nil error paired with a nil Type (which would later
		// panic in ResolveHostType's own dereference).
		result, err := rs.Classify(path)
		if err != nil {
			return
		}
		if result.Value.Type == nil {
			t.Fatalf("Classify(%v) returned a nil error but a nil Type", path)
		}
	})
}
