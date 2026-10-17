// Prune against this machine's real Docker engine, over repositories of
// the test's own only.
package image_test

import (
	"slices"
	"testing"
)

// TestPrune_KeepsTheNewestAndWhatContainersUse: four tags ordered as
// versions (1.2 < 1.9 < 1.10 < 2.0-rc1), keep_newest 2, and a container
// using 1.2's image: a check removes nothing and names 1.9; a run removes
// 1.9 alone; a second run is no change; another repository is untouched.
func TestPrune_KeepsTheNewestAndWhatContainersUse(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	other := h.repo(t)
	for _, tag := range []string{"1.2", "1.9", "1.10", "2.0-rc1"} {
		h.imported(t, repo+":"+tag, "prune "+tag)
	}
	h.imported(t, other+":0.1", "other")
	h.container(t, repo+":1.2")
	params := map[string]any{"repositories": repo, "keep_newest": 2}

	rc := fresh()
	if res, err := call(t, h, rc, "container.image.prune", true, params); err != nil || !res.Changed || h.idOf(t, repo+":1.9") == "" {
		t.Fatalf("a check of prune = %+v, %v", res, err)
	}
	if got, _ := rc.stats["removed"].([]string); !slices.Equal(got, []string{repo + ":1.9"}) {
		t.Errorf("the check would remove %v", got)
	}

	rc = fresh()
	if res, err := call(t, h, rc, "container.image.prune", false, params); err != nil || !res.Changed {
		t.Fatalf("prune = %+v, %v", res, err)
	}
	if got, _ := rc.stats["kept"].([]string); !slices.Equal(got, []string{repo + ":2.0-rc1", repo + ":1.10"}) {
		t.Errorf("kept = %v", got)
	}
	if got, _ := rc.stats["kept_in_use"].([]string); !slices.Equal(got, []string{repo + ":1.2"}) {
		t.Errorf("kept_in_use = %v", got)
	}
	for tag, want := range map[string]bool{"1.2": true, "1.9": false, "1.10": true, "2.0-rc1": true} {
		if got := h.idOf(t, repo+":"+tag) != ""; got != want {
			t.Errorf("%s:%s present = %v, want %v", repo, tag, got, want)
		}
	}
	if h.idOf(t, other+":0.1") == "" {
		t.Error("prune removed another repository's image")
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("prune recorded an undo")
	}
	if res, err := call(t, h, fresh(), "container.image.prune", false, params); err != nil || res.Changed {
		t.Errorf("a second prune = %+v, %v; want no change", res, err)
	}
	if _, err := call(t, h, fresh(), "container.image.prune", false, map[string]any{"repositories": "[", "keep_newest": 1}); err == nil {
		t.Error("a malformed pattern was accepted")
	}
	if _, err := call(t, h, fresh(), "container.image.prune", false, map[string]any{"repositories": repo}); err == nil {
		t.Error("a prune with no keep_newest was accepted")
	}
}
