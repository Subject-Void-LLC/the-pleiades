// Tests for onboarding's remaining edges: every generic type has a prober,
// credentials resolve as a run resolves them, a read-only inventory is
// refused, and each probe's refusals.
package onboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	reflectionalpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestProbers_OneForEveryGenericType: onboarding reaches every generic
// type, each through a prober naming its protocol.
func TestProbers_OneForEveryGenericType(t *testing.T) {
	want := map[string]string{generic.TypeSSH: "ssh", generic.TypeNetconf: "netconf", generic.TypeHTTP: "http", generic.TypeGRPC: "grpc"}
	for _, typ := range generic.Types() {
		p, ok := Lookup(typ)
		if !ok || p.Protocol() != want[typ] {
			t.Errorf("%s: prober %v, protocol %q", typ, ok, want[typ])
		}
	}
}

// failingStore fails every lookup as a broken store does.
type failingStore struct{}

func (failingStore) Lookup(context.Context, string) (credential.Credential, error) {
	return credential.Credential{}, errors.New("the master key is wrong")
}

// TestSecretsFrom resolves a stored credential flattened, a missing one
// as nothing, and a broken store as an error rather than as nothing.
func TestSecretsFrom(t *testing.T) {
	dev := build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a.invalid:1"})
	got, err := SecretsFrom(credential.NewStaticStore(map[string]string{"username": "u", "password": "p"}))(context.Background(), dev)
	if err != nil || got["password"] != "p" {
		t.Errorf("stored: %v, %v", got, err)
	}
	if got, err := SecretsFrom(credential.NewStaticStore(nil))(context.Background(), dev); err != nil || got != nil {
		t.Errorf("missing: %v, %v", got, err)
	}
	if _, err := SecretsFrom(failingStore{})(context.Background(), dev); err == nil {
		t.Error("a broken store read as no credential")
	}
	repo, _ := newRepo(t, "grpc1", generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a.invalid:1"})
	calls := 0
	res, err := onboardWith(context.Background(), repo, "grpc1", SecretsFrom(failingStore{}), fixedNow, only(countingProber{calls: &calls}))
	if err == nil || calls != 0 || res.State != "onboarding" {
		t.Errorf("a broken store: err %v after %d probes, state %s", err, calls, res.State)
	}
}

// TestOnboard_ReadOnlyInventory: an inventory opened read-only cannot be
// onboarded into, and says so before anything is probed.
func TestOnboard_ReadOnlyInventory(t *testing.T) {
	repo, _ := newRepo(t, "grpc1", generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a.invalid:1"})
	calls := 0
	_, err := onboardWith(context.Background(), inv.NewReadOnlyRepository(repo), "grpc1", staticSecrets(nil), fixedNow, only(countingProber{calls: &calls}))
	if !errors.Is(err, inv.ErrInventoryReadOnly) || calls != 0 {
		t.Fatalf("err %v after %d probes", err, calls)
	}
	if _, err := Onboard(context.Background(), repo, "missing", staticSecrets(nil), fixedNow); !errors.Is(err, inv.ErrItemNotFound) {
		t.Errorf("a missing device: %v", err)
	}
}

// TestHTTPProbe_Refusals: a server error, an answer past the bound, an
// OpenAPI document that is not one, and a device with no HTTP API each
// prove nothing.
func TestHTTPProbe_Refusals(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken":
			w.WriteHeader(http.StatusBadGateway)
		case "/huge":
			_, _ = w.Write([]byte(strings.Repeat("x", 70<<10)))
		case "/ok/openapi.json":
			_, _ = w.Write([]byte("<html>"))
		}
	}))
	defer srv.Close()
	p := httpProber{client: srv.Client()}
	for name, props := range map[string]map[string]inventory.PropertyValue{
		"server error":   {generic.BaseURLProperty: srv.URL + "/broken"},
		"too large":      {generic.BaseURLProperty: srv.URL + "/huge"},
		"not an OpenAPI": {generic.BaseURLProperty: srv.URL + "/ok", generic.OpenAPIPathProperty: "/openapi.json"},
	} {
		if _, err := p.Probe(context.Background(), build(t, generic.TypeHTTP, props), nil); err == nil {
			t.Errorf("%s proved the API", name)
		}
	}
	grpcDev := build(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a.invalid:1"})
	if _, err := p.Probe(context.Background(), grpcDev, nil); err == nil {
		t.Error("a device with no HTTP API was probed")
	}
	if _, err := (grpcProber{}).Probe(context.Background(), build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL}), nil); err == nil {
		t.Error("a device with no gRPC target was probed")
	}
}

