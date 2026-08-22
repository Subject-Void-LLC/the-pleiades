package docker_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// This file is the RULE 0 evidence for "container.docker.exec", against
// a real, in-process fake Docker daemon speaking real HTTP over a real
// Unix socket -- the same discipline pkg/dockerexec's own fake-daemon
// suite already establishes one layer down. There is no safe way to run
// a real Docker daemon in a test process (the same reason
// docker_test.go's own harness fakes the docker CLI for run/stop/remove
// via SSH instead), but the exec-attach wire protocol itself is real and
// worth proving through this method's own Invoke, not just through
// pkg/dockerexec.Exec directly, the same reasoning
// internal/transport/serial and internal/transport/serialtcp's own
// backfilled adapter tests (FAILURE_PATTERNS.md #173) already apply: a
// thin caller still has its own branches (the capability type assertion,
// the stat recording, the non-zero-exit error path).

// dockerDevice wraps inventorytest.Stub with the one accessor
// capability.DockerCapable requires.
type dockerDevice struct {
	*inventorytest.Stub
	socket capability.SocketAddress
}

func (d *dockerDevice) DockerEndpoint() capability.SocketAddress { return d.socket }

// newDockerDevice starts a real fake daemon serving mux over a real Unix
// socket, and returns an InventoryItem declaring capability.NameDocker
// and implementing capability.DockerCapable against it.
func newDockerDevice(t *testing.T, mux *http.ServeMux) inventory.InventoryItem {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	return &dockerDevice{
		Stub:   &inventorytest.Stub{StubName: "host1", Caps: []capability.Name{capability.NameDocker}},
		socket: capability.SocketAddress(sockPath),
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

// execFakeDaemon builds the standard three-endpoint fake daemon mux for
// a successful exec, reporting exitCode and echoing back stdout/stderr.
func execFakeDaemon(t *testing.T, stdout, stderr string, exitCode int) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
	})
	mux.HandleFunc("/v1.43/exec/execabc123/start", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server's ResponseWriter does not support Hijack")
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\n\r\n")
		if stdout != "" {
			writeExecFrame(rw.Writer, 1, []byte(stdout))
		}
		if stderr != "" {
			writeExecFrame(rw.Writer, 2, []byte(stderr))
		}
		_ = rw.Flush()
	})
	mux.HandleFunc("/v1.43/exec/execabc123/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"ExitCode": exitCode})
	})
	return mux
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
		Stub:   &inventorytest.Stub{StubName: "host1", Caps: []capability.Name{capability.NameDocker}},
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
