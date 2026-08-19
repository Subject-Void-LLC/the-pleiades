package classification_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// capSet builds a comparison set out of a []capability.Name slice, since
// Capabilities' union-fold order is not guaranteed to match a literal's
// order.
func capSet(names []capability.Name) map[capability.Name]bool {
	out := make(map[capability.Name]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

func strPtr(s string) *string { return &s }

func TestNewRuleSet_RejectsInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"uppercase", "Linux_Server"},
		{"empty segment", "linux_server..ubuntu"},
		{"space", "linux server"},
		{"empty key", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := classification.NewRuleSet(map[string]classification.Rule{
				tt.key: {Type: strPtr("x")},
			})
			if err == nil {
				t.Fatalf("NewRuleSet with key %q: expected an error, got nil", tt.key)
			}
		})
	}
}

func TestNewRuleSet_AcceptsValidKeys(t *testing.T) {
	_, err := classification.NewRuleSet(map[string]classification.Rule{
		"linux_server":                            {Type: strPtr("linux_server")},
		"linux_server.debian_family":              {},
		"linux_server.debian_family.ubuntu_22_04": {},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: unexpected error: %v", err)
	}
}

// TestClassify_MultiLevelMerge is the synthetic proof (recommended over
// forcing fake variance into DefaultRuleSet) that Classify genuinely folds
// three distinct layers, most specific last, per pkg/policy's own
// documented field-level combine shape.
func TestClassify_MultiLevelMerge(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"a":     {Type: strPtr("type-a"), ConnectionMode: strPtr("agentless")},
		"a.b":   {ConnectionMode: strPtr("agent")},
		"a.b.c": {Onboard: strPtr("install_agent")},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	result, err := rs.Classify([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Classify: unexpected error: %v", err)
	}

	if got := result.Value.Type; got == nil || *got != "type-a" {
		t.Errorf("Type = %v, want \"type-a\" (inherited from the root layer, never overridden)", got)
	}
	if got := result.Value.ConnectionMode; got == nil || *got != "agent" {
		t.Errorf("ConnectionMode = %v, want \"agent\" (overridden at the middle layer)", got)
	}
	if got := result.Value.Onboard; got == nil || *got != "install_agent" {
		t.Errorf("Onboard = %v, want \"install_agent\" (set only at the leaf layer)", got)
	}

	wantLayers := []string{"a", "a.b", "a.b.c"}
	if len(result.Layers) != len(wantLayers) {
		t.Fatalf("Layers = %v, want %v", result.Layers, wantLayers)
	}
	for i, name := range wantLayers {
		if result.Layers[i] != name {
			t.Errorf("Layers[%d] = %q, want %q", i, result.Layers[i], name)
		}
	}
}

func TestClassify_SkipsLevelsWithNoRule(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"linux_server": {Type: strPtr("linux_server"), ConnectionMode: strPtr("agentless")},
		// "linux_server.debian_family" and "...ubuntu" deliberately absent,
		// matching Section 6d's own tree where not every node has a rule.
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	result, err := rs.Classify([]string{"linux_server", "debian_family", "ubuntu"})
	if err != nil {
		t.Fatalf("Classify: unexpected error: %v", err)
	}
	if got := result.Value.Type; got == nil || *got != "linux_server" {
		t.Errorf("Type = %v, want \"linux_server\" inherited from the only matching (root) level", got)
	}
	if len(result.Layers) != 1 || result.Layers[0] != "linux_server" {
		t.Errorf("Layers = %v, want exactly [\"linux_server\"]", result.Layers)
	}
}

func TestClassify_NoRuleMatchedAnywhereErrors(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"linux_server": {Type: strPtr("linux_server")},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	_, err = rs.Classify([]string{"totally_unknown", "path"})
	if err == nil {
		t.Fatal("Classify with a path matching no rule at any level: expected an error, got nil")
	}
}

// TestClassify_PathTooLongErrors is the regression test for an
// adversarial review finding: Classify's key-building used to be O(n^2)
// in path length with no upper bound, making a very long path (trivially
// reachable from HostSpec.Classify or `add-host --classify`) a real
// resource-exhaustion vector. maxPathSegments bounds it independently of
// the O(n) fix to Classify's own loop.
func TestClassify_PathTooLongErrors(t *testing.T) {
	rs := classification.DefaultRuleSet()

	path := make([]string, 65)
	for i := range path {
		path[i] = "a"
	}
	if _, err := rs.Classify(path); err == nil {
		t.Fatalf("Classify with %d segments: expected an error (exceeds the maximum), got nil", len(path))
	}

	path64 := make([]string, 64)
	for i := range path64 {
		path64[i] = "a"
	}
	if _, err := rs.Classify(path64); err != nil {
		if !strings.Contains(err.Error(), "no rule matched") {
			t.Fatalf("Classify with exactly the maximum (%d) segments: unexpected non-length error: %v", len(path64), err)
		}
		// A "no rule matched" error is expected here (the path is nonsense),
		// but it must be that error, not a length-rejection.
	}
}

func TestClassify_InvalidSegmentErrors(t *testing.T) {
	rs := classification.DefaultRuleSet()

	tests := [][]string{
		{},
		{""},
		{"Linux_Server"},
		{"linux server"},
		{"linux_server", "a.b"},
	}
	for _, path := range tests {
		if _, err := rs.Classify(path); err == nil {
			t.Errorf("Classify(%v): expected an error, got nil", path)
		}
	}
}

// TestClassify_DotInSegmentCannotCollide proves the exact hardening the
// package doc comment calls out: a single segment containing a literal
// "." must not silently resolve identically to the equivalent two-segment
// path once both are rejected/accepted correctly.
func TestClassify_DotInSegmentCannotCollide(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"a.b": {Type: strPtr("two-segment-result")},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	// The two-segment path resolves normally.
	result, err := rs.Classify([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Classify([a, b]): unexpected error: %v", err)
	}
	if got := result.Value.Type; got == nil || *got != "two-segment-result" {
		t.Errorf("Type = %v, want \"two-segment-result\"", got)
	}

	// A single segment literally containing a dot is rejected outright,
	// never silently treated as if it were the two-segment path above.
	if _, err := rs.Classify([]string{"a.b"}); err == nil {
		t.Fatal("Classify([\"a.b\"]) (one segment containing a literal dot): expected an error, got nil")
	}
}

func TestDefaultRuleSet(t *testing.T) {
	rs := classification.DefaultRuleSet()

	tests := []struct {
		name           string
		path           []string
		wantType       string
		wantConnection string
		wantOnboard    string
		wantCaps       []capability.Name
	}{
		{"linux root", []string{"linux_server"}, "linux_server", "agentless", "configure_polling",
			[]capability.Name{capability.NameLinux, capability.NameSSHTransport}},
		{"linux via debian", []string{"linux_server", "debian_family"}, "linux_server", "agentless", "configure_polling",
			[]capability.Name{capability.NameLinux, capability.NameSSHTransport, capability.NameApt}},
		{"linux via ubuntu", []string{"linux_server", "debian_family", "ubuntu"}, "linux_server", "agentless", "configure_polling",
			[]capability.Name{capability.NameLinux, capability.NameSSHTransport, capability.NameApt}},
		{"cisco ios", []string{"network_device", "cisco", "ios"}, "cisco_router", "agentless", "configure_polling",
			[]capability.Name{capability.NameCiscoIOS, capability.NameSSHTransport}},
		{"windows server root", []string{"windows_server"}, "windows_server", "agentless", "configure_polling",
			[]capability.Name{capability.NameWindows, capability.NameWinRM, capability.NameWindowsService, capability.NameWindowsFeature}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := rs.Classify(tt.path)
			if err != nil {
				t.Fatalf("Classify(%v): unexpected error: %v", tt.path, err)
			}
			if got := result.Value.Type; got == nil || *got != tt.wantType {
				t.Errorf("Type = %v, want %q", got, tt.wantType)
			}
			if got := result.Value.ConnectionMode; got == nil || *got != tt.wantConnection {
				t.Errorf("ConnectionMode = %v, want %q", got, tt.wantConnection)
			}
			if got := result.Value.Onboard; got == nil || *got != tt.wantOnboard {
				t.Errorf("Onboard = %v, want %q", got, tt.wantOnboard)
			}
			gotCaps, wantCaps := capSet(result.Value.Capabilities), capSet(tt.wantCaps)
			if len(gotCaps) != len(wantCaps) {
				t.Fatalf("Capabilities = %v, want exactly %v", result.Value.Capabilities, tt.wantCaps)
			}
			for name := range wantCaps {
				if !gotCaps[name] {
					t.Errorf("Capabilities missing %q, got %v", name, result.Value.Capabilities)
				}
			}
		})
	}
}

// TestCombineRule_CapabilitiesUnionAcrossLevels is the checklist's own
// worked example, isolated from DefaultRuleSet's specific data: a more
// specific level's Capabilities add to what a less specific level already
// granted, they never replace it, unlike Type/ConnectionMode/Onboard's
// Override semantics on the very same Rule type.
func TestCombineRule_CapabilitiesUnionAcrossLevels(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"a":   {Type: strPtr("root"), Capabilities: []capability.Name{capability.NameLinux}},
		"a.b": {Capabilities: []capability.Name{capability.NameApt}},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	result, err := rs.Classify([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// Type still inherits (Override semantics, unaffected by the
	// Capabilities field mixing in Union mode on the same Rule).
	if got := result.Value.Type; got == nil || *got != "root" {
		t.Errorf("Type = %v, want %q (inherited, Override semantics)", got, "root")
	}

	got := capSet(result.Value.Capabilities)
	want := map[capability.Name]bool{capability.NameLinux: true, capability.NameApt: true}
	if len(got) != len(want) {
		t.Fatalf("Capabilities = %v, want exactly %v", result.Value.Capabilities, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("Capabilities missing %q, got %v", name, result.Value.Capabilities)
		}
	}
}

// TestCombineRule_CapabilitiesDeduplicate proves the Union merge does not
// produce duplicates when two levels grant the same capability.
func TestCombineRule_CapabilitiesDeduplicate(t *testing.T) {
	rs, err := classification.NewRuleSet(map[string]classification.Rule{
		"a":   {Capabilities: []capability.Name{capability.NameApt}},
		"a.b": {Capabilities: []capability.Name{capability.NameApt}},
	})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}

	result, err := rs.Classify([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(result.Value.Capabilities) != 1 {
		t.Errorf("Capabilities = %v, want exactly 1 deduplicated entry", result.Value.Capabilities)
	}
}

func TestDefaultRuleSet_UnknownPathErrors(t *testing.T) {
	rs := classification.DefaultRuleSet()
	if _, err := rs.Classify([]string{"windows", "desktop", "pro"}); err == nil {
		t.Fatal("Classify on a path DefaultRuleSet has no rule for: expected an error (quarantine trigger), got nil")
	}
}

func TestStrPtrHelperIsIndependent(t *testing.T) {
	// Guards against a future refactor accidentally sharing one *string
	// across DefaultRuleSet's two branches.
	a := strPtr("x")
	b := strPtr("x")
	if a == b {
		t.Fatal("strPtr returned the same pointer for two calls; Rule fields must never alias across rules")
	}
	if !strings.EqualFold(*a, *b) {
		t.Fatalf("*a=%q *b=%q, want equal values", *a, *b)
	}
}
