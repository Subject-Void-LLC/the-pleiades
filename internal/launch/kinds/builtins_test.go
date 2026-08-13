package kinds_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file asserts what the built-in set is, which is a statement no
// single kind's package can make: each kind's own test binary registers
// only that kind, so "the two kinds route differently" is only observable
// from here, where both are composed.

func TestBuiltins_BothKindsAreReachableFromTheComposedSet(t *testing.T) {
	for _, kind := range []string{"runbook", "playbook"} {
		if _, ok := launch.Lookup(kind); !ok {
			t.Errorf("the %q kind is not registered: a kind package nothing imports never registers, "+
				"which is FAILURE_PATTERNS.md #52 and the whole reason builtins.go exists", kind)
		}
	}
}

func TestBuiltins_TheTwoKindsRouteToDifferentAdapters(t *testing.T) {
	native, _ := launch.Lookup("runbook")
	sandboxed, _ := launch.Lookup("playbook")

	// The kind badge is not decoration. A runbook and a playbook have
	// different trust and performance stories, and the difference is real
	// only because the two reach different executors.
	if native.Adapter == sandboxed.Adapter {
		t.Fatalf("both built-in kinds run on adapter %q, so the badge says nothing about what will run",
			native.Adapter)
	}
	if native.BadgeClass == sandboxed.BadgeClass {
		t.Errorf("both built-in kinds render badge class %q, so a list cannot show which is which",
			native.BadgeClass)
	}
}

func TestBuiltins_NoKindIsRegisteredWithoutAnExecutor(t *testing.T) {
	// PLAN.md Section 28 names seven kinds and two have executors. A kind
	// registered without one would appear on the template form and produce
	// a job nothing ever picks up, which is the built-but-unreachable
	// failure recorded three times here (#52, #96, #101).
	adapters := map[string]bool{"native": true, "legacy": true}

	for _, d := range launch.Kinds() {
		if !adapters[d.Adapter] {
			t.Errorf("kind %q names adapter %q, which no composition root wires: "+
				"a template of that kind would dispatch to nothing", d.Kind, d.Adapter)
		}
	}
}
