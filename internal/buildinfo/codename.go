// Release codenames: each release has a name beside its number, such as
// "Paranoid Porpoise". The number stays the identity: the tag, the archive
// names, the checksum list, the engine-version constraints and every parser
// read it. The name is a label for people, shown in the release title and on
// the second line of a release build's version command, and nowhere else.
package buildinfo

import "strings"

// Codenames maps a release (MAJOR.MINOR.PATCH, no "v", no prerelease) to its
// name. The rule: one adjective and one animal, both starting with the same
// letter, and the letter moves forward with each named release. A release is
// named here in the change that prepares it, and a number with no entry has no
// name; the first release to be built, 0.3.0, is the first named.
var Codenames = map[string]string{
	"0.3.0": "Paranoid Porpoise",
}

// CodenameFor returns the name of the release v names, "" when it has none or
// when v is not a release (a development build). A prerelease such as 0.3.0-rc1
// carries its release's name.
func CodenameFor(v string) string {
	release, ok := Release(v)
	if !ok {
		return ""
	}
	return Codenames[release]
}

// Codename returns this build's codename: the name of its release, or "" for a
// development build.
func Codename() string { return CodenameFor(Version()) }

// validCodename reports whether name follows the rule: two words, the same
// first letter, nothing else.
func validCodename(name string) bool {
	words := strings.Fields(name)
	if len(words) != 2 || strings.Join(words, " ") != name {
		return false
	}
	a, b := strings.ToLower(words[0]), strings.ToLower(words[1])
	return a[0] == b[0] && a[0] >= 'a' && a[0] <= 'z'
}
