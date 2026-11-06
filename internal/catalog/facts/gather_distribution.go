// Package facts: the distribution facts as Ansible names them. A Linux
// device's /etc/os-release (and a Debian device's /etc/debian_version) is
// read into ansible_distribution, its version, major version and release, and
// ansible_os_family, each held to the value ansible.builtin.setup gives on the
// same machine (internal/catalog/facts/live measures that in each
// distribution's own container). Ansible reaches those values through
// module_utils/facts/system/distribution.py and the distro library it
// vendors; the rules here are the parts of both that decide a value on the
// distributions in ansibleDistributionByID, and a distribution not in that
// table reports what Ansible's own fallback reports: NAME and VERSION_ID.
package facts

import (
	"regexp"
	"strings"
)

// gatherFactOSFamily is ansible.builtin.setup's name for the family a
// distribution belongs to (RedHat, Debian, Suse), the fact a converted
// playbook's conditions read most.
const gatherFactOSFamily = "ansible_os_family"

// debianVersionMark separates /etc/os-release from /etc/debian_version in the
// min probe's answer, so a device without the second file cannot shift its
// lines into the first.
const debianVersionMark = "--pleiades-debian-version--"

// osReleaseCommand reads /etc/os-release and, where it exists,
// /etc/debian_version, which holds the Debian point release os-release does
// not (Ansible reads it through distro's best version). Both are plain cat,
// which a BusyBox device has.
const osReleaseCommand = "cat /etc/os-release; echo '" + debianVersionMark + "'; cat /etc/debian_version 2>/dev/null"

// ansibleDistributionByID is Ansible's distribution name for an os-release ID,
// for each distribution measured against ansible.builtin.setup. Ansible gets
// most of these from another file (Rocky, AlmaLinux and Fedora from the first
// word of /etc/redhat-release, Archlinux from /etc/arch-release existing), and
// the ID names the same thing on every release of each. The SUSE family is not
// here: Ansible reports its NAME, as the fallback does.
var ansibleDistributionByID = map[string]string{
	"debian":    "Debian",
	"ubuntu":    "Ubuntu",
	"rhel":      "RedHat",
	"centos":    "CentOS",
	"rocky":     "Rocky",
	"almalinux": "AlmaLinux",
	"ol":        "OracleLinux",
	"fedora":    "Fedora",
	"amzn":      "Amazon",
	"alpine":    "Alpine",
	"arch":      "Archlinux",
	"freebsd":   "FreeBSD",
}

// suseIDs are the os-release IDs Ansible reads through its SUSE parser, and
// whether each is an enterprise release (whose release is "0" when VERSION_ID
// has no minor part, where openSUSE's is empty).
var suseIDs = map[string]bool{
	"opensuse-leap":       false,
	"opensuse-tumbleweed": false,
	"opensuse":            false,
	"sles":                true,
	"sled":                true,
	"sles_sap":            true,
}

// ansibleOSFamilyMap is Ansible's OS_FAMILY_MAP (distribution.py), family to
// the distribution names in it, copied whole so a distribution that reports
// its NAME still finds its family when Ansible lists that name.
var ansibleOSFamilyMap = map[string][]string{
	"RedHat": {"RedHat", "RHEL", "Fedora", "CentOS", "Scientific", "SLC", "Ascendos", "CloudLinux", "PSBM", "OracleLinux", "OVS",
		"OEL", "Amazon", "Amzn", "Virtuozzo", "XenServer", "Alibaba", "EulerOS", "openEuler", "AlmaLinux", "Rocky", "TencentOS",
		"EuroLinux", "Kylin Linux Advanced Server", "MIRACLE"},
	"Debian": {"Debian", "Ubuntu", "Raspbian", "Neon", "KDE neon", "Linux Mint", "SteamOS", "Devuan", "Kali", "Cumulus Linux",
		"Pop!_OS", "Parrot", "Pardus GNU/Linux", "Uos", "Deepin", "OSMC"},
	"Suse":       {"SuSE", "SLES", "SLED", "openSUSE", "openSUSE Tumbleweed", "SLES_SAP", "SUSE_LINUX", "openSUSE Leap", "ALP-Dolomite"},
	"Archlinux":  {"Archlinux", "Antergos", "Manjaro"},
	"Mandrake":   {"Mandrake", "Mandriva"},
	"Solaris":    {"Solaris", "Nexenta", "OmniOS", "OpenIndiana", "SmartOS"},
	"Slackware":  {"Slackware"},
	"Altlinux":   {"Altlinux"},
	"SMGL":       {"SMGL"},
	"Gentoo":     {"Gentoo", "Funtoo"},
	"Alpine":     {"Alpine"},
	"AIX":        {"AIX"},
	"HP-UX":      {"HPUX"},
	"Darwin":     {"MacOSX"},
	"FreeBSD":    {"FreeBSD", "TrueOS"},
	"ClearLinux": {"Clear Linux OS", "Clear Linux Mix"},
	"DragonFly":  {"DragonflyBSD", "DragonFlyBSD", "Gentoo/DragonflyBSD", "Gentoo/DragonFlyBSD"},
	"NetBSD":     {"NetBSD"},
}

