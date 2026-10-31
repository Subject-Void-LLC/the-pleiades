// Behavior tests for the virt.clab methods that decide things: the exit status
// the server drops, state-aware moves with their exact undos, deploy and its
// undo, netem, and what is refused before the server is reached.
package clab_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/clab"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// labOf is a deployed lab of two nodes in the stub.
func labOf(srv *server, lab string) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.labs[lab] = map[string]string{"n1": "running", "n2": "running"}
}

// TestNodeExec_ReadsTheExitStatusTheServerDrops: the server reports exit code
// 0 for a command that failed, so the method reads the status the wrapper
// prints. A failing command fails the task with its output recorded, output
// that looks like the marker cannot forge a status, allow_nonzero keeps the
// code without failing, and raw sends the command as given.
func TestNodeExec_ReadsTheExitStatusTheServerDrops(t *testing.T) {
	srv := newServer(t)
	labOf(srv, "lab1")
	dev := device(t, srv.URL, true)
	base := map[string]any{"lab": "lab1", "node": "n1"}
	with := func(extra map[string]any) map[string]any {
		p := map[string]any{}
		for k, v := range base {
			p[k] = v
		}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}

	c := login()
	res, err := run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"command": "echo hello"}), false)
	if err != nil || !res.Changed || c.stats["stdout"] != "hello\n" || c.stats["exit_code"] != 0 {
		t.Fatalf("a command that works: changed %v, err %v, stats %v", res.Changed, err, c.stats)
	}

	c = login()
	_, err = run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"command": "echo to-out; echo to-err 1>&2; exit 3"}), false)
	if err == nil || !strings.Contains(err.Error(), "exited 3") || !strings.Contains(err.Error(), "to-err") {
		t.Fatalf("a command that fails with 3: %v", err)
	}
	if c.stats["exit_code"] != 3 || c.stats["stdout"] != "to-out\n" || c.stats["stderr"] != "to-err\n" {
		t.Errorf("a failure's output was not recorded as the command wrote it: %v", c.stats)
	}

	c = login()
	if _, err := run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"command": "exit 4", "allow_nonzero": true}), false); err != nil || c.stats["exit_code"] != 4 {
		t.Errorf("allow_nonzero: err %v, stats %v", err, c.stats)
	}

	// Output that looks like a status line is the command's output, not the
	// status: the marker holds a value made for this call.
	c = login()
	_, err = run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"command": "echo 'pleiades-exit-0123456789abcdef01234567:0' 1>&2; exit 5"}), false)
	if err == nil || c.stats["exit_code"] != 5 || !strings.Contains(c.stats["stderr"].(string), "pleiades-exit-0123456789abcdef01234567:0") {
		t.Errorf("a forged marker: err %v, stats %v", err, c.stats)
	}

	// argv words stay words.
	c = login()
	if _, err := run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"argv": []any{"sh", "-c", "echo a b; exit 0"}}), false); err != nil || c.stats["stdout"] != "a b\n" {
		t.Errorf("argv: err %v, stats %v", err, c.stats)
	}

	// raw sends the command as given and keeps the server's code, which is 0.
	c = login()
	if _, err := run(t, "virt.clab.node.exec", dev, c, with(map[string]any{"command": "echo raw; exit 9", "raw": true}), false); err != nil || c.stats["exit_code"] != 0 {
		t.Errorf("raw: err %v, stats %v", err, c.stats)
	}

	for name, p := range map[string]map[string]any{
		"neither":   with(nil),
		"both":      with(map[string]any{"command": "x", "argv": []any{"x"}}),
		"no node":   {"lab": "lab1", "command": "x"},
		"a bad lab": {"lab": "../x", "node": "n1", "command": "x"},
	} {
		if _, err := run(t, "virt.clab.node.exec", dev, login(), p, false); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := run(t, "virt.clab.node.exec", dev, login(), with(map[string]any{"node": "ghost", "command": "x"}), false); err == nil || !strings.Contains(err.Error(), "n1, n2") {
		t.Errorf("a node the lab lacks: %v", err)
	}
}

