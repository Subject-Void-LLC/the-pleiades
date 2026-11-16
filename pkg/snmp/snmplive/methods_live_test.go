// Package snmplive: the net.snmp.* methods, wait.snmp and the onboarding
// probe against net-snmp, through each one's registered entry point and a
// real generic_snmp device built by its own constructor.
package snmplive

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/snmp" // registers net.snmp.*
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"     // registers wait.snmp
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// recorder is a task's context: the device's SNMP credential, and what the
// method records.
type recorder struct {
	mu      sync.Mutex
	secrets map[string]string
	stats   map[string]any
	facts   map[string]any
}

// newRecorder is a context handing a method cred.
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

// warnings is the warnings stat as text.
func (r *recorder) warnings() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprint(r.stats[sdk.StatWarnings])
}

// recordedUndo is the undo rc's method recorded, and its parameters.
func recordedUndo(rc *recorder) (map[string]any, map[string]any) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	return inv, params
}

// device is a generic_snmp built by its own constructor for agent, with
// props added.
func device(t *testing.T, agent *testsupport.SNMPD, props map[string]any) inventory.InventoryItem {
	t.Helper()
	all := map[string]any{generic.HostProperty: agent.Host, snmp.PortProperty: agent.Port, snmp.TimeoutProperty: 2}
	for k, v := range props {
		all[k] = v
	}
	item, err := generic.NewSNMP(record.Record{Name: "lab-agent", Type: generic.TypeSNMP, Properties: all})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// invoke runs fqcn's registered Invoke, or with check its Check.
func invoke(t *testing.T, fqcn string, rc sdk.RunbookContext, item inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
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

// TestFactsGetAndWaitAgainstNetSNMP: net.snmp.facts records the system
// group, the engine ID and the interfaces as facts over SNMPv3 authPriv and
// as stats only over SNMPv2c, saying why; net.snmp.get reads and walks;
// wait.snmp answers, and stops at once on a wrong passphrase.
func TestFactsGetAndWaitAgainstNetSNMP(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	v3 := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA, wire.SNMPPrivAES128)].Cred
	v3Device := device(t, agent, nil)
	v2Device := device(t, agent, map[string]any{snmp.VersionProperty: "2c", snmp.AllowCleartextCommunityProperty: true})

	rc := newRecorder(v3)
	if _, err := invoke(t, "net.snmp.facts", rc, v3Device, nil, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"snmp.version", "snmp.security_level", "snmp.sys_descr", "snmp.sys_object_id", "snmp.sys_name", "snmp.engine_id", "snmp.interface_count", "snmp.interfaces"} {
		if rc.facts[key] == nil {
			t.Errorf("net.snmp.facts over authPriv recorded no fact %s", key)
		}
	}
	if rc.facts["snmp.security_level"] != "authPriv" || !strings.HasPrefix(fmt.Sprint(rc.facts["snmp.sys_object_id"]), "1.3.6.1.4.1.8072") {
		t.Errorf("facts = %v", rc.facts)
	}
	ifaces, _ := rc.facts["snmp.interfaces"].([]map[string]any)
	if len(ifaces) == 0 || ifaces[0]["descr"] == nil || ifaces[0]["oper_status"] == nil {
		t.Errorf("snmp.interfaces = %v", rc.facts["snmp.interfaces"])
	}

	v2rc := newRecorder(wire.SNMPCredential{Community: agent.ReadCommunity})
	if _, err := invoke(t, "net.snmp.facts", v2rc, v2Device, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(v2rc.facts) != 0 || v2rc.stats["snmp.sys_descr"] == nil || !strings.Contains(v2rc.warnings(), "in the clear") || !strings.Contains(v2rc.warnings(), "nothing this read found is recorded as a fact") {
		t.Errorf("over SNMPv2c: facts %v, warnings %s", v2rc.facts, v2rc.warnings())
	}
	if rc.stats["snmp.engine_id"] != v2rc.stats["snmp.engine_id"] {
		t.Errorf("engine ID %v over SNMPv3, %v over SNMPv2c: one agent", rc.stats["snmp.engine_id"], v2rc.stats["snmp.engine_id"])
	}

	get := newRecorder(v3)
	if _, err := invoke(t, "net.snmp.get", get, v3Device, map[string]any{"oids": []any{"sysName.0", "1.3.6.1.2.1.1.99.0"}}, false); err != nil {
		t.Fatal(err)
	}
	values, _ := get.stats["values"].([]map[string]any)
	if len(values) != 2 || values[0]["type"] != "octet-string" || values[1]["type"] != "no-such-object" {
		t.Errorf("net.snmp.get = %v", get.stats["values"])
	}
	walk := newRecorder(v3)
	if _, err := invoke(t, "net.snmp.get", walk, v3Device, map[string]any{"oids": []any{"system"}, "walk": true, "max_results": 3}, false); err != nil {
		t.Fatal(err)
	}
	if values, _ := walk.stats["values"].([]map[string]any); len(values) != 3 || walk.stats["truncated"] != true {
		t.Errorf("a walk of 3 = %v, truncated %v", walk.stats["values"], walk.stats["truncated"])
	}

	wait := newRecorder(v3)
	if _, err := invoke(t, "wait.snmp", wait, v3Device, map[string]any{"timeout": 30, "connect_timeout": 10}, false); err != nil {
		t.Fatal(err)
	}
	if wait.stats["version"] != "3" || wait.stats["tries"] != 1 {
		t.Errorf("wait.snmp stats = %v", wait.stats)
	}
	wrong := v3
	wrong.AuthPassphrase = "not-the-passphrase"
	_, err := invoke(t, "wait.snmp", newRecorder(wrong), v3Device, map[string]any{"timeout": 60, "sleep": 1}, false)
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wait.snmp with a wrong passphrase = %v, want it stopped at the first try", err)
	}
}

