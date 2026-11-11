// Package entstore: the Controller side of the Walk tier's facts, as
// internal/journal's Subscriber is the journal's. A Runner publishes a
// facts.Batch per task that emitted facts; one durable consumer, shared by
// every Controller replica, stores it.
package entstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/poison"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Subscriber writes published fact batches into the store.
type Subscriber struct {
	store  facts.Store
	logger *slog.Logger
	// admit decides whether a batch's job and device are ones the
	// Controller dispatched, the same check the journal's consumer makes
	// (internal/journal's admission.go): any Runner may publish on any
	// job's facts subject.
	admit journal.AdmissionCheck
	// declared is what the built-in catalog declares about a key
	// (WithDeclarations), which a Runner's batch is held to.
	declared func(key string) (facts.Spec, bool)
	// digestOpen and digestKeyer open and key the digests a Runner sealed
	// to this Controller (WithDigestKeys, digests.go); without them each is
	// dropped.
	digestOpen  *sealed.PrivateKey
	digestKeyer *facts.Keyer
	// builtins names the built-in methods (WithBuiltinProvenance); a fact a
	// built-in holds from any other method is stored and noted at info. Nil
	// notes nothing.
	builtins map[string]bool
}

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber)

// WithDeclarations holds every batch's facts to index, whatever method a
// batch says emitted them: a key the index declares is stored only as the
// kind and type declared there, and any other key (an external Collection's,
// which only a Runner loads) keeps the kind its Runner sent. The Controller
// links no catalog, so it passes the index of the module catalog it embeds
// (CatalogIndex). Without it every batch keeps the kinds its Runner sent.
func WithDeclarations(index collection.FactIndex) SubscriberOption {
	return func(s *Subscriber) {
		s.declared = func(key string) (facts.Spec, bool) {
			spec, ok := index.Lookup(key)
			return facts.SpecOf(string(spec.Kind), string(spec.Type), spec.Key, spec.NamesItems()), ok
		}
	}
}

// NewSubscriber builds the consumer over store, storing only batches admit
// admits; a nil admit stores every well-formed batch, which only a test of
// the store alone wants.
func NewSubscriber(store facts.Store, logger *slog.Logger, admit journal.AdmissionCheck, opts ...SubscriberOption) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Subscriber{store: store, logger: logger, admit: admit}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CatalogIndex reads a module catalog, the generated JSON of every built-in
// method's manifest by name (internal/api/wellknown's ModuleCatalog), as the
// index WithDeclarations takes.
func CatalogIndex(catalog []byte) (collection.FactIndex, error) {
	var manifests map[string]collection.Manifest
	if err := json.Unmarshal(catalog, &manifests); err != nil {
		return collection.FactIndex{}, fmt.Errorf("facts: reading the module catalog: %w", err)
	}
	return collection.NewFactIndex(manifests), nil
}

// Subscribe attaches s to every job's facts subject: one durable consumer
// group across replicas, so each batch is stored once.
func (s *Subscriber) Subscribe(ctx context.Context, bus event.Bus) error {
	if err := bus.Subscribe(ctx, topology.FactsSubjectAll(), s.Handle); err != nil {
		return fmt.Errorf("failed to subscribe the facts consumer: %w", err)
	}
	return nil
}

