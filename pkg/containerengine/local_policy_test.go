// Tests that a process reaches an engine through its own socket only when its
// composition root allows it.
package containerengine

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestConnect_TheLocalCarrierNeedsTheCommandLine: a device whose only
// carrier is this machine's own socket is refused in a process that has not
// allowed the local carrier, which is every Runner and its per-task child,
// and is reached (as far as the account check, here) once it has.
func TestConnect_TheLocalCarrierNeedsTheCommandLine(t *testing.T) {
	was := localCarrier.Load()
	t.Cleanup(func() { localCarrier.Store(was) })
	h := engineHost{
		caps:     []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal},
		engines:  []capability.EngineKind{capability.EngineDocker},
		socket:   "/var/run/docker.sock",
		carriers: []capability.Carrier{capability.CarrierLocal},
	}

	localCarrier.Store(false)
	_, _, err := (&Session{}).connect(context.Background(), nil, &h, nil, "container.test", &h, capability.EngineDocker)
	if err == nil || !strings.Contains(err.Error(), "only the pleiades command line may use") {
		t.Fatalf("a Runner's connect = %v, want the local carrier refused", err)
	}

	localCarrier.Store(true)
	_, _, err = (&Session{}).connect(context.Background(), nil, &h, nil, "container.test", &h, capability.EngineDocker)
	if err != nil && strings.Contains(err.Error(), "only the pleiades command line may use") {
		t.Fatalf("the command line's connect = %v, want the local carrier allowed", err)
	}
}
