// Package external_test holds pkg/external's tests, written against the
// exported API only, the way a third-party external Collection sees it.
//
// This file holds TestMain and the small fixtures several test files
// share. TestMain runs the whole suite under goleak, so a test that starts
// a goroutine (the response reader in program_test.go, the writers in
// context_test.go) and fails to end it fails the package rather than
// passing with a leak.
package external_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestMain runs every test in the package, then fails the run if any
// goroutine a test started is still alive.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fixtureReversibility is the reversibility answer every fixture method
// gives. Not reversible, with the reason registration requires: these
// fixtures change nothing on any device.
var fixtureReversibility = collection.Reversibility{Notes: "a test fixture that changes nothing"}

// registerTestMethod registers fn under a name in this package's own test
// namespace and returns the name. The registry is process-wide and
// outlives the test, so the snapshot is what lets a second iteration under
// -count>1 find the name free again.
func registerTestMethod(t *testing.T, suffix string, status collection.Status, fn collection.Method) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	name := "externaltest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name:     name,
		Manifest: collection.Manifest{Status: status, Reversibility: fixtureReversibility},
		Invoke:   fn,
	}); err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}

// callCounter counts how many times each of a descriptor's two functions
// ran, which is how a test proves a mode reached one function and never
// the other.
type callCounter struct {
	invoke int
	check  int
}

// countingDescriptor returns an implemented descriptor whose Invoke and
// Check each count their calls on c and record which of them ran. Invoke
// reports Changed true and Check reports Changed false, so the response
// alone also says which one answered. supportsCheck false leaves Check
// nil, which is the only shape collection.Register accepts for a method
// without check support.
func countingDescriptor(name string, supportsCheck bool, c *callCounter) collection.Descriptor {
	desc := collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: fixtureReversibility,
			SupportsCheck: supportsCheck,
		},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			c.invoke++
			return collection.Result{Changed: true}, rc.SetStat("ran", "invoke")
		},
	}
	if supportsCheck {
		desc.Check = func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			c.check++
			return collection.Result{Changed: false}, rc.SetStat("ran", "check")
		}
	}
	return desc
}

// tableLookup is a LookupFunc over a fixed set of descriptors, the same
// shape Serve builds over a program's own methods.
func tableLookup(descs ...collection.Descriptor) external.LookupFunc {
	byName := make(map[string]collection.Descriptor, len(descs))
	for _, d := range descs {
		byName[d.Name] = d
	}
	return func(name string) (collection.Descriptor, bool) {
		d, ok := byName[name]
		return d, ok
	}
}

// errReader fails every Read, standing in for a stdin that dies mid-frame.
type errReader struct{}

// Read implements io.Reader by always failing.
func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin exploded") }

// errWriter fails every Write, standing in for a response pipe whose read
// end has already gone away (a parent that died mid-invocation) or a
// stdout nobody is reading.
type errWriter struct{}

// Write implements io.Writer by always failing.
func (errWriter) Write([]byte) (int, error) { return 0, errors.New("response pipe closed") }
