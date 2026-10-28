// What container.image.info answers for a named image the engine does not
// hold (Phase 131, R4): the image listed under missing, never an error, in
// a run and in a check alike, so a plan can branch on whether an image is
// there yet. Run against this machine's real Docker engine.
package image_test

import "testing"

// TestInfo_AMissingImageIsListedInBothModes: a named image that is absent
// is listed under missing beside one that is present, with nothing
// described for it, and the task succeeds unchanged in both modes.
func TestInfo_AMissingImageIsListedInBothModes(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	id := h.imported(t, repo+":1", "info-absent")
	for _, check := range []bool{false, true} {
		rc := fresh()
		res, err := call(t, h, rc, "container.image.info", check, map[string]any{"images": []any{repo + ":absent", repo + ":1"}})
		if err != nil || res.Changed {
			t.Fatalf("info (check %v) = %+v, %v", check, res, err)
		}
		if missing, _ := rc.stats["missing"].([]string); len(missing) != 1 || missing[0] != repo+":absent" {
			t.Errorf("missing (check %v) = %#v, want only %s:absent", check, rc.stats["missing"], repo)
		}
		if images, _ := rc.stats["images"].([]map[string]any); len(images) != 1 || images[0]["id"] != id {
			t.Errorf("images (check %v) = %v, want only the present one", check, rc.stats["images"])
		}
	}
}
