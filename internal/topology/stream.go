package topology

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// streamMaxAge is a generous, single-node-dev-cluster-appropriate default
// retention window. No production deployment topology exists yet (Phase 2's
// own checklist scope is the bus mechanism and its composition roots, not
// clustering or capacity planning), so this is deliberately conservative
// rather than tuned; a later phase that needs a different value changes it
// here, once, rather than at each of the independent declarations this
// package replaces.
const streamMaxAge = 7 * 24 * time.Hour

// streamDuplicateWindow is how long JetStream remembers a message's
// Nats-Msg-Id (set via jetstream.WithMsgID on Publish) to reject a
// duplicate publish of the same idempotency key. Set explicitly rather
// than left at zero (which defers to the server's own default, currently 2
// minutes) so this codebase's own producer-side dedup guarantee does not
// silently change if the server's default ever does.
const streamDuplicateWindow = 2 * time.Minute

// StreamConfig returns the one JetStream stream configuration every
// Pleiades subject is published on. There is exactly one stream: see
// StreamName's doc comment for why splitting subjects across several
// streams (as this package's predecessor declarations did) is the drift
// this package exists to prevent.
func StreamConfig() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       StreamName,
		Subjects:   []string{StreamSubjectRoot},
		Retention:  jetstream.LimitsPolicy,
		Storage:    jetstream.FileStorage,
		MaxAge:     streamMaxAge,
		Replicas:   1,
		Duplicates: streamDuplicateWindow,
	}
}

// EnsureStream creates the Pleiades stream if it does not already exist, or
// updates it in place to match StreamConfig if it does. It is the single
// CreateOrUpdateStream call site for the entire codebase: event.NewNatsBus,
// cmd/controller, cmd/runner, cmd/demo, and the integration test suite all
// call this instead of each declaring their own stream.
func EnsureStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, StreamConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to ensure %s stream: %w", StreamName, err)
	}
	return stream, nil
}
