// Tests for which package internal/catalog/builtins.go imports for an entry
// whose method lives somewhere other than the package its scaffold would
// write (FAILURE_PATTERNS 677).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
)

// TestWriteCatalogBuiltins_ImportsThePackageThatHoldsTheMethods is the
// aggregator half of FAILURE_PATTERNS 677. Every virt.clab method is
// registered from internal/catalog/virt/clab, the parent of the package the
// scaffold names for it (virt/clab/lab, virt/clab/node). Imports computed
// from the entries' names alone dropped virt/clab, so its methods were no
// longer registered, and named packages that hold no source, so the
// catalog no longer built.
//
// The tree is laid out as the real one is: a shared register.go in
// virt/clab, a state.go in virt/cml/link holding the entry's own package,
// and virt/clab/node holding only a test, which is no package to import.
func TestWriteCatalogBuiltins_ImportsThePackageThatHoldsTheMethods(t *testing.T) {
	root := t.TempDir()
	catalog := filepath.Join(root, "internal", "catalog")
	for _, f := range []string{
		"builtins.go",
		filepath.Join("virt", "clab", "register.go"),
		filepath.Join("virt", "clab", "node", "node_test.go"),
		filepath.Join("virt", "cml", "link", "state.go"),
	} {
		path := filepath.Join(catalog, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	collections := []collectionscaffold.Config{
		{Name: "virt.clab.lab.list"},
		{Name: "virt.clab.node.start"},
		{Name: "virt.cml.link.start"},
	}
	if err := writeCatalogBuiltins(root, collections); err != nil {
		t.Fatalf("writeCatalogBuiltins: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(catalog, "builtins.go"))
	if err != nil {
		t.Fatalf("reading generated builtins.go: %v", err)
	}
	content := string(data)

	const prefix = `"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/`
	for _, want := range []string{"virt/clab", "virt/cml/link"} {
		if strings.Count(content, prefix+want+`"`) != 1 {
			t.Errorf("builtins.go should import %s exactly once, got:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"virt/clab/lab", "virt/clab/node"} {
		if strings.Contains(content, prefix+unwanted+`"`) {
			t.Errorf("builtins.go imports %s, which holds no package source, got:\n%s", unwanted, content)
		}
	}
}

// TestWriteCatalogBuiltins_ErrorsWhenAPackageCannotBeRead keeps a directory
// that cannot be listed from reading as one with no source: a regular file
// where the entry's package should be is an error naming it, not a reason
// to import a parent instead.
func TestWriteCatalogBuiltins_ErrorsWhenAPackageCannotBeRead(t *testing.T) {
	root := t.TempDir()
	notADir := filepath.Join(root, "internal", "catalog", "virt", "clab")
	if err := os.MkdirAll(filepath.Dir(notADir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notADir, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := writeCatalogBuiltins(root, []collectionscaffold.Config{{Name: "virt.clab.lab.list"}})
	if err == nil || !strings.Contains(err.Error(), notADir) {
		t.Fatalf("writeCatalogBuiltins over a file where a package should be: got %v, want an error naming %s", err, notADir)
	}
}
