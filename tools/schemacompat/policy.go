// Package main: which release a branch is held to, and what may accept a
// breaking change.
//
// The base is the newest release tag at or below the tree's release line
// (buildinfo.CurrentRelease). A branch whose line is a later minor than the
// base (main, opening 0.4.0 from v0.3.0) may break a contract, but only when
// a `changelog/*.breaking.md` fragment names each break by its token. A
// branch whose line is the base's own minor (a 0.3.x branch from v0.3.0) is
// a patch line: point releases are always safe, so nothing accepts a break
// there.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// version is a release version's three numbers.
type version struct{ major, minor, patch int }

// releasePattern matches a release version with no pre-release suffix.
var releasePattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// parseVersion reads "0.4.0" or "v0.4.0"; a pre-release or other name is not
// a release.
func parseVersion(s string) (version, bool) {
	m := releasePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return version{}, false
	}
	var v version
	v.major, _ = strconv.Atoi(m[1])
	v.minor, _ = strconv.Atoi(m[2])
	v.patch, _ = strconv.Atoi(m[3])
	return v, true
}

// less orders versions.
func (v version) less(w version) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// sameMinor reports whether two versions are on one minor line.
func (v version) sameMinor(w version) bool { return v.major == w.major && v.minor == w.minor }

// String writes the version without a v.
func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

// baseTag picks the newest release tag at or below line, or "" when there
// is none.
func baseTag(tags []string, line version) string {
	var best string
	var bestV version
	for _, t := range tags {
		v, ok := parseVersion(t)
		if !ok || line.less(v) {
			continue
		}
		if best == "" || bestV.less(v) {
			best, bestV = t, v
		}
	}
	return best
}

// tokenPattern finds a breaking-change token in a fragment: a rule id, one
// space, and a schema file's pointer.
var tokenPattern = regexp.MustCompile("([a-z][a-z-]*[a-z]) ([A-Za-z0-9._-]+\\.json#[^\\s`'\"]*)")

// acceptedTokens reads every `*.breaking.md` fragment in dir and returns
// each token it names, with the fragment that named it.
func acceptedTokens(dir string) (map[string]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.breaking.md"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p) // #nosec G304 -- a changelog fragment of this repository
		if err != nil {
			return nil, err
		}
		for _, m := range tokenPattern.FindAllStringSubmatch(string(b), -1) {
			out[m[1]+" "+m[2]] = filepath.Base(p)
		}
	}
	return out, nil
}

// Verdict is the check's result for a whole tree.
type Verdict struct {
	// SchemaVersion is this document's own version.
	SchemaVersion int `json:"schemaVersion"`
	// Base is the release tag compared against.
	Base string `json:"base"`
	// Line is the release line the tree is on.
	Line string `json:"line"`
	// Patch reports a patch line, where nothing accepts a break.
	Patch bool `json:"patch"`
	// Findings are every breaking change found.
	Findings []Judged `json:"findings"`
	// Stale are tokens a fragment names that match no finding.
	Stale []string `json:"stale"`
	// Problems are files the check could not compare.
	Problems []string `json:"problems"`
	// Pass reports whether the tree passes.
	Pass bool `json:"pass"`
}

// Judged is one finding and what accepts it.
type Judged struct {
	Finding
	// AcceptedBy names the fragment that accepts it, or is empty.
	AcceptedBy string `json:"acceptedBy,omitempty"`
}

// judge applies the policy to the findings.
//
// A fragment token that matches no finding fails too: an acceptance that
// outlives its change is how an exemption list rots, and here it would also
// tell a reader of the release notes about a break that never shipped.
func judge(findings []Finding, accepted map[string]string, patch bool) ([]Judged, []string, bool) {
	pass := true
	used := map[string]bool{}
	out := make([]Judged, 0, len(findings))
	for _, f := range findings {
		j := Judged{Finding: f}
		if by, ok := accepted[f.Token()]; ok && !patch {
			j.AcceptedBy = by
			used[f.Token()] = true
		} else {
			pass = false
		}
		out = append(out, j)
	}
	var stale []string
	for tok := range accepted {
		if !used[tok] {
			stale = append(stale, tok)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 && !patch {
		pass = false
	}
	return out, stale, pass
}
