// Live tests for engine onboarding: real Docker and Podman behind a real
// sshd, rootful and rootless, over every carrier each advertises.
package onboard

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// These are Phase 119b's RULE 0 evidence: each carrier and each engine, as
// onboarding finds it against a real engine behind a real sshd, then a
// session opened on the device that discovery describes, which is what every
// container method does.

// liveGateImage is the image the live tests run a container from.
const liveGateImage = "alpine:3.20.10"

// liveRC is a RunbookContext holding a device's credential.
type liveRC struct{ secrets map[string]string }

func (r liveRC) InjectSecrets() map[string]string { return r.secrets }
func (liveRC) SetStat(string, any) error          { return nil }
func (liveRC) EmitFact(string, any) error         { return nil }

// passwordFor is a fixture account's credential.
func passwordFor(user string) map[string]string {
	return map[string]string{wire.SecretUsername: user, wire.SecretPassword: testsupport.DockerHostPassword}
}

// trustFixture points host key verification at the fixture's real keys.
func trustFixture(t *testing.T, d *testsupport.EngineHost) {
	t.Helper()
	path, err := d.KnownHosts(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEIADES_KNOWN_HOSTS", path)
}

// onboarded probes a device of deviceType at props and returns the device
// rebuilt with what the probe proved, as onboarding stores it.
func onboarded(t *testing.T, deviceType string, props map[string]inventory.PropertyValue, secrets map[string]string) (inventory.InventoryItem, Probed) {
	t.Helper()
	prober, ok := Lookup(deviceType)
	if !ok {
		t.Fatalf("no prober for %s", deviceType)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	got, err := prober.Probe(ctx, build(t, deviceType, props), secrets)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	d := inventory.Discovery{Protocol: prober.Protocol(), Capabilities: got.Capabilities, Facts: got.Facts, ProbedAt: time.Now().UTC()}
	d.Binding = record.DiscoveryBinding(deviceType, inventory.NewProperties(props))
	withDiscovery := map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}
	for k, v := range props {
		withDiscovery[k] = v
	}
	return build(t, deviceType, withDiscovery), got
}

// listNames opens a session limited to listing containers and returns the
// names it sees.
func listNames(t *testing.T, device inventory.InventoryItem, secrets map[string]string, params map[string]any) (containerengine.Session, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := containerengine.Open(ctx, liveRC{secrets: secrets}, device, params, "test.list", containerengine.OpContainerList)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	list, err := s.API.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		t.Fatalf("ContainerList over %s: %v", s.Carrier, err)
	}
	var names []string
	for _, c := range list.Items {
		for _, n := range c.Names {
			names = append(names, strings.TrimPrefix(n, "/"))
		}
	}
	return *s, names
}

// TestEngineProbe_DockerOverEveryCarrierSSHAllows: with forwarding allowed,
// a Docker host is reached both through a channel to its socket and through
// dial-stdio, and a session takes the channel first.
func TestEngineProbe_DockerOverEveryCarrierSSHAllows(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	name := d.StartContainer(t, liveGateImage, "")
	props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
	secrets := passwordFor(testsupport.DockerHostUser)

	device, got := onboarded(t, container.TypeHost, props, secrets)
	for _, want := range []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket, capability.NameEngineStdio} {
		if !slices.Contains(got.Capabilities, want) {
			t.Errorf("capabilities %v lack %s", got.Capabilities, want)
		}
	}
	if got.Facts[engines.FactKey(capability.EngineDocker, engines.FieldID)] != d.EngineID(t) {
		t.Errorf("pinned %v, the engine says %s", got.Facts[engines.FactKey(capability.EngineDocker, engines.FieldID)], d.EngineID(t))
	}
	s, names := listNames(t, device, secrets, nil)
	if s.Carrier != capability.CarrierSocket || s.Kind != capability.EngineDocker {
		t.Errorf("session over %s to %s, want the socket channel to docker", s.Carrier, s.Kind)
	}
	if !slices.Contains(names, name) {
		t.Errorf("containers %v lack %s", names, name)
	}
}

