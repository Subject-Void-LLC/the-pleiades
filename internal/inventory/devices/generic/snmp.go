// generic_snmp: a device reached by SNMP and nothing else this platform
// knows of: a PDU, a console server, a satellite modem, an old switch.
package generic

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// HostProperty is the address a generic_snmp device's agent is reached at,
// the same key every host-addressed type reads.
const HostProperty = "host"

// SNMP is generic_snmp: a device whose agent this platform reads and writes
// over SNMP. Its version, port, opt-ins and agent quirks are its record's
// (pkg/snmp.Parse): SNMPv3 unless the record sets snmp_version 2c or 1 with
// snmp_allow_cleartext_community, and the old USM algorithms only with
// snmp_allow_legacy_algorithms.
//
// SNMPCapable itself is discovered: onboarding reads the agent's system group
// with the device's own SNMP credential, and only an answer grants it.
type SNMP struct {
	*record.Base
	host     string
	settings snmp.Settings
	// stale says why this device's discovery grants nothing, or is empty.
	stale string
}

// NewSNMP builds a generic_snmp device from rec, refusing a record with no
// host, a host that is not one name or address, and SNMP settings pkg/snmp
// refuses (a cleartext version without its opt-in among them).
func NewSNMP(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	host, _ := props.String(HostProperty)
	if host == "" || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' }) {
		return nil, fmt.Errorf("%s %s: property %s must be the agent's host name or address", TypeSNMP, rec.Name, HostProperty)
	}
	settings, err := snmp.Parse(props)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeSNMP, rec.Name, err)
	}
	caps, stale, err := record.Declared(TypeSNMP, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &SNMP{Base: record.NewBase(rec, caps), host: host, settings: settings, stale: stale}, nil
}

// DiscoveryBinding implements inventory.DiscoveryBinder: the digest a
// discovery must carry to hold for this record as it is now.
func (s *SNMP) DiscoveryBinding() string {
	return Binding(TypeSNMP, s.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (s *SNMP) StaleDiscovery() string {
	return s.stale
}

// HasCapability checks the declared set AND the structural assertion.
func (s *SNMP) HasCapability(name capability.Name) bool {
	return s.Declares(name) && capability.Implements(s, name)
}

// SNMPHost returns the agent's address.
func (s *SNMP) SNMPHost() string {
	return s.host
}

// SNMPPort returns the agent's UDP port.
func (s *SNMP) SNMPPort() int {
	return s.settings.Port
}

// SNMPSettings implements snmp.Configured: the record's validated settings.
func (s *SNMP) SNMPSettings() snmp.Settings {
	return s.settings
}

// IPAddress returns the agent's address.
func (s *SNMP) IPAddress() string {
	return s.host
}
