// Tests for the reserved parameter names (reserved.go): no method, built
// in or external, may declare a key the engine reads from a task's params.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// withParams builds a registerable implemented descriptor documenting the
// named params, and nothing else, under a name unique to the caller.
func withParams(name string, params ...string) collection.Descriptor {
	d := implemented(name, collection.Reversibility{Notes: "test"})
	for _, p := range params {
		d.Manifest.Doc.Params = append(d.Manifest.Doc.Params, collection.Param{Name: p, Type: "string", Description: "test"})
	}
	return d
}

// TestRegister_RefusesAReservedParam proves a method cannot declare a
// parameter the engine reads for itself, which is the collision that let
// net.netconf.config's datastore double as the device selector.
func TestRegister_RefusesAReservedParam(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	for _, name := range collection.ReservedParams() {
		err := collection.Register(withParams("test.reserved_"+name, "content", name))
		if err == nil {
			t.Fatalf("a method declaring %q registered, want it refused", name)
		}
		// The message has to name the parameter and say why, since the
		// author hitting it chose that name deliberately.
		for _, want := range []string{name, "device or tag"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to mention %q", err, want)
			}
		}
		if _, ok := collection.Lookup("test.reserved_" + name); ok {
			t.Errorf("refused method %q is in the registry anyway", "test.reserved_"+name)
		}
	}
}

// TestRegister_AcceptsParamsThatOnlyResembleAReservedOne is the control:
// a name that merely contains a reserved one is a different key in the
// params map and collides with nothing.
func TestRegister_AcceptsParamsThatOnlyResembleAReservedOne(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(withParams("test.reserved_lookalike", "target_group", "datastore", "Target")); err != nil {
		t.Fatalf("Register = %v, want a method with look-alike param names accepted", err)
	}
}

// TestReservedParams_IsACopy proves a caller cannot widen or narrow the
// reserved set through the slice it was handed.
func TestReservedParams_IsACopy(t *testing.T) {
	got := collection.ReservedParams()
	if len(got) == 0 || got[0] != collection.TargetParam {
		t.Fatalf("ReservedParams() = %v, want it to start with %q", got, collection.TargetParam)
	}
	got[0] = "changed"
	if !collection.IsReservedParam(collection.TargetParam) || collection.IsReservedParam("changed") {
		t.Fatal("editing the returned slice changed the reserved set")
	}
}
