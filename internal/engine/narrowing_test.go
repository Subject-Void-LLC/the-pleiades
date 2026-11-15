// Tests of a run narrowed to some of the inventory's devices (narrowing.go),
// through the real Executor: a task whose target names only devices the
// narrowing hid is skipped on this run, journaled as a limit skip, before its
// condition or parameters are read, in a block as at the top level and for a
// target rendered from data; a name that matches nothing still fails.
package engine_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// narrowResolver is an engine.Narrowing over all, hiding the devices named in
// hidden, as the CLI's --limit resolver does.
type narrowResolver struct {
	all    mapResolver
	hidden map[string]bool
}

// Resolve implements engine.TargetResolver: what all answers, less the hidden.
func (n narrowResolver) Resolve(target string) []inventory.InventoryItem {
	var out []inventory.InventoryItem
	for _, d := range n.all[target] {
		if !n.hidden[d.Name()] {
			out = append(out, d)
		}
	}
	return out
}

// Unnarrowed implements engine.Narrowing.
func (n narrowResolver) Unnarrowed(target string) []inventory.InventoryItem { return n.all[target] }

// HiddenBecause implements engine.Narrowing.
func (n narrowResolver) HiddenBecause(d inventory.InventoryItem) string {
	if n.hidden[d.Name()] {
		return `outside --limit "w1"`
	}
	return ""
}

// narrowInventory is two web hosts and one wan host, the wan host and w2
// hidden, so a run reaches w1 alone.
func narrowInventory() narrowResolver {
	mk := func(name string) inventory.InventoryItem {
		return &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive}
	}
	w1, w2, edge := mk("w1"), mk("w2"), mk("edge1")
	return narrowResolver{
		all: mapResolver{
			"web": {w1, w2}, "wan": {edge}, "w1": {w1}, "w2": {w2}, "edge1": {edge},
			// A tag whose own name holds the list separator.
			"a,b": {edge},
		},
		hidden: map[string]bool{"w2": true, "edge1": true},
	}
}

// TestResolveTargetsIn_TellsAHiddenNameFromAMissingOne: a name whose every
// device is hidden is Hidden and not Missing; a name that matches nothing is
// Missing, narrowed or not; and without a Narrowing nothing is Hidden.
func TestResolveTargetsIn_TellsAHiddenNameFromAMissingOne(t *testing.T) {
	r := narrowInventory()
	cases := []struct {
		target         string
		devices        []string
		missing        []string
		hidden         []string
		narrowedAway   bool
		resolverHidden bool
	}{
		{target: "wan", hidden: []string{"wan"}, narrowedAway: true},
		{target: "web,wan", devices: []string{"w1"}, hidden: []string{"wan"}},
		{target: "wan,nosuch", missing: []string{"nosuch"}, hidden: []string{"wan"}},
		{target: "nosuch", missing: []string{"nosuch"}},
		{target: "a,b", hidden: []string{"a,b"}, narrowedAway: true},
		{target: "w2", hidden: []string{"w2"}, narrowedAway: true},
	}
	for _, c := range cases {
		res := engine.ResolveTargetsIn(r, c.target)
		var devices, hidden []string
		for _, d := range res.Devices {
			devices = append(devices, d.Name())
		}
		for _, h := range res.Hidden {
			hidden = append(hidden, h.Name)
		}
		if !slices.Equal(devices, c.devices) || !slices.Equal(res.Missing, c.missing) || !slices.Equal(hidden, c.hidden) || res.NarrowedAway() != c.narrowedAway {
			t.Errorf("%q: devices %v missing %v hidden %v away %v, want %v %v %v %v", c.target, devices, res.Missing, hidden, res.NarrowedAway(), c.devices, c.missing, c.hidden, c.narrowedAway)
		}
		// ResolveTargets, which every other caller reads, agrees.
		if _, missing := engine.ResolveTargets(r, c.target); !slices.Equal(missing, c.missing) {
			t.Errorf("ResolveTargets(%q) missing %v, want %v", c.target, missing, c.missing)
		}
	}
	// Two devices of one name are named in order, and a device the resolver
	// hides without saying why is still named, with a reason of its own.
	r.hidden["w1"] = true
	res := engine.ResolveTargetsIn(disagreeing{r}, "web")
	if len(res.Hidden) != 1 || !slices.Equal(res.Hidden[0].Devices, []string{"w1", "w2"}) || !slices.Equal(res.Hidden[0].Why, []string{"not reached by this run", "not reached by this run"}) {
		t.Errorf("a hidden tag of two devices: %+v", res.Hidden)
	}
	delete(r.hidden, "w1")
	// A resolver that narrows nothing reports nothing hidden.
	if res := engine.ResolveTargetsIn(r.all, "nosuch"); len(res.Hidden) != 0 || !slices.Equal(res.Missing, []string{"nosuch"}) {
		t.Errorf("an unnarrowed resolver: %+v", res)
	}
}

// disagreeing is a narrowResolver that never says why it hides a device.
type disagreeing struct{ narrowResolver }

// HiddenBecause implements engine.Narrowing, saying nothing.
func (disagreeing) HiddenBecause(inventory.InventoryItem) string { return "" }

