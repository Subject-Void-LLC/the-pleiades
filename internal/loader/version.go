// Package loader: the engine version constraint a method may declare,
// and how it is checked against the running build.
package loader

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// version is a MAJOR.MINOR.PATCH release number.
type version struct {
	major, minor, patch uint64
}

// String renders v as MAJOR.MINOR.PATCH.
func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// less reports whether v is an older release than w.
func (v version) less(w version) bool {
	if v.major != w.major {
		return v.major < w.major
	}
	if v.minor != w.minor {
		return v.minor < w.minor
	}
	return v.patch < w.patch
}

// parseRelease parses exactly MAJOR.MINOR.PATCH, with an optional leading
// "v". Each part is a plain decimal number with no sign and no leading
// zero, so "1.02.3" is not a version, the way semantic versioning says.
func parseRelease(s string) (version, bool) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var nums [3]uint64
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return version{}, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return version{}, false
		}
		nums[i] = n
	}
	return version{major: nums[0], minor: nums[1], patch: nums[2]}, true
}

// parseConstraint parses a method's engine version constraint.
//
// The grammar is deliberately tiny: empty, meaning no constraint, or
// ">=MAJOR.MINOR.PATCH" with an optional leading "v" and optional spaces
// after the operator. It returns ok false for an empty constraint and an
// error for anything else, including every other operator. A range
// language would be easy to add and hard to take back, and the one
// question a Collection author needs answered today is "is this engine
// new enough", which a minimum answers.
func parseConstraint(c string) (minimum version, ok bool, err error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return version{}, false, nil
	}
	rest, found := strings.CutPrefix(c, ">=")
	if !found {
		return version{}, false, fmt.Errorf("unsupported engine version constraint %q: the only form accepted is >=MAJOR.MINOR.PATCH", c)
	}
	v, parsed := parseRelease(strings.TrimSpace(rest))
	if !parsed {
		return version{}, false, fmt.Errorf("unsupported engine version constraint %q: the only form accepted is >=MAJOR.MINOR.PATCH", c)
	}
	return v, true, nil
}

// checkEngineVersion decides whether a method constrained by constraint
// may load on a build reporting running.
//
// A malformed constraint is refused on every build, since that is
// knowable without a version. On a release build (buildinfo.Release), a
// constraint the build does not meet is refused, naming both versions.
// On a development build it cannot be meaningfully compared (every
// development build reports 0.0.0), so the method loads and unchecked is
// set, for Load to warn once per program. A release candidate is compared
// as the release it is for, so 1.0.0-rc1 meets >=1.0.0: the grammar has no
// way to name a prerelease, and strict ordering would lock a release
// candidate out of every method aimed at its own release.
func checkEngineVersion(constraint, running string) (unchecked bool, err error) {
	minimum, constrained, err := parseConstraint(constraint)
	if err != nil {
		return false, err
	}
	if !constrained {
		return false, nil
	}
	release, ok := buildinfo.Release(running)
	if !ok {
		return true, nil
	}
	current, ok := parseRelease(release)
	if !ok {
		// Release accepted what parseRelease refuses: the two disagree,
		// and refusing is the answer that cannot load a method a release
		// build should have kept out.
		return false, fmt.Errorf("this build's version %q could not be compared", running)
	}
	if current.less(minimum) {
		return false, fmt.Errorf("requires engine >=%s, and this build is %s", minimum, current)
	}
	return false, nil
}
