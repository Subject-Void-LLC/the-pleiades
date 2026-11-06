// The min subset's distribution probe against real distributions: its own
// command run in each one's container and read by the real parser, held to
// what ansible.builtin.setup reported in the same image (recorded in the facts
// package's testdata/distribution/ansible.json, 2026-11-06), so a converted
// condition such as ansible_distribution == "Debian" or ansible_os_family ==
// "RedHat" reads here as it did under Ansible (sweep 2026-11-04, item 4).
package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
)

// The distributions run here, each read in a way the others are not: Debian
// takes its point release from /etc/debian_version, Ubuntu has a
// debian_version it must not read, Rocky's release is the name in VERSION's
// parentheses, and Alpine reports no release.
const (
	debianImage = "debian:12.12"
	ubuntuImage = "ubuntu:24.04"
)

// distributionFacts is the facts the distribution probe answers.
var distributionFacts = []string{"ansible_distribution", "ansible_os_family", "ansible_distribution_version",
	"ansible_distribution_major_version", "ansible_distribution_release"}

func TestGatherDistribution_MatchesAnsibleOnRealDistributions(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "distribution", "ansible.json"))
	if err != nil {
		t.Fatal(err)
	}
	var measured struct {
		Images map[string]map[string]string `json:"images"`
	}
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	probes := facts.Probes("Linux", "min", "/etc/os-release")
	if len(probes) != 1 {
		t.Fatalf("%d os-release probes", len(probes))
	}
	for slug, image := range map[string]string{
		"debian-12.12": debianImage, "ubuntu-24.04": ubuntuImage,
		"rockylinux-rockylinux-9.6-minimal": rockyImage, "alpine-3.20.10": openrcImage,
	} {
		t.Run(slug, func(t *testing.T) {
			ansible := measured.Images[slug]
			if ansible["image"] != image {
				t.Fatalf("ansible.json measured %q for %s, not %s", ansible["image"], slug, image)
			}
			c := start(t, image)
			out, _ := runAs(t, c, "", probes[0].Command)
			got := probes[0].Read(out)
			for _, key := range distributionFacts {
				want, present := ansible[key], true
				// Ansible's NA, and its empty release, are what this method
				// leaves out rather than guessing.
				if want == "" || want == "NA" {
					present = false
				}
				value, ok := got[key]
				if ok != present || (present && value != want) {
					t.Errorf("%s = %v (%v); Ansible reported %q", key, value, ok, ansible[key])
				}
			}
		})
	}
}
