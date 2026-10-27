// Tests of how wait.routes reads a Cisco IOS routing table: show ip route,
// as pkg/iosparse parses it, becomes the route shape FRR's JSON has, so a
// connected route, a static one and an OSPF one through two next hops are
// each selected with their next hops; and a device that is neither FRR nor
// IOS is refused before it is reached.
package wait

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
)

// iosTable is show ip route from an IOS XE router: a connected tunnel, a
// static default and an OSPF route with two equal-cost next hops.
const iosTable = `Codes: L - local, C - connected, S - static, R - RIP, M - mobile, B - BGP
       D - EIGRP, EX - EIGRP external, O - OSPF, IA - OSPF inter area
Gateway of last resort is 10.0.12.2 to network 0.0.0.0

S*    0.0.0.0/0 [1/0] via 10.0.12.2
      10.0.0.0/8 is variably subnetted, 4 subnets, 3 masks
O        10.3.3.0/24 [110/20] via 10.0.12.2, 00:01:02, Ethernet0/1
                     [110/20] via 10.0.13.3, 00:01:02, Ethernet0/2
C        10.0.12.0/30 is directly connected, Ethernet0/1
L        10.0.12.1/32 is directly connected, Ethernet0/1
      172.31.0.0/16 is variably subnetted, 2 subnets, 2 masks
C        172.31.0.0/30 is directly connected, Tunnel0
L        172.31.0.1/32 is directly connected, Tunnel0
`

func TestIOSRoutes(t *testing.T) {
	_, parsed, err := iosparse.Parse("show ip route", iosTable)
	if err != nil {
		t.Fatal(err)
	}
	table := iosRoutes(parsed)
	hops := func(prefix string) []string {
		var out []string
		for _, r := range table[prefix] {
			if !r.Selected {
				t.Errorf("%s is not selected", prefix)
			}
			for _, h := range r.NextHops {
				if h.IP != "" {
					out = append(out, h.IP)
				} else {
					out = append(out, h.Interface)
				}
			}
		}
		return out
	}
	if got := hops("10.3.3.0/24"); !slices.Equal(got, []string{"10.0.12.2", "10.0.13.3"}) {
		t.Errorf("the OSPF route's next hops = %v", got)
	}
	if got := hops("172.31.0.0/30"); !slices.Equal(got, []string{"Tunnel0"}) {
		t.Errorf("the connected tunnel = %v", got)
	}
	if got := hops("0.0.0.0/0"); !slices.Equal(got, []string{"10.0.12.2"}) {
		t.Errorf("the static default = %v", got)
	}
	if r := table["10.3.3.0/24"][0]; r.Protocol != "ospf" || r.Distance != 110 || r.Metric != 20 {
		t.Errorf("the OSPF route = %+v", r)
	}
	want := routesWant{prefixes: []string{"10.3.3.0/24", "172.31.0.0/30"}, nextHops: map[string][]string{"10.3.3.0/24": {"10.0.13.3"}}}
	if _, missing := want.satisfied(table); len(missing) != 0 {
		t.Errorf("missing %v", missing)
	}
	// Uptimes are not in the shape, so a table read twice compares equal.
	_, again, _ := iosparse.Parse("show ip route", strings.ReplaceAll(iosTable, "00:01:02", "00:01:09"))
	if fingerprint(iosRoutes(again)) != fingerprint(table) {
		t.Error("an uptime moved the fingerprint")
	}

	if _, _, err := routeReader(context.Background(), nil, &inventorytest.Stub{StubName: "linux1"}, nil); err == nil || !strings.Contains(err.Error(), "neither an FRR router") {
		t.Errorf("a device neither FRR nor IOS: %v", err)
	}
}

// memIOS is an in-memory IOS shell for netcli.Open: each line is echoed
// with the device's answer and its prompt after it, as a real IOS device
// writes them (pkg/netcli's own scripted tests record that shape), and
// show ip route answers with table. A read error ends the shell.
type memIOS struct {
	table   string
	buf     string
	readErr error
}

