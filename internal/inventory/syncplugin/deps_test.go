package syncplugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// openStub is the plugin every descriptor below constructs. It records
// the Deps it was handed, so a test can assert the store really reached
// the plugin rather than only that Open returned without error, which is
// the distinction the defect this file guards against turned on.
type openStub struct {
	deps syncplugin.Deps
}

func (p *openStub) Connect(context.Context, syncplugin.Config) error { return nil }

func (p *openStub) Discover(context.Context) (syncplugin.RecordIterator, error) {
	return nil, syncplugin.ErrNotConnected
}

func (p *openStub) Classify(context.Context, record.Record) (syncplugin.Classification, error) {
	return syncplugin.Quarantine("stub"), nil
}

func (p *openStub) Sync(context.Context, inv.Repository) (syncplugin.Reconciliation, error) {
	return syncplugin.Reconciliation{}, syncplugin.ErrNotConnected
}

func (p *openStub) Close() error { return nil }

// staticStore is a credential.Store standing in for the project's own.
type staticStore struct{}

func (staticStore) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{Username: "u"}, nil
}

// openDescriptor builds a descriptor whose constructor records its Deps.
func openDescriptor(name string, settings []syncplugin.SettingSpec, requiresCredentials bool) (syncplugin.Descriptor, **openStub) {
	built := new(*openStub)
	return syncplugin.Descriptor{
		Name:                name,
		Description:         "a descriptor built for a test",
		DefaultConfig:       syncplugin.Config{Name: name},
		Settings:            settings,
		RequiresCredentials: requiresCredentials,
		New: func(deps syncplugin.Deps) syncplugin.Plugin {
			p := &openStub{deps: deps}
			*built = p
			return p
		},
	}, built
}

// TestOpen_HandsTheConstructorItsDeps is the assertion that would have
// failed before this port took Deps at all: the store a composition root
// supplies has to reach the plugin, not merely be accepted by Open.
func TestOpen_HandsTheConstructorItsDeps(t *testing.T) {
	desc, built := openDescriptor("stub_deps", nil, true)
	store := staticStore{}

	plugin, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_deps"}, syncplugin.Deps{Credentials: store})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plugin == nil {
		t.Fatal("Open returned a nil plugin with no error")
	}
	if *built == nil {
		t.Fatal("Open did not call the descriptor's constructor")
	}
	if (*built).deps.Credentials != credential.Store(store) {
		t.Error("the credential store the caller supplied did not reach the constructor")
	}
}

// TestOpen_RefusesAMissingCredentialStore proves RequiresCredentials
// changes the outcome rather than being a field nobody reads.
func TestOpen_RefusesAMissingCredentialStore(t *testing.T) {
	desc, _ := openDescriptor("stub_needs_creds", nil, true)

	_, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_needs_creds"}, syncplugin.Deps{})
	if err == nil {
		t.Fatal("Open with no credential store: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "credential store") {
		t.Errorf("error %q does not say what is missing", err)
	}
}

// TestOpen_BuildsAPluginThatNeedsNothing is the control for the test
// above: a plugin not declaring RequiresCredentials must still build
// with an empty Deps, or the refusal would be unconditional and would
// prove nothing about the flag.
func TestOpen_BuildsAPluginThatNeedsNothing(t *testing.T) {
	desc, _ := openDescriptor("stub_needs_nothing", nil, false)

	plugin, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_needs_nothing"}, syncplugin.Deps{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plugin == nil {
		t.Fatal("Open returned a nil plugin with no error")
	}
}

