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
