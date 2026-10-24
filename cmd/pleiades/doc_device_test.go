// Tests that pleiades doc reads a device type by its name: the properties
// its record is read for, how its devices start, and what onboarding may
// grant.
package main

import (
	"strings"
	"testing"
)

// TestDoc_RendersADeviceType: a type reached by an API names its TLS pin
// and its own credential, a Cisco router names the property that chooses
// its carrier, an onboarded type says it starts discovered and what
// onboarding may grant, and a name that is neither a method nor a type is
// refused saying a type may be looked up.
func TestDoc_RendersADeviceType(t *testing.T) {
	for typ, wants := range map[string][]string{
		"cml_controller": {"Device type: cml_controller", "Starts:      active", "its own only", "  tls_fingerprint\n", "  api_base\n"},
		"cisco_router":   {"  transport\n", "  console_server\n", "--type cisco_router --set key=value"},
		"ai_provider":    {"Starts:      discovered", "Onboarding may grant: AIProviderCapable", "  ai_kind\n"},
	} {
		var runErr error
		out := captureStdout(t, func() { runErr = runDoc([]string{typ}) })
		if runErr != nil {
			t.Fatalf("%s: %v", typ, runErr)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s: doc printed no %q:\n%s", typ, want, out)
			}
		}
	}
	if err := runDoc([]string{"no_such_type"}); err == nil || !strings.Contains(err.Error(), "a device type is looked up by its name") {
		t.Errorf("an unknown name: %v", err)
	}
}