// TestOpen_RefusesAMissingRequiredSetting proves the refusal names both
// the setting and what it means, so an operator can act on it without
// reading source. That is the whole difference from the error this
// replaced, which named a Go constructor option no user can call.
func TestOpen_RefusesAMissingRequiredSetting(t *testing.T) {
	desc, _ := openDescriptor("stub_needs_region", []syncplugin.SettingSpec{
		{Name: "region", Description: "the region to read from", Required: true},
		{Name: "profile", Description: "an optional profile name", Required: false},
	}, false)

	_, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_needs_region"}, syncplugin.Deps{})
	if err == nil {
		t.Fatal("Open with no region: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Errorf("error %q does not name the missing setting", err)
	}
	if !strings.Contains(err.Error(), "the region to read from") {
		t.Errorf("error %q does not carry the setting's description", err)
	}
	if strings.Contains(err.Error(), "profile") {
		t.Errorf("error %q reports an optional setting as missing", err)
	}
}

// TestOpen_AcceptsASuppliedSetting proves a supplied value satisfies the
// requirement, and that whitespace alone does not.
func TestOpen_AcceptsASuppliedSetting(t *testing.T) {
	desc, _ := openDescriptor("stub_region_supplied", []syncplugin.SettingSpec{
		{Name: "region", Description: "the region to read from", Required: true},
	}, false)

	cfg := syncplugin.Config{Name: "stub_region_supplied", Settings: map[string]string{"region": "us-east-1"}}
	if _, err := syncplugin.Open(desc, cfg, syncplugin.Deps{}); err != nil {
		t.Fatalf("Open with the setting supplied: %v", err)
	}

	blank := syncplugin.Config{Name: "stub_region_supplied", Settings: map[string]string{"region": "   "}}
	if _, err := syncplugin.Open(desc, blank, syncplugin.Deps{}); err == nil {
		t.Error("Open accepted a setting holding only whitespace, which supplies nothing")
	}
}

// TestOpen_RefusesABrokenDescriptorOrConfig covers the two failures that
// are the caller's own rather than the operator's.
func TestOpen_RefusesABrokenDescriptorOrConfig(t *testing.T) {
	t.Run("no constructor", func(t *testing.T) {
		desc := syncplugin.Descriptor{Name: "stub_no_ctor"}
		if _, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_no_ctor"}, syncplugin.Deps{}); err == nil {
			t.Fatal("Open on a descriptor with no constructor: got nil error, want a refusal")
		}
	})

	t.Run("constructor returns nil", func(t *testing.T) {
		desc := syncplugin.Descriptor{
			Name: "stub_nil_ctor",
			New:  func(syncplugin.Deps) syncplugin.Plugin { return nil },
		}
		if _, err := syncplugin.Open(desc, syncplugin.Config{Name: "stub_nil_ctor"}, syncplugin.Deps{}); err == nil {
			t.Fatal("Open on a constructor returning nil: got nil error, want a refusal")
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		desc, _ := openDescriptor("stub_bad_cfg", nil, false)
		// Config.Validate requires a non-empty Name.
		if _, err := syncplugin.Open(desc, syncplugin.Config{}, syncplugin.Deps{}); err == nil {
			t.Fatal("Open on an invalid Config: got nil error, want a refusal")
		}
	})
}

// TestMissingSettings_ReportsOnlyRequiredOnesSorted pins the ordering
// the refusal message depends on, so the text an operator reads does not
// depend on map iteration order.
func TestMissingSettings_ReportsOnlyRequiredOnesSorted(t *testing.T) {
	desc := syncplugin.Descriptor{
		Name:        "stub_ordering",
		Description: "a descriptor built for a test",
		Settings: []syncplugin.SettingSpec{
			{Name: "zone", Description: "z", Required: true},
			{Name: "account", Description: "a", Required: true},
			{Name: "profile", Description: "p", Required: false},
		},
		New: func(syncplugin.Deps) syncplugin.Plugin { return &openStub{} },
	}

	got := desc.MissingSettings(syncplugin.Config{Name: "stub_ordering"})
	want := []string{"account", "zone"}
	if len(got) != len(want) {
		t.Fatalf("MissingSettings() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MissingSettings() = %v, want %v", got, want)
		}
	}
}

