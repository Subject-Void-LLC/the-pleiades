// Tests for what a failure-pattern review of net.snmp.set and net.snmp.get
// found, against the in-process agent: an undo recorded before the SET, an
// object a write removes, a secret object, the agent's security tables, and
// one bound over every walk of a task.
package snmp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSet_TheUndoIsRecordedBeforeTheSET: over UDP an agent can apply a SET
// whose answer never arrives. The task then fails on the timeout, and the
// undo, writing back the value read before, is already recorded and kept.
func TestSet_TheUndoIsRecordedBeforeTheSET(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	store.Unanswered[snmp.OIDSysLocation] = true
	_, item := agentDevice(t, store, "2c")
	rc := newRecorder(wire.SNMPCredential{Community: community})
	_, err := call(t, "net.snmp.set", rc, item, map[string]any{"oid": "sysLocation.0", "type": "octet-string", "value": "rack 3"}, false)
	if err == nil {
		t.Fatal("a SET with no answer succeeded")
	}
	if v, _ := store.Value(snmp.OIDSysLocation); string(v.Value.([]byte)) != "rack 3" {
		t.Fatalf("the agent did not apply the SET: %q", v.Value)
	}
	inv, undo := undoOf(rc)
	if undo["value"] != "rack 1" || inv[sdk.InverseKeepOnFailureKey] != true {
		t.Fatalf("the undo of a SET whose answer was lost = %v, want the value read before, kept", inv)
	}
}

// TestSet_AnObjectTheWriteRemovesIsNamedForWhatItIs: a write that removes
// its object (a RowStatus destroy) fails the task saying the object is gone,
// never that the agent dropped the write.
func TestSet_AnObjectTheWriteRemovesIsNamedForWhatItIs(t *testing.T) {
	const rowStatus = "1.3.6.1.4.1.99.3.1.9.1"
	// SNMPv2c answers the read back noSuchObject; SNMPv1 answers it with the
	// noSuchName error status, read as no-such-instance.
	for version, gone := range map[string]snmp.Type{"2c": snmp.TypeNoSuchObject, "1": snmp.TypeNoSuchInstance} {
		t.Run("SNMPv"+version, func(t *testing.T) {
			store := snmptest.NewStore(community, snmptest.Int(rowStatus, 1))
			store.Removes[rowStatus] = true
			_, item := agentDevice(t, store, version)
			rc := newRecorder(wire.SNMPCredential{Community: community})
			_, err := call(t, "net.snmp.set", rc, item, map[string]any{"oid": rowStatus, "type": "integer", "value": "6"}, false)
			if err == nil || !strings.Contains(err.Error(), "is gone after it") || strings.Contains(err.Error(), "drop it") {
				t.Fatalf("a destroy = %v, want the object named gone", err)
			}
			if _, undo := undoOf(rc); undo["value"] != "1" {
				t.Errorf("the destroy's undo = %v", undo)
			}
			diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
			if after, _ := diff[sdk.DiffAfter].(map[string]any); after["type"] != string(gone) {
				t.Errorf("the diff after a destroy = %v", diff)
			}
		})
	}
}

// TestSet_SNMPv1RefusesAnAbsentObject: an SNMPv1 agent answers a read of an
// object it lacks with noSuchName, and net.snmp.set refuses the write with
// nothing sent, as it does for SNMPv2c's no-such-object.
func TestSet_SNMPv1RefusesAnAbsentObject(t *testing.T) {
	agent, item := agentDevice(t, snmptest.NewStore(community, snmptest.System()...), "1")
	_, err := call(t, "net.snmp.set", newRecorder(wire.SNMPCredential{Community: community}), item, map[string]any{"oid": "1.3.6.1.2.1.1.99.0", "type": "integer", "value": "1"}, false)
	if err == nil || !strings.Contains(err.Error(), "holds no such object") {
		t.Fatalf("a write of an absent object over SNMPv1 = %v", err)
	}
	for _, r := range agent.Requests() {
		if r.PDUType == gosnmp.SetRequest {
			t.Fatal("a SET was sent")
		}
	}
}

// TestSet_ASecretObjectIsNeitherShownNorUndone: with secret: true the diff
// shows no value, no undo is recorded, the values join the run's masking set,
// and the call says from its params alone that it records no undo.
func TestSet_ASecretObjectIsNeitherShownNorUndone(t *testing.T) {
	const key = "1.3.6.1.4.1.99.4.1.0"
	store := snmptest.NewStore(community, snmptest.Text(key, "old-radius-key-1"))
	_, item := agentDevice(t, store, "2c")
	rc := &secretRecorder{recorder: newRecorder(wire.SNMPCredential{Community: community})}
	params := map[string]any{"oid": key, "type": "octet-string", "value": "new-radius-key-2", "secret": true}
	if _, err := call(t, "net.snmp.set", rc, item, params, false); err != nil {
		t.Fatal(err)
	}
	shown := fmt.Sprint(rc.stats[sdk.StatDiff])
	if strings.Contains(shown, "radius-key") || !strings.Contains(shown, "(secret, not shown)") || rc.stats[sdk.StatInverse] != nil {
		t.Fatalf("a secret write recorded diff %s and inverse %v", shown, rc.stats[sdk.StatInverse])
	}
	if !strings.Contains(strings.Join(rc.registered, " "), "old-radius-key-1") || !strings.Contains(strings.Join(rc.registered, " "), "new-radius-key-2") {
		t.Errorf("the run's masking set gained %v", rc.registered)
	}
	d, _ := collection.Lookup("net.snmp.set")
	if d.UndoCall(params) == "" || d.UndoCall(map[string]any{"oid": key}) != "" {
		t.Error("UndoCall does not say a secret write records no undo")
	}
}

