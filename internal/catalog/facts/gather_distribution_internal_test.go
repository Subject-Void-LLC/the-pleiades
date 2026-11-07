// Tests of the distribution facts: each file in testdata/distribution is what
// the min probe's command printed in one distribution's container image, and
// testdata/distribution/ansible.json is what ansible.builtin.setup reported in
// the same image (2026-11-06), so the native facts read from each, mapped
// through pkg/ansiblefacts, are held to Ansible's own answers, and a
// converted condition such as ansible_distribution == "Debian" reads here as
// it did under Ansible.
package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// ansibleDistributionFacts is the five Ansible facts the distribution facts
// map to, by the names ansible.json records them under.
var ansibleDistributionFacts = []string{"ansible_distribution", "ansible_os_family", "ansible_distribution_version",
	"ansible_distribution_major_version", "ansible_distribution_release"}

func TestGatherOSRelease_MapsToAnsibleOnEveryMeasuredDistribution(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "distribution", "ansible.json"))
	if err != nil {
		t.Fatal(err)
	}
	var measured struct {
		Images map[string]map[string]string `json:"images"`
	}
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	if len(measured.Images) < 22 {
		t.Fatalf("ansible.json holds %d images, want every one measured", len(measured.Images))
	}
	for slug, ansible := range measured.Images {
		t.Run(slug, func(t *testing.T) {
			out, err := os.ReadFile(filepath.Join("testdata", "distribution", slug+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{}
			for _, key := range ansibleDistributionFacts {
				// Ansible's NA, and its empty release, are what the mapping
				// leaves out rather than guessing.
				if v := ansible[key]; v != "" && v != "NA" {
					want[key] = v
				}
			}
			native := valuesOf(gatherOSRelease(string(out)))
			if got := ansiblefacts.ToAnsible(native); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: gathered %v, mapped to %v\nAnsible reported %v", ansible["image"], native, got, want)
			}
		})
	}
}

// TestGatherFacts_EachHasItsAnsibleName holds this method's declared facts
// to pkg/ansiblefacts' key table, so a fact added here is not silently
// left out of the mapping a converted playbook reads through. os.edition is
// the one with no Ansible name of its own (it is Ansible's
// ansible_distribution on Windows).
func TestGatherFacts_EachHasItsAnsibleName(t *testing.T) {
	names := gatherFactNames()
	if len(names) != 17 {
		t.Errorf("%d facts declared: %v", len(names), names)
	}
	for _, key := range names {
		ansible, ok := ansiblefacts.AnsibleKey(key)
		if key == gatherFactEdition {
			if ok {
				t.Errorf("%s maps to %s", key, ansible)
			}
			continue
		}
		if !ok {
			t.Errorf("%s has no Ansible name", key)
		} else if native, _ := ansiblefacts.NativeKey(ansible); native != key {
			t.Errorf("%s maps to %s, which maps back to %s", key, ansible, native)
		}
	}
}

