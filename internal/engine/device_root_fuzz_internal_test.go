// FuzzDeviceRoot holds the device root to its allowlist whatever a device
// record holds, and holds what a template reads through it to the same: a
// secret a property holds never reaches a rendering, whatever path a
// template names.
package engine

import (
	"strings"
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

// fuzzRootCanary is a secret the fuzzed device holds where the root must
// never show it.
const fuzzRootCanary = "pleiades-fuzz-canary-7d1c4e"

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
	f.Add("community", "-----BEGIN PRIVATE KEY-----", "properties.site")
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
		d := fuzzTypedStub{&inventorytest.Stub{
			StubID: "d-1", StubName: "d1", StubState: inventory.StateActive,
			Props: map[string]inventory.PropertyValue{
				"site": site, "port": 22, "snmp_password": fuzzRootCanary, key: fuzzRootCanary,
			},
		}}
		root := deviceRoot(d)
		props, _ := root["properties"].(map[string]any)
		for k, v := range props {
			if !declared[k] || redact.SecretName(k) || inventory.IsReservedProperty(k) {
				t.Fatalf("the root holds the property %q", k)
			}
			if s, ok := v.(string); ok && (redact.SecretShaped(s) || s == fuzzRootCanary) {
				t.Fatalf("the root holds %q under %q", s, k)
			}
		}

		tmpl, err := eng.Compile("{{ device." + path + " | to_json }}")
		if err != nil {
			return
		}
		out, err := tmpl.Render(map[string]any{TemplateRootDevice: root})
		if err != nil {
			return
		}
		// The canary may be in the output only where the fuzzed site or path
		// wrote it there themselves.
		if strings.Contains(out, fuzzRootCanary) && !strings.Contains(site, fuzzRootCanary) && !strings.Contains(path, fuzzRootCanary) {
			t.Fatalf("{{ device.%s }} rendered a secret the device holds: %s", path, out)
		}
	})
}
