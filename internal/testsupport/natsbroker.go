// Starting a real NATS server for a test, in one place.
//
// # Why this exists
//
// Thirty three container starts across nine packages started a broker,
// twenty seven of them through a deprecated module wrapper, each repeating
// the same four line option block. That is the identical drift this
// package was created to stop one layer up: before it, the NATS image was
// named at seventeen call sites and had reached three different versions
// at once. A starter has the same shape of problem as an image pin. When
// the deployment's broker gains a setting, every one of those sites has to
// gain it too, and the one that does not is the one no test happens to
// catch.
//
// The concrete cost was already paid once. A Release Gate written to prove
// that a minted identity is enforced started its broker with a
// configuration file and no flags, which made it the only start in this
// repository running without JetStream. Five permission defects lived
// behind it, green, because every one of them was on the JetStream control
// plane the fixture had switched off.
//
// # What it deliberately does not do
//
// It does not replace the package local helpers above it. Those return
// what their own package needs, a lock manager or a JetStream handle or a
// proxy, and that is theirs to decide. This sits underneath them so that
// the part they all repeat, which is how a broker is started and what it
// is started with, exists once.
package testsupport

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// NATSImageBeforeLimitMarkerTTL is the last nats-server line that refuses
// the per-key TTL bucket configuration internal/lock requires.
//
// It exists so the one test that deliberately starts an unsupported server
// names its version through the same pin check every other image goes
// through, rather than as a literal a survey cannot see. Confirmed
// empirically while designing Phase 16 that this version specifically
// rejects that bucket configuration: it is a version specific regression
// test, not drift, and the assertion it serves is that
// NewNatsLockManager fails closed rather than building a manager that can
// never honor a positive ttl. See TestNewNatsLockManagerRejectsOldServer.
const NATSImageBeforeLimitMarkerTTL = "nats:2.10"

// NATSBroker is a running nats-server and the ways a test reaches it.
type NATSBroker struct {
	// Container is the running container, for the callers that act ON the
	// broker rather than through it: terminating it in the middle of a
	// test, or naming it as another container's upstream.
	Container testcontainers.Container

	tb  testing.TB
	ctx context.Context
}

// StartNATS starts a real nats-server and terminates it when tb finishes.
//
// It takes testing.TB rather than *testing.T because four of the callers
// this replaces are benchmarks or a fuzz target, and a starter that only
// serves tests would leave them on their own copies, which is how the
// drift began.
func StartNATS(tb testing.TB, opts ...NATSOption) *NATSBroker {
	tb.Helper()
	ctx := context.Background()

	settings := natsSettings{image: NATSImage, ports: []string{defaultNATSPort}}
	for _, opt := range opts {
		opt(&settings)
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: natsRequest(settings),
		Started:          true,
	})
	// Cleanup is registered before the error is checked, because a start
	// that fails its wait strategy still leaves a container behind, and
	// that is exactly the case whose log is worth reading.
	if c != nil {
		tb.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		tb.Fatalf("starting a NATS broker: %v\nbroker log:\n%s", err, brokerLog(ctx, c))
	}
	return &NATSBroker{Container: c, tb: tb, ctx: ctx}
}

// URL returns the nats:// address of the broker's client port.
//
// It fails the test rather than returning an error, because every caller
// would otherwise write the same three lines, and a broker that cannot be
// addressed is never something a test can carry on from.
func (b *NATSBroker) URL() string {
	b.tb.Helper()
	return "nats://" + b.Endpoint(defaultNATSPort)
}

// Endpoint returns a bare host:port, with no scheme, for one published
// port.
//
// Separate from URL because a caller configuring another container's
// upstream, or building a ws:// address, needs the pair without a scheme
// in front of it.
func (b *NATSBroker) Endpoint(port string) string {
	b.tb.Helper()
	if !containsSlash(port) {
		port += "/tcp"
	}
	host, err := b.Container.Host(b.ctx)
	if err != nil {
		b.tb.Fatalf("broker host: %v", err)
	}
	mapped, err := b.Container.MappedPort(b.ctx, port)
	if err != nil {
		b.tb.Fatalf("broker port %s: %v (was it passed to WithNATSExposedPorts?)", port, err)
	}
	return fmt.Sprintf("%s:%s", host, mapped.Port())
}

// containsSlash reports whether p already names a protocol.
func containsSlash(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			return true
		}
	}
	return false
}

// brokerLog reads whatever the server managed to say before it died.
//
// A malformed configuration makes nats-server print one line and exit,
// and testcontainers reports only "container exited with code 1", which
// names nothing an author can act on. Both mistakes this helper can
// actually produce, a missing system account and a setting repeated
// between the file and the flags, are one legible line in a log the
// server has already written.
func brokerLog(ctx context.Context, c testcontainers.Container) string {
	if c == nil {
		return "(no container was created)"
	}
	rc, err := c.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("(could not read the broker log: %v)", err)
	}
	defer func() { _ = rc.Close() }()
	out, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Sprintf("(could not read the broker log: %v)", err)
	}
	return string(out)
}
