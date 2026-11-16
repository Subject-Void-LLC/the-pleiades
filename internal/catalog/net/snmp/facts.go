// Package snmp: "net.snmp.facts", what a device's SNMP agent says about it:
// its system group, its engine and its interfaces, recorded as stats always
// and as facts only when SNMPv3 authenticated the agent's answers.
package snmp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// factsFQCN is the method's name.
const factsFQCN = "net.snmp.facts"

// The facts net.snmp.facts records.
const (
	factVersion        = "snmp.version"
	factSecurityLevel  = "snmp.security_level"
	factSysDescr       = "snmp.sys_descr"
	factSysObjectID    = "snmp.sys_object_id"
	factSysName        = "snmp.sys_name"
	factSysContact     = "snmp.sys_contact"
	factSysLocation    = "snmp.sys_location"
	factSysServices    = "snmp.sys_services"
	factSysUpTime      = "snmp.sys_uptime"
	factEngineID       = "snmp.engine_id"
	factEngineBoots    = "snmp.engine_boots"
	factInterfaceCount = "snmp.interface_count"
	factInterfaces     = "snmp.interfaces"
)

// factKeys are every fact a read may record, in the order they are recorded.
var factKeys = []string{factVersion, factSecurityLevel, factSysDescr, factSysObjectID, factSysName, factSysContact, factSysLocation,
	factSysServices, factSysUpTime, factEngineID, factEngineBoots, factInterfaceCount, factInterfaces}

// The parameter bounding the interface table.
const paramMaxInterfaces = "max_interfaces"

// defaultMaxInterfaces is how many interfaces are read unless
// max_interfaces says otherwise.
const defaultMaxInterfaces = 1000

// maxListFactBytes is the most snmp.interfaces may take as JSON and still be
// recorded, under the fact store's 64 KiB value bound with room for its own
// encoding (pkg/yangfacts.MaxListFactBytes' bound).
const maxListFactBytes = 60 << 10

func init() {
	doc := factsDoc()
	collection.MustRegister(collection.Descriptor{
		Name: factsFQCN,
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
			NoExistsReason:     collection.NoExistsDevice,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, factKeys, factSysUpTime, factEngineBoots, factInterfaces),
		},
		Invoke: Facts,
		Check:  Facts,
	})
}

