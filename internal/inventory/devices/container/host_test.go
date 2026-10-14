package container_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestNewHost_ConstructsFromRecord proves the constructor hydrates a real
// Record without error and that basic identity fields round-trip.
func TestNewHost_ConstructsFromRecord(t *testing.T) {
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if got := string(item.ID()); got != "t1" {
		t.Errorf("ID() = %q, want %q", got, "t1")
	}
	if got := item.Name(); got != "t1" {
		t.Errorf("Name() = %q, want %q", got, "t1")
	}
}

// TestNewHost_BaselineCapabilities proves what a container host holds
// before onboarding: its SSH login and address, which the record states,
// and nothing about its engine, which only onboarding may grant.
func TestNewHost_BaselineCapabilities(t *testing.T) {
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("expected the vendor baseline to include SSHTransportCapable")
	}
	for _, c := range engines.Grants {
		if item.HasCapability(c) {
			t.Errorf("a container host holds %s before onboarding proved it", c)
		}
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected no capability beyond the vendor baseline with no classification data")
	}
}

// TestNewHost_UnionsClassificationCapabilities mirrors
// linux.TestNewServer_UnionsClassificationCapabilities: classification-
// derived Capabilities are unioned into the declared set at the data
// layer, but HasCapability for a capability Host does not structurally
// implement correctly stays false -- neither side is trusted alone.
func TestNewHost_UnionsClassificationCapabilities(t *testing.T) {
	rec := record.Record{
		ID:           "t1",
		Name:         "t1",
		Type:         "container_host",
		Capabilities: []capability.Name{capability.NameApt},
	}
	item, err := container.NewHost(rec)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	declared := item.Capabilities()
	found := false
	for _, c := range declared {
		if c == capability.NameApt {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected classification Capabilities() to be unioned into the declared set")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected HasCapability(AptCapable) to stay false: Host does not structurally implement AptCapable")
	}
}

// TestHost_SSHHost proves the "host" property round-trips, and that an
// absent one reads back empty rather than panicking -- the same contract
// linux.Server.SSHHost documents.
func TestHost_SSHHost(t *testing.T) {
	withHost, err := container.NewHost(record.Record{
		ID: "t1", Name: "t1", Type: "container_host",
		Properties: map[string]any{"host": "10.0.0.5"},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	h, ok := withHost.(*container.Host)
	if !ok {
		t.Fatalf("NewHost returned %T, want *container.Host", withHost)
	}
	if got := h.SSHHost(); got != "10.0.0.5" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.5")
	}

	noHost, err := container.NewHost(record.Record{ID: "t2", Name: "t2", Type: "container_host"})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if got := noHost.(*container.Host).SSHHost(); got != "" {
		t.Errorf("SSHHost() with no host property = %q, want empty string", got)
	}
}

// TestHost_SSHPort proves the "port" property round-trips and that an
// absent or zero value defaults to 22, matching linux.Server.SSHPort's
// own documented default.
func TestHost_SSHPort(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]any
		want       int
	}{
		{name: "explicit port", properties: map[string]any{"port": 2222}, want: 2222},
		{name: "absent defaults to 22", properties: nil, want: 22},
		{name: "explicit zero defaults to 22", properties: map[string]any{"port": 0}, want: 22},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, err := container.NewHost(record.Record{
				ID: "t1", Name: "t1", Type: "container_host", Properties: tt.properties,
			})
			if err != nil {
				t.Fatalf("NewHost: %v", err)
			}
			if got := item.(*container.Host).SSHPort(); got != tt.want {
				t.Errorf("SSHPort() = %d, want %d", got, tt.want)
			}
		})
	}
}

