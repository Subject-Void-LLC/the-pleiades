// Tests for the net.snmp.* methods against the in-process agent
// (pkg/snmp/snmptest) over SNMPv1 and SNMPv2c, through each method's
// registered entry point and a real generic_snmp device: what they read,
// what they record, what they refuse before anything is sent, and the SET's
// read before, read back and undo. pkg/snmp/snmplive runs the same methods
// against net-snmp over every version and algorithm.
package snmp_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/gosnmp/gosnmp"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// community is the test agent's throwaway community.
const community = "test-agent-community"

// recorder is a task's context: the community, and what the method records.
type recorder struct {
	mu      sync.Mutex
	secrets map[string]string
	stats   map[string]any
	facts   map[string]any
}

// newRecorder hands a method cred.
func newRecorder(cred wire.SNMPCredential) *recorder {
	return &recorder{secrets: cred.Secrets(), stats: map[string]any{}, facts: map[string]any{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (r *recorder) InjectSecrets() map[string]string { return r.secrets }

// SetStat implements sdk.RunbookContext.
func (r *recorder) SetStat(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (r *recorder) EmitFact(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts[key] = value
	return nil
}

// agentDevice starts an agent serving store and returns a generic_snmp at
// it, speaking version with its opt-in.
func agentDevice(t *testing.T, store *snmptest.Store, version string) (*snmptest.Agent, inventory.InventoryItem) {
	t.Helper()
	agent := snmptest.Start(t, store.Handle)
	item, err := generic.NewSNMP(record.Record{Name: "test-agent", Type: generic.TypeSNMP, Properties: map[string]any{
		generic.HostProperty: "127.0.0.1", snmp.PortProperty: agent.Port(), snmp.VersionProperty: version,
		snmp.AllowCleartextCommunityProperty: true, snmp.TimeoutProperty: 1, snmp.RetriesProperty: 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return agent, item
}

// call runs fqcn's registered Invoke, or with check its Check.
func call(t *testing.T, fqcn string, rc sdk.RunbookContext, item inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, item, params)
	}
	return d.Invoke(context.Background(), rc, item, params)
}

// TestFacts_StatsOverACommunityAndTheTableJoined: over SNMPv2c the system
// group, engine and interfaces are stats and never facts, with a warning
// saying why; the interface table is joined by ifIndex, its statuses named,
// its address hex and a saturated ifSpeed taken from ifHighSpeed; and a value
// echoing the community is masked.
func TestFacts_StatsOverACommunityAndTheTableJoined(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	store.Put(snmptest.Text("1.3.6.1.2.1.1.1.0", "Linux test-agent, configured with "+community))
	agent, item := agentDevice(t, store, "2c")
	rc := newRecorder(wire.SNMPCredential{Community: community})
	if _, err := call(t, "net.snmp.facts", rc, item, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("a community read recorded facts: %v", rc.facts)
	}
	if rc.stats["snmp.version"] != "2c" || rc.stats["snmp.security_level"] != "community" || rc.stats["snmp.sys_name"] != "test-agent" ||
		rc.stats["snmp.sys_object_id"] != "1.3.6.1.4.1.8072.3.2.10" || rc.stats["snmp.sys_uptime"] != int64(12345) || rc.stats["snmp.engine_boots"] != int64(3) {
		t.Errorf("stats = %v", rc.stats)
	}
	if descr := fmt.Sprint(rc.stats["snmp.sys_descr"]); strings.Contains(descr, community) || !strings.HasPrefix(descr, "Linux test-agent") {
		t.Errorf("snmp.sys_descr = %q, want the community masked", descr)
	}
	if id := fmt.Sprint(rc.stats["snmp.engine_id"]); !strings.HasPrefix(id, "80001f8880") {
		t.Errorf("snmp.engine_id = %q", id)
	}
	ifaces, _ := rc.stats["snmp.interfaces"].([]map[string]any)
	if len(ifaces) != 2 || rc.stats["snmp.interface_count"] != 2 {
		t.Fatalf("snmp.interfaces = %v (warnings %v)", rc.stats["snmp.interfaces"], rc.stats[sdk.StatWarnings])
	}
	eth := ifaces[1]
	if eth["descr"] != "eth0" || eth["name"] != "eth0" || eth["oper_status"] != "down" || eth["admin_status"] != "up" || eth["phys_address"] != "0242ac110002" ||
		eth["speed"] != int64(10_000_000_000) || eth["high_speed"] != nil || ifaces[0]["name"] != nil {
		t.Errorf("eth0 = %v", eth)
	}
	warnings := fmt.Sprint(rc.stats[sdk.StatWarnings])
	if !strings.Contains(warnings, "in the clear") || !strings.Contains(warnings, "nothing this read found is recorded as a fact") {
		t.Errorf("warnings = %s", warnings)
	}
	// One GET for the system group, one for the engine, one walk per column.
	if n := len(agent.Requests()); n < 13 {
		t.Errorf("the agent saw %d requests", n)
	}
}

// TestFacts_SNMPv1ReadsWhatItCan: an SNMPv1 agent missing one system object
// refuses the group's GET whole (noSuchName), and the method reads the rest
// one by one; one holding none of them fails the task.
func TestFacts_SNMPv1ReadsWhatItCan(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.Text("1.3.6.1.2.1.1.5.0", "old-switch"), snmptest.Int("1.3.6.1.2.1.2.2.1.1.1", 1))
	_, item := agentDevice(t, store, "1")
	rc := newRecorder(wire.SNMPCredential{Community: community})
	if _, err := call(t, "net.snmp.facts", rc, item, nil, false); err != nil {
		t.Fatal(err)
	}
	if rc.stats["snmp.sys_name"] != "old-switch" || rc.stats["snmp.version"] != "1" || rc.stats["snmp.sys_descr"] != nil || rc.stats["snmp.interface_count"] != 1 {
		t.Errorf("stats = %v", rc.stats)
	}
	_, empty := agentDevice(t, snmptest.NewStore(community), "1")
	if _, err := call(t, "net.snmp.facts", newRecorder(wire.SNMPCredential{Community: community}), empty, nil, false); err == nil {
		t.Error("an agent holding no system object was recorded")
	}
	if _, err := call(t, "net.snmp.facts", newRecorder(wire.SNMPCredential{Community: community}), item, map[string]any{"max_interfaces": 0}, false); err == nil {
		t.Error("max_interfaces 0 was accepted")
	}
}

// TestGet_ReadsWalksAndRefusesBeforeSending: a GET answers each OID, an
// absent one by its type; a walk stops at max_results saying so; and a
// malformed call is refused with nothing sent.
func TestGet_ReadsWalksAndRefusesBeforeSending(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	agent, item := agentDevice(t, store, "2c")
	cred := wire.SNMPCredential{Community: community}
	rc := newRecorder(cred)
	if _, err := call(t, "net.snmp.get", rc, item, map[string]any{"oids": []any{"SNMPv2-MIB::sysName.0", "1.3.6.1.2.1.1.99.0", "ifPhysAddress.2"}}, false); err != nil {
		t.Fatal(err)
	}
	values, _ := rc.stats["values"].([]map[string]any)
	if len(values) != 3 || values[0]["value"] != "test-agent" || values[1]["type"] != "no-such-object" || values[2]["value"] != "0x0242ac110002" || values[2]["hex"] != true || rc.stats["truncated"] != nil {
		t.Errorf("values = %v", rc.stats["values"])
	}
	walk := newRecorder(cred)
	if _, err := call(t, "net.snmp.get", walk, item, map[string]any{"oids": []any{"ifDescr"}, "walk": true, "max_results": 1}, false); err != nil {
		t.Fatal(err)
	}
	if values, _ := walk.stats["values"].([]map[string]any); len(values) != 1 || walk.stats["truncated"] != true {
		t.Errorf("a walk of 1 = %v, truncated %v", walk.stats["values"], walk.stats["truncated"])
	}
	before := len(agent.Requests())
	for name, params := range map[string]map[string]any{
		"no oids":              {},
		"an unknown name":      {"oids": []any{"ciscoEnvMonTemperature.1"}},
		"max_results alone":    {"oids": []any{"sysName.0"}, "max_results": 5},
		"max_results too high": {"oids": []any{"sysName.0"}, "walk": true, "max_results": 10001},
		"oids not a list":      {"oids": 7},
	} {
		if _, err := call(t, "net.snmp.get", newRecorder(cred), item, params, false); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if after := len(agent.Requests()); after != before {
		t.Errorf("a refused call sent %d requests", after-before)
	}
	if _, err := call(t, "net.snmp.get", newRecorder(cred), nil, map[string]any{"oids": []any{"sysName.0"}}, false); err == nil {
		t.Error("a call with no device was accepted")
	}
}

// undoOf is the undo rc's method recorded, and its parameters.
func undoOf(rc *recorder) (map[string]any, map[string]any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	return inv, params
}

// TestSet_ChecksWritesReadsBackAndUndoes: a check predicts and sends no SET;
// a run writes, reads back and records the exact undo, which put back as
// recorded restores the value; a second run sends no SET; a binary value's
// undo is hex; and a SET the agent drops fails with its undo kept.
func TestSet_ChecksWritesReadsBackAndUndoes(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	store.Put(gosnmp.SnmpPDU{Name: "1.3.6.1.4.1.99.1.0", Type: gosnmp.OctetString, Value: []byte{0x00, 0xff}})
	store.Put(snmptest.Text("1.3.6.1.4.1.99.2.0", "kept"))
	store.Drops["1.3.6.1.4.1.99.2.0"] = true
	agent, item := agentDevice(t, store, "2c")
	cred := wire.SNMPCredential{Community: community}
	sets := func() int {
		n := 0
		for _, r := range agent.Requests() {
			if r.PDUType == gosnmp.SetRequest {
				n++
			}
		}
		return n
	}
	params := map[string]any{"oid": "sysLocation.0", "type": "octet-string", "value": "rack 2"}

	check := newRecorder(cred)
	if res, err := call(t, "net.snmp.set", check, item, params, true); err != nil || !res.Changed || sets() != 0 || check.stats[sdk.StatInverse] != nil {
		t.Fatalf("check = %+v, %v, %d SETs", res, err, sets())
	}
	rc := newRecorder(cred)
	if res, err := call(t, "net.snmp.set", rc, item, params, false); err != nil || !res.Changed || sets() != 1 {
		t.Fatalf("set = %+v, %v, %d SETs", res, err, sets())
	}
	inv, undo := undoOf(rc)
	if inv[sdk.InverseFQCNKey] != "net.snmp.set" || undo["oid"] != snmp.OIDSysLocation || undo["type"] != "octet-string" || undo["value"] != "rack 1" || inv[sdk.InverseKeepOnFailureKey] != true {
		t.Fatalf("undo = %v", inv)
	}
	if res, err := call(t, "net.snmp.set", newRecorder(cred), item, params, false); err != nil || res.Changed || sets() != 1 {
		t.Fatalf("a second run = %+v, %v, %d SETs", res, err, sets())
	}
	if _, err := call(t, "net.snmp.set", newRecorder(cred), item, undo, false); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Value(snmp.OIDSysLocation); string(v.Value.([]byte)) != "rack 1" {
		t.Fatalf("the undo left sysLocation.0 at %q", v.Value)
	}

	binary := newRecorder(cred)
	if _, err := call(t, "net.snmp.set", binary, item, map[string]any{"oid": "1.3.6.1.4.1.99.1.0", "type": "octet-string", "value": "text now"}, false); err != nil {
		t.Fatal(err)
	}
	if _, undo := undoOf(binary); undo["type"] != "hex-string" || undo["value"] != "0x00ff" {
		t.Errorf("a binary value's undo = %v", undo)
	}

	dropped := newRecorder(cred)
	_, err := call(t, "net.snmp.set", dropped, item, map[string]any{"oid": "1.3.6.1.4.1.99.2.0", "type": "octet-string", "value": "gone"}, false)
	if err == nil || !strings.Contains(err.Error(), "drop it") {
		t.Fatalf("a dropped SET = %v", err)
	}
	if _, undo := undoOf(dropped); undo["value"] != "kept" {
		t.Errorf("a dropped SET's undo = %v", undo)
	}
}

// routed is a task's context for a device reached through a route of SSH
// hops (sdk.Router).
type routed struct{ *recorder }

// DeviceRoute implements sdk.Router with a dialer that is never called.
func (routed) DeviceRoute() sdk.RouteDial {
	return func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("never dialed") }
}

// TestMethods_RefuseARoutedDevice: SNMP is UDP, which a route of SSH hops
// does not carry, so every method refuses a device reached through one
// before anything is sent.
func TestMethods_RefuseARoutedDevice(t *testing.T) {
	agent, item := agentDevice(t, snmptest.NewStore(community, snmptest.System()...), "2c")
	for fqcn, params := range map[string]map[string]any{
		"net.snmp.facts": nil,
		"net.snmp.get":   {"oids": []any{"sysName.0"}},
		"net.snmp.set":   {"oid": "sysLocation.0", "type": "octet-string", "value": "x"},
	} {
		_, err := call(t, fqcn, routed{newRecorder(wire.SNMPCredential{Community: community})}, item, params, false)
		if err == nil || !strings.Contains(err.Error(), "route of SSH hops") {
			t.Errorf("%s on a routed device = %v", fqcn, err)
		}
	}
	if n := len(agent.Requests()); n != 0 {
		t.Errorf("a routed device's agent was sent %d requests", n)
	}
}

// TestSet_RefusesBeforeSending: another type, an absent object, an object
// under snmpModules, a counter, a malformed value and a read-only object are
// each refused, and only the read-only one reaches a SET, which the agent
// refuses.
func TestSet_RefusesBeforeSending(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	store.ReadOnly["1.3.6.1.2.1.1.1.0"] = true
	agent, item := agentDevice(t, store, "2c")
	cred := wire.SNMPCredential{Community: community}
	for name, params := range map[string]map[string]any{
		"another type":    {"oid": "sysLocation.0", "type": "integer", "value": 7},
		"an absent one":   {"oid": "1.3.6.1.2.1.1.99.0", "type": "integer", "value": "7"},
		"snmpModules":     {"oid": "snmpEngineID.0", "type": "hex-string", "value": "0x80001f8880"},
		"a counter":       {"oid": "sysLocation.0", "type": "counter32", "value": "1"},
		"a bad integer":   {"oid": "sysServices.0", "type": "integer", "value": "seven"},
		"a fraction":      {"oid": "sysServices.0", "type": "integer", "value": 7.5},
		"no value":        {"oid": "sysServices.0", "type": "integer"},
		"a list value":    {"oid": "sysServices.0", "type": "integer", "value": []any{1}},
		"no oid":          {"type": "integer", "value": "1"},
		"no type":         {"oid": "sysServices.0", "value": "1"},
		"a read-only one": {"oid": "sysDescr.0", "type": "octet-string", "value": "renamed"},
	} {
		if _, err := call(t, "net.snmp.set", newRecorder(cred), item, params, false); err == nil {
			t.Errorf("%s was written", name)
		}
	}
	sets := 0
	for _, r := range agent.Requests() {
		if r.PDUType == gosnmp.SetRequest {
			sets++
		}
	}
	if sets != 1 {
		t.Errorf("%d SETs were sent, want only the read-only object's", sets)
	}
	if res, err := call(t, "net.snmp.set", newRecorder(cred), item, map[string]any{"oid": "sysServices.0", "type": "integer", "value": 72}, false); err != nil || res.Changed {
		t.Errorf("an unquoted number equal to the value = %+v, %v", res, err)
	}
}
