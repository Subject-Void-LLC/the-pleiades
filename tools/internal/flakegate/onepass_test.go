// The claim the one test pass rests on, held true against the source.
package flakegate

import (
	"bufio"
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNothingIsExcludedByTheIntegrationTag keeps `make test-full` honest.
//
// The one pass runs `go test -tags integration` and nothing untagged, on
// the claim that the tagged build is a superset of the untagged one. That
// is true only while no file is left out BY the tag: a `//go:build
// !integration` file would run in the old untagged pass and silently in
// neither pass now. This evaluates every file's build constraint the way
// the toolchain does, for this platform, with and without the tag, and
// refuses any file the tag turns off.
func TestNothingIsExcludedByTheIntegrationTag(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		expr, err := buildConstraint(path)
		if err != nil || expr == nil {
			return err
		}
		checked++
		without := expr.Eval(func(tag string) bool { return tag == runtime.GOOS || tag == runtime.GOARCH })
		with := expr.Eval(func(tag string) bool { return tag == runtime.GOOS || tag == runtime.GOARCH || tag == "integration" })
		if without && !with {
			t.Errorf("%s is built without the integration tag and left out with it, so `make test-full`, which runs only the tagged build, never tests it; drop the !integration or split the file", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("found no file with a build constraint, so this test checked nothing; the walk is misaimed")
	}
}

// TestBuildConstraintControl is the negative control: a file the tag
// excludes must be caught by the same evaluation the test above uses.
func TestBuildConstraintControl(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(path, []byte("//go:build !integration\n\npackage x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expr, err := buildConstraint(path)
	if err != nil || expr == nil {
		t.Fatalf("constraint = %v, %v", expr, err)
	}
	if !expr.Eval(func(string) bool { return false }) || expr.Eval(func(tag string) bool { return tag == "integration" }) {
		t.Fatal("the control file was not seen as excluded by the integration tag")
	}
}

// buildConstraint returns the file's //go:build expression, or nil when it
// has none. Only the header, before the package clause, is read, which is
// where the toolchain looks for it.
func buildConstraint(path string) (constraint.Expr, error) {
	f, err := os.Open(path) // #nosec G304 -- a file under this repository
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			return nil, nil
		}
		if constraint.IsGoBuild(line) {
			return constraint.Parse(line)
		}
	}
	return nil, scanner.Err()
}
