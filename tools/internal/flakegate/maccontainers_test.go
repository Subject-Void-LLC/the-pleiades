// Tests that every container package is either able to run on the Mac against
// Podman or has a written reason to stay on the machine that runs the rest of
// the gate, in tools/testgate/hosts.json, which pr-check shares the container
// pass between the two by.
package flakegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostsTable is the part of tools/testgate/hosts.json this test reads: each
// list maps a package's path in the module to its evidence or reason, and
// here_tests a Podman package's tests that stay here to theirs.
type hostsTable struct {
	Podman    map[string]string            `json:"podman"`
	Here      map[string]string            `json:"here"`
	HereTests map[string]map[string]string `json:"here_tests"`
}

// TestEveryContainerPackageIsClassified holds hosts.json to the Makefile's
// container list: every package it places is a container package, none is in
// both lists, every entry carries its evidence or reason, and every container
// package is placed. A container package added to DOCKER_DEPENDENT_PACKAGES
// fails here until it is placed, so a new one is never left out of both
// halves of the container pass (FAILURE_PATTERNS 424: a part nobody runs reads
// as a part that passed); one hosts.json does not name would run here, but
// silently, with no reason anyone wrote down.
func TestEveryContainerPackageIsClassified(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "..", "Makefile")))
	if err != nil {
		t.Fatal(err)
	}
	all := dockerDependentPackages.FindSubmatch(raw)
	if all == nil {
		t.Fatal("the Makefile no longer assigns DOCKER_DEPENDENT_PACKAGES as a block; update this test with it")
	}
	data, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "testgate", "hosts.json")))
	if err != nil {
		t.Fatal(err)
	}
	var table hostsTable
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatalf("tools/testgate/hosts.json: %v", err)
	}
	const prefix = "github.com/Subject-Void-LLC/the-pleiades/"
	docker := map[string]bool{}
	for _, p := range packagePath.FindAllString(string(all[1]), -1) {
		docker[strings.TrimPrefix(p, prefix)] = true
	}
	if len(docker) == 0 || len(table.Podman) == 0 {
		t.Fatalf("parsed %d container packages and %d for Podman, so this test would pass vacuously", len(docker), len(table.Podman))
	}
	for p, why := range table.Podman {
		if !docker[p] {
			t.Errorf("hosts.json runs %s on Podman, but it is not in DOCKER_DEPENDENT_PACKAGES", p)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("hosts.json runs %s on Podman with no evidence written", p)
		}
		if reason, stays := table.Here[p]; stays {
			t.Errorf("%s runs on Podman and is also written down as staying here (%s): pick one", p, reason)
		}
	}
	for p, why := range table.Here {
		if !docker[p] {
			t.Errorf("hosts.json keeps %s here, but it is not a container package: the entry is stale", p)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("hosts.json keeps %s here with no reason written", p)
		}
	}
	for p, tests := range table.HereTests {
		if _, ok := table.Podman[p]; !ok {
			t.Errorf("hosts.json keeps tests of %s here, but the package is not a Podman one", p)
		}
		for test, why := range tests {
			if strings.TrimSpace(why) == "" {
				t.Errorf("hosts.json keeps %s %s here with no reason written", p, test)
			}
		}
	}
	for p := range docker {
		_, podman := table.Podman[p]
		_, here := table.Here[p]
		if !podman && !here {
			t.Errorf("%s is a container package hosts.json does not place: add it to \"podman\" after a run on the Mac's Podman shows it passing, or to \"here\" with the reason it cannot run there", p)
		}
	}
}
