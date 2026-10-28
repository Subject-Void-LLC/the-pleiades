// Tests of what virt.cml.lab.info and virt.cml.lab.start report, against
// cmltest's controller: a missing lab read in a run and in a check, a lab's
// whole state with each interface's link and peer and the controller's
// running-node count, and a start refused partway reporting only the nodes
// it started, the same set its undo stops.
package lab_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestInfo_AMissingLabIsExistsFalseInBothModes: a lab the controller does
// not hold is exists false, with the controller's count, in a run and in a
// check alike, as the method's Doc says (runs 4 and 6 found the check
// failing with "cannot be checked").
func TestInfo_AMissingLabIsExistsFalseInBothModes(t *testing.T) {
	r, client := newRunner(t)
	ctx := context.Background()
	other, _ := client.CreateLab(ctx, "other")
	id, _ := client.CreateNode(ctx, other.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	_ = client.StartNode(ctx, other.ID, id)
	for _, check := range []bool{false, true} {
		res, rc, err := r.run("virt.cml.lab.info", check, map[string]any{"lab": "later"})
		if err != nil || res.Changed || rc.Stats["exists"] != false {
			t.Errorf("check %v: %+v %v %v", check, res, err, rc.Stats)
		}
		if rc.Stats["controller_nodes_running"] != 1 || rc.Stats["controller_labs"] != 1 {
			t.Errorf("check %v: the controller's count %v", check, rc.Stats)
		}
		if _, ok := rc.Stats["nodes"]; ok {
			t.Errorf("check %v: a missing lab reported nodes", check)
		}
	}
}

// TestInfo_ReportsTheWholeLab: each node's state, each interface's link
// with the link's state and the peer at its other end, each link's state,
// and the nodes running across every lab, in a run and in a check; a lab
// the login may not read is left out of the count.
func TestInfo_ReportsTheWholeLab(t *testing.T) {
	r, client := newRunner(t)
	ctx := context.Background()
	a, _ := client.CreateLab(ctx, "cvd")
	r1, _ := client.CreateNode(ctx, a.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	r2, _ := client.CreateNode(ctx, a.ID, cml.NodeSpec{Label: "R2", Definition: "iol-xe"})
	nodes, _ := client.Nodes(ctx, a.ID)
	e1, err := client.FindEndpoint(ctx, a.ID, nodes, "R1", "Ethernet0/0")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := client.FindEndpoint(ctx, a.ID, nodes, "R2", "Ethernet0/1")
	if err != nil {
		t.Fatal(err)
	}
	link, err := client.CreateLink(ctx, a.ID, e1.Interface.ID, e2.Interface.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.StopLink(ctx, a.ID, link)
	_ = client.StartNode(ctx, a.ID, r1)
	b, _ := client.CreateLab(ctx, "neighbour")
	for _, label := range []string{"B1", "B2", "B3"} {
		id, _ := client.CreateNode(ctx, b.ID, cml.NodeSpec{Label: label, Definition: "iosv"})
		if label != "B3" {
			_ = client.StartNode(ctx, b.ID, id)
		}
	}

	for _, check := range []bool{false, true} {
		res, rc, err := r.run("virt.cml.lab.info", check, map[string]any{"lab": "cvd"})
		if err != nil || res.Changed || rc.Stats["exists"] != true {
			t.Fatalf("check %v: %+v %v", check, res, err)
		}
		if rc.Stats["controller_nodes_running"] != 3 || rc.Stats["controller_labs"] != 2 {
			t.Errorf("check %v: the controller's count %v %v", check, rc.Stats["controller_nodes_running"], rc.Stats["controller_labs"])
		}
		got := rc.Stats["nodes"].([]any)
		if len(got) != 2 {
			t.Fatalf("check %v: nodes %v", check, got)
		}
		n1, n2 := got[0].(map[string]any), got[1].(map[string]any)
		if n1["label"] != "R1" || n1["definition"] != "iol-xe" || !cml.Running(n1["state"].(string)) || n2["state"] != cml.StateDefined || n2["id"] != r2 {
			t.Errorf("check %v: nodes %v %v", check, n1, n2)
		}
		want := map[string]map[string]any{
			"R1": {"label": "Ethernet0/0", "connected": true, "link": link, "link_state": cml.StateStopped, "peer": "R2", "peer_interface": "Ethernet0/1"},
			"R2": {"label": "Ethernet0/1", "connected": true, "link": link, "link_state": cml.StateStopped, "peer": "R1", "peer_interface": "Ethernet0/0"},
		}
		for _, n := range []map[string]any{n1, n2} {
			linked := 0
			for _, i := range n["interfaces"].([]any) {
				iface := i.(map[string]any)
				if _, on := iface["link"]; !on {
					if iface["connected"] != false || iface["peer"] != nil {
						t.Errorf("check %v: %s's free interface %v", check, n["label"], iface)
					}
					continue
				}
				linked++
				for k, v := range want[n["label"].(string)] {
					if iface[k] != v {
						t.Errorf("check %v: %s's %s = %v, want %v", check, n["label"], k, iface[k], v)
					}
				}
			}
			if linked != 1 {
				t.Errorf("check %v: %s has %d linked interfaces, want 1", check, n["label"], linked)
			}
		}
		links := rc.Stats["links"].([]any)
		if len(links) != 1 || links[0].(map[string]any)["state"] != cml.StateStopped || links[0].(map[string]any)["a"] != "R1" {
			t.Errorf("check %v: links %v", check, links)
		}
	}

	// A lab whose nodes this login may not read is not counted.
	r.ctl.Mu.Lock()
	r.ctl.Refuse = map[string]int{"GET /labs/" + b.ID + "/nodes": 403}
	r.ctl.Mu.Unlock()
	if _, rc, err := r.run("virt.cml.lab.info", false, map[string]any{"lab": "cvd"}); err != nil || rc.Stats["controller_nodes_running"] != 1 || rc.Stats["controller_labs"] != 1 {
		t.Errorf("a lab closed to the login: %v %v", err, rc.Stats)
	}
}

// TestStart_ReportsOnlyWhatItStarted: a start the controller refuses partway
// (run 6's 400) reports in started only the nodes it started, exactly the
// ones its undo stops, and names the nodes already running apart; the
// controller's own words reach the error. The next start reports the rest.
func TestStart_ReportsOnlyWhatItStarted(t *testing.T) {
	r, client := newRunner(t)
	ctx := context.Background()
	l, _ := client.CreateLab(ctx, "cvd")
	ids := map[string]string{}
	for _, label := range []string{"A1", "A2", "A3", "A4"} {
		ids[label], _ = client.CreateNode(ctx, l.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"})
	}
	_ = client.StartNode(ctx, l.ID, ids["A1"])
	r.ctl.Mu.Lock()
	r.ctl.Refuse = map[string]int{"PUT /nodes/" + ids["A3"] + "/state/start": 400}
	r.ctl.Mu.Unlock()

	if res, rc, err := r.run("virt.cml.lab.start", true, map[string]any{"lab": "cvd"}); err != nil || !res.Changed ||
		strings.Join(rc.Stats["started"].([]string), ",") != "A2,A3,A4" || strings.Join(rc.Stats["already_running"].([]string), ",") != "A1" {
		t.Errorf("check: %+v %v %v", res, err, rc.Stats)
	}

	res, rc, err := r.run("virt.cml.lab.start", false, map[string]any{"lab": "cvd", "timeout": 30})
	if err == nil || !strings.Contains(err.Error(), "refused by the test") || !res.Changed {
		t.Fatalf("a start refused partway: %+v %v", res, err)
	}
	if got := strings.Join(rc.Stats["started"].([]string), ","); got != "A2" {
		t.Errorf("started %s, want A2 alone", got)
	}
	if got := strings.Join(rc.Stats["already_running"].([]string), ","); got != "A1" {
		t.Errorf("already_running %s, want A1", got)
	}
	inv, ok := rc.Inverse()
	undo, _ := inv[sdk.InverseParamsKey].(map[string]any)
	if got, _ := undo["nodes"].([]string); !ok || strings.Join(got, ",") != ids["A2"] {
		t.Errorf("the undo stops %v, want A2's id alone", inv)
	}
	if r.ctl.Labs[l.ID].Nodes[ids["A4"]].State != cml.StateDefined {
		t.Error("a node after the refusal was started")
	}

	r.ctl.Mu.Lock()
	r.ctl.Refuse = nil
	r.ctl.Mu.Unlock()
	res, rc, err = r.run("virt.cml.lab.start", false, map[string]any{"lab": "cvd", "timeout": 30})
	if err != nil || !res.Changed || strings.Join(rc.Stats["started"].([]string), ",") != "A3,A4" || strings.Join(rc.Stats["already_running"].([]string), ",") != "A1,A2" {
		t.Errorf("the second start: %+v %v %v", res, err, rc.Stats)
	}
	inv, _ = rc.Inverse()
	undo, _ = inv[sdk.InverseParamsKey].(map[string]any)
	if got, _ := undo["nodes"].([]string); strings.Join(got, ",") != ids["A3"]+","+ids["A4"] {
		t.Errorf("the second undo stops %v", undo)
	}
}
