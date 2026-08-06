// Package syncplugin defines the PLAN.md Section 6a sync plugin port: the
// pluggable connector every external inventory source implements so the
// engine can pull devices from NetBox, AWS, a Cisco Catalyst Center, or a
// static YAML file without knowing which one it is talking to.
//
// The package is named syncplugin rather than sync because a package named
// sync would shadow the standard library's sync in every file that needs
// both, and this package's own reconciliation path needs neither the
// collision nor the import alias it would force on callers. The interface
// inside is therefore named Plugin, so the qualified name reads
// syncplugin.Plugin and no name stutters.
//
// CODE_SCAFFOLD.md Section J writes this interface as SyncPlugin with
// Sync() taking no arguments. Sync here takes the Repository it reconciles
// into, because a plugin that reaches for a repository it was not handed
// would have to construct one, and constructing one is a composition-root
// decision (which tier, which backend) that no plugin is entitled to make.
//
// Why this interface exists now and did not before: internal/inventory's
// StaticYAMLPlugin deliberately refused to build it, on the grounds that
// one static-file implementation is not enough evidence to design a
// four-method port around. A live Catalyst Center is the second consumer
// that changes that, and the two implementations are kept deliberately
// unalike (one reads a local file with no network, no auth, and no
// paging; the other pages an authenticated REST API) so the port is shaped
// by both rather than by whichever was written first.
package syncplugin

import (
	"context"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
)

// Plugin is the four-stage contract every inventory source satisfies. The
// stages are ordered and each depends on the one before it: Connect
// authenticates, Discover streams raw upstream data, Classify turns one raw
// item into a device type and capability set, and Sync drives the whole
// pipeline and reconciles the result into the local inventory.
//
// Section 6b names the pipeline SYNC -> CLASSIFY -> DISCOVER -> PROVISION.
// That ordering describes the onboarding lifecycle of a single device;
// the method ordering here describes the plugin's own call sequence, which
// is Connect -> Discover -> Classify per item -> reconcile. Provisioning is
// deliberately absent: it acts on devices after they land in inventory and
// belongs to the onboarding pipeline, not to the connector.
type Plugin interface {
	// Connect authenticates against the upstream system described by cfg.
	// It must be called before Discover or Sync, and it must be safe to
	// call more than once so a long-lived plugin can re-authenticate after
	// a token expires.
	Connect(ctx context.Context, cfg Config) error

	// Discover streams raw upstream device data as Records. The Records it
	// yields are unclassified: Type, Capabilities, and State are not yet
	// meaningful, and Properties carries whatever the upstream system
	// supplied. Callers must Close the iterator.
	Discover(ctx context.Context) (RecordIterator, error)

	// Classify maps one discovered Record onto a concrete device type and
	// capability set. A Record it cannot place must come back as a
	// Quarantine Classification carrying a reason, never as an error and
	// never as a silent default: Section 6g requires an unclassifiable
	// device stay visible for manual review rather than vanish.
	Classify(ctx context.Context, rec record.Record) (Classification, error)

	// Sync runs the full pipeline and reconciles every discovered device
	// into repo, returning what changed. Implementations should call
	// Reconcile rather than hand-rolling the add/update/conflict decision,
	// so every plugin agrees on what those words mean.
	Sync(ctx context.Context, repo inventory.Repository) (Reconciliation, error)

	// Close releases whatever Connect acquired: pooled connections, cached
	// tokens, open file handles. It must be safe to call on a plugin that
	// was never connected, and safe to call more than once.
	//
	// It is part of the port because Connect is. A contract that acquires
	// resources and offers no way to release them leaves every caller to
	// either leak them for the process lifetime or reach past this
	// interface to whatever concrete client is underneath. A plugin with
	// nothing to release (the static YAML one) returns nil, which costs it
	// one method and keeps the lifecycle symmetric for everyone else.
	Close() error
}

// RecordIterator streams discovered Records without materializing the whole
// upstream fleet in memory. It mirrors inventory.Iterator's contract
// exactly (Next advances and reports whether an item is available, Error
// reports why iteration stopped, Close releases the underlying cursor) but
// yields a pre-classification record.Record rather than a hydrated
// InventoryItem, because at Discover time there is not yet a device type to
// hydrate through.
type RecordIterator interface {
	// Next advances the cursor. It returns true if a Record is available,
	// or false at the end of the stream or on error.
	Next(ctx context.Context) bool

	// Record returns the Record at the current cursor position.
	Record() record.Record

	// Error returns whatever ended iteration early, or nil if the stream
	// simply ran out.
	Error() error

	// Close releases the underlying page cursor, HTTP body, or file handle.
	Close() error
}
