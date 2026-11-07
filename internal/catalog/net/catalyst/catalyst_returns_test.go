// Phase 131's R3 and R5 for the net.catalyst.* methods: every fact a
// method emits is a declared return, and device_facts returns the whole
// device record the controller answers.
package catalyst_test

import (
	"reflect"
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
		list, _ := rc.facts["catalyst.devices"].([]map[string]any)
		if len(list) != 1 {
			t.Fatalf("devices = %#v", rc.facts["catalyst.devices"])
		}
		for k, want := range map[string]string{"mac_address": "00:11:22:33:44:55", "type": "Cisco Catalyst 9300 Switch", "up_time": "12 days, 3:04:05.00", "description": "Cisco IOS Software [Cupertino]"} {
			if list[0][k] != want {
				t.Errorf("devices[0].%s = %v, want %q", k, list[0][k], want)
			}
		}
	}
}

// TestCatalystFacts_AreInTheCatalystFamily holds every net.catalyst.* method's
// fact declarations to their keys, kinds and types, written out rather than
// read from the package's constants, so a key that loses its catalyst. family
// fails here. Each key is also the stat a register reads, so it is a declared
// return as well.
func TestCatalystFacts_AreInTheCatalystFamily(t *testing.T) {
	want := map[string]map[string][2]string{
		"net.catalyst.device_facts": {
			"catalyst.devices":      {"volatile", "list"},
			"catalyst.device_count": {"stable", "integer"},
		},
		"net.catalyst.site_facts": {
			"catalyst.sites":      {"stable", "list"},
			"catalyst.site_count": {"stable", "integer"},
		},
		"net.catalyst.tag_facts": {
			"catalyst.tags":          {"stable", "list"},
			"catalyst.operator_tags": {"stable", "list"},
		},
		"net.catalyst.reachability": {
			"catalyst.reachability":       {"stable", "list"},
			"catalyst.unreachable":        {"stable", "list"},
			"catalyst.reachable_count":    {"stable", "integer"},
			"catalyst.managed_count":      {"stable", "integer"},
			"catalyst.total_device_count": {"stable", "integer"},
		},
	}
	for fqcn, keys := range want {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		got := map[string][2]string{}
		for _, f := range d.Manifest.Facts {
			got[f.Key] = [2]string{string(f.Kind), string(f.Type)}
		}
		if !reflect.DeepEqual(got, keys) {
			t.Errorf("%s facts =\n%v\nwant\n%v", fqcn, got, keys)
		}
		returns := declaredReturns(t, fqcn)
		for key := range keys {
			if !returns[key] {
				t.Errorf("%s declares fact %s with no return of that name", fqcn, key)
			}
		}
	}
}
