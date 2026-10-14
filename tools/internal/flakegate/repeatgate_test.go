package flakegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// dockerDependentPackages captures the Makefile's own exclusion list, which
// is written as one backslash-continued assignment.
var dockerDependentPackages = regexp.MustCompile(`(?ms)^DOCKER_DEPENDENT_PACKAGES :=(.*?)\n\n`)

// packagePath matches one module-qualified import path inside that block.
var packagePath = regexp.MustCompile(`github\.com/Subject-Void-LLC/the-pleiades[^\s\\]*`)

// TestEveryFlakyPackageIsExcludedFromTestRepeat is what allows `make
// test-repeat` to be a bare `go test` in `push-gate`, where every other
// test target goes through this package's tolerance instead.
//
// push-gate's contract is that a failure confined to a flaky-packages.json
// package warns rather than blocks, and a target added to push-gate without
// that tolerance breaks the contract. test-repeat is exempt for a reason
// rather than by oversight: it reuses test-no-docker's package filter, and
// every package flaky-packages.json names is in that filter, so a bare `go
// test` there cannot encounter a package the tolerance would have covered.
// The tolerance is a no-op for this target by construction.
//
// That is a claim about two lists nothing else connects, which is why it is
// a test rather than a sentence in the Makefile. Add a flaky package that
// is not container-backed, or drop a package from the exclusion list, and
// the reasoning silently stops holding: push-gate would start failing on a
// package the project has already written down as untrustworthy under load.
func TestEveryFlakyPackageIsExcludedFromTestRepeat(t *testing.T) {
	root := filepath.Join("..", "..", "..")

	rawFlaky, err := os.ReadFile(filepath.Clean(filepath.Join(root, "flaky-packages.json")))
	if err != nil {
		t.Fatalf("reading flaky-packages.json: %v", err)
	}
	var flaky struct {
		Packages []struct {
			ImportPath string `json:"import_path"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(rawFlaky, &flaky); err != nil {
		t.Fatalf("parsing flaky-packages.json: %v", err)
	}
	if len(flaky.Packages) == 0 {
		t.Fatal("flaky-packages.json lists no packages, so this test would pass vacuously")
	}

	rawMakefile, err := os.ReadFile(filepath.Clean(filepath.Join(root, "Makefile")))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	block := dockerDependentPackages.FindSubmatch(rawMakefile)
	if block == nil {
		t.Fatal("the Makefile no longer assigns DOCKER_DEPENDENT_PACKAGES as one block; " +
			"if it was renamed or reshaped, update this test with it")
	}

	excluded := map[string]bool{}
	for _, p := range packagePath.FindAllString(string(block[1]), -1) {
		excluded[p] = true
	}
	if len(excluded) == 0 {
		t.Fatal("parsed no packages out of DOCKER_DEPENDENT_PACKAGES, so this test would pass vacuously")
	}

	for _, p := range flaky.Packages {
		if !excluded[p.ImportPath] {
			t.Errorf("%s is listed in flaky-packages.json but is NOT in the Makefile's "+
				"DOCKER_DEPENDENT_PACKAGES, so `make test-repeat` runs it. test-repeat is a bare "+
				"`go test` in push-gate and has no tolerance, so a flake there now BLOCKS a push. "+
				"Either exclude it from test-repeat as well, or route test-repeat through testgate.",
				p.ImportPath)
		}
	}
}

// oneShotContainerPackages are the packages whose tests start a container
// yet stay out of DOCKER_DEPENDENT_PACKAGES, each for the reason written
// beside it. An entry may only name a package that starts nothing but
// one-shot docker or podman commands: a testcontainers-go fixture, waited
// on and kept running, is what fails under the fast tier's load, and no
// reason excuses one.
var oneShotContainerPackages = map[string]string{
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine": "one differential test builds the Ansible runner image and lists a playbook's tasks in one `docker run --rm`, with nothing to wait on; " +
		"listing the package would drop every other engine test from the macOS job, which has no Docker",
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook": "two differential tests build the Ansible runner image and read ansible-core in one `docker run --rm` each, with nothing to wait on; " +
		"listing the package would drop the converter's other tests from the macOS job, which has no Docker",
}

// TestEveryContainerPackageIsListed fails when a package whose tests start
// a container is missing from the Makefile's DOCKER_DEPENDENT_PACKAGES,
// unless oneShotContainerPackages names it and it starts no fixture.
//
// The list's own comment promises that a missing package fails loudly, and
// it does, but only as a container that never becomes ready under the fast
// tier's load, twenty minutes into a CI job. That is how internal/keyregistry
// was found missing, and how pkg/kubeapi was: it starts k3s through
// testsupport.StartK3s and imports no testcontainers-go itself, which the
// first version of this guard read for. This one follows each test file's
// calls through every function in the module (scanForContainers), so a
// helper's container counts, and says so in a second.
func TestEveryContainerPackageIsListed(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	rawMakefile, err := os.ReadFile(filepath.Clean(filepath.Join(root, "Makefile")))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	block := dockerDependentPackages.FindSubmatch(rawMakefile)
	if block == nil {
		t.Fatal("the Makefile no longer assigns DOCKER_DEPENDENT_PACKAGES as one block")
	}
	listed := map[string]bool{}
	for _, p := range packagePath.FindAllString(string(block[1]), -1) {
		listed[p] = true
	}

	scan, err := scanForContainers(root, "github.com/Subject-Void-LLC/the-pleiades")
	if err != nil {
		t.Fatalf("reading the module: %v", err)
	}
	starting, fixtures := scan.containerPackages(false), scan.containerPackages(true)
	if len(fixtures) == 0 {
		t.Fatal("found no package starting a testcontainers-go fixture, so this scan is misaimed")
	}
	for pkg, why := range starting {
		if listed[pkg] {
			continue
		}
		if _, oneShot := oneShotContainerPackages[pkg]; oneShot && fixtures[pkg] == "" {
			continue
		}
		if fixture := fixtures[pkg]; fixture != "" {
			why = fixture
		}
		t.Errorf("%s's tests start a container (%s), so it belongs in the Makefile's DOCKER_DEPENDENT_PACKAGES; "+
			"without it the fast tier runs it beside every other package and `make test-repeat` three times over", pkg, why)
	}
	for pkg := range oneShotContainerPackages {
		switch {
		case listed[pkg]:
			t.Errorf("%s is in DOCKER_DEPENDENT_PACKAGES and oneShotContainerPackages; drop it from one", pkg)
		case starting[pkg] == "":
			t.Errorf("%s starts no container any more; drop it from oneShotContainerPackages", pkg)
		}
	}
}
