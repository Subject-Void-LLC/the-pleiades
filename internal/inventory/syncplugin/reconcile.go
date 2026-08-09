package syncplugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Outcome is what reconciliation decided about one discovered device.
type Outcome string

const (
	// OutcomeAdded means the device was not in inventory and was created.
	OutcomeAdded Outcome = "added"

	// OutcomeUpdated means the device existed and at least one property
	// changed upstream, so a new revision was recorded.
	OutcomeUpdated Outcome = "updated"

	// OutcomeUnchanged means the device existed and nothing differed. No
	// write happened, deliberately: burning a version on a no-op write
	// produces a revision-free bump that later readers cannot explain.
	OutcomeUnchanged Outcome = "unchanged"

	// OutcomeQuarantined means classification could not place the device.
	// Section 6g requires it stay visible for manual review rather than be
	// silently dropped, which is what reporting it here achieves.
	OutcomeQuarantined Outcome = "quarantined"

	// OutcomeConflict means another sync plugin already owns the device.
	// Section 11 allows exactly one authoritative source per item, so the
	// second source reports the collision instead of overwriting.
	OutcomeConflict Outcome = "conflict"

	// OutcomeWouldAdd and OutcomeWouldUpdate are what OutcomeAdded and
	// OutcomeUpdated become when the inventory is open read-only.
	//
	// They exist because a dry run and a hard refusal are different
	// requests that share a mechanism. inventory.NewReadOnlyRepository is
	// right to fail loudly: a component that must not write should be told
	// so, not silently ignored. But a user running `inventory sync
	// --read-only` is asking what would change, and aborting on the first
	// device answers nothing. Catching the refusal here turns the same
	// guard into the simulate-first proof, with no second code path that
	// could drift from the real one: every decision above this point is
	// identical, and only the final write is intercepted.
	OutcomeWouldAdd Outcome = "would add"

	// OutcomeWouldUpdate is OutcomeUpdated under a read-only inventory.
	OutcomeWouldUpdate Outcome = "would update"
)

// DeviceResult is one device's reconciliation outcome and, when the outcome
// needs explaining, why.
type DeviceResult struct {
	// Name is the device's upstream name.
	Name string

	// Outcome is what reconciliation did about it.
	Outcome Outcome

	// Reason explains a quarantine or a conflict, and is empty otherwise.
	Reason string
}

// Reconciliation is the report a Sync returns: every device it saw and what
// happened to it. It is a list rather than a set of counters because
// "twelve quarantined" is not actionable and "these twelve, for these
// reasons" is.
type Reconciliation struct {
	// Results holds one entry per discovered device, in discovery order.
	Results []DeviceResult
}

// Count returns how many devices ended with the given outcome.
func (r Reconciliation) Count(o Outcome) int {
	n := 0
	for _, res := range r.Results {
		if res.Outcome == o {
			n++
		}
	}
	return n
}

// Total returns how many devices were discovered.
func (r Reconciliation) Total() int { return len(r.Results) }

// add appends one device's outcome to the report.
func (r *Reconciliation) add(name string, o Outcome, reason string) {
	r.Results = append(r.Results, DeviceResult{Name: name, Outcome: o, Reason: reason})
}

// Reconcile drives the full discover-classify-write pipeline for p and is
// what every Plugin's Sync should call rather than reimplementing. Sharing
// it is the point: "added", "updated", and "conflict" have to mean the same
// thing for every source, or a reconciliation report stops being comparable
// across plugins and the conformance suite has nothing to assert.
//
// Two behaviors are worth stating because they are choices, not oversights.
// A property present in inventory but absent upstream is left alone rather
// than deleted, because Section 11 lets a second source enrich a device it
// does not own, and deleting on every sync would erase that enrichment. And
// a device that classification could not place is reported but not
// persisted, because building an item requires a device type and there is
// no generic unclassified type registered to hydrate one as; the full
// Section 6g quarantine bucket needs that type to exist first.
func Reconcile(
	ctx context.Context,
	p Plugin,
	cfg Config,
	repo inv.Repository,
	factory *inv.ItemFactory,
) (Reconciliation, error) {
	var report Reconciliation

	it, err := p.Discover(ctx)
	if err != nil {
		return report, fmt.Errorf("sync plugin %q: discovery failed: %w", cfg.Name, err)
	}
	defer func() {
		// Close's error is deliberately dropped: the reconciliation result
		// is already computed by this point, and failing the whole sync
		// because a cursor could not be released would discard real work
		// over a cleanup problem.
		_ = it.Close()
	}()

	syncedAt := time.Now().UTC()

	for it.Next(ctx) {
		raw := it.Record()

		cls, err := p.Classify(ctx, raw)
		if err != nil {
			// Classify is contractually required to quarantine rather than
			// error, so an error here is a plugin defect and stopping is
			// the honest response: continuing would silently skip devices.
			return report, fmt.Errorf("sync plugin %q: classifying %s: %w", cfg.Name, raw.Name, err)
		}

		if cls.Quarantined() {
			report.add(raw.Name, OutcomeQuarantined, cls.Reason)
			continue
		}

		outcome, reason, err := reconcileOne(ctx, cfg, repo, factory, raw, cls, syncedAt)
		if err != nil {
			return report, err
		}
		report.add(raw.Name, outcome, reason)
	}

	if err := it.Error(); err != nil {
		return report, fmt.Errorf("sync plugin %q: discovery stream failed: %w", cfg.Name, err)
	}
	return report, nil
}

