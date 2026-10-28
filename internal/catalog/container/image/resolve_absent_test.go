// What container.image.resolve answers for a tag its registry does not
// hold (Phase 131, R4): exists false, in a run and in a check, so a plan can
// ask whether a release is published yet and branch on the answer. Run
// against this machine's real Docker engine and Docker Hub, which holds the
// nginx repository and no tag of this name.
package image_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// absentTag names a tag Docker Hub's nginx repository does not hold.
const absentTag = "nginx:pleiades-absent-tag-0"

// TestResolve_AnAbsentTagIsExistsFalse: a tag the registry answers "not
// found" for is exists false with nothing else reported, in both modes; a
// tag it holds is exists true beside its digest.
func TestResolve_AnAbsentTagIsExistsFalse(t *testing.T) {
	h := newHarness(t)
	for _, check := range []bool{false, true} {
		rc := fresh()
		res, err := call(t, h, rc, "container.image.resolve", check, map[string]any{"image": absentTag})
		if err != nil || res.Changed {
			t.Fatalf("check %v: resolving an absent tag = %+v, %v", check, res, err)
		}
		if len(rc.stats) != 2 || rc.stats["exists"] != false || rc.stats["image"] != absentTag {
			t.Errorf("check %v: stats = %v, want exists false and the image asked for", check, rc.stats)
		}
	}
	rc := fresh()
	if _, err := call(t, h, rc, "container.image.resolve", false, map[string]any{"image": testsupport.NginxImage}); err != nil || rc.stats["exists"] != true || rc.stats["digest"] == nil {
		t.Errorf("a tag the registry holds = %v, %v", rc.stats, err)
	}
}
