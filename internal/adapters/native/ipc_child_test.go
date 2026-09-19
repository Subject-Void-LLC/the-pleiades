package native

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// registerChildTestMethod registers a Collection method under a name unique
// to the calling test, for the tests that drive runCollectionChild. The
// child logic itself is pkg/external's, and its own tests live there.
func registerChildTestMethod(t *testing.T, suffix string, status collection.Status, fn collection.Method) string {
	t.Helper()
	// The registry is process-wide and outlives this test, so without the
	// snapshot a second iteration under -count>1 finds the name taken.
	t.Cleanup(collection.SnapshotForTest())
	name := "nativechildtest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name: name,
		// A test fixture still answers the question every real implemented
		// method answers. Not reversible, with the reason registration
		// requires: these fixtures change nothing on any device.
		Manifest: collection.Manifest{Status: status, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
		Invoke:   fn,
	}); err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}