// TestSetAgainstNetSNMP: net.snmp.set predicts in check mode and writes
// nothing; writes, reads back and records an exact undo; changes nothing on a
// second run; is put back by running its undo as recorded; refuses a value
// of another type, an object the agent does not hold, and snmpModules; and
// fails a SET the agent answered and dropped, keeping its undo.
func TestSetAgainstNetSNMP(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	writer := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA512, wire.SNMPPrivAES256)].Cred
	item := device(t, agent, nil)
	read := func() string {
		rc := newRecorder(writer)
		if _, err := invoke(t, "net.snmp.get", rc, item, map[string]any{"oids": []any{"sysLocation.0"}}, false); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(rc.stats["values"].([]map[string]any)[0]["value"])
	}
	original := read()
	params := map[string]any{"oid": "sysLocation.0", "type": "octet-string", "value": "rack 12, set by net.snmp.set"}

	check := newRecorder(writer)
	res, err := invoke(t, "net.snmp.set", check, item, params, true)
	if err != nil || !res.Changed || check.stats[sdk.StatInverse] != nil || read() != original {
		t.Fatalf("check: %+v, %v, inverse %v, sysLocation %q", res, err, check.stats[sdk.StatInverse], read())
	}

	rc := newRecorder(writer)
	if res, err = invoke(t, "net.snmp.set", rc, item, params, false); err != nil || !res.Changed || read() != params["value"] {
		t.Fatalf("set: %+v, %v, sysLocation %q", res, err, read())
	}
	inv, undoParams := recordedUndo(rc)
	if inv[sdk.InverseFQCNKey] != "net.snmp.set" || undoParams["oid"] != snmp.OIDSysLocation || undoParams["value"] != original || inv[sdk.InverseKeepOnFailureKey] != true {
		t.Fatalf("the recorded undo = %#v", rc.stats[sdk.StatInverse])
	}

	again := newRecorder(writer)
	if res, err = invoke(t, "net.snmp.set", again, item, params, false); err != nil || res.Changed || again.stats[sdk.StatInverse] != nil {
		t.Fatalf("a second run: %+v, %v, inverse %v", res, err, again.stats[sdk.StatInverse])
	}

	undo := newRecorder(writer)
	if _, err := invoke(t, "net.snmp.set", undo, item, undoParams, false); err != nil || read() != original {
		t.Fatalf("the undo as recorded: %v, sysLocation %q, want %q", err, read(), original)
	}

	for name, bad := range map[string]map[string]any{
		"another type":   {"oid": "sysLocation.0", "type": "integer", "value": 7},
		"an absent one":  {"oid": "1.3.6.1.2.1.1.99.0", "type": "integer", "value": 7},
		"snmpModules":    {"oid": "1.3.6.1.6.3.16.1.5.2.1.6.1.1", "type": "integer", "value": 1},
		"a counter type": {"oid": "sysLocation.0", "type": "counter32", "value": 1},
	} {
		if _, err := invoke(t, "net.snmp.set", newRecorder(writer), item, bad, false); err == nil {
			t.Errorf("%s was written", name)
		}
	}
	if read() != original {
		t.Fatalf("a refused write changed sysLocation.0 to %q", read())
	}

	dropped := newRecorder(writer)
	_, err = invoke(t, "net.snmp.set", dropped, item, map[string]any{"oid": testsupport.SNMPDDropOID, "type": "octet-string", "value": "a value nobody keeps"}, false)
	if err == nil || !strings.Contains(err.Error(), "accept a write and drop it") {
		t.Fatalf("a dropped SET = %v, want it refused as not made", err)
	}
	if _, kept := recordedUndo(dropped); kept["value"] != "kept as it was" {
		t.Errorf("a dropped SET's undo = %#v, want the value read before", dropped.stats[sdk.StatInverse])
	}
}

