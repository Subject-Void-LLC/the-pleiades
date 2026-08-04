package inventory_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

func TestStaticYAMLPlugin_Load(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.yaml")

	hosts := []inventory.HostSpec{
		{
			ID:   "11111111-1111-1111-1111-111111111111",
			Name: "webserver1",
			Type: "linux_server",
			Tags: []string{"web", "prod"},
			Properties: map[string]interface{}{
				"host":         "10.0.0.5",
				"distribution": "ubuntu",
			},
		},
	}

	if err := inventory.WriteHosts(path, hosts); err != nil {
		t.Fatalf("failed to write inventory: %v", err)
	}

	plugin := inventory.NewStaticYAMLPlugin(path, inventory.NewItemFactory())
	items, err := plugin.Load()
	if err != nil {
		t.Fatalf("failed to load inventory: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Name() != "webserver1" {
		t.Errorf("expected name 'webserver1', got %q", item.Name())
	}
	if item.ID() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("expected the explicit id to survive hydration, got %q", item.ID())
	}
	if !item.State().CanExecute() {
		t.Error("expected a statically listed host to be immediately active")
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Error("expected linux_server to declare LinuxCapable")
	}
	if item.Source().Plugin != "static_yaml" {
		t.Errorf("expected source plugin 'static_yaml', got %q", item.Source().Plugin)
	}
}

// TestHostsRoundTrip proves ParseHosts -> EncodeHosts -> ParseHosts is
// lossless at the data level: this is the YAML layer's round-trip
// requirement for the inventory side (yaml_test.go in internal/engine
// covers the runbook side).
func TestHostsRoundTrip(t *testing.T) {
	original := []inventory.HostSpec{
		{
			ID:   "id-1",
			Name: "rtr1",
			Type: "cisco_router",
			Tags: []string{"edge"},
			Properties: map[string]interface{}{
				"host":        "10.0.0.1",
				"ios_version": "17.3.2",
			},
		},
		{
			ID:   "id-2",
			Name: "web1",
			Type: "linux_server",
		},
	}

	data, err := inventory.EncodeHosts(nil, original)
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	parsed, err := inventory.ParseHosts(data)
	if err != nil {
		t.Fatalf("failed to parse re-encoded hosts: %v", err)
	}

	if len(parsed) != len(original) {
		t.Fatalf("expected %d hosts after round-trip, got %d", len(original), len(parsed))
	}
	// reflect.DeepEqual, not a field-by-field spot check: the original
	// version of this test compared only ID/Name/Type and silently never
	// caught a Tags or Properties regression (the chain audit's
	// verification pass, .SPECIFICATION/IMPLEMENTATION.md Phase W2).
	for i, h := range original {
		if !reflect.DeepEqual(parsed[i], h) {
			t.Errorf("host %d changed across round-trip: got %+v, want %+v", i, parsed[i], h)
		}
	}
}

func TestParseHosts_MissingType(t *testing.T) {
	data := []byte("hosts:\n  - name: no-type-host\n")
	hosts, err := inventory.ParseHosts(data)
	if err != nil {
		t.Fatalf("parsing itself should not fail on a missing type: %v", err)
	}

	factory := inventory.NewItemFactory()
	if _, err := inventory.HydrateHosts(factory, hosts); err == nil {
		t.Error("expected hydration to reject a host with no type")
	}
}

// TestHydrateHosts_ClassifyOnly proves a hand-written entry carrying only
// Classify (no Type) hydrates through the real DefaultRuleSet, the
// resolver's first consumer wired into the real hydration path rather than
// exercised only inside internal/classification's own package tests.
func TestHydrateHosts_ClassifyOnly(t *testing.T) {
	hosts := []inventory.HostSpec{
		{Name: "web1", Classify: []string{"linux_server", "debian_family", "ubuntu"}},
	}

	factory := inventory.NewItemFactory()
	items, err := inventory.HydrateHosts(factory, hosts)
	if err != nil {
		t.Fatalf("HydrateHosts: unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !items[0].HasCapability(capability.NameLinux) {
		t.Error("classify-resolved host does not declare LinuxCapable; classification did not resolve to linux_server")
	}
}

// TestHydrateHosts_TypeWinsOverClassify proves Type always wins when both
// are set: an intentionally unresolvable Classify path must not cause an
// error, since Type short-circuits ResolveHostType before Classify is ever
// consulted.
func TestHydrateHosts_TypeWinsOverClassify(t *testing.T) {
	hosts := []inventory.HostSpec{
		{Name: "web1", Type: "linux_server", Classify: []string{"totally_unresolvable_path"}},
	}

	factory := inventory.NewItemFactory()
	items, err := inventory.HydrateHosts(factory, hosts)
	if err != nil {
		t.Fatalf("HydrateHosts: unexpected error (Type should have won over an unresolvable Classify): %v", err)
	}
	if !items[0].HasCapability(capability.NameLinux) {
		t.Error("expected the explicit Type to determine the hydrated concrete type")
	}
}

func TestHydrateHosts_ClassifyUnresolvableErrors(t *testing.T) {
	hosts := []inventory.HostSpec{
		{Name: "mystery", Classify: []string{"totally_unresolvable_path"}},
	}

	factory := inventory.NewItemFactory()
	if _, err := inventory.HydrateHosts(factory, hosts); err == nil {
		t.Error("expected hydration to fail on a Classify path DefaultRuleSet has no rule for")
	}
}

// TestHostsRoundTrip_PersistsClassify is the regression test for the
// headline bug the classification wiring's design review caught:
// EncodeHosts/WriteHosts previously had no idea Classify existed, so it
// would parse fine on read but silently vanish on the very next write.
func TestHostsRoundTrip_PersistsClassify(t *testing.T) {
	original := []inventory.HostSpec{
		{ID: "id-1", Name: "web1", Classify: []string{"linux_server", "debian_family", "ubuntu"}},
	}

	// In-memory round trip (EncodeHosts/ParseHosts).
	data, err := inventory.EncodeHosts(nil, original)
	if err != nil {
		t.Fatalf("EncodeHosts: %v", err)
	}
	parsed, err := inventory.ParseHosts(data)
	if err != nil {
		t.Fatalf("ParseHosts: %v", err)
	}
	if !reflect.DeepEqual(parsed, original) {
		t.Fatalf("EncodeHosts/ParseHosts round trip = %+v, want %+v", parsed, original)
	}

	// The real on-disk path (WriteHosts/ReadHosts), which is what add-host
	// and Save actually call, going through mergeHostsIntoDocument rather
	// than a fresh yaml.Marshal.
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.yaml")
	if err := inventory.WriteHosts(path, original); err != nil {
		t.Fatalf("WriteHosts: %v", err)
	}
	fromDisk, err := inventory.ReadHosts(path)
	if err != nil {
		t.Fatalf("ReadHosts: %v", err)
	}
	if !reflect.DeepEqual(fromDisk, original) {
		t.Fatalf("WriteHosts/ReadHosts round trip = %+v, want %+v (Classify must survive the merge path)", fromDisk, original)
	}
}
