// Tests of the engine vocabulary: the engines, the carriers in the order a
// method prefers them, and the transport each carrier is.
package capability_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestEngineVocabulary: every carrier's transport is an engine transport,
// in the same preference order, and each is a transport the vocabulary
// knows.
func TestEngineVocabulary(t *testing.T) {
	if got := capability.Engines(); !slices.Equal(got, []capability.EngineKind{capability.EngineDocker, capability.EnginePodman}) {
		t.Errorf("Engines = %v", got)
	}
	carriers := capability.Carriers()
	if !slices.Equal(carriers, []capability.Carrier{capability.CarrierLocal, capability.CarrierSocket, capability.CarrierStdio, capability.CarrierTLS}) {
		t.Errorf("Carriers = %v", carriers)
	}
	transports := capability.EngineTransports()
	if len(transports) != len(carriers) {
		t.Fatalf("%d carriers, %d engine transports", len(carriers), len(transports))
	}
	for i, c := range carriers {
		if c.Transport() != transports[i] {
			t.Errorf("carrier %s is transport %q, want %q in the same order", c, c.Transport(), transports[i])
		}
		if reached, known := capability.ReachedBy(c.Transport()); !known || len(reached) == 0 {
			t.Errorf("no capability reaches transport %q", c.Transport())
		}
	}
}
