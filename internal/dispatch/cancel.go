// Package dispatch: stopping a job, as one operation rather than two.
//
// Cancelling has two halves that fail independently: the durable one, which
// settles the record and stops the fan-out, and the best-effort one, which
// carries the decision to whichever Runner is already executing the job.
// This type is what keeps them together.
//
// It exists because they came apart once. The JSON API did both and the
// browser's own Cancel button did only the first, so cancelling through the
// UI settled the record and left the runbook running on the device, which
// is the opposite of the way round anybody would want that bug: the browser
// is what an operator actually reaches for. Anything that can stop a job
// goes through here now, so there is one answer to what cancelling does
// rather than one per caller.
package dispatch

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// Canceller stops a job and tells whoever is running it.
type Canceller struct {
	store JobStore

	// signals may be nil, which is an ordinary arrangement rather than a
	// broken one: without it a cancel still settles the record and still
	// stops the fan-out, and only work already on a device is left to
	// finish.
	signals event.CancelPublisher

	logger *slog.Logger
}

// NewCanceller builds the one path a cancel takes.
func NewCanceller(store JobStore, signals event.CancelPublisher, logger *slog.Logger) *Canceller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Canceller{store: store, signals: signals, logger: logger}
}

// Cancel stops jobID on behalf of canceledBy.
//
// It returns the store's own error unchanged, so a caller can still tell
// ErrJobNotFound from ErrNotCancelable and answer 404 or 409 accordingly.
func (c *Canceller) Cancel(ctx context.Context, jobID string, canceledBy string) error {
	// The durable write first, always. It is what makes a cancel true;
	// the signal only carries that decision outward. Signalling first
	// would let a failed write leave a stopped execution behind a job
	// that still reads as running, which is the one inconsistency worth
	// ruling out by ordering alone.
	if err := c.store.Cancel(ctx, jobID, canceledBy); err != nil {
		return err
	}

	if c.signals == nil {
		return nil
	}

	// A failure here is logged and nothing more. The cancel has already
	// happened as far as the record is concerned, and returning an error
	// would invite a retry that would then be refused as not cancelable
	// while the job stayed canceled. What is lost is the best-effort
	// half, which is the half that was never promised.
	if err := c.signals.PublishCancel(ctx, jobID); err != nil {
		c.logger.Warn("job was canceled but the running execution could not be signalled",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
	}
	return nil
}
