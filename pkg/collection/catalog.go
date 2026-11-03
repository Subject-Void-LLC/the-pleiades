// Package collection: the manifests of the built-in methods, for a binary
// that registers none (the Controller). A Controller links no Collection, so
// every registry lookup it made found nothing: its device admission checked
// no method, and its rollback route refused every recorded undo as "not a
// built-in method this build runs". The generated module catalog it embeds
// (internal/api/wellknown) holds every built-in method's manifest, which is
// all those questions need; its composition root installs it here once, and
// BuiltinManifest answers from the registry first and the catalog second.
package collection

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// builtinCatalog is the installed catalog: built-in method manifests by
// name. Installed at most once, at start, before any reader runs.
var builtinCatalog struct {
	sync.RWMutex
	manifests map[string]Manifest
}

// ErrCatalogInstalled marks a second InstallCatalog.
var ErrCatalogInstalled = errors.New("collection: a built-in catalog is already installed")

// InstallCatalog installs catalog, the generated module catalog's JSON (a
// map of method name to Manifest), as the built-in manifests of a binary
// that registers no Collection. It refuses a second install, a catalog that
// does not decode, and one with no entries, so a composition root that wires
// it wrongly stops at start rather than checking nothing.
func InstallCatalog(catalog []byte) error {
	var manifests map[string]Manifest
	if err := json.Unmarshal(catalog, &manifests); err != nil {
		return fmt.Errorf("collection: reading the module catalog: %w", err)
	}
	if len(manifests) == 0 {
		return errors.New("collection: the module catalog holds no method")
	}
	builtinCatalog.Lock()
	defer builtinCatalog.Unlock()
	if builtinCatalog.manifests != nil {
		return ErrCatalogInstalled
	}
	builtinCatalog.manifests = manifests
	return nil
}

// BuiltinManifest returns the manifest of name when it is a built-in method:
// one registered here with no Provider (not an external Collection's), or,
// when it is not registered at all, one in the installed catalog. An external
// Collection's method is never built-in, whatever the catalog says.
func BuiltinManifest(name string) (Manifest, bool) {
	if d, ok := Lookup(name); ok {
		return d.Manifest, d.Provider == nil
	}
	builtinCatalog.RLock()
	defer builtinCatalog.RUnlock()
	m, ok := builtinCatalog.manifests[name]
	return m, ok
}

// CatalogForTest removes an installed catalog, returning the function that
// puts it back, for a test that installs its own.
func CatalogForTest() func() {
	builtinCatalog.Lock()
	saved := builtinCatalog.manifests
	builtinCatalog.manifests = nil
	builtinCatalog.Unlock()
	return func() {
		builtinCatalog.Lock()
		builtinCatalog.manifests = saved
		builtinCatalog.Unlock()
	}
}

// ManifestOf returns the manifest of name whatever provides it: a method
// registered here (a built-in or an external Collection's), or, when none
// is, one in the installed catalog.
func ManifestOf(name string) (Manifest, bool) {
	if d, ok := Lookup(name); ok {
		return d.Manifest, true
	}
	builtinCatalog.RLock()
	defer builtinCatalog.RUnlock()
	m, ok := builtinCatalog.manifests[name]
	return m, ok
}
