// Tests for the segmenter: each rule that ends a segment, the levels a
// parallel group makes, the block rule, the carried registers and Verify.
package segment_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/segment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// devices counts the targets these tests use: web and db name several
// devices, web01 and api one each, and "" (the job's devices) several.
var devices = segment.Devices{"web": 3, "db": 2, "web01": 1, "api": 1, "": 4}

// build compiles a runbook from YAML.
func build(t *testing.T, src string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(src))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return dag
}

// registerLocal registers t117b.local, a controller-side method that needs no
// device, for the rest of the test.
func registerLocal(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	err := collection.Register(collection.Descriptor{Name: "t117b.local", Invoke: run, Manifest: collection.Manifest{
		Status:           collection.StatusImplemented,
		Reversibility:    collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"},
		ExecutionContext: collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceNone},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// shape is a segment as a test states it: its placement, why it begins, and
// its tasks by name.
type shape struct {
	place segment.Placement
	cut   segment.Cut
	names []string
}

// shapes reads a plan as shapes, naming each task by its name: and leaving
// out a parallel group's structural nodes.
func shapes(dag *engine.DAG, plan segment.Plan) []shape {
	var out []shape
	for _, seg := range plan.Segments {
		s := shape{place: seg.Placement, cut: seg.Cut}
		for _, id := range seg.Nodes {
			if task := dag.Nodes[id]; task.Kind() != engine.TaskKindSynthetic {
				s.names = append(s.names, task.Name)
			}
		}
		out = append(out, s)
	}
	return out
}

func on(target string) segment.Placement {
	return segment.Placement{Kind: segment.KindDevices, Target: target}
}

var local = segment.Placement{Kind: segment.KindLocal}

// TestSplit: each rule that ends a segment ends one there, and nothing else
// does.
func TestSplit(t *testing.T) {
	registerLocal(t)
	for _, tc := range []struct {
		name  string
		yaml  string
		wants []shape
	}{
		{"one target is one segment",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n  - name: b\n    noop: {}\n",
			[]shape{{on("web"), segment.CutStart, []string{"a", "b"}}}},
		{"a target of its own",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n  - name: b\n    noop:\n      target: db\n  - name: c\n    noop: {}\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("db"), segment.CutTarget, []string{"b"}}, {on("web"), segment.CutTarget, []string{"c"}}}},
		{"the same target written out joins hosts:",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n  - name: b\n    noop:\n      target: web\n",
			[]shape{{on("web"), segment.CutStart, []string{"a", "b"}}}},
		{"no hosts: runs on the job's devices",
			"id: r\ntasks:\n  - name: a\n    noop: {}\n  - name: b\n    noop: {}\n",
			[]shape{{on(""), segment.CutStart, []string{"a", "b"}}}},
		{"a call needing no device runs local, consecutive ones together",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n  - name: b\n    t117b.local: {}\n  - name: c\n    t117b.local: {}\n  - name: d\n    noop: {}\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {local, segment.CutTarget, []string{"b", "c"}}, {on("web"), segment.CutTarget, []string{"d"}}}},
		{"a rendered target is a segment of its own, even twice in a row",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: pick\n  - name: b\n    noop:\n      target: \"{{ result.pick.x }}\"\n    within: db\n  - name: c\n    noop:\n      target: \"{{ result.pick.x }}\"\n    within: db\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {segment.Placement{Kind: segment.KindData, Target: "db"}, segment.CutData, []string{"b"}}, {segment.Placement{Kind: segment.KindData, Target: "db"}, segment.CutData, []string{"c"}}}},
		{"a read of a register written on several devices",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop: {}\n  - name: c\n    noop: {}\n    when: stat.x.size() > 0\n  - name: d\n    noop: {}\n",
			[]shape{{on("web"), segment.CutStart, []string{"a", "b"}}, {on("web"), segment.CutRead, []string{"c", "d"}}}},
		{"a read of a register written on one device",
			"id: r\nhosts: web01\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop: {}\n    when: result.x.ok\n",
			[]shape{{on("web01"), segment.CutStart, []string{"a", "b"}}}},
		{"a read of a register written by a call needing no device",
			"id: r\ntasks:\n  - name: a\n    t117b.local: {}\n    register: x\n  - name: b\n    t117b.local:\n      note: \"{{ result.x.ok }}\"\n",
			[]shape{{local, segment.CutStart, []string{"a", "b"}}}},
		{"a template reading a register written on several devices",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop:\n      note: \"{{ nodes.x | to_json }}\"\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("web"), segment.CutRead, []string{"b"}}}},
		{"secret_mask reading a register written on several devices",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop: {}\n    secret_mask:\n      register: x\n      fields: [token]\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("web"), segment.CutRead, []string{"b"}}}},
		{"a read of every register",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop: {}\n    when: nodes[vars.k].size() > 0\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("web"), segment.CutRead, []string{"b"}}}},
		{"a read of a register an earlier segment wrote",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop:\n      target: db\n  - name: c\n    noop:\n      target: db\n    when: stat.x.size() > 0\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("db"), segment.CutTarget, []string{"b", "c"}}}},
		{"a secret registered on several devices ends the segment",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n    register_mask: token\n  - name: b\n    noop: {}\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("web"), segment.CutSecret, []string{"b"}}}},
		{"a secret registered on one device does not",
			"id: r\nhosts: web01\ntasks:\n  - name: a\n    noop: {}\n    register: x\n    register_mask: token\n  - name: b\n    noop: {}\n",
			[]shape{{on("web01"), segment.CutStart, []string{"a", "b"}}}},
		{"a parallel group runs its parts in sequence, the current target first",
			"id: r\nhosts: web\ntasks:\n  - name: a\n    noop:\n      target: db\n  - name: p\n    parallel:\n      - name: w1\n        noop: {}\n      - name: d1\n        noop:\n          target: db\n  - name: z\n    noop: {}\n",
			[]shape{{on("db"), segment.CutStart, []string{"a", "d1"}}, {on("web"), segment.CutTarget, []string{"w1", "z"}}}},
		{"a block without a rescue may span segments",
			"id: r\nhosts: web\ntasks:\n  - name: blk\n    block:\n      - name: a\n        noop: {}\n      - name: b\n        noop:\n          target: db\n",
			[]shape{{on("web"), segment.CutStart, []string{"a"}}, {on("db"), segment.CutTarget, []string{"b"}}}},
		{"a block with a rescue on one target is one segment",
			"id: r\nhosts: web01\ntasks:\n  - name: blk\n    block:\n      - name: a\n        noop: {}\n        register: x\n      - name: b\n        noop: {}\n        when: result.x.ok\n    rescue:\n      - name: r\n        noop: {}\n    always:\n      - name: w\n        noop: {}\n",
			[]shape{{on("web01"), segment.CutStart, []string{"a", "b", "r", "w"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := build(t, tc.yaml)
			plan, err := segment.Split(dag, devices)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			got := shapes(dag, plan)
			if len(got) != len(tc.wants) {
				t.Fatalf("got %d segments %+v, want %d %+v", len(got), got, len(tc.wants), tc.wants)
			}
			for i := range got {
				if got[i].place != tc.wants[i].place || got[i].cut != tc.wants[i].cut || !slices.Equal(got[i].names, tc.wants[i].names) {
					t.Errorf("segment %d = %+v, want %+v", i, got[i], tc.wants[i])
				}
				if plan.Segments[i].Ordinal != i {
					t.Errorf("segment %d has ordinal %d", i, plan.Segments[i].Ordinal)
				}
			}
			if plan.Version != dag.Version {
				t.Errorf("plan version %q, want the DAG's %q", plan.Version, dag.Version)
			}
		})
	}
}

