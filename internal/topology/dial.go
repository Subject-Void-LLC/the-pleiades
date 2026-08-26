// Dial options for every NATS connection this module opens.
//
// This file exists because of a measured defect rather than a style
// preference. Before Phase 96a, every dial in the module was a bare
// nats.Connect(url) carrying zero options, which inherits nats.go's
// defaults: MaxReconnect 60 with ReconnectWait 2s, and
// RetryOnFailedConnect false. Against a real broker behind a real
// Toxiproxy, a hard TCP cut killed the connection permanently after 2m3s
// and it did not come back when the network was fully healed 30 seconds
// later: status CLOSED, reconnect callbacks zero, and a publish returning
// "nats: connection closed". A Runner that lost its link for longer than
// about two minutes was dead until a human restarted the process. The
// same runs measured a cold start against a not-yet-running broker
// failing in 2ms with a bare EOF (a listener whose backend is down) or in
// exactly 2s with i/o timeout (a black-holed address), while a fresh dial
// once the broker was reachable succeeded in 11ms. Recovery was always
// possible; nothing ever attempted it.
//
// SCOPE, because this is easy to overclaim: these options govern the
// CONTROL plane only, which is Controller to NATS to Runner. They have
// nothing to do with the execution plane, the Runner's own connection to
// a managed device over SSH, serial or WinRM, whose retry and circuit
// breaker live in pkg/remoteexec and whose deliberate rule is that a
// command already sent is never retried. A Runner that survives a
// two-hour control-plane outage still cannot resume an SSH session that
// died mid-command.
//
// The options live here, in the messaging topology owner, rather than in
// a new package. internal/topology is already Section 25's Build-Once
// "Messaging topology owner", is already in internal/archtest's
// adapterAllowlist, and is already the single declaration site for the
// stream, the consumers, the dedup bucket and the lock bucket. A new
// package holding these would import nats.go concretely and so would need
// a new allowlist entry, which layering_test.go calls a real design
// decision rather than a place to silence a failing test.
//
// The blank line below is deliberate: it keeps this a file comment rather
// than a second package comment. internal/topology's package doc lives in
// topology.go, and Go has no notion of a package documented in two
// places.

package topology

import (
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
	"github.com/nats-io/nats.go"
)

const (
	// DialTimeout bounds a single connection attempt. It matches the
	// nats.go default rather than lengthening it, because that default
	// was measured rather than assumed: a black-holed address failed in
	// exactly 2s with "dial tcp: i/o timeout", confirming the value
	// applies to the case a longer timeout would be meant to help. A
	// reachable broker answered in 11ms, so the timeout is never the
	// thing an operator waits on in the healthy case.
	DialTimeout = 2 * time.Second

	// ReconnectBaseDelay and ReconnectMaxDelay bound the exponential
	// backoff between reconnect attempts, fed to retry.Backoff by
	// ReconnectDelay below.
	//
	// The base is deliberately shorter than nats.go's 2s ReconnectWait
	// default and the maximum deliberately longer. A link that flaps for
	// a moment (a Starlink handoff, a truck passing under an overpass)
	// should be recovered from in well under a second rather than waiting
	// out a fixed 2s, and a link that is gone for an hour should not be
	// dialed 1800 times to discover that.
	ReconnectBaseDelay = 250 * time.Millisecond
	ReconnectMaxDelay  = 30 * time.Second

	// PingInterval and MaxPingsOutstanding decide how quickly a
	// connection notices a black hole: a link that stopped carrying
	// traffic without ever delivering a TCP reset, which is what a
	// satellite pass gap and a jam window both look like from the client
	// side. nats.go's defaults are 2m and 2, so the library takes four
	// minutes to conclude anything is wrong.
	//
	// The arithmetic is off by one from the obvious reading, which is
	// worth stating because it was got wrong here once already:
	// processPingTimer declares a stale connection when pout EXCEEDS
	// MaxPingsOut, so the failure lands on tick MaxPingsOut+1, not on
	// tick MaxPingsOut. Twenty seconds with 2 outstanding therefore gives
	// 60s, which is the intended target: inside the window in which a
	// Runner's liveness heartbeat would otherwise still be reporting a
	// healthy process that is no longer connected to anything.
	PingInterval        = 20 * time.Second
	MaxPingsOutstanding = 2

	// ConnectWaitTimeout bounds WaitForConnect, and therefore bounds how
	// long a composition root blocks at startup before reporting that the
	// broker is unreachable.
	//
	// This is a STARTUP FAILURE bound, not a full-backoff-cycle bound,
	// and the difference matters because an earlier draft of this comment
	// argued from the backoff ceiling and then set a value below it.
	//
	// A reachable broker connects in 11ms, so the bound is never paid in
	// the healthy case. A broker that is merely slow to come up is
	// covered: the backoff sequence from a 250ms base reaches roughly
	// 7.75s of cumulative delay by the fifth attempt, so a broker
	// appearing within ten seconds is caught. Past that the process
	// exits, which is the right answer rather than a compromise: every
	// supervisor this ships under restarts it (Kubernetes restartPolicy,
	// compose restart), so a genuinely slow broker converges by restart
	// while a typo in NATS_URL surfaces as a startup error instead of a
	// hang. Waiting longer would trade a clear failure for a silent one.
	ConnectWaitTimeout = 10 * time.Second
)

