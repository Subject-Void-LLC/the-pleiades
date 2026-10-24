// Tests of the calls that change a controller, against cmltest's
// controller: a lab built, started, taken apart and deleted in the order
// the virt.cml methods use, and each refusal a real controller makes.
package cml_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// connected is a client logged in to ctl.
func connected(t *testing.T, ctl *cmltest.Controller) *cml.Client {
	t.Helper()
	c, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestWrite_BuildsALabAndTakesItApart: a lab, two IOL XE nodes with a
// day-0 configuration, a link between their first Ethernet ports, a start
// that waits until both boot, then a stop, the link removed, a node wiped
// and deleted, and the lab wiped and deleted, each step read back.
func TestWrite_BuildsALabAndTakesItApart(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()

	defs, err := c.NodeDefinitions(ctx)
	if err != nil || !slices.Contains(defs, "iol-xe") {
		t.Fatalf("NodeDefinitions = %v, %v", defs, err)
	}
	lab, err := c.CreateLab(ctx, "cvd")
	if err != nil || lab.Title != "cvd" || lab.State != cml.StateDefined {
		t.Fatalf("CreateLab = %+v, %v", lab, err)
	}
	r1, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "CORE1", Definition: "iol-xe", Configuration: "hostname CORE1"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "BR1", Definition: "iol-xe", X: 200})
	if err != nil {
		t.Fatal(err)
	}
	port := func(node string) cml.Interface {
		ifaces, err := c.NodeInterfaces(ctx, lab.ID, node)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range ifaces {
			if i.Label == "Ethernet0/0" {
				return i
			}
		}
		t.Fatalf("node %s has no Ethernet0/0: %+v", node, ifaces)
		return cml.Interface{}
	}
	a, b := port(r1), port(r2)
	if a.IsConnected || a.Slot == nil || *a.Slot != 0 {
		t.Errorf("a free port reads as %+v", a)
	}
	link, err := c.CreateLink(ctx, lab.ID, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLink(ctx, lab.ID, a.ID, b.ID); err == nil {
		t.Error("a second link on a connected interface was made")
	}
	links, err := c.Links(ctx, lab.ID)
	if err != nil || len(links) != 1 || links[0].ID != link || links[0].NodeA != r1 || links[0].NodeB != r2 {
		t.Errorf("Links = %+v, %v", links, err)
	}

	if err := c.StartLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLab(ctx, lab.ID); err == nil || !strings.Contains(err.Error(), "stop and wipe") {
		t.Errorf("deleting a running lab = %v, want the controller's refusal quoted", err)
	}
	if err := c.WaitBooted(ctx, lab.ID, nil, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	nodes, err := c.Nodes(ctx, lab.ID)
	if err != nil || len(nodes) != 2 || !cml.Running(nodes[0].State) {
		t.Fatalf("Nodes after the start = %+v, %v", nodes, err)
	}
	if got, err := c.Lab(ctx, lab.ID); err != nil || got.State != cml.StateStarted || got.ID != lab.ID {
		t.Errorf("Lab = %+v, %v", got, err)
	}
	if err := c.StopNode(ctx, lab.ID, r2); err != nil {
		t.Fatal(err)
	}
	if err := c.StartNode(ctx, lab.ID, r2); err != nil {
		t.Fatal(err)
	}
	if err := c.StopLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLink(ctx, lab.ID, link); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNode(ctx, lab.ID, r2); err == nil {
		t.Error("a stopped node that was not wiped was deleted")
	}
	if err := c.WipeNode(ctx, lab.ID, r2); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNode(ctx, lab.ID, r2); err != nil {
		t.Fatal(err)
	}
	if err := c.WipeLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	var se *cml.StatusError
	if _, err := c.Lab(ctx, lab.ID); !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Errorf("the deleted lab reads as %v", err)
	}
}