// TestEngineProbe_DockerWhereSSHDDeniesForwarding: AllowTcpForwarding no
// leaves dial-stdio, which onboarding records alone, and a session uses.
func TestEngineProbe_DockerWhereSSHDDeniesForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{SSHDConfig: "AllowTcpForwarding no\n"})
	trustFixture(t, d)
	name := d.StartContainer(t, liveGateImage, "")
	props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
	secrets := passwordFor(testsupport.DockerHostUser)

	device, got := onboarded(t, container.TypeHost, props, secrets)
	if slices.Contains(got.Capabilities, capability.NameEngineSocket) || !slices.Contains(got.Capabilities, capability.NameEngineStdio) {
		t.Fatalf("capabilities %v, want stdio reach and no socket reach", got.Capabilities)
	}
	s, names := listNames(t, device, secrets, nil)
	if s.Carrier != capability.CarrierStdio {
		t.Errorf("session over %s, want stdio", s.Carrier)
	}
	if !slices.Contains(names, name) {
		t.Errorf("containers %v lack %s", names, name)
	}
}

// TestEngineProbe_Podman: Podman with no API socket listening is reached,
// rootful as root and rootless as its own user, each seeing its own store,
// through a service the session starts and stops.
func TestEngineProbe_Podman(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Podman behind a real sshd")
	}
	d := testsupport.StartPodmanHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	for _, tc := range []struct {
		user     string
		rootless string
	}{
		{testsupport.PodmanHostRootless, "true"},
		{testsupport.PodmanHostRootful, "false"},
	} {
		t.Run(tc.user, func(t *testing.T) {
			name := d.StartContainer(t, liveGateImage, tc.user)
			props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
			secrets := passwordFor(tc.user)
			device, got := onboarded(t, container.TypeHost, props, secrets)
			if !slices.Contains(got.Capabilities, capability.NamePodman) || !slices.Contains(got.Capabilities, capability.NameEngineStdio) {
				t.Fatalf("capabilities %v, want Podman over stdio", got.Capabilities)
			}
			if got.Facts[engines.FactKey(capability.EnginePodman, engines.FieldRootless)] != tc.rootless {
				t.Errorf("rootless = %v, want %s", got.Facts[engines.FactKey(capability.EnginePodman, engines.FieldRootless)], tc.rootless)
			}
			s, names := listNames(t, device, secrets, nil)
			if s.Kind != capability.EnginePodman || s.Carrier != capability.CarrierStdio {
				t.Errorf("session to %s over %s", s.Kind, s.Carrier)
			}
			if !slices.Contains(names, name) {
				t.Errorf("containers %v lack %s, which %s started", names, name, tc.user)
			}
			// The service the session started is gone once it closed. ps
			// failing is a failure, never an empty list that passes.
			ps, err := d.RootExec(context.Background(), "ps", "-eo", "args")
			if err != nil {
				t.Fatalf("listing the fixture's processes: %v", err)
			}
			if strings.Contains(ps, "podman system service --time=") {
				t.Errorf("a session's Podman service still runs after Close:\n%s", ps)
			}
		})
	}
}

// TestEngineProbe_LinuxServerFindsItsEngine: a linux_server gains the
// engine it runs through onboarding, and nothing else.
func TestEngineProbe_LinuxServerFindsItsEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
	device, got := onboarded(t, linux.TypeServer, props, passwordFor(testsupport.DockerHostUser))
	for _, c := range got.Capabilities {
		if !slices.Contains(engines.SSHGrants, c) {
			t.Errorf("a linux_server was granted %s", c)
		}
	}
	if !device.HasCapability(capability.NameDocker) || !device.HasCapability(capability.NameShellExec) {
		t.Errorf("the onboarded server lacks Docker or its own baseline: %v", device.Capabilities())
	}
}

// TestSession_RefusesAnotherEngine: a discovery whose pinned identity is
// not the engine that answers opens no session.
func TestSession_RefusesAnotherEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	props := map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port}
	secrets := passwordFor(testsupport.DockerHostUser)
	_, got := onboarded(t, container.TypeHost, props, secrets)
	got.Facts[engines.FactKey(capability.EngineDocker, engines.FieldID)] = "an-engine-onboarding-never-saw"
	disc := inventory.Discovery{Protocol: "engine", Capabilities: got.Capabilities, Facts: got.Facts}
	disc.Binding = record.DiscoveryBinding(container.TypeHost, inventory.NewProperties(props))
	device := build(t, container.TypeHost, map[string]inventory.PropertyValue{"host": d.Host, "port": d.Port, inventory.DiscoveredProperty: disc.Property()})
	_, err := containerengine.Open(context.Background(), liveRC{secrets: secrets}, device, nil, "test.list", containerengine.OpContainerList)
	if err == nil || !strings.Contains(err.Error(), "onboarding proved") {
		t.Fatalf("Open against a re-pinned identity: %v, want the identity refusal", err)
	}
}

