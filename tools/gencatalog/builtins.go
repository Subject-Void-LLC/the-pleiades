// This file decides which package internal/catalog/builtins.go imports for
// one catalogdata entry: the one its scaffold names, or, when the method is
// registered from further up, the package that really holds it.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// holdingPackage returns the package, relative to internal/catalog and with
// forward slashes, that builtins.go imports for an entry whose scaffold
// names packagePath. That is packagePath itself when its directory under
// root holds Go source, which after generateEntries is every entry the
// scaffold wrote. Otherwise it is the nearest parent below internal/catalog
// that does, because the entry's method is registered from there: every
// virt.clab method comes from internal/catalog/virt/clab, though the
// scaffold names virt/clab/lab, virt/clab/node and nine more for them.
// Importing the scaffold's names dropped virt/clab from builtins.go and named
// packages that do not exist (FAILURE_PATTERNS 677).
//
// When no directory on the way holds Go source, packagePath is returned as
// it is, the import the entry's own name gives, and the build that follows
// says it is missing.
func holdingPackage(root, packagePath string) (string, error) {
	catalog := filepath.Join(root, "internal", "catalog")
	// Walk up from the scaffold's package, one directory at a time, stopping
	// before internal/catalog itself, whose own source is builtins.go.
	for p := packagePath; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		ok, err := holdsGoSource(filepath.Join(catalog, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		if ok {
			return p, nil
		}
	}
	return packagePath, nil
}

// holdsGoSource reports whether dir holds a Go file that is not a test, the
// least a directory needs to be a package another one can import. A missing
// directory holds none.
func holdsGoSource(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true, nil
		}
	}
	return false, nil
}
