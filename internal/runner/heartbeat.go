// Package runner: the liveness heartbeat a Runner writes while its
// durable NATS consumer is genuinely reachable. The freshness rule a
// probe judges the file by lives in the sibling heartbeat_check.go, so
// the writer and the reader are two files and one format.
//
// # Why a file and not a port
//
// cmd/runner binds nothing. It dials out to the broker and pulls from a
// durable consumer group, so nothing ever connects to it, and
// Dockerfile.runner carries no EXPOSE for that reason. Giving this
// process an HTTP listener purely to answer "are you still working"
// would add a port to configure, a listener to secure, and one more
// thing that can be up while the work is not. A file on a writable path
// answers the same question with none of that: this process writes it,
// and `runner healthcheck`, the only other executable in the image,
// reads it.
//
// # What a beat is evidence of, stated exactly
//
// FAILURE_PATTERNS.md #119 is the failure this exists for: a Runner
// whose NATS connection closes for good stays alive, stays
// healthy-looking, and silently stops doing any work. A heartbeat driven
// by a bare ticker would keep beating through exactly that failure, so
// it would be a probe that cannot fail, which is worse than no probe at
// all.
//
// So every beat is preceded by a REAL round trip to the server about the
// very durable consumer this Agent pulls from (jetstream.Consumer's
// Info, which issues a JetStream API request and waits for the reply).
// The file is touched only when that round trip succeeds. One beat
// therefore proves four things at once: the NATS connection is usable,
// the server is answering, JetStream is up, and this exact durable
// consumer still exists on it. Sever the connection and every one of
// those stops being true within a single interval.
//
// # What it MISSES, because every liveness signal misses something
//
//   - A WEDGED WORKER POOL. If every worker is stuck inside a task
//     forever, no message is ever acknowledged and no new work starts,
//     yet Info keeps answering and this file keeps advancing. The
//     heartbeat proves the Agent CAN pull, never that it IS making
//     progress. Coupling it to real progress was considered and refused:
//     a Runner whose workers are all busy on long device conversations
//     is the normal healthy state, and a probe that killed the busiest
//     Runners would be worse than the gap it closed.
//   - A POISON MESSAGE redelivered forever. That is the Dead Letter
//     Queue's job (agent_handle.go), not this one.
//   - EVERY OTHER DEPENDENCY. The lock manager holds its own NATS
//     connection and the transports reach real devices; neither is on
//     this path. A Runner that can reach the broker and no device it is
//     asked to configure reports healthy here.
//
// The honest summary: this is a probe of the subscription, not of the
// work.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// HeartbeatPath is where a Runner writes its liveness heartbeat.
//
// A named type rather than a bare string because it travels through the
// composition root, an environment variable, a command-line flag and two
// packages, and each of those is a place a plain string could be
// silently swapped with some other path.
type HeartbeatPath string

// String returns the path as an ordinary string, for the call sites that
// have to hand it to the os package.
func (p HeartbeatPath) String() string { return string(p) }

// DefaultHeartbeatPath is where the heartbeat lands when nothing
// configures it.
//
// Under /tmp because that is the only writable path this process is
// guaranteed to have. Dockerfile.runner deliberately creates NO writable
// directory (its runtime stage says why), the Helm chart mounts an
// emptyDir at /tmp with a read-only root filesystem, and
// docker-compose.yml mounts a tmpfs there for the same reason. A
// subdirectory rather than a bare file so the beat cannot be confused
// with anything else a Go process may leave in os.TempDir.
//
// A tmpfs and an emptyDir both vanish with the container, which is
// correct here and would be wrong for the result WAL: a heartbeat is
// worthless the moment the process that wrote it is gone.
const DefaultHeartbeatPath HeartbeatPath = "/tmp/pleiades-runner/heartbeat"

// DefaultHeartbeatInterval is how often a Runner probes its consumer and
// touches the file.
//
// Ten seconds is a compromise with both ends stated. Faster means one
// JetStream API request per Runner per interval against a broker that is
// also carrying every dispatch, for no extra detection value once the
// probe periods are longer than this. Slower widens the window in which
// a dead subscription still looks fresh, since the file can never be
// more accurate than the cadence that writes it.
const DefaultHeartbeatInterval = 10 * time.Second

// heartbeatProbeTimeout bounds the one server round trip a beat makes.
//
// Shorter than the interval on purpose: a probe that could outlive its
// own period would pile up, and a request still waiting when the next
// tick arrives is already evidence enough to withhold this beat.
const heartbeatProbeTimeout = 5 * time.Second

// ConsumerProbe is the one thing a Heartbeat needs from JetStream: a real
// round trip to the server about the durable consumer the Agent pulls
// from.
//
// An interface rather than jetstream.Consumer itself so a test can drive
// the failure this whole file exists for without a broker, and so the
// beat cannot quietly grow a dependency on some cheaper, cached answer.
// jetstream.Consumer satisfies it; CachedInfo deliberately does not,
// because it performs no network request and would turn every beat back
// into the timer this design refuses.
type ConsumerProbe interface {
	// Info fetches the consumer's state FROM THE SERVER. It returns an
	// error when the connection is unusable, when the server does not
	// answer inside ctx, or when the consumer no longer exists.
	Info(ctx context.Context) (*jetstream.ConsumerInfo, error)
}

// Heartbeat writes a file on a bounded interval, but only while its
// consumer probe keeps succeeding. See this file's package comment for
// what a written beat proves and what it does not.
type Heartbeat struct {
	// path is the file whose MODIFICATION TIME carries the signal. See
	// beat for why the contents are documentation rather than data.
	path HeartbeatPath

	// interval is how often Run attempts a beat.
	interval time.Duration

	// probe is the evidence source. Never nil: NewHeartbeat refuses one.
	probe ConsumerProbe

	// timeout bounds one probe round trip (heartbeatProbeTimeout).
	timeout time.Duration

	logger *slog.Logger
}