// reconcileOne decides and applies the outcome for a single classified
// device.
func reconcileOne(
	ctx context.Context,
	cfg Config,
	repo inv.Repository,
	factory *inv.ItemFactory,
	raw record.Record,
	cls Classification,
	syncedAt time.Time,
) (Outcome, string, error) {
	source := inventory.SourceAuthority{Plugin: cfg.Name, SyncedAt: syncedAt}

	existing, err := repo.GetByName(ctx, raw.Name)
	if err != nil {
		if !errors.Is(err, inv.ErrItemNotFound) {
			return "", "", fmt.Errorf("sync plugin %q: loading %s: %w", cfg.Name, raw.Name, err)
		}
		return createDevice(ctx, cfg, repo, factory, raw, cls, source)
	}

	// Section 11's One Authority Per Item. An empty owner means the device
	// predates provenance tracking (a hand-written hosts.yaml entry), which
	// this source may adopt; a different owner may not be overwritten.
	if owner := existing.Source().Plugin; owner != "" && owner != cfg.Name {
		return OutcomeConflict, fmt.Sprintf("device is owned by sync plugin %q", owner), nil
	}

	return updateDevice(ctx, cfg, repo, factory, existing, raw, cls, source)
}

// createDevice inserts a device this source has not seen before.
func createDevice(
	ctx context.Context,
	cfg Config,
	repo inv.Repository,
	factory *inv.ItemFactory,
	raw record.Record,
	cls Classification,
	source inventory.SourceAuthority,
) (Outcome, string, error) {
	rec := raw
	rec.Type = cls.Type
	rec.Capabilities = cls.Capabilities
	rec.State = cls.State
	rec.Source = source

	item, err := factory.Build(rec)
	if err != nil {
		return "", "", fmt.Errorf("sync plugin %q: building %s: %w", cfg.Name, raw.Name, err)
	}
	if err := repo.Create(ctx, item); err != nil {
		if errors.Is(err, inv.ErrInventoryReadOnly) {
			return OutcomeWouldAdd, "inventory is open read-only", nil
		}
		return "", "", fmt.Errorf("sync plugin %q: creating %s: %w", cfg.Name, raw.Name, err)
	}
	return OutcomeAdded, "", nil
}

// updateDevice applies upstream changes to a device this source already
// owns.
//
// It rebuilds the item from the stored state rather than from the upstream
// record, then replays each upstream difference through AddInfo. That
// ordering is what makes the audit trail truthful: every Revision records
// the value that was actually replaced, which a rebuild from upstream data
// could not know.
func updateDevice(
	ctx context.Context,
	cfg Config,
	repo inv.Repository,
	factory *inv.ItemFactory,
	existing inventory.InventoryItem,
	raw record.Record,
	cls Classification,
	source inventory.SourceAuthority,
) (Outcome, string, error) {
	stored := existing.Properties().Raw()

	rec := record.Record{
		ID:           existing.ID(),
		Name:         existing.Name(),
		Type:         cls.Type,
		Properties:   stored,
		Tags:         existing.Tags(),
		State:        cls.State,
		Source:       source,
		Version:      existing.Version(),
		History:      existing.History(),
		Capabilities: cls.Capabilities,
	}

	item, err := factory.Build(rec)
	if err != nil {
		return "", "", fmt.Errorf("sync plugin %q: rebuilding %s: %w", cfg.Name, raw.Name, err)
	}

	for key, upstream := range raw.Properties {
		if current, ok := stored[key]; ok && reflect.DeepEqual(current, upstream) {
			continue
		}
		if err := item.AddInfo(key, upstream, true); err != nil {
			return "", "", fmt.Errorf("sync plugin %q: updating %s property %q: %w", cfg.Name, raw.Name, key, err)
		}
	}

	// Save is a no-op when nothing changed, so an unchanged device produces
	// no write and no version bump. Reporting it separately from "updated"
	// is what lets an operator see that a sync ran and found nothing new.
	if item.Version() == existing.Version() {
		return OutcomeUnchanged, "", nil
	}

	if err := repo.Save(ctx, item); err != nil {
		if errors.Is(err, inv.ErrInventoryReadOnly) {
			return OutcomeWouldUpdate, "inventory is open read-only", nil
		}
		return "", "", fmt.Errorf("sync plugin %q: saving %s: %w", cfg.Name, raw.Name, err)
	}
	return OutcomeUpdated, "", nil
}
