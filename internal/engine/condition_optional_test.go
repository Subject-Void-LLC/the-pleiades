// Tests of conditions written with CEL's optional syntax (.? and [?]), which
// the compile environment accepts: the readers that work out what a condition
// reads before a run must accept exactly the same syntax, so such a condition
// is decided on each device when it reads device or a device's variables, its
// register reads are seen by a tag selection, a check and the segmenter, and
// no condition that compiles is ever taken to read nothing.
package engine_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestConditionOptional_ReferencesAreReadOffEveryRoot: each reader of a
// condition's references parses the optional field and index, on every root
// a condition reads through (device, result, nodes, stat, vars, error), in
// when_cel and in a when: or when_or: item.
func TestConditionOptional_ReferencesAreReadOffEveryRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		c      engine.Conditional
		root   string
		reads  []string
		tested []string
	}{
		{"device in when_cel", engine.Conditional{WhenCEL: "device.?name.hasValue()"}, engine.TemplateRootDevice, nil, nil},
		{"device in a when item", engine.Conditional{When: engine.StringList{"device[?'name'].orValue('') == 'web1'"}}, engine.TemplateRootDevice, nil, nil},
		{"vars in a when_or item", engine.Conditional{WhenOr: engine.StringList{"vars.?site.orValue('') == 'hq'", "false"}}, engine.TemplateRootVars, nil, nil},
		{"error in when_cel", engine.Conditional{WhenCEL: "error.?msg.hasValue()"}, engine.TemplateRootError, nil, nil},
		{"result by field", engine.Conditional{WhenCEL: "result.?probe.hasValue()"}, engine.TemplateRootResult, []string{"probe"}, []string{"probe"}},
		{"nodes by index", engine.Conditional{WhenCEL: `nodes[?"probe"].hasValue()`}, engine.TemplateRootNodes, []string{"probe"}, []string{"probe"}},
		{"stat by field, then a plain read", engine.Conditional{When: engine.StringList{"stat.?probe.hasValue() && stat.other[''].ok"}}, "stat", []string{"other", "probe"}, []string{"probe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uses, err := engine.ConditionUsesRoot(tc.c, tc.root)
			if err != nil || !uses {
				t.Errorf("ConditionUsesRoot(%s) = %v, %v; want true", tc.root, uses, err)
			}
			names, dynamic, err := engine.ConditionReads(tc.c)
			if err != nil || dynamic || !slices.Equal(names, tc.reads) {
				t.Errorf("ConditionReads = %v, %v, %v; want %v", names, dynamic, err, tc.reads)
			}
			tested, err := engine.ConditionTestsPresence(tc.c)
			if err != nil {
				t.Fatalf("ConditionTestsPresence: %v", err)
			}
			var got []string
			for name := range tested {
				got = append(got, name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.tested) {
				t.Errorf("ConditionTestsPresence = %v, want %v", got, tc.tested)
			}
		})
	}
}

// TestConditionOptional_DecidedOnEachDevice: a condition reading device, or
// a variable the devices bring, through the optional syntax is decided on
// each device, so it runs where it holds and only there. Before the readers
// shared the compile environment's syntax, the first failed at run time with
// "no such attribute(s): device" and the second was decided once with no
// device's variables, skipping the device it should have run on.
func TestConditionOptional_DecidedOnEachDevice(t *testing.T) {
	registerRollbackFixtures(t)
	registerDeviceRootTypes()
	stub := func(name string, vars map[string]any) inventory.InventoryItem {
		return typedStub{&inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive,
			Props: map[string]inventory.PropertyValue{engine.DeviceVarsProperty: vars}}}
	}
	resolver := mapResolver{"web": {stub("web1", map[string]any{"site": "hq"}), stub("web2", map[string]any{"site": "br1"})}}
	for _, condition := range []string{
		`when_cel: "device.?name.orValue('') == 'web1'"`,
		`when: ["device[?'name'].orValue('') == 'web1'"]`,
		`when_or: ["vars.?site.orValue('') == 'hq'"]`,
		`when_cel: "vars[?'site'].orValue('') == 'hq'"`,
	} {
		t.Run(condition, func(t *testing.T) {
			seen := &paramsSeen{}
			x := engine.NewExecutor(resolver, seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), yamlRunbook(t, "id: o\nhosts: web\ntasks:\n  - name: only one\n    "+condition+"\n    rbtest.make:\n      name: x\n      note: ran\n"))
			if err != nil {
				t.Fatal(err)
			}
			if result.HasErrors() {
				t.Fatalf("%+v", result.Nodes)
			}
			if want := []string{"web1: ran"}; !slices.Equal(seen.seen, want) {
				t.Errorf("ran %v, want %v", seen.seen, want)
			}
		})
	}
}

