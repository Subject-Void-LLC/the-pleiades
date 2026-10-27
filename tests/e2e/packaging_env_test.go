//go:build integration

// The environment the packaging gates run their tools in, held to what an
// operator's shell would give them.
package e2e

import (
	"strings"
	"testing"
)

// TestScrubbedPackagingEnv_DropsWhatAParentMakePasses: the make running the
// tests (tools/monitor's `make --trace pr-check`) must not reach a gate's own
// make, whose output the gate reads.
func TestScrubbedPackagingEnv_DropsWhatAParentMakePasses(t *testing.T) {
	for _, name := range makeInherited {
		t.Setenv(name, "--trace")
	}
	t.Setenv("PLEIADES_GATE_KEEPS_THIS", "yes")
	env := scrubbedPackagingEnv()
	kept := false
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		for _, dropped := range makeInherited {
			if name == dropped {
				t.Errorf("%s reached a gate's command", name)
			}
		}
		kept = kept || name == "PLEIADES_GATE_KEEPS_THIS"
	}
	if !kept {
		t.Error("an unrelated variable was dropped too")
	}
}
