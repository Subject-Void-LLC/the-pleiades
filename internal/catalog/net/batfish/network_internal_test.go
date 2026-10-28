// Tests of the network parameter: the Batfish network a task makes is named
// after it, reported, and the one the task deletes, so labs sharing one
// Batfish service never touch each other's networks.
package batfish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	bfdevice "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// networkContext records the stats a task sets.
type networkContext struct{ stats map[string]any }

func (c *networkContext) InjectSecrets() map[string]string { return nil }
func (c *networkContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *networkContext) EmitFact(string, any) error       { return nil }

// fakeBatfish answers network creation and deletion, recording each name,
// and refuses every snapshot upload, so a task stops right after it has
// made its network and must still delete it.
type fakeBatfish struct {
	mu      sync.Mutex
	created []string
	deleted []string
}

// ServeHTTP implements http.Handler.
func (f *fakeBatfish) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2/networks":
		f.created = append(f.created, r.URL.Query().Get("name"))
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v2/networks/"):
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/v2/networks/"))
	default:
		http.Error(w, "refused by the test", http.StatusInternalServerError)
	}
}

// TestCheck_NamesTheNetworkAfterTheParameter: the network made begins with
// network and a hyphen, is reported as the network stat, and is the one
// deleted; two tasks given the same name still get different networks;
// with no network the name begins with pleiades, as before.
func TestCheck_NamesTheNetworkAfterTheParameter(t *testing.T) {
	fake := &fakeBatfish{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	svc, err := bfdevice.NewService(record.Record{Name: "batfish", Type: bfdevice.TypeService, Properties: map[string]inventory.PropertyValue{
		bfdevice.APIBaseProperty: srv.URL,
	}})
	if err != nil {
		t.Fatal(err)
	}
	run := func(network any) string {
		t.Helper()
		params := map[string]any{
			"devices":    []any{map[string]any{"name": "r1", "config": "hostname r1\nrouter ospf\nexit\n"}},
			"assertions": []any{map[string]any{"ospf_established": true}},
		}
		if network != nil {
			params[paramNetwork] = network
		}
		rc := &networkContext{stats: map[string]any{}}
		if _, err := Check(context.Background(), rc, svc, params); err == nil {
			t.Fatal("Check() error = nil, want the refused upload")
		}
		name, _ := rc.stats[paramNetwork].(string)
		return name
	}

	first, second, plain := run("pleiades-cvd-opus"), run("pleiades-cvd-opus"), run(nil)
	for _, n := range []string{first, second} {
		if !strings.HasPrefix(n, "pleiades-cvd-opus-") || len(n) != len("pleiades-cvd-opus-")+16 {
			t.Errorf("network stat = %q, want pleiades-cvd-opus- and 16 hex digits", n)
		}
	}
	if first == second {
		t.Errorf("two tasks given one name made the same network %q", first)
	}
	if !strings.HasPrefix(plain, "pleiades-") || len(plain) != len("pleiades-")+16 {
		t.Errorf("with no network the stat = %q, want the default pleiades- name", plain)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	want := []string{first, second, plain}
	if strings.Join(fake.created, ",") != strings.Join(want, ",") || strings.Join(fake.deleted, ",") != strings.Join(want, ",") {
		t.Errorf("created %q and deleted %q, want each task to make and delete only its own %q", fake.created, fake.deleted, want)
	}
}

// TestReadNetworkPrefix: the name is bounded and safe, and refused before
// Batfish is reached otherwise.
func TestReadNetworkPrefix(t *testing.T) {
	for _, ok := range []any{nil, "", "  ", "lab_a", "pleiades-cvd-haiku", "a.b-c_d9", strings.Repeat("a", 64)} {
		params := map[string]any{}
		if ok != nil {
			params[paramNetwork] = ok
		}
		if _, err := readNetworkPrefix(params); err != nil {
			t.Errorf("network %q was refused: %v", ok, err)
		}
	}
	for _, bad := range []any{"-lab", ".lab", "lab a", "lab/a", "../a", "lab%2f", "lab\x00", "läb", strings.Repeat("a", 65), 7, []any{"lab"}} {
		if _, err := readNetworkPrefix(map[string]any{paramNetwork: bad}); err == nil {
			t.Errorf("network %q was accepted", bad)
		}
	}
	if got, _ := readNetworkPrefix(map[string]any{paramNetwork: " lab-a "}); got != "lab-a" {
		t.Errorf("network \" lab-a \" read as %q, want lab-a", got)
	}
}
