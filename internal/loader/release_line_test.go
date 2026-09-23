// Package loader: the check that the catalog's own engine version
// constraint can load on the release this source tree is on.
package loader

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
)

// TestDefaultEngineVersionLoadsOnTheCurrentRelease proves the constraint
// every generated Collection manifest declares is one the first real
// release can actually meet.
//
// It exists because the value used to be ">=1.0.0" on all 75 built-in
// manifests while the roadmap's next release was 0.2.0. Nothing noticed,
// because no build is a release build yet: every development build reports
// 0.0.0-dev, and checkEngineVersion cannot compare against that, so it
// loads the method and only warns. The first stamped release would have
// refused the entire catalog. This test runs the real comparison against a
// stamped release rather than against the running build, which is the only
// way to ask the question before such a build exists.
func TestDefaultEngineVersionLoadsOnTheCurrentRelease(t *testing.T) {
	if _, ok := buildinfo.Release(buildinfo.CurrentRelease); !ok {
		t.Fatalf("buildinfo.CurrentRelease = %q, which is not a release version",
			buildinfo.CurrentRelease)
	}
	unchecked, err := checkEngineVersion(collectionscaffold.DefaultEngineVersion, buildinfo.CurrentRelease)
	if err != nil {
		t.Errorf("a method declaring %s cannot load on release %s: %v",
			collectionscaffold.DefaultEngineVersion, buildinfo.CurrentRelease, err)
	}
	if unchecked {
		t.Errorf("checkEngineVersion(%q, %q) left the constraint unchecked, so this test proved nothing",
			collectionscaffold.DefaultEngineVersion, buildinfo.CurrentRelease)
	}
}

// TestCurrentReleaseIsNotAheadOfAStampedBuild proves CurrentRelease names a
// release a build stamped with it satisfies, which is what makes it safe to
// use as the catalog's minimum. A value set ahead of the release actually
// being built would refuse every method carrying it.
func TestCurrentReleaseIsNotAheadOfAStampedBuild(t *testing.T) {
	if _, err := checkEngineVersion(">="+buildinfo.CurrentRelease, buildinfo.CurrentRelease); err != nil {
		t.Errorf("a build stamped %s refuses its own release line: %v", buildinfo.CurrentRelease, err)
	}
}
