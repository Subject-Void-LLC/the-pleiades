// Package facts: the packages subset's probes (Phase 41c). Each lists every
// installed package from one package database in one fixed command, guarded
// so a device without that database prints nothing, and is parsed here into
// one fact per package: package.<name> = {version, arch, source}.
package facts

import (
	"strings"
)

// The databases a package fact comes from, as its source names them.
const (
	sourceDpkg = "dpkg"
	sourceRPM  = "rpm"
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
			command: orFailed(`dpkg-query -W -f='${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Architecture}\n'`),
			parse:   func(out string) []gatherFact { return packageFacts(parseDpkg(out)) },
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			present: `command -v rpm >/dev/null 2>&1`,
			command: orFailed(`rpm -qa --qf '%{NAME}\t%{EPOCH}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n'`),
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
		value := map[string]any{"source": p.source}
		if p.version != "" {
			value["version"] = p.version
		}
		if p.arch != "" {
			value["arch"] = p.arch
		}
		out = append(out, gatherFact{key: itemKey(gatherFamilyPackage, p.name), value: value})
	}
	return out
}

// dpkgInstalled are the status letters (the second of dpkg's abbreviation)
// of a package whose files are on the system: installed, unpacked,
// half-configured, and awaiting or pending triggers. Not installed (n),
// config files only (c) and half installed (H) are not. The first letter, what
// is wanted of it (install, hold, remove, purge), does not decide: a held
// package (hi) is installed.
const dpkgInstalled = "iUFWt"

// parseDpkg reads dpkg-query's lines (status abbreviation, package, version,
// architecture), keeping the installed ones (dpkgInstalled).
func parseDpkg(out string) []installedPackage {
	var pkgs []installedPackage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || len(fields[0]) < 2 || !strings.ContainsRune(dpkgInstalled, rune(fields[0][1])) || fields[1] == "" || fields[2] == "" {
			continue
		}
		pkgs = append(pkgs, installedPackage{name: fields[1], version: fields[2], arch: fields[3], source: sourceDpkg})
	}
	return pkgs
}

// parseRPM reads rpm's lines (name, epoch, version-release, architecture),
// leaving out the gpg-pubkey entries rpm lists as packages for its keys. An
// epoch prefixes the version as rpm writes it (2:1.0-1); "(none)" is none.
func parseRPM(out string) []installedPackage {
	var pkgs []installedPackage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || fields[0] == "" || fields[0] == "gpg-pubkey" || fields[2] == "" {
			continue
		}
		version := fields[2]
		if epoch := fields[1]; epoch != "" && epoch != "(none)" && epoch != "0" {
			version = epoch + ":" + version
		}
		arch := fields[3]
		if arch == "(none)" {
			arch = ""
		}
		pkgs = append(pkgs, installedPackage{name: fields[0], version: version, arch: arch, source: sourceRPM})
	}
	return pkgs
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
