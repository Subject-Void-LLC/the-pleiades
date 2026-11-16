// The SNMP Collection methods (Phase 97): net.snmp.facts, net.snmp.get,
// net.snmp.set and wait.snmp, each reaching its device's agent through
// pkg/snmp with the SNMP family of the device's own credential.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// snmpCollections are the SNMP methods, each Doc word for word its
// registered method's (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
var snmpCollections = []collectionscaffold.Config{
	// net.snmp.facts records a device's system group, engine and interfaces.
	{
		Name:          "net.snmp.facts",
		Capabilities:  []capability.Name{capability.NameSNMP},
		Transports:    []string{"snmp"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Records what a device's SNMP agent says about it: its system group, engine and interfaces.",
			Description: "Reads the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use (SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c with snmp_allow_cleartext_community, which is warned about): the MIB-II system group in one GET, the agent's engine ID (the one SNMPv3 discovered, or snmpEngineID.0 for a community) and boots, and the interface table, ifTable's index, description, type, MTU, speed, address and statuses and ifXTable's name, alias and high speed, walked column by column up to max_interfaces rows. " +
				"It records them, changing nothing, as stats always and as facts only when the session authenticated the agent's answers (SNMPv3 at authNoPriv or authPriv): a community, or a noAuthNoPriv user, proves nothing about which agent answered, so what it says is not kept, and the run says so. A group the agent does not answer is left out, never recorded empty, and an agent that answers no system object at all fails the task. snmp.interfaces is left out, with a warning, when it does not fit in 60 KiB; snmp.interface_count is recorded whenever the table was read. Every text value is masked of the device's credential. Reports no change, and a check runs it for real.",
			Params: []collection.Param{
				{Name: "max_interfaces", Type: "int", Default: "1000", Description: "The most interfaces read, from 1 to 10000; a table with more is recorded as its first rows, with a warning."},
			},
			Returns: []collection.ReturnField{
				{Name: "snmp.version", Type: "string", Returned: "always, as a stat; as a fact only over SNMPv3 with authentication", Description: "The SNMP version the agent was read in: 3, 2c or 1."},
				{Name: "snmp.security_level", Type: "string", Returned: "always, as a stat; as a fact only over SNMPv3 with authentication", Description: "The SNMPv3 security level the session ran at (authPriv, authNoPriv or noAuthNoPriv), or community for SNMPv1 and SNMPv2c."},
				{Name: "snmp.sys_descr", Type: "string", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysDescr.0: the agent's description of the device, usually its hardware and software."},
				{Name: "snmp.sys_object_id", Type: "string", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysObjectID.0, dotted: the vendor's identifier of the device's kind, under its enterprise number (1.3.6.1.4.1.9 is Cisco's, 1.3.6.1.4.1.8072 net-snmp's)."},
				{Name: "snmp.sys_name", Type: "string", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysName.0: the name the device's administrator gave it."},
				{Name: "snmp.sys_contact", Type: "string", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysContact.0: who to contact about the device."},
				{Name: "snmp.sys_location", Type: "string", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysLocation.0: where the device is."},
				{Name: "snmp.sys_services", Type: "int", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysServices.0: the layers the device serves, as RFC 3418's sum of each layer's value (72 for a host offering applications, 78 for one that also bridges and routes)."},
				{Name: "snmp.sys_uptime", Type: "int", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "sysUpTime.0, in hundredths of a second since the agent's network management part started: it changes on every read, and goes back to 0 on a restart."},
				{Name: "snmp.engine_id", Type: "string", Returned: "when the agent has one it says, as a stat; as a fact only over SNMPv3 with authentication", Description: "The agent's SNMP engine ID as lowercase hex: the one SNMPv3 discovered for the session, or snmpEngineID.0 for a community. An engine ID names one agent, so a new one at the same address is another agent, or the same one reinstalled."},
				{Name: "snmp.engine_boots", Type: "int", Returned: "when the agent answers it, as a stat; as a fact only over SNMPv3 with authentication", Description: "snmpEngineBoots.0: how many times the agent's engine has started since its engine ID was set."},
				{Name: "snmp.interface_count", Type: "int", Returned: "when the interface table was read, as a stat; as a fact only over SNMPv3 with authentication", Description: "How many interfaces the table holds, as read (at most max_interfaces)."},
				{Name: "snmp.interfaces", Type: "list of dict", Returned: "when the interface table was read and fits in 60 KiB, as a stat; as a fact only over SNMPv3 with authentication", Description: "Each interface, by ifIndex order: index, descr, name, alias, type (IANAifType's number), mtu, speed (bits per second, from ifSpeed, or ifHighSpeed times a million when ifSpeed is at its 32-bit ceiling), phys_address (hex), admin_status and oper_status (up, down, testing and RFC 2863's others, or the number for one it does not name), each left out when the agent does not answer it."},
			},
			Examples: []collection.Example{
				{Name: "Record what a device's agent says", RunbookYAML: "- name: Read the agent's system group and interfaces\n  net.snmp.facts:\n  register: agent\n"},
			},
			SeeAlso: []string{"net.snmp.get", "wait.snmp"},
		},
	},
	// net.snmp.get reads OIDs, in one GET or each walked.
	{
		Name:          "net.snmp.get",
		Capabilities:  []capability.Name{capability.NameSNMP},
		Transports:    []string{"snmp"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Reads OIDs from a device's SNMP agent, in one GET or each walked, changing nothing.",
			Description: "Reads the OIDs oids names from the device's SNMP agent with the SNMP family of its own credential, through the client onboarding and wait.snmp use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), every one of which is warned about. Without walk the OIDs are read in one GET, at most 60 of them, and an object the agent does not hold is answered as a value of type no-such-object or no-such-instance; an SNMPv1 agent answers that noSuchName instead, which fails the task naming the OID. With walk each OID is the root of a subtree read in order, with GETBULK (unless the device's record sets snmp_no_bulk, or it speaks SNMPv1) or GETNEXT, until the subtree ends or max_results bindings are read, and an agent answering an OID not after the one asked from stops the walk rather than loop. " +
				"An OID is numeric (1.3.6.1.2.1.1.5.0) or a name from a small curated table with its instance (sysName.0, ifDescr.2, SNMPv2-MIB::sysName.0): the MIB-II system and interfaces groups, IF-MIB's ifXTable and the SNMP engine group; anything else is refused before anything is sent, and is written numerically. " +
				"The agent's own security configuration under snmpModules (1.3.6.1.6.3: VACM, USM's user table, SNMP-COMMUNITY-MIB, the target and notification tables), whose table indexes spell the names the agent is configured with, is never read: an OID there, or a walk rooted where it would read nothing else, is refused before anything is sent, and a walk whose subtree crosses it leaves every binding there out, OID and value, and says how many; the engine group, the message and USM counters and SNMPv2-MIB's own objects are read as any other. " +
				"A task reads at most 10000 bindings and 8 MiB of values over all its walks together; a task that stops there says so with truncated. Every value is masked of the device's credential. Reports no change, and a check runs it for real.",
			Params: []collection.Param{
				{Name: "oids", Type: "list", Required: true, Description: "The OIDs to read, or with walk the roots of the subtrees to walk: numeric, or a curated name with its instance (sysName.0). From 1 to 60 of them."},
				{Name: "walk", Type: "bool", Default: "false", Description: "Walk each OID's subtree instead of reading the OID itself."},
				{Name: "max_results", Type: "int", Default: "1000", Description: "With walk, the most bindings read for each OID, from 1 to 10000, within the task's own bound of 10000 bindings and 8 MiB over every OID; a walk that stops at either says so with truncated. Refused without walk."},
			},
			Returns: []collection.ReturnField{
				{Name: "values", Type: "list of dict", Returned: "always", Description: "Each binding read, in order: oid (numeric), type (integer, octet-string, object-identifier, ip-address, counter32, gauge32, timeticks, counter64, opaque, null, no-such-object, no-such-instance or end-of-mib-view) and value as text (a number in decimal, an OID dotted, an octet string as itself, or as 0x and hex when it is not printable, with hex true)."},
				{Name: "truncated", Type: "bool", Returned: "with walk", Description: "Whether a walk stopped at max_results, or the task at its bound over every walk, before a subtree ended, so values may hold only its first part."},
			},
			Examples: []collection.Example{
				{Name: "Read a device's name and uptime", RunbookYAML: "- name: Read the agent's name and uptime\n  net.snmp.get:\n    oids:\n      - sysName.0\n      - sysUpTime.0\n  register: system\n"},
				{Name: "Walk the interface descriptions", RunbookYAML: "- name: Walk ifDescr\n  net.snmp.get:\n    oids:\n      - ifDescr\n    walk: true\n    max_results: 500\n"},
			},
			SeeAlso: []string{"net.snmp.facts", "net.snmp.set", "wait.snmp"},
		},
	},
	// net.snmp.set writes one object, read before and after, with an exact undo.
	{
		Name:          "net.snmp.set",
		Capabilities:  []capability.Name{capability.NameSNMP},
		Transports:    []string{"snmp"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Writes one object on a device's SNMP agent, reading it before and after, its undo writing the value read before back.",
			Description: "Writes value, of type, to the object oid names on the device's SNMP agent in one SET, with the SNMP family of the device's own credential: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about, as a write in the clear with a community is. " +
				"The object is read first. One the agent does not hold is refused, since creating a row is not this method's (its undo would not be exact), and so is one holding another type than type. One that already holds value is left alone: nothing is sent and the run reports no change, and a check reports what a run would change and sends no SET. " +
				"After the SET the object is read again, and a value other than the one written fails the task: an agent can accept a write and drop it (IOS XE 17.15.1 accepted SNMPv3 users it never created, measured on 2026-11-15), so the answer to the SET is not taken as the change. " +
				"A run that changes the object records its undo, net.snmp.set of the same OID with the type and value read before, before the SET is sent, so a SET whose answer is lost still leaves it; it is exact for an object that keeps what it is written, and an object that does not (a RowStatus a destroy(6) removes, a TestAndIncr lock, a trigger) fails the task at the read back. Every object under snmpModules (1.3.6.1.6.3), the agent's own users, keys, views and communities, is refused before anything is sent: their values would ride the undo and the run's journal. A secret held elsewhere (a vendor MIB's trap receiver community, a RADIUS or TACACS+ key) is written with secret: true. An OID is numeric or a curated name with its instance (sysContact.0), as net.snmp.get takes it. The values recorded are masked of the device's credential.",
			Params: []collection.Param{
				{Name: "oid", Type: "string", Required: true, Description: "The object to write, one instance: numeric (1.3.6.1.2.1.1.4.0) or a curated name with its instance (sysContact.0)."},
				{Name: "type", Type: "string", Required: true, Choices: []string{"integer", "octet-string", "hex-string", "object-identifier", "ip-address", "gauge32", "timeticks"}, Description: "The value's SNMP type: integer (a signed 32-bit number), octet-string (text, written as its bytes), hex-string (an octet string given as hex, optionally after 0x, for one that is not text), object-identifier, ip-address (IPv4, dotted), gauge32 or timeticks (unsigned 32-bit numbers). Counters are read-only (RFC 2578) and are not offered. It must be the type the agent holds."},
				{Name: "value", Type: "string", Required: true, Description: "The value, as text of its type: a number in decimal, an OID dotted or by curated name, an address dotted, text, or hex. An octet string is at most 65535 bytes."},
				{Name: "secret", Type: "bool", Default: "false", Description: "The object holds a secret (a key, a community, a password): its values, read and written, are shown in the diff as (secret, not shown) and join the run's masking set, and no undo is recorded, since the value read before would be kept in the run's journal. A rollback then refuses the task unless it is left or its rollback: list says what to do."},
			},
			Returns: []collection.ReturnField{
				{Name: "diff", Type: "dict", Returned: "always", Description: "The object before and after, each its oid, type and value (masked), so a run that changed nothing shows the same value twice."},
			},
			Examples: []collection.Example{
				{Name: "Set a device's contact", RunbookYAML: "- name: Name the device's contact\n  net.snmp.set:\n    oid: sysContact.0\n    type: octet-string\n    value: noc@example.net\n"},
				{Name: "Shut an interface down", RunbookYAML: "- name: Set ifAdminStatus of interface 3 to down\n  net.snmp.set:\n    oid: ifAdminStatus.3\n    type: integer\n    value: \"2\"\n"},
			},
			SeeAlso: []string{"net.snmp.get", "net.snmp.facts"},
		},
	},
	// wait.snmp waits for a device's SNMP agent to answer its credential.
	{
		Name:          "wait.snmp",
		Capabilities:  []capability.Name{capability.NameSNMP},
		Transports:    []string{"snmp"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Waits until the target's SNMP agent answers its credential with sysUpTime.0.",
			Description: "Tries the device's SNMP agent until a GET of sysUpTime.0 is answered with the SNMP family of the device's own credential, and fails when the timeout runs out first, through the same client the net.snmp.* methods and onboarding use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about. " +
				"It is the task to put between one that starts or reconfigures a device and the first net.snmp.* task: a device answers a ping, and even SSH, before its agent runs, and wait.connection says nothing about SNMP. " +
				"Each try is a new client, so a session from an earlier try is never taken for an answer. " +
				"An SNMPv3 refusal ends the wait at once (an unknown user, a wrong authentication passphrase or protocol, a security level the user is not configured for, or a request the agent could not decrypt), since none of them changes by being asked again and a credential sent at every try can lock it out; so does a device record or credential the client refuses, such as a community for an SNMPv3 device or 3des privacy. " +
				"An agent that does not answer within connect_timeout is tried again until the timeout, and that includes a wrong community: an SNMPv1 or SNMPv2c agent drops a request whose community it does not know without a word, so it reads exactly as an agent not up yet; so is a host name that does not resolve yet. An SNMPv3 user still being configured is not waited through: the agent refuses it, and the wait ends. " +
				"A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. " +
				"The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.",
			Params: snmpWaitParams(),
			Returns: []collection.ReturnField{
				{Name: "elapsed", Type: "int", Returned: "always", Description: "How many whole seconds the task waited, including the delay."},
				{Name: "version", Type: "string", Returned: "always", Description: "The SNMP version the agent answered in: 3, 2c or 1."},
				{Name: "port", Type: "int", Returned: "always", Description: "The UDP port of the agent that answered."},
				{Name: "tries", Type: "int", Returned: "always", Description: "How many tries the task made, answered or not."},
				{Name: "failures", Type: "int", Returned: "always", Description: "How many of those tries did not answer."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The port that answered. Both halves are identical, since a wait changes nothing."},
			},
			Examples: []collection.Example{
				{Name: "Wait for a device's SNMP agent after its configuration is applied", RunbookYAML: "- name: Wait for the agent to answer its SNMPv3 user\n  wait.snmp:\n    timeout: 300\n    sleep: 5\n"},
			},
			SeeAlso: []string{"wait.connection", "net.snmp.facts", "net.snmp.get"},
		},
	},
}

// snmpWaitParams are wait.netconf's timing parameters, word for word but
// connect_timeout's and steady's, which describe an SNMP try.
func snmpWaitParams() []collection.Param {
	return []collection.Param{
		{Name: "timeout", Type: "int", Default: "600", Description: "How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0."},
		{Name: "delay", Type: "int", Default: "0", Description: "How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of."},
		{Name: "sleep", Type: "int", Default: "5", Description: "How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0."},
		{Name: "backoff", Type: "string", Default: "fixed", Choices: []string{"fixed", "exponential"}, Description: "How the gap between failed tries grows: fixed keeps it at sleep; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half."},
		{Name: "max_sleep", Type: "int", Default: "60", Description: "With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing."},
		{Name: "steady", Type: "int", Default: "1", Description: "How many tries in a row must be answered before the agent counts as ready. Must be at least 1, and the streak must fit in the timeout after the delay."},
		{Name: "steady_interval", Type: "int", Default: "10", Description: "With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing."},
		{Name: "connect_timeout", Type: "int", Default: "20", Description: "How many seconds one try may take, every retry of its GET included (the device's snmp_timeout and snmp_retries), before it counts as failed. Must be more than 0."},
	}
}
