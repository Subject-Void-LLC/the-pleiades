// SNMPCapable: a device reached by SNMP alone, with no agent of this
// platform's installed on it.
package capability

// NameSNMP is SNMPCapable's name.
const NameSNMP Name = "SNMPCapable"

// SNMPCapable is satisfied by a device whose SNMP agent this platform reads
// and writes (pkg/snmp): GET, GETNEXT, GETBULK and SET over UDP, in SNMPv3 by
// default and SNMPv1 or SNMPv2c only by the device's explicit opt-in.
//
// It has no parent and is no other capability's child, for the reason
// CatalystAPICapable's doc gives: an SNMP agent has no prompt, no shell and
// no API a method could call another way, so nesting it would let
// capability.Resolves answer yes to a question only true of a sibling. A
// device that also takes a login (a switch with SSH and SNMP) declares both.
type SNMPCapable interface {
	// SNMPHost returns the address the agent is reached at.
	SNMPHost() string
	// SNMPPort returns the agent's UDP port (161 unless the record says
	// otherwise).
	SNMPPort() int
}

func init() {
	Register(Descriptor{
		Name:   NameSNMP,
		Assert: func(item any) bool { _, ok := item.(SNMPCapable); return ok },
	})
}
