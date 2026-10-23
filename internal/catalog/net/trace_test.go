// Tests that net.trace reads tracepath's answer, holds the path to what
// was expected, and runs against a real router.
package net

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestParseTracepath reads hops once each, in order, a hop that did not
// answer kept in its place, and whether the destination answered.
func TestParseTracepath(t *testing.T) {
	reached := ` 1?: [LOCALHOST]                      pmtu 9500
 1:  10.0.13.1                                             0.168ms
 1:  10.0.13.1                                             0.007ms
 2:  10.3.3.10                                             0.129ms reached
     Resume: pmtu 9500 hops 2 back 2
`
	hops, ok := parseTracepath(reached)
	if !ok || strings.Join(hops, " ") != "10.0.13.1 10.3.3.10" {
		t.Errorf("got %v %v", hops, ok)
	}
	lost := ` 1?: [LOCALHOST]                      pmtu 1500
 1:  172.20.30.1                                           0.206ms
 2:  no reply
 3:  no reply
     Too many hops: pmtu 1500
`
	hops, ok = parseTracepath(lost)
	if ok || strings.Join(hops, " ") != "172.20.30.1 * *" {
		t.Errorf("got %v %v", hops, ok)
	}
}

// TestTraceExpect checks reached, includes and avoids, and refuses an
// expectation it does not know.
func TestTraceExpect(t *testing.T) {
	e, err := traceExpectParam(map[string]any{"expect": map[string]any{"reached": true, "includes": []any{"10.0.0.1"}, "avoids": []any{"10.0.0.9"}}})
	if err != nil {
		t.Fatal(err)
	}
	if p := e.check([]string{"10.0.0.1", "10.0.0.5"}, true); len(p) != 0 {
		t.Errorf("a path as expected: %v", p)
	}
	if p := e.check([]string{"10.0.0.9"}, false); len(p) != 3 {
		t.Errorf("a path wrong three ways: %v", p)
	}
	for _, bad := range []map[string]any{
		{"expect": "reached"},
		{"expect": map[string]any{"reachd": true}},
		{"expect": map[string]any{"reached": "yes"}},
		{"expect": map[string]any{"includes": []any{"r2"}}},
	} {
		if _, err := traceExpectParam(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

// statContext is a RunbookContext holding a credential and the stats set.
type statContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *statContext) InjectSecrets() map[string]string { return c.secrets }
func (c *statContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *statContext) EmitFact(string, any) error       { return nil }

// TestTrace_OnARealRouter: r1 reaches r2 across their link in one hop, a
// path avoiding r2 fails, an address nobody holds is not reached, and a
// destination that could be read as an option never reaches tracepath.
func TestTrace_OnARealRouter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	link := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.204.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.204.0.2"), "r2": netip.MustParseAddr("10.204.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2"}, nil, []testsupport.NetLabLink{link})
	lab.TrustHostKeys(t)
	r1 := lab.Nodes["r1"].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameLinux)
	rc := func() *statContext { return &statContext{secrets: lab.Secrets(), stats: map[string]any{}} }
	ctx := context.Background()

	got := rc()
	if _, err := Trace(ctx, got, r1, map[string]any{"dest": "10.204.0.3", "expect": map[string]any{"reached": true, "includes": []any{"10.204.0.3"}}}); err != nil {
		t.Fatal(err)
	}
	if hops, _ := got.stats["hops"].([]string); len(hops) != 1 || hops[0] != "10.204.0.3" || got.stats["reached"] != true {
		t.Errorf("stats = %v, want one hop to 10.204.0.3, reached", got.stats)
	}
	if _, err := Trace(ctx, rc(), r1, map[string]any{"dest": "10.204.0.3", "expect": map[string]any{"avoids": []any{"10.204.0.3"}}}); err == nil || !strings.Contains(err.Error(), "is on the path") {
		t.Errorf("a path through an address to avoid: err %v", err)
	}
	lost := rc()
	if _, err := Trace(ctx, lost, r1, map[string]any{"dest": "10.204.0.6", "max_hops": 2, "expect": map[string]any{"reached": true}}); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("an address nobody holds: err %v", err)
	}
	if lost.stats["reached"] != false {
		t.Errorf("reached = %v for an address nobody holds", lost.stats["reached"])
	}
	for _, d := range []string{"-V", "10.204.0.3; reboot", ""} {
		if _, err := Trace(ctx, rc(), r1, map[string]any{"dest": d}); err == nil {
			t.Errorf("dest %q was accepted", d)
		}
	}
}

// TestTrace_FromAContainerRefusesBeforeTheEngine: a container name no
// engine allows is refused before any engine is reached, and a device that
// is not a container host is refused by the engine session, naming the
// method. The trace itself, through a real engine and router, is
// cmd/pleiades's TestCLI_NetTraceFromAContainerThroughARouter.
func TestTrace_FromAContainerRefusesBeforeTheEngine(t *testing.T) {
	rc := &statContext{stats: map[string]any{}}
	if _, err := Trace(context.Background(), rc, nil, map[string]any{"dest": "10.0.0.1", "container": "h1; reboot"}); err == nil || !strings.Contains(err.Error(), "no engine allows") {
		t.Errorf("a bad container name: %v", err)
	}
	if _, err := Trace(context.Background(), rc, nil, map[string]any{"dest": "10.0.0.1", "container": "h1"}); err == nil || !strings.HasPrefix(err.Error(), traceFQCN) {
		t.Errorf("no container host: %v", err)
	}
	if _, err := Trace(context.Background(), rc, nil, map[string]any{"dest": "10.0.0.1", "container": 7}); err == nil || !strings.HasPrefix(err.Error(), traceFQCN) {
		t.Errorf("a container that is not text: %v", err)
	}
}
