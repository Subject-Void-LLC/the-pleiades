// Package ansiblefacts: the tables behind the value rules. Each is Ansible's
// word for a native value, for the values measured against
// ansible.builtin.setup (or, for the family table, Ansible's own OS_FAMILY_MAP
// copied whole, which is how a distribution's NAME finds its family).
package ansiblefacts

import (
	"regexp"
	"strings"
)

// windowsName is the native word for Windows in os.kernel.name, os.family
// and os.distribution.
const windowsName = "windows"

// distributionNames is Ansible's distribution name for each native
// os.distribution measured against it: the os-release ID on Linux (Ansible
// reads most of these from another file, such as the first word of
// /etc/redhat-release, and the ID names the same thing on every release of
// each; on the SUSE family Ansible reports NAME, which is fixed per ID),
// freebsd, and macos. Windows is not here: Ansible reports the edition's
// caption, which is os.edition.
var distributionNames = map[string]string{
	"debian":              "Debian",
	"ubuntu":              "Ubuntu",
	"kali":                "Kali",
	"devuan":              "Devuan",
	"rhel":                "RedHat",
	"centos":              "CentOS",
	"rocky":               "Rocky",
	"almalinux":           "AlmaLinux",
	"ol":                  "OracleLinux",
	"fedora":              "Fedora",
	"amzn":                "Amazon",
	"alpine":              "Alpine",
	"arch":                "Archlinux",
	"opensuse-leap":       "openSUSE Leap",
	"opensuse-tumbleweed": "openSUSE Tumbleweed",
	"sles":                "SLES",
	"freebsd":             "FreeBSD",
	"macos":               "MacOSX",
}

// suseIDs are the os-release IDs Ansible reads through its SUSE parser, and
// whether each is an enterprise release, whose release is "0" when its
// version has no minor part, where openSUSE's is empty.
var suseIDs = map[string]bool{
	"opensuse-leap":       false,
	"opensuse-tumbleweed": false,
	"opensuse":            false,
	"sles":                true,
	"sled":                true,
	"sles_sap":            true,
}

// suseVersion is Ansible's SUSE version (major and optional minor) at the
// start of the version, the minor part being its release.
var suseVersion = regexp.MustCompile(`^([0-9]+)\.?([0-9]*)`)

// kernelNames is Ansible's ansible_system for each native os.kernel.name:
// uname -s as it prints it, and Win32NT on Windows.
var kernelNames = map[string]string{
	"linux":     "Linux",
	"freebsd":   "FreeBSD",
	"darwin":    "Darwin",
	windowsName: "Win32NT",
}

// windowsArchitectures is Ansible's ansible_architecture on Windows (the
// operating system's OSArchitecture, as the lab's host printed it) for each
// native machine name.
var windowsArchitectures = map[string]string{
	"x86_64": "64-bit",
	"i686":   "32-bit",
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

// nativeFamilyWords are the native families whose word is not Ansible's
// lowercased: Archlinux is arch, and Darwin (macOS) is macos.
var nativeFamilyWords = map[string]string{
	"Archlinux": "arch",
	"Darwin":    "macos",
}

// nativeFamily is the native word for Ansible's family name: lowercased,
// but for the words nativeFamilyWords holds.
func nativeFamily(ansible string) string {
	if word, ok := nativeFamilyWords[ansible]; ok {
		return word
	}
	return strings.ToLower(ansible)
}

// ansibleFamilyOfNative is each native family's Ansible name: every family
// in Ansible's map, and Windows, which ansible.windows' setup names so.
var ansibleFamilyOfNative = func() map[string]string {
	out := map[string]string{windowsName: "Windows"}
	for family := range ansibleOSFamilyMap {
		out[nativeFamily(family)] = family
	}
	return out
}()

// nativeFamilyOfName is ansibleOSFamilyMap read the other way, as native
// families: a distribution name's family.
var nativeFamilyOfName = func() map[string]string {
	out := map[string]string{}
	for family, names := range ansibleOSFamilyMap {
		for _, name := range names {
			out[name] = nativeFamily(family)
		}
	}
	return out
}()

// FamilyOfName is the native family (debian, redhat, suse, arch) that
// Ansible's own family table gives a distribution called name, such as an
// os-release NAME of Linux Mint, and whether the table lists that name. It
// is how facts.gather finds the family of a distribution whose os-release ID
// it does not know, without guessing one.
func FamilyOfName(name string) (string, bool) {
	family, ok := nativeFamilyOfName[name]
	return family, ok
}
