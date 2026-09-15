// Package event: the per-job control channel, the one thing in this
// package that is core NATS rather than JetStream.
//
// Bus, in every other file here, is durable, at-least-once and consumed
// through a named durable consumer. That is right for a dispatch, a log
// line, a result and a journal entry, all of which must survive a
// subscriber being briefly absent. It is wrong for a control signal, in
// two ways that both matter:
//
//   - A durable consumer is a consumer GROUP. Every subscriber to one
//     topic shares it, so exactly one of them receives each message. A
//     cancel handed to one arbitrary Runner rather than the one actually
//     holding the job would abort nothing and look like it had worked.
//   - Redelivery is a promise that the message will arrive eventually. A
//     cancel that arrives eventually is a cancellation for a job that has
//     long since ended.
//
// So this is a plain core publish and a plain core subscription: it
// reaches whoever is listening at that instant, and nobody otherwise. That
// makes cancellation of work already in flight BEST EFFORT by
// construction, which is a real limit and is stated wherever this is
// described rather than hidden. The durable half of a cancel, the job's
// own record and the devices its fan-out has not yet reached, does not
// depend on this channel at all.
package event

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
)

// CancelPublisher signals that a job should stop, to whichever Runner is
// executing it right now.
//
// Declared here and depended on as an interface by the Controller side, so
// a composition root that wires no control channel passes nil and the
// cancel still settles the record.
type CancelPublisher interface {
	// PublishCancel sends jobID's stop signal. A nil error means the
	// signal was sent, never that anybody received it: nothing on this
	// channel is acknowledged.
	PublishCancel(ctx context.Context, jobID string) error
}

// CancelSubscriber watches one job's control subject.
//
// A Runner subscribes for the single job it is executing and unsubscribes
// when that execution ends, which is why this takes a job id rather than
// offering a stream of every cancellation.
type CancelSubscriber interface {
	// SubscribeCancel calls onCancel if jobID is canceled while the
	// returned unsubscribe function has not yet been called. onCancel
	// runs on the subscription's own goroutine and must not block.
	//
	// The returned function is always safe to call, including after an
	// error, and is safe to call more than once.
	SubscribeCancel(ctx context.Context, jobID string, onCancel func()) (func(), error)
}

// cancelSignal is the whole body of a control message.
//
// It carries the job id and nothing else, deliberately. A Runner's
// subscribe permission has to name the entire control subject space,
// because a grant cannot know in advance which job that Runner will be
// given, so every Runner can read every cancel signal. Anything richer on
// this channel, a reason string or an actor, would therefore be readable
// fleet-wide in exchange for information the receiver has no use for: a
// Runner does not report why it stopped, the Controller's own record
// already names who stopped it.
type cancelSignal struct {
	JobID string `json:"job_id"`
}

// natsControl is the core-NATS implementation of both halves.
type natsControl struct {
	nc *nats.Conn
}

// NewNATSControl builds a control channel over an existing connection.
//
// It takes the connection rather than dialing its own, because both
// composition roots that need one already hold a live nats.Conn and a
// third TCP connection per process would buy nothing. That also keeps this
// type out of the business of connection lifecycle: it never closes nc,
// since it did not open it.
func NewNATSControl(nc *nats.Conn) CancelChannel {
	return &natsControl{nc: nc}
}

// CancelChannel is both halves together, which is what a composition root
// wires and what an in-process implementation provides for tests.
type CancelChannel interface {
	CancelPublisher
	CancelSubscriber
}

// PublishCancel implements CancelPublisher.
func (c *natsControl) PublishCancel(_ context.Context, jobID string) error {
	body, err := json.Marshal(cancelSignal{JobID: jobID})
	if err != nil {
		return fmt.Errorf("failed to encode cancel signal for job %s: %w", jobID, err)
	}
	// A core publish, so no context and no acknowledgement: this returns
	// as soon as the bytes are handed to the connection. Flush is
	// deliberately not called. Waiting for the server to confirm receipt
	// would make an HTTP handler's latency depend on the broker for a
	// signal whose whole contract is best effort, and the durable half of
	// the cancel has already been written by the time this runs.
	if err := c.nc.Publish(topology.ControlSubject(jobID), body); err != nil {
		return fmt.Errorf("failed to publish cancel signal for job %s: %w", jobID, err)
	}
	return nil
}

// subscribeRegistrationTimeout bounds how long SubscribeCancel waits for
// the server to confirm it has registered the subscription. Generous, because
// the cost of exceeding it is refusing to watch for cancellations at all,
// and short enough that a wedged broker cannot hold an execution up
// indefinitely before it starts.
const subscribeRegistrationTimeout = 10 * time.Second

