// Package loader: the check that the catalog's own engine version
// constraint can load on the release this source tree is on.
package loader

import (
	"strings"
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
// because no build was a release build then: every development build
// reported 0.0.0-dev, which checkEngineVersion does not compare, so it
// loaded the method and only warned. The first stamped release would have
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

// TestADevelopmentBuildOfTheCurrentLineIsNeverCompared holds the version an
// unstamped build reports (<CurrentRelease>-dev, with or without a commit) to
// the development reading: a constraint naming the current line, an older
// one or a newer one is left unchecked, never met and never refused. The
// version carries the release's own numbers, so a reader that dropped the
// -dev suffix would take it for that release and meet ">=CurrentRelease".
func TestADevelopmentBuildOfTheCurrentLineIsNeverCompared(t *testing.T) {
	for _, running := range []string{
		buildinfo.Version(),
		buildinfo.CurrentRelease + "-dev",
		buildinfo.CurrentRelease + "-dev+abc123def456",
		buildinfo.CurrentRelease + "-dev+abc123def456.dirty",
	} {
		if !strings.HasPrefix(running, buildinfo.CurrentRelease+"-dev") {
			t.Fatalf("this test binary reports %q, not a development build of %s", running, buildinfo.CurrentRelease)
		}
		for _, constraint := range []string{">=0.1.0", ">=" + buildinfo.CurrentRelease, ">=99.0.0"} {
			unchecked, err := checkEngineVersion(constraint, running)
			if err != nil || !unchecked {
				t.Errorf("checkEngineVersion(%q, %q) = unchecked %v, err %v; want unchecked and no error", constraint, running, unchecked, err)
			}
		}
	}
}

// TestADryRunIsComparedAsTheReleaseItRehearses holds the version a dry-run
// release is stamped with (<CurrentRelease>-dryrun, make release-dry-run's)
// to the release it rehearses: it meets a constraint naming its own line and
// refuses a newer one, as the real release would.
func TestADryRunIsComparedAsTheReleaseItRehearses(t *testing.T) {
	dryRun := buildinfo.CurrentRelease + "-dryrun"
	if unchecked, err := checkEngineVersion(">="+buildinfo.CurrentRelease, dryRun); err != nil || unchecked {
		t.Errorf("a dry run of %s does not meet its own line: unchecked %v, err %v", buildinfo.CurrentRelease, unchecked, err)
	}
	if _, err := checkEngineVersion(">=99.0.0", dryRun); err == nil {
		t.Errorf("a dry run of %s met >=99.0.0", buildinfo.CurrentRelease)
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
