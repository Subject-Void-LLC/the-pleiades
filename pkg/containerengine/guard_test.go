// Tests for the guard's decisions and its answer bounds.
package containerengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// fakeEngine serves mux on a Unix socket in a short directory and records
// every request line that reached it.
type fakeEngine struct {
	socket string
	mu     sync.Mutex
	seen   []string
}

func startFakeEngine(t *testing.T, mux *http.ServeMux) *fakeEngine {
	t.Helper()
	dir, err := os.MkdirTemp("", "ce")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeEngine{socket: filepath.Join(dir, "e.sock")}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func (f *fakeEngine) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// engineMux answers the ping the client negotiates with and a container
// list.
func engineMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Api-Version", "1.44")
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/v1.44/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "abc", "Names": []string{"/web"}}})
	})
	return mux
}

// TestGuard_AnUndeclaredOperationNeverReachesTheEngine proves the central
// claim: a session opened without OpContainerList cannot list containers,
// and the engine sees no such request, only the version negotiation every
// session may make.
func TestGuard_AnUndeclaredOperationNeverReachesTheEngine(t *testing.T) {
	f := startFakeEngine(t, engineMux())
	api, err := newAPI(guard(Local(capSocket(f.socket)), []Op{OpExecCreate}))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	_, err = api.ContainerList(context.Background(), client.ContainerListOptions{})
	if err == nil || !errors.Is(err, errRefused) && !strings.Contains(err.Error(), "refused by the session's declared operations") {
		t.Fatalf("ContainerList on an exec-only session: %v, want the guard's refusal", err)
	}
	for _, line := range f.requests() {
		if strings.Contains(line, "/containers/json") {
			t.Errorf("the engine received %q", line)
		}
	}
}

