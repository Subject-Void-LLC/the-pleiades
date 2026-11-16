// Package snmp: a client built from an SNMP-capable device's record and the
// SNMP family of its credential, for the net.snmp methods, wait.snmp and
// onboarding alike, so none of them can disagree about how a device is
// reached.
package snmp

import (
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ClientFor builds the client of device, an SNMPCapable device named name:
// its agent's address and port, its record's SNMP settings (For), and the
// SNMP family of secrets (the flattened credential a task or a probe is
// handed). It returns what the settings and the credential make a run say
// (Settings.Warnings): a community in the clear, a legacy algorithm, a level
// below authPriv.
func ClientFor(device any, name string, secrets map[string]string) (*Client, []string, error) {
	dev, ok := device.(capability.SNMPCapable)
	if !ok {
		return nil, nil, errors.New("the device has no SNMP agent this platform reaches (it does not implement " + string(capability.NameSNMP) + ")")
	}
	s := For(device)
	if port := dev.SNMPPort(); port != 0 {
		s.Port = port
	}
	cred := wire.SNMPOf(secrets)
	c, err := Dial(dev.SNMPHost(), s, cred)
	if err != nil {
		return nil, nil, err
	}
	return c, s.Warnings(name, cred), nil
}
