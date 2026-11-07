// Tests of the mapping from native facts to Ansible's names and values. The
// distribution rules are also held to Ansible's measured answers in each
// distribution's container by internal/catalog/facts/live, and to the
// captured os-release files by internal/catalog/facts' own tests; these pin
// each rule on its own, the Windows, macOS and FreeBSD ones included.
package ansiblefacts

import (
	"reflect"
	"testing"
)

func TestKeys_AreOneTableReadBothWays(t *testing.T) {
	if len(ansibleByNative) != 16 {
		t.Errorf("%d keys mapped, want the 16 facts.gather declares with an Ansible name", len(ansibleByNative))
	}
	for native, ansible := range ansibleByNative {
		if got, ok := AnsibleKey(native); !ok || got != ansible {
			t.Errorf("AnsibleKey(%q) = %q, %v", native, got, ok)
		}
		if got, ok := NativeKey(ansible); !ok || got != native {
			t.Errorf("NativeKey(%q) = %q, %v", ansible, got, ok)
		}
	}
	for _, native := range []string{keyEdition, "package.nginx", "ansible_kernel", ""} {
		if got, ok := AnsibleKey(native); ok {
			t.Errorf("AnsibleKey(%q) = %q, want none", native, got)
		}
	}
	for _, ansible := range []string{"ansible_fqdn", "os.kernel.release", ""} {
		if got, ok := NativeKey(ansible); ok {
			t.Errorf("NativeKey(%q) = %q, want none", ansible, got)
		}
	}
}

