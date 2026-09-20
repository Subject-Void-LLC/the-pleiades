// Package buildinfo: tests of Version and Release.
package buildinfo

import (
	"runtime/debug"
	"testing"
)

// TestRelease is the one definition of a release every consumer shares:
// the loader enforcing a constraint and the scaffold writing one.
func TestRelease(t *testing.T) {
	for v, want := range map[string]string{
		"1.2.0":                        "1.2.0",
		"v1.2.0":                       "1.2.0",
		"1.0.0-rc1":                    "1.0.0",
		"1.2.3-rc1+build7":             "1.2.3",
		"1.2.0+meta":                   "1.2.0",
		"v1.2.0-3-gabc1234-dirty":      "1.2.0",
		"0.0.0-dev":                    "",
		"0.0.0-dev+abc123def456":       "",
		"0.0.0-dev+abc123def456.dirty": "",
		"1.3.0-dev":                    "",
		"1.3.0-devel.2":                "",
		"dev":                          "",
		"":                             "",
		"unknown":                      "",
		"1.2":                          "",
		"1.02.0":                       "",
		"1.2.0.4":                      "",
		"-1.2.0":                       "",
		"1.2.x":                        "",
	} {
		got, ok := Release(v)
		if ok != (want != "") || got != want {
			t.Errorf("Release(%q) = %q, %v; want %q, %v", v, got, ok, want, want != "")
		}
	}
}

// TestResolve covers where a version comes from: a stamped release wins;
// a stamped value that is not a release is never reported as a version;
// a development build names the commit, stamped or from the toolchain,
// marking an uncommitted tree; and a build with neither says 0.0.0-dev.
func TestResolve(t *testing.T) {
	toolchain := func(rev string, modified bool) *debug.BuildInfo {
		m := "false"
		if modified {
			m = "true"
		}
		return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: m}}}
	}
	const sha = "cb509f7d1e2a3b4c5d6e7f8091a2b3c4d5e6f708"
	for _, tc := range []struct {
		name, version, revision string
		info                    *debug.BuildInfo
		want                    string
	}{
		{"a stamped release", "1.4.0", sha, toolchain(sha, true), "1.4.0"},
		{"a stamped release candidate", "1.4.0-rc2", "", nil, "1.4.0-rc2"},
		{"a git describe off a tag", "v1.4.0-3-gcb509f7-dirty", sha, nil, "v1.4.0-3-gcb509f7-dirty"},
		{"a build argument left at unknown", "unknown", "unknown", nil, "0.0.0-dev"},
		{"unknown version with a stamped commit", "unknown", sha, nil, "0.0.0-dev+cb509f7d1e2a"},
		{"a stamped dev version is still development", "1.3.0-dev", sha, nil, "0.0.0-dev+cb509f7d1e2a"},
		{"nothing stamped, a clean toolchain stamp", "", "", toolchain(sha, false), "0.0.0-dev+cb509f7d1e2a"},
		{"nothing stamped, uncommitted changes", "", "", toolchain(sha, true), "0.0.0-dev+cb509f7d1e2a.dirty"},
		{"a stamped revision wins over the toolchain's", "", "abcdef0", toolchain(sha, true), "0.0.0-dev+abcdef0"},
		{"a revision that is not a commit", "", "main; rm -rf /", nil, "0.0.0-dev"},
		{"nothing at all, as under go test", "", "", &debug.BuildInfo{}, "0.0.0-dev"},
		{"no build information", "", "", nil, "0.0.0-dev"},
	} {
		if got := resolve(tc.version, tc.revision, tc.info); got != tc.want {
			t.Errorf("%s: resolve = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestVersion_IsADevelopmentBuildUnderTest pins what this very test binary
// reports, unstamped: a development version, never a release, so nothing
// run from a test ever enforces a constraint by accident.
func TestVersion_IsADevelopmentBuildUnderTest(t *testing.T) {
	v := Version()
	if _, ok := Release(v); ok {
		t.Errorf("an unstamped test binary reports a release, %q", v)
	}
	if len(v) < len(developmentBase) || v[:len(developmentBase)] != developmentBase {
		t.Errorf("Version() = %q, want it to start with %q", v, developmentBase)
	}
}
