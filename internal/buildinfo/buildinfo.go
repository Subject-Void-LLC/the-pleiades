// Package buildinfo is the one version every Pleiades binary reports, and
// the one rule for whether that version is a release.
//
// A build is stamped at link time with two variables:
//
//	go build -ldflags "-X github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo.version=1.2.0 \
//	    -X github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo.revision=<commit>" ./cmd/pleiades
//
// A stamped version is used only when it reads as a release (Release), so
// a build argument left at "unknown" or "dev" cannot pass for one. Every
// other build is a development build and reports 0.0.0-dev+<commit>: the
// stamped revision, or the version control stamp the Go toolchain embeds
// (with .dirty for uncommitted changes), or plain 0.0.0-dev when there is
// neither, as under go run and go test. The CLI, the Runner and the
// scaffold all read Version, so they cannot disagree about which build
// they are.
package buildinfo

import (
	"runtime/debug"
	"strconv"
	"strings"
)

// version and revision are stamped by -ldflags -X, and are empty on a
// build nobody stamped. See the package documentation for how each is
// used.
var (
	version  string
	revision string
)

// developmentBase is the version of every build nobody stamped.
const developmentBase = "0.0.0-dev"

// CurrentRelease is the release line this source tree is on: the version a
// release build cut from it would carry, and therefore the newest engine a
// method shipped inside it may require.
//
// It is a declared constant rather than something derived, because nothing
// in a source tree knows which release it belongs to. Move it in the same
// change that opens the next release line, and never above the release the
// roadmap is actually working toward: a constraint naming a release this
// build does not meet is refused by the loader (see the engine version
// constraint in internal/loader), so a value set ahead of the work makes
// every method carrying it unloadable on the very first real release.
const CurrentRelease = "0.2.0"

// Version is this build's version: the stamped one, or a development
// version. See the package documentation.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	return resolve(version, revision, info)
}

// resolve is Version over its inputs: the stamped version and revision,
// and the toolchain's build information (nil when there is none).
func resolve(stampedVersion, stampedRevision string, info *debug.BuildInfo) string {
	if _, ok := Release(stampedVersion); ok {
		return stampedVersion
	}
	rev, modified := stampedRevision, false
	if !isRevision(rev) {
		rev = ""
		if info != nil {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					modified = s.Value == "true"
				}
			}
		}
	}
	if !isRevision(rev) {
		return developmentBase
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified {
		rev += ".dirty"
	}
	return developmentBase + "+" + rev
}

// isRevision reports whether s looks like a commit hash: at least seven
// lowercase hexadecimal digits. Anything else (a build argument left at
// "unknown", a branch name) is not stamped into a version.
func isRevision(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Release reports whether v names a release, and which: MAJOR.MINOR.PATCH
// with an optional leading "v", optionally followed by a prerelease such
// as -rc1 and build metadata. A release candidate is a release here,
// since it is built to be run by somebody. A version whose prerelease
// starts with "dev", such as 0.0.0-dev+abc or 1.3.0-dev, is not: it is a
// development build that happens to carry numbers. Neither is anything
// that is not a version at all ("dev", empty).
//
// A git describe of a commit after a tag (v1.2.0-3-gabc1234) reads as that
// tag's release. That is the conservative reading for an engine version
// constraint: the build has everything the tag had, and a constraint
// naming a later release still refuses it.
//
// The release it returns has any prerelease and build metadata removed,
// which is the reading an engine version constraint is compared against.
func Release(v string) (release string, ok bool) {
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, prerelease, _ := strings.Cut(core, "-")
	if strings.HasPrefix(prerelease, "dev") {
		return "", false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return "", false
		}
		if _, err := strconv.ParseUint(p, 10, 64); err != nil {
			return "", false
		}
	}
	return core, true
}
