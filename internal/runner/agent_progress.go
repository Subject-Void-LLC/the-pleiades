// Package runner: telling JetStream a dispatch is still being worked on, so
// a run longer than the ack wait is not redelivered while it runs (Phase
// 103a). Nothing did before: every run over thirty seconds was delivered
// again, the copy met the run's own lease, and the redeliveries used up the
// delivery budget a failed run's retries and dead letter needed.
package runner

import (
	"log/slog"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

// defaultProgressInterval is a third of the ack wait, so two reports can be
// lost to a slow broker before a delivery lapses.
const defaultProgressInterval = topology.DispatchAckWait / 3

// WithProgressInterval overrides how often a dispatch in hand is reported
// in progress (a third of topology.DispatchAckWait). It exists for tests
// that need to see a tick without waiting ten seconds; d <= 0 is ignored.
func WithProgressInterval(d time.Duration) AgentOption {
	return func(a *Agent) {
		if d > 0 {
			a.progressEvery = d
		}
	}
}

// keepInProgress reports msg in progress every a.progressEvery until the
// returned stop is called, which handleMessage does immediately before it
// acks, naks or terminates msg, and again, as a no-op, when it returns.
// stop waits for the reporting goroutine to end, so nothing reports a
// message after it is settled.
//
// A failed report is logged and not fatal: during a broker outage the
// message may be delivered again, and the device lease and its owner absorb
// that (settleContention's first case).
func (a *Agent) keepInProgress(msg jetstream.Msg) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(a.progressEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := msg.InProgress(); err != nil {
					a.logger.Warn("could not report a dispatch in progress; it may be delivered again", slog.String("error", err.Error()))
				}
			}
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}
