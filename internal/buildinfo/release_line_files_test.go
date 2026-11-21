// Package buildinfo tests that the files outside Go which name this tree's
// release line agree with CurrentRelease.
package buildinfo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoFile reads path, relative to the repository root, which is two
// directories above this package's own.
func repoFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// onlyMatch returns the one submatch pattern finds in text, failing the test
// when it finds none or more than one, since a second match would be a line
// this test does not hold.
func onlyMatch(t *testing.T, file, text string, pattern *regexp.Regexp) string {
	t.Helper()
	matches := pattern.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		t.Fatalf("%s: found %d lines matching %s, want exactly one", file, len(matches), pattern)
	}
	return matches[0][1]
}

// TestReleaseLineFilesFollowCurrentRelease holds the two values that cannot
// be read from Go to the version an unstamped build reports and the one a dry
// run is stamped with. The Helm chart's appVersion names the development build
// a chart from this tree deploys (CurrentRelease-dev), and the release
// workflow's manual dispatch defaults to the dry run make release-dry-run
// builds (CurrentRelease-dryrun). Both are literals, so moving CurrentRelease
// without them would leave each naming the old line; this test fails instead.
func TestReleaseLineFilesFollowCurrentRelease(t *testing.T) {
	const chart = "helm/the-pleiades/Chart.yaml"
	appVersion := onlyMatch(t, chart, repoFile(t, chart), regexp.MustCompile(`(?m)^appVersion:\s*"([^"]*)"\s*$`))
	if appVersion != developmentBase {
		t.Errorf("%s: appVersion is %q, want %q (CurrentRelease with -dev)", chart, appVersion, developmentBase)
	}
	if _, ok := Release(appVersion); ok {
		t.Errorf("%s: appVersion %q reads as a release", chart, appVersion)
	}

	const workflow = ".github/workflows/release.yml"
	text := repoFile(t, workflow)
	dryRun := CurrentRelease + "-dryrun"
	if got := onlyMatch(t, workflow, text, regexp.MustCompile(`(?m)^\s+default:\s*"([^"]*)"\s*$`)); got != dryRun {
		t.Errorf("%s: the dispatch's default version is %q, want %q", workflow, got, dryRun)
	}
	if !strings.Contains(onlyMatch(t, workflow, text, regexp.MustCompile(`(?m)^\s+description:\s*"(Version to build[^"]*)"\s*$`)), dryRun) {
		t.Errorf("%s: the dispatch's description does not give %q as its example", workflow, dryRun)
	}
	if got, ok := Release(dryRun); !ok || got != CurrentRelease {
		t.Errorf("Release(%q) = %q, %v; a dry run must read as the release it rehearses", dryRun, got, ok)
	}
}
