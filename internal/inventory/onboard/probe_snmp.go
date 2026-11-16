// The SNMP probe: the agent's system group, its engine ID, and which of the
// MIB groups net.snmp.facts reads it answers.
package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

type snmpProber struct{}

func init() { Register(generic.TypeSNMP, snmpProber{}) }

func (snmpProber) Protocol() string { return "snmp" }

// snmpGroups are the MIB groups the probe asks the agent for, each by the
// name it is recorded under: the agent answers a group when a GETNEXT from
// its root lands inside it.
var snmpGroups = []struct{ name, root string }{
	{"system", snmp.OIDSystem},
	{"interfaces", snmp.OIDInterfaces},
	{"ifXTable", snmp.OIDIfXTable},
	{"snmpEngine", "1.3.6.1.6.3.10.2.1"},
	{"hostResources", snmp.OIDHostRes},
}

// Probe reads the agent's sysDescr, sysObjectID and sysName in one GET with
// the device's own SNMP credential (pkg/snmp.ClientFor, the client the
// net.snmp methods use), then asks for each of snmpGroups. An answer holding
// at least one of the three is what grants SNMPCapable: a GET the agent
// answered with nothing but absences proves no view of it. It records the
// version, the security level, the three values, the engine ID (the one USM
// discovered for SNMPv3, snmpEngineID.0 for a community when the agent
// answers it) and the groups the agent answers, every value masked of the
// credential and bounded. Each warning the device's settings give is
// reported, and one more for a session that authenticates nothing: nothing
// then proves which agent answered.
func (snmpProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	client, warnings, err := snmp.ClientFor(device, device.Name(), secrets)
	if err != nil {
		return Probed{}, err
	}
	defer func() { _ = client.Close() }()
	masked := snmp.MaskedSecrets(secrets)
	system, err := readSystem(ctx, client)
	if err != nil {
		return Probed{}, err
	}
	facts := map[string]any{
		"snmp_version":        string(client.Version()),
		"snmp_security_level": securityLevel(client),
	}
	answered := false
	for _, v := range system {
		// An agent answering an OID it was not asked for is not counted.
		key, asked := systemFacts[v.OID]
		if !asked || v.Type.Absent() {
			continue
		}
		answered = true
		facts[key] = factText(snmp.Mask(v, masked).Text)
	}
	if !answered {
		return Probed{}, errors.New("the agent answered, and held no sysDescr, sysObjectID or sysName for this credential: its view grants nothing to read")
	}
	if engine := engineID(ctx, client); engine != "" {
		facts["snmp_engine_id"] = engine
	}
	facts["snmp_mibs"] = factList(answeredGroups(ctx, client))
	if !client.Authenticated() {
		warnings = append(warnings, "the device is reached without SNMPv3 authentication, so nothing proves which agent answered this probe")
	}
	return Probed{Capabilities: []capability.Name{capability.NameSNMP}, Facts: facts, Warnings: warnings}, nil
}

// systemFacts names the fact each system-group OID the probe reads is
// recorded under.
var systemFacts = map[string]string{
	snmp.OIDSysDescr:    "snmp_sys_descr",
	snmp.OIDSysObjectID: "snmp_sys_object_id",
	snmp.OIDSysName:     "snmp_sys_name",
}

// readSystem reads the three system objects in one GET, or, from an SNMPv1
// agent that refuses the GET whole because it lacks one (noSuchName), one at
// a time, keeping what it holds.
func readSystem(ctx context.Context, client *snmp.Client) ([]snmp.Varbind, error) {
	oids := []string{snmp.OIDSysDescr, snmp.OIDSysObjectID, snmp.OIDSysName}
	got, err := client.Get(ctx, oids)
	var agent *snmp.AgentError
	if !errors.As(err, &agent) || agent.Status != "noSuchName" {
		return got, err
	}
	got = nil
	for _, oid := range oids {
		if one, err := client.Get(ctx, []string{oid}); err == nil {
			got = append(got, one...)
		}
	}
	return got, nil
}

// securityLevel is the session's SNMPv3 level, or "community" for SNMPv1
// and SNMPv2c.
func securityLevel(c *snmp.Client) string {
	if level := c.Level(); level != "" {
		return string(level)
	}
	return "community"
}

// engineID is the agent's engine ID: the one USM discovered, or, for a
// community, snmpEngineID.0 when the agent answers it; "" otherwise. A failed
// read of it is no failure of the probe.
func engineID(ctx context.Context, c *snmp.Client) string {
	if id := c.EngineID(); id != "" {
		return id
	}
	got, err := c.Get(ctx, []string{snmp.OIDEngineID})
	if err != nil || len(got) != 1 || got[0].Type != snmp.TypeOctetString || len(got[0].Bytes) == 0 || len(got[0].Bytes) > 32 {
		return ""
	}
	return fmt.Sprintf("%x", got[0].Bytes)
}

// answeredGroups names each of snmpGroups the agent answers. A group whose
// GETNEXT fails, or lands past it, is left out; neither fails the probe.
func answeredGroups(ctx context.Context, c *snmp.Client) []string {
	var out []string
	for _, g := range snmpGroups {
		got, err := c.GetNext(ctx, []string{g.root})
		if err == nil && len(got) == 1 && !got[0].Type.Absent() && snmp.Under(got[0].OID, g.root) {
			out = append(out, g.name)
		}
	}
	return out
}
