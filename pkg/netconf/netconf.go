// Package netconf is an RFC 6241 NETCONF client over the RFC 6242 SSH
// subsystem framing, implementing the pkg/datastore.Store port.
//
// # It dials nothing
//
// A Session is opened on an io.ReadWriteCloser the caller already has,
// which in production is a pkg/remoteexec.Subsystem obtained from
// Conn.Subsystem(ctx, "netconf"). This package holds no SSH code, no
// host key policy, no retry loop and no circuit breaker, because
// pkg/remoteexec already owns all four and a NETCONF session opens the
// exact same SSH "session" channel Run and Shell already do, just with
// a subsystem request instead of an exec or shell one.
//
// That is a deliberate correction to this phase's own original design,
// which had this package dialing golang.org/x/crypto/ssh directly and
// accepted the duplicated host key verification as a cost. Both of the
// reasons given for that were about internal/transport/ssh
// specifically: that a pkg/ package cannot import it, and that it is
// structurally one-shot. Neither is true of pkg/remoteexec, which is
// under pkg/ and whose Conn is explicitly a reusable, long-lived
// connection. The design predated that package's existence.
//
// Taking a plain io.ReadWriteCloser has a second consequence worth
// stating: every test in this package drives a real codec over a real
// pipe, and the Release Gate drives the identical code over a real SSH
// channel to a real device. There is no mock of the protocol under
// test anywhere, which is what AGENTS.md RULE 0 asks for.
//
// # What was verified against a real device, and what that changed
//
// Every framing and error shape here was pinned against a real Cisco
// IOS XE 17.12 device before it was written
// (pkg/remoteexec/live_subsystem_probe_test.go), not inferred from the
// RFC. Four things that observation changed:
//
//   - The device serves NETCONF on port 830 and NOT on port 22, where
//     it ACCEPTS the subsystem request and then immediately ends the
//     channel. A client that checked only the request reply would call
//     that device working.
//   - Its <hello> is 48,790 bytes, which is why maxHelloBytes is sized
//     in megabytes rather than by intuition.
//   - It advertises both base:1.0 and base:1.1, so "both advertised" is
//     the ordinary case here, not an adversarial edge case.
//   - It does NOT advertise :candidate, only :writable-running, so
//     configuration lands on the running datastore directly and Commit
//     is unusable against it. It does advertise :rollback-on-error,
//     which is what makes that safe; see Options.Target and SetConfig.
package netconf

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// The XML namespace every NETCONF base message lives in. Note that it
// is the base:1.0 namespace even in a base:1.1 session: 1.1 changed the
// framing, not the schema.
const baseNamespace = "urn:ietf:params:xml:ns:netconf:base:1.0"

// Capability URNs this package reasons about. A server advertises many
// more; these are the ones that change what this client does.
const (
	CapabilityBase10          = "urn:ietf:params:netconf:base:1.0"
	CapabilityBase11          = "urn:ietf:params:netconf:base:1.1"
	CapabilityCandidate       = "urn:ietf:params:netconf:capability:candidate:1.0"
	CapabilityWritableRunning = "urn:ietf:params:netconf:capability:writable-running:1.0"
	CapabilityRollbackOnError = "urn:ietf:params:netconf:capability:rollback-on-error:1.0"
	CapabilityValidate10      = "urn:ietf:params:netconf:capability:validate:1.0"
	CapabilityConfirmedCommit = "urn:ietf:params:netconf:capability:confirmed-commit:1.0"
)

// Datastore names a NETCONF configuration datastore (RFC 8342). It is a
// string rather than an iota type because it is written verbatim into
// the wire format as an element name, and because a server's advertised
// capability set, not this package, decides which of them exist on a
// given device.
type Datastore string

const (
	// Running is the datastore every NETCONF server has.
	Running Datastore = "running"
	// Candidate exists only on a server advertising :candidate. A real
	// Cisco IOS XE device does not.
	Candidate Datastore = "candidate"
	// Startup exists only on a server advertising :startup.
	Startup Datastore = "startup"
)