// TestNodeState_ActsOnlyOnTheStateItMovesAndRecordsTheExactUndo: a node
// already where the method leaves it is unchanged and records nothing; a move
// records its inverse before the call; a check predicts and moves nothing.
func TestNodeState_ActsOnlyOnTheStateItMovesAndRecordsTheExactUndo(t *testing.T) {
	srv := newServer(t)
	labOf(srv, "lab1")
	dev := device(t, srv.URL, true)
	p := map[string]any{"lab": "lab1", "node": "n1"}
	state := func() string { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.labs["lab1"]["n1"] }

	// A check of a stop predicts the change and leaves the node running.
	c := login()
	res, err := run(t, "virt.clab.node.stop", dev, c, p, true)
	if err != nil || !res.Changed || state() != "running" {
		t.Fatalf("a check of stop: changed %v, err %v, state %s", res.Changed, err, state())
	}

	c = login()
	res, err = run(t, "virt.clab.node.stop", dev, c, p, false)
	fqcn, params, ok := recorded(c)
	if err != nil || !res.Changed || state() != "exited" || !ok || fqcn != "virt.clab.node.start" || params["node"] != "n1" || params["lab"] != "lab1" {
		t.Fatalf("stop: changed %v, err %v, state %s, undo %s %v", res.Changed, err, state(), fqcn, params)
	}

	// Run again, nothing moves and nothing is recorded.
	c = login()
	res, err = run(t, "virt.clab.node.stop", dev, c, p, false)
	if _, _, has := recorded(c); err != nil || res.Changed || has {
		t.Errorf("stop of a stopped node: changed %v, err %v, undo recorded %v", res.Changed, err, has)
	}

	// The undo it recorded puts the node back.
	if _, err := run(t, fqcn, dev, login(), params, false); err != nil || state() != "running" {
		t.Fatalf("the undo: err %v, state %s", err, state())
	}

	// Pause and unpause are each other's undo, and a stop from paused is a
	// partial one, which a start does not restore.
	c = login()
	if _, err := run(t, "virt.clab.node.pause", dev, c, p, false); err != nil || state() != "paused" {
		t.Fatalf("pause: err %v, state %s", err, state())
	}
	if f, _, _ := recorded(c); f != "virt.clab.node.unpause" {
		t.Errorf("pause records %s", f)
	}
	c = login()
	if _, err := run(t, "virt.clab.node.stop", dev, c, p, false); err != nil || state() != "exited" {
		t.Fatalf("stop from paused: err %v, state %s", err, state())
	}
	if inv, _ := c.stats[sdk.StatInverse].(map[string]any); inv[sdk.InversePartialKey] != true {
		t.Errorf("a stop from paused records an undo that is not marked partial: %v", inv)
	}

	// A node the lab has, named by its container.
	if _, err := run(t, "virt.clab.node.start", dev, login(), map[string]any{"lab": "lab1", "node": "clab-lab1-n1"}, false); err != nil || state() != "running" {
		t.Errorf("start by container name: err %v, state %s", err, state())
	}
}