// WriteLine queues line's echo, its answer and the prompt.
func (m *memIOS) WriteLine(_ context.Context, line string) error {
	answer := ""
	if line == "show ip route" {
		answer = strings.ReplaceAll(m.table, "\n", "\r\n")
	}
	m.buf += line + "\r\n" + answer + "r1#"
	return nil
}

// ReadUntil returns what is queued up to the end of pattern's first match.
func (m *memIOS) ReadUntil(_ context.Context, pattern *regexp.Regexp, _ int) (string, error) {
	if m.readErr != nil && strings.Contains(m.buf, "show ip route") {
		return "", m.readErr
	}
	loc := pattern.FindStringIndex(m.buf)
	if loc == nil {
		return "", errors.New("the prompt never came")
	}
	out := m.buf[:loc[1]]
	m.buf = m.buf[loc[1]:]
	return out, nil
}

// Close does nothing.
func (m *memIOS) Close() error { return nil }

// TestReadIOSRoutes reads the table through a real netcli session over an
// in-memory IOS shell; a table that does not parse and a read that fails
// are each refused, naming wait.routes.
func TestReadIOSRoutes(t *testing.T) {
	open := func(m *memIOS) *netcli.Session {
		t.Helper()
		m.buf = "r1#"
		s, err := netcli.Open(context.Background(), m, netcli.IOS, netcli.Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return s
	}
	table, err := readIOSRoutes(context.Background(), open(&memIOS{table: iosTable}))
	if err != nil {
		t.Fatal(err)
	}
	if r := table["10.3.3.0/24"]; len(r) != 1 || len(r[0].NextHops) != 2 {
		t.Errorf("the OSPF route read through the session = %+v", r)
	}
	if _, err := readIOSRoutes(context.Background(), open(&memIOS{table: "% Invalid input detected at '^' marker.\n"})); err == nil || !strings.Contains(err.Error(), routesFQCN) {
		t.Errorf("a table that does not parse: %v", err)
	}
	if _, err := readIOSRoutes(context.Background(), open(&memIOS{table: iosTable, readErr: errors.New("connection reset")})); err == nil || !strings.Contains(err.Error(), "reading the routing table") {
		t.Errorf("a read that fails: %v", err)
	}
	for v, want := range map[any]int{int64(7): 7, 2.0: 2, "12": 12, nil: 0} {
		if got := number(v); got != want {
			t.Errorf("number(%v) = %d, want %d", v, got, want)
		}
	}
}

// emptyRC is a run context holding no secrets.
type emptyRC struct{}

func (emptyRC) InjectSecrets() map[string]string { return map[string]string{} }
func (emptyRC) SetStat(string, any) error        { return nil }
func (emptyRC) EmitFact(string, any) error       { return nil }

// TestRouteReader_RefusesWhatItCannotOpen: no device is refused by name,
// and an FRR or IOS device whose session cannot open (this stub has no
// address to reach) returns that error with nothing left to close.
func TestRouteReader_RefusesWhatItCannotOpen(t *testing.T) {
	if _, _, err := routeReader(context.Background(), emptyRC{}, nil, nil); err == nil || !strings.Contains(err.Error(), "no target device") {
		t.Errorf("no device: %v", err)
	}
	for name, caps := range map[string][]capability.Name{
		"frr1": {capability.NameFRR, capability.NameCommandExec},
		"ios1": {capability.NameCiscoIOS},
	} {
		read, closer, err := routeReader(context.Background(), emptyRC{}, &inventorytest.Stub{StubName: name, Caps: caps}, nil)
		if err == nil || read != nil || closer != nil {
			t.Errorf("%s, with no address: read %v, close %v, err %v; want only an error", name, read != nil, closer != nil, err)
		}
	}
	if number(3) != 3 {
		t.Error("number(3) is not 3")
	}
}
