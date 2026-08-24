package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ExecutionAdapter executes a runbook against a target device. The
// payload it receives is wire.DispatchPayload, the shared DTO
// pkg/wire now owns: this package used to define its own local
// DispatchPayload type, hand-kept in sync with the near-identical one
// api/dispatcher.go separately defined, a duplication pkg/wire's own doc
// comment names as the exact bug shape (a stale DeviceIP field naming an
// "ip" property no device type in this codebase ever populates, and a
// DeviceName field one of the two duplicates populated from the wrong
// accessor) this move exists to make impossible to repeat.
type ExecutionAdapter interface {
	Execute(ctx context.Context, payload wire.DispatchPayload) error
}

// Agent is the executor node that pulls jobs from NATS and runs them.
type Agent struct {
	consumer   jetstream.Consumer
	adapter    ExecutionAdapter
	js         jetstream.JetStream
	locks      lock.Manager
	maxDeliver int
	logger     *slog.Logger
	tracer     trace.Tracer
	baseSleep  time.Duration
	maxSleep   time.Duration

	// dedup suppresses a redelivered dispatch that already executed to
	// completion. Nil disables the check entirely, which is the
	// pre-Phase-96c behaviour and what a Runner started without a
	// reachable dedup bucket falls back to. See WithDedupStore.
	dedup DedupStore

	// dedupTTL is how long a completed dispatch is remembered. It must
	// exceed the deployment's outage budget, or the memory expires
	// before the redelivery it exists to suppress.
	dedupTTL time.Duration

	// poolSize is the number of concurrent handleMessage workers Run
	// starts. See WithPoolSize and defaultPoolSize.
	poolSize int

	// leaseTTL and heartbeatEvery configure executeWithLease's per-device
	// lock lease and heartbeat cadence (agent_exec.go). See
	// execLeaseTTL/heartbeatInterval's own doc comments for their
	// defaults and WithLeaseTTL/WithHeartbeatInterval for overriding them.
	leaseTTL       time.Duration
	heartbeatEvery time.Duration

	// wal and bus back optional Write-Ahead-Log result buffering
	// (agent_wal.go). Both nil unless WithResultWAL is passed to
	// NewAgent, in which case neither is ever nil again for this Agent's
	// lifetime.
	wal ResultWAL
	bus event.Bus

	// liveness is the optional liveness heartbeat this Agent runs
	// alongside its pull loop (heartbeat.go). Nil unless WithHeartbeat is
	// passed to NewAgent.
	//
	// Named liveness rather than heartbeat because this type already has
	// a heartbeat METHOD, and the two are different things: that one
	// renews a per-device lock lease inside one execution
	// (agent_exec.go), this one reports that the whole Agent is still
	// attached to its consumer.
	liveness *Heartbeat
}

// defaultPoolSize is how many handleMessage workers Run starts when no
// WithPoolSize option overrides it. It matches
// engine.defaultMaxConcurrency and ansible-playbook's own default forks
// value, so a benchmark comparing this Agent's throughput against either
// reference measures a comparable degree of parallelism rather than two
// arbitrarily different ones.
const defaultPoolSize = 5

// AgentOption configures optional, non-default behavior on an Agent built
// by NewAgent, following the same variadic-trailing-option idiom
// dispatch.WorkerOption already establishes in this codebase (see that
// type's own doc comment), so every existing NewAgent call site that
// passes no options keeps compiling and keeps today's defaults unchanged.
type AgentOption func(*Agent)

// WithPoolSize overrides Agent's default worker pool size
// (defaultPoolSize). n <= 0 is ignored (the default is kept), since a
// pool of zero or negative workers could never make progress.
func WithPoolSize(n int) AgentOption {
	return func(a *Agent) {
		if n > 0 {
			a.poolSize = n
		}
	}
}

// WithLeaseTTL overrides Agent's default per-device lock lease TTL
// (execLeaseTTL). It exists chiefly for tests that need to prove
// heartbeat/self-abort behavior without waiting out the real production
// window; ttl <= 0 is ignored.
func WithLeaseTTL(ttl time.Duration) AgentOption {
	return func(a *Agent) {
		if ttl > 0 {
			a.leaseTTL = ttl
		}
	}
}

