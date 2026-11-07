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
	slugs := make([]string, 0, len(measured.Images))
	for slug := range measured.Images {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		ansible := measured.Images[slug]
		t.Run(slug, func(t *testing.T) {
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
