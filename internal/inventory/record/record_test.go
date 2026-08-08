package record_test

import (
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// aptDevice is a synthetic device type embedding *record.Base and
// structurally implementing capability.AptCapable, the same shape
// Router/Server use in production. It exists to prove the Release Gate's
// worked example -- a task requiring the broad PackageManagerCapable
// against a device declaring only the narrower AptCapable resolves true
// at the capability layer this phase owns -- without needing a real
// generated device type (Phase 33/34 territory).
type aptDevice struct {
	*record.Base
}

func (aptDevice) PackageManagerName() string { return "apt" }
func (aptDevice) AptSourcesList() string     { return "/etc/apt/sources.list" }

func (d aptDevice) HasCapability(name capability.Name) bool {
	return d.Declares(name) && capability.Implements(d, name)
}

func newAptDevice(caps []capability.Name) aptDevice {
	base := record.NewBase(record.Record{ID: "dev1", Name: "dev1"}, caps)
	return aptDevice{Base: base}
}

func TestBase_Declares_DirectMatch(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt})
	if !dev.Declares(capability.NameApt) {
		t.Error("expected a directly declared capability to be Declared")
	}
}

// TestBase_HasCapability_HierarchyResolution is the Release Gate's own
// worked example: a task calling pkg.install against a device declaring
// only AptCapable resolves to apt at plan time (IMPLEMENTATION.md Phase
// 32). At this layer that means HasCapability(PackageManagerCapable)
// resolves true through Declares' Parent-chain walk plus Implements'
// structural interface embedding, while an unrelated sibling
// (DnfCapable) stays false.
func TestBase_HasCapability_HierarchyResolution(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt})

	if !dev.HasCapability(capability.NamePackageManager) {
		t.Error("expected a device declaring only AptCapable to satisfy the broader PackageManagerCapable")
	}
	if dev.HasCapability(capability.NameDnf) {
		t.Error("expected a device declaring only AptCapable to NOT satisfy the unrelated sibling DnfCapable")
	}
}

func TestBase_Declares_EmptyCapabilitySet(t *testing.T) {
	dev := newAptDevice(nil)
	if dev.Declares(capability.NameApt) {
		t.Error("expected an empty capability set to declare nothing")
	}
	if dev.HasCapability(capability.NamePackageManager) {
		t.Error("expected an empty capability set to satisfy nothing, including via the hierarchy")
	}
}

func TestBase_Capabilities_ReturnsDeclaredSet(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt, capability.NameSSHTransport})
	got := dev.Capabilities()

	want := map[capability.Name]bool{capability.NameApt: true, capability.NameSSHTransport: true}
	if len(got) != len(want) {
		t.Fatalf("Capabilities() = %v, want exactly %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("Capabilities() included unexpected %q", name)
		}
	}
}

// TestBase_AccessorsReflectTheHydratingRecord exercises every simple
// accessor Base offers, and forces NewBase's own Properties/Tags/History
// copy loops to run over a non-empty source (a Record with at least one
// entry in each), since a Record with nil slices/maps takes those loops'
// zero-iteration path and never proves they copy correctly.
func TestBase_AccessorsReflectTheHydratingRecord(t *testing.T) {
	syncedAt := time.Now().UTC()
	rec := record.Record{
		ID:   "dev1",
		Name: "dev1.example.com",
		Type: "cisco_router",
		Properties: map[string]inventory.PropertyValue{
			"model": "ISR4451",
		},
		Tags:  []inventory.Tag{"edge", "prod"},
		State: inventory.StateActive,
		Source: inventory.SourceAuthority{
			Plugin:   "static_yaml",
			SyncedAt: syncedAt,
		},
		Version: 3,
		History: []inventory.Revision{
			{Version: 1, Field: "model", NewValue: "ISR4451"},
		},
	}

	base := record.NewBase(rec, []capability.Name{capability.NameApt})

	if got := base.ID(); got != rec.ID {
		t.Errorf("ID() = %q, want %q", got, rec.ID)
	}
	if got := base.Name(); got != rec.Name {
		t.Errorf("Name() = %q, want %q", got, rec.Name)
	}
	if got := base.DeviceType(); got != rec.Type {
		t.Errorf("DeviceType() = %q, want %q", got, rec.Type)
	}
	if got := base.Version(); got != rec.Version {
		t.Errorf("Version() = %d, want %d", got, rec.Version)
	}
	if got := base.BaseVersion(); got != rec.Version {
		t.Errorf("BaseVersion() = %d, want %d", got, rec.Version)
	}
	if got := base.State(); got != rec.State {
		t.Errorf("State() = %v, want %v", got, rec.State)
	}
	if got := base.Source(); got != rec.Source {
		t.Errorf("Source() = %+v, want %+v", got, rec.Source)
	}

	props := base.Properties()
	if got, ok := props.Raw()["model"]; !ok || got != "ISR4451" {
		t.Errorf("Properties()[%q] = %v, %v; want %q, true", "model", got, ok, "ISR4451")
	}

	gotTags := base.Tags()
	wantTags := map[inventory.Tag]bool{"edge": true, "prod": true}
	if len(gotTags) != len(wantTags) {
		t.Fatalf("Tags() = %v, want exactly %v", gotTags, wantTags)
	}
	for _, tag := range gotTags {
		if !wantTags[tag] {
			t.Errorf("Tags() included unexpected %q", tag)
		}
	}

	gotHistory := base.History()
	if len(gotHistory) != 1 || gotHistory[0].Field != "model" {
		t.Errorf("History() = %+v, want a single %q revision", gotHistory, "model")
	}

	// ShowInfo is documented to delegate to Properties: prove it returns the
	// same data.
	if got, ok := base.ShowInfo().Raw()["model"]; !ok || got != "ISR4451" {
		t.Errorf("ShowInfo()[%q] = %v, %v; want %q, true", "model", got, ok, "ISR4451")
	}
}

