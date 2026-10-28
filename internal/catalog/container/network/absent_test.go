// What container.network.info answers for a network the engine does not
// have (Phase 131, R4): exists false, in a run and in a check. Run against
// this machine's real Docker engine.
package network_test

import "testing"

// TestInfo_AnAbsentNetworkIsExistsFalseInBothModes: the network is named
// and said not to exist, with nothing read from one, in both modes.
func TestInfo_AnAbsentNetworkIsExistsFalseInBothModes(t *testing.T) {
	h := newHarness(t)
	name := h.name(t) + "-absent"
	for _, check := range []bool{false, true} {
		rc := fresh()
		res, err := call(t, h, rc, "container.network.info", check, map[string]any{"name": name})
		if err != nil || res.Changed {
			t.Fatalf("check %v: %+v, %v", check, res, err)
		}
		if len(rc.stats) != 2 || rc.stats["exists"] != false || rc.stats["name"] != name {
			t.Errorf("check %v: stats = %v, want only the name and exists false", check, rc.stats)
		}
	}
}
