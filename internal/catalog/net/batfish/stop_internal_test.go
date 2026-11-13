// Failure-mode walk of a Batfish service that stops partway through a gate
// (Phase 127b's Adversarial Pattern Justification): the task fails closed,
// never reports the candidate as passed, and still deletes the network it
// made on the shared service.
package batfish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bfdevice "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// stoppingBatfish makes a network and takes the snapshot, then stops
// answering the gate's questions the way how says: "drop" closes each
// connection without a reply, as a process that died does, and "hang" holds
// it open with no reply, as one wedged under load does. Deleting a network
// is still answered, and recorded, so a test sees whether the task cleaned
// up.
type stoppingBatfish struct {
	how     string
	mu      sync.Mutex
	deleted []string
	asked   int
	release chan struct{}
}

// ServeHTTP implements http.Handler.
func (f *stoppingBatfish) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2/networks":
		return
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v2/networks/"):
		f.mu.Lock()
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/v2/networks/"))
		f.mu.Unlock()
		return
	case strings.Contains(r.URL.Path, "/snapshots/") && !strings.Contains(r.URL.Path, ":fork"):
		return
	}
	f.mu.Lock()
	f.asked++
	f.mu.Unlock()
	if f.how == "hang" {
		select {
		case <-r.Context().Done():
		case <-f.release:
		}
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

// TestCheck_BatfishStoppingMidGateFailsClosed: with the service gone after
// the snapshot is taken, by a dropped connection or a hang the run's
// deadline ends, the task returns an error, records no passed result, and
// deletes the network it made.
func TestCheck_BatfishStoppingMidGateFailsClosed(t *testing.T) {
	for _, how := range []string{"drop", "hang"} {
		t.Run(how, func(t *testing.T) {
			fake := &stoppingBatfish{how: how, release: make(chan struct{})}
			srv := httptest.NewServer(fake)
			t.Cleanup(func() {
				close(fake.release)
				srv.Close()
			})
			svc, err := bfdevice.NewService(record.Record{Name: "batfish", Type: bfdevice.TypeService, Properties: map[string]inventory.PropertyValue{
				bfdevice.APIBaseProperty: srv.URL,
			}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			rc := &networkContext{stats: map[string]any{}}
			start := time.Now()
			_, err = Check(ctx, rc, svc, map[string]any{
				"devices":    []any{map[string]any{"name": "r1", "config": "hostname r1\nrouter ospf\nexit\n"}},
				"assertions": []any{map[string]any{"ospf_established": true}},
			})
			if err == nil {
				t.Fatal("Check() error = nil with the service gone mid-gate")
			}
			if took := time.Since(start); took > 20*time.Second {
				t.Errorf("Check() took %s to give up", took)
			}
			if passed, ok := rc.stats["passed"].(bool); ok && passed {
				t.Error("a gate the service never answered recorded passed")
			}
			network, _ := rc.stats[paramNetwork].(string)
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.asked == 0 {
				t.Fatal("the gate never reached a question, so the service never stopped mid-gate")
			}
			if network == "" || strings.Join(fake.deleted, ",") != network {
				t.Errorf("deleted %q, want the network the task made, %q", fake.deleted, network)
			}
		})
	}
}
