package syncplugin_test

import (
	"context"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// stubPlugin is the minimum Plugin a registry test needs. It is not a mock
// of anything under test: the registry stores constructors, so it needs
// something constructible and nothing more.
type stubPlugin struct{}

func (stubPlugin) Connect(context.Context, syncplugin.Config) error { return nil }

func (stubPlugin) Discover(context.Context) (syncplugin.RecordIterator, error) {
	return nil, syncplugin.ErrNotConnected
}

func (stubPlugin) Classify(context.Context, record.Record) (syncplugin.Classification, error) {
	return syncplugin.Quarantine("stub"), nil
}

func (stubPlugin) Sync(context.Context, inv.Repository) (syncplugin.Reconciliation, error) {
	return syncplugin.Reconciliation{}, nil
}

func (stubPlugin) Close() error { return nil }

// TestRegister_Rejects covers the descriptor validation that runs before a
// plugin ever reaches the table. Each rejection exists because the
// alternative is a failure much further away from its cause.
func TestRegister_Rejects(t *testing.T) {
	t.Cleanup(syncplugin.SnapshotForTest())
	tests := []struct {
		name    string
		desc    syncplugin.Descriptor
		wantErr string
	}{
		{
			name:    "no name",
			desc:    syncplugin.Descriptor{New: func(syncplugin.Deps) syncplugin.Plugin { return stubPlugin{} }},
			wantErr: "no name",
		},
		{
			name:    "whitespace name",
			desc:    syncplugin.Descriptor{Name: "  ", New: func(syncplugin.Deps) syncplugin.Plugin { return stubPlugin{} }},
			wantErr: "no name",
		},
		{
			name:    "no constructor",
			desc:    syncplugin.Descriptor{Name: "registry_test_no_ctor"},
			wantErr: "no constructor",
		},
		{
			// A default config naming a different plugin would stamp every
			// synced device with a source authority that does not match the
			// name it was looked up under.
			name: "default config names a different plugin",
			desc: syncplugin.Descriptor{
				Name:          "registry_test_mismatch",
				DefaultConfig: syncplugin.Config{Name: "something_else"},
				New:           func(syncplugin.Deps) syncplugin.Plugin { return stubPlugin{} },
			},
			wantErr: "default config named",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := syncplugin.Register(tt.desc)
			if err == nil {
				t.Fatalf("Register() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Register() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestRegister_RoundTripAndDuplicate proves a registered plugin is
// retrievable and that a second registration under the same name is
// refused rather than silently replacing the first.
func TestRegister_RoundTripAndDuplicate(t *testing.T) {
	t.Cleanup(syncplugin.SnapshotForTest())
	const name = "registry_test_roundtrip"

	desc := syncplugin.Descriptor{
		Name:          name,
		Description:   "a stub used only by this test",
		DefaultConfig: syncplugin.Config{Name: name, ReadOnly: true},
		New:           func(syncplugin.Deps) syncplugin.Plugin { return stubPlugin{} },
	}
	if err := syncplugin.Register(desc); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := syncplugin.Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) found nothing", name)
	}
	if !got.DefaultConfig.ReadOnly {
		t.Error("expected the registered default config to survive lookup")
	}
	if got.New(syncplugin.Deps{}) == nil {
		t.Error("expected the registered constructor to build a plugin")
	}

	if err := syncplugin.Register(desc); err == nil {
		t.Error("expected a duplicate registration to be refused")
	}
}

// TestNames_IsSorted proves Names returns a stable ordering, which help
// text and tests both depend on.
//
// It registers its own plugins, in an order that is not the sorted one, so
// that it actually exercises the sort. It used to assert over whatever
// happened to be in the process-wide registry, which in this package's own
// test binary is nothing at all: the real plugins live in
// internal/inventory/plugins and are never linked here. That made this a
// test over an empty slice whose loop body never ran, passing for a reason
// unrelated to sorting -- visible only once the registrations other tests
// were leaking got cleaned up, which is what turned "0 or 1 entries" into
// the honest three below.
func TestNames_IsSorted(t *testing.T) {
	t.Cleanup(syncplugin.SnapshotForTest())

	for _, name := range []string{"zulu_sorted", "alpha_sorted", "mike_sorted"} {
		if err := syncplugin.Register(syncplugin.Descriptor{
			Name:          name,
			Description:   "a stub used only by this test",
			DefaultConfig: syncplugin.Config{Name: name, ReadOnly: true},
			New:           func(syncplugin.Deps) syncplugin.Plugin { return stubPlugin{} },
		}); err != nil {
			t.Fatalf("Register(%q): %v", name, err)
		}
	}

	names := syncplugin.Names()
	if len(names) < 3 {
		t.Fatalf("Names() = %v, want at least the three just registered", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names() is not sorted: %q came before %q", names[i-1], names[i])
		}
	}
}
