// Tests of virt.cml.lab.start against a controller at its node limit, as
// cmltest's controller answers one with CML 2.7.2's own licensing refusal:
// the check reports how full the controller is, and a start the controller
// refuses for capacity says so in plain words, with the count, how many it
// needed, how many it started, and the controller's own words.
package lab_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fullController arranges a controller allowing four running nodes, with a
// lab "other" running two and a lab "cvd" of three stopped nodes.
func fullController(t *testing.T) (runner, string) {
	t.Helper()
	r, client := newRunner(t)
	ctx := context.Background()
	other, _ := client.CreateLab(ctx, "other")
	for _, label := range []string{"X1", "X2"} {
		id, err := client.CreateNode(ctx, other.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.StartNode(ctx, other.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	cvd, _ := client.CreateLab(ctx, "cvd")
	for _, label := range []string{"A1", "A2", "A3"} {
		if _, err := client.CreateNode(ctx, cvd.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"}); err != nil {
			t.Fatal(err)
		}
	}
	r.ctl.Mu.Lock()
	r.ctl.NodeLimit = 4
	r.ctl.Mu.Unlock()
	return r, cvd.ID
}

// TestStart_SaysHowFullTheControllerIs: a check reports the nodes running
// and the nodes the start would bring it to, and starts nothing; the run is
// refused by the controller on its third node, fails naming the count, what
// it needed, what it started, the cause and the controller's words, and
// keeps the undo of exactly the two nodes it started.
func TestStart_SaysHowFullTheControllerIs(t *testing.T) {
	r, id := fullController(t)
	res, rc, err := r.run("virt.cml.lab.start", true, map[string]any{"lab": "cvd"})
	if err != nil || !res.Changed {
		t.Fatalf("check: %+v %v", res, err)
	}
	if rc.Stats["controller_nodes_running"] != 2 || rc.Stats["controller_nodes_needed"] != 5 || len(rc.Stats["started"].([]string)) != 3 {
		t.Errorf("check stats %v, want 2 running, 5 needed and 3 to start", rc.Stats)
	}
	for _, n := range r.ctl.Labs[id].Nodes {
		if n.State != cml.StateDefined {
			t.Errorf("a check started %s", n.Label)
		}
	}

	res, rc, err = r.run("virt.cml.lab.start", false, map[string]any{"lab": "cvd", "timeout": 30})
	if err == nil || !res.Changed {
		t.Fatalf("a start past the limit: %+v %v", res, err)
	}
	for _, want := range []string{
		"refused to start A3 because it already runs as many nodes as its license allows",
		"2 were running across the 2 labs this login can see before this task, which needed 3 more and started 2 of them",
		"does not read the limit itself",
		"free capacity first",
		`400: "` + cmltest.NodeLimitRefusal + `"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if got := strings.Join(rc.Stats["started"].([]string), ","); got != "A1,A2" || rc.Stats["controller_nodes_needed"] != 5 {
		t.Errorf("started %s, stats %v", got, rc.Stats)
	}
	inv, ok := rc.Inverse()
	if ids, _ := inv[sdk.InverseParamsKey].(map[string]any)["nodes"].([]string); !ok || len(ids) != 2 {
		t.Errorf("the refused start's undo: %v", inv)
	}
}

// TestStart_AnotherRefusalIsNotCalledCapacity: a 400 that does not name the
// node limit is reported as the controller said it, never as a full
// controller, so an agent is not sent to free capacity for nothing.
func TestStart_AnotherRefusalIsNotCalledCapacity(t *testing.T) {
	r, _ := fullController(t)
	r.ctl.Mu.Lock()
	r.ctl.NodeLimit = 0
	r.ctl.Refuse = map[string]int{"PUT /state/start": 400}
	r.ctl.Mu.Unlock()
	_, _, err := r.run("virt.cml.lab.start", false, map[string]any{"lab": "cvd"})
	if err == nil || strings.Contains(err.Error(), "license") || !strings.Contains(err.Error(), "refused by the test") {
		t.Errorf("a refusal that is not the limit: %v", err)
	}
}
