// Tests that drive the net.frr methods against real FRRouting routers in
// containers (testsupport.StartNetLab), reached over SSH as the
// unprivileged netops account, and judge each one by the router's own
// state read as root, never by what the method reported about itself.
package frr_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// runContext is a RunbookContext holding the lab's credential and the
// stats a method set, with a project directory for src.
type runContext struct {
	secrets map[string]string
	stats   map[string]any
	project string
}

// InjectSecrets returns the lab's credential.
func (c *runContext) InjectSecrets() map[string]string { return c.secrets }

// SetStat records a stat.
func (c *runContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}

// EmitFact records nothing; these methods emit none.
func (c *runContext) EmitFact(string, any) error { return nil }

// ReadProjectFile reads name from the test's project directory.
func (c *runContext) ReadProjectFile(name string, _ int64) ([]byte, error) {
	if c.project == "" {
		return nil, sdk.ErrNoProjectFiles
	}
	return os.ReadFile(filepath.Join(c.project, filepath.FromSlash(name))) // #nosec G304 -- a test's own temporary directory
}

// lab is the one net lab every test here shares: two routers on one link.
type lab struct {
	*testsupport.NetLab
	secrets map[string]string
	// link maps each router to its interface on the link.
	link map[string]string
}

// startLab starts two routers on one link, trusted in a known_hosts file
// of the test's own.
func startLab(t *testing.T) *lab {
	t.Helper()
	if testing.Short() {
		t.Skip("starts containers")
	}
	link := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.202.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.202.0.2"), "r2": netip.MustParseAddr("10.202.0.3"),
	}}
	nl := testsupport.StartNetLab(t, []string{"r1", "r2"}, nil, []testsupport.NetLabLink{link})
	nl.TrustHostKeys(t)
	return &lab{
		NetLab:  nl,
		secrets: nl.Secrets(),
		link: map[string]string{
			"r1": nl.Nodes["r1"].InterfaceWith(t, link.Ends["r1"]),
			"r2": nl.Nodes["r2"].InterfaceWith(t, link.Ends["r2"]),
		},
	}
}

// TestMethods_OnRealRouters runs each method against one lab, which costs
// a router's start once rather than per method.
func TestMethods_OnRealRouters(t *testing.T) {
	l := startLab(t)
	t.Run("facts", func(t *testing.T) { testFacts(t, l) })
	t.Run("command", func(t *testing.T) { testCommand(t, l) })
	t.Run("interface", func(t *testing.T) { testInterface(t, l) })
	t.Run("config", func(t *testing.T) { testConfig(t, l) })
}

// device returns name as a device.
func (l *lab) device(name string) inventory.InventoryItem {
	return l.Nodes[name].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameFRR)
}

// rc returns a fresh context.
func (l *lab) rc() *runContext {
	return &runContext{secrets: l.secrets, stats: map[string]any{}}
}

// running reads name's running configuration as root.
func (l *lab) running(t *testing.T, name string) string {
	t.Helper()
	out := l.Nodes[name].Exec(t, "vtysh", "-c", "show running-config")
	if i := strings.Index(out, "frr version"); i >= 0 {
		return out[i:]
	}
	return out
}

// inverseParams returns the params of the undo rc holds, failing when
// there is none.
func inverseParams(t *testing.T, rc *runContext) (map[string]any, bool) {
	t.Helper()
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("no undo was recorded; stats %v", rc.stats)
	}
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	keep, _ := inv[sdk.InverseKeepOnFailureKey].(bool)
	return params, keep
}

