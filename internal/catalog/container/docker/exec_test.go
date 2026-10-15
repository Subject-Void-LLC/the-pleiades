package docker_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// This file proves "container.docker.exec"'s own branches against an
// in-process fake engine speaking real HTTP over a real Unix socket, the
// way a real engine answers (a ping, then 101 and a framed stream for the
// exec start). The real engine is the release gates' job:
// cmd/pleiades/container_release_gate_test.go and internal/adapters/native's
// container exec test run this method against Docker behind a real sshd.
// The method is still worth proving through its own Invoke, not only
// through pkg/containerengine's Exec, the same reasoning
// internal/transport/serial and internal/transport/serialtcp's own
// backfilled adapter tests (FAILURE_PATTERNS.md #173) already apply: a
// thin caller still has its own branches (the capability type assertion,
// the stat recording, the non-zero-exit error path).

// engineUnderTest is the identity the fake daemon reports and the test
// device says onboarding pinned.
const engineUnderTest = "engine-under-test"

// dockerDevice wraps inventorytest.Stub with the accessors
// capability.DockerCapable and capability.EngineLocalCapable require: a
// device whose engine this machine reaches through its own socket, as
// onboarding would have proved for a host-less container_host.
type dockerDevice struct {
	*inventorytest.Stub
	socket capability.SocketAddress
	id     capability.EngineID
	user   string
}

func (d *dockerDevice) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *dockerDevice) DockerEndpoint() capability.SocketAddress { return d.socket }
func (d *dockerDevice) EngineSocket(capability.EngineKind) capability.SocketAddress {
	return d.socket
}
func (d *dockerDevice) EngineID(capability.EngineKind) capability.EngineID { return d.id }
func (d *dockerDevice) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}

// EngineLoginUser is the account this test runs as, which is who onboarding
// on this machine would have recorded.
func (d *dockerDevice) EngineLoginUser() string { return d.user }

// shortTempDir returns a fresh temporary directory, removed when the test
// ends, whose path is short enough to hold a Unix socket.
//
// t.TempDir is the obvious thing to reach for and is the wrong one here:
// it builds its directory name out of the test's own name, and macOS puts
// TMPDIR under /var/folders/<2>/<28>/T/, so a descriptively named test
// pushes the socket path past sockaddr_un's 104-byte sun_path limit and
// net.Listen fails with the famously unhelpful "bind: invalid argument".
// Linux allows 108 bytes and puts TMPDIR at /tmp, which is why this was
// invisible until CI grew a macos-latest leg. A two-character prefix keeps
// the whole path near 70 bytes on either platform.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "px")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newDockerDevice starts a real fake daemon serving mux over a real Unix
// socket, and returns an InventoryItem declaring capability.NameDocker
// and implementing capability.DockerCapable against it.
func newDockerDevice(t *testing.T, mux *http.ServeMux) inventory.InventoryItem {
	t.Helper()
	sockPath := filepath.Join(shortTempDir(t), "docker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	who, err := containerengine.LocalUser()
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	// This engine is reached through this machine's own socket, which only
	// the command line may use; these tests stand where it does.
	containerengine.AllowLocalCarrier()
	return &dockerDevice{
		Stub:   &inventorytest.Stub{StubName: "host1", Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal}},
		socket: capability.SocketAddress(sockPath),
		id:     engineUnderTest,
		user:   who,
	}
}

// writeExecFrame writes one Docker exec-attach frame (8-byte header plus
// payload) to a hijacked connection's buffered writer.
func writeExecFrame(w *bufio.Writer, streamType byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = streamType
	header[7] = byte(len(payload))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

// versionPrefix is the API version segment a client puts in front of every
// path but the ping.
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// fakeEngine serves what a real engine does before any operation: the ping
// that fixes the API version, answered with apiVersion, and the identity
// read, answered with engineUnderTest. Every other request goes to route
// with its version prefix removed.
func fakeEngine(apiVersion string, route func(w http.ResponseWriter, r *http.Request, path string)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := versionPrefix.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/_ping":
			w.Header().Set("API-Version", apiVersion)
			_, _ = w.Write([]byte("OK"))
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": engineUnderTest, "ServerVersion": "29.9.0"})
		default:
			route(w, r, path)
		}
	})
	return mux
}

