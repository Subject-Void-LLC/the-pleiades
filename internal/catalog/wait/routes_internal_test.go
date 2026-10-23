// Tests of how wait.routes reads a routing table and its parameters.
package wait

import (
	"strings"
	"testing"
)

// TestRoutes_FingerprintIgnoresWhatMovesWhileNothingDoes: an uptime or a
// group id is not a change; a next hop going inactive is.
func TestRoutes_FingerprintIgnoresWhatMovesWhileNothingDoes(t *testing.T) {
	a := map[string][]route{"10.1.0.0/24": {{Protocol: "ospf", Selected: true, Distance: 110, Metric: 20, NextHops: []nextHop{{IP: "10.0.0.1", Active: true, FIB: true}}}}}
	b := map[string][]route{"10.1.0.0/24": {{Protocol: "ospf", Selected: true, Distance: 110, Metric: 20, NextHops: []nextHop{{IP: "10.0.0.1", Active: true, FIB: true}}}}}
	if fingerprint(a) != fingerprint(b) {
		t.Error("two equal tables fingerprint apart")
	}
	b["10.1.0.0/24"][0].NextHops[0].Active = false
	if fingerprint(a) == fingerprint(b) {
		t.Error("a next hop going inactive is not a change")
	}
}

// TestRoutes_Satisfied: a selected route through the next hops wanted
// satisfies; an unselected one, a missing one and another next hop do not.
func TestRoutes_Satisfied(t *testing.T) {
	w := routesWant{prefixes: []string{"10.1.0.0/24", "10.2.0.0/24", "10.3.0.0/24"}, nextHops: map[string][]string{"10.3.0.0/24": {"10.0.0.9"}}}
	table := map[string][]route{
		"10.1.0.0/24": {{Selected: true, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}, {Interface: "eth2", Active: true}, {IP: "10.0.0.7"}}}},
		"10.2.0.0/24": {{Selected: false, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}},
		"10.3.0.0/24": {{Selected: true, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}},
	}
	used, missing := w.satisfied(table)
	if strings.Join(used["10.1.0.0/24"], ",") != "10.0.0.1,eth2" {
		t.Errorf("used = %v", used)
	}
	if len(missing) != 2 || !strings.Contains(missing[0], "10.2.0.0/24") || !strings.Contains(missing[1], "through 10.0.0.9") {
		t.Errorf("missing = %q", missing)
	}
}

// TestRoutes_ParamsAreCheckedFirst refuses what no router could answer.
func TestRoutes_ParamsAreCheckedFirst(t *testing.T) {
	for _, p := range []map[string]any{
		{},
		{"prefixes": []any{}},
		{"prefixes": []any{"10.1.0.0"}},
		{"prefixes": []any{"10.1.0.0/24"}, "next_hops": map[string]any{"10.9.0.0/24": []any{"10.0.0.1"}}},
		{"prefixes": []any{"10.1.0.0/24"}, "next_hops": map[string]any{"10.1.0.0/24": []any{"router"}}},
		{"prefixes": []any{"10.1.0.0/24"}, "next_hops": []any{"10.0.0.1"}},
		{"prefixes": []any{"10.1.0.0/24"}, "stable_for": 10, "timeout": 10},
		{"prefixes": []any{"10.1.0.0/24"}, "timeout": 0},
		{"prefixes": []any{"2001:db8::/64"}},
		{"prefixes": 5},
		{"prefixes": []any{"10.1.0.0/24"}, "next_hops": map[string]any{"10.1.0.0/24": []any{}}},
		{"prefixes": []any{"10.1.0.0/24"}, "stable_for": "soon"},
		{"prefixes": []any{"10.1.0.0/24"}, "timeout": "later"},
	} {
		if _, err := routesParams(p); err == nil {
			t.Errorf("%v was accepted", p)
		}
	}
	w, err := routesParams(map[string]any{"prefixes": []any{"10.1.0.5/24"}})
	if err != nil || w.prefixes[0] != "10.1.0.0/24" || w.stable.Seconds() != 5 || w.timeout.Seconds() != 120 {
		t.Errorf("defaults: %+v %v", w, err)
	}
}
