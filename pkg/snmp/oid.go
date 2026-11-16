// Package snmp: OIDs as a runbook writes them, numeric or by a name from a
// small curated table, and the OIDs this package reads itself.
//
// The table is not a MIB compiler and does not grow into one: it names the
// MIB-II system and interfaces groups, IF-MIB's ifXTable columns and the
// SNMP engine group, which is what net.snmp.facts reads and what a runbook
// most often names. Any other object is written numerically.
package snmp

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The OIDs this package reads by name.
const (
	OIDSystem      = "1.3.6.1.2.1.1"
	OIDSysDescr    = "1.3.6.1.2.1.1.1.0"
	OIDSysObjectID = "1.3.6.1.2.1.1.2.0"
	OIDSysUpTime   = "1.3.6.1.2.1.1.3.0"
	OIDSysContact  = "1.3.6.1.2.1.1.4.0"
	OIDSysName     = "1.3.6.1.2.1.1.5.0"
	OIDSysLocation = "1.3.6.1.2.1.1.6.0"
	OIDSysServices = "1.3.6.1.2.1.1.7.0"
	OIDInterfaces  = "1.3.6.1.2.1.2"
	OIDIfNumber    = "1.3.6.1.2.1.2.1.0"
	OIDIfTable     = "1.3.6.1.2.1.2.2.1"
	OIDIfXTable    = "1.3.6.1.2.1.31.1.1.1"
	OIDEngineID    = "1.3.6.1.6.3.10.2.1.1.0"
	OIDEngineBoots = "1.3.6.1.6.3.10.2.1.2.0"
	OIDHostRes     = "1.3.6.1.2.1.25"
	// OIDSNMPModules is snmpModules, under which the SNMP framework, USM,
	// VACM and community MIBs keep the agent's own security.
	OIDSNMPModules = "1.3.6.1.6.3"
)

// names is the curated table: an object's name to its OID. A scalar's
// instance is written after it (sysName.0), a column's index likewise
// (ifDescr.2).
var names = map[string]string{
	"sysDescr": "1.3.6.1.2.1.1.1", "sysObjectID": "1.3.6.1.2.1.1.2", "sysUpTime": "1.3.6.1.2.1.1.3",
	"sysContact": "1.3.6.1.2.1.1.4", "sysName": "1.3.6.1.2.1.1.5", "sysLocation": "1.3.6.1.2.1.1.6",
	"sysServices": "1.3.6.1.2.1.1.7", "system": OIDSystem,
	"interfaces": OIDInterfaces, "ifNumber": "1.3.6.1.2.1.2.1", "ifTable": "1.3.6.1.2.1.2.2", "ifEntry": OIDIfTable,
	"ifIndex": "1.3.6.1.2.1.2.2.1.1", "ifDescr": "1.3.6.1.2.1.2.2.1.2", "ifType": "1.3.6.1.2.1.2.2.1.3",
	"ifMtu": "1.3.6.1.2.1.2.2.1.4", "ifSpeed": "1.3.6.1.2.1.2.2.1.5", "ifPhysAddress": "1.3.6.1.2.1.2.2.1.6",
	"ifAdminStatus": "1.3.6.1.2.1.2.2.1.7", "ifOperStatus": "1.3.6.1.2.1.2.2.1.8", "ifLastChange": "1.3.6.1.2.1.2.2.1.9",
	"ifInOctets": "1.3.6.1.2.1.2.2.1.10", "ifOutOctets": "1.3.6.1.2.1.2.2.1.16",
	"ifXTable": "1.3.6.1.2.1.31.1.1", "ifXEntry": OIDIfXTable, "ifName": "1.3.6.1.2.1.31.1.1.1.1",
	"ifHCInOctets": "1.3.6.1.2.1.31.1.1.1.6", "ifHCOutOctets": "1.3.6.1.2.1.31.1.1.1.10",
	"ifHighSpeed": "1.3.6.1.2.1.31.1.1.1.15", "ifAlias": "1.3.6.1.2.1.31.1.1.1.18",
	"snmpEngineID": "1.3.6.1.6.3.10.2.1.1", "snmpEngineBoots": "1.3.6.1.6.3.10.2.1.2",
	"snmpEngineTime": "1.3.6.1.6.3.10.2.1.3", "snmpEngineMaxMessageSize": "1.3.6.1.6.3.10.2.1.4",
}

// modules are the MIB modules a name may be written under (SNMPv2-MIB::sysName.0),
// as net-snmp prints them.
var modules = map[string]bool{"SNMPv2-MIB": true, "RFC1213-MIB": true, "IF-MIB": true, "SNMP-FRAMEWORK-MIB": true}

// Names returns the curated table's names, sorted, for a reference page.
func Names() []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// maxSubIDs is the most sub-identifiers an OID holds (RFC 2578 section 3.5).
const maxSubIDs = 128

// ParseOID reads an OID as a runbook writes it, numeric with or without a
// leading dot (1.3.6.1.2.1.1.5.0), or as a name from the curated table with
// its instance (sysName.0, SNMPv2-MIB::sysName.0), and returns it numeric
// without the leading dot. It refuses anything else, a name outside the table
// included, rather than send an agent an OID nobody meant.
func ParseOID(text string) (string, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", errors.New("an OID is empty")
	}
	if module, rest, ok := strings.Cut(s, "::"); ok {
		if !modules[module] {
			return "", fmt.Errorf("OID %q names MIB module %s, which this client has no names for; write the OID numerically", text, module)
		}
		s = rest
	}
	if s == "" {
		return "", fmt.Errorf("OID %q names a MIB module and no object", text)
	}
	if first := s[0]; first != '.' && (first < '0' || first > '9') {
		name, instance, _ := strings.Cut(s, ".")
		base, known := names[name]
		if !known {
			return "", fmt.Errorf("OID %q names %s, which is not in this client's curated table (the MIB-II system and interfaces groups, IF-MIB's ifXTable and the SNMP engine group); write the OID numerically", text, name)
		}
		s = base
		if instance != "" {
			s += "." + instance
		}
	}
	s = strings.TrimPrefix(s, ".")
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > maxSubIDs {
		return "", fmt.Errorf("OID %q must have from 2 to %d numbers", text, maxSubIDs)
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || (len(part) > 1 && part[0] == '0') {
			return "", fmt.Errorf("OID %q holds %q, which is not a number from 0 to 4294967295 without leading zeros", text, part)
		}
		if i == 0 && n > 2 {
			return "", fmt.Errorf("OID %q must begin with 0, 1 or 2", text)
		}
	}
	return s, nil
}

// Under reports whether oid is root or inside its subtree, both numeric.
func Under(oid, root string) bool {
	return oid == root || strings.HasPrefix(oid, root+".")
}

// compareOIDs orders two numeric OIDs as an agent's walk does,
// lexicographically by sub-identifier: -1, 0 or 1.
func compareOIDs(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.ParseUint(as[i], 10, 64)
		y, _ := strconv.ParseUint(bs[i], 10, 64)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
