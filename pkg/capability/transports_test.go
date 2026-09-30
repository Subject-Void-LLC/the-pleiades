// Tests for the transport vocabulary and the capabilities that reach
// each transport.
package capability_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestReachedBy_EveryTransportNamesRegisteredCapabilities proves the
// table names only capabilities that exist: a row naming a misspelled or
// removed capability would make its transport unreachable by any device,
// and every method declaring it would be refused everywhere.
func TestReachedBy_EveryTransportNamesRegisteredCapabilities(t *testing.T) {
	transports := capability.Transports()
	if len(transports) == 0 {
		t.Fatal("no transports are known")
	}
	if !slices.IsSorted(transports) {
		t.Errorf("Transports() = %v, want it sorted", transports)
	}
	for _, transport := range transports {
		names, ok := capability.ReachedBy(transport)
		if !ok || len(names) == 0 {
			t.Errorf("%s: reached by %v, %v; want at least one capability", transport, names, ok)
		}
		for _, name := range names {
			if _, known := capability.Lookup(name); !known {
				t.Errorf("%s is reached by %s, which is not a registered capability", transport, name)
			}
		}
	}
}

// TestReachedBy_TheRows pins the table itself, since every method's
// declared transports are checked against it.
func TestReachedBy_TheRows(t *testing.T) {
	want := map[string][]capability.Name{
		capability.TransportSSH:     {capability.NameSSHTransport},
		capability.TransportWinRM:   {capability.NameWinRM},
		capability.TransportNetconf: {capability.NameNetconf},
		capability.TransportHTTPS:   {capability.NameHTTPAPI, capability.NameCatalystAPI},
		capability.TransportDocker:  {capability.NameDocker},
	}
	if got := capability.Transports(); len(got) != len(want) {
		t.Fatalf("Transports() = %v, want exactly %d", got, len(want))
	}
	for transport, names := range want {
		got, ok := capability.ReachedBy(transport)
		if !ok || !slices.Equal(got, names) {
			t.Errorf("ReachedBy(%q) = %v, %v; want %v", transport, got, ok, names)
		}
	}
}

// TestReachedBy_UnknownAndCopied covers an unknown transport and proves
// the returned slice is the caller's own, so no caller can edit the
// table through it.
func TestReachedBy_UnknownAndCopied(t *testing.T) {
	if names, ok := capability.ReachedBy("shh"); ok || names != nil {
		t.Errorf("ReachedBy(\"shh\") = %v, %v; want nil, false", names, ok)
	}
	names, _ := capability.ReachedBy(capability.TransportSSH)
	names[0] = "Tampered"
	again, _ := capability.ReachedBy(capability.TransportSSH)
	if again[0] != capability.NameSSHTransport {
		t.Fatal("editing ReachedBy's result edited the table")
	}
}
