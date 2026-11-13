package syncplugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
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
	// OutcomeRefused means the upstream name cannot name a device
	// (recordname.Check): it is "", "." or "..", which no URL can carry, so
	// the device could be stored but never opened, changed or deleted by its
	// address; or it holds a control code, a line break, a direction control
	// or an invisible character, which would make every line naming the
	// device say something else. It is reported with the reason and not
	// stored, and the sync goes on with the rest. Only a new device is
	// refused: one already stored under such a name, from before the rule,
	// is updated as any other. Failing the whole sync instead would let one
	// record nobody here controls stop every other device's updates on
	// every run until someone renamed it upstream.
	OutcomeRefused Outcome = "refused"
)

// DeviceResult is one device's reconciliation outcome and, when the outcome
// needs explaining, why.
type DeviceResult struct {
	// Name is the device's upstream name.
	Name string

	// Outcome is what reconciliation did about it.
	Outcome Outcome

	// Reason explains a quarantine, a conflict, a refused name
	// (recordname's reason) or a write a read-only inventory turned into a
	// would-add or would-update, and is empty otherwise.
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

		// A name no device may be created under is refused before
		// classifying, since no classification makes it usable, but only
		// when no device is stored under it: only a create is held to the
		// rule, so a device stored before the rule changed is updated like
		// any other (LESSONS 415).
		if err := recordname.Check(raw.Name); err != nil {
			stored, lookupErr := storedUnder(ctx, repo, raw.Name)
			if lookupErr != nil {
				return report, fmt.Errorf("sync plugin %q: loading %q: %w", cfg.Name, raw.Name, lookupErr)
			}
			if !stored {
				report.add(raw.Name, OutcomeRefused, err.Error())
				continue
			}
		}

		cls, err := p.Classify(ctx, raw)
		if err != nil {
			// Classify is contractually required to quarantine rather than
			// error, so an error here is a plugin defect and stopping is
			// the honest response: continuing would silently skip devices.
			return report, fmt.Errorf("sync plugin %q: classifying %q: %w", cfg.Name, raw.Name, err)
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

// storedUnder reports whether repo holds a device under name, and returns
// any error but the device's absence.
func storedUnder(ctx context.Context, repo inv.Repository, name string) (bool, error) {
	_, err := repo.GetByName(ctx, name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, inv.ErrItemNotFound):
		return false, nil
	default:
		return false, err
	}
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
			return "", "", fmt.Errorf("sync plugin %q: loading %q: %w", cfg.Name, raw.Name, err)
		}
		return createDevice(ctx, cfg, repo, factory, raw, cls, source)
	}

	// Section 11's One Authority Per Item. A different owner may not be
	// overwritten. An empty owner is a device added by hand (add-host, a
	// hosts.yaml entry): the same name is no proof it is the same machine,
	// so it is a conflict too, unless the sync was told to adopt it by name.
	switch owner := existing.Source().Plugin; {
	case owner != "" && owner != cfg.Name:
		return OutcomeConflict, fmt.Sprintf("device is owned by sync plugin %q", owner), nil
	case owner == "" && !cfg.Adopts(raw.Name):
		return OutcomeConflict, fmt.Sprintf("device was added by hand and no sync owns it; name it with --adopt %s to let sync plugin %q take it over", raw.Name, cfg.Name), nil
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
	for key := range raw.Properties {
		if inventory.IsReservedProperty(key) {
			return "", "", fmt.Errorf("sync plugin %q: %q sets property %s, which only %s writes", cfg.Name, raw.Name, key, inventory.ReservedPropertyWriter(key))
		}
	}
	rec := raw
	rec.Type = cls.Type
	rec.Capabilities = cls.Capabilities
	rec.State = cls.State
	rec.Source = source

	item, err := factory.Build(rec)
	if err != nil {
		return "", "", fmt.Errorf("sync plugin %q: building %q: %w", cfg.Name, raw.Name, err)
	}
	if err := repo.Create(ctx, item); err != nil {
		if errors.Is(err, inv.ErrInventoryReadOnly) {
			return OutcomeWouldAdd, "inventory is open read-only", nil
		}
		return "", "", fmt.Errorf("sync plugin %q: creating %q: %w", cfg.Name, raw.Name, err)
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
		ID:         existing.ID(),
		Name:       existing.Name(),
		Type:       cls.Type,
		Properties: stored,
		Tags:       existing.Tags(),
		// The stored state, never the classification's. A classification's
		// state is where a device lands when a source first adds it; after
		// that the lifecycle is an operator's decision, and rebuilding with
		// the landing state put a promoted device back to simulate-locked on
		// the next upstream change, with no revision saying so.
		State:        existing.State(),
		Source:       source,
		Version:      existing.Version(),
		History:      existing.History(),
		Capabilities: cls.Capabilities,
	}

	item, err := factory.Build(rec)
	if err != nil {
		return "", "", fmt.Errorf("sync plugin %q: rebuilding %q: %w", cfg.Name, raw.Name, err)
	}

	for key, upstream := range raw.Properties {
		if current, ok := stored[key]; ok && reflect.DeepEqual(current, upstream) {
			continue
		}
		if err := item.AddInfo(key, upstream, true); err != nil {
			return "", "", fmt.Errorf("sync plugin %q: updating %q property %q: %w", cfg.Name, raw.Name, key, err)
		}
	}

	// Save is a no-op when nothing changed, so an unchanged device produces
	// no write and no version bump. Reporting it separately from "updated"
	// is what lets an operator see that a sync ran and found nothing new.
	// An adoption changes the owner even when nothing else moved. It is
	// recorded as a revision, so the owner is saved (a repository saves
	// only a moved version) and the history says when it changed; without
	// it the next sync would find the device hand-added again.
	if previous := existing.Source().Plugin; previous != cfg.Name {
		adopter, ok := item.(interface{ RecordAdoption(previous string) bool })
		if !ok {
			return "", "", fmt.Errorf("sync plugin %q: %q cannot record its change of owner", cfg.Name, raw.Name)
		}
		adopter.RecordAdoption(previous)
	}
	if item.Version() == existing.Version() {
		return OutcomeUnchanged, "", nil
	}

	if err := repo.Save(ctx, item); err != nil {
		if errors.Is(err, inv.ErrInventoryReadOnly) {
			return OutcomeWouldUpdate, "inventory is open read-only", nil
		}
		return "", "", fmt.Errorf("sync plugin %q: saving %q: %w", cfg.Name, raw.Name, err)
	}
	return OutcomeUpdated, "", nil
}
