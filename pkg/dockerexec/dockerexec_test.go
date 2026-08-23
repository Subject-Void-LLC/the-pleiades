package dockerexec_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/dockerexec"
)

// This file is the RULE 0 evidence for dockerexec.Exec, against a real,
// in-process fake Docker daemon speaking real HTTP over a real Unix
// socket -- the same discipline pkg/remoteexec, pkg/serialtcp,
// pkg/telnetexec, and pkg/rfc2217's own fake-server suites already
// establish. It proves the three-request lifecycle, the exec-attach
// frame protocol, and the real exit code all work end to end against a
// real net/http.Server using real http.Hijacker semantics -- the same
// mechanism a real Docker daemon (itself written in Go) uses to serve
// this exact endpoint.

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

// fakeDaemon starts a real net/http.Server listening on a real Unix
// socket under shortTempDir, serving mux, and returns the socket path.
func fakeDaemon(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	sockPath := filepath.Join(shortTempDir(t), "docker.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sockPath
}

// writeRawFrame writes one Docker exec-attach frame directly to a
// hijacked bufio.Writer, mirroring internal_test.go's own writeFrame but
// against the *bufio.ReadWriter Hijack returns.
func writeRawFrame(w *bufio.Writer, streamType byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = streamType
	header[7] = byte(len(payload))
	header[6] = byte(len(payload) >> 8)
	header[5] = byte(len(payload) >> 16)
	header[4] = byte(len(payload) >> 24)
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

// TestExec_RoundTripsAgainstARealFakeDaemon proves the full three-request
// lifecycle: create sends Cmd wrapped in /bin/sh -c, start's hijacked
// response is parsed into separate stdout/stderr, and inspect's real
// ExitCode reaches Result.
func TestExec_RoundTripsAgainstARealFakeDaemon(t *testing.T) {
	var gotCmd []string
	var gotContainerID string

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/exec") {
			http.NotFound(w, r)
			return
		}
		gotContainerID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.43/containers/"), "/exec")
		var req struct {
			Cmd          []string
			AttachStdout bool
			AttachStderr bool
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotCmd = req.Cmd
		if !req.AttachStdout || !req.AttachStderr {
			t.Error("expected AttachStdout and AttachStderr both true")
		}
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
		writeRawFrame(rw.Writer, 1, []byte("hello stdout\n"))
		writeRawFrame(rw.Writer, 2, []byte("hello stderr\n"))
		_ = rw.Flush()
	})
	mux.HandleFunc("/v1.43/exec/execabc123/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"ExitCode": 7})
	})

	socket := fakeDaemon(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := dockerexec.Exec(ctx, socket, "web-1", dockerexec.Options{}, "echo hi")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if gotContainerID != "web-1" {
		t.Errorf("daemon saw container id %q, want %q", gotContainerID, "web-1")
	}
	wantCmd := []string{"/bin/sh", "-c", "echo hi"}
	if len(gotCmd) != len(wantCmd) {
		t.Fatalf("Cmd = %v, want %v", gotCmd, wantCmd)
	}
	for i := range wantCmd {
		if gotCmd[i] != wantCmd[i] {
			t.Errorf("Cmd[%d] = %q, want %q", i, gotCmd[i], wantCmd[i])
		}
	}
	if result.Stdout != "hello stdout\n" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello stdout\n")
	}
	if result.Stderr != "hello stderr\n" {
		t.Errorf("Stderr = %q, want %q", result.Stderr, "hello stderr\n")
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
}

// TestExec_RefusesEmptyOrHostileContainerID proves a bad container id is
// refused before any request is ever sent -- the daemon in this test
// would fail the test itself (via t.Fatal in its handler) if reached.
func TestExec_RefusesEmptyOrHostileContainerID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("daemon should never have been reached for a hostile container id, got %s %s", r.Method, r.URL.Path)
	})
	socket := fakeDaemon(t, mux)

	for _, id := range []string{"", "../create", "x/y"} {
		_, err := dockerexec.Exec(context.Background(), socket, id, dockerexec.Options{}, "cmd")
		if err == nil {
			t.Errorf("Exec with container id %q: expected an error", id)
		}
	}
}

// TestExec_RefusesWindowsNamedPipe proves an npipe: socket address fails
// with a clear, stated-gap error rather than a confusing dial failure.
func TestExec_RefusesWindowsNamedPipe(t *testing.T) {
	_, err := dockerexec.Exec(context.Background(), "npipe:////./pipe/docker_engine", "web-1", dockerexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a Windows named pipe address")
	}
}

