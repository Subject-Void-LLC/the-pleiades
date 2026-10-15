// Tests for gate.run's image checks and engine precondition, against an
// engine that answers over a real Unix socket the way a real one does;
// cmd/pleiades's gate release gate runs them on a real engine.
package gate_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// engineID is the identity the device's record pins.
const engineID capability.EngineID = "gate-engine-under-test"

// versionPrefix is the API version in front of every path but the ping.
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// engineDevice is an SSH device whose Docker engine is reached through a
// socket on this machine.
type engineDevice struct {
	*sshDevice
	socket capability.SocketAddress
	user   string
}

func (d *engineDevice) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *engineDevice) DockerEndpoint() capability.SocketAddress                    { return d.socket }
func (d *engineDevice) EngineSocket(capability.EngineKind) capability.SocketAddress { return d.socket }
func (d *engineDevice) EngineID(capability.EngineKind) capability.EngineID          { return engineID }
func (d *engineDevice) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (d *engineDevice) EngineLoginUser() string { return d.user }

// fakeEngine answers a one-shot run: the image is present, a container is
// created, attached (101 and the frames), started, and ends with exit; it
// says it is identity.
type fakeEngine struct {
	identity string
	output   string
	exit     int

	mu      sync.Mutex
	removed int
}

func (e *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	switch {
	case path == "/_ping":
		w.Header().Set("API-Version", "1.44")
		_, _ = w.Write([]byte("OK"))
	case path == "/info":
		_ = json.NewEncoder(w).Encode(map[string]string{"ID": e.identity, "ServerVersion": "29.9.0"})
	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:" + strings.Repeat("ef", 32)})
	case path == "/containers/create":
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("ab", 32), "Warnings": []string{}})
	case strings.HasSuffix(path, "/attach"):
		_, _ = io.Copy(io.Discard, r.Body)
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		writeFrame(rw.Writer, []byte(e.output))
		_ = rw.Flush()
	case strings.HasSuffix(path, "/start"):
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(path, "/wait"):
		_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": e.exit})
	case r.Method == http.MethodDelete:
		e.mu.Lock()
		e.removed++
		e.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+path, http.StatusTeapot)
	}
}

// writeFrame writes one standard-output frame of an attached stream.
func writeFrame(w *bufio.Writer, payload []byte) {
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

// withEngine gives h's device an engine served by e over a real socket.
func withEngine(t *testing.T, h *harness, e *fakeEngine) *engineDevice {
	t.Helper()
	dir, err := os.MkdirTemp("", "ge")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	who, err := containerengine.LocalUser()
	if err != nil {
		t.Fatal(err)
	}
	// The engine is this machine's socket, which only the command line may
	// reach; these tests stand where it does.
	containerengine.AllowLocalCarrier()
	h.device.Caps = append(h.device.Caps, capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal)
	return &engineDevice{sshDevice: h.device, socket: capability.SocketAddress(sock), user: who}
}

// imageRef is a digest-pinned image.
var imageRef = "alpine@sha256:" + strings.Repeat("cd", 32)

// TestRun_ImageChecksRunInAOneShotContainer: a passing, a failing and a
// declared-skip image check, each reported as itself, the failure with its
// output, and every container removed.
func TestRun_ImageChecksRunInAOneShotContainer(t *testing.T) {
	for name, tc := range map[string]struct {
		exit   int
		status string
	}{"passes": {0, "PASS"}, "fails": {2, "FAIL"}, "skips": {77, "SKIP"}} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			e := &fakeEngine{identity: string(engineID), output: "inside the container\n", exit: tc.exit}
			device := withEngine(t, h, e)
			manifest := h.manifest(t, "version: 1\nchecks:\n  - {name: img, kind: pure, timeout: 1m, shell: \"run-it\", image: '"+imageRef+"', skip_exit: [77]}\n")
			_, err := gate.Run(context.Background(), h.rc, device, params(manifest, nil))
			got := resultsByName(t, h.rc)["img"]
			if got["status"] != tc.status || (err != nil) != (tc.status == "FAIL") {
				t.Fatalf("img = %v, err = %v", got, err)
			}
			if tc.status != "PASS" && !strings.Contains(got["tail"].(string), "inside the container") {
				t.Errorf("tail = %q", got["tail"])
			}
			if e.removed != 1 {
				t.Errorf("removed %d containers, want 1", e.removed)
			}
		})
	}
}

// TestRun_TheEnginePreconditionAsksTheEngine: an engine that answers as
// the one onboarding pinned lets the check run; one answering as another
// skips it, naming why.
func TestRun_TheEnginePreconditionAsksTheEngine(t *testing.T) {
	for identity, want := range map[string]string{string(engineID): "PASS", "some-other-engine": "SKIP"} {
		h := newHarness(t)
		device := withEngine(t, h, &fakeEngine{identity: identity})
		manifest := h.manifest(t, "version: 1\nchecks:\n  - {name: needs-engine, kind: pure, timeout: 1m, shell: \"true\", requires: {engine: true}}\n")
		if _, err := gate.Run(context.Background(), h.rc, device, params(manifest, nil)); err != nil {
			t.Fatal(err)
		}
		got := resultsByName(t, h.rc)["needs-engine"]
		if got["status"] != want {
			t.Errorf("engine answering as %s: %v, want %s", identity, got, want)
		}
		if want == "SKIP" && !strings.HasPrefix(got["reason"].(string), "needs engine: ") {
			t.Errorf("reason = %v", got["reason"])
		}
	}
}