// TestGRPCProbe_OlderReflectionOnly: a server that predates reflection v1
// is still read, through v1alpha.
func TestGRPCProbe_OlderReflectionOnly(t *testing.T) {
	srv := grpc.NewServer()
	reflectionalpha.RegisterServerReflectionServer(srv, reflection.NewServer(reflection.ServerOptions{Services: srv}))
	got, err := grpcProber{}.Probe(context.Background(), grpcDevice(t, grpcServer(t, srv), true), nil)
	if err != nil {
		t.Fatal(err)
	}
	services, _ := got.Facts["services"].([]any)
	if got.Facts["reflection"] != "v1alpha" || !slices.Contains(services, any("grpc.reflection.v1alpha.ServerReflection")) {
		t.Errorf("facts %v", got.Facts)
	}
	if !slices.Equal(got.Capabilities, []capability.Name{capability.NameGRPC}) {
		t.Errorf("capabilities %v", got.Capabilities)
	}
}

// TestSSHAndNetconfProbes_RefuseBeforeDialing: a device that names no
// address for the protocol, or has no credential, is refused before any
// connection is attempted.
func TestSSHAndNetconfProbes_RefuseBeforeDialing(t *testing.T) {
	httpDev := build(t, generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://a.invalid"})
	sshDev := build(t, generic.TypeSSH, map[string]inventory.PropertyValue{"host": "a.invalid"})
	ncDev := build(t, generic.TypeNetconf, map[string]inventory.PropertyValue{"host": "a.invalid"})
	for name, probe := range map[string]func() error{
		"ssh, not an SSH device":        func() error { _, err := (sshProber{}).Probe(context.Background(), httpDev, nil); return err },
		"ssh, no credential":            func() error { _, err := (sshProber{}).Probe(context.Background(), sshDev, nil); return err },
		"netconf, not a NETCONF device": func() error { _, err := (netconfProber{}).Probe(context.Background(), sshDev, nil); return err },
		"netconf, no credential":        func() error { _, err := (netconfProber{}).Probe(context.Background(), ncDev, nil); return err },
	} {
		if err := probe(); err == nil {
			t.Errorf("%s was not refused", name)
		}
	}
}

// TestOnboard_AReprobeCanTakeACapabilityAway: what a device no longer
// proves is removed, and the result names it.
func TestOnboard_AReprobeCanTakeACapabilityAway(t *testing.T) {
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://api.invalid"})
	calls := 0
	granted := countingProber{calls: &calls, grants: []capability.Name{capability.NameHTTPAPI}}
	if _, err := onboardWith(context.Background(), repo, "api1", staticSecrets(nil), fixedNow, only(granted)); err != nil {
		t.Fatal(err)
	}
	res, err := onboardWith(context.Background(), repo, "api1", staticSecrets(nil), fixedNow, only(countingProber{calls: &calls}))
	if err != nil || !slices.Equal(res.Removed, []string{"HTTPAPICapable"}) {
		t.Fatalf("err %v, removed %v", err, res.Removed)
	}
	item, _ := repo.GetByName(context.Background(), "api1")
	if item.HasCapability(capability.NameHTTPAPI) {
		t.Error("the capability the re-probe no longer proved is still held")
	}
}

// TestRegister_DuplicatePanics: two probers for one type is a build error.
func TestRegister_DuplicatePanics(t *testing.T) {
	t.Cleanup(SnapshotForTest())
	defer func() {
		if recover() == nil {
			t.Error("a second prober for generic_ssh was accepted")
		}
	}()
	Register(generic.TypeSSH, sshProber{})
}