// engineDiscovery is a discovery of an engine reached over SSH, made
// against props as onboarding makes one, bound to the host and port.
func engineDiscovery(props map[string]inventory.PropertyValue, caps ...capability.Name) inventory.PropertyValue {
	d := inventory.Discovery{
		Protocol:     "docker",
		Capabilities: caps,
		Facts: map[string]any{
			engines.FactKey(capability.EngineDocker, engines.FieldSocket):   "/var/run/docker.sock",
			engines.FactKey(capability.EngineDocker, engines.FieldID):       "engine-1",
			engines.FactKey(capability.EngineDocker, engines.FieldVersion):  "29.9.0",
			engines.FactKey(capability.EngineDocker, engines.FieldCarriers): []any{"socket"},
		},
	}
	d.Binding = record.DiscoveryBinding(container.TypeHost, inventory.NewProperties(props))
	return d.Property()
}

// TestNewHost_RefusesATypedEndpoint is finding S1's fix at the record: a
// socket path typed into the record is refused by name, so an old
// inventory fails loudly rather than having its value ignored, and no
// value a person wrote can aim a task at a socket.
func TestNewHost_RefusesATypedEndpoint(t *testing.T) {
	for _, value := range []any{"/var/run/docker.sock", "/proc/self/root/run/other.sock", "npipe:////./pipe/docker_engine", ""} {
		_, err := container.NewHost(record.Record{
			ID: "t1", Name: "t1", Type: "container_host",
			Properties: map[string]inventory.PropertyValue{container.RefusedEndpointKey: value},
		})
		if err == nil || !strings.Contains(err.Error(), "pleiades onboard") {
			t.Errorf("docker_endpoint %q: err = %v, want a refusal naming onboarding", value, err)
		}
	}
}

// TestNewHost_GrantsWhatOnboardingProved proves a discovery grants the
// engine and its reach, and the accessors read the socket and identity
// onboarding recorded.
func TestNewHost_GrantsWhatOnboardingProved(t *testing.T) {
	props := map[string]inventory.PropertyValue{"host": "10.0.0.5"}
	props[inventory.DiscoveredProperty] = engineDiscovery(props, capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket)
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host", Properties: props})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	host := item.(*container.Host)
	if !host.HasCapability(capability.NameContainerEngine) || !slices.Equal(host.ContainerEngines(), []capability.EngineKind{capability.EngineDocker}) {
		t.Errorf("ContainerEngines() = %v, want docker alone", host.ContainerEngines())
	}
	if got := host.EngineCarriers(capability.EngineDocker); !slices.Equal(got, []capability.Carrier{capability.CarrierSocket}) {
		t.Errorf("EngineCarriers(docker) = %v, want [socket]", got)
	}
	if host.EngineID(capability.EnginePodman) != "" || host.HasCapability(capability.NamePodman) {
		t.Error("a Docker-only discovery answers for Podman")
	}
	if !host.HasCapability(capability.NameDocker) || !host.HasCapability(capability.NameEngineSocket) {
		t.Fatalf("capabilities = %v, want DockerCapable and EngineSocketCapable", host.Capabilities())
	}
	if host.HasCapability(capability.NameEngineLocal) {
		t.Error("EngineLocalCapable held without a discovery granting it")
	}
	if host.DockerEndpoint() != "/var/run/docker.sock" || host.EngineSocket(capability.EngineDocker) != "/var/run/docker.sock" || host.EngineID(capability.EngineDocker) != "engine-1" {
		t.Errorf("DockerEndpoint, EngineSocket, EngineID = %q, %q, %q", host.DockerEndpoint(), host.EngineSocket(capability.EngineDocker), host.EngineID(capability.EngineDocker))
	}
	if host.StaleDiscovery() != "" {
		t.Errorf("StaleDiscovery() = %q, want empty for a current discovery", host.StaleDiscovery())
	}
}

