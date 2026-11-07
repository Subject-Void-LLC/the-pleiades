// A read that fails after a container's state changed is a warning, not a
// failure: driven through the real engine, the journal keeps the change
// and its undo, so a rollback can still undo it (FAILURE_PATTERNS 563's
// rule, which settle's read had broken).
package container_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// inspectPath is an engine API path that inspects one container.
var inspectPath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/[^/]+/json$`)

// stopPath is an engine API path that stops one container.
var stopPath = regexp.MustCompile(`^(/v[0-9.]+)?/containers/[^/]+/stop$`)

// faultyEngine is a socket in front of this machine's Docker engine that
// passes every request through, until a stop has succeeded: from then on
// it answers every inspect with a server error, as an engine whose
// connection dropped right after the stop would.
type faultyEngine struct {
	socket  capability.SocketAddress
	stopped atomic.Bool
	refused atomic.Int32
}

// newFaultyEngine starts the socket, closed when the test ends.
func newFaultyEngine(t *testing.T) *faultyEngine {
	t.Helper()
	// A short directory of its own: a Unix socket's path is limited to
	// about a hundred bytes, which t.TempDir's can pass.
	dir, err := os.MkdirTemp("", "plsettle")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &faultyEngine{socket: capability.SocketAddress(filepath.Join(dir, "docker.sock"))}
	ln, err := net.Listen("unix", string(f.socket))
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(&url.URL{Scheme: "http", Host: "docker"}) },
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", dockerSocket)
		}},
		// The flag is set before the stop's answer reaches the method, so
		// every inspect after it is refused.
		ModifyResponse: func(res *http.Response) error {
			if res.Request.Method == http.MethodPost && stopPath.MatchString(res.Request.URL.Path) && res.StatusCode < 300 {
				f.stopped.Store(true)
			}
			return nil
		},
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && inspectPath.MatchString(r.URL.Path) && f.stopped.Load() {
			f.refused.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"connection to the engine dropped"}`)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	var served sync.WaitGroup
	served.Add(1)
	go func() {
		defer served.Done()
		_ = srv.Serve(ln) // ends with ErrServerClosed at cleanup
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		served.Wait()
	})
	return f
}

// mapJournal keeps every journal entry the run writes.
type mapJournal struct {
	mu      sync.Mutex
	entries []engine.JournalEntry
}

// Record keeps entries.
func (j *mapJournal) Record(_ context.Context, entries []engine.JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, entries...)
	return nil
}

// runThroughEngine runs runbook against device with the real executor and
// the real Collection dispatcher, journaling into the journal it returns.
func runThroughEngine(t *testing.T, device inventory.InventoryItem, runbook string) (engine.RunResult, *mapJournal) {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook))
	if err != nil {
		t.Fatalf("building the runbook: %v", err)
	}
	journal := &mapJournal{}
	resolver := engine.TargetResolverFunc(func(string) []inventory.InventoryItem { return []inventory.InventoryItem{device} })
	actions := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext)
	x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithJournal(journal), engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, journal
}

// TestStop_AFailedReadAfterTheStopKeepsTheUndoInTheJournal stops a real
// container through the engine while the engine refuses every inspect
// once the stop is made. The stop happened, so the task succeeds with a
// warning naming the failed read, and the journal holds it as a change
// with container.start as its undo, held exactly. Before the fix the
// method returned the read's error, the engine journaled a task that
// failed during its action and dropped its undo, and a rollback refused
// it as an unknown effect.
func TestStop_AFailedReadAfterTheStopKeepsTheUndoInTheJournal(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx}); err != nil {
		t.Fatal(err)
	}
	faulty := newFaultyEngine(t)
	base := h.device.(*engineDevice)
	device := &engineDevice{Stub: base.Stub, id: base.id, user: base.user, socket: faulty.socket}

	result, journal := runThroughEngine(t, device, `id: settle
hosts: engine-host
tasks:
  - name: stop it
    container.stop:
      name: `+name+`
      timeout: 1
`)

	if !faulty.stopped.Load() || faulty.refused.Load() == 0 {
		t.Fatalf("the fault never fired: stopped %v, inspects refused %d", faulty.stopped.Load(), faulty.refused.Load())
	}
	if state := h.state(t, name); state != "exited" {
		t.Fatalf("the container is %q, want exited: the stop did not happen", state)
	}
	var found bool
	for _, e := range journal.entries {
		if e.NodeID != "tasks[0]" {
			continue
		}
		found = true
		if e.Outcome == engine.OutcomeFailed || !e.ActionChanged || e.InverseFQCN != "container.start" || !e.InverseComplete {
			t.Errorf("journaled outcome %q, changed %v, undo %q, complete %v; want a change with container.start held exactly",
				e.Outcome, e.ActionChanged, e.InverseFQCN, e.InverseComplete)
		}
	}
	if !found {
		t.Errorf("no journal entry for tasks[0] in %+v", journal.entries)
	}
	var node *engine.NodeResult
	for i := range result.Nodes {
		if result.Nodes[i].NodeID == "tasks[0]" {
			node = &result.Nodes[i]
		}
	}
	if node == nil {
		t.Fatalf("no result for tasks[0] in %+v", result.Nodes)
	}
	if node.Err != nil {
		t.Errorf("the stop failed: %v", node.Err)
	}
	warnings, _ := node.Stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "reading it back afterwards failed") {
		t.Errorf("warnings = %q, want the failed read named", warnings)
	}
}
