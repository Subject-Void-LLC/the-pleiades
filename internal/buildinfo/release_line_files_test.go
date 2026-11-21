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

// TestTheMakefileReadsCurrentReleaseFromThisSource holds the Makefile's own
// read of the release line, which no Go code runs: its sed pattern, applied
// as written to buildinfo.go, must yield CurrentRelease. Moving the constant
// into a const block, or reformatting its line, would leave both dry-run
// targets refusing for want of a version, and only a person running one would
// find out; this fails instead.
func TestTheMakefileReadsCurrentReleaseFromThisSource(t *testing.T) {
	const makefile = "Makefile"
	// The Makefile doubles a dollar to pass one to the shell.
	const sedPattern = `s/^const CurrentRelease = "\([0-9][0-9.]*\)"\r\{0,1\}$$/\1/p`
	if !strings.Contains(repoFile(t, makefile), sedPattern+"' internal/buildinfo/buildinfo.go") {
		t.Fatalf("%s no longer reads CurrentRelease with %s; hold the new pattern here", makefile, sedPattern)
	}
	// The same pattern in Go's syntax: sed's \( \) and \{0,1\} are ( ) and ?.
	const source = "internal/buildinfo/buildinfo.go"
	got := onlyMatch(t, source, repoFile(t, source), regexp.MustCompile(`(?m)^const CurrentRelease = "([0-9][0-9.]*)"\r?$`))
	if got != CurrentRelease {
		t.Errorf("%s: the Makefile's pattern reads %q, and CurrentRelease is %q", source, got, CurrentRelease)
	}
}