// execFakeDaemon builds the standard fake daemon mux for a successful
// exec, reporting engineUnderTest as its identity, exitCode as the
// command's status, and echoing back stdout/stderr. The exec start is
// answered the way a real engine answers it: 101, then the framed stream.
func execFakeDaemon(t *testing.T, stdout, stderr string, exitCode int) *http.ServeMux {
	t.Helper()
	return fakeEngine("1.44", func(w http.ResponseWriter, r *http.Request, path string) {
		switch {
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
		case path == "/exec/execabc123/start":
			// The request's body first, as a real engine reads it: a socket
			// closed with unread input sends a reset rather than an end of
			// stream, and the client could lose the frames before it.
			_, _ = io.Copy(io.Discard, r.Body)
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server's ResponseWriter does not support Hijack")
				return
			}
			conn, rw, err := hj.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			if stdout != "" {
				writeExecFrame(rw.Writer, 1, []byte(stdout))
			}
			if stderr != "" {
				writeExecFrame(rw.Writer, 2, []byte(stderr))
			}
			_ = rw.Flush()
		case path == "/exec/execabc123/json":
			_ = json.NewEncoder(w).Encode(map[string]int{"ExitCode": exitCode})
		default:
			t.Errorf("the fake engine was sent %s %s, which an exec does not send", r.Method, path)
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	})
}

// TestExec_RoundTrips proves the happy path end to end: a real container
// name and cmd reach the fake daemon, and its real response reaches
// collection.Result and the recorded stats.
func TestExec_RoundTrips(t *testing.T) {
	device := newDockerDevice(t, execFakeDaemon(t, "app started\n", "", 0))
	rc := &ctxStub{stats: map[string]any{}}

	result, err := dockermod.Exec(context.Background(), rc, device, map[string]any{
		"name": "web-1",
		"cmd":  "echo hi",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !result.Changed {
		t.Error("expected Changed true")
	}
	if rc.stats["name"] != "web-1" {
		t.Errorf("stat name = %v, want web-1", rc.stats["name"])
	}
	if rc.stats["exit_code"] != 0 {
		t.Errorf("stat exit_code = %v, want 0", rc.stats["exit_code"])
	}
	if rc.stats["stdout"] != "app started\n" {
		t.Errorf("stat stdout = %v, want %q", rc.stats["stdout"], "app started\n")
	}
}

// TestExec_NonZeroExitIsAnError proves a non-zero real exit code is
// treated as a task failure, exactly the line exec.command draws, with
// stats still recorded before the error is returned.
func TestExec_NonZeroExitIsAnError(t *testing.T) {
	device := newDockerDevice(t, execFakeDaemon(t, "", "not found\n", 127))
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, device, map[string]any{
		"name": "web-1",
		"cmd":  "nosuchcmd",
	})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit code")
	}
	if rc.stats["exit_code"] != 127 {
		t.Errorf("stat exit_code = %v, want 127 (recorded before the error return)", rc.stats["exit_code"])
	}
}

// TestExec_MissingName proves a missing required "name" param is
// refused before any daemon request is attempted.
func TestExec_MissingName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("daemon should never be reached with a missing name param, got %s %s", r.Method, r.URL.Path)
	})
	device := newDockerDevice(t, mux)
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, device, map[string]any{"cmd": "echo hi"})
	if err == nil {
		t.Fatal("expected an error for a missing name param")
	}
}

// TestExec_MissingCmd proves a missing required "cmd" param is refused
// before any daemon request is attempted.
func TestExec_MissingCmd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("daemon should never be reached with a missing cmd param, got %s %s", r.Method, r.URL.Path)
	})
	device := newDockerDevice(t, mux)
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, device, map[string]any{"name": "web-1"})
	if err == nil {
		t.Fatal("expected an error for a missing cmd param")
	}
}

