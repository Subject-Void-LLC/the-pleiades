package catalyst_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/SubjectVoidLLC/the-pleiades/internal/catalog/net/catalyst"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
	pkginv "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/sdk"

	// The device types these tests hydrate have to be registered, and only
	// each vendor package's own init does that. linux is here to supply a
	// device that genuinely lacks CatalystAPICapable, which is the negative
	// case the capability guard exists for.
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/catalyst"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/linux"
)

// These tests replay JSON captured from the real Cisco DevNet sandbox
// (testdata/*.json, taken from sandboxdnac.cisco.com) through an
// httptest.Server. That keeps them hermetic and offline while still
// exercising real response shapes, including the fields that come back as
// JSON strings where a number would be expected.
//
// They are not the proof these methods work: tests/e2e/catalyst_test.go is,
// because it runs against the actual controller. These prove the parsing,
// fact shaping, and error handling, which a live test cannot assert
// precisely because live data changes.

// fakeContext is a minimal sdk.RunbookContext that records what a method
// emits, so a test can assert on the facts rather than on a log.
type fakeContext struct {
	secrets map[string]string
	facts   map[string]interface{}
}

func newFakeContext(secrets map[string]string) *fakeContext {
	return &fakeContext{secrets: secrets, facts: map[string]interface{}{}}
}

func (c *fakeContext) InjectSecrets() map[string]string { return c.secrets }

func (c *fakeContext) SetStat(key string, value interface{}) error {
	c.facts[key] = value
	return nil
}

func (c *fakeContext) EmitFact(key string, value interface{}) error {
	c.facts[key] = value
	return nil
}

var _ sdk.RunbookContext = (*fakeContext)(nil)

// fixture reads a captured sandbox response.
func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// newSandbox stands up an httptest.Server replaying the captured responses,
// and returns it alongside a hydrated catalyst_center device pointed at it.
//
// The handler asserts the auth header on every non-auth request rather than
// ignoring it, so a method that forgot to authenticate fails here instead of
// silently passing against a fake that never checked.
func newSandbox(t *testing.T) (*httptest.Server, pkginv.InventoryItem) {
	t.Helper()

	devices := fixture(t, "devices.json")
	sites := fixture(t, "sites.json")
	tags := fixture(t, "tags.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/dna/system/api/v1/auth/token" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "devnetuser" || pass != "Cisco123!" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Token":"fixture-token"}`))
			return
		}

		if r.Header.Get("X-Auth-Token") != "fixture-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			_, _ = w.Write([]byte(`{"response":4}`))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			// Honor paging so the multi-page path is exercised: a handler
			// that always returned every device would make EachDevice's
			// short-page termination untestable.
			_, _ = w.Write(pageDevices(t, devices, r))
		case r.URL.Path == "/dna/intent/api/v1/site":
			_, _ = w.Write(sites)
		case r.URL.Path == "/dna/intent/api/v1/tag":
			_, _ = w.Write(tags)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, newController(t, srv.URL)
}

// newController hydrates a catalyst_center device pointed at baseURL,
// through the real factory rather than a hand-built fake, so the capability
// check inside the methods runs against a genuine device type.
func newController(t *testing.T, baseURL string) pkginv.InventoryItem {
	t.Helper()

	ctor, ok := record.LookupType("catalyst_center")
	if !ok {
		t.Fatal("catalyst_center device type is not registered")
	}
	item, err := ctor(record.Record{
		ID:   "controller-1",
		Name: "test-controller",
		Type: "catalyst_center",
		Properties: map[string]pkginv.PropertyValue{
			"catalyst_base_url": baseURL,
		},
		Capabilities: []capability.Name{capability.NameCatalystAPI},
		State:        pkginv.StateActive,
	})
	if err != nil {
		t.Fatalf("hydrating controller: %v", err)
	}
	return item
}

// validSecrets is what every happy-path test authenticates with.
func validSecrets() map[string]string {
	return map[string]string{"username": "devnetuser", "password": "Cisco123!"}
}

// invoke looks a method up in the real registry and calls it, so these
// tests exercise the same path the engine's dispatcher does rather than
// calling the exported function directly.
func invoke(t *testing.T, name string, rc sdk.RunbookContext, device pkginv.InventoryItem, params map[string]any) (collection.Result, error) {
	t.Helper()

	desc, ok := collection.Lookup(name)
	if !ok {
		t.Fatalf("collection method %q is not registered", name)
	}
	if desc.Invoke == nil {
		t.Fatalf("collection method %q carries no implementation", name)
	}
	return desc.Invoke(context.Background(), rc, device, params)
}

