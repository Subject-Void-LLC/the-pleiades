package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// FuzzInProcessWorkflowContext proves Merge and Read never panic on
// arbitrary nodeID/deviceID/key/value input, including empty strings (the
// controller-side device key Executor relies on) and repeated Merge calls
// for the same pair (the overwrite path).
func FuzzInProcessWorkflowContext(f *testing.F) {
	f.Add("precheck", "host1", "needs_reboot", "true")
	f.Add("", "", "", "")
	f.Add("node[0]", "", "k", "v")

	f.Fuzz(func(t *testing.T, nodeID, deviceID, key, value string) {
		wc := engine.NewInProcessWorkflowContext()

		if err := wc.Merge(nodeID, deviceID, map[string]interface{}{key: value}); err != nil {
			t.Fatalf("Merge returned an error for in-memory-only input: %v", err)
		}
		// A second Merge for the same pair exercises the overwrite path.
		if err := wc.Merge(nodeID, deviceID, map[string]interface{}{key: value + value}); err != nil {
			t.Fatalf("second Merge returned an error: %v", err)
		}

		if _, err := wc.Read(); err != nil {
			t.Fatalf("Read returned an error: %v", err)
		}
	})
}