// ReconnectDelay is the delay before reconnect attempt number attempt,
// exposed separately from DialOptions so it can be tested and fuzzed as
// the pure function it is.
//
// It consumes pkg/retry.Backoff rather than computing a backoff here.
// Only the delay half of pkg/retry is the right half to consume: retry.Do
// owns a retry loop, and nats.go's reconnect machinery already owns that
// loop and calls this back once per attempt. internal/event/dlq.go
// already uses Backoff in exactly this shape, as a bare delay
// computation with no loop around it, for NakWithDelay.
//
// attempt arrives from nats.go as a 1-based count of attempts already
// made, while retry.Backoff treats attempt as a 0-based exponent, so the
// conversion below is not cosmetic: passing nats.go's value through
// unchanged would skip the first backoff step entirely. retry.Backoff
// clamps a negative attempt to zero and caps the exponent, so this is
// total over every int.
func ReconnectDelay(attempt int) time.Duration {
	exponent := attempt - 1
	if exponent < 0 {
		exponent = 0
	}
	return retry.Backoff(ReconnectBaseDelay, ReconnectMaxDelay, exponent)
}

// DialOptions returns the one option set every nats.Connect call in this
// module passes. component names the dialing subsystem (for example
// "runner-dispatch") and reaches the server as part of the connection
// name, so an operator reading `nats server report connections` can tell
// which of a Runner's three connections is which.
//
// logger receives the connection lifecycle events. Pass the composition
// root's own logger rather than slog.Default(): internal/redact's masking
// ruleset is installed on the root's handler, and
// internal/archtest.TestEverySlogHandlerCarriesTheMaskingRuleset is what
// keeps that true. A nil logger falls back to slog.Default() so a test
// or a tool need not build one.
//
// The scale limit, stated rather than discovered later: this returns ONE
// option set for every consumer in the deployment, and a Runner on a
// satellite link genuinely wants different numbers from a Controller in
// the same rack as the broker. That divergence is real and this
// deliberately does not serve it. When it is needed, it arrives as a
// named profile argument to this function, never as a second option
// literal at a call site, because per-call-site literals are precisely
// the drift this package exists to end.
func DialOptions(logger *slog.Logger, component string) []nats.Option {
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("component", component, "subsystem", "nats")

	return []nats.Option{
		nats.Name("pleiades-" + component),
		nats.Timeout(DialTimeout),

		// The two options that close the measured defect.
		//
		// MaxReconnects(-1) removes the 60-attempt budget whose
		// exhaustion is what made a two-minute outage permanent.
		// RetryOnFailedConnect(true) makes a dial against a broker that
		// does not exist yet enter the reconnect loop instead of
		// returning an error the caller treats as fatal.
		//
		// RetryOnFailedConnect has a consequence every caller must
		// handle: nats.Connect then returns a *Conn that is NOT yet
		// connected. Any caller that immediately uses the handle (to
		// build a JetStream context, ensure a stream, or create a
		// bucket) must wait for the first connect first. WaitForConnect
		// below is that wait, and it is why this option is safe to set
		// here for everyone rather than per call site.
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),

		nats.CustomReconnectDelay(ReconnectDelay),
		nats.PingInterval(PingInterval),
		nats.MaxPingsOutstanding(MaxPingsOutstanding),

		// A disconnect used to be completely silent. None of the five
		// dial sites set a single handler, so a mesh connection could
		// die without producing one log line, which is how a Runner
		// could look healthy while doing no work at all.
		//
		// FIVE handlers are registered rather than the obvious four,
		// because nats.go routes the initial-connect retry window
		// through a different pair than the steady-state one. While
		// Conn.initc is true (that is, until the FIRST successful
		// connect), doReconnect consults ReconnectErrCB and never
		// DisconnectedErrCB, and the eventual success calls ConnectedCB
		// and never ReconnectedCB. Registering only the steady-state
		// pair therefore leaves exactly the cold-start path that
		// RetryOnFailedConnect exists to enable completely silent, which
		// is the case an operator most needs to see.
		nats.ConnectHandler(func(nc *nats.Conn) {
			log.Info("nats connection established", "url", nc.ConnectedUrlRedacted())
		}),
		nats.ReconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("nats connect attempt failed, still retrying", "error", err)
		}),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			// ConnectedUrlRedacted is deliberately not read here. By the
			// time this fires the connection has already left CONNECTED,
			// so nats.go reports an empty string for it, and logging
			// url="" on every disconnect is worse than not logging it:
			// it reads as a connection that never had an address.
			log.Warn("nats connection lost, reconnecting", "error", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Info("nats connection reestablished",
				"url", nc.ConnectedUrlRedacted(), "reconnects", nc.Stats().Reconnects)
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			// This used to say it should never fire at all, on the
			// grounds that MaxReconnects(-1) reconnects forever and
			// NoCallbacksAfterClientClose suppresses the explicit-Close
			// case. That reasoning was correct about the network and
			// wrong about credentials, and Phase 101c measured the
			// difference against a real broker.
			//
			// nats.go abandons reconnection after the SAME
			// AUTHENTICATION ERROR TWICE regardless of MaxReconnects(-1)
			// (nats.go@v1.52.0/nats.go:3961), unless
			// IgnoreAuthErrorAbort is set, which this function does not
			// set. So on an authenticated mesh there is a second, real
			// route here: a credential that expires or is revoked under
			// a live connection closes it permanently.
			// TestReleaseGate_AnExpiringCredentialEvictsALiveConnection
			// exercises exactly that and observes this handler firing.
			//
			// It stays at Error, and the reason is now stronger rather
			// than weaker. This is the one log line that distinguishes
			// "the Runner stopped because its identity lapsed" from "the
			// Runner is quietly doing nothing", and nothing else in the
			// process reports it.
			log.Error("nats connection closed permanently", "last_error", nc.LastError())
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			subject := ""
			if sub != nil {
				subject = sub.Subject
			}
			log.Error("nats asynchronous error", "subject", subject, "error", err)
		}),

		// Without this, Conn.Close() invokes DisconnectErrHandler and
		// ClosedHandler on the way out, so every deliberate SIGTERM
		// logged a WARN saying "reconnecting" and an ERROR saying
		// "closed permanently" about a shutdown that was going exactly
		// to plan. A Runner and a Controller each hold three
		// connections, so an ordinary rolling update produced six false
		// lines per pod, at the two levels an operator is most likely to
		// be alerting on.
		nats.NoCallbacksAfterClientClose(),
	}
}