// testFacts: version, hostname, the link's interface and
// a connected route come from the router, the configuration comes back by
// default with its secret masked, and include_config false leaves it out.
func testFacts(t *testing.T, l *lab) {
	l.Nodes["r1"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r1"], "-c", "ip ospf authentication-key facts-secret")
	t.Cleanup(func() {
		l.Nodes["r1"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r1"], "-c", "no ip ospf authentication-key")
	})
	rc := l.rc()
	// No include_config: the masked configuration is the default (R5).
	if _, err := frr.Facts(context.Background(), rc, l.device("r1"), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := rc.stats["version"].(string); !strings.HasPrefix(v, "10.1.1") {
		t.Errorf("version = %q, want 10.1.1", v)
	}
	if h, _ := rc.stats["hostname"].(string); h == "" || !strings.Contains(l.running(t, "r1"), "hostname "+h) {
		t.Errorf("hostname = %q, not the one the running configuration names", h)
	}
	if ifs, _ := rc.stats["interfaces"].(map[string]any); ifs[l.link["r1"]] == nil {
		t.Errorf("interfaces has no %s: %v", l.link["r1"], ifs)
	}
	if routes, _ := rc.stats["routes"].(map[string]any); routes["10.202.0.0/29"] == nil {
		t.Errorf("routes has no connected 10.202.0.0/29")
	}
	if _, ok := rc.stats["ospf_neighbors"].(map[string]any); !ok {
		t.Errorf("ospf_neighbors is %T, want a map", rc.stats["ospf_neighbors"])
	}
	if d, _ := rc.stats["config_sha256"].(string); len(d) != 64 {
		t.Errorf("config_sha256 = %q", d)
	}
	config, _ := rc.stats["running_config"].(string)
	if strings.Contains(config, "facts-secret") || !strings.Contains(config, "ip ospf authentication-key pleiades-masked-") {
		t.Errorf("running_config does not mask the key:\n%s", config)
	}
	narrowed := l.rc()
	if _, err := frr.Facts(context.Background(), narrowed, l.device("r1"), map[string]any{"include_config": false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := narrowed.stats["running_config"]; ok || narrowed.stats["config_sha256"] != rc.stats["config_sha256"] {
		t.Errorf("include_config false: running_config present %v, digest %v", ok, narrowed.stats["config_sha256"])
	}
}

// testCommand: a show command's JSON
// comes back as data, and a configuration command never reaches the
// router.
func testCommand(t *testing.T, l *lab) {
	rc := l.rc()
	if _, err := frr.Command(context.Background(), rc, l.device("r1"), map[string]any{"command": "show ip route json"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := rc.stats["data"].(map[string]any); data["10.202.0.0/29"] == nil {
		t.Errorf("data has no connected route: %v", rc.stats["data"])
	}
	// show running-config is a show command, so its secrets are masked.
	l.Nodes["r1"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r1"], "-c", "ip ospf authentication-key cmdkey1")
	t.Cleanup(func() {
		l.Nodes["r1"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r1"], "-c", "no ip ospf authentication-key")
	})
	shown := l.rc()
	if _, err := frr.Command(context.Background(), shown, l.device("r1"), map[string]any{"command": "show running-config"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := shown.stats["stdout"].(string); strings.Contains(out, "cmdkey1") || !strings.Contains(out, "pleiades-masked-") {
		t.Errorf("show running-config's secret was not masked:\n%s", out)
	}
	before := l.running(t, "r1")
	for _, c := range []string{"configure terminal", "show version; reboot", "show version\nconfigure terminal", "write memory"} {
		if _, err := frr.Command(context.Background(), l.rc(), l.device("r1"), map[string]any{"command": c}); err == nil {
			t.Errorf("%q was accepted", c)
		}
	}
	if after := l.running(t, "r1"); after != before {
		t.Errorf("a refused command changed the configuration")
	}
}

// testInterface: shutting the link
// changes the router's state and records an undo to up, kept through a
// failure; a check changes nothing; the undo's params bring it back.
func testInterface(t *testing.T, l *lab) {
	name := l.link["r1"]
	adminDown := func() bool {
		return strings.Contains(l.Nodes["r1"].Exec(t, "vtysh", "-c", "show running-config"), "interface "+name+"\n shutdown")
	}
	params := map[string]any{"name": name, "enabled": false}

	rc := l.rc()
	res, err := frr.CheckInterface(context.Background(), rc, l.device("r1"), params)
	if err != nil || !res.Changed || adminDown() {
		t.Fatalf("check: changed %v, err %v, down %v; want a predicted change and none made", res.Changed, err, adminDown())
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("a check recorded an undo")
	}

	rc = l.rc()
	if res, err = frr.Interface(context.Background(), rc, l.device("r1"), params); err != nil || !res.Changed {
		t.Fatalf("shut: changed %v, err %v", res.Changed, err)
	}
	if !adminDown() {
		t.Fatal("the router does not hold the link shut")
	}
	undo, keep := inverseParams(t, rc)
	if undo["enabled"] != true || undo["name"] != name || !keep {
		t.Errorf("undo = %v keep %v, want enabled true for %s, kept", undo, keep, name)
	}

	again := l.rc()
	if res, err = frr.Interface(context.Background(), again, l.device("r1"), params); err != nil || res.Changed {
		t.Errorf("a second shut: changed %v, err %v; want no change", res.Changed, err)
	}
	if _, ok := again.stats[sdk.StatInverse]; ok {
		t.Error("a run that changed nothing recorded an undo")
	}

	if _, err = frr.Interface(context.Background(), l.rc(), l.device("r1"), undo); err != nil {
		t.Fatal(err)
	}
	if adminDown() {
		t.Error("the undo left the link shut")
	}
	if _, err = frr.Interface(context.Background(), l.rc(), l.device("r1"), map[string]any{"name": "eth1; reboot", "enabled": true}); err == nil {
		t.Error("an interface name holding a command was accepted")
	}
}

// candidate returns name's running configuration with block added before
// its final end.
func (l *lab) candidate(t *testing.T, name, block string) string {
	t.Helper()
	base := strings.TrimRight(l.running(t, name), "\n")
	base = strings.TrimSuffix(base, "end")
	return base + block + "end\n"
}

// testConfig is the whole contract on one router:
// a candidate vtysh refuses changes nothing; a check predicts without
// applying; a real run applies, saves, keeps a 0600 backup and masks the
// secret it reports; a second run changes nothing; the undo restores the
// configuration and frr.conf; and an undo refuses a router changed since
// unless forced.
func testConfig(t *testing.T, l *lab) {
	r2 := l.Nodes["r2"]
	ctx := context.Background()
	baseline := l.running(t, "r2")
	confBefore := r2.Exec(t, "cat", "/etc/frr/frr.conf")

	bad := l.candidate(t, "r2", "!\nrouter ospf\n not-a-command 1\nexit\n!\n")
	if _, err := frr.Config(ctx, l.rc(), l.device("r2"), map[string]any{"config": bad}); err == nil || !strings.Contains(err.Error(), "refuses the candidate") {
		t.Fatalf("a candidate vtysh refuses: err %v", err)
	}
	if l.running(t, "r2") != baseline {
		t.Fatal("a refused candidate changed the running configuration")
	}

	good := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n description pleiades-test\n ip ospf authentication-key cfgkey1\nexit\n!\nrouter ospf\n ospf router-id 10.255.0.2\nexit\n!\n")
	sum := sha256.Sum256([]byte(good))
	if _, err := frr.Config(ctx, l.rc(), l.device("r2"), map[string]any{"config": good, "expect_sha256": strings.Repeat("0", 64)}); err == nil {
		t.Fatal("a candidate whose digest differs from expect_sha256 was applied")
	}

	rc := l.rc()
	res, err := frr.CheckConfig(ctx, rc, l.device("r2"), map[string]any{"config": good, "save": true})
	if err != nil || !res.Changed {
		t.Fatalf("check: changed %v, err %v", res.Changed, err)
	}
	if l.running(t, "r2") != baseline {
		t.Fatal("a check changed the running configuration")
	}
	if added, _ := rc.stats["lines_added"].([]string); len(added) == 0 {
		t.Errorf("the check predicted no added lines")
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "r2.conf"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	rc = l.rc()
	rc.project = project
	res, err = frr.Config(ctx, rc, l.device("r2"), map[string]any{"src": "r2.conf", "save": true, "expect_sha256": hex.EncodeToString(sum[:])})
	if err != nil || !res.Changed {
		t.Fatalf("apply: changed %v, err %v", res.Changed, err)
	}
	now := l.running(t, "r2")
	if !strings.Contains(now, "description pleiades-test") || !strings.Contains(now, "ospf router-id 10.255.0.2") {
		t.Fatalf("the router does not run the candidate:\n%s", now)
	}
	if conf := r2.Exec(t, "cat", "/etc/frr/frr.conf"); conf != now {
		t.Errorf("frr.conf is not the running configuration:\n%s", conf)
	}
	added, _ := rc.stats["lines_added"].([]string)
	if joined := strings.Join(added, "\n"); strings.Contains(joined, "cfgkey1") || !strings.Contains(joined, "pleiades-masked-") {
		t.Errorf("lines_added does not mask the key: %v", added)
	}
	undo, keep := inverseParams(t, rc)
	backup, _ := undo["restore"].(string)
	if !keep || undo["save"] != true || backup != rc.stats["backup"] {
		t.Fatalf("undo = %v keep %v, want restore of %v with save, kept", undo, keep, rc.stats["backup"])
	}
	if mode := strings.TrimSpace(r2.Exec(t, "stat", "-c", "%a %U", backup)); mode != "600 "+testsupport.NetLabUser {
		t.Errorf("the backup is %q, want 600 owned by %s", mode, testsupport.NetLabUser)
	}
	if got := r2.Exec(t, "cat", backup); got != baseline {
		t.Errorf("the backup is not the configuration that ran before")
	}

	again := l.rc()
	if res, err = frr.Config(ctx, again, l.device("r2"), map[string]any{"config": good, "save": true}); err != nil || res.Changed {
		t.Errorf("a second run: changed %v, err %v; want no change", res.Changed, err)
	}

	// Someone changes the router after the change; the undo refuses, then
	// restores when forced.
	r2.Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r2"], "-c", "description changed-since")
	if _, err = frr.Config(ctx, l.rc(), l.device("r2"), undo); err == nil || !strings.Contains(err.Error(), "no longer runs") {
		t.Fatalf("an undo over a changed router: err %v", err)
	}
	forced := map[string]any{"restore": backup, "save": true, "force": true}
	if _, err = frr.Config(ctx, l.rc(), l.device("r2"), forced); err != nil {
		t.Fatal(err)
	}
	if got := l.running(t, "r2"); got != baseline {
		t.Errorf("the restore did not bring back the configuration:\n%s\nwant\n%s", got, baseline)
	}
	if conf := r2.Exec(t, "cat", "/etc/frr/frr.conf"); conf != confBefore {
		t.Errorf("the restore did not put frr.conf back:\n%q\nwant\n%q", conf, confBefore)
	}

	// FRR keeps only the first eight characters of an OSPF simple key, so a
	// longer one is a candidate the router runs other than as written: the
	// task fails, its undo is kept, and the undo brings the baseline back.
	altered := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n ip ospf authentication-key longer-than-eight\nexit\n!\n")
	rc = l.rc()
	if _, err = frr.Config(ctx, rc, l.device("r2"), map[string]any{"config": altered}); err == nil || !strings.Contains(err.Error(), "does not run the candidate") || strings.Contains(err.Error(), "longer-than") {
		t.Fatalf("a candidate FRR alters: err %v", err)
	}
	undo, keep = inverseParams(t, rc)
	if !keep {
		t.Error("the undo of a change that landed and then failed was not kept")
	}
	if _, err = frr.Config(ctx, l.rc(), l.device("r2"), undo); err != nil {
		t.Fatal(err)
	}
	if got := l.running(t, "r2"); got != baseline {
		t.Errorf("the kept undo did not bring back the configuration:\n%s", got)
	}

	// A configuration as net.frr.facts reads it, its secrets masked, is
	// refused before the router is reached: applied, it would set every key
	// to its placeholder.
	if _, err := frr.Config(ctx, l.rc(), l.device("r2"), map[string]any{"config": good + "! pleiades-masked-0123456789abcdef\n"}); err == nil || !strings.Contains(err.Error(), "masked secret") {
		t.Errorf("a masked candidate: err %v", err)
	}

	// A save the account cannot make is refused before the reload, by a
	// check as by a real run.
	r2.Exec(t, "chmod", "0444", "/etc/frr/frr.conf")
	t.Cleanup(func() { r2.Exec(t, "chmod", "0664", "/etc/frr/frr.conf") })
	unsaved := l.running(t, "r2")
	for name, call := range map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){"check": frr.CheckConfig, "run": frr.Config} {
		if _, err := call(ctx, l.rc(), l.device("r2"), map[string]any{"config": good, "save": true}); err == nil || !strings.Contains(err.Error(), "cannot write /etc/frr/frr.conf") {
			t.Errorf("%s of a save to an unwritable frr.conf: err %v", name, err)
		}
	}
	if l.running(t, "r2") != unsaved {
		t.Error("a refused save changed the running configuration")
	}
	r2.Exec(t, "chmod", "0664", "/etc/frr/frr.conf")

	// An account that cannot write FRR's run directory is refused before
	// anything is sent, by a check as by a real run.
	r2.Exec(t, "chmod", "g-w", "/var/run/frr")
	t.Cleanup(func() { r2.Exec(t, "chmod", "g+w", "/var/run/frr") })
	held := l.running(t, "r2")
	for name, run := range map[string]func() error{
		"check": func() error {
			_, err := frr.CheckConfig(ctx, l.rc(), l.device("r2"), map[string]any{"config": good})
			return err
		},
		"run": func() error {
			_, err := frr.Config(ctx, l.rc(), l.device("r2"), map[string]any{"config": good})
			return err
		},
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "cannot write /var/run/frr") {
			t.Errorf("%s with the run directory unwritable: err %v", name, err)
		}
	}
	if l.running(t, "r2") != held {
		t.Error("a refused reload changed the router")
	}
	r2.Exec(t, "chmod", "g+w", "/var/run/frr")

	for _, p := range []map[string]any{
		{"restore": "/etc/frr/frr.conf"},
		{"restore": filepath.Dir(backup) + "/../.ssh/authorized_keys"},
		{"config": good, "src": "r2.conf"},
		{"config": good, "force": true},
	} {
		if _, err := frr.Config(ctx, l.rc(), l.device("r2"), p); err == nil {
			t.Errorf("%v was accepted", p)
		}
	}
}

// TestRegistered: every method is implemented, checks, and declares what
// its undo may record.
func TestRegistered(t *testing.T) {
	for _, name := range []string{"net.frr.config", "net.frr.interface", "net.frr.facts", "net.frr.command"} {
		d, ok := collection.Lookup(name)
		if !ok || d.Manifest.Status != collection.StatusImplemented || !d.Manifest.SupportsCheck || d.Check == nil {
			t.Errorf("%s: registered %v, %+v", name, ok, d.Manifest)
		}
	}
}