// factsDoc is net.snmp.facts' documentation, word for word its catalog
// entry's.
func factsDoc() collection.Doc {
	const when = "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication"
	return collection.Doc{
		Summary: "Records what a device's SNMP agent says about it: its system group, engine and interfaces.",
		Description: "Reads the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use (SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c with snmp_allow_cleartext_community, which is warned about): the MIB-II system group in one GET, the agent's engine ID (the one SNMPv3 discovered, or snmpEngineID.0 for a community) and boots, and the interface table, ifTable's index, description, type, MTU, speed, address and statuses and ifXTable's name, alias and high speed, walked column by column up to max_interfaces rows. " +
			"It records them, changing nothing, as stats always and as facts only when the session authenticated the agent's answers (SNMPv3 at authNoPriv or authPriv): a community, or a noAuthNoPriv user, proves nothing about which agent answered, so what it says is not kept, and the run says so. A group the agent does not answer is left out, never recorded empty, and an agent that answers no system object at all fails the task. snmp.interfaces is left out, with a warning, when it does not fit in 60 KiB; snmp.interface_count is recorded whenever the table was read. Every text value is masked of the device's credential. Reports no change, and a check runs it for real.",
		Params: []collection.Param{
			{Name: paramMaxInterfaces, Type: "int", Default: "1000", Description: "The most interfaces read, from 1 to 10000; a table with more is recorded as its first rows, with a warning."},
		},
		Returns: []collection.ReturnField{
			{Name: factVersion, Type: "string", Returned: "always, as a stat; as a fact only over SNMPv3 with authentication", Description: "The SNMP version the agent was read in: 3, 2c or 1."},
			{Name: factSecurityLevel, Type: "string", Returned: "always, as a stat; as a fact only over SNMPv3 with authentication", Description: "The SNMPv3 security level the session ran at (authPriv, authNoPriv or noAuthNoPriv), or community for SNMPv1 and SNMPv2c."},
			{Name: factSysDescr, Type: "string", Returned: when, Description: "sysDescr.0: the agent's description of the device, usually its hardware and software."},
			{Name: factSysObjectID, Type: "string", Returned: when, Description: "sysObjectID.0, dotted: the vendor's identifier of the device's kind, under its enterprise number (1.3.6.1.4.1.9 is Cisco's, 1.3.6.1.4.1.8072 net-snmp's)."},
			{Name: factSysName, Type: "string", Returned: when, Description: "sysName.0: the name the device's administrator gave it."},
			{Name: factSysContact, Type: "string", Returned: when, Description: "sysContact.0: who to contact about the device."},
			{Name: factSysLocation, Type: "string", Returned: when, Description: "sysLocation.0: where the device is."},
			{Name: factSysServices, Type: "int", Returned: when, Description: "sysServices.0: the layers the device serves, as RFC 3418's sum of each layer's value (72 for a host offering applications, 78 for one that also bridges and routes)."},
			{Name: factSysUpTime, Type: "int", Returned: when, Description: "sysUpTime.0, in hundredths of a second since the agent's network management part started: it changes on every read, and goes back to 0 on a restart."},
			{Name: factEngineID, Type: "string", Returned: "when the agent has one it says, as a stat; as a fact only over SNMPv3 with authentication", Description: "The agent's SNMP engine ID as lowercase hex: the one SNMPv3 discovered for the session, or snmpEngineID.0 for a community. An engine ID names one agent, so a new one at the same address is another agent, or the same one reinstalled."},
			{Name: factEngineBoots, Type: "int", Returned: when, Description: "snmpEngineBoots.0: how many times the agent's engine has started since its engine ID was set."},
			{Name: factInterfaceCount, Type: "int", Returned: "when the interface table was read, as a stat; as a fact only over SNMPv3 with authentication", Description: "How many interfaces the table holds, as read (at most max_interfaces)."},
			{Name: factInterfaces, Type: "list of dict", Returned: "when the interface table was read and fits in 60 KiB, as a stat; as a fact only over SNMPv3 with authentication", Description: "Each interface, by ifIndex order: index, descr, name, alias, type (IANAifType's number), mtu, speed (bits per second, from ifSpeed, or ifHighSpeed times a million when ifSpeed is at its 32-bit ceiling), phys_address (hex), admin_status and oper_status (up, down, testing and RFC 2863's others, or the number for one it does not name), each left out when the agent does not answer it."},
		},
		Examples: []collection.Example{
			{Name: "Record what a device's agent says", RunbookYAML: "- name: Read the agent's system group and interfaces\n  net.snmp.facts:\n  register: agent\n"},
		},
		SeeAlso: []string{"net.snmp.get", "wait.snmp"},
	}
}

// Facts implements the "net.snmp.facts" collection method.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	limit, set, err := sdk.IntParam(params, paramMaxInterfaces)
	switch {
	case err != nil:
		return collection.Result{}, fmt.Errorf("%s: %w", factsFQCN, err)
	case !set:
		limit = defaultMaxInterfaces
	case limit < 1 || limit > snmp.MaxWalkResults:
		return collection.Result{}, fmt.Errorf("%s: %s must be from 1 to %d, not %d", factsFQCN, paramMaxInterfaces, snmp.MaxWalkResults, limit)
	}
	s, err := open(rc, device, factsFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()
	values, err := s.system(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", factsFQCN, device.Name(), err)
	}
	s.engine(ctx, values)
	var warnings []string
	ifaces, truncated, err := s.interfaces(ctx, limit)
	switch {
	case err != nil:
		warnings = append(warnings, "the device's interface table was not read: "+err.Error())
	default:
		values[factInterfaceCount] = len(ifaces)
		if raw, merr := json.Marshal(ifaces); merr == nil && len(raw) <= maxListFactBytes {
			values[factInterfaces] = ifaces
		} else {
			warnings = append(warnings, fmt.Sprintf("the device's %d interfaces do not fit in 60 KiB; %s is left out", len(ifaces), factInterfaces))
		}
		if truncated {
			warnings = append(warnings, fmt.Sprintf("the device has more than %d interfaces (%s); the first %d are recorded", limit, paramMaxInterfaces, limit))
		}
	}
	authenticated := s.client.Authenticated()
	for _, key := range factKeys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", factsFQCN, err)
		}
		if authenticated {
			if err := rc.EmitFact(key, value); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", factsFQCN, err)
			}
		}
	}
	if !authenticated {
		warnings = append(warnings, "the device was reached without SNMPv3 authentication, where nothing proves which agent answered, so nothing this read found is recorded as a fact")
	}
	if err := s.warn(rc, warnings...); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", factsFQCN, err)
	}
	return collection.Result{Changed: false}, nil
}

