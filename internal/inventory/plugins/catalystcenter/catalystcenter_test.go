package catalystcenter_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/catalystcenter"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginv "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"

	// The device types this plugin classifies into must be registered for
	// the factory to hydrate them.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/catalyst"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
)

// These are this plugin's own tests. The cross-plugin assertions that keep
// the syncplugin port honest live in internal/inventory/plugins'
// conformance suite, and the proof this actually talks to a Cisco Catalyst
// Center lives in tests/e2e. What is left here is the behavior specific to
// this plugin: how it maps raw upstream fields onto a classification path,
// and what it does when the controller answers in ways a live sandbox
// cannot be made to answer on demand.

// upstreamDevice is one device the fake controller reports.
type upstreamDevice struct {
	Hostname     string
	IP           string
	SoftwareType string
	Family       string
}

// staticStore is a credential.Store returning one fixed credential.
type staticStore struct{ cred credential.Credential }

func (s staticStore) Lookup(context.Context, string) (credential.Credential, error) {
	return s.cred, nil
}

// newFakeController stands up a controller reporting devices, paging as the
// real one does.
func newFakeController(t *testing.T, devices []upstreamDevice) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "devnetuser" || pass != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
			return
		}
		if r.Header.Get("X-Auth-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			fmt.Fprintf(w, `{"response":%d}`, len(devices))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			_, _ = w.Write(devicePage(devices, r))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// devicePage renders the requested slice of devices in the wire shape.
func devicePage(devices []upstreamDevice, r *http.Request) []byte {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 1 {
		offset = 1
	}
	if limit < 1 {
		limit = len(devices)
	}

	start := offset - 1
	if start > len(devices) {
		start = len(devices)
	}
	end := start + limit
	if end > len(devices) {
		end = len(devices)
	}

	var b strings.Builder
	b.WriteString(`{"response":[`)
	for i, d := range devices[start:end] {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"id-%s","hostname":"%s","managementIpAddress":"%s",`+
			`"family":"%s","softwareType":"%s","softwareVersion":"17.12.1",`+
			`"reachabilityStatus":"Reachable","collectionStatus":"Managed"}`,
			d.Hostname, d.Hostname, d.IP, d.Family, d.SoftwareType)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// connect builds a plugin pointed at srv and connects it.
func connect(t *testing.T, srv *httptest.Server, readOnly bool) *catalystcenter.CatalystCenter {
	t.Helper()

	p := catalystcenter.New(catalystcenter.WithCredentialStore(
		staticStore{credential.Credential{Username: "devnetuser", Password: "secret"}}))
	t.Cleanup(func() { _ = p.Close() })

	cfg := syncplugin.Config{
		Name:     catalystcenter.Name,
		Endpoint: srv.URL,
		ReadOnly: readOnly,
		PageSize: 2,
	}
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return p
}

// newRepo builds an empty project inventory.
func newRepo(t *testing.T) inv.Repository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating inventory: %v", err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

// TestClassify_MapsUpstreamFieldsToTypes is the heart of this plugin's own
// behavior: which classification path a raw upstream record derives.
//
// The negative cases matter most. Catalyst Center manages Cisco gear this
// codebase has no rules for (IOS-XR, NX-OS) and third-party devices
// onboarded through it, and guessing at those would hydrate a device as
// something it is not.
func TestClassify_MapsUpstreamFieldsToTypes(t *testing.T) {
	p := connect(t, newFakeController(t, nil), true)

	tests := []struct {
		name            string
		props           map[string]pkginv.PropertyValue
		wantType        string
		wantQuarantined bool
	}{
		{
			name:     "the controller itself",
			props:    map[string]pkginv.PropertyValue{"catalyst_role": "controller"},
			wantType: "catalyst_center",
		},
		{
			name: "an IOS-XE switch",
			props: map[string]pkginv.PropertyValue{
				"catalyst_role":          "managed_device",
				"catalyst_software_type": "IOS-XE",
				"catalyst_family":        "Switches and Hubs",
			},
			wantType: "cisco_switch",
		},
		{
			name: "an IOS-XE device that is not a switch",
			props: map[string]pkginv.PropertyValue{
				"catalyst_role":          "managed_device",
				"catalyst_software_type": "IOS-XE",
				"catalyst_family":        "Routers",
			},
			wantType: "cisco_router",
		},
		{
			name: "software type is matched case-insensitively",
			props: map[string]pkginv.PropertyValue{
				"catalyst_role":          "managed_device",
				"catalyst_software_type": "ios-xe",
				"catalyst_family":        "Switches and Hubs",
			},
			wantType: "cisco_switch",
		},
		{
			name: "a software type with no rule quarantines",
			props: map[string]pkginv.PropertyValue{
				"catalyst_role":          "managed_device",
				"catalyst_software_type": "NX-OS",
			},
			wantQuarantined: true,
		},
		{
			name:            "a device reporting no software type quarantines",
			props:           map[string]pkginv.PropertyValue{"catalyst_role": "managed_device"},
			wantQuarantined: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Classify(context.Background(), record.Record{Name: "d1", Properties: tt.props})
			if err != nil {
				t.Fatalf("Classify must not error, got %v", err)
			}

			if got.Quarantined() != tt.wantQuarantined {
				t.Fatalf("Quarantined() = %v, want %v (classification: %+v)", got.Quarantined(), tt.wantQuarantined, got)
			}
			if tt.wantQuarantined {
				if got.Reason == "" {
					t.Error("a quarantined classification must explain why")
				}
				return
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
		})
	}
}

// TestClassify_ReadOnlySourceSimulateLocks proves the read-only flag's
// first concrete effect. A device Pleiades has only read about, and never
// authenticated to directly, must not accept real work until an admin
// promotes it.
func TestClassify_ReadOnlySourceSimulateLocks(t *testing.T) {
	tests := []struct {
		name      string
		readOnly  bool
		wantState pkginv.LifecycleState
	}{
		{name: "read-only source", readOnly: true, wantState: pkginv.StateSimulateLocked},
		{name: "writable source", readOnly: false, wantState: pkginv.StateActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := connect(t, newFakeController(t, nil), tt.readOnly)

			got, err := p.Classify(context.Background(), record.Record{
				Name: "sw1",
				Properties: map[string]pkginv.PropertyValue{
					"catalyst_role":          "managed_device",
					"catalyst_software_type": "IOS-XE",
					"catalyst_family":        "Switches and Hubs",
				},
			})
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got.State != tt.wantState {
				t.Errorf("State = %v, want %v", got.State, tt.wantState)
			}
			if tt.readOnly && got.State.CanExecute() {
				t.Error("a device imported read-only must not be executable")
			}
		})
	}
}

// TestDiscover_EmitsControllerEvenWithNoDevices proves the controller is
// onboarded regardless of fleet size. A Catalyst Center managing nothing is
// still a device worth targeting, and it is the target every net.catalyst.*
// method addresses.
func TestDiscover_EmitsControllerEvenWithNoDevices(t *testing.T) {
	p := connect(t, newFakeController(t, nil), true)
	ctx := context.Background()

	it, err := p.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	defer func() { _ = it.Close() }()

	var records []record.Record
	for it.Next(ctx) {
		records = append(records, it.Record())
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterating: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("discovered %d records, want just the controller", len(records))
	}
	if role, _ := records[0].Properties["catalyst_role"].(string); role != "controller" {
		t.Errorf("the only record has role %q, want controller", role)
	}
}

// TestDiscover_PagesThroughEveryDevice proves the iterator follows paging
// to the end without repeating or dropping a device across boundaries.
func TestDiscover_PagesThroughEveryDevice(t *testing.T) {
	devices := make([]upstreamDevice, 0, 7)
	for i := 1; i <= 7; i++ {
		devices = append(devices, upstreamDevice{
			Hostname:     fmt.Sprintf("sw%d", i),
			IP:           fmt.Sprintf("10.0.0.%d", i),
			SoftwareType: "IOS-XE",
			Family:       "Switches and Hubs",
		})
	}

	// Page size 2 against 7 devices: several full pages then a short one.
	p := connect(t, newFakeController(t, devices), true)
	ctx := context.Background()

	it, err := p.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	defer func() { _ = it.Close() }()

	seen := map[string]bool{}
	for it.Next(ctx) {
		name := it.Record().Name
		if seen[name] {
			t.Errorf("record %q was yielded twice across a page boundary", name)
		}
		seen[name] = true
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterating: %v", err)
	}

	// Seven devices plus the controller.
	if len(seen) != 8 {
		t.Fatalf("discovered %d distinct records, want 8", len(seen))
	}
	for i := 1; i <= 7; i++ {
		if name := fmt.Sprintf("sw%d", i); !seen[name] {
			t.Errorf("device %s was never yielded", name)
		}
	}
}

// TestSync_OnboardsControllerAndDevices proves the whole pipeline against
// the fake controller, including that the properties the rest of the
// codebase reads are populated.
func TestSync_OnboardsControllerAndDevices(t *testing.T) {
	devices := []upstreamDevice{
		{Hostname: "sw1", IP: "10.0.0.1", SoftwareType: "IOS-XE", Family: "Switches and Hubs"},
		{Hostname: "sw2", IP: "10.0.0.2", SoftwareType: "IOS-XE", Family: "Switches and Hubs"},
	}
	p := connect(t, newFakeController(t, devices), true)
	repo := newRepo(t)
	ctx := context.Background()

	report, err := p.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeAdded); got != 3 {
		t.Fatalf("added %d, want 3 (two switches and the controller): %+v", got, report.Results)
	}

	sw1, err := repo.GetByName(ctx, "sw1")
	if err != nil {
		t.Fatalf("GetByName(sw1): %v", err)
	}
	if !sw1.HasCapability(capability.NameCiscoIOS) {
		t.Errorf("sw1 does not declare %s", capability.NameCiscoIOS)
	}
	// The SSH transport resolves a target through host; the API dispatcher
	// reads ip. A device missing either is present but unreachable.
	for _, key := range []string{"host", "ip"} {
		if v, _ := sw1.Properties().String(key); v != "10.0.0.1" {
			t.Errorf("sw1 property %q = %q, want 10.0.0.1", key, v)
		}
	}
	if v, _ := sw1.Properties().String("catalyst_software_version"); v != "17.12.1" {
		t.Errorf("sw1 software version = %q, want 17.12.1", v)
	}
}

// TestSync_DeviceNameFallsBackToAddress proves a device reporting no
// hostname still gets an addressable name rather than an empty one that
// GetByName could never match.
func TestSync_DeviceNameFallsBackToAddress(t *testing.T) {
	devices := []upstreamDevice{
		{Hostname: "", IP: "10.0.0.55", SoftwareType: "IOS-XE", Family: "Switches and Hubs"},
	}
	p := connect(t, newFakeController(t, devices), true)
	repo := newRepo(t)
	ctx := context.Background()

	if _, err := p.Sync(ctx, repo); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := repo.GetByName(ctx, "10.0.0.55"); err != nil {
		t.Fatalf("a device with no hostname was not addressable by its management address: %v", err)
	}
}

// TestConnect_Rejects covers the refusals that happen before any upstream
// call, plus the one that happens because of it.
func TestConnect_Rejects(t *testing.T) {
	srv := newFakeController(t, nil)

	tests := []struct {
		name    string
		cfg     syncplugin.Config
		store   credential.Store
		wantErr string
	}{
		{
			name:    "no endpoint",
			cfg:     syncplugin.Config{Name: catalystcenter.Name},
			store:   staticStore{credential.Credential{Username: "devnetuser", Password: "secret"}},
			wantErr: "endpoint is required",
		},
		{
			name:    "no credential store",
			cfg:     syncplugin.Config{Name: catalystcenter.Name, Endpoint: srv.URL},
			store:   nil,
			wantErr: "credential store",
		},
		{
			name:    "wrong password",
			cfg:     syncplugin.Config{Name: catalystcenter.Name, Endpoint: srv.URL},
			store:   staticStore{credential.Credential{Username: "devnetuser", Password: "wrong"}},
			wantErr: "connecting to",
		},
		{
			name:    "invalid config",
			cfg:     syncplugin.Config{Endpoint: srv.URL},
			store:   staticStore{credential.Credential{Username: "devnetuser", Password: "secret"}},
			wantErr: "name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []catalystcenter.Option
			if tt.store != nil {
				opts = append(opts, catalystcenter.WithCredentialStore(tt.store))
			}
			p := catalystcenter.New(opts...)
			defer func() { _ = p.Close() }()

			err := p.Connect(context.Background(), tt.cfg)
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestNotConnected proves the port's call order is enforced with a typed
// error rather than a nil-pointer panic.
func TestNotConnected(t *testing.T) {
	p := catalystcenter.New()

	if _, err := p.Discover(context.Background()); !errors.Is(err, syncplugin.ErrNotConnected) {
		t.Errorf("Discover before Connect = %v, want ErrNotConnected", err)
	}
	if _, err := p.Sync(context.Background(), nil); !errors.Is(err, syncplugin.ErrNotConnected) {
		t.Errorf("Sync before Connect = %v, want ErrNotConnected", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close on an unconnected plugin = %v, want nil", err)
	}
}

// TestRegistered proves the plugin reaches the shared registry claiming a
// real implementation, which is what makes it selectable from the CLI.
func TestRegistered(t *testing.T) {
	desc, ok := syncplugin.Lookup(catalystcenter.Name)
	if !ok {
		t.Fatalf("plugin %q is not registered", catalystcenter.Name)
	}
	if !desc.Implemented() {
		t.Errorf("status = %q, want %q", desc.Status, syncplugin.StatusImplemented)
	}
	if !desc.DefaultConfig.ReadOnly {
		t.Error("a controller is an authoritative upstream; the shipped default must be read-only")
	}
	if desc.New() == nil {
		t.Error("registered constructor returned nil")
	}
}