// TestExec_NoDockerAccessor proves a device that declares NameDocker
// (inventorytest.Stub trusts the declaration, per its own doc comment)
// but does not implement DockerCapable's accessor fails explicitly
// rather than panicking -- the defense-in-depth branch behind
// HasCapability, mirroring
// TestTransportActionExecutor_RejectsCapabilityWithNoTargetAccessor's own
// reasoning in internal/engine.
func TestExec_NoDockerAccessor(t *testing.T) {
	impostor := &inventorytest.Stub{StubName: "impostor", Caps: []capability.Name{capability.NameDocker}}
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, impostor, map[string]any{
		"name": "web-1",
		"cmd":  "echo hi",
	})
	if err == nil {
		t.Fatal("expected an error for a device that declares NameDocker but does not implement DockerCapable")
	}
}

// TestExec_NilDevice proves a nil target device is refused with a clear
// error rather than a nil-pointer panic.
func TestExec_NilDevice(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dockermod.Exec(context.Background(), rc, nil, map[string]any{
		"name": "web-1",
		"cmd":  "echo hi",
	})
	if err == nil {
		t.Fatal("expected an error for a nil device")
	}
}

// TestExec_DeviceDoesNotDeclareDocker proves a device that declares no
// capabilities at all (as opposed to TestExec_NoDockerAccessor's device,
// which declares NameDocker but does not implement it) is refused by the
// HasCapability check itself, the first line of defense.
func TestExec_DeviceDoesNotDeclareDocker(t *testing.T) {
	plain := &inventorytest.Stub{StubName: "plain"}
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, plain, map[string]any{
		"name": "web-1",
		"cmd":  "echo hi",
	})
	if err == nil {
		t.Fatal("expected an error for a device that does not declare capability.NameDocker")
	}
}

// TestExec_RecordStatFails proves a stat-recording failure (the
// RunbookContext itself failing, not the daemon) is surfaced as a real
// error rather than silently dropped, for each of the four stats Exec
// records, mirroring TestStop_RecordStatFails's own shape for the
// identical class of failure in this package.
func TestExec_RecordStatFails(t *testing.T) {
	for _, key := range []string{"name", "exit_code", "stdout", "stderr"} {
		t.Run(key, func(t *testing.T) {
			device := newDockerDevice(t, execFakeDaemon(t, "out\n", "", 0))
			rc := &ctxStub{stats: map[string]any{}, failOnKey: key}

			_, err := dockermod.Exec(context.Background(), rc, device, map[string]any{
				"name": "web-1",
				"cmd":  "echo hi",
			})
			if err == nil {
				t.Errorf("expected an error when recording stat %q fails", key)
			}
		})
	}
}

// TestExec_DaemonUnreachableIsAClearError proves a device pointing at a
// socket nothing is listening on fails with a clear, wrapped error.
func TestExec_DaemonUnreachableIsAClearError(t *testing.T) {
	device := &dockerDevice{
		Stub:   &inventorytest.Stub{StubName: "host1", Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker}},
		socket: capability.SocketAddress(filepath.Join(t.TempDir(), "no-daemon.sock")),
	}
	rc := &ctxStub{stats: map[string]any{}}

	_, err := dockermod.Exec(context.Background(), rc, device, map[string]any{
		"name": "web-1",
		"cmd":  "echo hi",
	})
	if err == nil {
		t.Fatal("expected an error when the daemon socket is unreachable")
	}
}

// TestExec_RefusesAnotherEngine proves an engine whose identity differs
// from the one onboarding pinned is refused before any exec request
// reaches it, the way a pinned host key refuses another machine.
func TestExec_RefusesAnotherEngine(t *testing.T) {
	execReached := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "some-other-engine"})
			return
		}
		execReached = true
		http.Error(w, "should not be reached", http.StatusTeapot)
	})
	device := newDockerDevice(t, mux)

	_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": "web", "cmd": "id"})
	if err == nil || !strings.Contains(err.Error(), "some-other-engine") {
		t.Fatalf("err = %v, want a refusal naming the engine that answered", err)
	}
	if execReached {
		t.Error("an exec request reached an engine onboarding never proved")
	}
}

