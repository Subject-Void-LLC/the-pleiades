// Package launchable_test covers the registry and the reach predicate: what
// a launchable type must declare to exist, and who may point something at
// one.
//
// Every test registers its own types into a snapshotted registry rather than
// relying on the built-ins, so what is asserted is the rule rather than
// today's two entries, and a third type added later changes nothing here.
package launchable_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// testTypes registers two types into an isolated registry: one that takes a
// saved configuration and one that does not, which is the pair every rule
// below needs to be tested against.
func testTypes(t *testing.T) {
	t.Helper()
	restore := launchable.SnapshotForTest()
	t.Cleanup(restore)

	if err := launchable.Register(launchable.Descriptor{
		Type:               "test_template",
		Label:              "Test template",
		UnifiedJobType:     launchable.UnifiedJobJob,
		LaunchScope:        auth.ScopeRunbookExecute,
		AcceptsSavedConfig: true,
	}); err != nil {
		t.Fatalf("registering the template type: %v", err)
	}
	if err := launchable.Register(launchable.Descriptor{
		Type:           "test_sync",
		Label:          "Test sync",
		UnifiedJobType: launchable.UnifiedJobProjectUpdate,
		LaunchScope:    auth.ScopeProjectWrite,
	}); err != nil {
		t.Fatalf("registering the sync type: %v", err)
	}
}

// target builds a Target of a type, in organization 1.
func target(targetType string) launchable.Target {
	return launchable.Target{
		ID: 7, Type: targetType, Name: "thing",
		OrganizationID: 1, OrganizationName: "acme",
	}
}

// TestRegister_RefusesADescriptorThatWouldFailLater covers each required
// field, because each omission fails somewhere far from the registration: an
// unlabelled type renders as a blank option in a picker, one with no unified
// job name produces a run nothing can classify, and one with no launch scope
// would be launchable by anybody who could write a schedule.
func TestRegister_RefusesADescriptorThatWouldFailLater(t *testing.T) {
	restore := launchable.SnapshotForTest()
	t.Cleanup(restore)

	full := launchable.Descriptor{
		Type:           "complete",
		Label:          "Complete",
		UnifiedJobType: launchable.UnifiedJobJob,
		LaunchScope:    auth.ScopeRunbookExecute,
	}

	cases := []struct {
		name    string
		breakIt func(launchable.Descriptor) launchable.Descriptor
	}{
		{"no type", func(d launchable.Descriptor) launchable.Descriptor { d.Type = "  "; return d }},
		{"no label", func(d launchable.Descriptor) launchable.Descriptor { d.Label = ""; return d }},
		{"no unified job type", func(d launchable.Descriptor) launchable.Descriptor { d.UnifiedJobType = ""; return d }},
		{"no launch scope", func(d launchable.Descriptor) launchable.Descriptor { d.LaunchScope = ""; return d }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := launchable.Register(tc.breakIt(full)); err == nil {
				t.Error("a descriptor missing a required field was registered")
			}
		})
	}

	// The control: the same descriptor with nothing missing registers, so
	// the four refusals above are about the missing field rather than about
	// a registry that refuses everything.
	if err := launchable.Register(full); err != nil {
		t.Errorf("registering a complete descriptor = %v", err)
	}
	if _, ok := launchable.Lookup("complete"); !ok {
		t.Error("a registered type cannot be looked up")
	}
}

// TestTypes_AreOrderedByKey proves the listing is stable, which is what
// keeps a picker's groups and a reference page's rows from reordering
// themselves when an import moves.
func TestTypes_AreOrderedByKey(t *testing.T) {
	testTypes(t)

	got := launchable.Types()
	if len(got) != 2 {
		t.Fatalf("Types() returned %d entries, want the two registered", len(got))
	}
	if got[0].Type != "test_sync" || got[1].Type != "test_template" {
		t.Errorf("Types() = %q, %q, want them sorted by key", got[0].Type, got[1].Type)
	}
}

