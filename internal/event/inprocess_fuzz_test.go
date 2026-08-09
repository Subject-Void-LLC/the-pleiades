package event_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// FuzzPublish fuzzes InProcessBus.Publish with randomized topic strings and
// randomized Data bytes, against a bus that already has a wildcard
// subscriber registered. The only assertion is the absence of a panic:
// arbitrary topic strings must flow through subjectMatches without it
// panicking on unusual input such as empty strings, leading/trailing dots,
// or repeated ">" tokens, and an arbitrary Data payload (which Publish
// never parses; it is opaque to the Bus) must never cause a panic either.
func FuzzPublish(f *testing.F) {
	f.Add("pleiades.events.device.created", []byte(`{"id":"seed-1","type":"device.created"}`))
	f.Add("pleiades.events.foo.bar", []byte(`not json at all`))
	f.Add("", []byte(``))
	f.Add(">", []byte(`{}`))
	f.Add("pleiades.events.>.>", []byte(`{"id":"seed-2"}`))
	f.Add("...", []byte(`null`))

	bus := event.NewInProcessBus()
	// Register a wildcard subscriber up front so every fuzz iteration that
	// happens to produce a matching topic actually exercises the
	// handler-dispatch path, not just the no-match path.
	err := bus.Subscribe(context.Background(), "pleiades.events.>", func(evt event.Event) error { return nil })
	if err != nil {
		f.Fatalf("failed to subscribe: %v", err)
	}

	f.Fuzz(func(t *testing.T, topic string, data []byte) {
		evt := event.Event{ID: "fuzz", Type: "fuzz.event", Data: data}
		// The only contract under test here is "never panics". A returned
		// error is a valid outcome for arbitrary fuzzed input (e.g. an
		// already-canceled context is not exercised here, but a future
		// caller doing so remains valid).
		if err := bus.Publish(context.Background(), topic, evt); err != nil {
			t.Logf("Publish returned an error for topic %q: %v", topic, err)
		}
	})
}