// TestExec_DialFailureIsAClearError proves a nonexistent socket path
// fails with a wrapped error, not a panic.
func TestExec_DialFailureIsAClearError(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "no-such-daemon.sock")
	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{DialTimeout: time.Second}, "cmd")
	if err == nil {
		t.Fatal("expected an error dialing a socket nothing is listening on")
	}
}

// TestExec_CreateFailureSurfacesTheDaemonsOwnMessage proves a non-2xx
// response from the create-exec endpoint (a real "no such container", as
// the daemon itself would report) reaches the caller as a clear error.
func TestExec_CreateFailureSurfacesTheDaemonsOwnMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container: web-1"}`))
	})
	socket := fakeDaemon(t, mux)

	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a 404 from the create-exec endpoint")
	}
	if !strings.Contains(err.Error(), "No such container") {
		t.Errorf("error = %v, want it to surface the daemon's own message", err)
	}
}

// TestExec_InspectFailureSurfacesTheDaemonsOwnMessage proves a non-2xx
// response from the inspect endpoint (after a successful create and
// start) also reaches the caller as a clear error, not a zero-value
// success with a silently wrong exit code.
func TestExec_InspectFailureSurfacesTheDaemonsOwnMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
	})
	mux.HandleFunc("/v1.43/exec/execabc123/start", func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\n\r\n")
		_ = rw.Flush()
	})
	mux.HandleFunc("/v1.43/exec/execabc123/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"server error"}`))
	})
	socket := fakeDaemon(t, mux)

	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a 500 from the inspect endpoint")
	}
}

// TestExec_RefusesHostileExecIDFromDaemon proves a malicious or
// malformed exec id in the daemon's own create-exec response (a
// compromised or buggy daemon, or a machine-in-the-middle answering for
// it) is refused before ever being interpolated into the start/inspect
// request paths.
func TestExec_RefusesHostileExecIDFromDaemon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "../create"})
	})
	mux.HandleFunc("/v1.43/exec/", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("should never reach exec/start or exec/json with a hostile exec id, got %s %s", r.Method, r.URL.Path)
	})
	socket := fakeDaemon(t, mux)

	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a hostile exec id returned by the daemon")
	}
}

// TestExec_StartFailureSurfacesTheDaemonsOwnMessage proves a non-2xx
// response from the exec-start endpoint itself (e.g. the daemon refusing
// because the exec instance is already running) reaches the caller as a
// clear error.
func TestExec_StartFailureSurfacesTheDaemonsOwnMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
	})
	mux.HandleFunc("/v1.43/exec/execabc123/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"exec already running"}`))
	})
	socket := fakeDaemon(t, mux)

	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a 409 from the exec-start endpoint")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %v, want it to surface the daemon's own message", err)
	}
}

// TestExec_RefusesOutputExceedingTheCap proves a command streaming
// unbounded output through the real hijacked connection is refused
// outright once it exceeds MaxOutputBytes.
func TestExec_RefusesOutputExceedingTheCap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
	})
	mux.HandleFunc("/v1.43/exec/execabc123/start", func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\n\r\n")
		chunk := make([]byte, 4096)
		for i := 0; i < 100; i++ {
			writeRawFrame(rw.Writer, 1, chunk)
			if err := rw.Flush(); err != nil {
				return
			}
		}
	})
	socket := fakeDaemon(t, mux)

	_, err := dockerexec.Exec(context.Background(), socket, "web-1", dockerexec.Options{MaxOutputBytes: 1024}, "cmd")
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded MaxOutputBytes")
	}
}

// TestExec_ContextCancellationClosesTheConnectionPromptly proves the
// package doc comment's own claim: cancellation closes the underlying
// connection immediately rather than waiting for any timeout. The
// handler holds the hijacked connection open forever without writing a
// terminating EOF; only a canceled context can end the call, and this
// test asserts it does so quickly.
func TestExec_ContextCancellationClosesTheConnectionPromptly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "execabc123"})
	})
	mux.HandleFunc("/v1.43/exec/execabc123/start", func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\n\r\n")
		_ = rw.Flush()
		// Never writes another frame and never closes: only cancellation
		// can end this call.
		<-make(chan struct{})
	})
	socket := fakeDaemon(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := dockerexec.Exec(ctx, socket, "web-1", dockerexec.Options{}, "cmd")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the context is canceled mid-stream")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Exec took %v to return after cancellation, want well under the 10s dial-timeout ceiling this test would hit if the connection were not closed promptly", elapsed)
	}
}
