// Tests of the virt.cml.lab.* methods against cmltest's controller, through
// the real TLS pin and login a cml_controller device takes: a lab made,
// read, started, stopped and removed, each change's undo, each check, and
// each refusal.
package lab_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/cml/lab"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() { lab.SetPollInterval(time.Millisecond) }

// method is a virt.cml.lab method's run or check.
type method func(context.Context, sdk.RunbookContext, *cmltest.Device, map[string]any) (collection.Result, error)

// runner runs methods against one controller, each with a fresh context.
type runner struct {
	t      *testing.T
	ctl    *cmltest.Controller
	device *cmltest.Device
}

// run calls the registered method fqcn (its check with check set) with
// params, returning its result, its context and its error.
func (r runner) run(fqcn string, check bool, params map[string]any) (collection.Result, *cmltest.Context, error) {
	r.t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		r.t.Fatalf("%s is not registered", fqcn)
	}
	call := d.Invoke
	if check {
		call = d.Check
	}
	rc := cmltest.NewContext(r.ctl)
	res, err := call(context.Background(), rc, r.device, params)
	return res, rc, err
}

// newRunner starts a controller and returns a runner for it and a client
// logged in to arrange it.
func newRunner(t *testing.T) (runner, *cml.Client) {
	t.Helper()
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runner{t: t, ctl: ctl, device: cmltest.NewDevice(t, ctl)}, client
}

