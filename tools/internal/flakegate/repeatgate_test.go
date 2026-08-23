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