// TestSplit_RefusesABlockItWouldSplit: a block with a rescue:, an always: or
// rollback_on_failure: whose tasks would land in several segments is
// refused, naming the block and why it splits; a failed segment ends the
// run, so the rescue would never run.
func TestSplit_RefusesABlockItWouldSplit(t *testing.T) {
	for _, tc := range []struct {
		name, handler, inner, want string
	}{
		{"a rescue on another target", "rescue:\n      - name: r\n        noop:\n          target: db\n", "", `target changes to "db"`},
		{"an always on another target", "always:\n      - name: w\n        noop:\n          target: db\n", "", `target changes to "db"`},
		{"rollback_on_failure across targets", "rollback_on_failure: true\n", "      - name: b\n        noop:\n          target: db\n", `target changes to "db"`},
		{"a read inside the block", "rescue:\n      - name: r\n        noop: {}\n", "      - name: b\n        noop: {}\n        when: stat.x.size() > 0\n", `reads the register "x"`},
		{"a read of every register inside the block", "rescue:\n      - name: r\n        noop: {}\n", "      - name: b\n        noop: {}\n        when: nodes[vars.k].size() > 0\n", "reads every register"},
		{"a rendered target inside the block", "rescue:\n      - name: r\n        noop: {}\n", "      - name: b\n        noop:\n          target: \"{{ vars.x }}\"\n        within: db\n", `rendered from data within "db"`},
		{"a secret inside the block", "rescue:\n      - name: r\n        noop: {}\n", "      - name: b\n        noop: {}\n        register_mask: token\n      - name: c\n        noop: {}\n", "registered a secret"},
		{"a rescue needing no device", "rescue:\n      - name: r\n        t117b.local: {}\n", "", "target changes to no device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registerLocal(t)
			src := "id: r\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: a\n        noop: {}\n        register: x\n" + tc.inner + "    " + tc.handler
			_, err := segment.Split(build(t, src), devices)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `"guarded"`) {
				t.Fatalf("Split error = %v, want one naming the block and saying %q", err, tc.want)
			}
		})
	}
}