// TestEngineProbe_DockerOverMutualTLS: a container host with no SSH login
// and an engine listening on TLS is onboarded and reached over TLS, its
// authority pinned and its client certificate from the credential store.
func TestEngineProbe_DockerOverMutualTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine")
	}
	d, addr, pki := testsupport.StartTLSDockerHost(t)
	name := d.StartContainer(t, liveGateImage, "")
	props := map[string]inventory.PropertyValue{
		container.EngineTLSProperty:         addr,
		devicetls.CAPEMProperty:             pki.CAPEM,
		devicetls.ServerNameProperty:        "localhost",
		devicetls.ClientCertificateProperty: true,
	}
	secrets := map[string]string{wire.SecretCertificatePEM: pki.ClientCert, wire.SecretPrivateKeyPEM: pki.ClientKey}

	device, got := onboarded(t, container.TypeHost, props, secrets)
	if !slices.Contains(got.Capabilities, capability.NameEngineTLS) {
		t.Fatalf("capabilities %v lack EngineTLSCapable", got.Capabilities)
	}
	s, names := listNames(t, device, secrets, nil)
	if s.Carrier != capability.CarrierTLS {
		t.Errorf("session over %s, want TLS", s.Carrier)
	}
	if !slices.Contains(names, name) {
		t.Errorf("containers %v lack %s", names, name)
	}

	// Without the client certificate the engine refuses the handshake.
	if _, err := containerengine.Open(context.Background(), liveRC{secrets: map[string]string{}}, device, nil, "test.list", containerengine.OpContainerList); err == nil {
		t.Error("a session opened with no client certificate")
	}
}

// TestPodmanService_ADroppedSessionLeavesNothingRunning is the failure-mode
// walk's far side for the stdio carrier: a login that drops without the
// session closing leaves no dial-stdio behind at once, and the Podman
// service the session started exits on its own within its idle time, so a
// lost Runner does not leave an engine API listening on the host.
func TestPodmanService_ADroppedSessionLeavesNothingRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Podman and waits out its idle time")
	}
	d := testsupport.StartPodmanHost(t, testsupport.EngineHostOptions{})
	trustFixture(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	runner := remoteexec.Shared(remoteexec.Options{})
	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: d.Host, Port: d.Port}, remoteexec.PasswordAuth(testsupport.PodmanHostRootless, testsupport.DockerHostPassword))
	if err != nil {
		t.Fatal(err)
	}
	dial, _ := containerengine.SSHStdio(conn, capability.EnginePodman, "")
	if _, err := containerengine.ReadIdentity(ctx, dial, capability.EnginePodman); err != nil {
		t.Fatalf("reading the identity through a session service: %v", err)
	}
	running := func(pattern string) bool {
		out, err := d.RootExec(ctx, "ps", "-eo", "args")
		if err != nil {
			t.Fatalf("listing the fixture's processes: %v", err)
		}
		return strings.Contains(out, pattern)
	}
	if !running("podman system service --time=") {
		out, _ := d.RootExec(ctx, "ps", "-eo", "user,args")
		t.Fatalf("the session's service is not running, so the test proves nothing:\n%s", out)
	}
	// The login drops: the session's stop function is never called.
	_ = conn.Close()

	deadline := time.Now().Add(15 * time.Second)
	for running("dial-stdio") && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	if running("dial-stdio") {
		t.Error("a dial-stdio process outlived the login it ran on")
	}
	deadline = time.Now().Add(90 * time.Second)
	for running("podman system service --time=") && time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
	}
	if running("podman system service --time=") {
		t.Error("the session's Podman service still runs 90 seconds after its login dropped")
	}
}