func TestToAnsible_GivesAnsiblesNamesAndWords(t *testing.T) {
	cases := []struct {
		name   string
		native map[string]any
		want   map[string]any
	}{
		{"linux", map[string]any{
			keyHostName: "web01", keyHostNodename: "web01.example", keyHostUptime: int64(311), keyKernelName: "linux",
			keyKernelRelease: "6.8.0-45-generic", keyDistribution: "ubuntu", keyFamily: "debian", keyVersion: "24.04",
			keyVersionMajor: 24, keyCodename: "noble", keyArchitecture: "x86_64", keyMemoryMB: int64(7944),
			keySwapMB: int64(2047), keyCPUSockets: 1, keyCPUCores: 4, keyCPUThreads: 8,
			"package.nginx": map[string]any{"version": "1.24"},
		}, map[string]any{
			"ansible_hostname": "web01", "ansible_nodename": "web01.example", "ansible_uptime_seconds": int64(311),
			"ansible_system": "Linux", "ansible_kernel": "6.8.0-45-generic", "ansible_distribution": "Ubuntu",
			"ansible_os_family": "Debian", "ansible_distribution_version": "24.04", "ansible_distribution_major_version": "24",
			"ansible_distribution_release": "noble", "ansible_architecture": "x86_64", "ansible_memtotal_mb": int64(7944),
			"ansible_swaptotal_mb": int64(2047), "ansible_processor_count": 1, "ansible_processor_cores": 4,
			"ansible_processor_vcpus": 8,
		}},
		{"windows", map[string]any{
			keyHostName: "WIN-CHECK", keyKernelName: "windows", keyKernelRelease: "10.0.26100.0", keyDistribution: "windows",
			keyFamily: "windows", keyVersion: "10.0.26100.0", keyVersionMajor: 10, keyEdition: "Microsoft Windows 11 Pro",
			keyArchitecture: "x86_64", keyMemoryMB: 2046,
		}, map[string]any{
			"ansible_hostname": "WIN-CHECK", "ansible_system": "Win32NT", "ansible_kernel": "10.0.26100.0",
			"ansible_distribution": "Microsoft Windows 11 Pro", "ansible_os_family": "Windows",
			"ansible_distribution_version": "10.0.26100.0", "ansible_distribution_major_version": "10",
			"ansible_architecture": "64-bit", "ansible_memtotal_mb": 2046, "ansible_swaptotal_mb": 0,
		}},
		// A Windows architecture the lab has not measured is left out, and so
		// is a caption the host did not give; swap is still Ansible's 0.
		{"windows arm", map[string]any{keyKernelName: "windows", keyArchitecture: "aarch64", keySwapMB: 5},
			map[string]any{"ansible_system": "Win32NT", "ansible_swaptotal_mb": 0}},
		{"windows i686 by family alone", map[string]any{keyFamily: "windows", keyArchitecture: "i686"},
			map[string]any{"ansible_os_family": "Windows", "ansible_architecture": "32-bit", "ansible_swaptotal_mb": 0}},
		{"macos", map[string]any{keyKernelName: "darwin", keyDistribution: "macos", keyFamily: "macos", keyVersion: "15.1",
			keyVersionMajor: 15, keyArchitecture: "arm64"}, map[string]any{
			"ansible_system": "Darwin", "ansible_distribution": "MacOSX", "ansible_os_family": "Darwin",
			"ansible_distribution_version": "15.1", "ansible_distribution_major_version": "15", "ansible_architecture": "arm64"}},
		{"freebsd", map[string]any{keyKernelName: "freebsd", keyDistribution: "freebsd", keyFamily: "freebsd", keyVersion: "14.1"},
			map[string]any{"ansible_system": "FreeBSD", "ansible_distribution": "FreeBSD", "ansible_os_family": "FreeBSD",
				"ansible_distribution_version": "14.1", "ansible_distribution_major_version": "14"}},
		// Debian testing: no native major, and Ansible's is the whole version.
		{"debian testing", map[string]any{keyDistribution: "debian", keyVersion: "forky/sid", keyCodename: "forky"},
			map[string]any{"ansible_distribution": "Debian", "ansible_distribution_version": "forky/sid",
				"ansible_distribution_major_version": "forky/sid", "ansible_distribution_release": "forky"}},
		// With no os.version, the native major is written out.
		{"major alone", map[string]any{keyVersionMajor: 9}, map[string]any{"ansible_distribution_major_version": "9"}},
		{"major alone int64", map[string]any{keyVersionMajor: int64(2023)}, map[string]any{"ansible_distribution_major_version": "2023"}},
		{"major of another type", map[string]any{keyVersionMajor: "9"}, map[string]any{}},
		// Ansible's release on the SUSE family is the version's minor part.
		{"sles minor", map[string]any{keyDistribution: "sles", keyVersion: "15.6"},
			map[string]any{"ansible_distribution": "SLES", "ansible_distribution_version": "15.6",
				"ansible_distribution_major_version": "15", "ansible_distribution_release": "6"}},
		{"sles first", map[string]any{keyDistribution: "sles", keyVersion: "15", keyCodename: "ignored"},
			map[string]any{"ansible_distribution": "SLES", "ansible_distribution_version": "15",
				"ansible_distribution_major_version": "15", "ansible_distribution_release": "0"}},
		{"leap first", map[string]any{keyDistribution: "opensuse-leap", keyVersion: "15"},
			map[string]any{"ansible_distribution": "openSUSE Leap", "ansible_distribution_version": "15",
				"ansible_distribution_major_version": "15"}},
		{"suse no version", map[string]any{keyDistribution: "sled"}, map[string]any{}},
		// CentOS Stream names no codename; Ansible says Stream. CentOS Linux
		// names one.
		{"centos stream", map[string]any{keyDistribution: "centos"},
			map[string]any{"ansible_distribution": "CentOS", "ansible_distribution_release": "Stream"}},
		{"centos linux", map[string]any{keyDistribution: "centos", keyCodename: "Core"},
			map[string]any{"ansible_distribution": "CentOS", "ansible_distribution_release": "Core"}},
		// What this package has not measured is left out, never guessed.
		{"unknown", map[string]any{keyDistribution: "raspbian", keyFamily: "unheard", keyKernelName: "sunos", keyCodename: ""},
			map[string]any{}},
		{"listed family", map[string]any{keyFamily: "hp-ux"}, map[string]any{"ansible_os_family": "HP-UX"}},
		{"empty", map[string]any{}, map[string]any{}},
	}
	for _, tc := range cases {
		if got := ToAnsible(tc.native); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v\nwant %v", tc.name, got, tc.want)
		}
	}
}

func TestToAnsible_MapsEveryNativeFamilyBack(t *testing.T) {
	for family, names := range ansibleOSFamilyMap {
		for _, name := range names {
			native, ok := FamilyOfName(name)
			if !ok {
				t.Fatalf("FamilyOfName(%q) found nothing", name)
			}
			if got := ToAnsible(map[string]any{keyFamily: native})["ansible_os_family"]; got != family {
				t.Errorf("%s: native %q maps back to %v, want %s", name, native, got, family)
			}
		}
	}
}

func TestFamilyOfName_ReadsAnsiblesTableAsNativeWords(t *testing.T) {
	for name, want := range map[string]string{"Linux Mint": "debian", "Manjaro": "arch", "MacOSX": "macos",
		"openSUSE Leap": "suse", "CloudLinux": "redhat", "Gentoo": "gentoo", "HPUX": "hp-ux"} {
		if got, ok := FamilyOfName(name); !ok || got != want {
			t.Errorf("FamilyOfName(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"Raspbian GNU/Linux", "", "debian"} {
		if got, ok := FamilyOfName(name); ok {
			t.Errorf("FamilyOfName(%q) = %q, want none", name, got)
		}
	}
}
