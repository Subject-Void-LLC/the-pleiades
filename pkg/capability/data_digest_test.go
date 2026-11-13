// Tests for the digest that binds a grant to a data capability's
// definition.
package capability_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// tenancyDigest is DefinitionDigest(tenancy()), pinned. The test below
// computes it on every operating system CI runs on, so a digest that read
// anything of the platform (map order, the size of an int, a path
// separator) would fail on one of them. The value was checked against an
// independent computation (Python's hashlib over the same domain line and
// compact JSON), so it pins the encoding, not just this code's output.
const tenancyDigest = "sha256:3c8a0857625a4ac59c9c988bca8b7d6fa4b62107cd959ea75162765c8763dbfa"

// TestDefinitionDigest_IsPinnedAndSurvivesADescribe: the digest of a
// definition is one fixed value, and the same definition decoded from the
// JSON a program's describe prints (as another build of the program, on
// another platform, would print it) digests the same.
func TestDefinitionDigest_IsPinnedAndSurvivesADescribe(t *testing.T) {
	got := capability.DefinitionDigest(tenancy())
	if got != tenancyDigest {
		t.Fatalf("DefinitionDigest(tenancy()) = %s, want the pinned %s", got, tenancyDigest)
	}
	if !capability.IsDefinitionDigest(got) {
		t.Errorf("%s does not have a definition digest's shape", got)
	}
	encoded, err := json.Marshal(tenancy())
	if err != nil {
		t.Fatal(err)
	}
	var described capability.DataDefinition
	if err := json.Unmarshal(encoded, &described); err != nil {
		t.Fatal(err)
	}
	if again := capability.DefinitionDigest(described); again != got {
		t.Errorf("the definition read back from its JSON digests %s, want %s", again, got)
	}
}

// TestDefinitionDigest_IgnoresOrderAndProse: listing the requirements and
// properties in another order, naming a requirement twice, and rewriting
// the prose and the probe leave the digest as it was.
func TestDefinitionDigest_IgnoresOrderAndProse(t *testing.T) {
	want := capability.DefinitionDigest(tenancy())
	for name, mutate := range map[string]func(*capability.DataDefinition){
		"properties reversed": func(d *capability.DataDefinition) {
			for i, j := 0, len(d.Properties)-1; i < j; i, j = i+1, j-1 {
				d.Properties[i], d.Properties[j] = d.Properties[j], d.Properties[i]
			}
		},
		"requirements reordered": func(d *capability.DataDefinition) {
			d.Requires = []capability.Name{capability.NamePOSIXFileSystem, capability.NameSSHTransport}
		},
		"a requirement named twice": func(d *capability.DataDefinition) {
			d.Requires = []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem, capability.NameSSHTransport}
		},
		"the doc rewritten":                func(d *capability.DataDefinition) { d.Doc = "Speaks to an OCI tenancy." },
		"a property's description written": func(d *capability.DataDefinition) { d.Properties[1].Description = "The home region." },
		"another probe":                    func(d *capability.DataDefinition) { d.Probe = "oci.tenancy.probe" },
	} {
		t.Run(name, func(t *testing.T) {
			def := tenancy()
			def.Requires = []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem}
			base := capability.DefinitionDigest(def)
			mutate(&def)
			if got := capability.DefinitionDigest(def); got != base {
				t.Errorf("digest moved from %s to %s", base, got)
			}
		})
	}
	if capability.DefinitionDigest(tenancy()) != want {
		t.Error("DefinitionDigest changed its argument")
	}
}

// TestDefinitionDigest_MovesWithWhatItMeans: every field the digest covers
// moves it when it changes.
func TestDefinitionDigest_MovesWithWhatItMeans(t *testing.T) {
	base := capability.DefinitionDigest(tenancy())
	for name, mutate := range map[string]func(*capability.DataDefinition){
		"the name":                func(d *capability.DataDefinition) { d.Name = "oci.tenancy_api2" },
		"the provider":            func(d *capability.DataDefinition) { d.Provider = "pleiades-oci-fork" },
		"the revision":            func(d *capability.DataDefinition) { d.Revision = 1 },
		"a requirement added":     func(d *capability.DataDefinition) { d.Requires = append(d.Requires, capability.NamePOSIXFileSystem) },
		"the requirements gone":   func(d *capability.DataDefinition) { d.Requires = nil },
		"a property's type":       func(d *capability.DataDefinition) { d.Properties[3].Type = "string" },
		"a property now required": func(d *capability.DataDefinition) { d.Properties[2].Required = true },
		"a property renamed":      func(d *capability.DataDefinition) { d.Properties[2].Name = "always_free" },
		"a property added": func(d *capability.DataDefinition) {
			d.Properties = append(d.Properties, capability.DataProperty{Name: "realm", Type: "string"})
		},
		"a property removed": func(d *capability.DataDefinition) { d.Properties = d.Properties[:3] },
	} {
		t.Run(name, func(t *testing.T) {
			def := tenancy()
			mutate(&def)
			if got := capability.DefinitionDigest(def); got == base {
				t.Errorf("changing %s left the digest at %s", name, got)
			}
		})
	}
}

// TestIsDefinitionDigest_IsTheDigestsShape: a digest has its prefix and
// 64 lower case hex digits, and nothing else passes.
func TestIsDefinitionDigest_IsTheDigestsShape(t *testing.T) {
	good := capability.DefinitionDigest(tenancy())
	if !capability.IsDefinitionDigest(good) {
		t.Fatalf("%s refused", good)
	}
	for _, bad := range []string{
		"",
		strings.TrimPrefix(good, capability.DefinitionDigestPrefix),
		"sha512:" + strings.TrimPrefix(good, capability.DefinitionDigestPrefix),
		strings.ToUpper(good),
		good[:len(good)-1],
		good + "0",
		good + "\n",
	} {
		if capability.IsDefinitionDigest(bad) {
			t.Errorf("IsDefinitionDigest(%q) = true", bad)
		}
	}
}

// TestValidateData_HoldsTheRevisionToItsBounds: a revision from 0 to
// MaxDataRevision is a definition's own business; below or above is
// refused, naming the capability.
func TestValidateData_HoldsTheRevisionToItsBounds(t *testing.T) {
	for _, rev := range []int{0, 1, capability.MaxDataRevision} {
		def := tenancy()
		def.Revision = rev
		if err := capability.ValidateData(def); err != nil {
			t.Errorf("revision %d refused: %v", rev, err)
		}
	}
	for _, rev := range []int{-1, capability.MaxDataRevision + 1} {
		def := tenancy()
		def.Revision = rev
		if err := capability.ValidateData(def); err == nil || !strings.Contains(err.Error(), `"oci.tenancy_api"`) {
			t.Errorf("revision %d = %v, want a refusal naming the capability", rev, err)
		}
	}
}
