// Tests of the distribution facts against Ansible's own answers: each file in
// testdata/distribution is what the min probe's command printed in one
// distribution's container image, and testdata/distribution/ansible.json is
// what ansible.builtin.setup reported in the same image (2026-11-06), so a
// converted condition such as ansible_distribution == "Debian" reads here as
// it did under Ansible.
package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ansibleDistributionFacts is the five facts gatherOSRelease answers, by the
// names ansible.json records them under.
var ansibleDistributionFacts = []string{gatherFactDistribution, gatherFactOSFamily, gatherFactDistributionVersion,
	gatherFactDistributionMajor, gatherFactDistributionRelease}

func TestGatherOSRelease_MatchesAnsibleOnEveryMeasuredDistribution(t *testing.T) {
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
	if len(measured.Images) < 20 {
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
				// Ansible's NA, and its empty release, are what this method
				// leaves out rather than guessing.
				if v := ansible[key]; v != "" && v != "NA" {
					want[key] = v
				}
			}
			if got := valuesOf(gatherOSRelease(string(out))); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: gathered %v\nAnsible reported %v", ansible["image"], got, want)
			}
		})
	}
}

func TestGatherOSRelease_ReadsWhatTheFileSays(t *testing.T) {
	cases := []struct {
		name, out string
		want      map[string]any
	}{
		// An os-release with no ID reports its NAME, as Ansible's fallback
		// does, and no family Ansible names, so the family is the name.
		{"no ID", "NAME=\"Example OS\"\nVERSION_ID=3.1\n", map[string]any{gatherFactDistribution: "Example OS", gatherFactOSFamily: "Example OS",
			gatherFactDistributionVersion: "3.1", gatherFactDistributionMajor: "3"}},
		// A NAME Ansible's family table lists finds its family.
		{"listed NAME", "NAME=\"Linux Mint\"\nID=linuxmint\nVERSION_ID=\"22\"\nVERSION_CODENAME=wilma\n", map[string]any{gatherFactDistribution: "Linux Mint",
			gatherFactOSFamily: "Debian", gatherFactDistributionVersion: "22", gatherFactDistributionMajor: "22", gatherFactDistributionRelease: "wilma"}},
		// Debian's VERSION_ID wins over a debian_version no more precise.
		{"debian no point", "NAME=\"Debian GNU/Linux\"\nID=debian\nVERSION_ID=\"12\"\n" + debianVersionMark + "\n12\n", map[string]any{gatherFactDistribution: "Debian",
			gatherFactOSFamily: "Debian", gatherFactDistributionVersion: "12", gatherFactDistributionMajor: "12"}},
		// A debian_version on a distribution that is not Debian is not read.
		{"ubuntu sid", "NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"24.04\"\n" + debianVersionMark + "\ntrixie/sid\n", map[string]any{gatherFactDistribution: "Ubuntu",
			gatherFactOSFamily: "Debian", gatherFactDistributionVersion: "24.04", gatherFactDistributionMajor: "24"}},
		// SLES with no minor release is release 0.
		{"sles first", "NAME=\"SLES\"\nID=\"sles\"\nVERSION_ID=\"15\"\n", map[string]any{gatherFactDistribution: "SLES", gatherFactOSFamily: "Suse",
			gatherFactDistributionVersion: "15", gatherFactDistributionMajor: "15", gatherFactDistributionRelease: "0"}},
		// A codename after a comma, as the distro library reads one.
		{"comma codename", "NAME=Example\nID=example\nVERSION=\"7, Hydrogen\"\n", map[string]any{gatherFactDistribution: "Example", gatherFactOSFamily: "Example",
			gatherFactDistributionRelease: "Hydrogen"}},
		// UBUNTU_CODENAME, where there is no VERSION_CODENAME.
		{"ubuntu codename", "NAME=Example\nID=example\nUBUNTU_CODENAME=noble\nVERSION=\"1 (Other)\"\n", map[string]any{gatherFactDistribution: "Example",
			gatherFactOSFamily: "Example", gatherFactDistributionRelease: "noble"}},
		// A SUSE VERSION_ID with no number in it names no release.
		{"suse no version", "NAME=\"openSUSE Tumbleweed\"\nID=\"opensuse-tumbleweed\"\n", map[string]any{gatherFactDistribution: "openSUSE Tumbleweed",
			gatherFactOSFamily: "Suse"}},
		{"empty", "", map[string]any{}},
	}
	for _, tc := range cases {
		if got := valuesOf(gatherOSRelease(tc.out)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
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
	if got := probes[0].Read(string(out)); got[gatherFactDistribution] != "Debian" || got[gatherFactDistributionVersion] != "12.12" {
		t.Errorf("%v", got)
	}
}
