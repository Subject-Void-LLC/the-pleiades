// Package facts: the packages subset's probes (Phase 41c). Each lists every
// installed package from one package database in one fixed command, guarded
// so a device without that database prints nothing, and is parsed here into
// one fact per package: package.<name> = {version, arch, source}.
package facts

import (
	"strconv"
	"strings"
)

// The databases a package fact comes from, as its source names them.
const (
	sourceDpkg = "dpkg"
	sourceRPM  = "rpm"
	sourceAPK  = "apk"
)

// linuxPackageProbes are the packages subset's probes on Linux: the dpkg,
// rpm and apk databases, each read only where its tool exists. A device with
// two of them (rare) reports both; a name both hold is told apart by source
// (packageFacts).
func linuxPackageProbes() []gatherProbe {
	return []gatherProbe{
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: `command -v dpkg-query >/dev/null 2>&1 && dpkg-query -W -f='${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Architecture}\n'`,
			parse:   func(out string) []gatherFact { return packageFacts(parseDpkg(out)) },
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: `command -v rpm >/dev/null 2>&1 && rpm -qa --qf '%{NAME}\t%{EPOCH}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n'`,
			parse:   func(out string) []gatherFact { return packageFacts(parseRPM(out)) },
		},
		{
			subset:  gatherSubsetPackages,
			family:  gatherFamilyPackage,
			command: `command -v apk >/dev/null 2>&1 && apk list --installed 2>/dev/null`,
			parse:   func(out string) []gatherFact { return packageFacts(parseAPK(out)) },
		},
	}
}

// installedPackage is one package a database reports installed.
type installedPackage struct {
	name, version, arch, source string
}

// packageFacts turns pkgs into one fact each. A name already taken (rpm's
// multilib packages share one name across architectures) takes its
// architecture after a colon, as dpkg names a foreign one, a name still
// taken its source after an at sign, and one taken even so (two of
// Windows' Uninstall entries may share a display name) a number after a
// hash, counted in the order the source lists them, so no package
// overwrites another. A package its source gives no version has none.
func packageFacts(pkgs []installedPackage) []gatherFact {
	out := make([]gatherFact, 0, len(pkgs))
	taken := map[string]bool{}
	for _, p := range pkgs {
		name := p.name
		if taken[name] && p.arch != "" {
			name += ":" + p.arch
		}
		if taken[name] {
			name += "@" + p.source
		}
		for n, base := 2, name; taken[name]; n++ {
			name = base + "#" + strconv.Itoa(n)
		}
		taken[name] = true
		value := map[string]any{"source": p.source}
		if p.version != "" {
			value["version"] = p.version
		}
		if p.arch != "" {
			value["arch"] = p.arch
		}
		out = append(out, gatherFact{key: itemKey(gatherFamilyPackage, name), value: value})
	}
	return out
}

// parseDpkg reads dpkg-query's lines (status abbreviation, package, version,
// architecture), keeping only the installed ones ("ii").
func parseDpkg(out string) []installedPackage {
	var pkgs []installedPackage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !strings.HasPrefix(fields[0], "ii") || fields[1] == "" || fields[2] == "" {
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
