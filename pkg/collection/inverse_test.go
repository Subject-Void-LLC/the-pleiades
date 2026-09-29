package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// noopMethod is a real Method value, needed because Register refuses an
// implemented descriptor carrying no implementation, and every case below
// is about an implemented one.
func noopMethod() collection.Method {
	return func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
}

// implemented builds a registerable implemented descriptor carrying r,
// with a unique name per test so registrations cannot collide.
func implemented(name string, r collection.Reversibility) collection.Descriptor {
	return collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: r,
		},
		Invoke: noopMethod(),
	}
}

// There is deliberately very little to check here, and that is the
// result of a design correction rather than thin testing.
//
// An earlier version of this field held a whole inverse on the manifest:
// the FQCN that undoes the method, and the prior-state keys a rollback
// would feed it. This file then checked the internal consistency of that
// claim, and every one of those checks was verifying the coherence of
// something that could not be right in the first place, because the true
// inverse depends on what a RUN found rather than on what the method is.
//
// What survives is the one thing a manifest can honestly state and a
// registration can honestly enforce: an implemented method must say
// whether it can ever undo itself, and a method answering no must say
// why. The instruction itself is emitted at run time and is tested where
// it is produced, by each method's own tests.

// TestRegister_NotReversibleMustSayWhy proves the one rule with teeth.
//
// "This cannot be undone" is the answer an operator most needs a reason
// for, and it is also the easiest answer to reach for when the real one
// is that working out the inverse looked like effort. Requiring the
// reason is what keeps the field from becoming a shrug.
func TestRegister_NotReversibleMustSayWhy(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	err := collection.Register(implemented("test.rev_bare_false", collection.Reversibility{}))
	if err == nil {
		t.Fatal("expected an implemented, non-reversible method with no Notes to be refused")
	}
	// The message has to say what kind of answer is wanted, since the
	// author hitting it is being asked a question they may not have
	// considered.
	for _, want := range []string{"not reversible", "Notes", "observe or reconstruct"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestRegister_ReversibilityAccepted(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	tests := []struct {
		name          string
		fqcn          string
		reversibility collection.Reversibility
	}{
		{
			// A reversible method needs no Notes, though one saying what the
			// inverse does NOT restore is usually worth writing.
			name:          "reversible with no notes",
			fqcn:          "test.rev_true_bare",
			reversibility: collection.Reversibility{Reversible: true},
		},
		{
			name:          "reversible with notes",
			fqcn:          "test.rev_true_notes",
			reversibility: collection.Reversibility{Reversible: true, Notes: "the previous content is not restored"},
		},
		{
			name:          "not reversible with a reason",
			fqcn:          "test.rev_false_notes",
			reversibility: collection.Reversibility{Notes: "a command's effect is unknown to this platform"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := collection.Register(implemented(tc.fqcn, tc.reversibility)); err != nil {
				t.Fatalf("Register: unexpected error: %v", err)
			}
		})
	}
}

// TestRegister_DeclaredStubIsExempt proves a stub does not have to answer.
//
// Forcing all sixty-odd declared stubs to answer a question about code
// nobody has written would produce a table of guesses, which is worse
// than an empty field because it would look like an answer.
func TestRegister_DeclaredStubIsExempt(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	d := collection.Descriptor{
		Name:     "test.rev_declared_stub",
		Manifest: collection.Manifest{Status: collection.StatusDeclared},
	}
	if err := collection.Register(d); err != nil {
		t.Fatalf("a declared stub must not need a Reversibility answer: %v", err)
	}
}

// TestValidateReversibility_RefusesADeclarationThatCannotBeHonored covers
// the undo allowlist's own coherence, each case a declaration a rollback
// could not honor as written.
func TestValidateReversibility_RefusesADeclarationThatCannotBeHonored(t *testing.T) {
	spec := func(fqcn string, record, withhold []string) sdk.InverseSpec {
		return sdk.InverseSpec{FQCN: fqcn, Record: record, Withhold: withhold}
	}
	for _, tc := range []struct {
		name string
		r    collection.Reversibility
		want string
	}{
		{"read-only and reversible", collection.Reversibility{Reversible: true, ReadOnly: true}, "read-only"},
		{"read-only with undo methods", collection.Reversibility{Notes: "x", ReadOnly: true, Inverses: []sdk.InverseSpec{spec("a.b", nil, nil)}}, "read-only"},
		{"undo methods on an irreversible method", collection.Reversibility{Notes: "x", Inverses: []sdk.InverseSpec{spec("a.b", nil, nil)}}, "not reversible"},
		{"an undo method with no namespace", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("remove", nil, nil)}}, "namespaced"},
		{"the same undo method twice", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", nil, nil), spec("a.b", nil, nil)}}, "twice"},
		{"a param both recorded and withheld", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", []string{"name"}, []string{"name"})}}, "name"},
		{"a param recorded twice", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", []string{"name", "name"}, nil)}}, "name"},
		{"an empty param name", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", []string{""}, nil)}}, "empty"},
		{"the device selector recorded", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", []string{collection.TargetParam}, nil)}}, "device or tag"},
		{"the device selector withheld", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", nil, []string{collection.TargetParam})}}, "device or tag"},
		{"the host key bypass recorded", collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{spec("a.b", []string{sdk.ParamInsecureSkipHostKeyVerify}, nil)}}, "host key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := collection.ValidateReversibility(tc.r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateReversibility = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// TestValidateReversibility_AcceptsTheShapesTheCatalogUses is the control.
func TestValidateReversibility_AcceptsTheShapesTheCatalogUses(t *testing.T) {
	for name, r := range map[string]collection.Reversibility{
		"read-only":              {Notes: "reads", ReadOnly: true},
		"reversible, undeclared": {Reversible: true},
		"two targets": {Reversible: true, Inverses: []sdk.InverseSpec{
			{FQCN: "file.remove", Record: []string{"path"}},
			{FQCN: "file.permissions", Record: []string{"path", "mode", "owner", "group"}, MayBePartial: true},
		}},
		"a withheld param": {Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "file.copy", Record: []string{"dest"}, Withhold: []string{"content"}}}},
	} {
		if err := collection.ValidateReversibility(r); err != nil {
			t.Errorf("%s: ValidateReversibility = %v, want nil", name, err)
		}
	}
}

// TestRegister_RefusesAContradictoryUndoDeclaration proves Register runs
// the same validation, naming the method.
func TestRegister_RefusesAContradictoryUndoDeclaration(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	err := collection.Register(implemented("test.rev_readonly_undo", collection.Reversibility{Reversible: true, ReadOnly: true}))
	if err == nil || !strings.Contains(err.Error(), "test.rev_readonly_undo") {
		t.Fatalf("Register = %v, want the contradiction refused naming the method", err)
	}
}
