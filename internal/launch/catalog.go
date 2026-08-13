package launch

import (
	"context"
	"errors"
	"fmt"
)

// This file is the catalog port: the enumerable set of definitions the
// platform can actually launch, and the check that a template's definition
// is one of them.
//
// It exists because the authoring surface shipped without it, and the
// result was recorded in this repository's own audit: the template form
// asked the operator to type a runbook id or playbook id into a free-text
// control, existence was checked nowhere at create and nowhere at launch,
// and the first failure was a failed job at fan-out, discovered later,
// possibly by somebody else. AWX's job template form auto-populates its
// Playbook field from a scan of the project's own base path; the defining
// property of a template form is that you choose what to run from what the
// platform knows exists. This port is that knowledge, expressed where the
// launch domain can consume it.
//
// It is interfaces only, which is what keeps this package's purity claim
// ("no ent, no HTTP, no runbook source, no filesystem") true. The concrete
// per-kind implementations are adapters over the real sources
// (internal/runbook, internal/playbook), wired in the composition root; a
// deployment that carries no playbook directory simply wires no playbook
// entry, and creating a playbook template is refused with a reason instead
// of succeeding against nothing.

// ErrDefinitionNotFound is returned when a template names a definition its
// kind's source cannot resolve.
//
// A distinct sentinel rather than a wrap of ErrInvalidTemplate, because
// the two failures belong to different people. An invalid template is a
// mistake in the submission; a missing definition may be a submission
// mistake or a runbook somebody deleted an hour ago, and a caller mapping
// errors onto a form wants to put this one on the definition control
// specifically.
var ErrDefinitionNotFound = errors.New("launch: no such definition exists")

// CatalogEntry is one launchable definition: a kind and the reference its
// source resolves.
type CatalogEntry struct {
	Kind       string
	Definition string
}

// KindCatalog lists and verifies one kind's definitions.
//
// Verify means "resolvable", not merely "present in a listing": the
// runbook adapter resolves through the same compile path a dispatch uses,
// so a runbook that exists but does not compile is refused at template
// create, which is the earliest moment anybody can be told.
type KindCatalog interface {
	// List returns every definition this kind can currently launch,
	// sorted by the underlying source.
	List(ctx context.Context) ([]string, error)

	// Verify reports whether definition resolves, returning an error
	// satisfying errors.Is(err, ErrDefinitionNotFound) when it does not
	// and a plain error for a source failure (which is an outage, not a
	// refusal, and must not be reported as "no such definition").
	Verify(ctx context.Context, definition string) error
}

// KindCatalogFuncs adapts two functions onto KindCatalog, which is how the
// composition root wires a real source in without this package importing
// it.
type KindCatalogFuncs struct {
	ListFunc   func(ctx context.Context) ([]string, error)
	VerifyFunc func(ctx context.Context, definition string) error
}

// List implements KindCatalog.
func (f KindCatalogFuncs) List(ctx context.Context) ([]string, error) { return f.ListFunc(ctx) }

// Verify implements KindCatalog.
func (f KindCatalogFuncs) Verify(ctx context.Context, definition string) error {
	return f.VerifyFunc(ctx, definition)
}

// Catalog is every launchable definition the deployment can see, across
// kinds.
type Catalog interface {
	// List returns every entry, grouped by kind in registry order and
	// sorted within a kind by the source's own ordering.
	List(ctx context.Context) ([]CatalogEntry, error)

	// Verify reports whether (kind, definition) is launchable here.
	Verify(ctx context.Context, kind, definition string) error
}

// sourceCatalog aggregates per-kind catalogs.
type sourceCatalog struct {
	kinds map[string]KindCatalog
}

// NewSourceCatalog builds a Catalog over the per-kind catalogs the
// composition root supplies, keyed by kind.
//
// A registered kind with no entry here is not an error at construction,
// exactly mirroring internal/adapters/routing.New's rule for adapters: a
// deployment that has never run Ansible legitimately wires no playbook
// source, and refusing to boot over a capability it does not use would be
// worse than refusing the one create that needs it. Such a kind lists
// nothing and verifies nothing, so a template of it cannot be created
// here, which the error says in those words.
func NewSourceCatalog(kinds map[string]KindCatalog) Catalog {
	owned := make(map[string]KindCatalog, len(kinds))
	for kind, catalog := range kinds {
		if catalog != nil {
			owned[kind] = catalog
		}
	}
	return &sourceCatalog{kinds: owned}
}

// List implements Catalog. Kinds iterate in registry order (Kinds() is
// sorted), so the picker built from this reads grouped and stable rather
// than in map order.
func (c *sourceCatalog) List(ctx context.Context) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	for _, d := range Kinds() {
		source, ok := c.kinds[d.Kind]
		if !ok {
			continue
		}
		definitions, err := source.List(ctx)
		if err != nil {
			// An outage in one source fails the whole listing rather than
			// silently narrowing it: a picker missing every playbook looks
			// exactly like a deployment that has none, and that ambiguity
			// is the one this platform keeps recording (a control built
			// over a partial answer is an affordance that misleads).
			return nil, fmt.Errorf("listing %s definitions: %w", d.Label, err)
		}
		for _, definition := range definitions {
			entries = append(entries, CatalogEntry{Kind: d.Kind, Definition: definition})
		}
	}
	return entries, nil
}

// Verify implements Catalog.
func (c *sourceCatalog) Verify(ctx context.Context, kind, definition string) error {
	source, ok := c.kinds[kind]
	if !ok {
		// The kind may be perfectly real (registered, routable) while this
		// deployment carries no source of its definitions. Refused as
		// not-found so one error path covers "never existed" and "cannot
		// exist here", with the message carrying the difference.
		return fmt.Errorf("%w: this deployment has no source of %q definitions", ErrDefinitionNotFound, kind)
	}
	return source.Verify(ctx, definition)
}

// StaticCatalog is a Catalog over a fixed entry set.
//
// It exists for composition in tests and for the smallest deployments,
// where the set of launchable definitions is known at wiring time. Verify
// is membership, so it makes the same promise the source-backed catalog
// does at the moment of wiring and no later: nothing static can notice a
// file deleted afterwards, which is why every real composition root wires
// NewSourceCatalog instead.
func StaticCatalog(entries ...CatalogEntry) Catalog { return staticCatalog(entries) }

type staticCatalog []CatalogEntry

// List implements Catalog.
func (c staticCatalog) List(context.Context) ([]CatalogEntry, error) {
	return append([]CatalogEntry(nil), c...), nil
}

// Verify implements Catalog.
func (c staticCatalog) Verify(_ context.Context, kind, definition string) error {
	for _, entry := range c {
		if entry.Kind == kind && entry.Definition == definition {
			return nil
		}
	}
	return fmt.Errorf("%w: no %s definition %q", ErrDefinitionNotFound, kind, definition)
}
