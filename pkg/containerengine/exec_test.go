// Tests for Session.Exec against an in-memory engine that answers the way a
// real one does.
package containerengine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// versionedPath is the API version a client puts in front of a path.
var versionedPath = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// execEngine answers the requests one Exec sends, the way a real engine
// does: a ping, the create, the start as 101 followed by the stream, and the
// inspection. Each field is what the engine says at that step.
type execEngine struct {
	createStatus string // the create's status line; empty means 201 with execID
	createBody   string
	execID       string
	media        string // the start's Content-Type
	stream       []byte // what the start sends after its headers
	exitCode     int

	started atomic.Bool
}

func (e *execEngine) answer(req *http.Request) string {
	path := versionedPath.ReplaceAllString(req.URL.Path, "")
	switch {
	case path == "/_ping":
		return "HTTP/1.1 200 OK\r\nAPI-Version: 1.44\r\nContent-Length: 2\r\n\r\nOK"
	case strings.HasSuffix(path, "/exec") && strings.HasPrefix(path, "/containers/"):
		if e.createStatus != "" {
			return fmt.Sprintf("%s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", e.createStatus, len(e.createBody), e.createBody)
		}
		body := fmt.Sprintf(`{"Id":%q}`, e.execID)
		return fmt.Sprintf("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	case strings.HasSuffix(path, "/start"):
		e.started.Store(true)
		return "HTTP/1.1 101 UPGRADED\r\nContent-Type: " + e.media + "\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n" + string(e.stream)
	case strings.HasSuffix(path, "/json"):
		body := fmt.Sprintf(`{"ExitCode":%d,"Running":false}`, e.exitCode)
		return fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	}
	return "HTTP/1.1 418 Unexpected\r\nContent-Length: 0\r\n\r\n"
}

// execSession opens a session on dial allowing exactly what Exec sends.
func execSession(t *testing.T, dial Dialer) *Session {
	t.Helper()
	api, err := newAPI(guard(dial, ExecOps))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })
	return &Session{API: api}
}

// frames builds a framed stream from (stream, payload) pairs.
func frames(parts ...any) []byte {
	var buf bytes.Buffer
	for i := 0; i+1 < len(parts); i += 2 {
		writeFrame(&buf, parts[i].(byte), []byte(parts[i+1].(string)))
	}
	return buf.Bytes()
}

// TestExec_ReportsOutputAndTheRealExitCode is the ordinary run: both
// streams kept apart, and the exit code the engine's inspection reports.
func TestExec_ReportsOutputAndTheRealExitCode(t *testing.T) {
	e := &execEngine{execID: "exec1", media: "application/vnd.docker.multiplexed-stream", stream: frames(byte(1), "hello\n", byte(2), "warn\n", byte(1), "bye\n"), exitCode: 3}
	res, err := execSession(t, (&pipeEngine{answer: e.answer}).dial).Exec(context.Background(), "web", "echo hello", ExecOptions{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "hello\nbye\n" || res.Stderr != "warn\n" || res.ExitCode != 3 {
		t.Errorf("Exec = %+v, want both streams and exit 3", res)
	}
}

// TestExec_ReadsARawStreamAsOne proves a stream the engine says it did not
// frame is read as output, not parsed as frames it does not contain.
func TestExec_ReadsARawStreamAsOne(t *testing.T) {
	e := &execEngine{execID: "exec1", media: rawStream, stream: []byte("plain output\n")}
	res, err := execSession(t, (&pipeEngine{answer: e.answer}).dial).Exec(context.Background(), "web", "id", ExecOptions{})
	if err != nil || res.Stdout != "plain output\n" {
		t.Fatalf("Exec = %+v, %v; want the raw stream as output", res, err)
	}
}

// TestExec_SurfacesTheEnginesRefusal proves the engine's own reason for
// refusing the create reaches the caller.
func TestExec_SurfacesTheEnginesRefusal(t *testing.T) {
	e := &execEngine{createStatus: "HTTP/1.1 404 Not Found", createBody: `{"message":"No such container: web"}`}
	_, err := execSession(t, (&pipeEngine{answer: e.answer}).dial).Exec(context.Background(), "web", "id", ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "No such container: web") {
		t.Fatalf("Exec = %v, want the engine's own message", err)
	}
}

// TestExec_RefusesAHostileExecIDFromTheEngine proves an exec ID the engine
// answers is held to a name's shape before it becomes the next request's
// path, so no start is ever sent for it.
func TestExec_RefusesAHostileExecIDFromTheEngine(t *testing.T) {
	e := &execEngine{execID: "../../containers/create"}
	_, err := execSession(t, (&pipeEngine{answer: e.answer}).dial).Exec(context.Background(), "web", "id", ExecOptions{})
	if err == nil || !strings.Contains(err.Error(), "unusable exec ID") {
		t.Fatalf("Exec = %v, want the exec ID refused", err)
	}
	if e.started.Load() {
		t.Error("a start was sent for an exec ID that could reshape its path")
	}
}

// TestExec_RefusesOutputPastItsBound proves output past MaxOutputBytes is an
// error rather than truncated output a caller would take as complete.
func TestExec_RefusesOutputPastItsBound(t *testing.T) {
	e := &execEngine{execID: "exec1", media: "application/vnd.docker.multiplexed-stream", stream: frames(byte(1), strings.Repeat("x", 100))}
	_, err := execSession(t, (&pipeEngine{answer: e.answer}).dial).Exec(context.Background(), "web", "yes", ExecOptions{MaxOutputBytes: 10})
	if err == nil || !strings.Contains(err.Error(), "output exceeded 10 bytes") {
		t.Fatalf("Exec = %v, want the output bound refused", err)
	}
}

// TestExec_CancelClosesTheStream proves cancelling a run whose command
// never stops writing returns promptly, by closing the stream, rather than
// when the command ends.
func TestExec_CancelClosesTheStream(t *testing.T) {
	e := &execEngine{execID: "exec1"}
	held := func(ctx context.Context) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			req, err := http.ReadRequest(bufio.NewReader(s))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			if !strings.HasSuffix(req.URL.Path, "/start") {
				_, _ = s.Write([]byte(e.answer(req)))
				return
			}
			_, _ = s.Write([]byte("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"))
			for {
				if _, err := s.Write(frames(byte(1), "tick\n")); err != nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		return c, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	begun := time.Now()
	_, err := execSession(t, held).Exec(ctx, "web", "yes", ExecOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec = %v, want the cancellation", err)
	}
	if took := time.Since(begun); took > 5*time.Second {
		t.Errorf("Exec returned %s after a cancel, want promptly", took)
	}
}

// FuzzDemux proves the demultiplexer never panics or hangs on any stream an
// engine sends, and never keeps more than its bound: an engine's answer is
// untrusted, and a frame's length is read straight off the wire.
func FuzzDemux(f *testing.F) {
	f.Add(frames(byte(1), "out", byte(2), "err"), uint16(1024))
	f.Add([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}, uint16(16))
	f.Add([]byte{2, 0, 0, 0, 0, 0, 0, 3, 'a'}, uint16(16))
	f.Add([]byte{}, uint16(0))
	f.Fuzz(func(t *testing.T, stream []byte, limit uint16) {
		stdout, stderr, err := demux(bytes.NewReader(stream), int(limit))
		if err == nil && len(stdout)+len(stderr) > int(limit) {
			t.Fatalf("kept %d bytes past a %d-byte bound", len(stdout)+len(stderr), limit)
		}
	})
}
