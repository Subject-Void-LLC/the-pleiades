// Tests for SNMPCapable: its assertion tells a device with an agent from one
// without, and it has no parent.
package capability_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// snmpAgent is a device with an SNMP agent.
type snmpAgent struct{}

// SNMPHost implements capability.SNMPCapable.
func (snmpAgent) SNMPHost() string { return "10.0.0.7" }

// SNMPPort implements capability.SNMPCapable.
func (snmpAgent) SNMPPort() int { return 161 }

// TestImplementsSNMP: a type with the accessors satisfies SNMPCapable and one
// without does not, and the capability is nobody's child, so declaring a CLI
// or an HTTP API never claims an agent.
func TestImplementsSNMP(t *testing.T) {
	if !capability.Implements(snmpAgent{}, capability.NameSNMP) {
		t.Error("a device with an agent does not satisfy SNMPCapable")
	}
	if capability.Implements(sshDevice{}, capability.NameSNMP) {
		t.Error("an SSH device satisfies SNMPCapable")
	}
	d, ok := capability.Lookup(capability.NameSNMP)
	if !ok || d.Parent != "" {
		t.Errorf("SNMPCapable = %+v, %v; want it registered with no parent", d, ok)
	}
}
