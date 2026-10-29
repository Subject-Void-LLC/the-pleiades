// Phase 131's R3 and R5 for the net.catalyst.* methods: every fact a
// method emits is a declared return, and device_facts returns the whole
// device record the controller answers.
package catalyst_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// declaredReturns is the Returns names fqcn declares.
func declaredReturns(t *testing.T, fqcn string) map[string]bool {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	out := map[string]bool{}
	for _, r := range d.Manifest.Doc.Returns {
		out[r.Name] = true
	}
	return out
}

// TestDeviceFacts_ReturnsTheWholeRecordAndDeclaresIt: the controller's
// MAC address, type, uptime and description come back beside the rest, and
// every fact either method emits is declared.
func TestDeviceFacts_ReturnsTheWholeRecordAndDeclaresIt(t *testing.T) {
	const devices = `{"response":[{"id":"d1","hostname":"sw1","managementIpAddress":"10.0.0.1","macAddress":"00:11:22:33:44:55",
		"type":"Cisco Catalyst 9300 Switch","upTime":"12 days, 3:04:05.00","description":"Cisco IOS Software [Cupertino]",
		"reachabilityStatus":"Reachable","collectionStatus":"Managed"}]}`
	url := answeringController(t, map[string]string{"/dna/intent/api/v1/network-device": devices})

	for _, fqcn := range []string{"net.catalyst.device_facts", "net.catalyst.reachability"} {
		rc := newFakeContext(validSecrets())
		if _, err := invoke(t, fqcn, rc, newController(t, url), nil); err != nil {
			t.Fatalf("%s: %v", fqcn, err)
		}
		declared := declaredReturns(t, fqcn)
		for k := range rc.facts {
			if !declared[k] {
				t.Errorf("%s emits %s, which its Returns does not declare", fqcn, k)
			}
		}
		if fqcn != "net.catalyst.device_facts" {
			continue
		}
		list, _ := rc.facts["devices"].([]map[string]any)
		if len(list) != 1 {
			t.Fatalf("devices = %#v", rc.facts["devices"])
		}
		for k, want := range map[string]string{"mac_address": "00:11:22:33:44:55", "type": "Cisco Catalyst 9300 Switch", "up_time": "12 days, 3:04:05.00", "description": "Cisco IOS Software [Cupertino]"} {
			if list[0][k] != want {
				t.Errorf("devices[0].%s = %v, want %q", k, list[0][k], want)
			}
		}
	}
}
