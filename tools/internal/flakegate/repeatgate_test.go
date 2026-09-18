package flakegate

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// TestEveryContainerPackageIsListed fails when a package whose test files
// import testcontainers-go is missing from the Makefile's
// DOCKER_DEPENDENT_PACKAGES.
//
// The list's own comment promises that a missing package fails loudly, and
// it does, but only as a container that never becomes ready under
// test-repeat's -count=3 load, twenty minutes into a gate. That is how
// internal/keyregistry was found missing. This reads every _test.go file's
// imports, in every build configuration, and says so in a second.
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

	found := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if !strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), "github.com/testcontainers/testcontainers-go") {
				continue
			}
			found++
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg := "github.com/Subject-Void-LLC/the-pleiades/" + filepath.ToSlash(rel)
			if !listed[pkg] {
				t.Errorf("%s imports testcontainers-go, so %s provisions real containers and belongs in the Makefile's DOCKER_DEPENDENT_PACKAGES; without it `make test-repeat` runs it three times over under full load", path, pkg)
			}
			break
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	if found == 0 {
		t.Fatal("found no test file importing testcontainers-go, so this walk is misaimed")
	}
}