// NewHeartbeat prepares the heartbeat file and returns the writer for it.
//
// It fails closed at construction, the same shape NewFileWAL and
// runbook.NewDirSource already use in this codebase: an unwritable path
// is a startup-time error the operator must fix, never something the
// first tick discovers after the process has already started accepting
// work and reporting itself healthy.
//
// The write probe does a second job that matters more than it looks. A
// Kubernetes emptyDir survives a container restart within the same pod,
// so a Runner that was just killed BY its liveness probe would start up
// next to the heartbeat its previous life wrote. If that file were
// recent enough, the new process would report healthy before it had
// reached the broker even once. Removing it here makes absence mean
// exactly one thing: no beat has been written yet by THIS process.
//
// interval <= 0 falls back to DefaultHeartbeatInterval, following
// WithPoolSize's own convention for a non-positive value.
func NewHeartbeat(path HeartbeatPath, interval time.Duration, probe ConsumerProbe, logger *slog.Logger) (*Heartbeat, error) {
	if path == "" {
		return nil, errors.New("heartbeat path is empty")
	}
	if probe == nil {
		// A Heartbeat with nothing to probe is a timer, and a timer would
		// keep beating through the exact failure this type exists to
		// catch. Refusing it here is what stops that being one nil away.
		return nil, errors.New("heartbeat has no consumer to probe, which would make it a timer that cannot fail")
	}
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}
	if logger == nil {
		logger = slog.Default()
	}

	dir := filepath.Dir(path.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("heartbeat directory %q could not be created: %w", dir, err)
	}

	// MkdirAll succeeding only proves the directory exists; it says
	// nothing about whether this process may write into it (a read-only
	// bind mount, a volume owned by another uid). A real write proves
	// that now.
	if err := os.WriteFile(path.String(), []byte(heartbeatProbeMarker), 0o600); err != nil {
		return nil, fmt.Errorf("heartbeat file %q is not writable: %w", path, err)
	}
	if err := os.Remove(path.String()); err != nil {
		return nil, fmt.Errorf("heartbeat file %q could not be cleared at startup: %w", path, err)
	}

	return &Heartbeat{
		path:     path,
		interval: interval,
		probe:    probe,
		timeout:  heartbeatProbeTimeout,
		logger:   logger,
	}, nil
}

// heartbeatProbeMarker is what NewHeartbeat's write probe puts in the
// file it immediately removes. It never survives construction; it exists
// so that a crash between the write and the remove leaves something that
// says what it was rather than an empty file that looks like a beat.
const heartbeatProbeMarker = "pleiades runner heartbeat: startup write probe, not a beat\n"

// Path reports where this Heartbeat writes, so a composition root can log
// it and a test can read it without knowing the layout.
func (h *Heartbeat) Path() HeartbeatPath { return h.path }

// Run beats until ctx is done, then returns.
//
// The first beat is attempted immediately rather than one interval in, so
// a Runner that came up healthy becomes reportable as soon as it can be
// instead of spending its first interval indistinguishable from one that
// never reached the broker.
//
// Run never returns an error. A failed beat is a fact about the broker,
// not about this loop, and the whole design is that the failure is
// reported by the file NOT being written rather than by anything this
// process does. Returning an error would tempt a caller into killing the
// Runner over a transient blip, which is the orchestrator's decision to
// make and not this package's.
func (h *Heartbeat) Run(ctx context.Context) {
	h.beatOnce(ctx)

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.beatOnce(ctx)
		}
	}
}

// beatOnce performs one beat and logs the reason when it is withheld.
//
// Warn rather than Error: one failed round trip during a broker restart
// is ordinary and self-correcting, and a Runner logging at Error every
// ten seconds through a thirty-second blip trains an operator to ignore
// the level. What makes a sustained outage visible is the probe, not the
// log line.
func (h *Heartbeat) beatOnce(ctx context.Context) {
	if err := h.beat(ctx); err != nil {
		// A canceled context is this process shutting down, not the
		// broker failing, so it is not worth a warning on every exit.
		if ctx.Err() != nil {
			return
		}
		h.logger.Warn("liveness heartbeat withheld",
			slog.String("path", h.path.String()),
			slog.String("error", err.Error()))
	}
}

// beat probes the consumer and, only if the server answered, touches the
// file.
//
// The ORDER is the whole mechanism: nothing writes before the round trip
// succeeds, so a broker this Runner cannot reach leaves the file exactly
// where it was and its age starts climbing.
//
// What lands in the file is a human-readable timestamp, and nothing ever
// parses it. The signal a probe reads is the file's MODIFICATION TIME
// (see CheckHeartbeat), which is the kernel's own record of when this
// write happened rather than a claim the writer makes about a clock. The
// text is there so that an operator who has the volume mounted can see
// at a glance when the Runner was last attached, since the distroless
// image has no shell to compute it with.
func (h *Heartbeat) beat(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	if _, err := h.probe.Info(probeCtx); err != nil {
		return fmt.Errorf("the durable consumer did not answer: %w", err)
	}

	// 0600, not 0644: nothing but this process and the probe (which runs
	// as the same uid inside the same container) has any business reading
	// when this Runner was last alive.
	line := time.Now().Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(h.path.String(), []byte(line), 0o600); err != nil {
		return fmt.Errorf("writing the heartbeat to %q: %w", h.path, err)
	}
	return nil
}