// TestConditionOptional_LockAllIsRefusedForADeviceRead: a task taking every
// device's lock together cannot read device, and one reading it through the
// optional syntax is refused as one reading it plainly is.
func TestConditionOptional_LockAllIsRefusedForADeviceRead(t *testing.T) {
	registerRollbackFixtures(t)
	registerDeviceRootTypes()
	resolver := mapResolver{"web": {typedStub{&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive,
		Props: map[string]inventory.PropertyValue{engine.DeviceVarsProperty: map[string]any{"site": "hq"}}}}}}
	// A device read, and a read of a variable the device brings, which
	// differs by device (engine.VarsDifferByDevice, which validate asks too).
	for _, condition := range []string{`device.?name.hasValue()`, `vars.?site.orValue('') == 'hq'`} {
		seen := &paramsSeen{}
		x := engine.NewExecutor(resolver, seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
			engine.WithRenderer(render.New()))
		result, err := x.Run(context.Background(), yamlRunbook(t, "id: l\nhosts: web\ntasks:\n  - name: all\n    lock_acquisition: all_at_plan_time\n    when_cel: \""+condition+"\"\n    rbtest.make:\n      name: x\n"))
		if err != nil {
			t.Fatal(err)
		}
		refused := false
		for _, n := range result.Nodes {
			if n.Err != nil && strings.Contains(n.Err.Error(), engine.ErrDeviceRootLockAll) {
				refused = true
			}
		}
		if !refused || len(seen.seen) != 0 {
			t.Errorf("lock all with %s: ran %v, nodes %+v", condition, seen.seen, result.Nodes)
		}
	}
}

// TestConditionOptional_SelectAndSegmenterSeeTheRead: a tag selection holds
// an optional register read to the selection as it holds a plain one, and
// refuses nothing in a runbook whose condition only reads vars that way; the
// segmenter's TaskReads names the register too.
func TestConditionOptional_SelectAndSegmenterSeeTheRead(t *testing.T) {
	built, err := buildYAML(t, "id: r\ntasks:\n  - name: probe\n    noop: {}\n    register: probe\n    tags: check\n  - name: act\n    noop: {}\n    tags: act\n    when_cel: \"result.?probe.hasValue()\"\n  - name: other\n    noop: {}\n    tags: other\n    when_cel: \"vars.?x.orValue(false)\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Select(built, engine.TagFilter{Tags: []string{"act"}}); err == nil || !strings.Contains(err.Error(), `reads the result registered as "probe"`) {
		t.Errorf("Select(act) = %v, want a refusal naming probe", err)
	}
	if _, err := engine.Select(built, engine.TagFilter{Tags: []string{"other"}}); err != nil {
		t.Errorf("Select(other) = %v, want no refusal", err)
	}
	reads, err := engine.TaskReads(render.New(), built.Nodes["tasks[1]"])
	if err != nil || !reflect.DeepEqual(reads, engine.Reads{Names: []string{"probe"}}) {
		t.Errorf("TaskReads = %+v, %v; want probe", reads, err)
	}
	if !built.Conditional("tasks[1]") || !built.Conditional("tasks[2]") {
		t.Errorf("an optional condition is not Conditional: %v, %v", built.Conditional("tasks[1]"), built.Conditional("tasks[2]"))
	}
}

