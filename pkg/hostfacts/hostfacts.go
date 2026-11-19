// Package hostfacts is what a host's package facts are, shared by the method
// that gathers them (facts.gather's packages subset) and the methods that
// change a package and read it back after (pkg.apt.*, pkg.dnf.*; Phase 41c's
// post-change reading). Both must store the same fact under the same key, or
// the next gather would read as drift: so the query format, the parser, the
// item key and the value are written once, here.
package hostfacts

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PackageFamily is the package facts' family: package.<name> = {version, arch,
// source}.
const PackageFamily = "package."

// The databases a package fact names as its source.
const (
	SourceDpkg = "dpkg"
	SourceRPM  = "rpm"
)

// DpkgFormat is the dpkg-query format both sides read: status abbreviation,
// binary package (name:arch for a multi-arch package), version and
// architecture, tab-separated, one package a line.
const DpkgFormat = `${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Architecture}\n`

// RPMFormat is rpm's query format both sides read: name, epoch,
// version-release and architecture, tab-separated, one package a line.
const RPMFormat = `%{NAME}\t%{EPOCH}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n`

// Package is one package a database reports installed.
type Package struct {
	Name, Version, Arch, Source string
}

// dpkgInstalled are the status letters (the second of dpkg's abbreviation) of
// a package whose files are on the system: installed, unpacked,
// half-configured, and awaiting or pending triggers. Not installed (n), config
// files only (c) and half installed (H) are not. The first letter, what is
// wanted of it, does not decide: a held package (hi) is installed.
const dpkgInstalled = "iUFWt"

// ParseDpkg reads dpkg-query's lines in DpkgFormat, keeping the installed ones.
func ParseDpkg(out string) []Package {
	var pkgs []Package
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || len(fields[0]) < 2 || !strings.ContainsRune(dpkgInstalled, rune(fields[0][1])) || fields[1] == "" || fields[2] == "" {
			continue
		}
		pkgs = append(pkgs, Package{Name: fields[1], Version: fields[2], Arch: fields[3], Source: SourceDpkg})
	}
	return pkgs
}

// ParseRPM reads rpm's lines in RPMFormat, leaving out the gpg-pubkey entries
// rpm lists as packages for its keys. An epoch prefixes the version as rpm
// writes it (2:1.0-1); "(none)" is none.
func ParseRPM(out string) []Package {
	var pkgs []Package
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
		pkgs = append(pkgs, Package{Name: fields[0], Version: version, Arch: arch, Source: SourceRPM})
	}
	return pkgs
}

// PackageValue is p's fact value: its source, and its version and arch where
// the database gives them.
func PackageValue(p Package) map[string]any {
	value := map[string]any{"source": p.Source}
	if p.Version != "" {
		value["version"] = p.Version
	}
	if p.Arch != "" {
		value["arch"] = p.Arch
	}
	return value
}

// PackageKey is p's fact key, its name encoded as one item of the family.
func PackageKey(p Package) string { return ItemKey(PackageFamily, p.Name) }

// ItemKey is family followed by name, with every byte that cannot stand in a
// fact key (a slash, a percent sign, whitespace, an unprintable or invalid
// rune) written as %XX, every byte of a multi-byte rune, so the key is exact
// and reversible.
func ItemKey(family, name string) string {
	var b strings.Builder
	b.WriteString(family)
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		raw := name[i : i+size]
		i += size
		if (r == utf8.RuneError && size == 1) || r == '/' || r == '%' || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			for _, c := range []byte(raw) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
			continue
		}
		b.WriteString(raw)
	}
	return b.String()
}
