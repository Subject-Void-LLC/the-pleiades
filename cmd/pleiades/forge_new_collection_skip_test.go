// Tests for `forge new-collection --skip-existing` when the method an entry
// names is already registered from a file of another name, or from another
// package (FAILURE_PATTERNS 677).
package main

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// TestForgeNewCollectionSkipExisting_SkipsARegisteredMethod is FAILURE_PATTERNS
// 677. `forge new-collection --skip-existing` decided an entry was new by the
// names of the files its scaffold would write, so a method registered in a
// file of another name was scaffolded again on every run of tools/gencatalog,
// and the stub registered it a second time. The cases are the three shapes the
// catalog really has: virt.cml.link.start registered from a table in state.go,
// virt.cml.capture.stop registered on its own in capture.go, and every
// virt.clab method registered from a map in internal/catalog/virt/clab, the
// parent of the package the scaffold would write.
//
// The command runs in this binary, which links the whole built-in catalog as
// the pleiades binary tools/gencatalog builds does, so the registry it asks is
// the real one. The directory is empty: what decides is the method, not the
// file names, and an empty directory is where the old check could only say
// "new".
func TestForgeNewCollectionSkipExisting_SkipsARegisteredMethod(t *testing.T) {
	cases := []struct {
		name string
		fqcn string
		// written is whether the run must write the scaffold's files.
		written bool
	}{
		{name: "registered from a table in a shared file", fqcn: "virt.cml.link.start"},
		{name: "registered on its own in a shared file", fqcn: "virt.cml.capture.stop"},
		{name: "registered from a map in the parent package", fqcn: "virt.clab.lab.list"},
		// The control: a name nothing registers is still scaffolded, so an
		// empty directory below is the skip and not a run that writes nothing.
		{name: "a name nothing registers", fqcn: "test.forgeskip.start", written: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{tc.fqcn, "--transports", "https", "--engine-version", ">=0.3.0", "--skip-existing", "--dir", dir}
			if err := runForgeNewCollection(args); err != nil {
				t.Fatalf("forge new-collection %v: %v", args, err)
			}
			files := filesBelow(t, dir)
			switch {
			case tc.written && len(files) == 0:
				t.Errorf("%s is registered by nothing, yet no file was written", tc.fqcn)
			case !tc.written && len(files) != 0:
				t.Errorf("%s is already registered, yet the run wrote %v, a second registration of it", tc.fqcn, files)
			}
		})
	}
}

// filesBelow lists every regular file under root, as paths relative to it.
func filesBelow(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s: %v", root, err)
	}
	return files
}
