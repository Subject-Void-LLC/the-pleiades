package topology_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

func TestSubscribeConsumerConfig(t *testing.T) {
	cfg := topology.SubscribeConsumerConfig("my-durable", "pleiades.events.device.created")

	if cfg.Durable != "my-durable" {
		t.Errorf("Durable = %q, want %q", cfg.Durable, "my-durable")
	}
	if cfg.FilterSubject != "pleiades.events.device.created" {
		t.Errorf("FilterSubject = %q, want %q", cfg.FilterSubject, "pleiades.events.device.created")
	}
	if cfg.AckPolicy != jetstream.AckExplicitPolicy {
		t.Errorf("AckPolicy = %v, want AckExplicitPolicy (at-least-once requires explicit ack)", cfg.AckPolicy)
	}
	if cfg.MaxDeliver != topology.MaxDeliverDefault {
		t.Errorf("MaxDeliver = %d, want %d", cfg.MaxDeliver, topology.MaxDeliverDefault)
	}
}

func TestDispatchConsumerConfig(t *testing.T) {
	cfg := topology.DispatchConsumerConfig()

	if cfg.Durable != topology.DispatchDurableName {
		t.Errorf("Durable = %q, want %q (every Runner replica must share this exact name to form one consumer group)", cfg.Durable, topology.DispatchDurableName)
	}
	if cfg.FilterSubject != topology.DispatchSubjectAll() {
		t.Errorf("FilterSubject = %q, want %q", cfg.FilterSubject, topology.DispatchSubjectAll())
	}

	// The fleet consumer group's guarantee is unchanged by the device
	// token: its filter must still match EVERY device's dispatch.
	//
	// Note what this does NOT prove, since the obvious reading of it is
	// wrong. The filter ends in ">", which matches one or more trailing
	// tokens, so it would match even a device subject that had wrongly
	// widened into several. Exactly-one-token is asserted at the producer
	// by TestDispatchSubjectIsOneTokenPerDevice, not here.
	if got := topology.DispatchSubject("any-device-at-all"); !subjectCoveredByRoot(cfg.FilterSubject, got) {
		t.Errorf("fleet filter %q does not match %q", cfg.FilterSubject, got)
	}
}

func TestDispatchConsumerConfig_StableAcrossCalls(t *testing.T) {
	// Two calls (standing in for two Runner replicas independently
	// bootstrapping) must produce an identical config, or
	// CreateOrUpdateConsumer would treat them as different consumers
	// instead of joining the same group.
	a := topology.DispatchConsumerConfig()
	b := topology.DispatchConsumerConfig()
	if a.Durable != b.Durable || a.FilterSubject != b.FilterSubject {
		t.Errorf("DispatchConsumerConfig() is not stable across calls: %+v vs %+v", a, b)
	}
}

func TestLogViewerConsumerConfig(t *testing.T) {
	cfg := topology.LogViewerConsumerConfig("job-42")

	if cfg.Durable != "" {
		t.Errorf("Durable = %q, want empty (log viewers must be ephemeral, never durable/shared)", cfg.Durable)
	}
	if cfg.FilterSubject != topology.LogSubject("job-42") {
		t.Errorf("FilterSubject = %q, want %q", cfg.FilterSubject, topology.LogSubject("job-42"))
	}
	if cfg.AckPolicy != jetstream.AckNonePolicy {
		t.Errorf("AckPolicy = %v, want AckNonePolicy (PLAN.md 26.4: every viewer sees every line, no shared ack)", cfg.AckPolicy)
	}
}

func TestLogViewerConsumerConfig_ScopedPerJob(t *testing.T) {
	a := topology.LogViewerConsumerConfig("job-1")
	b := topology.LogViewerConsumerConfig("job-2")
	if a.FilterSubject == b.FilterSubject {
		t.Errorf("two different job IDs produced the same FilterSubject %q; a viewer for job-1 would see job-2's logs", a.FilterSubject)
	}
}