// Default bounds. Each is overridable through Options; each has a
// measured or cited reason rather than a round number chosen for looking
// reasonable.
const (
	// defaultMaxMessageBytes follows pkg/catalystcenter/client.go's own
	// maxResponseBytes and its stated reason: a device is a trusted-ish
	// upstream, but "trusted" is not "allowed to exhaust this process's
	// memory." A full running-config from a large chassis is genuinely
	// tens of megabytes, so this cannot be small.
	defaultMaxMessageBytes = 64 << 20 // 64 MiB

	// defaultMaxHelloBytes is separate from, and much smaller than, the
	// message bound because the hello is the ONE message read before any
	// negotiation has happened, and therefore the one an unauthenticated
	// framing decision rests on. A real Cisco IOS XE 17.12 device sends
	// 48,790 bytes of it, almost all YANG module capability URNs, so 4
	// MiB is roughly eighty times the observed size: generous for a
	// device with far more modules loaded, and still eighty times
	// smaller than letting the message bound cover it.
	defaultMaxHelloBytes = 4 << 20 // 4 MiB

	// defaultMaxDepth bounds element nesting in any message this client
	// decodes. It is not redundant with the byte bound, and the
	// amplification is why: encoding/xml's tokenizer keeps a heap node
	// per open element, so a 64 MiB document of nothing but "<a>"
	// declares roughly sixteen million of them, costing far more memory
	// than the document itself. encoding/xml enforces its own limit only
	// on subtrees it unmarshals INTO A FIELD, and this package captures
	// configuration data as raw inner XML precisely so it never recurses
	// into it, which means nothing in the standard library bounds this
	// for us. 512 leaves two orders of magnitude over the deepest real
	// YANG data tree.
	defaultMaxDepth = 512
)

// Options configures a Session. Every field has a documented default
// applied when left at its zero value.
type Options struct {
	// Target is the datastore GetConfig reads and SetConfig writes.
	// Defaults to Running.
	//
	// Open REFUSES a target the server did not advertise support for
	// rather than discovering it at the first RPC. On a device that
	// does not offer :candidate, that turns a silent late failure into
	// an immediate, named one naming the capability that is missing.
	Target Datastore

	// MaxMessageBytes bounds any single message read from the server.
	// Defaults to defaultMaxMessageBytes.
	MaxMessageBytes int

	// MaxHelloBytes bounds the server's <hello> specifically. Defaults
	// to defaultMaxHelloBytes.
	MaxHelloBytes int

	// MaxDepth bounds element nesting in a decoded message. Defaults to
	// defaultMaxDepth.
	MaxDepth int

	// ClientCapabilities are advertised to the server in addition to
	// base:1.0 and base:1.1, which this package always advertises.
	ClientCapabilities []string
}

func (o Options) maxMessageBytes() int {
	if o.MaxMessageBytes > 0 {
		return o.MaxMessageBytes
	}
	return defaultMaxMessageBytes
}

func (o Options) maxHelloBytes() int {
	if o.MaxHelloBytes > 0 {
		return o.MaxHelloBytes
	}
	return defaultMaxHelloBytes
}

func (o Options) maxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return defaultMaxDepth
}

func (o Options) target() Datastore {
	if o.Target != "" {
		return o.Target
	}
	return Running
}

// Session is one live NETCONF session. It implements datastore.Store.
//
// A Session is not safe for concurrent use by multiple goroutines, the
// same restriction pkg/remoteexec's Conn, Shell and Subsystem all
// carry: NETCONF's message-id correlation would need a dispatcher to
// support concurrent RPCs, and no caller in this codebase issues two at
// once.
//
// A ctx deadline firing during any operation closes the underlying
// stream and leaves the Session permanently unusable, the same
// clean-immediate-failure choice Shell documents: a half-read framed
// message cannot be resynchronized from, and a client that tried would
// be guessing at where the next message starts.
type Session struct {
	rwc     io.ReadWriteCloser
	r       *reader
	w       *writer
	opts    Options
	framing Framing

	serverCapabilities []string
	capabilitySet      map[string]struct{}
	sessionID          string

	// messageID is the RFC 6241 section 4.1 message-id, monotonic per
	// session and compared against every reply. Atomic so that the
	// value is well-defined even though concurrent use is unsupported:
	// a data race here would be silent misattribution of a reply, which
	// is worse than a refusal.
	messageID atomic.Int64
}

