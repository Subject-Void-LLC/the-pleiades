// The min subset's distribution probe against real distributions, and the
// mapping to Ansible's names with it: in each image ansible.builtin.setup was
// measured in (the facts package's testdata/distribution/ansible.json,
// 2026-11-06), the probe's own command is run in that image's container and
// read by the real parser into native facts, and those facts, passed through
// pkg/ansiblefacts' ToAnsible, are held to what Ansible reported there.
// That is the proof the native facts and the mapping together give a
// converted condition such as ansible_distribution == "Debian" or
// ansible_os_family == "RedHat" what it read under Ansible.
package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// ansibleDistributionFacts is the Ansible facts the distribution facts map
// to, by the names ansible.json records them under.
var ansibleDistributionFacts = []string{"ansible_distribution", "ansible_os_family", "ansible_distribution_version",
	"ansible_distribution_major_version", "ansible_distribution_release"}

// The pinned images CI runs, beside rpm_test.go's rockyImage and
// openrc_test.go's openrcImage; each is the image ansible.json measured.
const (
	debianImage = "debian:12.12"
	ubuntuImage = "ubuntu:24.04"
)

// osReleaseIDCommand prints the image's os-release ID by the shell's own
// reading of the file, apart from the parser under test.
const osReleaseIDCommand = `. /etc/os-release && printf '%s' "$ID"`

func TestGatherDistribution_MapsToAnsibleOnRealDistributions(t *testing.T) {
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
	if len(measured.Images) < 22 {
		t.Fatalf("ansible.json holds %d images, want every one measured", len(measured.Images))
	}
	probes := facts.Probes("Linux", "min", "/etc/os-release")
	if len(probes) != 1 {
		t.Fatalf("%d os-release probes", len(probes))
	}
	// CI runs the pinned images, each named in Go so tools/testimages caches
	// and checks it, and each held to the image ansible.json measured. Every
	// measured image, the rolling ones (Tumbleweed, Arch, Kali, Debian testing)
	// among them, whose values move with each snapshot, runs only when asked
	// (PLEIADES_LIVE_ALL_DISTRIBUTIONS=1), as a measurement on a given day.
	pinned := map[string]string{
		"debian-12.12": debianImage, "ubuntu-24.04": ubuntuImage,
		"rockylinux-rockylinux-9.6-minimal": rockyImage, "alpine-3.20.10": openrcImage,
	}
	slugs := make([]string, 0, len(measured.Images))
	for slug := range measured.Images {
		if _, ok := pinned[slug]; ok || os.Getenv("PLEIADES_LIVE_ALL_DISTRIBUTIONS") == "1" {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	if len(slugs) < len(pinned) {
		t.Fatalf("ansible.json lacks a pinned image: running %q", slugs)
	}
	for _, slug := range slugs {
		ansible := measured.Images[slug]
		t.Run(slug, func(t *testing.T) {
			if image, ok := pinned[slug]; ok && ansible["image"] != image {
				t.Fatalf("ansible.json measured %q for %s, not the pinned %s", ansible["image"], slug, image)
			}
			c := start(t, ansible["image"])
			out, _ := runAs(t, c, "", probes[0].Command)
			native := probes[0].Read(out)

			// The native distribution is the image's own os-release ID,
			// lowercase, as the shell reads it.
			if id := strings.ToLower(run(t, c, osReleaseIDCommand)); native["os.distribution"] != id {
				t.Errorf("os.distribution = %v, the image's ID is %q", native["os.distribution"], id)
			}
			want := map[string]any{}
			for _, key := range ansibleDistributionFacts {
				// Ansible's NA, and its empty release, are what the mapping
				// leaves out rather than guessing.
				if v := ansible[key]; v != "" && v != "NA" {
					want[key] = v
				}
			}
			if got := ansiblefacts.ToAnsible(native); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: gathered %v, mapped to %v\nAnsible reported %v", ansible["image"], native, got, want)
			}
		})
	}
}