// TestConditionOptional_CheckTellsWhatTheConditionReads: in a check, a
// condition that fails against a prediction is told apart by the registers it
// reads, through the optional syntax too: a field a prediction lacks is
// unchecked, and a register no task writes fails as the real run would.
// Before, both were reported as reading what "cannot be told".
func TestConditionOptional_CheckTellsWhatTheConditionReads(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		want            conditionOutcome
		reason          string
	}{
		{"a field the prediction does not carry", `"when_cel": "stat.?b.value()[\"\"].rc == 0"`, outcomeUnchecked, "may not carry every field"},
		{"a misspelled register", `"when_cel": "stat[?\"reslt\"].value()[\"\"].x == 1"`, outcomeFailed, `registers "reslt"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, node := checkConditionCase(t, tc.condition)
			if got != tc.want {
				t.Fatalf("outcome = %s, want %s: %+v", got, tc.want, node)
			}
			text := node.SkipReason
			if node.Err != nil {
				text = node.Err.Error()
			}
			if !strings.Contains(text, tc.reason) {
				t.Errorf("the %s task says %q, want it to contain %q", got, text, tc.reason)
			}
		})
	}
}

// TestConditionOptional_AnUnreadableConditionIsDecidedOnEachDevice: a task
// whose condition compiled but whose references cannot be read (its source
// changed after the build, the one way to reach it) is decided on each
// device, never once for all of them, since what it reads cannot be told.
func TestConditionOptional_AnUnreadableConditionIsDecidedOnEachDevice(t *testing.T) {
	registerRollbackFixtures(t)
	registerDeviceRootTypes()
	stub := func(name string, vars map[string]any) inventory.InventoryItem {
		return typedStub{&inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive,
			Props: map[string]inventory.PropertyValue{engine.DeviceVarsProperty: vars}}}
	}
	resolver := mapResolver{"web": {stub("web1", map[string]any{"site": "hq"}), stub("web2", map[string]any{"site": "br1"})}}
	for _, condition := range []string{"device.name == 'web1'", "vars.site == 'hq'"} {
		t.Run(condition, func(t *testing.T) {
			dag := yamlRunbook(t, "id: u\nhosts: web\ntasks:\n  - name: only one\n    when_cel: \""+condition+"\"\n    rbtest.make:\n      name: x\n      note: ran\n")
			dag.Nodes["tasks[0]"].WhenCEL = "x =="
			seen := &paramsSeen{}
			x := engine.NewExecutor(resolver, seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatal(err)
			}
			if result.HasErrors() {
				t.Fatalf("%+v", result.Nodes)
			}
			if want := []string{"web1: ran"}; !slices.Equal(seen.seen, want) {
				t.Errorf("ran %v, want %v", seen.seen, want)
			}
		})
	}
}

// TestConditionOptional_OnlyASafelyConsumedOptionalIsAPresenceTest: an
// optional read is written for the register's absence only when its optional
// is consumed without failing on an empty one (hasValue, orValue, or, either
// side, optMap, optFlatMap, through a chain of optional steps); one whose value
// is taken (.value()) fails without the register and is a plain read.
func TestConditionOptional_OnlyASafelyConsumedOptionalIsAPresenceTest(t *testing.T) {
	for _, tc := range []struct {
		cond   string
		tested []string
	}{
		{"result.?a.hasValue()", []string{"a"}},
		{"result.?a.orValue({'rc': 1}).rc == 0", []string{"a"}},
		{"result.?a.or(result.?b).hasValue()", []string{"a", "b"}},
		{"optional.none().or(nodes[?'a']).hasValue()", []string{"a"}},
		{"result.?a.optMap(v, v.rc).orValue(1) == 0", []string{"a"}},
		{"result.?a.optFlatMap(v, v.?rc).hasValue()", []string{"a"}},
		{"result.?a.?b.hasValue()", []string{"a"}},
		{"result.?a.value().rc == 0", nil},
		{"result.?a.?b.value() == 1", nil},
		{"stat[?'a'].value()[''].rc == 0", nil},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			tested, err := engine.ConditionTestsPresence(engine.Conditional{WhenCEL: tc.cond})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for name := range tested {
				got = append(got, name)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.tested) {
				t.Errorf("ConditionTestsPresence(%s) = %v, want %v", tc.cond, got, tc.tested)
			}
		})
	}
}