// Compile-time proof that a Session really is the port it claims to be.
// Without this, a signature drift in either package would be caught
// only wherever a Session happens to be assigned to a Store.
var _ datastore.Store = (*Session)(nil)

// Open performs the RFC 6241 <hello> exchange over rwc and returns a
// ready Session. The caller retains ownership of rwc and must Close the
// Session, which closes rwc.
//
// The hello exchange is where a client is most exposed, because it
// happens before anything has been negotiated, and every branch here is
// therefore a refusal rather than a fallback:
//
//   - A server advertising NEITHER base:1.0 nor base:1.1 is refused. It
//     is not defaulted into either framing, because choosing one would
//     mean guessing at how to find the end of every subsequent message.
//   - A server advertising both selects base:1.1, per RFC 6241 section
//     8.1's highest-common-version rule. Chunked framing is not merely
//     newer: end-of-message framing's "]]>]]>" delimiter is legal inside
//     XML character data, so a configuration value containing it ends
//     the message early.
//   - The hello read is bounded by Options.MaxHelloBytes BEFORE the
//     first byte is parsed, so a server sending an endless capability
//     list cannot exhaust memory during the one exchange that has to
//     happen before any bound could be negotiated.
//   - A Target the server did not advertise is refused here rather than
//     at the first RPC.
func Open(ctx context.Context, rwc io.ReadWriteCloser, opts Options) (*Session, error) {
	s := &Session{
		rwc:  rwc,
		opts: opts,
		// The hello itself is read under the hello bound, not the
		// message bound; readMessage's bound is raised to the message
		// bound immediately afterward.
		r: newReader(rwc, opts.maxHelloBytes()),
		w: newWriter(rwc),
	}

	stop := s.watchContext(ctx)
	defer stop()

	raw, err := s.r.readMessage()
	if err != nil {
		return nil, fmt.Errorf("netconf: reading the server hello: %w", wrapCtx(ctx, err))
	}
	if err := checkDepth(raw, opts.maxDepth()); err != nil {
		return nil, fmt.Errorf("netconf: server hello: %w", err)
	}

	var hello helloMessage
	if err := xml.Unmarshal(raw, &hello); err != nil {
		return nil, fmt.Errorf("netconf: parsing the server hello: %w", err)
	}
	s.serverCapabilities = hello.Capabilities
	s.capabilitySet = capabilitySet(hello.Capabilities)
	s.sessionID = strings.TrimSpace(hello.SessionID)

	framing, err := negotiateFraming(s.capabilitySet)
	if err != nil {
		return nil, err
	}
	s.framing = framing

	if err := s.writeClientHello(); err != nil {
		return nil, fmt.Errorf("netconf: sending the client hello: %w", wrapCtx(ctx, err))
	}

	// Both directions switch together, immediately after the hellos and
	// never before: RFC 6242 section 4.1 is explicit that the hello
	// exchange itself is always end-of-message framed.
	s.r.setFraming(framing)
	s.w.setFraming(framing)
	s.r.maxBytes = opts.maxMessageBytes()

	if err := s.checkTarget(); err != nil {
		return nil, err
	}
	return s, nil
}

// checkTarget refuses a configured Target the server never advertised
// support for, naming the capability rather than the datastore, since
// the capability is what an operator has to enable on the device.
func (s *Session) checkTarget() error {
	switch s.opts.target() {
	case Running:
		// Every server has a running datastore. Writing to it directly
		// additionally needs :writable-running, but that is SetConfig's
		// concern: a session opened only to read must not be refused
		// for lacking a write capability.
		return nil
	case Candidate:
		if !s.HasCapability(CapabilityCandidate) {
			return fmt.Errorf("netconf: this device does not support the candidate datastore (it does not advertise %s); use the running datastore instead", CapabilityCandidate)
		}
	case Startup:
		if !s.HasCapability("urn:ietf:params:netconf:capability:startup:1.0") {
			return fmt.Errorf("netconf: this device does not support the startup datastore (it does not advertise urn:ietf:params:netconf:capability:startup:1.0)")
		}
	default:
		return fmt.Errorf("netconf: unknown datastore %q: valid datastores are running, candidate and startup", s.opts.target())
	}
	return nil
}

