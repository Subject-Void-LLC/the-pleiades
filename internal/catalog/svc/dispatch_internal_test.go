package svc

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
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

	_, err := dispatch(context.Background(), rc, dev, map[string]any{"name": "x"}, "dispatchtestonly")
	if err == nil {
		t.Fatal("expected a refusal: the target is declared, not implemented")
	}
	for _, want := range []string{dispatchTestOnlyFQCN, "declared but not implemented"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}
