// Tests that net.trace reads tracepath's answer, holds the path to what
// was expected, and runs against a real router.
package net

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
	p := parseTracepath(reached)
	if !p.reached || strings.Join(p.addresses(), " ") != "10.0.13.1 10.3.3.10" || p.state() != pathReached {
		t.Errorf("got %+v", p)
	}
	if out, back, ok := p.returnCounts(); !ok || out != 2 || back != 2 {
		t.Errorf("return counts %d %d %v, want 2 2", out, back, ok)
	}
	lost := ` 1?: [LOCALHOST]                      pmtu 1500
 1:  172.20.30.1                                           0.206ms
 2:  no reply
 3:  no reply
     Too many hops: pmtu 1500
     Resume: pmtu 1500
`
	p = parseTracepath(lost)
	if p.reached || strings.Join(p.addresses(), " ") != "172.20.30.1 * *" || p.state() != pathUnreached {
		t.Errorf("got %+v", p)
	}
	if _, _, ok := p.returnCounts(); ok {
		t.Error("a trace that did not reach its destination has return counts")
	}
}

// labAsymmetricTrace is tracepath's answer for the CML lab's asymmetric
// path on 2026-11-17 (Host1 to Host2 through C8K-A and C8K-B, the reply
// straight back from C8K-B), as its lines read ("2: ... asymm 1" and
// "Resume: pmtu 1500 hops 3 back 2"), the lab's addresses replaced by
// documentation ones. tracepath ends most lines with a space, which a raw
// string would not keep visibly.
const labAsymmetricTrace = " 1?: [LOCALHOST]                      pmtu 1500\n" +
	" 1:  192.0.2.1                                             0.532ms \n" +
	" 1:  192.0.2.1                                             0.389ms \n" +
	" 2:  198.51.100.2                                          0.868ms asymm  1 \n" +
	" 3:  203.0.113.10                                          1.061ms reached\n" +
	"     Resume: pmtu 1500 hops 3 back 2 \n"

// TestParseTracepath_KeepsAsymmetry reads tracepath's asymm and back
// columns, which net.trace used to drop: the second hop's reply came back
// over one hop, and the destination's over two after three out.
func TestParseTracepath_KeepsAsymmetry(t *testing.T) {
	p := parseTracepath(labAsymmetricTrace)
	details := hopMaps(p.hopDetails())
	// Hop 2's from its asymm column, the destination's from the Resume
	// line's back, since tracepath prints no asymm on the reached line.
	if len(details) != 3 || details[1]["return_hops"] != 1 || details[0]["return_hops"] != nil || details[2]["return_hops"] != 2 {
		t.Fatalf("hop details %v, want return_hops 1 on hop 2 and 2 on hop 3", details)
	}
	if details[1]["hop"] != 2 || details[1]["address"] != "198.51.100.2" {
		t.Errorf("hop 2 is %v", details[1])
	}
	out, back, ok := p.returnCounts()
	if !ok || out != 3 || back != 2 {
		t.Errorf("return counts %d %d %v, want 3 out and 2 back", out, back, ok)
	}
	rc := &statContext{stats: map[string]any{}}
	if err := setTraceStats(rc, p); err != nil {
		t.Fatal(err)
	}
	if rc.stats["asymmetric"] != true || rc.stats["return_hops"] != 2 || rc.stats["path"] != pathReached {
		t.Errorf("stats %v, want an asymmetric path reached, 2 hops back", rc.stats)
	}
	sym := parseTracepath(strings.Replace(strings.Replace(labAsymmetricTrace, "back 2", "back 3", 1), "asymm  1 ", "", 1))
	rc = &statContext{stats: map[string]any{}}
	if err := setTraceStats(rc, sym); err != nil {
		t.Fatal(err)
	}
	if rc.stats["asymmetric"] != false || rc.stats["return_hops"] != 3 {
		t.Errorf("stats %v, want a symmetric path, 3 hops back", rc.stats)
	}
	if d := hopMaps(sym.hopDetails()); d[2]["return_hops"] != nil {
		t.Errorf("a symmetric destination has return_hops: %v", d)
	}
	// No back on the Resume line: tracepath could not read the reply's TTL,
	// and nothing is said about symmetry.
	unread := parseTracepath(strings.Replace(labAsymmetricTrace, "back 2 ", "", 1))
	rc = &statContext{stats: map[string]any{}}
	if err := setTraceStats(rc, unread); err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.stats["asymmetric"]; ok {
		t.Errorf("stats %v say whether a path is asymmetric with no back count", rc.stats)
	}
}

// TestTracePathState tells a filtered path from a loop and from a trace no
// hop answered, never reading a loop from unanswered hops: the CML lab's
// route loop answered nothing to the host tracing it (twelve "*"), which is
// unknown, while a router's own trace through it alternated two addresses.
func TestTracePathState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hops  []string
		state string
	}{
		{"every hop silent, as the lab's loop was from Host1", []string{"*", "*", "*", "*", "*", "*", "*", "*", "*", "*", "*", "*"}, pathUnknown},
		{"a filtered last hop", []string{"10.0.0.1", "10.0.1.1", "*", "*", "*"}, pathUnreached},
		{"two routers alternating", []string{"10.0.10.2", "10.0.10.1", "10.0.10.2", "10.0.10.1"}, pathLoop},
		{"a router passed twice past a silent one", []string{"10.0.0.1", "*", "10.0.0.1"}, pathLoop},
		{"a router answering twice in a row is not a loop", []string{"10.0.0.1", "10.0.0.1", "10.0.0.2", "*"}, pathUnreached},
		{"a loop of silent routers is not read from stars", []string{"10.0.0.1", "*", "*", "*"}, pathUnreached},
	} {
		var b strings.Builder
		b.WriteString(" 1?: [LOCALHOST]                      pmtu 1500\n")
		for i, h := range tc.hops {
			if h == "*" {
				fmt.Fprintf(&b, "%2d:  no reply\n", i+1)
				continue
			}
			fmt.Fprintf(&b, "%2d:  %-50s 0.300ms\n", i+1, h)
		}
		b.WriteString("     Too many hops: pmtu 1500\n     Resume: pmtu 1500 \n")
		p := parseTracepath(b.String())
		if got := p.state(); got != tc.state {
			t.Errorf("%s: %s, want %s (%v)", tc.name, got, tc.state, p.addresses())
		}
	}
}

