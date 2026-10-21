// Package runner: a dispatch whose device another run holds (Phase 103a).
// It used to be retried on the fetch loop's own short backoff, five
// deliveries in about six seconds, after which the broker stopped
// delivering it, no result was reported, and its job stayed running
// forever (FAILURE_PATTERNS 396 and 496).
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// contentionBase and contentionMax shape a contended dispatch's waits
// between deliveries: retry.Backoff(15s, 2m, n) for the n-th delivery, so
// 30s, 60s, 120s and 120s, about five and a half minutes before the final
// delivery reports the device busy. A schedule of its own, apart from a
// failed execution's, because waiting for another run to finish is not
// retrying a failure.
const (
	contentionBase = 15 * time.Second
	contentionMax  = 2 * time.Minute
)

// settleContention settles a delivery whose device lease could not be
// taken, three ways:
//
//  1. Held by this same dispatch: a duplicate delivery of work already
//     running here or on another Runner. Nak with a delay of one lease TTL,
//     by which time the holder has settled or died, and report nothing.
//  2. Held by another run, with deliveries left: Nak on the contention
//     schedule (contentionBase).
//  3. Held by another run, on the final delivery: report the dispatch
//     failed as "device busy", naming the holder, and Term it, so the job
//     ends with a result rather than staying running.
//
// A hold that names no owner, taken by a Runner from before owners were
// recorded, counts as another run's. So does an Acquire that failed for a
// reason other than a hold (the lock store unreachable), which is retried
// the same way and named for what it was on the final delivery.
//
// settle stops reporting msg in progress; it is called immediately before
// msg is nak'd or terminated.
func (a *Agent) settleContention(ctx context.Context, msg jetstream.Msg, payload wire.DispatchPayload, err error, settle func()) {
	var held *lock.HeldError
	isHeld := errors.As(err, &held)
	delivered := numDeliveredFor(msg)
	switch {
	case isHeld && held.HeldBy(topology.DispatchKey(payload.JobID, payload.DeviceID)):
		a.logger.Info("this dispatch is already running, so this delivery waits for it",
			slog.String("job_id", payload.JobID), slog.String("device_id", payload.DeviceID))
		settle()
		a.nakContended(msg, a.leaseTTL)
	case delivered < a.maxDeliver:
		delay := retry.Backoff(contentionBase, contentionMax, delivered)
		a.logger.Warn("device lock contention, retrying later",
			slog.String("device_id", payload.DeviceID), slog.Duration("retry_in", delay),
			slog.Int("delivery", delivered), slog.String("error", err.Error()))
		settle()
		a.nakContended(msg, delay)
	default:
		reason := busyReason(held, err)
		a.logger.Error("giving up on a busy device",
			slog.String("job_id", payload.JobID), slog.String("device_id", payload.DeviceID), slog.String("reason", reason))
		a.reportResult(ctx, payload, wire.Outcome{}, errors.New(reason))
		settle()
		if termErr := msg.Term(); termErr != nil {
			a.logger.Error("failed to terminate a dispatch for a busy device", slog.String("error", termErr.Error()))
		}
	}
}

// nakContended redelivers msg after delay.
func (a *Agent) nakContended(msg jetstream.Msg, delay time.Duration) {
	if err := msg.NakWithDelay(delay); err != nil {
		a.logger.Error("failed to nak contended message", slog.String("error", err.Error()))
	}
}

// busyReason is the failed result's reason for a device still held on the
// final delivery: "device busy: held by job <id>" for each holder whose
// owner is a dispatch key, which begins with its job's UUID.
func busyReason(held *lock.HeldError, err error) string {
	if held == nil {
		return fmt.Sprintf("device busy: its lock could not be taken: %v", err)
	}
	names := make([]string, 0, len(held.Owners))
	for _, owner := range held.Owners {
		names = append(names, holderName(owner))
	}
	if len(names) == 0 {
		return "device busy"
	}
	return "device busy: held by " + strings.Join(names, ", ")
}

// holderName names the run behind owner, a topology.DispatchKey, whose
// first 36 characters are its job's UUID followed by "_".
func holderName(owner string) string {
	if owner == "" {
		return "a run that did not record which job it is"
	}
	if len(owner) > 36 && owner[36] == '_' {
		if id, err := uuid.Parse(owner[:36]); err == nil {
			return "job " + id.String()
		}
	}
	return owner
}
