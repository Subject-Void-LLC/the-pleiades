package classification_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
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

// fuzzCapNames is a small, fixed sample of the real registered vocabulary,
// used only to build synthetic per-level Rule.Capabilities assignments.
var fuzzCapNames = []capability.Name{
	capability.NameLinux, capability.NameSSHTransport, capability.NameApt,
	capability.NameDnf, capability.NameCiscoIOS,
}

// FuzzClassifyCapabilitiesUnion fuzzes combineRule's Capabilities merge
// (indirectly -- combineRule itself is unexported, so this drives it the
// only way a caller outside the package can, through Classify) across an
// arbitrary number of levels, each granting an arbitrary subset of
// fuzzCapNames. It asserts the checklist's own invariant: the result is
// the true set union of every level's Capabilities, deduplicated, with no
// dependence on how many levels carried a capability more than once.
func FuzzClassifyCapabilitiesUnion(f *testing.F) {
	f.Add(uint32(0))
	f.Add(uint32(1))
	f.Add(^uint32(0))
	f.Add(uint32(0b111_101_010_001))

	const levels = 4 // "l0".."l3", each a valid segment on its own

	f.Fuzz(func(t *testing.T, bits uint32) {
		path := make([]string, levels)
		rules := make(map[string]classification.Rule, levels)
		want := make(map[capability.Name]bool)

		prefix := ""
		for i := 0; i < levels; i++ {
			path[i] = []string{"l0", "l1", "l2", "l3"}[i]
			if prefix == "" {
				prefix = path[i]
			} else {
				prefix = prefix + "." + path[i]
			}

			sel := (bits >> uint(i*3)) % 8 // 0-4 select a name, 5-7 select none
			var caps []capability.Name
			if int(sel) < len(fuzzCapNames) {
				caps = []capability.Name{fuzzCapNames[sel]}
				want[fuzzCapNames[sel]] = true
			}
			rules[prefix] = classification.Rule{Capabilities: caps}
		}

		rs, err := classification.NewRuleSet(rules)
		if err != nil {
			t.Fatalf("NewRuleSet: %v", err)
		}
		result, err := rs.Classify(path)
		if err != nil {
			t.Fatalf("Classify(%v): %v", path, err)
		}

		got := make(map[capability.Name]bool, len(result.Value.Capabilities))
		for _, c := range result.Value.Capabilities {
			if got[c] {
				t.Fatalf("Capabilities contained a duplicate %q: %v", c, result.Value.Capabilities)
			}
			got[c] = true
		}
		if len(got) != len(want) {
			t.Fatalf("Capabilities = %v, want exactly the union %v", result.Value.Capabilities, want)
		}
		for name := range want {
			if !got[name] {
				t.Fatalf("Capabilities missing %q, got %v", name, result.Value.Capabilities)
			}
		}
	})
}
