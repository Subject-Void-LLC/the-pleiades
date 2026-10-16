// The harness the build methods' tests run against: a real SSH server in
// this process running each command through a real /bin/sh on this machine
// (so the files, their modes and the atomic replace are real), and an
// engine answering the distribution route over a real Unix socket.
package digests_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// engineID is the identity the device's record pins.
const engineID capability.EngineID = "build-engine-under-test"

// versionPrefix is the API version in front of every path but the ping.
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// device is an SSH device that runs commands, with a Docker engine on a
// socket of this machine when engine is set.
type device struct {
	*inventorytest.Stub
	host   string
	port   int
	socket capability.SocketAddress
	user   string
}

func (d *device) SSHHost() string          { return d.host }
func (d *device) SSHPort() int             { return d.port }
func (d *device) WorkingDirectory() string { return "" }
func (d *device) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *device) DockerEndpoint() capability.SocketAddress                    { return d.socket }
func (d *device) EngineSocket(capability.EngineKind) capability.SocketAddress { return d.socket }
func (d *device) EngineID(capability.EngineKind) capability.EngineID          { return engineID }
func (d *device) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (d *device) EngineLoginUser() string { return d.user }

// stubContext keeps every return a method sets.
type stubContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *stubContext) InjectSecrets() map[string]string     { return c.secrets }
func (c *stubContext) SetStat(key string, value any) error  { c.stats[key] = value; return nil }
func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// registry answers the distribution route: digests maps a reference
// (name:tag) to the digest its tag names now; during, when set, runs as a
// reference is resolved.
type registry struct {
	mu      sync.Mutex
	digests map[string]string
	during  func(ref string)
	asked   []string
}

func (r *registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := versionPrefix.ReplaceAllString(req.URL.Path, "")
	switch {
	case path == "/_ping":
		w.Header().Set("API-Version", "1.44")
		_, _ = w.Write([]byte("OK"))
	case path == "/info":
		_ = json.NewEncoder(w).Encode(map[string]string{"ID": string(engineID), "ServerVersion": "29.9.0"})
	case strings.HasPrefix(path, "/distribution/") && strings.HasSuffix(path, "/json"):
		ref := strings.TrimSuffix(strings.TrimPrefix(path, "/distribution/"), "/json")
		r.mu.Lock()
		r.asked = append(r.asked, ref)
		digest, ok := r.digests[ref]
		during := r.during
		r.mu.Unlock()
		if during != nil {
			during(ref)
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "manifest unknown"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Descriptor": map[string]any{"mediaType": "application/vnd.oci.image.index.v1+json", "digest": digest, "size": 100},
			"Platforms":  []any{map[string]string{"os": "linux", "architecture": "amd64"}},
		})
	default:
		http.Error(w, "unexpected "+path, http.StatusTeapot)
	}
}

// harness is one test's device, context and project directory.
type harness struct {
	device  *device
	rc      *stubContext
	project string
}

// newHarness starts the SSH server; with reg, the device also has an
// engine answering through reg.
func newHarness(t *testing.T, reg *registry) *harness {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	// The server's commands run as this process, in its environment, so
	// the login's state directory (where a refresh keeps its undo record)
	// is the test's own rather than this machine's.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d := &device{
		Stub: &inventorytest.Stub{StubID: "build-1", StubName: "build-box", StubState: inventory.StateActive,
			Caps: []capability.Name{capability.NameSSHTransport, capability.NameCommandExec}},
		host: srv.Host, port: srv.Port,
	}
	if reg != nil {
		dir, err := os.MkdirTemp("", "be")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		sock := filepath.Join(dir, "d.sock")
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		hs := &http.Server{Handler: reg}
		go func() { _ = hs.Serve(ln) }()
		t.Cleanup(func() { _ = hs.Close() })
		if d.user, err = containerengine.LocalUser(); err != nil {
			t.Fatal(err)
		}
		// The engine is this machine's socket, which only the command line
		// may reach; these tests stand where it does.
		containerengine.AllowLocalCarrier()
		d.socket = capability.SocketAddress(sock)
		d.Caps = append(d.Caps, capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal)
	}
	return &harness{
		device:  d,
		rc:      &stubContext{secrets: map[string]string{"username": srv.Username, "password": srv.Password}, stats: map[string]any{}},
		project: t.TempDir(),
	}
}

// write writes content to name in the project with mode, returning its path.
func (h *harness) write(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(h.project, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// read reads a project file.
func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// params builds a task's parameters with extra merged in.
func params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// paths is a list parameter of paths.
func paths(ps ...string) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = p
	}
	return out
}

// digest is a well-formed digest built from one hex byte.
func digest(b string) string { return "sha256:" + strings.Repeat(b, 32) }