// TestConfig_Setting reads a value through the accessor a plugin uses,
// including the nil-map case a Config built with no settings has.
func TestConfig_Setting(t *testing.T) {
	var empty syncplugin.Config
	if value, ok := empty.Setting("region"); ok || value != "" {
		t.Errorf("Setting on a Config with no settings = (%q, %v), want (\"\", false)", value, ok)
	}

	cfg := syncplugin.Config{Settings: map[string]string{"region": "us-east-1"}}
	if value, ok := cfg.Setting("region"); !ok || value != "us-east-1" {
		t.Errorf("Setting(\"region\") = (%q, %v), want (\"us-east-1\", true)", value, ok)
	}
	if _, ok := cfg.Setting("absent"); ok {
		t.Error("Setting reported a key the Config does not hold")
	}
}

// TestRegister_RefusesAMalformedSetting proves a setting nobody can name
// or look up is refused at registration, where the mistake is, rather
// than at the first sync that needs the value.
func TestRegister_RefusesAMalformedSetting(t *testing.T) {
	tests := []struct {
		name     string
		settings []syncplugin.SettingSpec
	}{
		{name: "no name", settings: []syncplugin.SettingSpec{{Description: "d"}}},
		{name: "blank name", settings: []syncplugin.SettingSpec{{Name: "  ", Description: "d"}}},
		{name: "no description", settings: []syncplugin.SettingSpec{{Name: "region"}}},
		{name: "duplicate", settings: []syncplugin.SettingSpec{
			{Name: "region", Description: "d"},
			{Name: "region", Description: "d again"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := syncplugin.Register(syncplugin.Descriptor{
				Name:        "stub_bad_setting_" + strings.ReplaceAll(tt.name, " ", "_"),
				Description: "a descriptor built for a test",
				Settings:    tt.settings,
				New:         func(syncplugin.Deps) syncplugin.Plugin { return &openStub{} },
			})
			if err == nil {
				t.Fatal("Register accepted a malformed setting, want a refusal")
			}
		})
	}
}

// TestDescriptor_Implemented covers the empty-means-declared default
// callers rely on rather than comparing Status themselves.
func TestDescriptor_Implemented(t *testing.T) {
	if (syncplugin.Descriptor{}).Implemented() {
		t.Error("a descriptor with no Status must read as declared, never implemented")
	}
	if (syncplugin.Descriptor{Status: syncplugin.StatusDeclared}).Implemented() {
		t.Error("a declared descriptor must not report itself implemented")
	}
	if !(syncplugin.Descriptor{Status: syncplugin.StatusImplemented}).Implemented() {
		t.Error("an implemented descriptor must report itself implemented")
	}
}

// TestAll_ReturnsASnapshot proves All hands back the registered
// descriptors, which is what the CLI's plugin listing and every archtest
// sweep read.
func TestAll_ReturnsASnapshot(t *testing.T) {
	const name = "stub_all_snapshot"
	desc, _ := openDescriptor(name, nil, false)
	if err := syncplugin.Register(desc); err != nil {
		t.Fatalf("Register: %v", err)
	}

	all := syncplugin.All()
	got, ok := all[name]
	if !ok {
		t.Fatalf("All() does not hold %q", name)
	}
	if got.Name != name {
		t.Errorf("All()[%q].Name = %q", name, got.Name)
	}
}

// TestMustRegister_PanicsOnADuplicate proves the panic a plugin
// package's own init() relies on: a duplicate built-in name must take
// the process down at start rather than silently shadow one
// registration with another.
func TestMustRegister_PanicsOnADuplicate(t *testing.T) {
	const name = "stub_must_register"
	desc, _ := openDescriptor(name, nil, false)
	syncplugin.MustRegister(desc)

	defer func() {
		if recover() == nil {
			t.Error("MustRegister on a duplicate name did not panic")
		}
	}()
	syncplugin.MustRegister(desc)
}
