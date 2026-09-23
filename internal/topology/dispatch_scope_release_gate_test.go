// This file holds Phase 101a's Release Gate: the proof, against a real
// NATS JetStream broker rather than a fake, that giving the dispatch
// subject a device token bought a filtered consumer without costing the
// fleet consumer group its exactly-once guarantee.
//
// It is written in three acts, and the third is the deliverable. Act one
// establishes that the shared fleet consumer still delivers every dispatch
// to exactly one replica. Act two shows a per-device consumer receiving its
// own device's dispatch. Act three shows that same consumer NOT receiving
// another device's. Without act three, act two cannot tell a working filter
// from a consumer that was receiving everything, which is the failure this
// whole change would otherwise ship silently.
package topology_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// gateDeviceCount is how many devices the gate dispatches to. Small on
// purpose: this gate is about routing, and the 10,000-device fan-out is
// already proven in internal/api's own release gate.
const gateDeviceCount = 8

// startGateBroker brings up a real JetStream broker with this project's own
// stream already provisioned through the real ProvisionStream call.
func startGateBroker(t *testing.T) jetstream.JetStream {
	t.Helper()
	ctx := context.Background()

	url := testsupport.StartNATS(t).URL()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("ProvisionStream: %v", err)
	}
	return js
}

// gateDeviceID names the nth device the gate dispatches to. It is a
// hostname on purpose: a dotted id is exactly what would have widened a
// three-token subject into a six-token one before SubjectToken existed, so
// the gate runs against the shape that used to break rather than against a
// convenient one.
func gateDeviceID(n int) string {
	return fmt.Sprintf("router%d.example.com", n)
}

// publishGateDispatches publishes one dispatch per device and returns the
// subject each landed on.
func publishGateDispatches(t *testing.T, js jetstream.JetStream) []string {
	t.Helper()
	ctx := context.Background()

	subjects := make([]string, 0, gateDeviceCount)
	for i := 0; i < gateDeviceCount; i++ {
		subject := topology.DispatchSubject(gateDeviceID(i))
		if _, err := js.Publish(ctx, subject, []byte(gateDeviceID(i))); err != nil {
			t.Fatalf("publishing dispatch for %q on %q: %v", gateDeviceID(i), subject, err)
		}
		subjects = append(subjects, subject)
	}
	return subjects
}

// drain fetches from consumer until it goes quiet, returning the subject of
// every message it delivered. It acks, because the fleet consumer is an
// explicit-ack consumer and an unacked message would be redelivered and
// counted twice.
func drain(t *testing.T, consumer jetstream.Consumer, limit int) []string {
	t.Helper()

	var seen []string
	for {
		batch, err := consumer.Fetch(limit, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				return seen
			}
			t.Fatalf("fetch: %v", err)
		}
		n := 0
		for msg := range batch.Messages() {
			if err := msg.Ack(); err != nil {
				t.Fatalf("ack: %v", err)
			}
			seen = append(seen, msg.Subject())
			n++
		}
		if err := batch.Error(); err != nil {
			t.Fatalf("batch: %v", err)
		}
		if n == 0 {
			return seen
		}
	}
}

// TestReleaseGate_TheDeviceTokenScopesDeliveryWithoutCostingTheFleetGroup is
// Phase 101a's Release Gate. See this file's own doc comment for why it is
// three acts and why the last one is the point.
func TestReleaseGate_TheDeviceTokenScopesDeliveryWithoutCostingTheFleetGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	js := startGateBroker(t)

	// The per-device consumer is created BEFORE the publishes only to prove
	// nothing about ordering matters: SubscribeConsumerConfig uses
	// DeliverAllPolicy, so it would receive the same messages either way.
	scopedSubject := topology.DispatchSubject(gateDeviceID(0))
	scoped, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName,
		topology.SubscribeConsumerConfig("gate-scoped-to-one-device", scopedSubject))
	if err != nil {
		t.Fatalf("creating the per-device consumer: %v", err)
	}

	published := publishGateDispatches(t, js)

	// ---- Act one: the fleet consumer group is unchanged. ----
	//
	// One durable, bound twice, standing in for two Runner replicas that
	// each called CreateOrUpdateConsumer with the identical config. Every
	// dispatch must reach exactly one of them: not one replica, and not
	// both.
	if _, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig()); err != nil {
		t.Fatalf("creating the fleet consumer: %v", err)
	}
	replicaA, err := js.Consumer(ctx, topology.StreamName, topology.DispatchDurableName)
	if err != nil {
		t.Fatalf("binding replica A: %v", err)
	}
	replicaB, err := js.Consumer(ctx, topology.StreamName, topology.DispatchDurableName)
	if err != nil {
		t.Fatalf("binding replica B: %v", err)
	}

	// Small batches, alternating, so both replicas genuinely compete for
	// the same work rather than the first one draining everything.
	delivered := map[string]int{}
	for _, replica := range []jetstream.Consumer{replicaA, replicaB, replicaA, replicaB} {
		for _, subject := range drain(t, replica, 2) {
			delivered[subject]++
		}
	}

	if len(delivered) != gateDeviceCount {
		t.Errorf("fleet consumer group delivered %d distinct dispatches, want %d", len(delivered), gateDeviceCount)
	}
	for _, subject := range published {
		switch n := delivered[subject]; {
		case n == 0:
			t.Errorf("dispatch on %q reached no replica at all", subject)
		case n > 1:
			t.Errorf("dispatch on %q reached %d replicas, want exactly 1: the consumer group's guarantee is broken", subject, n)
		}
	}

	// ---- Act two: a per-device consumer receives its own device. ----
	scopedSeen := drain(t, scoped, gateDeviceCount)
	if len(scopedSeen) != 1 {
		t.Fatalf("per-device consumer received %d dispatches, want exactly 1: %v", len(scopedSeen), scopedSeen)
	}
	if scopedSeen[0] != scopedSubject {
		t.Errorf("per-device consumer received %q, want %q", scopedSeen[0], scopedSubject)
	}

	// ---- Act three: and receives nothing else. This is the deliverable. ----
	//
	// Act two on its own would pass identically against a consumer with no
	// filter at all, because that consumer would also have received device
	// zero's dispatch. Only this act tells the two apart.
	for i := 1; i < gateDeviceCount; i++ {
		other := topology.DispatchSubject(gateDeviceID(i))
		for _, got := range scopedSeen {
			if got == other {
				t.Errorf("per-device consumer scoped to %q also received %q", scopedSubject, other)
			}
		}
	}

	// The negative control needs its own positive control, or "received
	// nothing else" could just mean "nothing else was ever published".
	// Every other device's dispatch did reach the fleet consumer above, so
	// the messages existed and this consumer declined them.
	for i := 1; i < gateDeviceCount; i++ {
		if other := topology.DispatchSubject(gateDeviceID(i)); delivered[other] == 0 {
			t.Errorf("dispatch on %q was never delivered to anyone, so act three proves nothing about it", other)
		}
	}
}
