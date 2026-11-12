// Package operand holds the rules for a value a Collection places on a
// device's command line as an operand: the name of one package, unit,
// service, account or group, or one field of a mount, written after the
// program's own options.
//
// # Why quoting is not enough
//
// pkg/remoteexec.QuoteArg makes a value one word that no shell interprets.
// It cannot stop the program that receives the word from reading it as
// something other than a name. Two kinds of word are read that way, each by
// its own set of programs:
//
//   - A word beginning with "-" is an option to every getopt-style program
//     this platform passes a name to: apt-get, apt-cache, dpkg-query, dnf,
//     rpm, systemctl, chown, chgrp, glibc's getent, useradd and the other
//     shadow tools, mount, umount, findmnt, rm and ln. Measured on Debian
//     bookworm: dpkg-query refuses "-oX" as an unknown option (exit 2),
//     which pkg.apt.install read as "not installed", and apt-get install -y
//     then took "-oDPkg::Pre-Invoke::=touch /tmp/pwned" as a configuration
//     option and exited 0 (FAILURE_PATTERNS 1174). GNU chown took an owner
//     of --reference=/etc/shadow as "give the file /etc/shadow's owner and
//     group" and exited 0 (Debian 13), and glibc's getent passwd -i listed
//     every account and exited 0, so the first record, root's, read as the
//     account named -i. PowerShell is the exception: a quoted value bound
//     to a named parameter such as -Name is never read as a parameter, so a
//     leading "-" is no option to the service cmdlets.
//   - A word holding "*", "?", "[" or "]" is a pattern to apt-get and
//     apt-cache (which also read "^", "$" and "+" as a regular expression
//     when no package has the exact name: apt-get install "jq++" planned
//     upgrades of dpkg and libc6 on Debian 12), to dnf, to systemctl
//     show, start, stop and restart, which match it against every loaded
//     unit, and to PowerShell's -Name on Get-Service, Start-Service,
//     Stop-Service and Restart-Service. Measured on Rocky Linux 9:
//     systemctl show "s*" prints the state of every loaded unit whose name
//     starts with s, and dnf remove "openssh*" plans the removal of both
//     openssh packages. chown, chgrp, getent, the shadow tools, rpm -q,
//     mount, umount and findmnt read those characters as themselves.
//
// The rule is applied to every operand all the same, whichever program
// receives it, for two reasons. A generic method (pkg.install, svc.start)
// hands a name to whichever program the device runs, and validation and
// the engine judge the value before anyone knows which that is; and a rule
// that differed by program would be one more place for the next method to
// pick the wrong one. What that costs is stated where it falls: an account
// or group whose name holds a pattern character, which useradd and
// groupadd make on Debian 12 and Ubuntu 22.04 and 24.04 (Debian 13, Arch,
// Alpine 3.22 and the RHEL 9 family refuse one), cannot be named, and a
// mount source or mountpoint holding one can, since mount reads it as
// itself (MountField).
//
// A "--" before the operands, where the program takes one, stops the first
// kind as well and is added beside the rule. It is not added where the
// program reads "--" itself as a word: musl's getent takes no options and
// reads "--" as a key, and dnf5 (Fedora 41 and later) refuses "--" as an
// unknown argument and exits 2, so for those the rule is all that stands
// between a name and the options. Nothing stops the second kind but
// refusing it.
//
// # Kinds
//
// Name is the rule above. The other kinds (kinds.go) are a program's own
// grammar for one parameter: SystemdUnit admits systemd's two root units,
// whose names begin with "-"; AptPackage, RPMPackage and Package hold a
// package name to the characters its package manager gives one, refusing
// the forms that select something other than the one package named (a
// release, a version, a group, a file); PackageVersion is the version a
// package manager joins onto that name; MountField is one field of a mount
// command and of its fstab line.
//
// # Where the rules are applied
//
// pkg/collection maps each kind to a parameter format (ParamFormatOperand
// and its siblings) and holds a parameter declaring one to its rule at
// validation (a literal), in the engine (a rendered value) and in the
// rollback planner (a journaled undo). Each method holds the value it reads
// to the same rule again through pkg/sdk before anything is sent, which
// covers a call that reached the method some other way (a generic
// dispatcher, a replayed undo).
package operand