// TestMethodsAreRegisteredAsImplemented proves all four methods reached the
// registry claiming a real implementation, which is what makes them
// reachable from the engine's dispatcher at all.
func TestMethodsAreRegisteredAsImplemented(t *testing.T) {
	for _, name := range []string{
		"net.catalyst.device_facts",
		"net.catalyst.site_facts",
		"net.catalyst.tag_facts",
		"net.catalyst.reachability",
	} {
		t.Run(name, func(t *testing.T) {
			desc, ok := collection.Lookup(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			if desc.Manifest.Status != collection.StatusImplemented {
				t.Errorf("status = %q, want %q", desc.Manifest.Status, collection.StatusImplemented)
			}
			if desc.Invoke == nil {
				t.Error("carries no implementation")
			}
			if len(desc.Manifest.RequiredCapabilities) == 0 {
				t.Error("declares no required capability")
			}
		})
	}
}

// TestDeviceFacts proves the fleet listing is parsed and shaped correctly,
// including that paging is followed to the end.
func TestDeviceFacts(t *testing.T) {
	_, controller := newSandbox(t)
	rc := newFakeContext(validSecrets())

	// A page size below the fixture's device count forces EachDevice to
	// make more than one request and stop on the short final page.
	result, err := invoke(t, "net.catalyst.device_facts", rc, controller, map[string]any{"page_size": 2})
	if err != nil {
		t.Fatalf("DeviceFacts: %v", err)
	}
	if result.Changed {
		t.Error("a fact gatherer must never report Changed")
	}

	count, ok := rc.facts["device_count"].(int)
	if !ok || count != 4 {
		t.Fatalf("device_count = %v, want 4", rc.facts["device_count"])
	}

	devices, ok := rc.facts["devices"].([]map[string]any)
	if !ok {
		t.Fatalf("devices fact has type %T, want []map[string]any", rc.facts["devices"])
	}
	if len(devices) != 4 {
		t.Fatalf("got %d devices, want 4", len(devices))
	}

	first := devices[0]
	if first["hostname"] != "sw1" {
		t.Errorf("first device hostname = %v, want sw1", first["hostname"])
	}
	if first["software_type"] != "IOS-XE" {
		t.Errorf("first device software_type = %v, want IOS-XE", first["software_type"])
	}
	if first["management_ip"] != "10.10.20.175" {
		t.Errorf("first device management_ip = %v, want 10.10.20.175", first["management_ip"])
	}
}

// TestSiteFacts proves the site hierarchy is parsed and that the full path,
// not the bare name, is what gets emitted.
func TestSiteFacts(t *testing.T) {
	_, controller := newSandbox(t)
	rc := newFakeContext(validSecrets())

	if _, err := invoke(t, "net.catalyst.site_facts", rc, controller, nil); err != nil {
		t.Fatalf("SiteFacts: %v", err)
	}

	if count, ok := rc.facts["site_count"].(int); !ok || count != 25 {
		t.Fatalf("site_count = %v, want 25", rc.facts["site_count"])
	}

	sites, ok := rc.facts["sites"].([]map[string]any)
	if !ok {
		t.Fatalf("sites fact has type %T, want []map[string]any", rc.facts["sites"])
	}

	var foundNested bool
	for _, s := range sites {
		hierarchy, _ := s["site_name_hierarchy"].(string)
		if strings.Contains(hierarchy, "/") {
			foundNested = true
			break
		}
	}
	if !foundNested {
		t.Error("expected at least one site to carry a full slash-separated hierarchy")
	}
}

// TestTagFacts proves system tags and operator tags are reported
// separately, which is the distinction a runbook grouping by operator
// intent depends on.
func TestTagFacts(t *testing.T) {
	_, controller := newSandbox(t)
	rc := newFakeContext(validSecrets())

	if _, err := invoke(t, "net.catalyst.tag_facts", rc, controller, nil); err != nil {
		t.Fatalf("TagFacts: %v", err)
	}

	all, ok := rc.facts["tags"].([]map[string]any)
	if !ok {
		t.Fatalf("tags fact has type %T, want []map[string]any", rc.facts["tags"])
	}
	operator, ok := rc.facts["operator_tags"].([]string)
	if !ok {
		t.Fatalf("operator_tags fact has type %T, want []string", rc.facts["operator_tags"])
	}

	if len(all) == 0 {
		t.Fatal("no tags were emitted")
	}
	if len(operator) >= len(all) {
		t.Errorf("operator_tags (%d) should be a strict subset of tags (%d); the fixture contains system tags",
			len(operator), len(all))
	}
}

// TestReachability proves both upstream health signals are reported and
// that the unreachable list is derived from the right one.
func TestReachability(t *testing.T) {
	_, controller := newSandbox(t)
	rc := newFakeContext(validSecrets())

	if _, err := invoke(t, "net.catalyst.reachability", rc, controller, nil); err != nil {
		t.Fatalf("Reachability: %v", err)
	}

	if total, ok := rc.facts["total_device_count"].(int); !ok || total != 4 {
		t.Fatalf("total_device_count = %v, want 4", rc.facts["total_device_count"])
	}
	// Every device in the captured fixture is Reachable and Managed.
	if got, ok := rc.facts["reachable_count"].(int); !ok || got != 4 {
		t.Errorf("reachable_count = %v, want 4", rc.facts["reachable_count"])
	}
	if got, ok := rc.facts["managed_count"].(int); !ok || got != 4 {
		t.Errorf("managed_count = %v, want 4", rc.facts["managed_count"])
	}
	unreachable, ok := rc.facts["unreachable"].([]string)
	if !ok {
		t.Fatalf("unreachable fact has type %T, want []string", rc.facts["unreachable"])
	}
	if len(unreachable) != 0 {
		t.Errorf("unreachable = %v, want empty for the captured fixture", unreachable)
	}
}

// TestMethodsRejectBadInput covers the refusals every method shares. Each
// one exists because the alternative is a confusing failure much further
// from its cause: a nil device would panic, a device without the capability
// would fail at the HTTP layer, and a missing secret would look like an
// authentication problem on the controller's side.
func TestMethodsRejectBadInput(t *testing.T) {
	methods := []string{
		"net.catalyst.device_facts",
		"net.catalyst.site_facts",
		"net.catalyst.tag_facts",
		"net.catalyst.reachability",
	}

	tests := []struct {
		name    string
		device  func(t *testing.T) pkginv.InventoryItem
		secrets map[string]string
		wantErr string
	}{
		{
			name:    "no target device",
			device:  func(*testing.T) pkginv.InventoryItem { return nil },
			secrets: validSecrets(),
			wantErr: "no target device",
		},
		{
			name: "device without the capability",
			device: func(t *testing.T) pkginv.InventoryItem {
				ctor, ok := record.LookupType("linux_server")
				if !ok {
					t.Fatal("linux_server is not registered")
				}
				item, err := ctor(record.Record{ID: "l-1", Name: "web1", Type: "linux_server", State: pkginv.StateActive})
				if err != nil {
					t.Fatalf("hydrating linux_server: %v", err)
				}
				return item
			},
			secrets: validSecrets(),
			wantErr: "does not have CatalystAPICapable",
		},
		{
			name:    "missing username secret",
			device:  func(t *testing.T) pkginv.InventoryItem { return newController(t, "https://example.invalid") },
			secrets: map[string]string{},
			wantErr: `no "username" secret`,
		},
	}

	for _, method := range methods {
		for _, tt := range tests {
			t.Run(method+"/"+tt.name, func(t *testing.T) {
				rc := newFakeContext(tt.secrets)
				_, err := invoke(t, method, rc, tt.device(t), nil)
				if err == nil {
					t.Fatalf("expected an error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
			})
		}
	}
}

// TestMethodsRejectWrongCredentials proves a bad password surfaces as an
// error rather than as an empty fact set. An empty fleet and a rejected
// login look identical to a runbook otherwise.
func TestMethodsRejectWrongCredentials(t *testing.T) {
	_, controller := newSandbox(t)
	rc := newFakeContext(map[string]string{"username": "devnetuser", "password": "wrong"})

	if _, err := invoke(t, "net.catalyst.device_facts", rc, controller, nil); err == nil {
		t.Fatal("expected a wrong password to fail")
	}
	if _, emitted := rc.facts["devices"]; emitted {
		t.Error("no facts should be emitted when authentication fails")
	}
}