// TestLabDeploy_IsIdempotentAndRecordsItsUndoBeforeTheCall: an absent lab is
// deployed and its destroy recorded, a lab already there is left alone with no
// undo, a topology that names another lab is refused before anything is sent,
// and a file in the project is read as the topology.
func TestLabDeploy_IsIdempotentAndRecordsItsUndoBeforeTheCall(t *testing.T) {
	srv := newServer(t)
	dev := device(t, srv.URL, true)
	topo := map[string]any{"name": "lab2", "topology": map[string]any{"nodes": map[string]any{"a": map[string]any{"kind": "linux"}, "b": map[string]any{"kind": "linux"}}}}

	if _, err := run(t, "virt.clab.lab.deploy", dev, login(), map[string]any{"lab": "lab2", "topology": map[string]any{"name": "other"}}, false); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("a topology naming another lab: %v", err)
	}
	if _, err := run(t, "virt.clab.lab.deploy", dev, login(), map[string]any{"lab": "lab2"}, false); err == nil {
		t.Fatal("no topology was accepted")
	}
	if _, err := run(t, "virt.clab.lab.deploy", dev, login(), map[string]any{"lab": "lab2", "topology": topo, "source_url": "https://x/y"}, false); err == nil {
		t.Fatal("two topologies were accepted")
	}
	if len(srv.calls) != 0 {
		t.Errorf("a refused deploy reached the server: %v", srv.calls)
	}

	c := login()
	res, err := run(t, "virt.clab.lab.deploy", dev, c, map[string]any{"lab": "lab2", "topology": topo}, true)
	if err != nil || !res.Changed {
		t.Fatalf("a check of a deploy: changed %v, err %v", res.Changed, err)
	}
	if _, there := srv.labs["lab2"]; there {
		t.Fatal("a check deployed the lab")
	}

	c = login()
	res, err = run(t, "virt.clab.lab.deploy", dev, c, map[string]any{"lab": "lab2", "topology": topo}, false)
	fqcn, params, ok := recorded(c)
	if err != nil || !res.Changed || c.stats["created"] != true || !ok || fqcn != "virt.clab.lab.destroy" || params["lab"] != "lab2" || params["cleanup"] != true {
		t.Fatalf("deploy: changed %v, err %v, stats %v, undo %s %v", res.Changed, err, c.stats, fqcn, params)
	}

	c = login()
	res, err = run(t, "virt.clab.lab.deploy", dev, c, map[string]any{"lab": "lab2", "topology": topo}, false)
	if _, _, has := recorded(c); err != nil || res.Changed || has || c.stats["created"] != false {
		t.Errorf("deploy of a deployed lab: changed %v, err %v, undo %v, stats %v", res.Changed, err, has, c.stats)
	}

	// The undo it recorded destroys the lab; destroying it again is unchanged.
	if _, err := run(t, fqcn, dev, login(), params, false); err != nil {
		t.Fatal(err)
	}
	if _, there := srv.labs["lab2"]; there {
		t.Error("the undo left the lab deployed")
	}
	if res, err := run(t, "virt.clab.lab.destroy", dev, login(), map[string]any{"lab": "lab2"}, false); err != nil || res.Changed {
		t.Errorf("destroy of an absent lab: changed %v, err %v", res.Changed, err)
	}

	// A project file is the topology.
	c = login()
	c.project = map[string]string{"lab/lab3.clab.yml": "name: lab3\ntopology:\n  nodes:\n    x: {kind: linux}\n"}
	if _, err := run(t, "virt.clab.lab.deploy", dev, c, map[string]any{"lab": "lab3", "topology_file": "lab/lab3.clab.yml"}, false); err != nil {
		t.Fatalf("topology_file: %v", err)
	}
	if _, there := srv.labs["lab3"]; !there {
		t.Error("topology_file did not deploy the lab")
	}
}

