// The properties a device type's accessors read, which is all of a device
// that travels to a Runner.
package record

import (
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// dispatchProperties holds, per device type, the property keys its
// accessors and constructor read. The Controller puts exactly these on a
// dispatch (plus the discovered property onboarding writes), so a Runner
// rebuilds the real type and every accessor answers there as it does on
// the Controller, while no other property (an operator may keep anything
// in one) ever reaches the message stream.
//
// internal/archtest holds each package's declared keys equal to the keys
// its code reads, and refuses any key that names a secret.
var dispatchProperties = registry.New[[]string]()

// RegisterDispatchProperties declares deviceType's accessor property keys.
// Each device package calls it from init(), beside RegisterType; an empty
// list is a declaration too (a type whose accessors read nothing). It
// panics on a duplicate, as RegisterType does.
func RegisterDispatchProperties(deviceType string, keys ...string) {
	dispatchProperties.MustRegister(deviceType, slices.Clone(keys))
}

// DispatchProperties returns deviceType's declared keys, and whether it
// declared any.
func DispatchProperties(deviceType string) ([]string, bool) {
	keys, ok := dispatchProperties.Get(deviceType)
	return slices.Clone(keys), ok
}

// Dispatched returns item's type and the properties a Runner needs to
// rebuild it as that type: the keys the type declares its accessors read
// and its discovery, and nothing else of its record. An item that reports
// no type, or whose type declares nothing, yields no properties, and the
// Runner then falls back to a device built from its address alone.
func Dispatched(item inventory.InventoryItem) (string, map[string]any) {
	typed, ok := item.(interface{ DeviceType() string })
	if !ok {
		return "", nil
	}
	deviceType := typed.DeviceType()
	keys, ok := DispatchProperties(deviceType)
	if !ok {
		return deviceType, nil
	}
	raw := item.Properties().Raw()
	props := make(map[string]any, len(keys)+1)
	for _, k := range append(keys, inventory.DiscoveredProperty) {
		if v, present := raw[k]; present {
			props[k] = v
		}
	}
	return deviceType, props
}
