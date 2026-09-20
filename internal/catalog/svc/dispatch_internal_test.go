package svc

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

// dispatchTestOnlyFQCN is a throwaway descriptor registered purely so
// TestDispatch_DeclaredButNotImplementedTargetIsNamed has a real,
// registered-but-unimplemented target to resolve to.
//
// Every concrete verb this dispatcher's own managerNamespace table
// actually maps to (svc.systemd.*, svc.windows.*) is now a real
// implementation, which is progress this test should not stand in the
// way of. dispatch's own "declared but not implemented" branch is still
// real code, though, and still deserves a real registry entry to prove
// itself against rather than going untested -- svc_test.go (the external
// black-box suite) can only ever reach the five real verb names, since
// Start/Stop/Restart/Enable/Disable each hardcode their own, so this
// whitebox file is what lets a test call dispatch directly with an
// invented verb.
const dispatchTestOnlyFQCN = "svc.systemd.dispatchtestonly"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: dispatchTestOnlyFQCN,
		Manifest: collection.Manifest{
			RequiredCapabilities: []capability.Name{capability.NameSystemd},
			Status:               collection.StatusDeclared,
			Doc:                  collection.Doc{Summary: "test fixture only: never wired to an Invoke"},
		},
	})
}

type dispatchTestRC struct{ stats map[string]any }

func (c *dispatchTestRC) InjectSecrets() map[string]string { return map[string]string{} }
func (c *dispatchTestRC) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *dispatchTestRC) EmitFact(k string, v any) error   { return c.SetStat(k, v) }

type dispatchTestDevice struct{ *inventorytest.Stub }

func (d *dispatchTestDevice) ServiceManagerName() string { return "systemd" }

func TestDispatch_DeclaredButNotImplementedTargetIsNamed(t *testing.T) {
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{
		StubName: "s1", Caps: []capability.Name{capability.NameSystemd},
	}}
	rc := &dispatchTestRC{stats: map[string]any{}}

	_, err := dispatch(context.Background(), rc, dev, map[string]any{"name": "x"}, "dispatchtestonly", collection.ModeExecute)
	if err == nil {
		t.Fatal("expected a refusal: the target is declared, not implemented")
	}
	for _, want := range []string{dispatchTestOnlyFQCN, "declared but not implemented"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestDispatch_ACheckReachesTheConcreteCheck covers the generic methods'
// checks: in check mode dispatch runs the concrete method's Check and
// never its Invoke, and a concrete method with no check support answers
// "cannot check this call".
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
			Name:     "svc.systemd." + verb,
			Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"}, SupportsCheck: supports},
			Invoke:   invoke,
		}
		if supports {
			d.Check = check
		}
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{StubName: "web1"}}
	if _, err := dispatch(context.Background(), nil, dev, nil, "faketest_checkable", collection.ModeCheck); err != nil {
		t.Fatalf("a check of a checkable concrete method: %v", err)
	}
	_, err := dispatch(context.Background(), nil, dev, nil, "faketest_uncheckable", collection.ModeCheck)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "svc.systemd.faketest_uncheckable does not declare check support") {
		t.Errorf("a check of an uncheckable concrete method = %v, want a CannotCheckError naming it", err)
	}
	if invoked != 0 || checked != 1 {
		t.Errorf("Invoke ran %d time(s) and Check %d; want 0 and 1", invoked, checked)
	}
}

// TestDispatch_RefusesWhatItCannotResolve covers the two refusals no real
// verb reaches: a concrete method the device's manager maps to that is not
// registered in this binary, and a mode that is neither a run nor a check.
// Each must stop before anything is invoked and say what it could not
// resolve, rather than falling through to some other method.
func TestDispatch_RefusesWhatItCannotResolve(t *testing.T) {
	dev := &dispatchTestDevice{Stub: &inventorytest.Stub{
		StubName: "s1", Caps: []capability.Name{capability.NameSystemd},
	}}
	rc := &dispatchTestRC{stats: map[string]any{}}

	_, err := dispatch(context.Background(), rc, dev, map[string]any{"name": "x"}, "nosuchverb", collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.systemd.nosuchverb is not registered") {
		t.Errorf("an unregistered target = %v, want a refusal naming svc.systemd.nosuchverb", err)
	}

	_, err = dispatch(context.Background(), rc, dev, map[string]any{"name": "x"}, "start", collection.Mode("rehearse"))
	if err == nil || !strings.Contains(err.Error(), `unknown mode "rehearse"`) {
		t.Errorf("an unknown mode = %v, want a refusal naming the mode", err)
	}
	var cannot *collection.CannotCheckError
	if errors.As(err, &cannot) {
		t.Error("an unknown mode was answered as a check that cannot happen, which a check run would count as merely unchecked")
	}
}
