// Package journal: storing each row of a Runner's batch under the method the
// Controller dispatched for its node (nodes.go), not the method the batch
// names.
package journal

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// bindNodes holds every row of batch to the method its node ran, as the
// Controller's resolver says: a row whose claim the node may have run is kept
// as it is, its undo with it. That undo is not judged here: the rollback
// planner holds every recorded undo it would replay to the declarations of
// the method the row is stored under (internal/rollback's steps.go), which
// is the one place a row's undo is judged. One whose claim differs is stored
// under the node's method, with the identity, program and undo the claim
// brought cleared, since each was
// made for another method; and one whose node cannot be resolved is stored
// with its method unresolved (engine.FQCNUnregistered) and the same fields
// cleared. A row the Runner itself could not resolve keeps that answer.
// What it says of each is held in notes, said once the batch is stored.
// Nothing is bound without a resolver. A resolver error is returned, so the
// bus redelivers the batch.
func (s *Subscriber) bindNodes(ctx context.Context, batch *Batch, notes *Notes) error {
	if s.nodes == nil {
		return nil
	}
	unresolved := 0
	reason := ""
	for i := range batch.Entries {
		entry := &batch.Entries[i]
		node, err := s.nodes(ctx, batch.JobID, batch.DeviceID, entry.NodeID)
		if err != nil {
			return err
		}
		// The Runner's own record of a method it could not dispatch: the
		// sentinel names nothing, so there is no claim to hold to the node,
		// and nothing the row carries can be vouched for.
		if entry.FQCNUnresolved || entry.FQCN == engine.FQCNUnregistered {
			entry.FQCN, entry.FQCNUnresolved = engine.FQCNUnregistered, true
			clearClaimed(entry)
			continue
		}
		judged := node.Judge(entry.FQCN)
		switch judged.Verdict {
		case VerdictNamed:
			s.holdProvider(batch, entry, notes)
		case VerdictKept:
			// Said at info, as the facts consumer says a newer built-in's
			// facts: the Runner's release and this one differ.
			notes.Info(KeptNote,
				slog.String("job", termsafe.Quote(batch.JobID)),
				slog.String("device", termsafe.Quote(batch.DeviceID)),
				slog.String("node", termsafe.Quote(entry.NodeID)),
				slog.String("fqcn", termsafe.Quote(entry.FQCN)))
			s.holdProvider(batch, entry, notes)
		case VerdictMismatch:
			notes.Warn("storing a run journal entry under the method its node ran, not the one its batch names",
				slog.String("job", termsafe.Quote(batch.JobID)),
				slog.String("device", termsafe.Quote(batch.DeviceID)),
				slog.String("node", termsafe.Quote(entry.NodeID)),
				slog.String("claimed", termsafe.Quote(entry.FQCN)),
				slog.String("dispatched", termsafe.Quote(judged.Method)))
			entry.FQCN, entry.FQCNUnresolved = judged.Method, false
			clearClaimed(entry)
		default:
			// Not resolvable: the claim is not trusted, and is not stored.
			unresolved++
			if reason == "" {
				reason = judged.Reason
			}
			entry.FQCN, entry.FQCNUnresolved = engine.FQCNUnregistered, true
			clearClaimed(entry)
		}
	}
	if unresolved > 0 {
		if reason == "" {
			reason = "the node's method could not be resolved"
		}
		notes.Warn("storing run journal entries with their method unresolved, since this controller cannot say what their nodes ran",
			slog.String("job", termsafe.Quote(batch.JobID)),
			slog.String("device", termsafe.Quote(batch.DeviceID)),
			slog.Int("entries", unresolved),
			slog.String("reason", reason))
	}
	return nil
}

// holdProvider clears, on a row whose claim its node ran, the program and
// Collection identity it names when they cannot be the method's
// (IdentityProblem, the rule the facts consumer applies too), held in notes
// as a warning, naming no delivered byte unbounded.
func (s *Subscriber) holdProvider(batch *Batch, entry *engine.JournalEntry, notes *Notes) {
	why := IdentityProblem(entry.FQCN, entry.Collection, entry.ProviderProgram)
	if why == "" {
		return
	}
	if FirstPartyMethod(entry.FQCN) {
		entry.ProviderProgram, entry.ProviderDigest = "", ""
	}
	entry.Collection = wire.CollectionIdentity{}
	notes.Warn("storing a run journal entry without the Collection identity its batch names, which cannot be its method's",
		slog.String("job", termsafe.Quote(batch.JobID)),
		slog.String("device", termsafe.Quote(batch.DeviceID)),
		slog.String("node", termsafe.Quote(entry.NodeID)),
		slog.String("fqcn", termsafe.Quote(entry.FQCN)),
		slog.String("reason", why))
}

// clearClaimed clears what a row's claimed method brought with it: the
// Collection identity and program the engine stamps for that method, and the
// undo it recorded and the rendered rollback: steps the journal kept under that
// method's declarations. An
// entry cleared this way reads as one whose identity is unknown and which
// recorded no undo, so a rollback of it replays nothing it carried.
func clearClaimed(entry *engine.JournalEntry) {
	entry.Collection = wire.CollectionIdentity{}
	entry.ProviderProgram, entry.ProviderDigest = "", ""
	entry.InverseFQCN, entry.InverseFQCNUnresolved = "", false
	entry.InverseParamKeys, entry.UndeclaredInverseParamCount = nil, 0
	entry.InverseParams = nil
	entry.InverseComplete, entry.InversePartial = false, false
	entry.InverseSpecDigest = ""
	// Its rendered rollback: steps were held to the claimed method's
	// declarations too.
	entry.AuthoredSteps = nil
}