func TestBase_AddInfo(t *testing.T) {
	base := record.NewBase(record.Record{ID: "dev1"}, nil)

	if err := base.AddInfo("model", "ISR4451", false); err != nil {
		t.Fatalf("AddInfo on a fresh key: unexpected error: %v", err)
	}
	if got := base.Version(); got != 1 {
		t.Errorf("Version() after one AddInfo = %d, want 1", got)
	}
	if got, ok := base.Properties().Raw()["model"]; !ok || got != "ISR4451" {
		t.Errorf("Properties()[%q] = %v, %v; want %q, true", "model", got, ok, "ISR4451")
	}

	if err := base.AddInfo("model", "ISR4331", false); err == nil {
		t.Fatal("AddInfo on an existing key without overwrite: expected an error, got nil")
	}
	if got := base.Version(); got != 1 {
		t.Errorf("Version() after a rejected AddInfo = %d, want unchanged 1", got)
	}

	if err := base.AddInfo("model", "ISR4331", true); err != nil {
		t.Fatalf("AddInfo on an existing key with overwrite: unexpected error: %v", err)
	}
	if got := base.Version(); got != 2 {
		t.Errorf("Version() after an overwriting AddInfo = %d, want 2", got)
	}
	if got, ok := base.Properties().Raw()["model"]; !ok || got != "ISR4331" {
		t.Errorf("Properties()[%q] = %v, %v; want %q, true", "model", got, ok, "ISR4331")
	}

	history := base.History()
	if len(history) != 2 {
		t.Fatalf("History() after two AddInfo calls = %d entries, want 2", len(history))
	}
	if history[1].OldValue != "ISR4451" || history[1].NewValue != "ISR4331" {
		t.Errorf("History()[1] = %+v, want OldValue %q, NewValue %q", history[1], "ISR4451", "ISR4331")
	}
}

func TestBase_RemoveInfo(t *testing.T) {
	base := record.NewBase(record.Record{
		ID:         "dev1",
		Properties: map[string]inventory.PropertyValue{"model": "ISR4451"},
	}, nil)

	if err := base.RemoveInfo("does_not_exist"); err == nil {
		t.Fatal("RemoveInfo on a missing key: expected an error, got nil")
	}

	if err := base.RemoveInfo("model"); err != nil {
		t.Fatalf("RemoveInfo on an existing key: unexpected error: %v", err)
	}
	if _, ok := base.Properties().Raw()["model"]; ok {
		t.Error("Properties() still contains a key after RemoveInfo")
	}
	if got := base.Version(); got != 1 {
		t.Errorf("Version() after one RemoveInfo = %d, want 1", got)
	}

	history := base.History()
	if len(history) != 1 || history[0].NewValue != nil || history[0].OldValue != "ISR4451" {
		t.Errorf("History() after RemoveInfo = %+v, want one entry with OldValue %q and nil NewValue", history, "ISR4451")
	}
}

func TestNewBase_CapabilitiesFieldIsNotConsultedDirectly(t *testing.T) {
	// NewBase's caps parameter, not rec.Capabilities, determines what a
	// Base declares -- callers (Router/Server) decide how the two combine
	// (Phase 32's constructors union them; this proves NewBase itself does
	// no implicit merging of its own).
	rec := record.Record{ID: "dev1", Capabilities: []capability.Name{capability.NameDnf}}
	base := record.NewBase(rec, []capability.Name{capability.NameApt})

	if base.Declares(capability.NameDnf) {
		t.Error("expected NewBase to ignore rec.Capabilities and use only its own caps parameter")
	}
	if !base.Declares(capability.NameApt) {
		t.Error("expected NewBase's explicit caps parameter to be what Declares reflects")
	}
}
