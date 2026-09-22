// This file is the controller's drain on shutdown: stop being ready, wait for
// the load balancer to notice, and only then stop accepting connections.
//
// Without it a rolling upgrade drops requests. The orchestrator removes a
// terminating pod from its Service at about the same moment it sends SIGTERM,
// and the two race: requests already routed to this pod keep arriving for a
// moment after the signal. A server that closes its listener on the signal
// refuses them. One that first reports itself not ready, and keeps serving
// for a short, bounded time, lets every in-flight route finish landing.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

// defaultDrain is how long a controller keeps serving after it stops being
// ready. Five seconds covers an endpoint update in a healthy cluster; an
// operator with a slower one raises SHUTDOWN_DRAIN.
const defaultDrain = 5 * time.Second

// maxDrain bounds SHUTDOWN_DRAIN, so a typo cannot make a controller refuse
// to stop for an hour. The chart's termination grace period must exceed the
// drain, and a minute is already generous for that.
const maxDrain = time.Minute

// drain is the shutdown drain's state.
type drain struct {
	wait     time.Duration
	draining atomic.Bool
}

// newDrain reads SHUTDOWN_DRAIN (a Go duration, "0s" to switch it off).
func newDrain() (*drain, error) {
	d := &drain{wait: defaultDrain}
	raw := os.Getenv("SHUTDOWN_DRAIN")
	if raw == "" {
		return d, nil
	}
	wait, err := time.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("SHUTDOWN_DRAIN %q is not a duration: %w", raw, err)
	}
	if wait < 0 || wait > maxDrain {
		return nil, fmt.Errorf("SHUTDOWN_DRAIN %s is outside 0s to %s", wait, maxDrain)
	}
	d.wait = wait
	return d, nil
}

// check is the readiness check that fails for the length of the drain.
func (d *drain) check() api.ReadinessCheck {
	return api.ReadinessCheck{
		Name: "draining",
		Probe: func(context.Context) error {
			if d.draining.Load() {
				return errors.New("shutting down")
			}
			return nil
		},
	}
}

// begin stops being ready and waits out the drain, or until ctx ends.
func (d *drain) begin(ctx context.Context) {
	d.draining.Store(true)
	select {
	case <-time.After(d.wait):
	case <-ctx.Done():
	}
}