// TestNewHost_ARepointedRecordGrantsNothing proves the discovery is bound
// to host and port: once the record points elsewhere, the engine and its
// reach are gone until the host is onboarded again, so a discovery made
// against one machine never vouches for another.
func TestNewHost_ARepointedRecordGrantsNothing(t *testing.T) {
	props := map[string]inventory.PropertyValue{"host": "10.0.0.5"}
	props[inventory.DiscoveredProperty] = engineDiscovery(props, capability.NameDocker, capability.NameEngineSocket)
	props["host"] = "10.0.0.6"
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host", Properties: props})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if item.HasCapability(capability.NameDocker) || item.HasCapability(capability.NameEngineSocket) {
		t.Error("a repointed record still holds what the old host's discovery proved")
	}
	if !strings.Contains(item.(*container.Host).StaleDiscovery(), "pleiades onboard") {
		t.Errorf("StaleDiscovery() = %q, want it to name onboarding", item.(*container.Host).StaleDiscovery())
	}
}

// TestNewHost_ClassificationCannotGrantTheEngine proves a classification
// rule naming the engine's capabilities grants none of them: a rule is a
// claim nobody verified, and these are what onboarding alone may grant.
func TestNewHost_ClassificationCannotGrantTheEngine(t *testing.T) {
	item, err := container.NewHost(record.Record{
		ID: "t1", Name: "t1", Type: "container_host",
		Capabilities: engines.Grants,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	for _, c := range engines.Grants {
		if item.HasCapability(c) {
			t.Errorf("classification granted %s", c)
		}
	}
}

// TestNewHost_RefusesADiscoveryOutsideItsBound proves a discovery naming a
// capability onboarding may not grant a container host fails to load,
// rather than granting it.
func TestNewHost_RefusesADiscoveryOutsideItsBound(t *testing.T) {
	props := map[string]inventory.PropertyValue{"host": "10.0.0.5"}
	props[inventory.DiscoveredProperty] = engineDiscovery(props, capability.NameDocker, capability.NameApt)
	if _, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: "container_host", Properties: props}); err == nil {
		t.Fatal("expected a refusal for a discovery granting AptCapable")
	}
}

// TestHost_AddressAndTLSAccessors: the address a wait reaches, the engine's
// TLS address and pinned authority, and the binding a discovery must carry,
// read from the record as it is now.
func TestHost_AddressAndTLSAccessors(t *testing.T) {
	ca := selfSignedPEM(t)
	props := map[string]inventory.PropertyValue{"host": "10.0.0.5", container.EngineTLSProperty: "10.0.0.5:2376", devicetls.CAPEMProperty: ca}
	item, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: container.TypeHost, Properties: props})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	host := item.(*container.Host)
	if host.IPAddress() != "10.0.0.5" || host.EngineTLSAddress() != "10.0.0.5:2376" {
		t.Errorf("IPAddress, EngineTLSAddress = %q, %q", host.IPAddress(), host.EngineTLSAddress())
	}
	if string(host.TLSSettings().CAPEM()) != ca {
		t.Errorf("the pinned authority did not reach TLSSettings")
	}
	binding := host.DiscoveryBinding()
	if binding == "" || binding != record.DiscoveryBinding(container.TypeHost, inventory.NewProperties(props)) {
		t.Errorf("DiscoveryBinding = %q", binding)
	}
	props["host"] = "10.0.0.6"
	moved, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: container.TypeHost, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	if moved.(*container.Host).DiscoveryBinding() == binding {
		t.Error("moving the host did not change the binding")
	}
}

// TestNewHost_RefusesWeakTLSSettings: a TLS setting devicetls refuses is
// refused here too, naming the host.
func TestNewHost_RefusesWeakTLSSettings(t *testing.T) {
	_, err := container.NewHost(record.Record{ID: "t1", Name: "t1", Type: container.TypeHost,
		Properties: map[string]inventory.PropertyValue{devicetls.AllowDeprecatedProperty: true}})
	if err == nil || !strings.Contains(err.Error(), "t1") {
		t.Fatalf("NewHost = %v, want a refusal naming the host", err)
	}
}

// selfSignedPEM is a throwaway certificate authority, as PEM.
func selfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "engine ca"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
