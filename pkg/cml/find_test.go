// Tests of finding labs, nodes, interfaces and links by what a person
// calls them, and of the client a task logs in through, against cmltest's
// controller.
package cml_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestFind_ByIDOrNameAndNeverAGuess: a lab is found by its id or its
// title, a title two labs share is refused naming both, and a name nothing
// has is ErrNotFound; the same for a node's label and an interface's.
func TestFind_ByIDOrNameAndNeverAGuess(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	one, _ := c.CreateLab(ctx, "one")
	a, _ := c.CreateLab(ctx, "twin")
	b, _ := c.CreateLab(ctx, "twin")
	for ref, want := range map[string]string{one.ID: one.ID, "one": one.ID} {
		if got, err := c.FindLab(ctx, ref); err != nil || got.ID != want {
			t.Errorf("FindLab(%q) = %+v, %v", ref, got, err)
		}
	}
	if _, err := c.FindLab(ctx, "twin"); err == nil || !strings.Contains(err.Error(), a.ID) || !strings.Contains(err.Error(), b.ID) {
		t.Errorf("a shared title: %v", err)
	}
	if _, err := c.FindLab(ctx, "none"); !errors.Is(err, cml.ErrNotFound) {
		t.Errorf("no such lab: %v", err)
	}
	if _, err := c.FindLab(ctx, ""); err == nil {
		t.Error("an empty name found a lab")
	}

	r1, _ := c.CreateNode(ctx, one.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	r2, _ := c.CreateNode(ctx, one.ID, cml.NodeSpec{Label: "R2", Definition: "iol-xe"})
	_, _ = c.CreateNode(ctx, one.ID, cml.NodeSpec{Label: "R2", Definition: "iol-xe"})
	nodes, err := c.Nodes(ctx, one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := cml.FindNode(nodes, "R1"); err != nil || n.ID != r1 {
		t.Errorf("FindNode(R1) = %+v, %v", n, err)
	}
	if n, err := cml.FindNode(nodes, r2); err != nil || n.ID != r2 {
		t.Errorf("FindNode(id) = %+v, %v", n, err)
	}
	if _, err := cml.FindNode(nodes, "R2"); err == nil || !strings.Contains(err.Error(), "2 nodes") {
		t.Errorf("a shared label: %v", err)
	}
	if _, err := cml.FindNode(nodes, "R9"); !errors.Is(err, cml.ErrNotFound) {
		t.Errorf("no such node: %v", err)
	}

	end, err := c.FindEndpoint(ctx, one.ID, nodes, "R1", "Ethernet0/1")
	if err != nil || end.Node.ID != r1 || end.Interface.Label != "Ethernet0/1" {
		t.Fatalf("FindEndpoint = %+v, %v", end, err)
	}
	if _, err := c.FindEndpoint(ctx, one.ID, nodes, "R1", "Ethernet9/9"); !errors.Is(err, cml.ErrNotFound) || !strings.Contains(err.Error(), "Ethernet0/0") {
		t.Errorf("no such interface: %v", err)
	}
	if _, err := c.FindEndpoint(ctx, one.ID, nodes, "R9", "Ethernet0/1"); !errors.Is(err, cml.ErrNotFound) {
		t.Errorf("an end on no node: %v", err)
	}
	other, _ := c.FindEndpoint(ctx, one.ID, nodes, r2, "Ethernet0/1")
	id, err := c.CreateLink(ctx, one.ID, end.Interface.ID, other.Interface.ID)
	if err != nil {
		t.Fatal(err)
	}
	links, _ := c.Links(ctx, one.ID)
	for _, pair := range [][2]string{{end.Interface.ID, other.Interface.ID}, {other.Interface.ID, end.Interface.ID}} {
		if l, ok := cml.Between(links, pair[0], pair[1]); !ok || l.ID != id {
			t.Errorf("Between(%v) = %+v, %v", pair, l, ok)
		}
	}
	if _, ok := cml.Between(links, end.Interface.ID, "nothing"); ok {
		t.Error("Between found a link that is not there")
	}
}

// TestClientFor_LogsInThroughTheDevice: a client built from a controller
// device logs in with the device's own login through its pinned
// certificate; a device that is not a controller, one with no login stored,
// and a login the controller refuses are each an error.
func TestClientFor_LogsInThroughTheDevice(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	device := cmltest.NewDevice(t, ctl)
	login := map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "pw"}
	client, version, warnings, err := cml.ClientFor(context.Background(), device, "cml", login)
	if err != nil || client == nil || version != "2.7.2+build.26" || len(warnings) != 1 || !strings.Contains(warnings[0], "pinned certificate") {
		t.Fatalf("ClientFor = %v, %q, %q, %v", client, version, warnings, err)
	}
	if _, _, _, err := cml.ClientFor(context.Background(), &inventorytest.Stub{}, "x", login); err == nil {
		t.Error("a device that is not a controller was logged in to")
	}
	if _, _, _, err := cml.ClientFor(context.Background(), device, "cml", map[string]string{wire.SecretUsername: "admin"}); err == nil || !strings.Contains(err.Error(), "add-credential cml") {
		t.Errorf("no password stored: %v", err)
	}
	wrong := map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "nope"}
	if _, _, warnings, err := cml.ClientFor(context.Background(), device, "cml", wrong); err == nil || len(warnings) != 1 {
		t.Errorf("a refused login: %v, warnings %q", err, warnings)
	}

	rc := cmltest.NewContext(ctl)
	if _, err := cml.ForTask(context.Background(), rc, device); err != nil {
		t.Fatal(err)
	}
	if w, _ := rc.Stats["warnings"].([]string); len(w) != 1 {
		t.Errorf("the task's warnings: %v", rc.Stats)
	}
	rc.Secrets[wire.SecretPassword] = "nope"
	if _, err := cml.ForTask(context.Background(), rc, device); err == nil {
		t.Error("ForTask logged in with a refused password")
	}
}