// TestTraceExpect checks reached, includes, avoids and symmetric, and
// refuses an expectation it does not know.
func TestTraceExpect(t *testing.T) {
	e, err := traceExpectParam(map[string]any{"expect": map[string]any{"reached": true, "includes": []any{"10.0.0.1"}, "avoids": []any{"10.0.0.9"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := func(reached bool, hops ...string) tracedPath {
		p := tracedPath{reached: reached, out: -1, back: -1}
		for i, h := range hops {
			p.hops = append(p.hops, tracedHop{n: i + 1, addr: h})
		}
		return p
	}
	if p := e.check(path(true, "10.0.0.1", "10.0.0.5")); len(p) != 0 {
		t.Errorf("a path as expected: %v", p)
	}
	if p := e.check(path(false, "10.0.0.9")); len(p) != 3 {
		t.Errorf("a path wrong three ways: %v", p)
	}
	asym := parseTracepath(labAsymmetricTrace)
	for _, tc := range []struct {
		symmetric bool
		path      tracedPath
		want      string
	}{
		{true, asym, "went out over 3 hops and the reply came back over 2"},
		{false, asym, ""},
		{false, parseTracepath(strings.Replace(labAsymmetricTrace, "back 2", "back 3", 1)), "over 3 hops each"},
		{true, path(false, "10.0.0.1", "*"), "did not answer"},
		{true, parseTracepath(strings.Replace(labAsymmetricTrace, "back 2 ", "", 1)), "could not read"},
	} {
		e, err := traceExpectParam(map[string]any{"expect": map[string]any{"symmetric": tc.symmetric}})
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(e.check(tc.path), "; ")
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("symmetric %v over %v: %q, want %q", tc.symmetric, tc.path.addresses(), got, tc.want)
		}
	}
	for _, bad := range []map[string]any{
		{"expect": "reached"},
		{"expect": map[string]any{"reachd": true}},
		{"expect": map[string]any{"reached": "yes"}},
		{"expect": map[string]any{"symmetric": "yes"}},
		{"expect": map[string]any{"includes": []any{"r2"}}},
	} {
		if _, err := traceExpectParam(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
	// Only reached: false is something a trace no hop answered can show.
	for _, tc := range []struct {
		expect map[string]any
		only   bool
	}{
		{map[string]any{"reached": false}, true},
		{map[string]any{"reached": true}, false},
		{map[string]any{"reached": false, "avoids": []any{"10.0.0.9"}}, false},
		{map[string]any{"reached": false, "symmetric": false}, false},
		{map[string]any{}, false},
	} {
		e, err := traceExpectParam(map[string]any{"expect": tc.expect})
		if err != nil {
			t.Fatal(err)
		}
		if e.onlyUnreached() != tc.only {
			t.Errorf("%v: onlyUnreached is %v", tc.expect, !tc.only)
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

// TestTrace_AnEmptyContainerKeepsTheShapeItWasCheckedFor: a call giving
// container, even empty (as a template rendering nothing gives it), is held
// by validation, the engine and admission to the container-engine shape
// (Manifest.For), so the method refuses it rather than tracing over SSH, a
// shape nothing checked the device for. Before, "" fell through to SSH.
func TestTrace_AnEmptyContainerKeepsTheShapeItWasCheckedFor(t *testing.T) {
	d, ok := collection.Lookup(traceFQCN)
	if !ok {
		t.Fatal("net.trace is not registered")
	}
	params := map[string]any{"dest": "10.0.0.1", "container": ""}
	if shaped := d.Manifest.For(params); !slices.Equal(shaped.RequiredCapabilities, []capability.Name{capability.NameContainerEngine}) {
		t.Fatalf("an empty container is held to %v; want the container-engine shape", shaped.RequiredCapabilities)
	}
	rc := &statContext{stats: map[string]any{}}
	if _, err := Trace(context.Background(), rc, nil, params); err == nil || !strings.Contains(err.Error(), "container is empty") {
		t.Errorf("an empty container: %v; want it refused, not traced over SSH", err)
	}
	if len(rc.stats) != 0 {
		t.Errorf("an empty container recorded %v", rc.stats)
	}
	// Left out, or null, it is the device's own shape, which the real router
	// test above runs over SSH.
	for _, p := range []map[string]any{{"dest": "10.0.0.1"}, {"dest": "10.0.0.1", "container": nil}} {
		if shaped := d.Manifest.For(p); slices.Contains(shaped.RequiredCapabilities, capability.NameContainerEngine) {
			t.Errorf("%v is held to the container shape", p)
		}
	}
}
