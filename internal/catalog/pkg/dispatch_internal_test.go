package pkg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file is an internal test (package pkg, not pkg_test) specifically
// to reach dispatch's two refusal branches that the public Install,
// Remove and Upgrade functions can never reach on their own: they always
// call dispatch with a hardcoded verb ("install", "remove", "upgrade"),
// and every concrete method under both real managerNamespace entries
// (pkg.apt.*, pkg.dnf.*) is implemented today, so "not registered" and
// "declared but not implemented" are both genuinely unreachable through
// the public API. dispatch itself still needs to answer them correctly,
// for the day a verb is added to the generic layer before its concrete
// siblings exist.

type dispatchTestDevice struct {
	*inventorytest.Stub
	manager string
}

func (d *dispatchTestDevice) SSHHost() string            { return "127.0.0.1" }
func (d *dispatchTestDevice) SSHPort() int               { return 22 }
func (d *dispatchTestDevice) PackageManagerName() string { return d.manager }

func TestDispatch_TargetNotRegistered(t *testing.T) {
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}, manager: "apt"}
	_, err := dispatch(context.Background(), nil, dev, nil, "reinstall", collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "pkg.apt.reinstall is not registered") {
		t.Errorf("err = %v, want it to say pkg.apt.reinstall is not registered", err)
	}
}

func TestDispatch_TargetDeclaredButNotImplemented(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())

	const fake = "pkg.apt.faketest_dispatch_coverage"
	if err := collection.Register(collection.Descriptor{
		Name: fake,
		Manifest: collection.Manifest{
			RequiredCapabilities: []capability.Name{capability.NameApt},
			Status:               collection.StatusDeclared,
		},
	}); err != nil {
		t.Fatalf("registering the test fixture: %v", err)
	}

	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}, manager: "apt"}
	_, err := dispatch(context.Background(), nil, dev, nil, "faketest_dispatch_coverage", collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "declared but not implemented yet") {
		t.Errorf("err = %v, want it to say declared but not implemented", err)
	}
}

// TestDispatch_ACheckReachesTheConcreteCheck covers the generic methods'
// checks: in check mode dispatch runs the concrete method's Check and
// never its Invoke, and a concrete method with no check support answers
// "cannot check this call", so the task is reported unchecked rather than
// failed or run.
func TestDispatch_ACheckReachesTheConcreteCheck(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	var invoked, checked int
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		invoked++
		return collection.Result{Changed: true}, nil
	}
	check := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		checked++
		return collection.Result{Changed: true}, nil
	}
	for verb, supports := range map[string]bool{"faketest_checkable": true, "faketest_uncheckable": false} {
		d := collection.Descriptor{
			Name: "pkg.apt." + verb,
			Manifest: collection.Manifest{
				Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"}, SupportsCheck: supports,
			},
			Invoke: invoke,
		}
		if supports {
			d.Check = check
		}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}, manager: "apt"}

	if _, err := dispatch(context.Background(), nil, dev, nil, "faketest_checkable", collection.ModeCheck); err != nil {
		t.Fatalf("a check of a checkable concrete method: %v", err)
	}
	_, err := dispatch(context.Background(), nil, dev, nil, "faketest_uncheckable", collection.ModeCheck)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "pkg.apt.faketest_uncheckable does not declare check support") {
		t.Errorf("a check of an uncheckable concrete method = %v, want a CannotCheckError naming it", err)
	}
	if invoked != 0 || checked != 1 {
		t.Errorf("Invoke ran %d time(s) and Check %d; want 0 and 1", invoked, checked)
	}
}