// SubscribeCancel implements CancelSubscriber.
func (c *natsControl) SubscribeCancel(ctx context.Context, jobID string, onCancel func()) (func(), error) {
	sub, err := c.nc.Subscribe(topology.ControlSubject(jobID), func(msg *nats.Msg) {
		var signal cancelSignal
		if err := json.Unmarshal(msg.Data, &signal); err != nil {
			// A malformed body on this subject is not this Runner's
			// problem to solve and must not abort a healthy execution:
			// stopping a real run because somebody published nonsense
			// would turn a junk message into an outage.
			return
		}
		// The subject is derived from the job id through SubjectToken,
		// which sanitizes and appends a hash, so a signal arriving here
		// for a different job would need a token collision. Checked
		// anyway, because the cost is one comparison and the failure it
		// guards against is aborting somebody else's running job.
		if signal.JobID != jobID {
			return
		}
		onCancel()
	})
	if err != nil {
		return func() {}, fmt.Errorf("failed to subscribe to cancel signals for job %s: %w", jobID, err)
	}

	// Wait for the server to acknowledge the subscription before telling
	// the caller it is watching. nats.go buffers the SUB protocol line and
	// writes it asynchronously, so without this the function returns while
	// the server may still know nothing about the subscription.
	//
	// That window is not merely a delay, it is permanent data loss. A
	// cancel is a plain core publish with no retry and no queue, so one
	// arriving in that window is dropped and never redelivered: the
	// execution runs to completion and nothing anywhere reports that a
	// cancellation was missed. And the window lands exactly where it does
	// the most harm, because the caller subscribes immediately before
	// starting the execution, which is the moment an operator watching a
	// run they have just launched is most likely to stop it.
	//
	// Found by a test that failed for the full length of its timeout
	// rather than intermittently, which is the shape this bug has: losing
	// the race does not make delivery late, it makes delivery never.
	flushCtx, cancel := context.WithTimeout(ctx, subscribeRegistrationTimeout)
	defer cancel()
	if err := c.nc.FlushWithContext(flushCtx); err != nil {
		// Unsubscribed rather than left dangling: a subscription the
		// server may not have registered is not one to hand back as
		// working, and the caller treats this error by carrying on
		// without cancellation rather than by failing the job.
		_ = sub.Unsubscribe()
		return func() {}, fmt.Errorf("failed to confirm the cancel subscription for job %s: %w", jobID, err)
	}

	return func() { _ = sub.Unsubscribe() }, nil
}

// inProcessControl is the in-memory CancelChannel, for a single-process
// deployment and for tests that must not need a broker.
//
// It mirrors NewInProcessBus's own role exactly: the same contract, no
// network, and no durability, which costs nothing here because the real
// implementation offers no durability either.
type inProcessControl struct {
	mu sync.Mutex
	// watchers maps a job id to the callbacks waiting on it. A slice
	// rather than one callback per job, because nothing in the contract
	// says only one party may watch a job, and a map that silently
	// replaced an existing watcher would be a cancel that stopped
	// whichever subscriber happened to register last.
	watchers map[string][]*cancelWatcher
}

// cancelWatcher is one registered callback, identified by pointer so
// unsubscribing removes exactly the right one.
type cancelWatcher struct {
	onCancel func()
}

// NewInProcessControl builds an in-memory control channel.
func NewInProcessControl() CancelChannel {
	return &inProcessControl{watchers: map[string][]*cancelWatcher{}}
}

// PublishCancel implements CancelPublisher.
func (c *inProcessControl) PublishCancel(_ context.Context, jobID string) error {
	c.mu.Lock()
	// Copied out under the lock and called outside it, so a callback that
	// unsubscribes itself (which the Runner's own does) cannot deadlock
	// against the lock this call is holding.
	watchers := make([]*cancelWatcher, len(c.watchers[jobID]))
	copy(watchers, c.watchers[jobID])
	c.mu.Unlock()

	for _, w := range watchers {
		w.onCancel()
	}
	return nil
}

// SubscribeCancel implements CancelSubscriber.
func (c *inProcessControl) SubscribeCancel(_ context.Context, jobID string, onCancel func()) (func(), error) {
	watcher := &cancelWatcher{onCancel: onCancel}

	c.mu.Lock()
	c.watchers[jobID] = append(c.watchers[jobID], watcher)
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			remaining := c.watchers[jobID][:0]
			for _, w := range c.watchers[jobID] {
				if w != watcher {
					remaining = append(remaining, w)
				}
			}
			if len(remaining) == 0 {
				delete(c.watchers, jobID)
				return
			}
			c.watchers[jobID] = remaining
		})
	}, nil
}