// TestSplit_CarriesOnlyWhatLaterSegmentsRead: a segment reads in the
// registers an earlier one wrote and returns the ones a later one reads;
// a register nobody reads later is not returned, and a read of every
// register returns everything before it.
func TestSplit_CarriesOnlyWhatLaterSegmentsRead(t *testing.T) {
	dag := build(t, "id: r\nhosts: web\ntasks:\n"+
		"  - name: a\n    noop: {}\n    register: used\n"+
		"  - name: b\n    noop: {}\n    register: unused\n"+
		"  - name: c\n    noop:\n      target: db\n      note: \"{{ nodes.used | to_json }}\"\n    register: fromdb\n"+
		"  - name: d\n    noop:\n      target: api\n    when: nodes[vars.k].size() > 0\n")
	plan, err := segment.Split(dag, devices)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(plan.Segments))
	}
	web, db, api := plan.Segments[0], plan.Segments[1], plan.Segments[2]
	if !slices.Equal(web.Writes, []string{"unused", "used"}) || !slices.Equal(web.Returns, []string{"unused", "used"}) {
		t.Errorf("web writes %v returns %v; a read of every register later returns both", web.Writes, web.Returns)
	}
	if !slices.Equal(db.Reads, []string{"used"}) || db.ReadsAll {
		t.Errorf("db reads %v (all %v), want only used", db.Reads, db.ReadsAll)
	}
	if !slices.Equal(db.Returns, []string{"fromdb"}) {
		t.Errorf("db returns %v, want fromdb", db.Returns)
	}
	if !api.ReadsAll || len(api.Returns) != 0 {
		t.Errorf("api reads all %v returns %v", api.ReadsAll, api.Returns)
	}

	// Without the read of everything, unused is never returned; and a
	// register a segment both writes and reads (on one device) is not one
	// it carries in.
	dag = build(t, "id: r\nhosts: web\ntasks:\n"+
		"  - name: a\n    noop: {}\n    register: used\n"+
		"  - name: b\n    noop: {}\n    register: unused\n"+
		"  - name: c\n    noop:\n      target: api\n      note: \"{{ result.used.x }}\"\n    register: own\n"+
		"  - name: d\n    noop:\n      target: api\n    when: result.own.ok\n")
	plan, err = segment.Split(dag, devices)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Segments[0].Returns; !slices.Equal(got, []string{"used"}) {
		t.Errorf("returns %v, want only the register a later segment reads", got)
	}
	if got := plan.Segments[1]; !slices.Equal(got.Reads, []string{"used"}) || len(got.Returns) != 0 {
		t.Errorf("api reads %v returns %v, want to read only used and return nothing", got.Reads, got.Returns)
	}
}

