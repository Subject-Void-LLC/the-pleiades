// Package facts: the packages subset's probes (Phase 41c). Each lists every
// installed package from one package database in one fixed command, guarded
// so a device without that database prints nothing, and is parsed here into
// one fact per package: package.<name> = {version, arch, source}.
package facts

import (
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/hostfacts"
)

// The databases a package fact comes from, as its source names them.
const (
	sourceDpkg = hostfacts.SourceDpkg
	sourceRPM  = hostfacts.SourceRPM
	sourceAPK  = "apk"
)

// linuxPackageProbes are the packages subset's probes on Linux: the dpkg,
// rpm and apk databases, each read only where its tool exists, and a listing
// that fails failing the family (gather_outcome.go). A device with
// two of them (rare) reports both; a name both hold is told apart where
// Gather emits it (gatherNames).
func linuxPackageProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			present: `command -v dpkg-query >/dev/null 2>&1`,
			command: orFailed(`dpkg-query -W -f='` + hostfacts.DpkgFormat + `'`),
			parse:   func(out string) []gatherFact { return packageFacts(parseDpkg(out)) },
		},
		{
			subset: gatherSubsetPackages,
			family: gatherFamilyPackage,
			// The database, not just the tool: Debian and Ubuntu can carry an
			// rpm with no database of its own, whose failing listing would
			// fail the family dpkg answered (FAILURE_PATTERNS 925).
			present: `command -v rpm >/dev/null 2>&1 && d=$(rpm --eval '%_dbpath' 2>/dev/null) && [ -d "$d" ]`,
			command: orFailed(`rpm -qa --qf '` + hostfacts.RPMFormat + `'`),
			parse:   func(out string) []gatherFact { return packageFacts(parseRPM(out)) },
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			present: `command -v apk >/dev/null 2>&1`,
			command: orFailed(`apk list --installed 2>/dev/null`),
			parse:   func(out string) []gatherFact { return packageFacts(parseAPK(out)) },
		},
	}
}

// installedPackage is one package a database reports installed.
type installedPackage struct {
	name, version, arch, source string
}

// packageFacts turns pkgs into one fact each, keyed by name; a name another
// package already took is told apart where Gather emits it (gatherNames). A
// package its source gives no version has none.
func packageFacts(pkgs []installedPackage) []gatherFact {
	out := make([]gatherFact, 0, len(pkgs))
	for _, p := range pkgs {
		hp := hostfacts.Package{Name: p.name, Version: p.version, Arch: p.arch, Source: p.source}
		out = append(out, gatherFact{key: hostfacts.PackageKey(hp), value: hostfacts.PackageValue(hp)})
	}
	return out
}

// parseDpkg reads dpkg-query's lines (status abbreviation, package, version,
// architecture), keeping the installed ones (dpkgInstalled).
func parseDpkg(out string) []installedPackage { return fromHostfacts(hostfacts.ParseDpkg(out)) }

// parseRPM reads rpm's lines (name, epoch, version-release, architecture),
// leaving out the gpg-pubkey entries rpm lists as packages for its keys. An
// epoch prefixes the version as rpm writes it (2:1.0-1); "(none)" is none.
func parseRPM(out string) []installedPackage { return fromHostfacts(hostfacts.ParseRPM(out)) }

// fromHostfacts is pkg/hostfacts' packages as this file's own type, which apk's
// parser shares.
func fromHostfacts(pkgs []hostfacts.Package) []installedPackage {
	out := make([]installedPackage, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, installedPackage{name: p.Name, version: p.Version, arch: p.Arch, source: p.Source})
	}
	return out
}

// parseAPK reads apk's installed list ("name-1.2.3-r0 x86_64 {origin}
// (license) [installed]"). An apk version holds no hyphen, so the name is
// everything before the last two hyphen-separated fields (version and
// release).
func parseAPK(out string) []installedPackage {
	var pkgs []installedPackage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, "[installed") {
			continue
		}
		parts := strings.Split(fields[0], "-")
		if len(parts) < 3 || !strings.HasPrefix(parts[len(parts)-1], "r") {
			continue
		}
		name := strings.Join(parts[:len(parts)-2], "-")
		version := parts[len(parts)-2] + "-" + parts[len(parts)-1]
		if name == "" {
			continue
		}
		pkgs = append(pkgs, installedPackage{name: name, version: version, arch: fields[1], source: sourceAPK})
	}
	return pkgs
}