// TestOnboardingProbesTheRealAgent: the registered SNMP prober grants
// SNMPCapable from an authenticated read, records the system group, the
// engine ID and the MIB groups the agent answers, and warns about a
// community; a wrong passphrase proves nothing.
func TestOnboardingProbesTheRealAgent(t *testing.T) {
	agent := testsupport.StartSNMPD(t)
	prober, ok := onboard.Lookup(generic.TypeSNMP)
	if !ok {
		t.Fatal("generic_snmp has no prober")
	}
	v3 := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA, wire.SNMPPrivAES256Reeder)].Cred
	probed, err := prober.Probe(context.Background(), device(t, agent, nil), v3.Secrets())
	if err != nil {
		t.Fatal(err)
	}
	if len(probed.Capabilities) != 1 || probed.Capabilities[0] != capability.NameSNMP || probed.Facts["snmp_security_level"] != "authPriv" ||
		probed.Facts["snmp_engine_id"] == nil || !strings.HasPrefix(fmt.Sprint(probed.Facts["snmp_sys_descr"]), "Linux") || len(probed.Warnings) != 0 {
		t.Fatalf("probed %+v", probed)
	}
	if mibs := fmt.Sprint(probed.Facts["snmp_mibs"]); !strings.Contains(mibs, "system") || !strings.Contains(mibs, "interfaces") {
		t.Errorf("snmp_mibs = %s", mibs)
	}
	v2, err := prober.Probe(context.Background(), device(t, agent, map[string]any{snmp.VersionProperty: "2c", snmp.AllowCleartextCommunityProperty: true}),
		wire.SNMPCredential{Community: agent.ReadCommunity}.Secrets())
	if err != nil || v2.Facts["snmp_engine_id"] != probed.Facts["snmp_engine_id"] || !strings.Contains(strings.Join(v2.Warnings, " "), "nothing proves which agent") {
		t.Fatalf("the v2c probe = %+v, %v", v2, err)
	}
	wrong := v3
	wrong.AuthPassphrase = "not-the-passphrase"
	if _, err := prober.Probe(context.Background(), device(t, agent, nil), wrong.Secrets()); err == nil {
		t.Fatal("a wrong passphrase proved the device")
	}
}
