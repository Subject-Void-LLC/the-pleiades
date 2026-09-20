// Package types_test pins what the built-in launchable types declare.
//
// Every other test in internal/launchable registers its own types, which is
// right for testing the rules and leaves nothing checking the two types this
// build actually ships. Each field asserted here is one a later edit could
// change without failing anything else, and two of them are security
// decisions: the scope a type declares is what a schedule needs before it
// may launch it, and whether a type takes a saved configuration decides what
// a form offers.
//
// The keys are AWX's own subclass names. They are asserted literally, since
// their whole purpose is that an imported AWX schedule resolves its
// unified_job_template without a translation table.
package types_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types"
)

// TestBuiltins_AreRegisteredAsDeclared asserts the shipped table entry by
// entry, and that it holds nothing else.
func TestBuiltins_AreRegisteredAsDeclared(t *testing.T) {
	want := map[string]launchable.Descriptor{
		"job_template": {
			Type:               "job_template",
			Label:              "Job template",
			UnifiedJobType:     launchable.UnifiedJobJob,
			LaunchScope:        auth.ScopeRunbookExecute,
			AcceptsSavedConfig: true,
		},
		"project": {
			Type:               "project",
			Label:              "Project sync",
			UnifiedJobType:     launchable.UnifiedJobProjectUpdate,
			LaunchScope:        auth.ScopeProjectWrite,
			AcceptsSavedConfig: false,
		},
	}

	got := launchable.Types()
	if len(got) != len(want) {
		t.Fatalf("%d types are registered, want %d: %+v", len(got), len(want), got)
	}
	for _, d := range got {
		expected, ok := want[d.Type]
		if !ok {
			t.Errorf("type %q is registered and this test does not know about it; if it is new, pin it here", d.Type)
			continue
		}
		if d != expected {
			t.Errorf("type %q = %+v, want %+v", d.Type, d, expected)
		}
	}
}

// TestBuiltins_DeclareTheScopeTheirOwnRouteRequires is the rule behind the
// scopes above, stated as its own assertion because the reason is not
// visible from the value: arranging for something to run repeatedly and
// unattended must need at least what running it once by hand needs. A type
// declaring a weaker scope than its own route would make a schedule a way
// around that route.
func TestBuiltins_DeclareTheScopeTheirOwnRouteRequires(t *testing.T) {
	// The scope each type's own launch route declares (apispec.LaunchTemplate
	// and apispec.SyncProject). Restated rather than read from apispec, which
	// would make this package depend on the route table to assert a property
	// of itself.
	routeScopes := map[string]auth.Scope{
		"job_template": auth.ScopeRunbookExecute,
		"project":      auth.ScopeProjectWrite,
	}

	for _, d := range launchable.Types() {
		want, ok := routeScopes[d.Type]
		if !ok {
			t.Errorf("type %q declares no route scope in this test; add it with the route it matches", d.Type)
			continue
		}
		if d.LaunchScope != want {
			t.Errorf("type %q launches on %q, but its own route requires %q", d.Type, d.LaunchScope, want)
		}
		// Neither type may launch on a schedule-writing scope, which is the
		// hole this seam closes: schedule:write decides who may arrange a
		// run, never what may be run.
		if d.LaunchScope == auth.ScopeScheduleWrite || d.LaunchScope == auth.ScopeScheduleRead {
			t.Errorf("type %q launches on %q, a schedule scope, so writing a schedule would grant launching it", d.Type, d.LaunchScope)
		}
	}
}
