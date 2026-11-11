// Package main: the built-in methods' manifests, for a Controller that links
// no Collection (FAILURE_PATTERNS 841). Its device admission, its rollback
// route and its runbook builder ask about built-in methods, and a registry
// lookup in this binary finds none, so the generated module catalog it
// embeds and serves is installed as their answer before anything can ask.
//
// It is installed by this package's init rather than by a call in main, so
// the Controller's own test binary runs the same wiring the server does: a
// test cannot call main, and a call there that went missing would leave
// every test green (review of 841, the class of 837).
package main

import (
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func init() {
	if err := installBuiltinCatalog(); err != nil {
		// The catalog is compiled in, so this fails only for a build whose
		// generated catalog is broken: refuse to start rather than check
		// nothing.
		panic(fmt.Sprintf("failed to install the built-in method catalog: %v", err))
	}
}

// installBuiltinCatalog installs the embedded module catalog as this
// binary's built-in manifests (collection.InstallCatalog).
func installBuiltinCatalog() error {
	return collection.InstallCatalog(wellknown.ModuleCatalog)
}

// newFactsSubscriber is the Controller's facts consumer over store: each
// batch a Runner publishes is held to the built-in methods' fact
// declarations, read from the module catalog this binary embeds
// (FAILURE_PATTERNS 837), with opts after (the digest keys). main calls it,
// and so does its test.
func newFactsSubscriber(store facts.Store, logger *slog.Logger, admit journal.AdmissionCheck, opts ...entstore.SubscriberOption) (*entstore.Subscriber, error) {
	index, err := entstore.CatalogIndex(wellknown.ModuleCatalog)
	if err != nil {
		return nil, err
	}
	// A fact a built-in holds from a method this catalog lacks is kept and
	// noted at info: which methods are built-in is the Runner's release's to
	// say (FP 1020; the Runner's loader is what refuses a program's).
	builtins, err := entstore.BuiltinMethods(wellknown.ModuleCatalog)
	if err != nil {
		return nil, err
	}
	base := []entstore.SubscriberOption{entstore.WithDeclarations(index), entstore.WithBuiltinProvenance(builtins)}
	return entstore.NewSubscriber(store, logger, admit, append(base, opts...)...), nil
}
