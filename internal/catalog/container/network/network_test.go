// A network's whole life through the container.network.* methods, against
// this machine's real Docker engine.
package network_test

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// call runs fqcn's Invoke, or its Check, with params.
func call(t *testing.T, h *harness, rc *recorder, fqcn string, check bool, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, h.device, params)
	}
	return d.Invoke(context.Background(), rc, h.device, params)
}

// inverseOf is the undo a run recorded.
func inverseOf(rc *recorder) (string, map[string]any, bool) {
	inv, _ := rc.stats["inverse"].(map[string]any)
	fqcn, _ := inv["fqcn"].(string)
	params, _ := inv["params"].(map[string]any)
	partial, _ := inv["partial"].(bool)
	return fqcn, params, partial
}

// networkExists reports whether the engine has a network named name.
func (h *harness) networkExists(t *testing.T, name string) (string, bool) {
	t.Helper()
	res, err := h.api.NetworkInspect(context.Background(), name, client.NetworkInspectOptions{})
	if err != nil || res.Network.Name != name {
		return "", false
	}
	return res.Network.ID, true
}

// TestNetwork_ALifeThroughTheMethods: a check of create makes nothing;
// create makes the network and records its removal by ID; a second create
// is no change and a differing one is refused; info reads it; connect and
// disconnect record each other and converge; remove refuses a network a
// container uses, refuses another network's ID, removes, and records a
// create that, replayed, makes the network again.
func TestNetwork_ALifeThroughTheMethods(t *testing.T) {
	h := newHarness(t)
	net := h.networkName(t)
	declared := map[string]any{"name": net, "subnet": "10.231.17.0/24", "gateway": "10.231.17.1", "labels": map[string]any{"app": "pleiades-test"}}

	if res, err := call(t, h, fresh(), "container.network.create", true, declared); err != nil || !res.Changed {
		t.Fatalf("a check of create = %+v, %v", res, err)
	}
	if _, ok := h.networkExists(t, net); ok {
		t.Fatal("a check created the network")
	}
	rc := fresh()
	if res, err := call(t, h, rc, "container.network.create", false, declared); err != nil || !res.Changed {
		t.Fatalf("create = %+v, %v", res, err)
	}
	id, ok := h.networkExists(t, net)
	if !ok || rc.stats["id"] != id {
		t.Fatalf("create left %q (exists %t), stat id %v", id, ok, rc.stats["id"])
	}
	if fqcn, p, _ := inverseOf(rc); fqcn != "container.network.remove" || p["id"] != id || p["name"] != net {
		t.Errorf("create's undo = %s %v", fqcn, p)
	}
	if res, err := call(t, h, fresh(), "container.network.create", false, declared); err != nil || res.Changed {
		t.Errorf("a second create = %+v, %v; want no change", res, err)
	}
	if res, err := call(t, h, fresh(), "container.network.create", false, map[string]any{"name": net}); err != nil || res.Changed {
		t.Errorf("a create leaving the subnet to the engine = %+v, %v; want no change", res, err)
	}
	differing := map[string]any{"name": net, "internal": true, "subnet": "10.231.18.0/24"}
	if _, err := call(t, h, fresh(), "container.network.create", true, differing); err == nil || !strings.Contains(err.Error(), "internal: false") || !strings.Contains(err.Error(), "subnet: 10.231.18.0/24") {
		t.Errorf("a differing create = %v, want it refused naming each difference", err)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.network.info", true, map[string]any{"name": net}); err != nil {
		t.Fatal(err)
	}
	subnets, _ := rc.stats["subnets"].([]map[string]any)
	if rc.stats["exists"] != true || rc.stats["driver"] != "bridge" || len(subnets) != 1 || subnets[0]["subnet"] != "10.231.17.0/24" || subnets[0]["gateway"] != "10.231.17.1" {
		t.Errorf("info = %v", rc.stats)
	}
	if labels, _ := rc.stats["labels"].(map[string]string); labels["app"] != "pleiades-test" {
		t.Errorf("info's labels = %v", rc.stats["labels"])
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.network.info", false, map[string]any{"name": net + "-absent"}); err != nil || rc.stats["exists"] != false {
		t.Errorf("info of an absent network = %v, %v", rc.stats, err)
	}

	ctr, ctrID := h.container(t, "bridge")
	join := map[string]any{"network": net, "container": ctr, "aliases": []any{"shop"}, "ipv4_address": "10.231.17.50"}
	if res, err := call(t, h, fresh(), "container.network.connect", true, join); err != nil || !res.Changed {
		t.Fatalf("a check of connect = %+v, %v", res, err)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.network.connect", false, join); err != nil || !res.Changed {
		t.Fatalf("connect = %+v, %v", res, err)
	}
	if fqcn, p, _ := inverseOf(rc); fqcn != "container.network.disconnect" || p["network_id"] != id || p["container_id"] != ctrID {
		t.Errorf("connect's undo = %s %v", fqcn, p)
	}
	if res, err := call(t, h, fresh(), "container.network.connect", false, join); err != nil || res.Changed {
		t.Errorf("a second connect = %+v, %v; want no change", res, err)
	}
	if _, err := call(t, h, fresh(), "container.network.connect", false, map[string]any{"network": net, "container": ctr, "aliases": []any{"other"}}); err == nil || !strings.Contains(err.Error(), "aliases: other") {
		t.Errorf("a connect with another alias = %v", err)
	}

	if _, err := call(t, h, fresh(), "container.network.remove", true, map[string]any{"name": net}); err == nil || !strings.Contains(err.Error(), ctr) {
		t.Errorf("removing a network in use = %v, want it refused naming %s", err, ctr)
	}

	rc = fresh()
	if res, err := call(t, h, rc, "container.network.disconnect", false, map[string]any{"network": net, "container": ctr, "container_id": ctrID}); err != nil || !res.Changed {
		t.Fatalf("disconnect = %+v, %v", res, err)
	}
	fqcn, back, _ := inverseOf(rc)
	if aliases, _ := back["aliases"].([]string); fqcn != "container.network.connect" || back["ipv4_address"] != "10.231.17.50" || !contains(aliases, "shop") {
		t.Errorf("disconnect's undo = %s %v", fqcn, back)
	}
	if res, err := call(t, h, fresh(), "container.network.disconnect", false, map[string]any{"network": net, "container": ctr}); err != nil || res.Changed {
		t.Errorf("a second disconnect = %+v, %v; want no change", res, err)
	}
	// The undo, replayed, puts the container back as it was.
	if res, err := call(t, h, fresh(), fqcn, false, back); err != nil || !res.Changed {
		t.Fatalf("replaying disconnect's undo = %+v, %v", res, err)
	}
	if res, err := call(t, h, fresh(), "container.network.disconnect", false, map[string]any{"network": net, "container": ctr}); err != nil || !res.Changed {
		t.Fatalf("disconnecting again = %+v, %v", res, err)
	}

	isolated, _ := h.container(t, "none")
	if _, err := call(t, h, fresh(), "container.network.connect", true, map[string]any{"network": net, "container": isolated}); err == nil || !strings.Contains(err.Error(), "container.recreate") {
		t.Errorf("connecting a container with network none = %v", err)
	}

	other := strings.Repeat("0", 64)
	if _, err := call(t, h, fresh(), "container.network.remove", false, map[string]any{"name": net, "id": other}); err == nil || !strings.Contains(err.Error(), "does nothing to it") {
		t.Errorf("removing with another network's ID = %v", err)
	}
	if res, err := call(t, h, fresh(), "container.network.remove", true, map[string]any{"name": net}); err != nil || !res.Changed {
		t.Errorf("a check of remove = %+v, %v", res, err)
	}
	if _, ok := h.networkExists(t, net); !ok {
		t.Fatal("a check removed the network")
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.network.remove", false, map[string]any{"name": net, "id": id}); err != nil || !res.Changed {
		t.Fatalf("remove = %+v, %v", res, err)
	}
	if _, ok := h.networkExists(t, net); ok {
		t.Fatal("remove left the network")
	}
	fqcn, recreate, partial := inverseOf(rc)
	if fqcn != "container.network.create" || recreate["subnet"] != "10.231.17.0/24" || recreate["gateway"] != "10.231.17.1" || partial {
		t.Errorf("remove's undo = %s %v, partial %t", fqcn, recreate, partial)
	}
	if res, err := call(t, h, fresh(), "container.network.remove", false, map[string]any{"name": net}); err != nil || res.Changed {
		t.Errorf("removing an absent network = %+v, %v; want no change", res, err)
	}
	// The undo, replayed, makes the network again as it was.
	if res, err := call(t, h, fresh(), fqcn, false, recreate); err != nil || !res.Changed {
		t.Fatalf("replaying remove's undo = %+v, %v", res, err)
	}
	if res, err := call(t, h, fresh(), "container.network.create", false, declared); err != nil || res.Changed {
		t.Errorf("the declaration after the undo = %+v, %v; want no change", res, err)
	}
}

// TestNetwork_TheEnginesOwnNetworksAreRefused: bridge is never created or
// removed by a task, and host is never joined.
func TestNetwork_TheEnginesOwnNetworksAreRefused(t *testing.T) {
	h := newHarness(t)
	if _, err := call(t, h, fresh(), "container.network.remove", true, map[string]any{"name": "bridge"}); err == nil || !strings.Contains(err.Error(), "engine's own") {
		t.Errorf("removing bridge = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.network.create", true, map[string]any{"name": "bridge"}); err == nil || !strings.Contains(err.Error(), "engine's own") {
		t.Errorf("creating bridge = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.network.connect", true, map[string]any{"network": "host", "container": "x"}); err == nil || !strings.Contains(err.Error(), "network mode") {
		t.Errorf("joining host = %v", err)
	}
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
