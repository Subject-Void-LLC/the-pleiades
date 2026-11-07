// Package facts: the distribution facts. A Linux device's /etc/os-release
// (and a Debian device's /etc/debian_version) is read into os.distribution
// (the os-release ID), os.family, os.version, os.version_major and
// os.codename, each a native value: the ID as the file writes it, a
// lowercase family, an integer major version. Ansible's names and words for
// the same facts are pkg/ansiblefacts' to give (its ToAnsible), and
// internal/catalog/facts/live holds the two together in each distribution's
// own container: these facts, mapped, are what ansible.builtin.setup printed
// there.
package facts

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// gatherFactFamily is the family a distribution belongs to (debian, redhat,
// suse), the fact a condition choosing a package manager reads most.
const gatherFactFamily = "os.family"

// debianVersionMark separates /etc/os-release from /etc/debian_version in the
// min probe's answer, so a device without the second file cannot shift its
// lines into the first.
const debianVersionMark = "--pleiades-debian-version--"

// osReleaseCommand reads /etc/os-release and, where it exists,
// /etc/debian_version, which holds the Debian point release os-release does
// not. Both are plain cat, which a BusyBox device has.
const osReleaseCommand = "cat /etc/os-release; echo '" + debianVersionMark + "'; cat /etc/debian_version 2>/dev/null"

// familyByID is the native family of each os-release ID measured, and of
// FreeBSD, whose os-release names it too.
var familyByID = map[string]string{
	"debian":              "debian",
	"ubuntu":              "debian",
	"kali":                "debian",
	"devuan":              "debian",
	"rhel":                "redhat",
	"centos":              "redhat",
	"rocky":               "redhat",
	"almalinux":           "redhat",
	"ol":                  "redhat",
	"fedora":              "redhat",
	"amzn":                "redhat",
	"opensuse-leap":       "suse",
	"opensuse-tumbleweed": "suse",
	"opensuse":            "suse",
	"sles":                "suse",
	"sled":                "suse",
	"sles_sap":            "suse",
	"arch":                "arch",
	"alpine":              "alpine",
	"freebsd":             "freebsd",
}

// suseFamily is the native family whose version is cut to major.minor.
const suseFamily = "suse"

// versionCodename is a codename in os-release's VERSION: a parenthesized run
// holding no digit ("9.6 (Blue Onyx)"), or what follows a comma.
var versionCodename = regexp.MustCompile(`\((\D+)\)|,\s*(\D+)`)

// suseVersion is a SUSE version's major and optional minor part at the start
// of VERSION_ID.
var suseVersion = regexp.MustCompile(`^([0-9]+)\.?([0-9]*)`)

// gatherOSRelease reads the min probe's answer (os-release, the mark, and
// /etc/debian_version) into the distribution facts. A value the files do not
// give is left out, as every fact this method cannot determine is.
func gatherOSRelease(stdout string) []gatherFact {
	osRelease, debianVersion, _ := strings.Cut(stdout, debianVersionMark)
	values := gatherKeyValues(osRelease)
	id := strings.ToLower(values["ID"])
	family, known := familyByID[id]
	if !known {
		// An ID this method has not measured finds its family only where
		// Ansible's own family table lists its NAME: a family is never
		// guessed (ID_LIKE says what a distribution resembles, not what
		// Ansible or a runbook written for it calls it).
		family, known = ansiblefacts.FamilyOfName(values["NAME"])
	}
	// Its first line only, so a second line in the file never reaches the
	// version.
	firstLine, _, _ := strings.Cut(strings.TrimLeft(debianVersion, "\n"), "\n")
	version := distributionVersion(id, family, values, strings.TrimSpace(firstLine))

	var facts []gatherFact
	if id != "" {
		facts = append(facts, gatherFact{key: gatherFactDistribution, value: id})
	}
	if known {
		facts = append(facts, gatherFact{key: gatherFactFamily, value: family})
	}
	facts = append(facts, versionFacts(version)...)
	if codename := distributionCodename(values); codename != "" {
		facts = append(facts, gatherFact{key: gatherFactCodename, value: codename})
	}
	return facts
}

// versionFacts is version as os.version and, when the text before its first
// dot is a whole number, os.version_major as that integer; Debian testing's
// forky/sid has a version and no major one. An empty version is neither.
func versionFacts(version string) []gatherFact {
	if version == "" {
		return nil
	}
	facts := []gatherFact{{key: gatherFactVersion, value: version}}
	if major, ok := versionMajor(version); ok {
		facts = append(facts, gatherFact{key: gatherFactVersionMajor, value: major})
	}
	return facts
}

// versionMajor is the whole number before version's first dot, and whether
// there is one: digits only, so 15-SP6 and forky/sid have none.
func versionMajor(version string) (int, bool) {
	head, _, _ := strings.Cut(version, ".")
	if head == "" || strings.Trim(head, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(head)
	return n, err == nil
}

// distributionVersion is the distribution version: VERSION_ID, but on Debian
// the more precise of VERSION_ID and /etc/debian_version (the first present,
// replaced by a later one with more dots), so 12 with a debian_version of
// 12.12 is 12.12, and testing, which has no VERSION_ID, is what
// debian_version says (forky/sid). On the SUSE family it is VERSION_ID's
// leading major.minor.
func distributionVersion(id, family string, values map[string]string, debianVersion string) string {
	version := values["VERSION_ID"]
	switch {
	case id == "debian" && debianVersion != "":
		if version == "" || strings.Count(debianVersion, ".") > strings.Count(version, ".") {
			version = debianVersion
		}
	case family == suseFamily:
		if m := suseVersion.FindString(version); m != "" {
			version = m
		}
	}
	return version
}

// distributionCodename is the codename os-release names: VERSION_CODENAME
// when the file has the key (even empty, which Fedora's is, and which names
// none), then UBUNTU_CODENAME, then VERSION's parenthesized name (Rocky's
// Blue Onyx, Red Hat's Plow, which name theirs nowhere else). Ansible's
// other words for a release (Stream on CentOS Stream, a SUSE minor version)
// are not codenames and are pkg/ansiblefacts' to give.
func distributionCodename(values map[string]string) string {
	if codename, ok := values["VERSION_CODENAME"]; ok {
		return codename
	}
	if codename, ok := values["UBUNTU_CODENAME"]; ok {
		return codename
	}
	if m := versionCodename.FindStringSubmatch(values["VERSION"]); m != nil {
		return strings.TrimSpace(m[1] + m[2])
	}
	return ""
}
