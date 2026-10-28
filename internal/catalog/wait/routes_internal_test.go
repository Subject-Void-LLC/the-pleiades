// Tests of how wait.routes reads a routing table and its parameters.
package wait

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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

// TestRoutes_Gone: with state absent a prefix with no selected route is
// gone, an unselected route does not count, a selected one stays named with
// its next hops, and a prefix next_hops names is gone once its route uses
// none of them.
func TestRoutes_Gone(t *testing.T) {
	w := routesWant{absent: true, prefixes: []string{"10.1.0.0/24", "10.2.0.0/24", "10.3.0.0/24", "10.4.0.0/24", "10.5.0.0/24"}, nextHops: map[string][]string{
		"10.4.0.0/24": {"10.0.0.9"}, "10.5.0.0/24": {"10.0.0.9"},
	}}
	table := map[string][]route{
		"10.2.0.0/24": {{Selected: false, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}},
		"10.3.0.0/24": {{Selected: true, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}},
		"10.4.0.0/24": {{Selected: true, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}},
		"10.5.0.0/24": {{Selected: true, NextHops: []nextHop{{IP: "10.0.0.9", Active: true}}}},
	}
	used, remaining := w.gone(table)
	if strings.Join(remaining, "; ") != "to 10.3.0.0/24 (through 10.0.0.1); to 10.5.0.0/24 through 10.0.0.9" {
		t.Errorf("remaining = %q", remaining)
	}
	if got := used["10.1.0.0/24"]; got == nil || len(got) != 0 {
		t.Errorf("used[10.1.0.0/24] = %#v, want an empty list", got)
	}
	if strings.Join(used["10.4.0.0/24"], ",") != "10.0.0.1" {
		t.Errorf("used = %v", used)
	}
}

// TestRoutes_GoneNeedsATableItCanRead: a table with no selected route at
// all (an empty read, a pager cut, an IOS answer whose shape the parser
// skips, every route unselected) proves nothing gone, so state absent keeps
// waiting, naming why, rather than passing on a table it never saw.
func TestRoutes_GoneNeedsATableItCanRead(t *testing.T) {
	w := routesWant{absent: true, prefixes: []string{"10.9.9.0/24"}, nextHops: map[string][]string{}}
	for name, table := range map[string]map[string][]route{
		"empty":            {},
		"nil":              nil,
		"unparsed ios":     iosRoutes(map[string]any{}),
		"nothing selected": {"10.1.0.0/24": {{Selected: false, NextHops: []nextHop{{IP: "10.0.0.1", Active: true}}}}},
	} {
		used, remaining := w.gone(table)
		if len(remaining) != 1 || remaining[0] != tableUnread {
			t.Errorf("%s: remaining = %q, want the table named unread", name, remaining)
		}
		if got := used["10.9.9.0/24"]; got == nil || len(got) != 0 {
			t.Errorf("%s: used = %#v, want an empty list per prefix", name, used)
		}
	}
	// A table holding only a connected route is a real read: the prefix is gone.
	connected := map[string][]route{"10.0.12.0/24": {{Protocol: "connected", Selected: true, NextHops: []nextHop{{Interface: "eth1", Active: true}}}}}
	if _, remaining := w.gone(connected); len(remaining) != 0 {
		t.Errorf("remaining = %q, want the prefix gone", remaining)
	}
}

// TestRoutes_State: state is present or absent, its case ignored, and
// anything else is refused before the router is reached.
func TestRoutes_State(t *testing.T) {
	for state, absent := range map[string]bool{"": false, "present": false, "Present": false, " absent ": true, "ABSENT": true} {
		w, err := routesParams(map[string]any{"prefixes": []any{"10.1.0.0/24"}, "state": state})
		if err != nil || w.absent != absent {
			t.Errorf("state %q = %+v, %v, want absent %t", state, w, err, absent)
		}
	}
	for _, state := range []any{"gone", "started", 1} {
		if _, err := routesParams(map[string]any{"prefixes": []any{"10.1.0.0/24"}, "state": state}); err == nil {
			t.Errorf("state %v was accepted", state)
		}
	}
	d, ok := collection.Lookup(routesFQCN)
	if !ok {
		t.Fatal("wait.routes is not registered")
	}
	for _, p := range d.Manifest.Doc.Params {
		if p.Name == waitParamState && (!p.ChoicesIgnoreCase || strings.Join(p.Choices, ",") != "present,absent") {
			t.Errorf("state declares %v (ignore case %t), want present and absent, case ignored", p.Choices, p.ChoicesIgnoreCase)
		}
	}
}