func TestGatherOSRelease_ReadsWhatTheFileSays(t *testing.T) {
	cases := []struct {
		name, out string
		want      map[string]any
	}{
		// An os-release with no ID names no distribution and no family: the
		// file does not say, and a family is not guessed.
		{"no ID", "NAME=\"Example OS\"\nVERSION_ID=3.1\n", map[string]any{gatherFactVersion: "3.1", gatherFactVersionMajor: 3}},
		// An ID this method has not measured is still the distribution; its
		// NAME is not in Ansible's family table, so it has no family.
		{"unlisted derivative", "NAME=\"Raspbian GNU/Linux\"\nID=raspbian\nID_LIKE=debian\nVERSION_ID=\"11\"\nVERSION_CODENAME=bullseye\n", map[string]any{
			gatherFactDistribution: "raspbian", gatherFactVersion: "11", gatherFactVersionMajor: 11, gatherFactCodename: "bullseye"}},
		// A NAME Ansible's family table lists finds its family, lowercased.
		{"listed NAME", "NAME=\"Linux Mint\"\nID=linuxmint\nVERSION_ID=\"22\"\nVERSION_CODENAME=wilma\n", map[string]any{gatherFactDistribution: "linuxmint",
			gatherFactFamily: "debian", gatherFactVersion: "22", gatherFactVersionMajor: 22, gatherFactCodename: "wilma"}},
		{"listed NAME outside the measured families", "NAME=Gentoo\nID=gentoo\nVERSION_ID=2.17\n", map[string]any{gatherFactDistribution: "gentoo",
			gatherFactFamily: "gentoo", gatherFactVersion: "2.17", gatherFactVersionMajor: 2}},
		// The ID is the distribution, lowercase whatever the file wrote.
		{"uppercase ID", "NAME=\"Debian GNU/Linux\"\nID=Debian\nVERSION_ID=\"12\"\n", map[string]any{gatherFactDistribution: "debian",
			gatherFactFamily: "debian", gatherFactVersion: "12", gatherFactVersionMajor: 12}},
		// Debian's VERSION_ID wins over a debian_version no more precise.
		{"debian no point", "NAME=\"Debian GNU/Linux\"\nID=debian\nVERSION_ID=\"12\"\n" + debianVersionMark + "\n12\n", map[string]any{gatherFactDistribution: "debian",
			gatherFactFamily: "debian", gatherFactVersion: "12", gatherFactVersionMajor: 12}},
		// Only debian_version's first line is the version.
		{"debian two lines", "NAME=\"Debian GNU/Linux\"\nID=debian\nVERSION_ID=\"12\"\n" + debianVersionMark + "\n12.12\n# built 2026.11.06\n", map[string]any{gatherFactDistribution: "debian",
			gatherFactFamily: "debian", gatherFactVersion: "12.12", gatherFactVersionMajor: 12}},
		// Debian testing's version has no whole-number major part, so it has
		// no os.version_major (Ansible's forky/sid is the mapping's).
		{"debian testing", "NAME=\"Debian GNU/Linux\"\nID=debian\nVERSION_CODENAME=forky\n" + debianVersionMark + "\nforky/sid\n", map[string]any{gatherFactDistribution: "debian",
			gatherFactFamily: "debian", gatherFactVersion: "forky/sid", gatherFactCodename: "forky"}},
		// A debian_version on a distribution that is not Debian is not read.
		{"ubuntu sid", "NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"24.04\"\n" + debianVersionMark + "\ntrixie/sid\n", map[string]any{gatherFactDistribution: "ubuntu",
			gatherFactFamily: "debian", gatherFactVersion: "24.04", gatherFactVersionMajor: 24}},
		// SLES names no codename; Ansible's release 0 is the mapping's.
		{"sles first", "NAME=\"SLES\"\nID=\"sles\"\nVERSION_ID=\"15\"\nVERSION=\"15\"\n", map[string]any{gatherFactDistribution: "sles", gatherFactFamily: "suse",
			gatherFactVersion: "15", gatherFactVersionMajor: 15}},
		// On the SUSE family the version is cut to major.minor.
		{"suse long version", "NAME=\"SLES\"\nID=\"sles\"\nVERSION_ID=\"15.6.1\"\n", map[string]any{gatherFactDistribution: "sles", gatherFactFamily: "suse",
			gatherFactVersion: "15.6", gatherFactVersionMajor: 15}},
		// CentOS Stream names no codename; Ansible's Stream is the mapping's.
		{"centos stream", "NAME=\"CentOS Stream\"\nID=\"centos\"\nVERSION=\"9\"\nVERSION_ID=\"9\"\n", map[string]any{gatherFactDistribution: "centos",
			gatherFactFamily: "redhat", gatherFactVersion: "9", gatherFactVersionMajor: 9}},
		// FreeBSD's os-release is read as any other.
		{"freebsd", "NAME=FreeBSD\nVERSION=\"14.1-RELEASE\"\nVERSION_ID=\"14.1\"\nID=freebsd\n", map[string]any{gatherFactDistribution: "freebsd",
			gatherFactFamily: "freebsd", gatherFactVersion: "14.1", gatherFactVersionMajor: 14}},
		// A codename after a comma, as the distro library reads one.
		{"comma codename", "NAME=Example\nID=example\nVERSION=\"7, Hydrogen\"\n", map[string]any{gatherFactDistribution: "example",
			gatherFactCodename: "Hydrogen"}},
		// UBUNTU_CODENAME, where there is no VERSION_CODENAME.
		{"ubuntu codename", "NAME=Example\nID=example\nUBUNTU_CODENAME=noble\nVERSION=\"1 (Other)\"\n", map[string]any{gatherFactDistribution: "example",
			gatherFactCodename: "noble"}},
		// An empty VERSION_CODENAME names none, whatever VERSION holds.
		{"empty codename", "NAME=Fedora\nID=fedora\nVERSION_ID=42\nVERSION_CODENAME=\"\"\nVERSION=\"42 (Container Image)\"\n", map[string]any{gatherFactDistribution: "fedora",
			gatherFactFamily: "redhat", gatherFactVersion: "42", gatherFactVersionMajor: 42}},
		// A SUSE VERSION_ID with no number in it names no version.
		{"suse no version", "NAME=\"openSUSE Tumbleweed\"\nID=\"opensuse-tumbleweed\"\n", map[string]any{gatherFactDistribution: "opensuse-tumbleweed",
			gatherFactFamily: "suse"}},
		{"empty", "", map[string]any{}},
	}
	for _, tc := range cases {
		if got := valuesOf(gatherOSRelease(tc.out)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVersionMajor_IsAWholeNumberOrNothing(t *testing.T) {
	for version, want := range map[string]any{"24.04": 24, "42": 42, "20261101.0.620205": 20261101, "10.0.26100.0": 10,
		"forky/sid": nil, "15-SP6": nil, ".1": nil, "x.1": nil, "": nil} {
		got, ok := versionMajor(version)
		if (want == nil && ok) || (want != nil && (!ok || got != want)) {
			t.Errorf("versionMajor(%q) = %v, %v; want %v", version, got, ok, want)
		}
	}
}

func TestGatherLower_KeepsTheKernelNameLowercase(t *testing.T) {
	for out, want := range map[string]any{"Linux\n": "linux", "FreeBSD": "freebsd", "Darwin\n": "darwin", "  \n": nil} {
		got := valuesOf(gatherLower(gatherFactKernelName)(out))
		if v, ok := got[gatherFactKernelName]; (want == nil && ok) || (want != nil && v != want) {
			t.Errorf("%q read as %v", out, got)
		}
	}
}

func TestProbeRead_ReadsTheMinProbeAsGatherDoes(t *testing.T) {
	probes := Probes(platformLinux, gatherSubsetMin, "/etc/os-release")
	if len(probes) != 1 {
		t.Fatalf("%d os-release probes", len(probes))
	}
	out, err := os.ReadFile(filepath.Join("testdata", "distribution", "debian-12.12.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := probes[0].Read(string(out)); got[gatherFactDistribution] != "debian" || got[gatherFactVersion] != "12.12" || got[gatherFactVersionMajor] != 12 {
		t.Errorf("%v", got)
	}
}