// WithHeartbeatInterval overrides Agent's default lease heartbeat cadence
// (heartbeatInterval). It exists chiefly for tests that need to observe a
// heartbeat tick, or a heartbeat failure, without waiting out the real
// production interval; d <= 0 is ignored.
func WithHeartbeatInterval(d time.Duration) AgentOption {
	return func(a *Agent) {
		if d > 0 {
			a.heartbeatEvery = d
		}
	}
}

// WithHeartbeat runs hb for as long as Agent.Run runs, so the liveness
// heartbeat (heartbeat.go) starts and stops with the pull loop it
// reports on rather than with some unrelated goroutine in a composition
// root.
//
// That coupling is the reason this is an Agent option at all: a
// heartbeat that outlived Run could keep writing a healthy-looking file
// for a Runner that has already stopped pulling, which is a smaller copy
// of the exact failure it exists to catch. A nil hb is ignored, so an
// Agent built without this option behaves as though the option did not
// exist.
func WithHeartbeat(hb *Heartbeat) AgentOption {
	return func(a *Agent) {
		if hb != nil {
			a.liveness = hb
		}
	}
}

// NewAgent creates a new Runner Agent connected to a JetStream consumer.
//
// js and maxDeliver back handleMessage's Dead Letter Queue handling
// (PLAN.md Section 26.3): Agent deliberately stays on its own pull-based
// consumer.Fetch loop rather than going through event.Bus.Subscribe
// (PATTERNS.md's own "Push vs Pull Execution Model" entry names this pull
// model as deliberate), but it must not hand-roll a second DLQ mechanism
// either, per Section 25's Build-Once rule -- see handleMessage, which
// calls the same event.HandleDeliveryFailure natsBus.Subscribe itself
// uses. maxDeliver should match consumer's own configured MaxDeliver
// (topology.DispatchConsumerConfig's, in real use), or the two can
// disagree about when a message is actually exhausted.
//
// locks is PLAN.md Section 13's backend lock manager: "Locks live in the
// backend (not the runner), since multiple execution environments may
// target the same device." handleMessage acquires a per-device lease
// through it before executing (executeWithLease, agent_exec.go), which is
// what makes concurrent pool workers (poolSize > 1) safe against two of
// them racing the same device.
//
// tracer is what makes this node the far end of PLAN.md Section 19's
// distributed trace: handleMessage continues the trace the Controller
// started, rather than beginning an unrelated one, by reading the W3C
// trace context the publishing Bus left in the message headers. A nil
// tracer means no spans, which is right for a test and wrong for a
// deployment.
//
// opts applies optional, non-default configuration (WithPoolSize,
// WithLeaseTTL, WithHeartbeatInterval, WithResultWAL); every existing
// caller can omit it entirely and gets this function's own defaults.
func NewAgent(consumer jetstream.Consumer, adapter ExecutionAdapter, js jetstream.JetStream, locks lock.Manager, maxDeliver int, logger *slog.Logger, tracer trace.Tracer, opts ...AgentOption) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("github.com/Subject-Void-LLC/the-pleiades/internal/runner")
	}
	a := &Agent{
		consumer:       consumer,
		adapter:        adapter,
		js:             js,
		locks:          locks,
		maxDeliver:     maxDeliver,
		logger:         logger,
		tracer:         tracer,
		baseSleep:      100 * time.Millisecond,
		maxSleep:       10 * time.Second,
		poolSize:       defaultPoolSize,
		leaseTTL:       execLeaseTTL,
		heartbeatEvery: heartbeatInterval,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithDedupStore gives the Agent a durable memory of which dispatches
// have already executed, so a redelivery of work that completed is
// suppressed rather than run twice.
//
// It is an option rather than a constructor parameter because the check
// degrades safely: an Agent without one behaves exactly as it did before
// Phase 96c. That is the opposite of the reasoning
// LESSONS_LEARNED.md #154 applies to required dependencies, and the
// difference is that this one has a correct and previously shipped
// behaviour to fall back to.
//
// ttl must exceed the deployment's outage budget. A memory shorter than
// the outage it covers is worse than none, because it looks like
// protection and is not.
func WithDedupStore(store DedupStore, ttl time.Duration) AgentOption {
	return func(a *Agent) {
		a.dedup = store
		a.dedupTTL = ttl
	}
}
