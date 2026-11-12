// FuzzDeviceRoot holds the device root to its allowlist whatever a device
// record holds, and holds what a template reads through it to the same: the
// same device holding two different secrets gives one root and one
// rendering, whatever path a template names, so no secret reaches either
// (a differential, as FAILURE_PATTERNS 236 settled on, rather than a search
// for the secret in output that echoes fuzzed input).
package engine

import (
	"reflect"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// fuzzRootType is the device type the fuzzed devices report; it declares a
// plain key, a number, and a key naming a secret.
const fuzzRootType = "device_root_fuzz"

var registerFuzzRootType = sync.OnceFunc(func() {
	record.RegisterDispatchProperties(fuzzRootType, "site", "port", "snmp_password")
})

// fuzzTypedStub is a stub that reports fuzzRootType, as record.Base does.
type fuzzTypedStub struct{ *inventorytest.Stub }

// DeviceType reports the fuzzed type.
func (fuzzTypedStub) DeviceType() string { return fuzzRootType }

func FuzzDeviceRoot(f *testing.F) {
	registerFuzzRootType()
	f.Add("location", "hq", "properties.site")
	f.Add("note", "anything", "properties.note")
	f.Add("api_token", "x", "properties.snmp_password")
	f.Add("community", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----", "properties.site")
	f.Add("community", "AKIAIOSFODNN7EXAMPLE", "properties.site")
	f.Add("discovered", "{}", "properties")
	f.Add("x", "y", "tags")
	f.Add("x", "y", "properties['snmp_password']")
	eng := render.New()
	declared := map[string]bool{"site": true, "port": true, "snmp_password": true}

	f.Fuzz(func(t *testing.T, key, site, path string) {
		// key is a property the type does not declare: one it declares may
		// hold any text that is not shaped like a secret, by design.
		if declared[key] {
			return
		}
		device := func(secret string) inventory.InventoryItem {
			return fuzzTypedStub{&inventorytest.Stub{
				StubID: "d-1", StubName: "d1", StubState: inventory.StateActive,
				Props: map[string]inventory.PropertyValue{
					"site": site, "port": 22, "snmp_password": secret, key: secret,
				},
			}}
		}
		root, other := deviceRoot(device("first-secret-a1")), deviceRoot(device("second-secret-b2"))
		if !reflect.DeepEqual(root, other) {
			t.Fatalf("the root changes with a secret the device holds: %v and %v", root, other)
		}
		props, _ := root["properties"].(map[string]any)
		for k, v := range props {
			if !declared[k] || redact.SecretName(k) || inventory.IsReservedProperty(k) {
				t.Fatalf("the root holds the property %q", k)
			}
			if s, ok := v.(string); ok && redact.SecretShaped(s) {
				t.Fatalf("the root holds a secret-shaped %q under %q", s, k)
			}
		}

		tmpl, err := eng.Compile("{{ device." + path + " | to_json }}")
		if err != nil {
			return
		}
		out, err := tmpl.Render(map[string]any{TemplateRootDevice: root})
		outOther, errOther := tmpl.Render(map[string]any{TemplateRootDevice: other})
		if out != outOther || (err == nil) != (errOther == nil) {
			t.Fatalf("{{ device.%s }} renders differently as the device's secret changes: %q and %q", path, out, outOther)
		}
	})
}