// TestNetem_SetIsIdempotentAndItsUndoPutsBackWhatWasThere: setting what is
// there is unchanged, setting something else records an undo that restores the
// old settings (or resets when there were none), and a reset records the set
// that puts them back.
func TestNetem_SetIsIdempotentAndItsUndoPutsBackWhatWasThere(t *testing.T) {
	srv := newServer(t)
	labOf(srv, "lab1")
	dev := device(t, srv.URL, true)
	set := func(extra map[string]any) (*rc, collection.Result, error) {
		c := login()
		p := map[string]any{"lab": "lab1", "node": "n1", "interface": "eth1"}
		for k, v := range extra {
			p[k] = v
		}
		res, err := run(t, "virt.clab.netem.set", dev, c, p, false)
		return c, res, err
	}

	c, res, err := set(map[string]any{"delay": "50ms", "loss": 1})
	fqcn, _, _ := recorded(c)
	if err != nil || !res.Changed || fqcn != "virt.clab.netem.reset" {
		t.Fatalf("first set: changed %v, err %v, undo %s", res.Changed, err, fqcn)
	}
	c, res, err = set(map[string]any{"delay": "50ms", "loss": 1})
	if _, _, has := recorded(c); err != nil || res.Changed || has {
		t.Errorf("setting what is there: changed %v, err %v, undo %v", res.Changed, err, has)
	}
	c, res, err = set(map[string]any{"delay": "100ms", "jitter": "5ms"})
	fqcn, params, _ := recorded(c)
	if err != nil || !res.Changed || fqcn != "virt.clab.netem.set" || params["delay"] != "50ms" || params["loss"] != float64(1) && params["loss"] != 1 {
		t.Fatalf("changing it: changed %v, err %v, undo %s %v", res.Changed, err, fqcn, params)
	}
	// Its undo sets the earlier settings back.
	if _, err := run(t, fqcn, dev, login(), params, false); err != nil {
		t.Fatal(err)
	}
	cc := login()
	if _, err := run(t, "virt.clab.netem.show", dev, cc, map[string]any{"lab": "lab1", "node": "n1"}, false); err != nil {
		t.Fatal(err)
	}
	list, _ := cc.stats["interfaces"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["delay"] != "50ms" {
		t.Errorf("after the undo: %v", cc.stats["interfaces"])
	}

	// Reset, then the set it recorded.
	c = login()
	res, err = run(t, "virt.clab.netem.reset", dev, c, map[string]any{"lab": "lab1", "node": "n1", "interface": "eth1"}, false)
	fqcn, params, _ = recorded(c)
	if err != nil || !res.Changed || fqcn != "virt.clab.netem.set" || params["delay"] != "50ms" {
		t.Fatalf("reset: changed %v, err %v, undo %s %v", res.Changed, err, fqcn, params)
	}
	if res, err := run(t, "virt.clab.netem.reset", dev, login(), map[string]any{"lab": "lab1", "node": "n1", "interface": "eth1"}, false); err != nil || res.Changed {
		t.Errorf("reset of a clean interface: changed %v, err %v", res.Changed, err)
	}

	for name, p := range map[string]map[string]any{
		"nothing to set":    {"lab": "lab1", "node": "n1", "interface": "eth1"},
		"jitter, no delay":  {"lab": "lab1", "node": "n1", "interface": "eth1", "jitter": "5ms"},
		"a bad duration":    {"lab": "lab1", "node": "n1", "interface": "eth1", "delay": "fast"},
		"loss over 100":     {"lab": "lab1", "node": "n1", "interface": "eth1", "loss": 101},
		"a negative rate":   {"lab": "lab1", "node": "n1", "interface": "eth1", "rate": -1},
		"an interface path": {"lab": "lab1", "node": "n1", "interface": "../x", "delay": "1ms"},
	} {
		if _, err := run(t, "virt.clab.netem.set", dev, login(), p, false); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestClab_RefusesWhatIsNotAContainerlabServerAndNeverShowsTheLogin: a device
// onboarding did not recognise is refused, an unknown parameter is named, a
// rejected login is reported without the password even when the server
// echoes it, and the password is in no stat.
func TestClab_RefusesWhatIsNotAContainerlabServerAndNeverShowsTheLogin(t *testing.T) {
	srv := newServer(t)
	labOf(srv, "lab1")

	if _, err := run(t, "virt.clab.lab.info", device(t, srv.URL, false), login(), map[string]any{"lab": "lab1"}, false); err == nil || !strings.Contains(err.Error(), "containerlab API server") {
		t.Errorf("a device onboarding did not recognise: %v", err)
	}
	dev := device(t, srv.URL, true)
	if _, err := run(t, "virt.clab.lab.info", dev, login(), map[string]any{"lab": "lab1", "colour": "red"}, false); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Errorf("an unknown parameter: %v", err)
	}
	bad := login()
	bad.secrets[wire.SecretPassword] = testPassword + "x"
	_, err := run(t, "virt.clab.lab.info", dev, bad, map[string]any{"lab": "lab1"}, false)
	if err == nil || !strings.Contains(err.Error(), "logging in") || strings.Contains(err.Error(), testPassword) {
		t.Errorf("a rejected login: %v", err)
	}
	c := login()
	if _, err := run(t, "virt.clab.lab.info", dev, c, map[string]any{"lab": "lab1"}, false); err != nil {
		t.Fatal(err)
	}
	for k, v := range c.stats {
		if s, ok := v.(string); ok && (strings.Contains(s, testPassword) || strings.Contains(s, testToken)) {
			t.Errorf("stat %s holds the login", k)
		}
	}
	// A refused parameter is refused before the server hears of the run.
	srv.mu.Lock()
	before, loginsBefore := len(srv.calls), srv.logins
	srv.mu.Unlock()
	if _, err := run(t, "virt.clab.lab.info", dev, login(), map[string]any{"lab": "bad name"}, false); err == nil {
		t.Error("a lab with a space was accepted")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.calls) != before || srv.logins != loginsBefore {
		t.Errorf("a refused parameter reached the server: %d calls, %d logins", len(srv.calls)-before, srv.logins-loginsBefore)
	}
	var _ inventory.InventoryItem = dev
}

// TestClab_ALoginTheServerRefusesIsRenewedOnce: the real server refuses the
// first token it issues after it starts, so a 401 is answered by one fresh
// login and one more try, and a server that keeps refusing is an error.
func TestClab_ALoginTheServerRefusesIsRenewedOnce(t *testing.T) {
	defer clab.SetReauthDelay(0)()
	srv := newServer(t)
	srv.refuseFirstToken = true
	labOf(srv, "lab1")
	dev := device(t, srv.URL, true)
	c := login()
	if _, err := run(t, "virt.clab.lab.info", dev, c, map[string]any{"lab": "lab1"}, false); err != nil || c.stats["exists"] != true {
		t.Fatalf("a refused first token: err %v, stats %v", err, c.stats)
	}
	if srv.logins != 2 {
		t.Errorf("%d logins, want a second after the 401", srv.logins)
	}

	// A token the server never accepts is one retry and then the refusal.
	never := newServer(t)
	never.refuseFirstToken = true
	labOf(never, "lab1")
	never.mu.Lock()
	never.labs["lab1"] = nil
	never.mu.Unlock()
	bad := login()
	bad.secrets[wire.SecretPassword] = "wrong"
	if _, err := run(t, "virt.clab.lab.info", device(t, never.URL, true), bad, map[string]any{"lab": "lab1"}, false); err == nil || !strings.Contains(err.Error(), "logging in") {
		t.Errorf("a wrong password: %v", err)
	}
}

// TestEdgeshark_InstallAndUninstallActOnlyOnTheStateTheyMove: an Edgeshark that
// runs is not installed again, an install records the uninstall that undoes it,
// and a check predicts the change and makes none.
func TestEdgeshark_InstallAndUninstallActOnlyOnTheStateTheyMove(t *testing.T) {
	srv := newServer(t)
	dev := device(t, srv.URL, true)
	running := func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.edgeshark }

	if res, err := run(t, "virt.clab.edgeshark.install", dev, login(), nil, true); err != nil || !res.Changed || running() {
		t.Fatalf("a check of an install: changed %v, err %v, running %v", res.Changed, err, running())
	}
	c := login()
	res, err := run(t, "virt.clab.edgeshark.install", dev, c, nil, false)
	fqcn, _, ok := recorded(c)
	if err != nil || !res.Changed || !running() || !ok || fqcn != "virt.clab.edgeshark.uninstall" {
		t.Fatalf("install: changed %v, err %v, running %v, undo %s", res.Changed, err, running(), fqcn)
	}
	c = login()
	if res, err := run(t, "virt.clab.edgeshark.install", dev, c, nil, false); err != nil || res.Changed {
		t.Errorf("install of a running Edgeshark: changed %v, err %v", res.Changed, err)
	}
	if _, _, has := recorded(c); has {
		t.Error("an install that changed nothing recorded an undo")
	}
	if _, err := run(t, fqcn, dev, login(), nil, false); err != nil || running() {
		t.Fatalf("the install's undo: err %v, running %v", err, running())
	}
	if res, err := run(t, "virt.clab.edgeshark.uninstall", dev, login(), nil, false); err != nil || res.Changed {
		t.Errorf("uninstall of one that is not there: changed %v, err %v", res.Changed, err)
	}
}

// TestClab_TheLoginTokenIsMaskedInAnError: a server that quotes the token it was
// sent in an error does not put it in the task's, under basic auth where the
// token is not the stored password.
func TestClab_TheLoginTokenIsMaskedInAnError(t *testing.T) {
	srv := newServer(t)
	_, err := run(t, "virt.clab.lab.info", device(t, srv.URL, true), login(), map[string]any{"lab": "leaky"}, false)
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testPassword) || !strings.Contains(err.Error(), "[masked]") {
		t.Errorf("an error that quoted the token: %v", err)
	}
}