// TestDescribe_RefusesAnUnknownType proves an unregistered type is refused
// rather than defaulted: a default would launch something other than what
// the caller named.
func TestDescribe_RefusesAnUnknownType(t *testing.T) {
	testTypes(t)

	if _, err := launchable.Describe(target("test_template")); err != nil {
		t.Fatalf("Describe of a registered type = %v", err)
	}
	_, err := launchable.Describe(target("inventory_source"))
	if !errors.Is(err, launchable.ErrUnknownType) {
		t.Errorf("Describe of an unregistered type = %v, want ErrUnknownType", err)
	}
}

// TestReach_Admits is the authorization table. The case that matters most is
// the one in the middle: a caller holding schedule:write and nothing else is
// refused, which is the hole this seam closes (FAILURE_PATTERNS.md #268).
func TestReach_Admits(t *testing.T) {
	testTypes(t)

	identity := func(scopes ...auth.Scope) *auth.Identity {
		return &auth.Identity{Subject: "somebody", Role: auth.RoleOperator, Scopes: scopes}
	}

	cases := []struct {
		name    string
		reach   launchable.Reach
		target  launchable.Target
		wantErr error
	}{
		{
			name:   "the scope the type declares admits it",
			reach:  launchable.ReachOf(identity(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute), 1),
			target: target("test_template"),
		},
		{
			name:    "schedule:write alone launches nothing",
			reach:   launchable.ReachOf(identity(auth.ScopeScheduleWrite), 1),
			target:  target("test_template"),
			wantErr: launchable.ErrNotPermitted,
		},
		{
			name:    "one type's scope does not grant another's",
			reach:   launchable.ReachOf(identity(auth.ScopeRunbookExecute), 1),
			target:  target("test_sync"),
			wantErr: launchable.ErrNotPermitted,
		},
		{
			name:   "each type admits its own scope",
			reach:  launchable.ReachOf(identity(auth.ScopeProjectWrite), 1),
			target: target("test_sync"),
		},
		{
			name:    "another organization's target is refused",
			reach:   launchable.ReachOf(identity(auth.ScopeRunbookExecute), 2),
			target:  target("test_template"),
			wantErr: launchable.ErrCrossTenant,
		},
		{
			name:    "a target in no organization is refused rather than treated as a wildcard",
			reach:   launchable.ReachOf(identity(auth.ScopeRunbookExecute), 0),
			target:  launchable.Target{ID: 7, Type: "test_template", Name: "orphan"},
			wantErr: launchable.ErrCrossTenant,
		},
		{
			name:    "an unauthenticated caller admits nothing",
			reach:   launchable.ReachOf(nil, 1),
			target:  target("test_template"),
			wantErr: launchable.ErrNotPermitted,
		},
		{
			name:    "the zero Reach refuses",
			reach:   launchable.Reach{},
			target:  target("test_template"),
			wantErr: launchable.ErrNotPermitted,
		},
		{
			name:   "a mechanism acting on an earlier decision admits it",
			reach:  launchable.Everything(),
			target: target("test_sync"),
		},
		{
			name:    "an unknown type is refused before anything else is considered",
			reach:   launchable.Everything(),
			target:  target("workflow"),
			wantErr: launchable.ErrUnknownType,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.reach.Admits(tc.target)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("Admits() = %v, want it admitted", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("Admits() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestAdmitsSavedConfig proves a type that takes no launch-time overrides
// refuses one rather than ignoring it, so nobody saves a schedule carrying
// values that would never be applied.
func TestAdmitsSavedConfig(t *testing.T) {
	testTypes(t)

	tmpl, ok := launchable.Lookup("test_template")
	if !ok {
		t.Fatal("the template type is not registered")
	}
	sync, ok := launchable.Lookup("test_sync")
	if !ok {
		t.Fatal("the sync type is not registered")
	}

	if err := tmpl.AdmitsSavedConfig(3); err != nil {
		t.Errorf("a template refused a saved configuration: %v", err)
	}
	if err := tmpl.AdmitsSavedConfig(0); err != nil {
		t.Errorf("a template refused running with its own defaults: %v", err)
	}
	if err := sync.AdmitsSavedConfig(0); err != nil {
		t.Errorf("a sync refused running with no configuration: %v", err)
	}
	if err := sync.AdmitsSavedConfig(3); !errors.Is(err, launchable.ErrSavedConfigRefused) {
		t.Errorf("a sync with a saved configuration = %v, want ErrSavedConfigRefused", err)
	}
}