// negotiateFraming selects the framing both sides will use from the
// server's advertised base capabilities.
func negotiateFraming(caps map[string]struct{}) (Framing, error) {
	_, has10 := caps[CapabilityBase10]
	_, has11 := caps[CapabilityBase11]
	switch {
	case has11:
		return FramingChunked, nil
	case has10:
		return FramingEndOfMessage, nil
	default:
		return 0, fmt.Errorf("netconf: the server's hello advertises neither %s nor %s, so there is no agreed message framing; refusing rather than guessing at one", CapabilityBase10, CapabilityBase11)
	}
}

// writeClientHello advertises both base versions plus anything the
// caller added. It is sent end-of-message framed, always.
func (s *Session) writeClientHello() error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintf(&b, `<hello xmlns=%q><capabilities>`, baseNamespace)
	fmt.Fprintf(&b, `<capability>%s</capability>`, CapabilityBase10)
	fmt.Fprintf(&b, `<capability>%s</capability>`, CapabilityBase11)
	for _, c := range s.opts.ClientCapabilities {
		b.WriteString("<capability>")
		xml.EscapeText(&b, []byte(c)) // #nosec G104 -- strings.Builder writes never fail
		b.WriteString("</capability>")
	}
	b.WriteString(`</capabilities></hello>`)
	return s.w.writeMessage([]byte(b.String()))
}

// Capabilities returns everything the server advertised, verbatim and
// in the order it sent them.
func (s *Session) Capabilities() []string {
	out := make([]string, len(s.serverCapabilities))
	copy(out, s.serverCapabilities)
	return out
}

// HasCapability reports whether the server advertised urn.
//
// A capability URN may carry query parameters (a real device advertises
// "...:with-defaults:1.0?basic-mode=explicit&also-supported=..."), and
// those parameters are part of the advertisement, not part of the
// capability's identity. Matching is therefore against the URN up to
// the first "?", so a caller asking about with-defaults does not have
// to reproduce a device's exact parameter string to get a true answer.
func (s *Session) HasCapability(urn string) bool {
	_, ok := s.capabilitySet[urn]
	return ok
}

// SessionID is the server-assigned session identifier from its hello.
func (s *Session) SessionID() string { return s.sessionID }

// Framing reports which RFC 6242 framing was negotiated.
func (s *Session) Framing() Framing { return s.framing }

// Target reports the datastore this session reads and writes.
func (s *Session) Target() Datastore { return s.opts.target() }

// capabilitySet indexes advertised capabilities by their URN with any
// query parameters stripped; see HasCapability for why.
func capabilitySet(caps []string) map[string]struct{} {
	set := make(map[string]struct{}, len(caps))
	for _, c := range caps {
		c = strings.TrimSpace(c)
		if i := strings.IndexByte(c, '?'); i >= 0 {
			c = c[:i]
		}
		if c != "" {
			set[c] = struct{}{}
		}
	}
	return set
}

// helloMessage is the subset of <hello> this client reads. Everything
// else a server sends in it is ignored rather than rejected: a hello is
// extensible by design and refusing an unknown child would break
// against the next vendor extension.
type helloMessage struct {
	XMLName      xml.Name `xml:"hello"`
	Capabilities []string `xml:"capabilities>capability"`
	SessionID    string   `xml:"session-id"`
}

// wrapCtx substitutes ctx's own error for err when ctx is what actually
// ended the operation, mirroring pkg/remoteexec's function of the same
// name and for the same reason: closing the stream out from under a
// blocked read produces an I/O error describing the mechanism rather
// than the cause.
func wrapCtx(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// watchContext closes the underlying stream if ctx ends before the
// returned stop function is called, which is how a per-call deadline
// reaches a read that has no deadline of its own. It is the same shape
// remoteexec.Shell's methods each use. The returned function must
// always be called, normally by defer.
func (s *Session) watchContext(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Not actionable here: this goroutine exists only to
			// unblock the read, which is what reports the failure.
			_ = s.rwc.Close() // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()
	return func() { close(done) }
}
