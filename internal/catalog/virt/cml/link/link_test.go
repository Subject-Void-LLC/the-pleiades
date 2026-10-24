// Tests of the virt.cml.link.* methods against cmltest's controller: a link
// made and its undo, the same link again unchanged, an interface already in
// use refused, and a link removed by its id or its ends with an undo that
// makes it again.
package link_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/cml/link"
)

// run calls fqcn on ctl (its check with check set) with params.
func run(t *testing.T, ctl *cmltest.Controller, fqcn string, check bool, params map[string]any) (collection.Result, *cmltest.Context, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	call := d.Invoke
	if check {
		call = d.Check
	}
	rc := cmltest.NewContext(ctl)
	res, err := call(context.Background(), rc, cmltest.NewDevice(t, ctl), params)
	return res, rc, err
}

// ends are CORE1's and BR1's Ethernet0/1, the link both tests make.
var ends = map[string]any{"lab": "cvd", "a": "CORE1", "a_interface": "Ethernet0/1", "b": "BR1", "b_interface": "Ethernet0/1"}

// withLab starts a controller holding lab cvd with nodes CORE1, BR1 and
// BR2.
func withLab(t *testing.T) (*cmltest.Controller, string) {
	t.Helper()
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"CORE1", "BR1", "BR2"} {
		if _, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"}); err != nil {
			t.Fatal(err)
		}
	}
	return ctl, lab.ID
}

// with returns ends with over applied.
func with(over map[string]any) map[string]any {
	p := map[string]any{}
	for k, v := range ends {
		p[k] = v
	}
	for k, v := range over {
		p[k] = v
	}
	return p
}

