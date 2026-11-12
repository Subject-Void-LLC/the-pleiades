// Package operand: each program's own grammar for one kind of operand, on
// top of the general rule (Problem).
package operand

import (
	"strings"
	"unicode"
)

// systemdRootUnits are the only systemd units whose names begin with "-":
// the root mount (the mount unit for "/") and the root slice, both of which
// systemd.special(7) names and every systemd host loads. "-" is systemd's
// escape of the path "/" (systemd.unit(5)), so a name beginning with it
// names the root of a hierarchy, and those two are the roots there are. A
// loop over a host's loaded units meets both, and systemctl, which always
// gets "--" before a unit, reads each as the unit it names.
var systemdRootUnits = map[string]bool{"-.mount": true, "-.slice": true}

// systemdUnitProblem is the SystemdUnit rule: the general rule, except that
// a systemd root unit's exact name passes. Nothing else beginning with "-"
// does, and no pattern does: systemctl matches one against every loaded unit.
func systemdUnitProblem(text string) string {
	if systemdRootUnits[text] {
		return ""
	}
	return Problem(text)
}

// aptPackageProblem is the AptPackage rule: a Debian package name as dpkg
// defines one (a letter or digit, then letters, digits, "+", ".", "-" and
// "_"), with an optional ":architecture", and no form apt-get reads as
// something other than that one package. Each refused form is one apt-get
// takes (measured on Debian 12, FAILURE_PATTERNS 1174): "name/release"
// installs from that release and "name=version" that version, under a word
// dpkg-query reports absent before and after, so the undo recorded by the
// word removes nothing; a trailing "-" removes the package; and "~", "?"
// and "^" start a search pattern or a regular expression matching many
// packages.
func aptPackageProblem(text string) string {
	if problem := Problem(text); problem != "" || text == "" {
		// A dash or a pattern says so in the general rule's own words, and
		// an empty name is the required check's concern.
		return problem
	}
	name, arch, qualified := strings.Cut(text, ":")
	switch {
	case strings.Contains(text, "/"):
		return `it holds "/", which apt-get reads as name/release, installing the package under a word dpkg never records, so the undo recorded by that word would remove nothing`
	case strings.Contains(text, "="):
		return `it holds "=", which apt-get reads as name=version, under a word dpkg never records; give the version in its own parameter`
	case strings.HasSuffix(name, "-") || strings.HasSuffix(text, "-"):
		return `it ends in "-", which apt-get install reads as "remove the package before the dash" rather than as a name`
	case name == "" || !isASCIILetterOrDigit(rune(name[0])):
		return `it does not begin with a letter or a digit, as every Debian package name does, and apt-get reads a word beginning with "~" or "^" as a search pattern or a regular expression matching many packages`
	case !only(name, isDebianNameCharacter):
		return `it holds a character no Debian package name holds (only letters, digits, "+", ".", "-" and "_", then an optional ":architecture"), which apt-get could read as a pattern or a regular expression matching many packages`
	case qualified && (arch == "" || !only(arch, isArchitectureCharacter)):
		return `it holds a ":" that is not followed by an architecture name (letters, digits and "-" only)`
	}
	return ""
}

// rpmPackageProblem is the RPMPackage rule: a package name as rpm builds
// one (letters, digits, ".", "_", "+" and "-"), and no form dnf reads as
// something other than one package. Each refused form is one dnf installs
// by and rpm -q never finds by that word (measured on Rocky Linux 9 and
// Fedora 42: rpm -q reports "@core", "/usr/bin/jq" and "perl(strict)" not
// installed after dnf installs what each names), so the undo recorded by
// the word would remove nothing.
func rpmPackageProblem(text string) string {
	if problem := Problem(text); problem != "" || text == "" {
		return problem
	}
	switch {
	case strings.HasPrefix(text, "@"):
		return `it begins with "@", which dnf reads as a group or a module of many packages rather than as one package`
	case strings.Contains(text, "/"):
		return `it holds "/", which dnf reads as a file and installs whatever package provides it, under a word rpm never records`
	case strings.ContainsAny(text, "()"):
		return `it holds "(" or ")", which dnf reads as a capability or a rich dependency and installs whatever package provides it, under a word rpm never records`
	case strings.ContainsAny(text, "<>= \t\n"):
		return `it holds a space or a comparison, which dnf reads as a version constraint on another package; give the version in its own parameter`
	case strings.Contains(text, ":"):
		return `it holds ":", which dnf reads as the epoch of a version; give the version in its own parameter`
	case strings.HasSuffix(text, "-"):
		return `it ends in "-", which no RPM package name does`
	case !only(text, isRPMNameCharacter):
		return `it holds a character no RPM package name holds (only letters, digits, ".", "_", "+" and "-")`
	}
	return ""
}

