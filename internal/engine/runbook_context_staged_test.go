// Tests that only a context the command line wraps lets a check stage a
// change in a device's staging area.
package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestWithStagedChecks: a wrapped context allows a staged check, an
// unwrapped one (a Runner's) does not, a builder's error passes through, and
// a context of another kind is left as it is.
func TestWithStagedChecks(t *testing.T) {
	plain, err := NewDeviceRunbookContext(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sdk.StagedChecksAllowed(plain) {
		t.Error("an unwrapped context allows a staged check")
	}
	rc, err := WithStagedChecks(NewDeviceRunbookContext)(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sdk.StagedChecksAllowed(rc) {
		t.Error("a context the command line wraps does not allow a staged check")
	}

	boom := errors.New("no credential store")
	failing := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) { return nil, boom }
	if _, err := WithStagedChecks(failing)(context.Background(), nil); !errors.Is(err, boom) {
		t.Errorf("an error from the wrapped builder = %v, want it passed through", err)
	}
	other := struct{ sdk.RunbookContext }{}
	foreign := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) { return other, nil }
	if rc, err := WithStagedChecks(foreign)(context.Background(), nil); err != nil || rc != other || sdk.StagedChecksAllowed(rc) {
		t.Errorf("a context of another kind = %v, %v; want it unchanged and not allowing a staged check", rc, err)
	}
}
