// Package snmp: "net.snmp.set", one object written: read before, so a check
// predicts and a run that changes nothing sends nothing; written in one SET;
// read back, since an agent can accept a write and drop it; and its undo
// recorded exactly, the value read before written back with its own type.
package snmp

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// setFQCN is the method's name.
const setFQCN = "net.snmp.set"

// net.snmp.set's parameters.
const (
	paramOID   = "oid"
	paramType  = "type"
	paramValue = "value"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: setFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSNMP},
			RequiredCapabilities: []capability.Name{capability.NameSNMP},
			ExecutionContext: collection.ExecutionContext{
				Site:   collection.SiteTarget,
				Device: collection.DeviceRequired,
			},
			EngineVersion:      ">=0.4.0",
			Status:             collection.StatusImplemented,
			NoBecomeReason:     noBecomeReason,
			SupportsCheck:      true,
			UsesSNMPCredential: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "A run that changed the object records net.snmp.set of the same OID with the type and value read before the write, which is exact: an SNMP object holds one typed value, and writing it back restores it. It is recorded as soon as the SET is answered, so it stands whatever happens after, the read back included. " +
					"The value read before is kept in the run's journal, which is why net.snmp.set refuses every object under snmpModules (1.3.6.1.6.3), where an agent keeps its users, keys and communities; a value holding one of the run's secrets is withheld by the journal, and such an undo is not replayed. A run that changed nothing records nothing.",
				Inverses: []sdk.InverseSpec{{
					FQCN:             setFQCN,
					Record:           []string{paramOID, paramType, paramValue},
					MayKeepOnFailure: true,
				}},
			},
			Doc: setDoc(),
		},
		Invoke: Set,
		Check:  SetCheck,
	})
}

// setDoc is net.snmp.set's documentation, word for word its catalog entry's.
func setDoc() collection.Doc {
	return collection.Doc{
		Summary: "Writes one object on a device's SNMP agent, reading it before and after, with an exact undo.",
		Description: "Writes value, of type, to the object oid names on the device's SNMP agent in one SET, with the SNMP family of the device's own credential: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about, as a write in the clear with a community is. " +
			"The object is read first. One the agent does not hold is refused, since creating a row is not this method's (its undo would not be exact), and so is one holding another type than type. One that already holds value is left alone: nothing is sent and the run reports no change, and a check reports what a run would change and sends no SET. " +
			"After the SET the object is read again, and a value other than the one written fails the task: an agent can accept a write and drop it (IOS XE 17.15.1 accepted SNMPv3 users it never created, measured on 2026-11-15), so the answer to the SET is not taken as the change. " +
			"A run that changed the object records its undo, net.snmp.set of the same OID with the type and value read before, as soon as the SET is answered. Every object under snmpModules (1.3.6.1.6.3), the agent's own users, keys, views and communities, is refused before anything is sent: their values would ride the undo and the run's journal. An OID is numeric or a curated name with its instance (sysContact.0), as net.snmp.get takes it. The values recorded are masked of the device's credential.",
		Params: []collection.Param{
			{Name: paramOID, Type: "string", Required: true, Description: "The object to write, one instance: numeric (1.3.6.1.2.1.1.4.0) or a curated name with its instance (sysContact.0)."},
			{Name: paramType, Type: "string", Required: true, Choices: []string{"integer", "octet-string", "hex-string", "object-identifier", "ip-address", "gauge32", "timeticks"}, Description: "The value's SNMP type: integer (a signed 32-bit number), octet-string (text, written as its bytes), hex-string (an octet string given as hex, optionally after 0x, for one that is not text), object-identifier, ip-address (IPv4, dotted), gauge32 or timeticks (unsigned 32-bit numbers). Counters are read-only (RFC 2578) and are not offered. It must be the type the agent holds."},
			{Name: paramValue, Type: "string", Required: true, Description: "The value, as text of its type: a number in decimal, an OID dotted or by curated name, an address dotted, text, or hex. An octet string is at most 65535 bytes."},
		},
		Returns: []collection.ReturnField{
			{Name: "diff", Type: "dict", Returned: "always", Description: "The object before and after, each its oid, type and value (masked), so a run that changed nothing shows the same value twice."},
		},
		Examples: []collection.Example{
			{Name: "Set a device's contact", RunbookYAML: "- name: Name the device's contact\n  net.snmp.set:\n    oid: sysContact.0\n    type: octet-string\n    value: noc@example.net\n"},
			{Name: "Shut an interface down", RunbookYAML: "- name: Set ifAdminStatus of interface 3 to down\n  net.snmp.set:\n    oid: ifAdminStatus.3\n    type: integer\n    value: \"2\"\n"},
		},
		SeeAlso: []string{"net.snmp.get", "net.snmp.facts"},
	}
}

// Set implements the "net.snmp.set" collection method.
func Set(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return set(ctx, rc, device, params, false)
}

// SetCheck is net.snmp.set's check: the same reads and refusals, and no SET.
func SetCheck(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return set(ctx, rc, device, params, true)
}

