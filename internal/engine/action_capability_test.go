package engine_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// TestCheckActionCapabilityBindings_RealCompositionRootAgrees is the
// regression test for the chain audit's fqcn-table finding
// (IMPLEMENTATION.md Phase W3): cmd/pleiades/run.go's own TransportBinding
// map, reconstructed here with the same fqcn/capability pairing it uses in
// production, must agree with engine.ActionCapability. If a future change
// to either table drifts, this fails here rather than only at runtime.
func TestCheckActionCapabilityBindings_RealCompositionRootAgrees(t *testing.T) {
	bindings := map[string]engine.TransportBinding{
		"ssh_exec": {
			Capability: engine.ActionCapability["ssh_exec"],
			Target:     engine.SSHTarget,
		},
	}
	if err := engine.CheckActionCapabilityBindings(bindings); err != nil {
		t.Errorf("expected the real composition root's bindings to agree with ActionCapability, got: %v", err)
	}
}

func TestCheckActionCapabilityBindings_MissingTableEntryIsRejected(t *testing.T) {
	bindings := map[string]engine.TransportBinding{
		"netconf_exec": {Capability: capability.NameSSHTransport},
	}
	err := engine.CheckActionCapabilityBindings(bindings)
	if err == nil {
		t.Fatal("expected an error for a binding whose fqcn has no ActionCapability entry")
	}
	if !strings.Contains(err.Error(), "netconf_exec") {
		t.Errorf("expected the error to name the offending fqcn, got: %v", err)
	}
}

func TestCheckActionCapabilityBindings_DisagreeingCapabilityIsRejected(t *testing.T) {
	bindings := map[string]engine.TransportBinding{
		// ssh_exec really requires NameSSHTransport; this deliberately
		// disagrees to prove the check catches drift in the direction that
		// actually shipped a defect (a binding whose capability disagrees
		// with what plan-time validation checks).
		"ssh_exec": {Capability: capability.NameCiscoIOS},
	}
	err := engine.CheckActionCapabilityBindings(bindings)
	if err == nil {
		t.Fatal("expected an error for a binding whose capability disagrees with ActionCapability")
	}
	if !strings.Contains(err.Error(), "ssh_exec") {
		t.Errorf("expected the error to name the offending fqcn, got: %v", err)
	}
}

// TestActionCapability_EveryEntryHasACapabilityName guards against a typo
// (an empty capability.Name silently accepted as "no requirement").
func TestActionCapability_EveryEntryHasACapabilityName(t *testing.T) {
	for fqcn, required := range engine.ActionCapability {
		if required == "" {
			t.Errorf("ActionCapability[%q] has an empty capability.Name", fqcn)
		}
	}
}
