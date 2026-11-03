// Package main: the built-in methods' manifests, for a Controller that links
// no Collection (FAILURE_PATTERNS 841). Its device admission and its rollback
// route ask about built-in methods, and a registry lookup in this binary
// finds none, so the generated module catalog it embeds and serves is
// installed as their answer before anything can ask.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// installBuiltinCatalog installs the embedded module catalog as this
// binary's built-in manifests (collection.InstallCatalog).
func installBuiltinCatalog() error {
	return collection.InstallCatalog(wellknown.ModuleCatalog)
}
