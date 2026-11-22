// This file holds the test that says when the short-path rescue can go: it
// reads the gosec version the Makefile pins, with no gosec and no container
// needed, so it runs in every test job.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// lastSplittingGosec is the newest gosec release known to load a package as
// its files' absolute paths, which go/packages splits. securego/gosec #1803
// (2026-10-26) loads a module's package by its directory instead; no release
// after v2.30.0 existed when this was written (2026-11-22).
const lastSplittingGosec = "v2.30.0"

// TestGosecPin_SaysWhenTheRescueCanGo fails once the Makefile's
// GOSEC_VERSION is past lastSplittingGosec, naming what to check and what to
// delete, so the rescue does not outlive its cause.
func TestGosecPin_SaysWhenTheRescueCanGo(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^GOSEC_VERSION\s*\??=\s*(\S+)`).FindSubmatch(data)
	if m == nil {
		t.Fatal("the Makefile sets no GOSEC_VERSION")
	}
	pinned := string(m[1])
	if newer(pinned, lastSplittingGosec) {
		t.Errorf("the Makefile pins gosec %s, past %s. Check whether that release contains securego/gosec #1803 "+
			"(Analyzer.load passing a module's package directory, not its files). If it does, delete tools/gosec-check's "+
			"split.go, shortpath.go, shortpath_unix.go, shortpath_windows.go, the two-pass branch in scan.go (everything "+
			"after `if len(long) == 0`), their tests (split_test.go, shortpath_test.go, shortpath_unix_test.go, "+
			"TestScan_RescuesAPackageSplitByALongPath and TestScan_RefusesWhatTheShortPathCannotRescue in scan_test.go) "+
			"and this file. If it does not, set lastSplittingGosec to %s.", pinned, lastSplittingGosec, pinned)
	}
}

// newer reports whether version a (vX.Y.Z) is after b. A version that does
// not read as one is newer, so the test asks to be looked at.
func newer(a, b string) bool {
	pa, pb := semver(a), semver(b)
	if pa == nil || pb == nil {
		return true
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// semver reads vX.Y.Z, nil when v is not that.
func semver(v string) []int {
	m := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

// TestNewer_ComparesEachPart: versions compare part by part as numbers.
func TestNewer_ComparesEachPart(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v2.30.0", "v2.30.0", false}, {"v2.30.1", "v2.30.0", true}, {"v2.31.0", "v2.30.0", true},
		{"v2.29.9", "v2.30.0", false}, {"v3.0.0", "v2.30.0", true}, {"v2.100.0", "v2.30.0", true}, {"latest", "v2.30.0", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