// packageProblem is the Package rule, for a generic method that hands the
// name to whichever package manager the device runs: a name either
// AptPackage or RPMPackage admits. The concrete method holds the name to its
// own manager's rule again before it connects, so a name only the other
// manager admits (an RPM name beginning with "_", a Debian
// ":architecture") is refused there. A name both refuse is refused here,
// with both reasons when they differ.
func packageProblem(text string) string {
	apt, rpm := aptPackageProblem(text), rpmPackageProblem(text)
	switch {
	case apt == "" || rpm == "":
		return ""
	case apt == rpm:
		return apt
	}
	return "for APT, " + apt + "; and for RPM, " + rpm
}

// packageVersionProblem is the PackageVersion rule: a version as Debian and
// RPM write one (letters, digits, ".", "+", "~", "^", "_", ":" and "-"),
// beginning with neither "-" nor, as nothing does, anything apt-get or dnf
// would read as a pattern, and not ending in "-". The version is joined onto
// the name as one word, name=version for apt-get and name-version for dnf,
// so a pattern in it matches many versions or many packages (dnf install
// "nginx-*" installs every nginx- package), and apt-get reads name=version-
// as "remove the package" (measured on Debian 12: "Remv diffutils").
func packageVersionProblem(text string) string {
	switch {
	case text == "":
		// No version is "whatever is current", the parameter's own default.
		return ""
	case strings.HasPrefix(text, "-"):
		return `it begins with "-", which no package version does`
	case strings.ContainsAny(text, patternCharacters):
		return `it holds "*", "?", "[" or "]", which apt-get and dnf would read, joined onto the name, as a pattern matching many versions or many packages`
	case strings.HasSuffix(text, "-"):
		return `it ends in "-", which apt-get reads at the end of name=version as "remove the package"`
	case !only(text, isVersionCharacter):
		return `it holds a character no Debian or RPM version holds (only letters, digits, ".", "+", "~", "^", "_", ":" and "-")`
	}
	return ""
}

// mountFieldProblem is the MountField rule: one field of a mount command
// and of the fstab line it may write. A leading "-" is an option to mount,
// umount and findmnt; a leading "#" makes the fstab line a comment; a space,
// a tab, a line break or another control character ends an fstab field or
// line, so the entry written would hold fields or lines the task never
// named; and "\" starts one of fstab's octal escapes, so the entry would
// name another source or mountpoint at boot than the one mounted now. The
// pattern characters pass: mount, umount and findmnt read them as
// themselves, and a path may hold them.
func mountFieldProblem(text string) string {
	switch {
	case strings.HasPrefix(text, "-"):
		return `it begins with "-", which mount, umount and findmnt would read as an option rather than as a path or a name`
	case strings.HasPrefix(text, "#"):
		return `it begins with "#", which makes its fstab line a comment`
	case strings.IndexFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return `it holds a space, a tab, a line break or another control character, which fstab reads as the end of a field or of a line, so the entry written would hold fields or lines the task never named`
	case strings.Contains(text, `\`):
		return `it holds "\", which fstab reads as the start of an escape, so the entry would name another path at boot than the one mounted now`
	}
	return ""
}

// only reports whether every rune of text satisfies allowed.
func only(text string, allowed func(rune) bool) bool {
	for _, r := range text {
		if !allowed(r) {
			return false
		}
	}
	return true
}

// isASCIILetterOrDigit reports whether r is an ASCII letter or digit, the
// only characters a Debian package name may begin with.
func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// isDebianNameCharacter reports whether r may appear in a Debian package
// name: dpkg's own set (pkg_name_is_illegal), letters, digits and "-+._".
func isDebianNameCharacter(r rune) bool {
	return isASCIILetterOrDigit(r) || strings.ContainsRune("-+._", r)
}

// isArchitectureCharacter reports whether r may appear in a Debian
// architecture name (amd64, arm64, kfreebsd-amd64, any).
func isArchitectureCharacter(r rune) bool {
	return isASCIILetterOrDigit(r) || r == '-'
}

// isRPMNameCharacter reports whether r may appear in an RPM package name.
func isRPMNameCharacter(r rune) bool {
	return isASCIILetterOrDigit(r) || strings.ContainsRune("._+-", r)
}

// isVersionCharacter reports whether r may appear in a Debian or an RPM
// version: Debian's ".+~-:" and RPM's "._+~^", with ":" for an epoch.
func isVersionCharacter(r rune) bool {
	return isASCIILetterOrDigit(r) || strings.ContainsRune(".+~^_:-", r)
}