// TestWrite_Refusals: every write needs a login first; a node definition
// the controller lacks is its refusal; a wait that runs out names each node
// not booted and its state, and one a lab does not hold as missing.
func TestWrite_Refusals(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	ctx := context.Background()
	fresh, _ := cml.New(ctl.URL, ctl.Client(), cml.Credentials{})
	if _, err := fresh.CreateLab(ctx, "x"); err == nil || !strings.Contains(err.Error(), "Connect first") {
		t.Errorf("CreateLab before Connect = %v", err)
	}
	c := connected(t, ctl)
	lab, err := c.CreateLab(ctx, "waits")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "X", Definition: "no-such"}); err == nil || !strings.Contains(err.Error(), "node definition not found") {
		t.Errorf("an unknown definition = %v", err)
	}
	node, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "SLOW", Definition: "iosv"})
	if err != nil {
		t.Fatal(err)
	}
	ctl.Mu.Lock()
	ctl.BootPolls = 1 << 30
	ctl.Mu.Unlock()
	if err := c.StartLab(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err = c.WaitBooted(short, lab.ID, []string{node, "gone"}, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "SLOW (STARTED)") || !strings.Contains(err.Error(), "gone (missing)") || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitBooted = %v", err)
	}
}

// TestWrite_AMadeThingWithNoIDIsAnError: a controller that answers a
// create with no id is refused, since nothing could undo what it made.
func TestWrite_AMadeThingWithNoIDIsAnError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v0/system_information":
			_, _ = w.Write([]byte(`{"version":"2.7.2","ready":true}`))
		case "/api/v0/authenticate":
			_, _ = w.Write([]byte(`"tok"`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := client(t, srv, cml.Credentials{Username: "u"})
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateLab(ctx, "x"); err == nil || !strings.Contains(err.Error(), "no id") {
		t.Errorf("CreateLab = %v", err)
	}
	if _, err := c.CreateNode(ctx, "l", cml.NodeSpec{Label: "n", Definition: "iosv"}); err == nil || !strings.Contains(err.Error(), "no id") {
		t.Errorf("CreateNode = %v", err)
	}
	if _, err := c.CreateLink(ctx, "l", "a", "b"); err == nil || !strings.Contains(err.Error(), "no id") {
		t.Errorf("CreateLink = %v", err)
	}
}

// TestConsoleKey_ReadsANodesConsoleUUID: a node's first console line's UUID
// comes back as the controller gives it, and a node the lab lacks is the
// controller's refusal.
func TestConsoleKey_ReadsANodesConsoleUUID(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	lab, _ := c.CreateLab(ctx, "cvd")
	id, _ := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	key, err := c.ConsoleKey(ctx, lab.ID, id)
	if err != nil || key != "11111111-2222-4333-8444-"+id[len(id)-12:] {
		t.Errorf("ConsoleKey = %q, %v", key, err)
	}
	if _, err := c.ConsoleKey(ctx, lab.ID, "no-such-node"); err == nil {
		t.Error("a node the lab lacks had a console key")
	}
}

// TestCreateInterfaces_AddsEverySlotUpToTheOneAsked: an IOL node's slots
// 4 to 7 come as Ethernet1/0 to Ethernet1/3, slots it has are kept, and a
// slot out of range is refused before any call.
func TestCreateInterfaces_AddsEverySlotUpToTheOneAsked(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	lab, err := c.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	node, err := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "WAN1", Definition: "iol-xe"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CreateInterfaces(ctx, lab.ID, node, 7); err != nil {
		t.Fatal(err)
	}
	ifaces, err := c.NodeInterfaces(ctx, lab.ID, node)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, i := range ifaces {
		labels = append(labels, i.Label)
	}
	if !slices.Contains(labels, "Ethernet0/0") || !slices.Contains(labels, "Ethernet1/3") || len(labels) != 9 {
		t.Errorf("interfaces = %v, want Loopback0 and Ethernet0/0 to Ethernet1/3", labels)
	}
	calls := len(ctl.Calls)
	for _, slot := range []int{-1, cml.MaxSlots + 1} {
		if err := c.CreateInterfaces(ctx, lab.ID, node, slot); err == nil {
			t.Errorf("slot %d was accepted", slot)
		}
	}
	if len(ctl.Calls) != calls {
		t.Error("a slot out of range reached the controller")
	}
}
