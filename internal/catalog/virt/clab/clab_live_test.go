// The virt.clab methods against a real containerlab API server deploying a
// real lab: the device is onboarded by the real prober, two real containers are
// linked, and the methods are judged by what the nodes then do (a ping that
// crosses the link, one that stops under netem loss, an exit status the server
// itself drops), never by what a method reported about itself.
package clab_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// live is a real server, the device onboarding made of it, and its login.
type live struct {
	t   *testing.T
	dev inventory.InventoryItem
	pw  string
}

// ctx is the context every call here uses.
func (l *live) ctx() context.Context { return context.Background() }

// call runs a method as a run would.
func (l *live) call(fqcn string, params map[string]any, check bool) (*rc, collection.Result, error) {
	c := &rc{secrets: map[string]string{wire.SecretUsername: testsupport.ClabAPIUser, wire.SecretPassword: l.pw}, stats: map[string]any{}}
	res, err := run(l.t, fqcn, l.dev, c, params, check)
	return c, res, err
}

// must is call that fails the test on an error.
func (l *live) must(fqcn string, params map[string]any) *rc {
	l.t.Helper()
	c, _, err := l.call(fqcn, params, false)
	if err != nil {
		l.t.Fatalf("%s: %v", fqcn, err)
	}
	return c
}

// sh runs a command in a node and returns its stdout, failing the test when
// the command fails, as the method reads the status.
func (l *live) sh(lab, node, command string) string {
	l.t.Helper()
	return l.must("virt.clab.node.exec", map[string]any{"lab": lab, "node": node, "command": command}).stats["stdout"].(string)
}

