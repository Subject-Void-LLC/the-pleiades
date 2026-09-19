// Package catalyst_test: what the methods do when the controller cannot
// be addressed, and when a fact the controller answered cannot be stored.
//
// The happy paths in catalyst_test.go replay real captured JSON. These
// cover the other half: a task pointed at something that is not a usable
// controller, which must be refused before any request, and the storing
// step at the end, where a method that swallowed the failure would report
// a gather that recorded nothing.
package catalyst_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestMethodsRefuseADeviceTheyCannotAddress covers each way a target is
// not a controller this method can reach: one that claims the capability
// without implementing it, and one implementing it with no base URL.
// Both are refused naming the device, before any request is sent.
func TestMethodsRefuseADeviceTheyCannotAddress(t *testing.T) {
	// A stub carries the capability name in its list without the accessor
	// the real device type has, which is the shape the refusal exists
	// for: a device type built wrong, rather than a device of the wrong
	// type, which the capability check above it already refuses.
	claimsOnly := &inventorytest.Stub{
		StubName: "not-really-a-controller",
		Caps:     []capability.Name{capability.NameCatalystAPI},
	}
	noURL := newController(t, "")

	for _, name := range []string{
		"net.catalyst.device_facts",
		"net.catalyst.site_facts",
		"net.catalyst.tag_facts",
		"net.catalyst.reachability",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := invoke(t, name, newFakeContext(validSecrets()), claimsOnly, nil)
			if err == nil || !strings.Contains(err.Error(), "does not implement it") {
				t.Errorf("a device claiming the capability without implementing it = %v", err)
			}
			_, err = invoke(t, name, newFakeContext(validSecrets()), noURL, nil)
			if err == nil || !strings.Contains(err.Error(), "no Catalyst Center base URL") {
				t.Errorf("a controller with no base URL = %v", err)
			}
		})
	}
}

// TestMethodsRefuseARunWithNoUsername covers the credential the task is
// given rather than the one it sends: with no username in the run's
// secrets there is nothing to authenticate as, so the method says which
// secret is missing instead of sending an anonymous request the
// controller would answer with an unexplained 401.
func TestMethodsRefuseARunWithNoUsername(t *testing.T) {
	_, controller := newSandbox(t)
	_, err := invoke(t, "net.catalyst.device_facts", newFakeContext(map[string]string{"password": "Cisco123!"}), controller, nil)
	if err == nil || !strings.Contains(err.Error(), `no "username" secret`) {
		t.Errorf("err = %v, want it to name the missing secret", err)
	}
}

// TestMethodsFailWhenAFactCannotBeStored covers the last step of each
// method: the facts are its whole product, so one that could not be
// stored fails the task. Each case names a fact emitted after at least
// one other, so it also proves a method does not stop at its first
// emission and report success for the rest.
func TestMethodsFailWhenAFactCannotBeStored(t *testing.T) {
	_, controller := newSandbox(t)
	for _, tc := range []struct {
		method string
		fact   string
	}{
		{method: "net.catalyst.device_facts", fact: "device_count"},
		{method: "net.catalyst.site_facts", fact: "site_count"},
		{method: "net.catalyst.tag_facts", fact: "operator_tags"},
		{method: "net.catalyst.reachability", fact: "reachability"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rc := newFakeContext(validSecrets())
			rc.failFact = tc.fact
			result, err := invoke(t, tc.method, rc, controller, nil)
			if !errors.Is(err, errEmitting) {
				t.Errorf("err = %v, want the storing failure", err)
			}
			if result.Changed {
				t.Error("a fact gather reported a change")
			}
		})
	}
}
