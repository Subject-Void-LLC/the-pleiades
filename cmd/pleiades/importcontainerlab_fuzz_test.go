// Fuzz test of a containerlab topology's decode: a file the operator
// names, never trusted to be containerlab's.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzClabTopology: any topology selects nodes and addresses them without
// panicking, and a selected node is always the named lab's.
func FuzzClabTopology(f *testing.F) {
	f.Add([]byte(`{"name":"lab","nodes":{"r1":{"shortname":"r1","mgmt-ipv4-address":"172.20.30.11","labels":{"containerlab":"lab"},"port-bindings":[{"host-ip":"0.0.0.0","host-port":2211,"port":22,"protocol":"tcp"}]}}}`), "")
	f.Add([]byte(`{"name":"lab","nodes":{"x":{"labels":{"containerlab":"other"}}}}`), "x")
	f.Fuzz(func(t *testing.T, data []byte, only string) {
		var topo clabTopology
		if json.Unmarshal(data, &topo) != nil || topo.Name == "" {
			return
		}
		names, err := clabSelect(topo, topo.Name, only)
		if err != nil {
			return
		}
		for _, n := range names {
			if topo.Nodes[n].Labels["containerlab"] != topo.Name || strings.ContainsAny(n, "/ ") {
				t.Fatalf("selected %q", n)
			}
			_, _, _, _ = clabAddress(topo.Nodes[n])
		}
	})
}