// ansibleOSFamily is ansibleOSFamilyMap read the other way: a distribution's
// family.
var ansibleOSFamily = func() map[string]string {
	out := map[string]string{}
	for family, names := range ansibleOSFamilyMap {
		for _, name := range names {
			out[name] = family
		}
	}
	return out
}()

// osFamilyFact is distribution's ansible_os_family: its family in Ansible's
// map, or the distribution itself, as Ansible reports one the map does not
// list.
func osFamilyFact(distribution string) gatherFact {
	if family, ok := ansibleOSFamily[distribution]; ok {
		return gatherFact{key: gatherFactOSFamily, value: family}
	}
	return gatherFact{key: gatherFactOSFamily, value: distribution}
}

// versionCodename is the distro library's codename in os-release's VERSION:
// a parenthesized run holding no digit ("9.6 (Blue Onyx)"), or what follows a
// comma.
var versionCodename = regexp.MustCompile(`\((\D+)\)|,\s*(\D+)`)

// suseVersion is Ansible's SUSE version (major and optional minor) at the
// start of VERSION_ID, the minor part being its release.
var suseVersion = regexp.MustCompile(`^([0-9]+)\.?([0-9]*)`)

// gatherOSRelease reads the min probe's answer (os-release, the mark, and
// /etc/debian_version) into the distribution facts as ansible.builtin.setup
// reports them. A value Ansible would report as NA, or as an empty string, is
// left out, as every fact this method cannot determine is.
func gatherOSRelease(stdout string) []gatherFact {
	osRelease, debianVersion, _ := strings.Cut(stdout, debianVersionMark)
	values := gatherKeyValues(osRelease)
	id := values["ID"]

	distribution := ansibleDistributionByID[id]
	if distribution == "" {
		// Ansible's fallback (parse_distribution_file_NA) and its SUSE parser
		// both report NAME.
		distribution = values["NAME"]
	}
	version := distributionVersion(id, values, strings.TrimSpace(debianVersion))
	release := distributionRelease(id, values)

	var facts []gatherFact
	if distribution != "" {
		facts = append(facts, gatherFact{key: gatherFactDistribution, value: distribution}, osFamilyFact(distribution))
	}
	if version != "" {
		major, _, _ := strings.Cut(version, ".")
		facts = append(facts, gatherFact{key: gatherFactDistributionVersion, value: version},
			gatherFact{key: gatherFactDistributionMajor, value: major})
	}
	if release != "" {
		facts = append(facts, gatherFact{key: gatherFactDistributionRelease, value: release})
	}
	return facts
}

// distributionVersion is Ansible's distribution version: VERSION_ID, but on
// Debian the more precise of VERSION_ID and /etc/debian_version, by distro's
// best-version rule (the first present, replaced by a later one with more
// dots), so 12 with a debian_version of 12.12 is 12.12, and testing, which has
// no VERSION_ID, is what debian_version says (forky/sid). On the SUSE family it
// is VERSION_ID's leading major.minor, as Ansible's SUSE parser cuts it.
func distributionVersion(id string, values map[string]string, debianVersion string) string {
	version := values["VERSION_ID"]
	switch {
	case id == "debian" && debianVersion != "":
		if version == "" || strings.Count(debianVersion, ".") > strings.Count(version, ".") {
			version = debianVersion
		}
	case isSUSE(id):
		if m := suseVersion.FindString(version); m != "" {
			version = m
		}
	}
	return version
}

// distributionRelease is Ansible's distribution release. On the SUSE family it
// is VERSION_ID's minor part ("0" on an enterprise release with none); on
// CentOS Stream it is Stream, which Ansible reads off /etc/centos-release and
// NAME carries too. Elsewhere it is the distro library's codename:
// VERSION_CODENAME when the file has the key (even empty, which Fedora's is),
// then UBUNTU_CODENAME, then VERSION's parenthesized name.
func distributionRelease(id string, values map[string]string) string {
	if isSUSE(id) {
		m := suseVersion.FindStringSubmatch(values["VERSION_ID"])
		if m == nil {
			return ""
		}
		if m[2] == "" && suseIDs[id] {
			return "0"
		}
		return m[2]
	}
	if id == "centos" && strings.Contains(values["NAME"], "Stream") {
		return "Stream"
	}
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

// isSUSE reports whether id is read through Ansible's SUSE parser.
func isSUSE(id string) bool {
	_, ok := suseIDs[id]
	return ok
}
