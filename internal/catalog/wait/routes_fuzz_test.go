// Fuzz test of reading a routing table: a router's answer, never trusted.
package wait

import (
	"encoding/json"
	"testing"
)

// FuzzRoutesTable: any table that decodes fingerprints and is held to a
// want without panicking, and a table equals itself.
func FuzzRoutesTable(f *testing.F) {
	f.Add([]byte(`{"10.1.0.0/24":[{"protocol":"ospf","selected":true,"nexthops":[{"ip":"10.0.0.1","active":true}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var table map[string][]route
		if json.Unmarshal(data, &table) != nil {
			return
		}
		if fingerprint(table) != fingerprint(table) {
			t.Fatal("a table fingerprints unlike itself")
		}
		w := routesWant{prefixes: []string{"10.1.0.0/24"}, nextHops: map[string][]string{"10.1.0.0/24": {"10.0.0.1"}}}
		w.satisfied(table)
	})
}