// Handle decodes one batch and stores what passes. A batch that cannot be
// decoded, names no job or device or one that is not well formed, holds
// nothing that passes, names a device the database lacks, was never
// dispatched, or holds a value the database refuses as data is acknowledged
// and logged: redelivery would never change it. A database failure, or a dispatch not
// recorded yet, is returned, so the bus redelivers.
func (s *Subscriber) Handle(evt event.Event) error {
	// Numbers as written, so a value hashes here as it did on the Runner
	// (an integer past 2^53 would not survive float64).
	var batch facts.Batch
	dec := json.NewDecoder(bytes.NewReader(evt.Data))
	dec.UseNumber()
	if err := dec.Decode(&batch); err != nil {
		s.logger.Error("dropping a malformed facts batch", slog.String("event", evt.ID))
		return nil
	}
	// Before any query: an ID PostgreSQL refuses failed every query made
	// with it, the admission check's included (FAILURE_PATTERNS 919).
	if err := errors.Join(poison.CheckID(batch.JobID), poison.CheckID(batch.DeviceID)); err != nil {
		s.logger.Error("dropping a facts batch whose job or device id cannot name one",
			slog.String("event", evt.ID), slog.String("reason", err.Error()))
		return nil
	}
	// The run that observed them, held to the batch's own job and attempt
	// rather than trusting a second copy of each.
	by := batch.By
	by.Job, by.Attempt = batch.JobID, batch.Attempt
	// A sealed digest is verified as the Runner hashed it, then opened and
	// keyed (or dropped) before anything is written.
	guard := facts.Guard{Shaped: redact.SecretShaped, SecretName: redact.SecretName, Declared: s.declared, AllowSealed: true}
	entries, refused, err := facts.Verify(batch.Entries, guard)
	entries, lost := s.keyDigests(batch.JobID, batch.DeviceID, entries)
	if len(lost) > 0 {
		// Named by fact, never by value: a Controller without the digest
		// key, a Runner sealing to another key, or a sealed digest from
		// another job or device. Each such fact is stored without one.
		s.logger.Warn("stored facts without the digest a Runner sealed, which this controller could not open as theirs",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.Any("facts", lost))
	}
	if err != nil || refused > 0 {
		s.logger.Warn("dropping facts a Runner published that do not pass the rules",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.Int("refused", refused), slog.Any("error", err))
	}
	if len(entries) == 0 {
		return nil
	}
	if s.admit != nil {
		admission, err := s.admit(context.Background(), batch.JobID, batch.DeviceID)
		if err != nil {
			return fmt.Errorf("failed to check the facts batch for job %s: %w", batch.JobID, err)
		}
		switch admission {
		case journal.AdmitDispatched:
		case journal.AdmitNotDispatched:
			s.logger.Warn("dropping a facts batch for a device this controller never dispatched",
				slog.String("job", batch.JobID), slog.String("device", batch.DeviceID))
			return nil
		default:
			return fmt.Errorf("the facts batch names job %s device %s, which this controller has not recorded dispatching yet", batch.JobID, batch.DeviceID)
		}
	}
	// The Collection identity is the Runner's engine's, never the method's
	// (Phase 136b). This controller writes it, so it holds it to the frozen
	// rule for a delivered identity (CheckDeliveredCollection, wire's
	// Deliverable), as the journal's store holds the same task's identity
	// (internal/journal's identity.go): one that fails (a Runner out of step
	// with itself, or a batch made by hand) is not stored, the facts are the
	// device's and are kept, and the identity reads as unknown, as it does
	// for a value from a Runner before it. Never to Valid, whose Unicode
	// tables are this toolchain's: where it would differ, that is said at info
	// and changes nothing stored. How the Runner routed the method to a group
	// is its build's to say too: a group list grown, or a method moved,
	// between its build and this one reads differently here, which is said
	// at info likewise (LESSONS 415, 422).
	cleared := by.CheckDeliveredCollection()
	if cleared != nil {
		by.Collection = wire.CollectionIdentity{}
	}
	obs := facts.Observation{Entries: entries, At: batch.ObservedAt, By: by}
	if err := s.store.Record(context.Background(), batch.DeviceID, obs); err != nil {
		if errors.Is(err, ErrUnknownDevice) {
			s.logger.Error("dropping a facts batch for a device the database does not hold",
				slog.String("job", batch.JobID), slog.String("device", batch.DeviceID))
			return nil
		}
		// The server refused a value itself, so storing this batch again
		// fails again: redelivering it would only spend its delivery budget
		// and hold up the batches behind it (FAILURE_PATTERNS 919).
		if code, ok := poison.Unstorable(err); ok {
			s.logger.Error("dropping a facts batch the database refuses as data",
				slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.String("sqlstate", code))
			return nil
		}
		return fmt.Errorf("failed to store the facts for job %s: %w", batch.JobID, err)
	}
	// What was stored is said once it is: a batch this controller drops, or
	// one the bus redelivers after a failed write, is not said again.
	s.noteStored(batch, by, cleared, entries)
	s.logger.Debug("stored a facts batch", slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.Int("facts", len(entries)))
	return nil
}

// noteStored says, for a batch whose facts were stored as by, what this
// controller's build would have done differently: the identity it cleared
// (cleared, a warning), one its own toolchain would not make, one it routes
// otherwise, and the facts its release reserves for a built-in from a method
// its catalog lacks (each at info). The method is the batch's own text, so
// it is named bounded and escaped (termsafe.Quote), and every reason is text
// that names no delivered byte unbounded.
func (s *Subscriber) noteStored(batch facts.Batch, by facts.Provenance, cleared error, entries []facts.Entry) {
	method := termsafe.Quote(by.Method)
	if cleared != nil {
		s.logger.Warn("stored facts with no Collection identity, since the batch names one no build writes",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.String("method", method), slog.String("reason", cleared.Error()))
	}
	if id := by.Collection; !id.IsZero() && id.Valid() != nil {
		// Valid's refusal is fixed text, so the group is not repeated.
		s.logger.Info("stored facts under a Collection identity this controller's own toolchain would not make; the Runner's build and this one differ",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.String("method", method), slog.String("reason", id.Valid().Error()))
	}
	if err := by.CollectionRouting(); err != nil {
		s.logger.Info("stored facts under a Collection identity this controller's build routes otherwise; the Runner's build and this one differ",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.String("method", method), slog.String("reason", err.Error()))
	}
	// Facts this release reserves for a built-in, from a method its catalog
	// lacks, are kept and noted (provenance.go): which methods are built-in
	// is the Runner's release's to say.
	s.noteBuiltinDifference(batch, method, by.Method, entries)
}