// callLog records each call as "<task>@<device>".
type callLog struct {
	mu    sync.Mutex
	calls []string
}

// Execute implements engine.ActionExecutor, changing nothing.
func (c *callLog) Execute(_ context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	device := ""
	if d != nil {
		device = d.Name()
	}
	c.calls = append(c.calls, task.Name+"@"+device)
	return engine.ActionResult{Stats: map[string]any{"x": "y"}}, nil
}

// runNarrowed runs runbook over narrowInventory with extra as the run's
// variables, returning the calls, the journal entries and the result.
func runNarrowed(t *testing.T, runbook string, extra map[string]any) ([]string, []engine.JournalEntry, engine.RunResult) {
	t.Helper()
	log, journal := &callLog{}, &entriesJournal{}
	opts := []engine.ExecutorOption{engine.WithRenderer(render.New()), engine.WithJournal(journal)}
	if extra != nil {
		opts = append(opts, engine.WithVariables(extra))
	}
	x := engine.NewExecutor(narrowInventory(), log, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	slices.Sort(log.calls)
	return log.calls, journal.entries, result
}

// TestNarrowing_ATaskOutsideTheRunIsSkippedNotFailed: run 10's defect.
// Under --limit CORE1, a task with target: wan failed with "wan matches no
// inventory host or tag", though the inventory had WAN1 tagged wan and
// validate had passed. Here every such task is skipped, naming the device and
// the limit, journaled as a limit skip with no device: at the top level, one
// reading a register only a skipped task would have written, a hosts list
// half outside the run, a unit block's task, and a target rendered from data.
func TestNarrowing_ATaskOutsideTheRunIsSkippedNotFailed(t *testing.T) {
	calls, entries, result := runNarrowed(t, `id: n
hosts: web,wan
tasks:
  - name: wan-links
    noop:
      target: wan
    register: links
  - name: reads-links
    when: ["result.links.x == 'y'"]
    noop:
      target: wan
      note: "{{ result.links.x }}"
  - name: everywhere
    noop: {}
  - name: listed
    noop:
      target: wan,web
  - name: guarded
    block:
      - name: in-block
        noop:
          target: wan
    rescue:
      - name: in-rescue
        noop: {}
  - name: rendered
    within: wan
    noop:
      target: "{{ vars.which }}"
`, map[string]any{"which": "edge1, edge1"})
	if result.HasErrors() {
		for _, n := range result.Nodes {
			if n.Err != nil {
				t.Errorf("%s failed: %v", n.NodeID, n.Err)
			}
		}
	}
	if want := []string{"everywhere@w1", "listed@w1"}; !slices.Equal(calls, want) {
		t.Errorf("ran %v, want %v", calls, want)
	}
	skippedByLimit := map[string]bool{}
	for _, e := range entries {
		if e.SkipKind != engine.SkipKindLimit {
			continue
		}
		skippedByLimit[e.NodeID] = true
		if e.Outcome != engine.OutcomeSkipped || e.DeviceID != "" {
			t.Errorf("%s: outcome %s, device %q", e.NodeID, e.Outcome, e.DeviceID)
		}
	}
	reasons := map[string]string{}
	for _, n := range result.Nodes {
		if n.Skipped {
			reasons[n.NodeID] = n.SkipReason
		}
	}
	for _, node := range []string{"tasks[0]", "tasks[1]", "tasks[4].block[0]", "tasks[5]"} {
		if !skippedByLimit[node] {
			t.Errorf("%s was not journaled as skipped by the limit: %+v", node, entries)
		}
		if !strings.Contains(reasons[node], `names no device this run reaches: edge1 is outside --limit "w1"`) {
			t.Errorf("%s: skip reason %q does not name the device and the limit", node, reasons[node])
		}
	}
	if result.Rescued() {
		t.Error("a task skipped by the limit failed its block")
	}
}

// TestNarrowing_ANameThatMatchesNothingStillFails: narrowing never excuses a
// name the inventory does not have, alone, beside a hidden one, or rendered.
func TestNarrowing_ANameThatMatchesNothingStillFails(t *testing.T) {
	for runbook, want := range map[string]string{
		"id: m\nhosts: web\ntasks:\n  - name: a\n    noop:\n      target: nosuch\n":                                   `target "nosuch" matches no inventory host or tag`,
		"id: m\nhosts: web\ntasks:\n  - name: a\n    noop:\n      target: wan,nosuch\n":                               `target "wan,nosuch": nosuch matches no inventory host or tag`,
		"id: m\nhosts: web\ntasks:\n  - name: a\n    within: wan\n    noop:\n      target: \"{{ vars.which }}\"\n":    `names "w2", which is not a device within "wan"`,
		"id: m\nhosts: web\ntasks:\n  - name: a\n    within: nosuch\n    noop:\n      target: \"{{ vars.which }}\"\n": `within: "nosuch" matches no inventory host or tag`,
	} {
		_, _, result := runNarrowed(t, runbook, map[string]any{"which": "w2"})
		if len(result.Nodes) == 0 || result.Nodes[0].Err == nil || !strings.Contains(result.Nodes[0].Err.Error(), want) {
			t.Errorf("%s: got %+v, want an error holding %q", runbook, result.Nodes, want)
		}
	}
}