// startLive starts the real server and onboards a generic_http device for it.
func startLive(t *testing.T) *live {
	t.Helper()
	if testing.Short() {
		t.Skip("starts containers")
	}
	base, pw := testsupport.StartClabServer(t)
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(t.TempDir(), inv.DefaultInventoryFilename), factory)
	item, err := factory.Build(record.Record{ID: "clab-api-id", Name: "clab-api", Type: generic.TypeHTTP, State: record.InitialState(generic.TypeHTTP),
		Properties: map[string]inventory.PropertyValue{generic.BaseURLProperty: base, generic.HTTPAuthProperty: httpapi.AuthBasic, httpapi.AllowPlaintextCredentialsProperty: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{wire.SecretUsername: testsupport.ClabAPIUser, wire.SecretPassword: pw}
	res, err := onboard.Onboard(context.Background(), repo, "clab-api", func(context.Context, inventory.InventoryItem) (map[string]string, error) { return creds, nil }, time.Now)
	if err != nil || res.State != "active" {
		t.Fatalf("onboarding the server: state %q, err %v", res.State, err)
	}
	dev, err := repo.GetByName(context.Background(), "clab-api")
	if err != nil {
		t.Fatal(err)
	}
	if !dev.HasCapability(capability.NameClabAPI) {
		t.Fatalf("onboarding did not recognise a containerlab API server; capabilities %v", dev.Capabilities())
	}
	return &live{t: t, dev: dev, pw: pw}
}

// TestLive_ARealLabEndToEnd deploys a two-node lab and exercises the methods on
// it, then runs the undo the deploy recorded.
func TestLive_ARealLabEndToEnd(t *testing.T) {
	l := startLive(t)
	lab := "plt" + strconv.FormatInt(time.Now().UnixNano()%1e8, 10)
	testsupport.CleanClabLab(t, lab)
	topo := map[string]any{"name": lab, "topology": map[string]any{
		"nodes": map[string]any{
			"n1": map[string]any{"kind": "linux", "image": testsupport.ClabNodeImage, "cmd": "sleep infinity"},
			"n2": map[string]any{"kind": "linux", "image": testsupport.ClabNodeImage, "cmd": "sleep infinity"},
		},
		"links": []any{map[string]any{"endpoints": []any{"n1:eth1", "n2:eth1"}}},
	}}

	// The server: version, health (asked with no login), metrics, images.
	if v := l.must("virt.clab.server.version", nil).stats["version"].(string); !strings.Contains(v, "version") {
		t.Errorf("server.version = %q", v)
	}
	if h := l.must("virt.clab.server.health", nil).stats; h["status"] != "healthy" || h["api_version"] == "" {
		t.Errorf("server.health = %v", h)
	}
	if m, _ := l.must("virt.clab.server.metrics", nil).stats["metrics"].(map[string]any); m["cpu"] == nil {
		t.Errorf("server.metrics holds no cpu: %v", m)
	}
	if images, _ := l.must("virt.clab.image.list", nil).stats["images"].([]any); len(images) == 0 {
		t.Errorf("image.list found no images")
	}
	// The node image is on the host already (the net lab builds on it), so
	// pulling it is unchanged, in a run and in a check.
	for _, check := range []bool{false, true} {
		if _, res, err := l.call("virt.clab.image.pull", map[string]any{"image": testsupport.ClabNodeImage}, check); err != nil || res.Changed {
			t.Errorf("pull of a present image (check %v): changed %v, err %v", check, res.Changed, err)
		}
	}

	// Deploy: absent, so it deploys and records its destroy; run again it is
	// unchanged.
	deploy := l.must("virt.clab.lab.deploy", map[string]any{"lab": lab, "topology": topo})
	undoFQCN, undoParams, ok := recorded(deploy)
	if !ok || undoFQCN != "virt.clab.lab.destroy" || deploy.stats["created"] != true {
		t.Fatalf("deploy: undo %s %v, stats %v", undoFQCN, undoParams, deploy.stats)
	}
	if again := l.must("virt.clab.lab.deploy", map[string]any{"lab": lab, "topology": topo}); again.stats["created"] != false {
		t.Errorf("a second deploy created the lab again: %v", again.stats)
	}
	info := l.must("virt.clab.lab.info", map[string]any{"lab": lab}).stats
	if info["exists"] != true || info["node_count"] != 2 {
		t.Fatalf("lab.info = %v", info)
	}
	if names := l.must("virt.clab.lab.list", nil).stats["labs"].([]string); !contains(names, lab) {
		t.Errorf("lab.list = %v does not hold %s", names, lab)
	}
	if links, _ := l.must("virt.clab.lab.links", map[string]any{"lab": lab}).stats["links"].([]any); len(links) < 2 {
		t.Errorf("lab.links = %v", links)
	}
	if ifs, _ := l.must("virt.clab.lab.interfaces", map[string]any{"lab": lab, "node": "n1"}).stats["interfaces"].([]any); !strings.Contains(toText(ifs), "eth1") {
		t.Errorf("lab.interfaces = %v lacks eth1", ifs)
	}
	if topology := l.must("virt.clab.lab.topology", map[string]any{"lab": lab}).stats["topology"].(string); !strings.Contains(topology, lab) {
		t.Errorf("lab.topology = %q", topology)
	}
	if v := l.must("virt.clab.lab.validate", map[string]any{"lab": lab}).stats; v["valid"] != true || v["nodes"] != float64(2) && v["nodes"] != 2 {
		t.Errorf("lab.validate = %v", v)
	}
	for _, check := range []bool{false, true} {
		c, res, err := l.call("virt.clab.lab.apply", map[string]any{"lab": lab}, check)
		if err != nil || res.Changed || c.stats["dry_run"] != check {
			t.Errorf("apply of an unchanged lab (check %v): changed %v, err %v, stats %v", check, res.Changed, err, c.stats)
		}
	}

	// exec, judged by the node. The server reports exit code 0 for a command
	// that failed; the method reads the real status.
	if out := l.sh(lab, "n1", "hostname"); strings.TrimSpace(out) != "n1" {
		t.Errorf("hostname = %q", out)
	}
	c, _, err := l.call("virt.clab.node.exec", map[string]any{"lab": lab, "node": "n1", "command": "echo out; echo err 1>&2; exit 3"}, false)
	if err == nil || !strings.Contains(err.Error(), "exited 3") || c.stats["exit_code"] != 3 || c.stats["stdout"] != "out\n" || c.stats["stderr"] != "err\n" {
		t.Fatalf("a failing command: err %v, stats %v", err, c.stats)
	}

	// Put an address on each end and cross the link, then impair it.
	l.sh(lab, "n1", "ip addr add 10.9.9.1/30 dev eth1 && ip link set eth1 up")
	l.sh(lab, "n2", "ip addr add 10.9.9.2/30 dev eth1 && ip link set eth1 up")
	ping := func() error {
		_, _, err := l.call("virt.clab.node.exec", map[string]any{"lab": lab, "node": "n1", "command": "ping -c 3 -W 1 10.9.9.2"}, false)
		return err
	}
	if err := ping(); err != nil {
		t.Fatalf("n1 cannot reach n2 across the link: %v", err)
	}
	set := l.must("virt.clab.netem.set", map[string]any{"lab": lab, "node": "n1", "interface": "eth1", "loss": 100})
	if f, _, _ := recorded(set); f != "virt.clab.netem.reset" {
		t.Errorf("netem.set on a clean interface records %q", f)
	}
	if err := ping(); err == nil {
		t.Error("n1 still reaches n2 with 100% loss on its interface")
	}
	if _, res, err := l.call("virt.clab.netem.set", map[string]any{"lab": lab, "node": "n1", "interface": "eth1", "loss": 100}, false); err != nil || res.Changed {
		t.Errorf("setting what is set: changed %v, err %v", res.Changed, err)
	}
	if shown, _ := l.must("virt.clab.netem.show", map[string]any{"lab": lab, "node": "n1"}).stats["interfaces"].([]any); len(shown) != 1 {
		t.Errorf("netem.show = %v", shown)
	}
	l.must("virt.clab.netem.reset", map[string]any{"lab": lab, "node": "n1", "interface": "eth1"})
	if err := ping(); err != nil {
		t.Errorf("n1 cannot reach n2 after netem.reset: %v", err)
	}

	// Node state, with the undo each records run for real.
	stop := l.must("virt.clab.node.stop", map[string]any{"lab": lab, "node": "n2"})
	if st := nodeState(t, l, lab, "n2"); st != "exited" {
		t.Fatalf("n2 is %s after stop", st)
	}
	if _, res, err := l.call("virt.clab.node.stop", map[string]any{"lab": lab, "node": "n2"}, false); err != nil || res.Changed {
		t.Errorf("stop of a stopped node: changed %v, err %v", res.Changed, err)
	}
	f, p, _ := recorded(stop)
	if f != "virt.clab.node.start" {
		t.Fatalf("stop recorded the undo %s, want virt.clab.node.start", f)
	}
	l.must(f, p)
	if st := nodeState(t, l, lab, "n2"); st != "running" {
		t.Errorf("n2 is %s after the undo of stop", st)
	}
	l.must("virt.clab.node.pause", map[string]any{"lab": lab, "node": "n2"})
	if st := nodeState(t, l, lab, "n2"); st != "paused" {
		t.Errorf("n2 is %s after pause", st)
	}
	l.must("virt.clab.node.unpause", map[string]any{"lab": lab, "node": "n2"})
	l.must("virt.clab.node.restart", map[string]any{"lab": lab, "node": "n2"})
	if st := nodeState(t, l, lab, "n2"); st != "running" {
		t.Errorf("n2 is %s after restart", st)
	}

	// A lab-wide stop and start.
	l.must("virt.clab.lab.stop", map[string]any{"lab": lab})
	if res, err := l.callChanged("virt.clab.lab.stop", map[string]any{"lab": lab}); err != nil || res {
		t.Errorf("lab.stop of a stopped lab: changed %v, err %v", res, err)
	}
	l.must("virt.clab.lab.start", map[string]any{"lab": lab})
	if st := nodeState(t, l, lab, "n1"); st != "running" {
		t.Errorf("n1 is %s after lab.start", st)
	}

	// Logs, ports, and a temporary SSH door that its undo closes.
	l.must("virt.clab.node.logs", map[string]any{"lab": lab, "node": "n1", "tail": 5})
	l.must("virt.clab.node.ports", map[string]any{"lab": lab, "node": "n1"})
	door := l.must("virt.clab.node.ssh_access", map[string]any{"lab": lab, "node": "n1", "duration": "5m"})
	f, p, _ = recorded(door)
	if f != "virt.clab.ssh_session.terminate" || door.stats["port"] == nil {
		t.Fatalf("ssh_access: undo %s %v, stats %v", f, p, door.stats)
	}
	if sessions, _ := l.must("virt.clab.ssh_session.list", map[string]any{"all": true}).stats["sessions"].([]any); len(sessions) != 1 {
		t.Errorf("ssh_session.list = %v", sessions)
	}
	if c, res, err := l.call(f, p, false); err != nil || !res.Changed {
		t.Fatalf("closing the door: changed %v, err %v, %v", res.Changed, err, c.stats)
	}
	if _, res, _ := l.call(f, p, false); res.Changed {
		t.Error("terminating a closed session changed something")
	}

	l.must("virt.clab.lab.save", map[string]any{"lab": lab})
	l.must("virt.clab.node.disable_tx_offload", map[string]any{"lab": lab, "node": "n1"})
	gen := l.must("virt.clab.topology.generate", map[string]any{"name": "g1", "default_kind": "linux", "images": map[string]any{"linux": testsupport.ClabNodeImage}, "tiers": []any{map[string]any{"count": 2, "kind": "linux"}}})
	if !strings.Contains(gen.stats["topology"].(string), "name: g1") {
		t.Errorf("topology.generate = %v", gen.stats)
	}
	// Capture links need Edgeshark, which is not running here: the status
	// says so, and the server's own refusal reaches the caller whole, naming
	// the reason. Only that refusal of packetflix is proven live.
	status, _ := l.must("virt.clab.edgeshark.status", nil).stats["status"].(map[string]any)
	if running, ok := status["running"].(bool); !ok || running {
		t.Errorf("edgeshark.status = %v, want running: false", status)
	}
	if _, _, err := l.call("virt.clab.capture.packetflix", map[string]any{"lab": lab, "targets": []any{map[string]any{"container_name": "clab-" + lab + "-n1", "interface_name": "eth1"}}}, false); err == nil || !strings.Contains(err.Error(), "Edgeshark is not running") {
		t.Errorf("packetflix without Edgeshark: %v", err)
	}

	// The undo the deploy recorded destroys the lab, and a second destroy is
	// unchanged.
	if _, res, err := l.call(undoFQCN, undoParams, false); err != nil || !res.Changed {
		t.Fatalf("the deploy's undo: changed %v, err %v", res.Changed, err)
	}
	if gone := l.must("virt.clab.lab.info", map[string]any{"lab": lab}).stats; gone["exists"] != false {
		t.Errorf("the lab is still deployed after its undo: %v", gone)
	}
	if res, err := l.callChanged("virt.clab.lab.destroy", map[string]any{"lab": lab}); err != nil || res {
		t.Errorf("destroy of an absent lab: changed %v, err %v", res, err)
	}
}

// callChanged runs a method and returns whether it reported a change.
func (l *live) callChanged(fqcn string, params map[string]any) (bool, error) {
	_, res, err := l.call(fqcn, params, false)
	return res.Changed, err
}

// nodeState reads a node's state from the server.
func nodeState(t *testing.T, l *live, lab, node string) string {
	t.Helper()
	nodes, _ := l.must("virt.clab.lab.info", map[string]any{"lab": lab}).stats["nodes"].([]any)
	for _, n := range nodes {
		m := n.(map[string]any)
		if m["node"] == node {
			return m["state"].(string)
		}
	}
	t.Fatalf("lab %s has no node %s: %v", lab, node, nodes)
	return ""
}

// contains reports whether list holds v.
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// toText is a result rendered for a substring check.
func toText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