// TestCreate_LinksOnceAndRefusesAnInterfaceInUse: a check links nothing; a
// link is made and its undo removes it by id; the same link again is
// unchanged, with no undo; and an end already linked elsewhere, an end that
// does not exist, and both ends on one interface are refused.
func TestCreate_LinksOnceAndRefusesAnInterfaceInUse(t *testing.T) {
	ctl, lab := withLab(t)
	if res, _, err := run(t, ctl, "virt.cml.link.create", true, ends); err != nil || !res.Changed || len(ctl.Labs[lab].Links) != 0 {
		t.Fatalf("check: %+v %v", res, err)
	}
	res, rc, err := run(t, ctl, "virt.cml.link.create", false, ends)
	if err != nil || !res.Changed || len(ctl.Labs[lab].Links) != 1 {
		t.Fatalf("create: %+v %v", res, err)
	}
	id, _ := rc.Stats["id"].(string)
	if inv, ok := rc.Inverse(); !ok || inv[sdk.InverseFQCNKey] != "virt.cml.link.delete" || inv["params"].(map[string]any)["link"] != id {
		t.Errorf("the undo: %v", inv)
	}
	res, rc, err = run(t, ctl, "virt.cml.link.create", false, with(map[string]any{"a": "BR1", "b": "CORE1"}))
	if err != nil || res.Changed || rc.Stats["id"] != id {
		t.Errorf("the same link, ends swapped: %+v %v %v", res, err, rc.Stats)
	}
	if _, ok := rc.Inverse(); ok {
		t.Error("an unchanged link recorded an undo")
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"an end in use":         {with(map[string]any{"b": "BR2"}), "already linked"},
		"an interface it lacks": {with(map[string]any{"b_interface": "Ethernet9/9"}), "no interface"},
		"one interface twice":   {with(map[string]any{"a": "BR2", "a_interface": "Ethernet0/2", "b": "BR2", "b_interface": "Ethernet0/2"}), "same one"},
		"a missing end":         {with(map[string]any{"b_interface": ""}), "all four"},
		"no lab":                {with(map[string]any{"lab": "nothing"}), "not found"},
	} {
		if _, _, err := run(t, ctl, "virt.cml.link.create", false, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
}

// TestDelete_ByIDOrEndsWithAnUndoThatLinksAgain: a link removed by its ends
// records the create of the same two interfaces by label; one removed by id
// the same; a link, lab or end that does not exist is unchanged; and naming
// it both ways, or by part of its ends, is refused.
func TestDelete_ByIDOrEndsWithAnUndoThatLinksAgain(t *testing.T) {
	ctl, lab := withLab(t)
	if _, _, err := run(t, ctl, "virt.cml.link.create", false, ends); err != nil {
		t.Fatal(err)
	}
	if res, _, err := run(t, ctl, "virt.cml.link.delete", true, ends); err != nil || !res.Changed || len(ctl.Labs[lab].Links) != 1 {
		t.Fatalf("check: %+v %v", res, err)
	}
	res, rc, err := run(t, ctl, "virt.cml.link.delete", false, with(map[string]any{"a": "BR1", "b": "CORE1"}))
	if err != nil || !res.Changed || len(ctl.Labs[lab].Links) != 0 {
		t.Fatalf("delete by ends: %+v %v", res, err)
	}
	inv, ok := rc.Inverse()
	undo, _ := inv["params"].(map[string]any)
	if !ok || inv[sdk.InverseFQCNKey] != "virt.cml.link.create" || undo["lab"] != lab || undo["a"] == undo["b"] || undo["a_interface"] != "Ethernet0/1" {
		t.Fatalf("the undo: %v", inv)
	}
	// The undo, run as a rollback would, makes the same link again.
	if _, _, err := run(t, ctl, "virt.cml.link.create", false, undo); err != nil || len(ctl.Labs[lab].Links) != 1 {
		t.Fatalf("the undo did not link again: %v", err)
	}
	var id string
	for id = range ctl.Labs[lab].Links {
	}
	if res, _, err := run(t, ctl, "virt.cml.link.delete", false, map[string]any{"lab": "cvd", "link": id}); err != nil || !res.Changed || len(ctl.Labs[lab].Links) != 0 {
		t.Errorf("delete by id: %+v %v", res, err)
	}
	for name, params := range map[string]map[string]any{
		"a link that is gone": ends,
		"an id that is gone":  {"lab": "cvd", "link": id},
		"a lab that is gone":  with(map[string]any{"lab": "nothing"}),
		"a node that is gone": with(map[string]any{"a": "R9"}),
	} {
		if res, _, err := run(t, ctl, "virt.cml.link.delete", false, params); err != nil || res.Changed {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	for name, params := range map[string]map[string]any{
		"both ways":   with(map[string]any{"link": id}),
		"part of it":  {"lab": "cvd", "a": "CORE1"},
		"neither way": {"lab": "cvd"},
	} {
		if _, _, err := run(t, ctl, "virt.cml.link.delete", false, params); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestLink_FailureWalk: each call a link method makes, failed by the
// controller in turn, fails the task, and so does a login the controller
// refuses.
func TestLink_FailureWalk(t *testing.T) {
	for _, call := range []string{"GET /labs", "GET /nodes", "/interfaces", "GET /links", "POST /links"} {
		ctl, _ := withLab(t)
		ctl.Fail = map[string]bool{call: true}
		if _, _, err := run(t, ctl, "virt.cml.link.create", false, ends); err == nil {
			t.Errorf("create with %s failing: no error", call)
		}
	}
	for _, call := range []string{"GET /nodes", "GET /links", "/interfaces", "DELETE /links/"} {
		ctl, lab := withLab(t)
		if _, _, err := run(t, ctl, "virt.cml.link.create", false, ends); err != nil {
			t.Fatal(err)
		}
		if call == "DELETE /links/" {
			for id := range ctl.Labs[lab].Links {
				call = "DELETE /labs/" + lab + "/links/" + id
			}
		}
		ctl.Fail = map[string]bool{call: true}
		if _, _, err := run(t, ctl, "virt.cml.link.delete", false, ends); err == nil {
			t.Errorf("delete with %s failing: no error", call)
		}
	}
	ctl, _ := withLab(t)
	ctl.Mu.Lock()
	ctl.Password = "rotated"
	ctl.Mu.Unlock()
	for _, fqcn := range []string{"virt.cml.link.create", "virt.cml.link.delete"} {
		d, _ := collection.Lookup(fqcn)
		rc := cmltest.NewContext(ctl)
		rc.Secrets["password"] = "wrong"
		if _, err := d.Invoke(context.Background(), rc, cmltest.NewDevice(t, ctl), ends); err == nil || !strings.Contains(err.Error(), "refused the login") {
			t.Errorf("%s with the login refused: %v", fqcn, err)
		}
	}
}
