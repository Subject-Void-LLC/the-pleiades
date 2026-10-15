// In-process tests of the container host probe against fake engines that
// answer the identity requests a real one does: the TLS carrier, the
// machine's own sockets, and every refusal before an engine is reached.
package onboard

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// engineHandler answers the identity request of kind as an engine with id.
func engineHandler(kind capability.EngineKind, id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case kind == capability.EngineDocker && r.URL.Path == "/info":
			fmt.Fprintf(w, `{"ID":%q,"ServerVersion":"29.9.0"}`, id)
		case kind == capability.EnginePodman && r.URL.Path == "/v5.0.0/libpod/info":
			fmt.Fprintf(w, `{"host":{"hostname":"h"},"store":{"graphRoot":%q,"runRoot":"/run/containers"},"version":{"Version":"5.8.7"}}`, id)
		default:
			http.NotFound(w, r)
		}
	})
}

// serveEngineSocket serves kind as an engine with id on a Unix socket at
// path.
func serveEngineSocket(t *testing.T, path string, kind capability.EngineKind, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: engineHandler(kind, id)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// shortTempDir is a temporary directory with a path short enough for a
// Unix socket in it.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// hostRecord builds a container_host from props.
func hostRecord(t *testing.T, props map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	item, err := container.NewHost(record.Record{ID: "h1", Name: "h1", Type: container.TypeHost, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// TestWithTLS: an engine reached over TLS is one more carrier to the engine
// SSH found when they agree, a new engine when SSH found none of its kind,
// and refused when they are two engines.
func TestWithTLS(t *testing.T) {
	tls := engineFound{kind: capability.EngineDocker, id: containerengine.Identity{ID: "e1"}, carriers: []capability.Carrier{capability.CarrierTLS}}

	got, err := withTLS([]engineFound{{kind: capability.EngineDocker, id: containerengine.Identity{ID: "e1"}, carriers: []capability.Carrier{capability.CarrierSocket}}}, tls)
	if err != nil || len(got) != 1 || !slices.Equal(got[0].carriers, []capability.Carrier{capability.CarrierSocket, capability.CarrierTLS}) {
		t.Errorf("the same engine: %+v, %v", got, err)
	}
	if again, _ := withTLS(got, tls); len(again[0].carriers) != 2 {
		t.Errorf("TLS was added twice: %v", again[0].carriers)
	}

	podman := engineFound{kind: capability.EnginePodman, id: containerengine.Identity{ID: "p1"}}
	if got, err := withTLS([]engineFound{podman}, tls); err != nil || len(got) != 2 {
		t.Errorf("no Docker over SSH: %+v, %v; want the TLS engine added", got, err)
	}

	other := []engineFound{{kind: capability.EngineDocker, id: containerengine.Identity{ID: "e2"}}}
	if _, err := withTLS(other, tls); err == nil || !strings.Contains(err.Error(), "one Docker engine") {
		t.Errorf("two engines: %v", err)
	}
}

// TestProbeSockets_TwoCarriersAnsweringAsTwoEngines: carriers to one socket
// that answer as different engines are named, and the first is kept.
func TestProbeSockets_TwoCarriersAnsweringAsTwoEngines(t *testing.T) {
	dir := shortTempDir(t)
	a, b := filepath.Join(dir, "a.sock"), filepath.Join(dir, "b.sock")
	serveEngineSocket(t, a, capability.EngineDocker, "engine-a")
	serveEngineSocket(t, b, capability.EngineDocker, "engine-b")
	found, notes := probeSockets(context.Background(), capability.EngineDocker, []string{"/socket"}, func(string) []attempt {
		return []attempt{
			{carrier: capability.CarrierSocket, dial: containerengine.Local(capability.SocketAddress(a)), release: func() {}},
			{carrier: capability.CarrierStdio, dial: containerengine.Local(capability.SocketAddress(b)), release: func() {}},
		}
	})
	if found == nil || found.id.ID != "engine-a" || !slices.Equal(found.carriers, []capability.Carrier{capability.CarrierSocket}) {
		t.Fatalf("found %+v, want engine-a over the socket alone", found)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "answered as engine-a") {
		t.Errorf("notes %v, want the disagreement named", notes)
	}
}

// TestProbeLocalEngines: refused unless the composition root allows it;
// then this user's rootless sockets, under XDG_RUNTIME_DIR, are found and
// marked rootless.
func TestProbeLocalEngines(t *testing.T) {
	allowLocalEngine(t, false)
	if _, _, err := probeLocalEngines(context.Background()); err == nil || !strings.Contains(err.Error(), "the machine that runs pleiades") {
		t.Fatalf("a probe of this machine without AllowLocalCarrier: %v", err)
	}

	allowLocalEngine(t, true)
	runtime := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	awayFromThisMachine(t, runtime)
	podmanSocket := filepath.Join(runtime, "podman", "podman.sock")
	serveEngineSocket(t, filepath.Join(runtime, "docker.sock"), capability.EngineDocker, "rootless-docker")
	serveEngineSocket(t, podmanSocket, capability.EnginePodman, "/home/me/.local/share/containers")
	found, _, err := probeLocalEngines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[capability.EngineKind]engineFound{}
	for _, e := range found {
		byKind[e.kind] = e
	}
	for kind, rootless := range map[capability.EngineKind]string{capability.EngineDocker: filepath.Join(runtime, "docker.sock"), capability.EnginePodman: podmanSocket} {
		e, ok := byKind[kind]
		if !ok {
			t.Errorf("no %s engine found", kind)
			continue
		}
		// With the system sockets pointed away from this machine, the engine
		// found is the one under the runtime directory, and it is rootless.
		if e.socket != rootless || !e.rootless || !slices.Equal(e.carriers, []capability.Carrier{capability.CarrierLocal}) {
			t.Errorf("%s at %s: rootless %v, carriers %v; want %s, rootless", kind, e.socket, e.rootless, e.carriers, rootless)
		}
	}
}

// TestContainerProbe_LocalHost: a container host with no address is this
// machine, probed through its own sockets and recorded with the account
// that opened them.
func TestContainerProbe_LocalHost(t *testing.T) {
	allowLocalEngine(t, true)
	runtime := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	awayFromThisMachine(t, runtime)
	serveEngineSocket(t, filepath.Join(runtime, "podman", "podman.sock"), capability.EnginePodman, "/home/me/.local/share/containers")

	got, err := containerProber{}.Probe(context.Background(), hostRecord(t, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Capabilities, capability.NamePodman) || !slices.Contains(got.Capabilities, capability.NameEngineLocal) {
		t.Errorf("capabilities %v, want Podman reached locally", got.Capabilities)
	}
	user, err := containerengine.LocalUser()
	if err != nil {
		t.Fatal(err)
	}
	if got.Facts[engines.FactLoginUser] != user {
		t.Errorf("login_user %v, want %q", got.Facts[engines.FactLoginUser], user)
	}
}

// TestContainerProbe_TLS: a host with only an engine_tls address is probed
// over TLS against the authority its record pins, and one whose TLS engine
// does not answer is refused naming what was tried.
func TestContainerProbe_TLS(t *testing.T) {
	srv := httptest.NewTLSServer(engineHandler(capability.EngineDocker, "tls-engine"))
	t.Cleanup(srv.Close)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	addr := srv.Listener.Addr().String()

	got, err := containerProber{}.Probe(context.Background(), hostRecord(t, map[string]inventory.PropertyValue{
		container.EngineTLSProperty: addr, devicetls.CAPEMProperty: ca,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Capabilities, capability.NameEngineTLS) || got.Facts[engines.FactKey(capability.EngineDocker, engines.FieldID)] != "tls-engine" {
		t.Errorf("probed %+v, want the Docker engine over TLS", got)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := closed.Addr().String()
	_ = closed.Close()
	_, err = containerProber{}.Probe(context.Background(), hostRecord(t, map[string]inventory.PropertyValue{container.EngineTLSProperty: gone}), nil)
	if err == nil || !strings.Contains(err.Error(), "no container engine answered") || !strings.Contains(err.Error(), "over TLS") {
		t.Errorf("an engine_tls address with nothing there: %v", err)
	}
}

// TestContainerProbe_Refusals: what cannot be probed is refused before any
// engine is asked.
func TestContainerProbe_Refusals(t *testing.T) {
	if _, err := (containerProber{}).Probe(context.Background(), nowhere{}, nil); err == nil {
		t.Error("a device that says nothing of where it is was probed")
	}
	if _, err := (containerProber{}).Probe(context.Background(), hostRecord(t, map[string]inventory.PropertyValue{"host": "10.0.0.5"}), map[string]string{}); err == nil {
		t.Error("a remote host with no credential was probed")
	}
	clientCert := hostRecord(t, map[string]inventory.PropertyValue{container.EngineTLSProperty: "127.0.0.1:2376", devicetls.ClientCertificateProperty: true})
	if _, err := (containerProber{}).Probe(context.Background(), clientCert, map[string]string{}); err == nil || !strings.Contains(err.Error(), "TLS settings") {
		t.Errorf("a client-certificate host with no certificate: %v", err)
	}
}

// nowhere is a device with no address of any kind.
type nowhere struct{ inventory.InventoryItem }

func (nowhere) Name() string { return "nowhere" }

// TestSSHEngineProbes_RefuseWhatCannotLogIn: the Linux and container host
// probers refuse a server with no address, one with no credential, and one
// whose login cannot connect, before any engine is asked.
func TestSSHEngineProbes_RefuseWhatCannotLogIn(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()
	password := map[string]string{"username": "u", "password": "p"}

	server := func(props map[string]inventory.PropertyValue) inventory.InventoryItem {
		item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server", Properties: props})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	if _, err := (linuxProber{}).Probe(context.Background(), server(nil), password); err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("a server with no address: %v", err)
	}
	for name, p := range map[string]Prober{"linux": linuxProber{}, "container": containerProber{}} {
		var device inventory.InventoryItem
		props := map[string]inventory.PropertyValue{"host": "127.0.0.1", "port": port}
		if name == "linux" {
			device = server(props)
		} else {
			device = hostRecord(t, props)
		}
		if _, err := p.Probe(context.Background(), device, map[string]string{}); err == nil {
			t.Errorf("%s: a server with no credential was probed", name)
		}
		if _, err := p.Probe(context.Background(), device, password); err == nil {
			t.Errorf("%s: a login to a closed port succeeded", name)
		}
	}
}

// awayFromThisMachine points the local system sockets at paths under dir
// that nothing serves, so a test never reads this machine's own Docker or
// Podman engine, whatever is running on it.
func awayFromThisMachine(t *testing.T, dir string) {
	t.Helper()
	was := localSystemSockets
	t.Cleanup(func() { localSystemSockets = was })
	localSystemSockets = map[capability.EngineKind]string{
		capability.EngineDocker: filepath.Join(dir, "absent-system-docker.sock"),
		capability.EnginePodman: filepath.Join(dir, "absent-system-podman.sock"),
	}
}

// allowLocalEngine answers onboarding's question whether this process may
// probe its own engine sockets, for this test alone.
func allowLocalEngine(t *testing.T, allowed bool) {
	t.Helper()
	was := localEngineAllowed
	t.Cleanup(func() { localEngineAllowed = was })
	localEngineAllowed = func() bool { return allowed }
}

// TestLocalEngine_IsTheEnginePortsOneSwitch: onboarding may probe this
// machine's own sockets exactly when the engine port may reach them, with
// no switch of its own a process could set without the other (the onboarded
// device would then be unreachable from the process that onboarded it).
func TestLocalEngine_IsTheEnginePortsOneSwitch(t *testing.T) {
	containerengine.AllowLocalCarrier()
	if !localEngineAllowed() || !containerengine.LocalCarrierAllowed() {
		t.Fatal("onboarding does not read the engine port's local switch")
	}
}
