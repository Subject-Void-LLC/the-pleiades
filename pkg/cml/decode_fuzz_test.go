// Fuzz tests of what the client decodes from a controller it does not
// trust to be well formed: a node's configuration in either shape, and the
// addresses read out of it.
package cml

import (
	"encoding/json"
	"net/netip"
	"testing"
)

// FuzzConfigText feeds configText any bytes as a node's configuration, and
// the address pattern whatever text comes back: neither may panic, and
// every address the pattern finds is one netip reads or one Nodes drops.
func FuzzConfigText(f *testing.F) {
	for _, seed := range []string{
		`"hostname R1\ninterface E0/0\n ip address 10.0.0.1 255.255.255.0"`,
		`[{"name":"node.cfg","content":"ip addr add 10.10.20.177/24 dev eth0"}]`,
		`null`, `{}`, `[1,2]`, `"ifconfig eth1 999.1.1.1 up"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		text := configText(json.RawMessage(raw))
		for _, m := range configAddress.FindAllStringSubmatch(text, -1) {
			if len(m) != 2 {
				t.Fatalf("a match with %d groups", len(m))
			}
			_, _ = netip.ParseAddr(m[1])
		}
	})
}