// TestGuard_ADeclaredOperationReachesTheEngine is the control: the same
// call on a session that declared it is answered.
func TestGuard_ADeclaredOperationReachesTheEngine(t *testing.T) {
	f := startFakeEngine(t, engineMux())
	api, err := newAPI(guard(Local(capSocket(f.socket)), []Op{OpContainerList}))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	list, err := api.ContainerList(context.Background(), client.ContainerListOptions{})
	if err != nil {
		t.Fatalf("ContainerList: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "abc" {
		t.Errorf("list = %+v", list)
	}
}

// TestAllowed_Routes covers the table's matching rules: the version prefix
// is ignored, a query decides between operations sharing a path, and an
// identifier cannot add a segment, encoded or not.
func TestAllowed_Routes(t *testing.T) {
	for _, tc := range []struct {
		ops            []Op
		method, target string
		ok             bool
	}{
		{nil, "GET", "/_ping", true},
		{nil, "HEAD", "/v1.44/_ping", true},
		{[]Op{OpContainerInspect}, "GET", "/v1.44/containers/web/json", true},
		{[]Op{OpContainerInspect}, "GET", "/v5.8.7/containers/web/json", true},
		{[]Op{OpContainerInspect}, "GET", "/v1.44/containers/web/x/json", false},
		{[]Op{OpContainerInspect}, "GET", "/v1.44/containers/web%2Fx/json", false},
		{[]Op{OpContainerInspect}, "GET", "/v1.44/containers/web%2F..%2Fcreate", false},
		{[]Op{OpExecCreate}, "POST", `/v0.0/containers/"0%2Fexec`, false},
		{[]Op{OpExecCreate}, "POST", "/v1.44/containers/../create/exec", false},
		{[]Op{OpExecCreate}, "POST", "/v1.44/containers/./web/exec", false},
		{[]Op{OpExecCreate}, "POST", "/v1.44/containers/%77eb/exec", false},
		{[]Op{OpExecCreate}, "POST", "/v1.44/containers/%2e%2e/exec", false},
		{[]Op{OpExecCreate}, "POST", "/v1.44/containers/web%zz/exec", false},
		{[]Op{OpImageInspect}, "GET", "/v1.44/images/a%3Ab/json", true},
		{[]Op{OpImageInspect}, "GET", "/v1.44/images/ghcr.io/org/app:1/json", true},
		{[]Op{OpImagePull}, "POST", "/v1.44/images/create?fromImage=alpine&tag=3", true},
		{[]Op{OpImagePull}, "POST", "/v1.44/images/create?fromSrc=-", false},
		{[]Op{OpImageImport}, "POST", "/v1.44/images/create?fromSrc=-", true},
		{[]Op{OpImageImport}, "POST", "/v1.44/images/create?fromSrc=-&fromImage=x", false},
		{[]Op{OpExecStart}, "POST", "/v1.44/exec/abc/start", true},
		{[]Op{OpExecStart}, "POST", "/v1.44/containers/create", false},
		{[]Op{OpLibpodInfo}, "GET", "/v5.0.0/libpod/info", true},
		{[]Op{OpInfo}, "GET", "http://engine/info", false},
		{[]Op{OpInfo}, "GET", "/info", true},
	} {
		_, err := allowed(tc.ops, tc.method, tc.target)
		if (err == nil) != tc.ok {
			t.Errorf("allowed(%v, %s %s) = %v, want ok=%v", tc.ops, tc.method, tc.target, err, tc.ok)
		}
	}
}

// TestGuard_ALongOrMalformedRequestLineIsRefused proves the guard buffers
// no more than a request line's bound, and refuses a line it cannot read.
func TestGuard_ALongOrMalformedRequestLineIsRefused(t *testing.T) {
	for name, payload := range map[string]string{
		"too long":  "GET /" + strings.Repeat("a", maxRequestLine+10),
		"malformed": "GET\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			g := &guardedConn{Conn: client, ops: []Op{OpInfo}}
			if _, err := g.Write([]byte(payload)); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

// TestKnownOp proves every operation in the table is known and a stray
// name is not, so Open refuses a misspelled declaration at once.
func TestKnownOp(t *testing.T) {
	for op := range routes {
		if !KnownOp(op) {
			t.Errorf("%s is in the table and not known", op)
		}
	}
	if KnownOp("container.everything") {
		t.Error("a name outside the table is known")
	}
}

// TestPodmanID proves the store digest changes with each part and cannot be
// made by moving characters between parts.
func TestPodmanID(t *testing.T) {
	a := podmanID("h", "/var/lib/containers/storage", "/run/containers/storage")
	if a != podmanID("h", "/var/lib/containers/storage", "/run/containers/storage") {
		t.Error("the digest is not stable")
	}
	for _, other := range []capabilityEngineIDArgs{
		{"h2", "/var/lib/containers/storage", "/run/containers/storage"},
		{"h", "/home/u/.local/share/containers/storage", "/run/containers/storage"},
		{"h/", "var/lib/containers/storage", "/run/containers/storage"},
	} {
		if podmanID(other.host, other.graph, other.run) == a {
			t.Errorf("%+v digests like the original", other)
		}
	}
}

type capabilityEngineIDArgs struct{ host, graph, run string }

// capSocket is a test's socket path as the capability type Local takes.
func capSocket(path string) capability.SocketAddress { return capability.SocketAddress(path) }

// floodEngine answers every request with a status line and headers, then
// streams body bytes until the client stops reading, which is what an engine
// that does not stop looks like from the client's side.
func floodEngine(status string) Dialer {
	return func(context.Context) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close()
			req, err := http.ReadRequest(bufio.NewReader(s))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			if _, err := s.Write([]byte(status + "\r\n\r\n")); err != nil {
				return
			}
			chunk := bytes.Repeat([]byte("x"), 32<<10)
			for {
				if _, err := s.Write(chunk); err != nil {
					return
				}
			}
		}()
		return c, nil
	}
}

// TestGuard_CutsOffAnAnswerPastItsBound proves an answer longer than its
// operation's bound ends with the guard's error at the bound, rather than
// being read for as long as the engine sends. The ping's 64 KiB bound
// stands in for every bounded operation; the mechanism is the same.
func TestGuard_CutsOffAnAnswerPastItsBound(t *testing.T) {
	conn, err := guard(floodEngine("HTTP/1.1 200 OK\r\nConnection: close"), nil)(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /_ping HTTP/1.1\r\nHost: docker\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	n, err := io.Copy(io.Discard, conn)
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("reading a flood ended with %v after %d bytes, want the guard's bound", err, n)
	}
	if n > maxPingAnswer {
		t.Errorf("read %d bytes, past the %d-byte bound", n, maxPingAnswer)
	}
}

// TestGuard_LeavesACallerBoundedStreamAlone proves a stream whose size is
// the content (an exec's output here) is not cut off by the guard: the
// method reading it bounds what it keeps.
func TestGuard_LeavesACallerBoundedStreamAlone(t *testing.T) {
	conn, err := guard(floodEngine("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp"), ExecOps)(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("POST /v1.44/exec/abc/start HTTP/1.1\r\nHost: docker\r\nContent-Length: 2\r\n\r\n{}")); err != nil {
		t.Fatalf("write: %v", err)
	}
	const past = 4 * maxPingAnswer
	n, err := io.CopyN(io.Discard, conn, past)
	if err != nil || n != past {
		t.Fatalf("read %d bytes of a caller-bounded stream, then %v; want %d and no error", n, err, past)
	}
}

// TestBoundFor holds the classification: a document by default, progress
// streams larger, content streams left to their caller.
func TestBoundFor(t *testing.T) {
	for op, want := range map[Op]int64{
		OpInfo:             maxDocumentAnswer,
		OpContainerInspect: maxDocumentAnswer,
		OpContainerList:    maxDocumentAnswer,
		OpImagePull:        maxProgressAnswer,
		OpImageBuild:       maxProgressAnswer,
		OpExecStart:        unbounded,
		OpContainerLogs:    unbounded,
		OpImageSave:        unbounded,
		opPing:             maxPingAnswer,
	} {
		if got := boundFor(op); got != want {
			t.Errorf("boundFor(%s) = %d, want %d", op, got, want)
		}
	}
}