// systemOIDs are the system group's scalars, each with its fact.
var systemOIDs = []struct{ oid, fact string }{
	{snmp.OIDSysDescr, factSysDescr}, {snmp.OIDSysObjectID, factSysObjectID}, {snmp.OIDSysUpTime, factSysUpTime},
	{snmp.OIDSysContact, factSysContact}, {snmp.OIDSysName, factSysName}, {snmp.OIDSysLocation, factSysLocation},
	{snmp.OIDSysServices, factSysServices},
}

// system reads the system group in one GET, or for an SNMPv1 agent that
// refuses it whole (noSuchName for one object) one object at a time, and
// returns the facts it gives with the version and level. An agent holding no
// system object for this credential is refused: its answer proves nothing.
func (s *session) system(ctx context.Context) (map[string]any, error) {
	oids := make([]string, len(systemOIDs))
	for i, o := range systemOIDs {
		oids[i] = o.oid
	}
	got, err := s.client.Get(ctx, oids)
	var agent *snmp.AgentError
	if errors.As(err, &agent) && agent.Status == "noSuchName" {
		got, err = nil, nil
		for _, oid := range oids {
			one, oerr := s.client.Get(ctx, []string{oid})
			if oerr == nil {
				got = append(got, one...)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	level := string(s.client.Level())
	if level == "" {
		level = "community"
	}
	values := map[string]any{factVersion: string(s.client.Version()), factSecurityLevel: level}
	answered := false
	for _, v := range got {
		for _, o := range systemOIDs {
			if v.OID != o.oid || v.Type.Absent() {
				continue
			}
			answered = true
			values[o.fact] = s.value(v)
		}
	}
	if !answered {
		return nil, errors.New("the agent answered, and held no system group object for this credential: its view grants nothing to read")
	}
	return values, nil
}

// value is v as a fact holds it: a number as a number, anything else as its
// masked text.
func (s *session) value(v snmp.Varbind) any {
	switch v.Type {
	case snmp.TypeInteger, snmp.TypeGauge32, snmp.TypeTimeTicks, snmp.TypeCounter32, snmp.TypeCounter64:
		if n, err := strconv.ParseInt(v.Text, 10, 64); err == nil {
			return n
		}
	}
	return snmp.Mask(v, s.masked).Text
}

// engine adds the agent's engine ID and boots to values, leaving out what it
// does not answer.
func (s *session) engine(ctx context.Context, values map[string]any) {
	if id := s.client.EngineID(); id != "" {
		values[factEngineID] = id
	}
	got, err := s.client.Get(ctx, []string{snmp.OIDEngineID, snmp.OIDEngineBoots})
	if err != nil {
		// An SNMPv1 agent refuses the pair whole when it lacks either.
		return
	}
	for _, v := range got {
		switch {
		case v.OID == snmp.OIDEngineID && v.Type == snmp.TypeOctetString && len(v.Bytes) > 0 && values[factEngineID] == nil:
			values[factEngineID] = fmt.Sprintf("%x", v.Bytes)
		case v.OID == snmp.OIDEngineBoots && v.Type == snmp.TypeInteger:
			values[factEngineBoots] = s.value(v)
		}
	}
}
