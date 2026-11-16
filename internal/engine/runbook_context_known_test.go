// Tests that only a context the command line wraps hands a method the values
// its run masks from its start.
package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestWithKnownSecretValues: a wrapped context hands out the values, a copy
// each time and of what it was given; an unwrapped one (a Runner's) hands out
// none; a builder's error passes through; a context of another kind is left
// as it is.
func TestWithKnownSecretValues(t *testing.T) {
	plain, err := NewDeviceRunbookContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sdk.KnownSecretValues(plain); len(got) != 0 {
		t.Errorf("an unwrapped context hands out %v", got)
	}
	given := []string{"psk-from-the-vault", "another-named-secret"}
	rc, err := WithKnownSecretValues(NewDeviceRunbookContext, given)(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	given[0] = "changed by the caller"
	got := sdk.KnownSecretValues(rc)
	if !slices.Equal(got, []string{"psk-from-the-vault", "another-named-secret"}) {
		t.Fatalf("KnownSecretValues = %v, want what the run was given", got)
	}
	got[1] = "changed by the method"
	if again := sdk.KnownSecretValues(rc); again[1] != "another-named-secret" {
		t.Errorf("a method changed the run's values: %v", again)
	}

	boom := errors.New("no credential store")
	failing := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) { return nil, boom }
	if _, err := WithKnownSecretValues(failing, given)(context.Background(), nil); !errors.Is(err, boom) {
		t.Errorf("an error from the wrapped builder = %v, want it passed through", err)
	}
	other := struct{ sdk.RunbookContext }{}
	foreign := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) { return other, nil }
	if rc, err := WithKnownSecretValues(foreign, given)(context.Background(), nil); err != nil || rc != other || sdk.KnownSecretValues(rc) != nil {
		t.Errorf("a context of another kind = %v, %v; want it unchanged, handing out nothing", rc, err)
	}
}