// TestSplit_RefusesAnUncountedTarget: a target the caller did not count is
// an error naming it, never a guess about how many devices it names; and a
// segment on the job's devices says so in a message.
func TestSplit_RefusesAnUncountedTarget(t *testing.T) {
	dag := build(t, "id: r\nhosts: nowhere\ntasks:\n  - name: a\n    noop: {}\n")
	if _, err := segment.Split(dag, devices); err == nil || !strings.Contains(err.Error(), `"nowhere"`) {
		t.Fatalf("Split error = %v, want one naming the uncounted target", err)
	}
	if err := segment.Verify(dag, devices, dag.Version, segment.Segment{}); err == nil {
		t.Fatal("Verify accepted a segment of a runbook Split refuses")
	}
	if got := on("").String(); got != "the job's devices" {
		t.Errorf("String() = %q", got)
	}
}

// TestSplit_RefusesAGraphItCannotWalk: a DAG assembled by hand with a cycle,
// or with a condition that does not parse, is an error from Split and
// Selectors, never a partial plan.
func TestSplit_RefusesAGraphItCannotWalk(t *testing.T) {
	cycle := &engine.DAG{
		ID:         "c",
		EntryPoint: "a",
		Nodes:      map[string]*engine.Task{"a": {FQCN: "noop"}, "b": {FQCN: "noop"}},
		Adjacency:  map[string][]engine.EdgeConfig{"a": {{To: "b"}}, "b": {{To: "a"}}},
	}
	if _, err := segment.Split(cycle, devices); err == nil {
		t.Error("Split accepted a cycle")
	}
	if _, err := segment.Selectors(cycle); err == nil {
		t.Error("Selectors accepted a cycle")
	}
	unparsed := &engine.DAG{
		ID:         "u",
		Hosts:      "web",
		EntryPoint: "a",
		Nodes:      map[string]*engine.Task{"a": {FQCN: "noop", Conditional: engine.Conditional{When: []string{"x =="}}}},
	}
	if _, err := segment.Split(unparsed, devices); err == nil || !strings.Contains(err.Error(), "task a:") {
		t.Errorf("Split error = %v, want one naming the task", err)
	}
}

// TestSelectors: every literal target the walk reaches, once, and nothing
// for a rendered target or a call needing no device.
func TestSelectors(t *testing.T) {
	registerLocal(t)
	dag := build(t, "id: r\nhosts: web\ntasks:\n"+
		"  - name: a\n    noop: {}\n    register: pick\n"+
		"  - name: b\n    noop:\n      target: db\n"+
		"  - name: c\n    noop:\n      target: \"{{ result.pick.x }}\"\n    within: api\n"+
		"  - name: d\n    t117b.local: {}\n"+
		"  - name: p\n    parallel:\n      - name: e\n        noop: {}\n      - name: f\n        noop:\n          target: db\n")
	got, err := segment.Selectors(dag)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"db", "web"}) {
		t.Errorf("Selectors = %v, want [db web]", got)
	}
}

// TestVerify: the segment this copy computes passes; another version, other
// nodes, another placement, other carried registers and an ordinal past the
// end are each refused.
func TestVerify(t *testing.T) {
	dag := build(t, "id: r\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n    register: x\n  - name: b\n    noop:\n      target: db\n    when: stat.x.size() > 0\n")
	plan, err := segment.Split(dag, devices)
	if err != nil {
		t.Fatal(err)
	}
	good := plan.Segments[1]
	if err := segment.Verify(dag, devices, dag.Version, good); err != nil {
		t.Fatalf("Verify refused the segment it computes: %v", err)
	}
	for name, tc := range map[string]struct {
		version string
		seg     func(segment.Segment) segment.Segment
		want    string
	}{
		"another version": {"sha256:other", func(s segment.Segment) segment.Segment { return s }, "not the version"},
		"other nodes": {dag.Version, func(s segment.Segment) segment.Segment {
			s.Nodes = append(slices.Clone(s.Nodes), plan.Segments[0].Nodes...)
			return s
		}, "runs"},
		"another placement":       {dag.Version, func(s segment.Segment) segment.Segment { s.Placement = on("web"); return s }, "runs on"},
		"other carried registers": {dag.Version, func(s segment.Segment) segment.Segment { s.Reads = nil; return s }, "carries other registers"},
		"past the end":            {dag.Version, func(s segment.Segment) segment.Segment { s.Ordinal = 9; return s }, "does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			err := segment.Verify(dag, devices, tc.version, tc.seg(good))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}