// secretRecorder is a recorder that keeps what a method adds to the run's
// masking set (sdk.SecretRegistrar).
type secretRecorder struct {
	*recorder
	registered []string
}

// RegisterSecret implements sdk.SecretRegistrar.
func (r *secretRecorder) RegisterSecret(values ...string) {
	r.registered = append(r.registered, values...)
}

// TestGet_TheAgentsSecurityTablesAreNeverRead: an OID in the agent's own
// security configuration is refused before anything is sent, and a walk
// crossing it leaves every binding there out, OID and value, saying how
// many, while the engine group under the same snmpModules is read.
func TestGet_TheAgentsSecurityTablesAreNeverRead(t *testing.T) {
	// vacmGroupName indexed by securityModel 2 and the security name
	// "public", spelled as sub-identifiers, as an agent may index it.
	const vacm = "1.3.6.1.6.3.16.1.2.1.3.2.6.112.117.98.108.105.99"
	store := snmptest.NewStore(community, append(snmptest.System(), snmptest.Text(vacm, "group-public"))...)
	agent, item := agentDevice(t, store, "2c")
	rc := newRecorder(wire.SNMPCredential{Community: community})
	if _, err := call(t, "net.snmp.get", rc, item, map[string]any{"oids": []any{snmp.OIDSNMPModules}, "walk": true}, false); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprint(rc.stats["values"])
	if strings.Contains(read, "1.3.6.1.6.3.16") || strings.Contains(read, "group-public") || !strings.Contains(read, snmp.OIDEngineID) {
		t.Fatalf("a walk of snmpModules read %s", read)
	}
	if !strings.Contains(fmt.Sprint(rc.stats[sdk.StatWarnings]), "1 bindings under the agent's own security configuration") {
		t.Errorf("warnings = %v", rc.stats[sdk.StatWarnings])
	}
	before := len(agent.Requests())
	for _, oids := range [][]any{{vacm}, {"1.3.6.1.6.3.16"}, {"1.3.6.1.6.3.15.1.2.2"}} {
		if _, err := call(t, "net.snmp.get", newRecorder(wire.SNMPCredential{Community: community}), item, map[string]any{"oids": oids, "walk": true}, false); err == nil {
			t.Errorf("a walk of %v was read", oids)
		}
	}
	if len(agent.Requests()) != before {
		t.Error("a refused read reached the agent")
	}
}

// TestGet_OneBoundHoldsOverEveryWalk: a task's walks together read at most
// snmp.MaxWalkResults bindings, however many roots it names, and say so.
func TestGet_OneBoundHoldsOverEveryWalk(t *testing.T) {
	const table = "1.3.6.1.4.1.99.5"
	agent := snmptest.Start(t, snmptest.Rows(community, table, 6000, 1))
	item, err := generic.NewSNMP(record.Record{Name: "big-table", Type: generic.TypeSNMP, Properties: map[string]any{
		generic.HostProperty: "127.0.0.1", snmp.PortProperty: agent.Port(), snmp.VersionProperty: "2c",
		snmp.AllowCleartextCommunityProperty: true, snmp.TimeoutProperty: 2, snmp.RetriesProperty: 0, snmp.MaxRepetitionsProperty: 100,
	}})
	if err != nil {
		t.Fatal(err)
	}
	rc := newRecorder(wire.SNMPCredential{Community: community})
	if _, err := call(t, "net.snmp.get", rc, item, map[string]any{"oids": []any{table, table}, "walk": true, "max_results": 10000}, false); err != nil {
		t.Fatal(err)
	}
	values, _ := rc.stats["values"].([]map[string]any)
	if len(values) != snmp.MaxWalkResults || rc.stats["truncated"] != true {
		t.Fatalf("two walks of 6000 rows read %d bindings, truncated %v; want %d and truncated", len(values), rc.stats["truncated"], snmp.MaxWalkResults)
	}
}

// TestCounter32Holds keeps the store's Counter32 a Counter32, so the counter
// case of TestSet_RefusesBeforeSending writes to an object of the type it
// names and is refused only for being a counter.
func TestCounter32Holds(t *testing.T) {
	store := snmptest.NewStore(community, snmptest.System()...)
	if v, ok := store.Value("1.3.6.1.2.1.2.2.1.10.2"); !ok || v.Type != gosnmp.Counter32 {
		t.Fatalf("ifInOctets.2 = %+v", v)
	}
}