// TestLab_MakeStartStopAndRemove walks one lab through its life: made (a
// second make refused), two nodes started (and a second start unchanged),
// one stopped, read back, and removed (a second removal unchanged), each
// change recording exactly the undo that reverses it, and each check
// changing nothing.
func TestLab_MakeStartStopAndRemove(t *testing.T) {
	r, client := newRunner(t)
	ctx := context.Background()

	if res, rc, err := r.run("virt.cml.lab.create", true, map[string]any{"title": "cvd"}); err != nil || !res.Changed || len(r.ctl.Labs) != 0 {
		t.Fatalf("check create: %+v %v, %d labs", res, err, len(r.ctl.Labs))
	} else if _, ok := rc.Inverse(); ok {
		t.Error("a check recorded an undo")
	}
	res, rc, err := r.run("virt.cml.lab.create", false, map[string]any{"title": "cvd"})
	if err != nil || !res.Changed {
		t.Fatalf("create: %+v %v", res, err)
	}
	id, _ := rc.Stats["id"].(string)
	if inv, ok := rc.Inverse(); !ok || inv["fqcn"] != "virt.cml.lab.delete" || inv["params"].(map[string]any)["lab"] != id {
		t.Errorf("create's undo: %v", inv)
	}
	if _, _, err := r.run("virt.cml.lab.create", false, map[string]any{"title": "cvd"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second create: %v", err)
	}

	core, _ := client.CreateNode(ctx, id, cml.NodeSpec{Label: "CORE1", Definition: "iol-xe"})
	br, _ := client.CreateNode(ctx, id, cml.NodeSpec{Label: "BR1", Definition: "iol-xe"})
	if res, rc, err := r.run("virt.cml.lab.start", true, map[string]any{"lab": "cvd"}); err != nil || !res.Changed || len(rc.Stats["started"].([]string)) != 2 {
		t.Errorf("check start: %+v %v %v", res, err, rc.Stats)
	}
	if r.ctl.Labs[id].Nodes[core].State != cml.StateDefined {
		t.Error("a check started a node")
	}
	res, rc, err = r.run("virt.cml.lab.start", false, map[string]any{"lab": "cvd", "timeout": 30})
	if err != nil || !res.Changed {
		t.Fatalf("start: %+v %v", res, err)
	}
	if got := strings.Join(rc.Stats["started"].([]string), ","); got != "BR1,CORE1" {
		t.Errorf("started %s", got)
	}
	inv, ok := rc.Inverse()
	if !ok || inv["fqcn"] != "virt.cml.lab.stop" || inv[sdk.InverseKeepOnFailureKey] != true {
		t.Errorf("start's undo: %v", inv)
	}
	if ids, _ := inv["params"].(map[string]any)["nodes"].([]string); len(ids) != 2 {
		t.Errorf("start's undo names nodes %v", inv["params"])
	}
	if res, rc, err := r.run("virt.cml.lab.start", false, map[string]any{"lab": id}); err != nil || res.Changed {
		t.Errorf("a second start: %+v %v", res, err)
	} else if _, ok := rc.Inverse(); ok {
		t.Error("a start that changed nothing recorded an undo")
	}

	res, rc, err = r.run("virt.cml.lab.stop", false, map[string]any{"lab": "cvd", "nodes": []any{"CORE1"}})
	if err != nil || !res.Changed || r.ctl.Labs[id].Nodes[core].State != cml.StateStopped || r.ctl.Labs[id].Nodes[br].State != cml.StateBooted {
		t.Fatalf("stop CORE1: %+v %v", res, err)
	}
	if inv, ok := rc.Inverse(); !ok || inv["fqcn"] != "virt.cml.lab.start" || strings.Join(inv["params"].(map[string]any)["nodes"].([]string), ",") != core {
		t.Errorf("stop's undo: %v", inv)
	}

	res, rc, err = r.run("virt.cml.lab.info", false, map[string]any{"lab": "cvd"})
	if err != nil || res.Changed || rc.Stats["exists"] != true || rc.Stats["state"] != cml.StateStarted || len(rc.Stats["nodes"].([]any)) != 2 {
		t.Errorf("info: %+v %v %v", res, err, rc.Stats)
	}
	if _, rc, err := r.run("virt.cml.lab.info", false, map[string]any{"lab": "nothing"}); err != nil || rc.Stats["exists"] != false {
		t.Errorf("info of no lab: %v %v", err, rc.Stats)
	}
	if _, rc, err := r.run("virt.cml.lab.list", false, nil); err != nil || len(rc.Stats["labs"].([]any)) != 1 {
		t.Errorf("list: %v %v", err, rc.Stats)
	}

	if res, _, err := r.run("virt.cml.lab.delete", true, map[string]any{"lab": "cvd"}); err != nil || !res.Changed || len(r.ctl.Labs) != 1 {
		t.Errorf("check delete: %+v %v", res, err)
	}
	if res, rc, err := r.run("virt.cml.lab.delete", false, map[string]any{"lab": "cvd"}); err != nil || !res.Changed || len(r.ctl.Labs) != 0 {
		t.Fatalf("delete: %+v %v", res, err)
	} else if _, ok := rc.Inverse(); ok {
		t.Error("an irreversible delete recorded an undo")
	}
	if res, _, err := r.run("virt.cml.lab.delete", false, map[string]any{"lab": "cvd"}); err != nil || res.Changed {
		t.Errorf("a second delete: %+v %v", res, err)
	}
}

// TestLab_Refusals: a title two labs share, a node a lab does not hold, a
// timeout that is not above zero and a missing lab are refused; a boot that
// outlasts the timeout fails and keeps the undo of what it started.
func TestLab_Refusals(t *testing.T) {
	r, client := newRunner(t)
	ctx := context.Background()
	a, _ := client.CreateLab(ctx, "dup")
	b, _ := client.CreateLab(ctx, "dup")
	if _, _, err := r.run("virt.cml.lab.start", false, map[string]any{"lab": "dup"}); err == nil || !strings.Contains(err.Error(), a.ID) || !strings.Contains(err.Error(), b.ID) {
		t.Errorf("a shared title: %v", err)
	}
	if _, err := client.CreateNode(ctx, a.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"}); err != nil {
		t.Fatal(err)
	}
	for name, params := range map[string]map[string]any{
		"an unknown node":  {"lab": a.ID, "nodes": []any{"R9"}},
		"a zero timeout":   {"lab": a.ID, "timeout": 0},
		"no lab":           {"lab": "nothing"},
		"no lab parameter": {},
	} {
		if _, _, err := r.run("virt.cml.lab.start", false, params); err == nil {
			t.Errorf("%s: started", name)
		}
	}
	if _, _, err := r.run("virt.cml.lab.stop", false, map[string]any{"lab": "nothing"}); err == nil {
		t.Error("a stop of no lab succeeded")
	}
	r.ctl.Mu.Lock()
	r.ctl.BootPolls = 1 << 30
	r.ctl.Mu.Unlock()
	res, rc, err := r.run("virt.cml.lab.start", false, map[string]any{"lab": a.ID, "timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "R1 (STARTED)") || !res.Changed {
		t.Errorf("a boot past its timeout: %+v %v", res, err)
	}
	if inv, ok := rc.Inverse(); !ok || inv[sdk.InverseKeepOnFailureKey] != true {
		t.Errorf("a failed start kept no undo: %v", inv)
	}
}
