// Tests for a method's execution context: which declarations Register
// refuses, and which calls need a device.
package collection

import (
	"strings"
	"testing"
)

// TestCheckExecutionContext: each declaration that is not one of the named
// values, or contradicts itself, is refused naming why; the coherent ones
// pass, the empty context included, since it reads as target-side with a
// device required, which every method was before the field existed.
func TestCheckExecutionContext(t *testing.T) {
	needs := func(map[string]any) bool { return true }
	for _, tc := range []struct {
		name       string
		ec         ExecutionContext
		deviceCall func(map[string]any) bool
		want       string
	}{
		{name: "empty", ec: ExecutionContext{}},
		{name: "target, required", ec: ExecutionContext{Site: SiteTarget, Device: DeviceRequired}},
		{name: "controller, required", ec: ExecutionContext{Site: SiteController, Device: DeviceRequired}},
		{name: "controller, none", ec: ExecutionContext{Site: SiteController, Device: DeviceNone}},
		{name: "controller, optional", ec: ExecutionContext{Site: SiteController, Device: DeviceOptional}, deviceCall: needs},
		{name: "hybrid, required", ec: ExecutionContext{Site: SiteHybrid, Device: DeviceRequired}},
		{name: "unknown site", ec: ExecutionContext{Site: "local"}, want: "execution site"},
		{name: "unknown device", ec: ExecutionContext{Device: "sometimes"}, want: "device use"},
		{name: "target, none", ec: ExecutionContext{Site: SiteTarget, Device: DeviceNone}, want: "runs on its target"},
		{name: "empty site, optional", ec: ExecutionContext{Device: DeviceOptional}, deviceCall: needs, want: "runs on its target"},
		{name: "optional without DeviceCall", ec: ExecutionContext{Site: SiteController, Device: DeviceOptional}, want: "no DeviceCall"},
		{name: "DeviceCall without optional", ec: ExecutionContext{Site: SiteController, Device: DeviceRequired}, deviceCall: needs, want: "carries a DeviceCall"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExecutionContext(Descriptor{Name: "test.method", Manifest: Manifest{ExecutionContext: tc.ec}, DeviceCall: tc.deviceCall})
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused a coherent context: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

// TestRegister_RefusesAnIncoherentExecutionContext: Register itself runs
// the execution-context check, so a method that claims to run on its
// target with no device is refused and never becomes reachable.
func TestRegister_RefusesAnIncoherentExecutionContext(t *testing.T) {
	const name = "test.exec_incoherent"
	err := Register(Descriptor{Name: name, Manifest: Manifest{ExecutionContext: ExecutionContext{Site: SiteTarget, Device: DeviceNone}}})
	if err == nil || !strings.Contains(err.Error(), "runs on its target") {
		t.Fatalf("Register = %v, want the execution-context refusal", err)
	}
	if _, ok := Lookup(name); ok {
		t.Error("a refused method was registered anyway")
	}
}

// TestNeedsDevice: none never needs a device, optional asks DeviceCall
// with the call's params, and anything else, unstated included, does.
func TestNeedsDevice(t *testing.T) {
	isPath := func(p map[string]any) bool {
		s, _ := p["url"].(string)
		return strings.HasPrefix(s, "/")
	}
	optional := Descriptor{Manifest: Manifest{ExecutionContext: ExecutionContext{Site: SiteController, Device: DeviceOptional}}, DeviceCall: isPath}
	if !optional.NeedsDevice(map[string]any{"url": "/items"}) {
		t.Error("an optional method's device call said no for a path")
	}
	if optional.NeedsDevice(map[string]any{"url": "https://api.example.com/items"}) {
		t.Error("an optional method's device call said yes for a full URL")
	}
	none := Descriptor{Manifest: Manifest{ExecutionContext: ExecutionContext{Site: SiteController, Device: DeviceNone}}}
	if none.NeedsDevice(nil) {
		t.Error("a method using no device needs one")
	}
	for _, d := range []Descriptor{{}, {Manifest: Manifest{ExecutionContext: ExecutionContext{Site: SiteTarget, Device: DeviceRequired}}}} {
		if !d.NeedsDevice(nil) {
			t.Errorf("%+v needs no device", d.Manifest.ExecutionContext)
		}
	}
}
