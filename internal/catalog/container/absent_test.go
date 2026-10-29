// What container.info, container.logs and container.copy_out answer for a
// container the engine does not have (Phase 131, R4): exists false, in a run and in a check, so a
// plan can branch on it. Run against this machine's real Docker engine.
package container_test

import (
	"testing"
)

// TestReads_AnAbsentContainerIsExistsFalseInBothModes: each read names the
// container and says it does not exist, reporting nothing it could only
// have read from one, in a run and in a check alike.
func TestReads_AnAbsentContainerIsExistsFalseInBothModes(t *testing.T) {
	h := newHarness(t)
	name := h.name(t) + "-absent"
	for _, fqcn := range []string{"container.info", "container.logs", "container.copy_out"} {
		for _, check := range []bool{false, true} {
			rc := fresh()
			res, err := call(t, h, rc, fqcn, check, map[string]any{"name": name, "src": "/etc/hostname"})
			if err != nil || res.Changed {
				t.Fatalf("%s, check %v: %+v, %v", fqcn, check, res, err)
			}
			if rc.stats["exists"] != false {
				t.Errorf("%s, check %v: exists = %v, want false", fqcn, check, rc.stats["exists"])
			}
			for _, key := range []string{"id", "state", "stdout", "stderr", "content"} {
				if _, ok := rc.stats[key]; ok {
					t.Errorf("%s, check %v: an absent container reported %s: %v", fqcn, check, key, rc.stats)
				}
			}
		}
	}
}
