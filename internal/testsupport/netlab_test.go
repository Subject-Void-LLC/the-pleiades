// Tests for the net lab helper against real containers.
package testsupport

import (
	"net/netip"
	"strings"
	"testing"
)

// TestStartNetLab_RoutersAnswerAndKnowTheirLinks: two routers on one link
// answer SSH as NetLabUser, and each holds its link address on an interface
// InterfaceWith finds, with vtysh working as root inside.
func TestStartNetLab_RoutersAnswerAndKnowTheirLinks(t *testing.T) {
	need := func(cond bool, why string) {
		if !cond {
			t.Skip(why)
		}
	}
	need(!testing.Short(), "starts containers")
	link := NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.200.12.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.200.12.2"), "r2": netip.MustParseAddr("10.200.12.3"),
	}}
	lab := StartNetLab(t, []string{"r1", "r2"}, nil, []NetLabLink{link})
	for name, addr := range link.Ends {
		node := lab.Nodes[name]
		if node.HostKey == nil {
			t.Fatalf("%s presented no host key", name)
		}
		ifname := node.InterfaceWith(t, addr)
		if !strings.HasPrefix(ifname, "eth") {
			t.Errorf("%s holds %s on %q", name, addr, ifname)
		}
		if out := node.Exec(t, "vtysh", "-c", "show version"); !strings.Contains(out, "FRRouting 10.1.1") {
			t.Errorf("%s's vtysh answered %q", name, out)
		}
	}
	if !strings.Contains(string(lab.PrivateKeyPEM), "OPENSSH PRIVATE KEY") {
		t.Error("the lab's key is not an OpenSSH private key")
	}
}