import (
	"fmt"
	"strings"
)

// patternCharacters are the characters a program reading names expands as
// a pattern: the shell-style wildcards apt-get, dnf and systemctl match
// against their names, and PowerShell's wildcard set.
const patternCharacters = "*?[]"

// Fixed sentences each rule returns. None repeats the value, because the
// value may have been rendered from what a device returned, and what an
// author needs to fix it is the rule, not the text.
const (
	problemOption  = `it begins with "-", which the program would read as an option rather than as a name`
	problemPattern = `it holds "*", "?", "[" or "]", which the program would read as a pattern matching many names rather than as one name`
)

// Problem returns "" when text can only be read as the one name it holds,
// and otherwise a sentence saying why not. It is the Name kind's rule.
func Problem(text string) string {
	switch {
	case strings.HasPrefix(text, "-"):
		// The program's option parser sees this before any name lookup does.
		return problemOption
	case strings.ContainsAny(text, patternCharacters):
		// apt-get, dnf, systemctl and PowerShell's -Name each expand these.
		return problemPattern
	}
	return ""
}

// Check returns an error naming key when text fails Problem, and nil when it
// passes. It is the form a method calls on a value it is about to send.
func Check(key, text string) error {
	return Name.Check(key, text)
}

// Kind is one rule for an operand: the general one (Name), or a program's
// own grammar for one parameter (kinds.go).
type Kind string

// The kinds. Each is the value pkg/collection's matching parameter format
// is mapped to.
const (
	// Name is the general rule: no leading "-", no pattern character.
	Name Kind = "name"
	// SystemdUnit is a systemd unit: Name, except that systemd's own root
	// units, whose names begin with "-", are admitted exactly.
	SystemdUnit Kind = "systemd_unit"
	// AptPackage is a Debian package name as apt-get, apt-cache and
	// dpkg-query take it.
	AptPackage Kind = "apt_package"
	// RPMPackage is a package name as dnf and rpm take it.
	RPMPackage Kind = "rpm_package"
	// Package is a package name a generic method hands to whichever package
	// manager the device runs: one AptPackage or RPMPackage admits.
	Package Kind = "package"
	// PackageVersion is a version apt-get joins onto a name as name=version
	// and dnf as name-version.
	PackageVersion Kind = "package_version"
	// MountField is one field of a mount command and its fstab line: a
	// mountpoint, a source, a filesystem type or the options.
	MountField Kind = "mount_field"
)

// Problem returns "" when text passes k's rule, and otherwise a fixed
// sentence saying why not. An unknown kind refuses everything, so a kind
// added to one side and not the other fails closed.
func (k Kind) Problem(text string) string {
	switch k {
	case Name:
		return Problem(text)
	case SystemdUnit:
		return systemdUnitProblem(text)
	case AptPackage:
		return aptPackageProblem(text)
	case RPMPackage:
		return rpmPackageProblem(text)
	case Package:
		return packageProblem(text)
	case PackageVersion:
		return packageVersionProblem(text)
	case MountField:
		return mountFieldProblem(text)
	}
	return "this build knows no operand rule of that kind, so it cannot say the value is one name"
}

// Check returns an error naming key when text fails k's rule, and nil when
// it passes.
func (k Kind) Check(key, text string) error {
	if problem := k.Problem(text); problem != "" {
		return fmt.Errorf("%s is refused: %s", key, problem)
	}
	return nil
}

// Kinds lists every kind, for a caller that maps them (pkg/collection) and a
// test that holds the map to this list.
func Kinds() []Kind {
	return []Kind{Name, SystemdUnit, AptPackage, RPMPackage, Package, PackageVersion, MountField}
}
