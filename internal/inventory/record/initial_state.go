// The lifecycle state a device starts in, which depends on whether its
// type's capabilities come from onboarding.
package record

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// onboardedTypes holds the device types whose capabilities come from
// onboarding rather than from Go code alone: the generic, protocol-shaped
// types. A device of one of them starts discovered, which admits no
// execution (engine.LifecycleAdmits), and reaches active only when
// onboarding has probed it.
var onboardedTypes = registry.New[struct{}]()

// RegisterOnboardedType marks deviceType as one whose devices start
// discovered. The type's package calls it from init(), beside
// RegisterType. It panics on a duplicate, as RegisterType does.
func RegisterOnboardedType(deviceType string) {
	onboardedTypes.MustRegister(deviceType, struct{}{})
}

// IsOnboardedType reports whether deviceType's devices reach active only
// through onboarding.
func IsOnboardedType(deviceType string) bool {
	_, ok := onboardedTypes.Get(deviceType)
	return ok
}

// InitialState is the lifecycle state a device of deviceType starts in
// when nothing has recorded one: discovered for an onboarded type, active
// for every other, which is what every type started in before onboarding
// existed.
func InitialState(deviceType string) inventory.LifecycleState {
	if IsOnboardedType(deviceType) {
		return inventory.StateDiscovered
	}
	return inventory.StateActive
}
