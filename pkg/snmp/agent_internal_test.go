// Package snmp: what the client tests share about the in-process agent
// (pkg/snmp/snmptest): the settings of a device reached at it, and a table
// it walks.
package snmp

import (
	"strconv"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp/snmptest"
)

// testCommunity is the test agent's throwaway community.
const testCommunity = "fake-agent-community"

// v2cAt is the settings of a device reached over SNMPv2c at a, quick to time
// out.
func v2cAt(a *snmptest.Agent) Settings {
	s := Defaults()
	s.Port, s.Version, s.AllowCleartextCommunity = a.Port(), Version2c, true
	s.Timeout, s.Retries = 200*time.Millisecond, 0
	return s
}

// table is a sorted subtree the test agent walks: n bindings under root,
// root.1 to root.n, each an octet string.
type table struct {
	root string
	n    int
}

// after answers a GETNEXT or GETBULK from oid with up to count bindings
// after it, and endOfMibView past the last.
func (tb table) after(oid string, count int) []gosnmp.SnmpPDU {
	next := 1
	if Under(oid, tb.root) && oid != tb.root {
		i, _ := strconv.Atoi(oid[len(tb.root)+1:])
		next = i + 1
	} else if compareOIDs(oid, tb.root) > 0 {
		next = tb.n + 1
	}
	var out []gosnmp.SnmpPDU
	for ; count > 0; count-- {
		if next > tb.n {
			return append(out, gosnmp.SnmpPDU{Name: "." + tb.root + ".999999", Type: gosnmp.EndOfMibView})
		}
		out = append(out, gosnmp.SnmpPDU{Name: "." + tb.root + "." + strconv.Itoa(next), Type: gosnmp.OctetString, Value: []byte("row " + strconv.Itoa(next))})
		next++
	}
	return out
}
