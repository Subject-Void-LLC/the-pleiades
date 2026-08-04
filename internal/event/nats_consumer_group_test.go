package event_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
)

// TestNatsBusSubscribe_DurableConsumerGroupSplitsMessages is this phase's
// Release Gate proof for PLAN.md Section 26.4's Consumer Group requirement:
// "Controller side subscriptions therefore use named durable consumers
// shared across replicas, so exactly one controller processes each event."
//
// Two independent natsBus instances (standing in for two Runner/Controller
// replicas) both Subscribe to the same topic, which means they join the
// same durable consumer (topology.DurableName is deterministic per topic).
// N published messages must be split between them with zero duplicates and
// zero drops: every message delivered to exactly one of the two, never
// both, and every message accounted for.
func TestNatsBusSubscribe_DurableConsumerGroupSplitsMessages(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	busA, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("failed to init bus A: %v", err)
	}
	busB, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("failed to init bus B: %v", err)
	}

	const topic = "pleiades.events.consumer-group.split"
	const messageCount = 20

	var mu sync.Mutex
	receivedBy := make(map[string][]string) // event ID -> list of receivers that saw it

	record := func(receiver string) func(event.Event) error {
		return func(e event.Event) error {
			mu.Lock()
			receivedBy[e.ID] = append(receivedBy[e.ID], receiver)
			mu.Unlock()
			return nil
		}
	}

	if err := busA.Subscribe(ctx, topic, record("A")); err != nil {
		t.Fatalf("subscribe A: %v", err)
	}
	if err := busB.Subscribe(ctx, topic, record("B")); err != nil {
		t.Fatalf("subscribe B: %v", err)
	}

	for i := 0; i < messageCount; i++ {
		evt := event.Event{ID: fmt.Sprintf("split-%d", i), Type: "consumer-group.split"}
		if err := busA.Publish(ctx, topic, evt); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.After(15 * time.Second)
	for {
		mu.Lock()
		count := len(receivedBy)
		mu.Unlock()
		if count >= messageCount {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			mu.Lock()
			t.Fatalf("timed out: only %d/%d messages accounted for after 15s: %v", len(receivedBy), messageCount, receivedBy)
			mu.Unlock()
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedBy) != messageCount {
		t.Fatalf("expected exactly %d distinct message IDs delivered, got %d: %v", messageCount, len(receivedBy), receivedBy)
	}

	sawA, sawB := 0, 0
	for id, receivers := range receivedBy {
		if len(receivers) != 1 {
			t.Errorf("message %s was delivered to %d receivers (%v), want exactly 1 (consumer group split violated)", id, len(receivers), receivers)
			continue
		}
		switch receivers[0] {
		case "A":
			sawA++
		case "B":
			sawB++
		}
	}
	t.Logf("split: A received %d, B received %d, total %d", sawA, sawB, sawA+sawB)
	if sawA == 0 || sawB == 0 {
		t.Errorf("all %d messages went to a single receiver (A=%d, B=%d); this does not prove the consumer group actually spans both subscribers", messageCount, sawA, sawB)
	}
}