// set is one write, or with check its prediction.
func set(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, check bool) (collection.Result, error) {
	want, err := readSet(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", setFQCN, err)
	}
	s, err := open(rc, device, setFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()
	before, err := s.readOne(ctx, want.OID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: reading %s before the write: %w; nothing was written", setFQCN, device.Name(), want.OID, err)
	}
	if before.Type != want.Type {
		return collection.Result{}, fmt.Errorf("%s: device %q: the agent holds a value of type %s at %s, and the task writes %s; nothing was written", setFQCN, device.Name(), before.Type, want.OID, want.Type)
	}
	if !s.client.Authenticated() {
		if err := s.warn(rc, "the device was reached without SNMPv3 authentication, so nothing proves which agent read and wrote this object"); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", setFQCN, err)
		}
	}
	if snmp.Same(before, want) {
		return collection.Result{Changed: false}, sdk.RecordDiff(rc, sdk.Unchanged(s.binding(before)))
	}
	if check {
		return collection.Result{Changed: true}, sdk.RecordDiff(rc, sdk.Diff{Before: s.binding(before), After: s.binding(want)})
	}
	if _, err := s.client.Set(ctx, []snmp.Varbind{want}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", setFQCN, device.Name(), err)
	}
	// The SET was answered, so the change may be on the device: its undo is
	// recorded now and kept whatever the read back finds.
	if err := sdk.RecordInverse(rc, undo(before)); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", setFQCN, err)
	}
	after, err := s.readOne(ctx, want.OID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: the agent answered the SET, and reading %s back failed: %w", setFQCN, device.Name(), want.OID, err)
	}
	if err := sdk.RecordDiff(rc, sdk.Diff{Before: s.binding(before), After: s.binding(after)}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", setFQCN, err)
	}
	if !snmp.Same(after, want) {
		return collection.Result{}, fmt.Errorf("%s: device %q: the agent answered the SET and holds %v at %s, not the value written: an agent can accept a write and drop it, so the write is not taken as made",
			setFQCN, device.Name(), s.binding(after)["value"], want.OID)
	}
	return collection.Result{Changed: true}, nil
}

// readSet reads and checks the parameters into the binding to write,
// refusing an object under snmpModules.
func readSet(params map[string]any) (snmp.Varbind, error) {
	oid, err := sdk.RequiredStringParam(params, paramOID)
	if err != nil {
		return snmp.Varbind{}, err
	}
	typ, err := sdk.RequiredStringParam(params, paramType)
	if err != nil {
		return snmp.Varbind{}, err
	}
	value, err := valueParam(params)
	if err != nil {
		return snmp.Varbind{}, err
	}
	want, err := snmp.Value(oid, snmp.Type(typ), value)
	if err != nil {
		return snmp.Varbind{}, fmt.Errorf("%s: %w", paramValue, err)
	}
	if snmp.Under(want.OID, snmp.OIDSNMPModules) {
		return snmp.Varbind{}, fmt.Errorf("%s %s is under snmpModules (%s), where an agent keeps its own users, keys, views and communities; their values would ride this task's undo and the run's journal, so net.snmp.set does not write them",
			paramOID, want.OID, snmp.OIDSNMPModules)
	}
	return want, nil
}

// valueParam reads value as text, taking a number YAML handed over unquoted
// as its decimal text.
func valueParam(params map[string]any) (string, error) {
	switch v := params[paramValue].(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		if v != float64(int64(v)) {
			return "", fmt.Errorf("%s must be text or a whole number", paramValue)
		}
		return strconv.FormatInt(int64(v), 10), nil
	case nil:
		return "", fmt.Errorf("%s is required", paramValue)
	}
	return "", fmt.Errorf("%s must be text or a whole number", paramValue)
}

// readOne reads oid, refusing an object the agent does not hold: an Absent
// type, or SNMPv1's noSuchName.
func (s *session) readOne(ctx context.Context, oid string) (snmp.Varbind, error) {
	got, err := s.client.Get(ctx, []string{oid})
	var agent *snmp.AgentError
	if errors.As(err, &agent) && agent.Status == "noSuchName" {
		return snmp.Varbind{}, errors.New("the agent holds no such object, and net.snmp.set writes only an object that is there (creating a row is not offered, since its undo would not be exact)")
	}
	if err != nil {
		return snmp.Varbind{}, err
	}
	if len(got) != 1 || got[0].OID != oid {
		return snmp.Varbind{}, fmt.Errorf("the agent answered %d bindings for one OID", len(got))
	}
	if got[0].Type.Absent() {
		return snmp.Varbind{}, fmt.Errorf("the agent answered %s, and net.snmp.set writes only an object that is there (creating a row is not offered, since its undo would not be exact)", got[0].Type)
	}
	return got[0], nil
}

// undo is the instruction that writes before back: the same OID, its type,
// and its value, as hex when it is not text, so it is written byte for byte.
func undo(before snmp.Varbind) sdk.Inverse {
	typ, value := string(before.Type), before.Text
	if before.Hex {
		typ = string(snmp.HexType)
	}
	return sdk.Inverse{
		FQCN:          setFQCN,
		Params:        map[string]any{paramOID: before.OID, paramType: typ, paramValue: value},
		Description:   fmt.Sprintf("write the value %s held before back (%s)", before.OID, before.Type),
		KeepOnFailure: true,
	}
}
