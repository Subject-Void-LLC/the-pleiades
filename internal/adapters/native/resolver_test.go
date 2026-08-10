package native

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestSingleDeviceResolver_AlwaysReturnsTheOneDevice(t *testing.T) {
	device := newWireDevice(wire.DispatchPayload{DeviceID: "dev-1", DeviceName: "core-switch-1"})
	resolver := singleDeviceResolver{device: device}

	for _, target := range []string{"", "core-switch-1", "some-other-target", "a-tag-nobody-has"} {
		got := resolver.Resolve(target)
		if len(got) != 1 || got[0] != device {
			t.Errorf("Resolve(%q) = %v, want exactly [device], regardless of target", target, got)
		}
	}
}