// TestExec_RefusesADeviceThatAdvertisesNoReach proves a device that says it
// runs Docker but advertises no way to reach the engine is refused naming
// onboarding, rather than any socket being dialed.
func TestExec_RefusesADeviceThatAdvertisesNoReach(t *testing.T) {
	device := &dockerDevice{
		Stub:   &inventorytest.Stub{StubName: "host1", Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker}},
		socket: "/var/run/docker.sock",
		id:     engineUnderTest,
	}
	_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": "web", "cmd": "id"})
	if err == nil || !strings.Contains(err.Error(), "pleiades onboard") {
		t.Fatalf("err = %v, want a refusal naming onboarding", err)
	}
}

// TestExec_RefusesAnEmptyPinnedIdentity proves a device whose discovery
// holds no engine identity pins nothing, so it is refused rather than
// matched against whatever answers.
func TestExec_RefusesAnEmptyPinnedIdentity(t *testing.T) {
	device := newDockerDevice(t, execFakeDaemon(t, "", "", 0)).(*dockerDevice)
	device.id = ""
	_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": "web", "cmd": "id"})
	if err == nil || !strings.Contains(err.Error(), "identity on record") {
		t.Fatalf("err = %v, want a refusal for a missing identity", err)
	}
}

// TestExec_RefusesAnEngineBelowTheFloor proves an engine serving an API
// older than the port's floor is refused, naming its version and the floor,
// before any exec request is formed.
func TestExec_RefusesAnEngineBelowTheFloor(t *testing.T) {
	execReached := false
	device := newDockerDevice(t, fakeEngine("1.40", func(w http.ResponseWriter, r *http.Request, path string) {
		execReached = true
		http.Error(w, "should not be reached", http.StatusTeapot)
	}))
	_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": "web", "cmd": "id"})
	if err == nil || !strings.Contains(err.Error(), "1.40") || !strings.Contains(err.Error(), "1.41") {
		t.Fatalf("err = %v, want a refusal naming the engine's API version and the floor", err)
	}
	if execReached {
		t.Error("an exec request reached an engine below the floor")
	}
}

// TestExec_RefusesAnotherAccount proves a run as a different account from
// the one onboarding found the engine as is refused, naming both and
// re-onboarding, before the engine is dialed at all.
func TestExec_RefusesAnotherAccount(t *testing.T) {
	reached := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { reached = true })
	device := newDockerDevice(t, mux).(*dockerDevice)
	device.user = "someone-else"

	_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": "web", "cmd": "id"})
	if err == nil || !strings.Contains(err.Error(), "someone-else") || !strings.Contains(err.Error(), "pleiades onboard") {
		t.Fatalf("err = %v, want a refusal naming the account onboarding found and re-onboarding", err)
	}
	if reached {
		t.Error("the engine was reached for a run as another account")
	}
}

// TestExec_RefusesAHostileContainerName proves a container name that could
// reshape a request path is refused naming it, before the engine is asked
// anything beyond the session's own opening.
func TestExec_RefusesAHostileContainerName(t *testing.T) {
	for _, name := range []string{"web/../../images/create", "web?force=1", "web#x", "web%2Fx", "-web", "web x"} {
		t.Run(name, func(t *testing.T) {
			device := newDockerDevice(t, execFakeDaemon(t, "", "", 0))
			_, err := dockermod.Exec(context.Background(), &ctxStub{stats: map[string]any{}}, device, map[string]any{"name": name, "cmd": "id"})
			if err == nil || !strings.Contains(err.Error(), "no engine allows") {
				t.Fatalf("err = %v, want the name refused", err)
			}
		})
	}
}
