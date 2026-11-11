// Package entstore: a batch's facts judged under the method its node ran, as
// the Controller dispatched it (internal/journal's nodes.go), rather than the
// method the batch names.
//
// A batch names its node and its method, and both are the Runner's text. The
// node is held to the job: the Controller resolves which method it dispatched
// for that node, from the runbook graph pinned on the job at its first
// fan-out or a rollback job's own plan, both fixed once the job dispatches.
// The batch is then stored under that method. Which
// facts a built-in holds (heldByBuiltin) is decided under it too: from an
// external Collection's method they are dropped, since the Runner's loader
// refuses a program declaring one and an honest Runner never sends them; from
// a first-party method this Controller's catalog lacks, which a newer
// release's Runner runs, they are kept and noted (provenance.go). A node the
// Controller cannot resolve (a job that pinned no graph, or a node the graph
// lacks) keeps only the facts no built-in holds, and is stored with no method
// and no Collection identity, which reads as unknown.
package entstore

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// WithNodeResolver has the consumer judge and store each batch under the
// method resolve says its node ran.
func WithNodeResolver(resolve journal.NodeResolver) SubscriberOption {
	return func(s *Subscriber) { s.nodes = resolve }
}

// bindNode returns entries as they are stored for batch once by, its
// provenance, names the method its node ran: by.Method becomes the node's
// method (or empty when the node cannot be resolved, with the Collection
// identity cleared, as it is when the claim differs), and the facts a
// built-in holds are dropped unless that method is first-party. Each change
// is held in notes, said once the batch is stored, at warning, naming the job, device, node and both methods quoted
// (termsafe.Quote), and counts, never a value. Without a resolver entries
// come back as they were. A resolver error is returned.
func (s *Subscriber) bindNode(ctx context.Context, batch facts.Batch, by *facts.Provenance, entries []facts.Entry, notes *journal.Notes) ([]facts.Entry, error) {
	if s.nodes == nil {
		return entries, nil
	}
	node, err := s.nodes(ctx, batch.JobID, batch.DeviceID, by.Node)
	if err != nil {
		return nil, err
	}
	// A facts batch never carries the Runner's unresolved sentinel: the
	// engine stores a task's facts only under what the emitting method
	// declares, and a method its registry does not hold declares none, so an
	// honest Runner sends no batch for it. One naming the sentinel is held as
	// the journal holds its rows: unresolved, never judged as a claim.
	judged := node.Judge(by.Method)
	if by.Method == engine.FQCNUnregistered {
		judged = journal.Judgement{Verdict: journal.VerdictUnresolved, Reason: "the batch names the method as one its Runner could not dispatch"}
	}
	switch judged.Verdict {
	case journal.VerdictNamed, journal.VerdictKept:
		if judged.Verdict == journal.VerdictKept {
			notes.Info(journal.KeptNote,
				slog.String("job", termsafe.Quote(batch.JobID)), slog.String("device", termsafe.Quote(batch.DeviceID)),
				slog.String("node", termsafe.Quote(by.Node)), slog.String("fqcn", termsafe.Quote(by.Method)))
		}
		// The identity is held to the method by the one rule the journal's
		// consumer applies too (journal.IdentityProblem).
		if why := journal.IdentityProblem(by.Method, by.Collection, ""); why != "" {
			notes.Warn("storing facts without the Collection identity their batch names, which cannot be their method's",
				slog.String("job", termsafe.Quote(batch.JobID)), slog.String("device", termsafe.Quote(batch.DeviceID)),
				slog.String("node", termsafe.Quote(by.Node)), slog.String("method", termsafe.Quote(by.Method)), slog.String("reason", why))
			by.Collection = wire.CollectionIdentity{}
		}
	case journal.VerdictMismatch:
		notes.Warn("storing facts under the method their node ran, not the one their batch names",
			slog.String("job", termsafe.Quote(batch.JobID)), slog.String("device", termsafe.Quote(batch.DeviceID)),
			slog.String("node", termsafe.Quote(by.Node)), slog.String("claimed", termsafe.Quote(by.Method)),
			slog.String("dispatched", termsafe.Quote(judged.Method)))
		by.Method, by.Collection = judged.Method, wire.CollectionIdentity{}
	default:
		kept, dropped := s.withoutReserved(entries)
		reason := judged.Reason
		if reason == "" {
			reason = "the node's method could not be resolved"
		}
		notes.Warn("storing facts with no method, since this controller cannot say what their node ran; facts a built-in holds were dropped",
			slog.String("job", termsafe.Quote(batch.JobID)), slog.String("device", termsafe.Quote(batch.DeviceID)),
			slog.String("node", termsafe.Quote(by.Node)), slog.String("claimed", termsafe.Quote(by.Method)),
			slog.Int("kept", len(kept)), slog.Int("dropped", dropped), slog.String("reason", reason))
		by.Method, by.Collection = "", wire.CollectionIdentity{}
		return kept, nil
	}
	if journal.FirstPartyMethod(by.Method) {
		return entries, nil
	}
	kept, dropped := s.withoutReserved(entries)
	if dropped > 0 {
		notes.Warn("dropping facts a built-in holds, from a node that ran an external Collection's method",
			slog.String("job", termsafe.Quote(batch.JobID)), slog.String("device", termsafe.Quote(batch.DeviceID)),
			slog.String("node", termsafe.Quote(by.Node)), slog.String("method", termsafe.Quote(by.Method)),
			slog.Int("kept", len(kept)), slog.Int("dropped", dropped))
	}
	return kept, nil
}

// withoutReserved returns entries without each fact a built-in holds
// (heldByBuiltin: under the fact floor, or declared by the catalog), and how
// many it left out.
func (s *Subscriber) withoutReserved(entries []facts.Entry) ([]facts.Entry, int) {
	kept := make([]facts.Entry, 0, len(entries))
	for _, e := range entries {
		if s.heldByBuiltin(e.Key) != "" {
			continue
		}
		kept = append(kept, e)
	}
	return kept, len(entries) - len(kept)
}
