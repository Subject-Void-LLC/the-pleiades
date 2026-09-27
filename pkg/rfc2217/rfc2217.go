// Package rfc2217 speaks the Telnet Com Port Control Option (RFC 2217)
// well enough to negotiate a console server's line settings and assert
// its modem control lines - the real control channel RawPassthroughCapable
// bare TCP passthrough (pkg/serialtcp) does not have.
//
// # Why this is hand-rolled instead of github.com/annetutil/gnetcli/pkg/streamer/rfc2217
//
// Phase 73's own plan required evaluating that package (MIT, v1.3.11 at
// evaluation time) before writing a single IAC/DO/WILL/SB byte by hand.
// It was rejected, for two independent reasons found by actually
// importing it into a scratch module and reading its real API surface
// (`go doc -all`), not by reading its README:
//
//  1. rfc2217.NewStreamer(host, port int, credentials credentials.Credentials, ...)
//     requires gnetcli's own credentials.Credentials type - a second,
//     unrelated credentials vocabulary alongside internal/credential.Credential,
//     with its own SimpleCredentials/WithPassword/WithPrivateKey shape.
//     Bridging the two at every call site is exactly the kind of second
//     vocabulary this module's own logging decision (Phase 70) already
//     ruled out once for zap; WithLogger(*zap.Logger) being a real
//     StreamerOption is that same zap dependency showing up a second time,
//     in a different package, confirming it is not a one-off.
//  2. The exposed Streamer is gnetcli's full expect-style network-CLI
//     abstraction - Cmd, ReadTo(ctx, expr.Expr), Download/Upload,
//     InitAgentForward, HasFeature - not a narrow RFC 2217 control
//     channel. There is no exported "assert DTR" or "send a break": the
//     actual SET_CONTROL/SET_BAUDRATE subnegotiation logic this package
//     needs is an unexported FSM behind that broad surface, reachable
//     only through Cmd's own command-string shape, which is precisely
//     the "assert DTR and send a break are not command strings" mismatch
//     the plan flagged as the reason this needs its own interface at all.
//
// A narrow, purpose-built Client, talking to internal/credential and
// nothing else, and exposing exactly the five operations a console
// server's control channel offers (SetLine, AssertDTR, AssertRTS,
// SendBreak, plus raw Read/Write once negotiated) was the smaller and
// more honest option once the dependency's real shape was visible.
//
// # No TransportBinding, unlike pkg/serialexec/pkg/serialtcp/pkg/telnetexec
//
// "Assert DTR" and "send a break" are not command strings, so Client
// cannot implement transport.Transport's Exec(ctx, target, cred, command)
// signature meaningfully - there is no command to run, only line-control
// operations to perform before or around whatever transport actually
// carries console traffic (raw passthrough, most commonly). This is the
// identical reasoning Phase 77 applies to SFTP: a capability that is real
// and useful, but not Exec-shaped, gets its own interface and stays out
// of engine.TransportBinding entirely rather than being forced into a
// shape it does not fit.
//
// # An unsolicited frame is dropped, never applied
//
// Client holds no local cache of "the line's current settings" that an
// inbound COM-PORT-OPTION subnegotiation could corrupt. Every method that
// waits for a specific server acknowledgement (setBaudRate, setControl,
// ...) only ever completes on the ONE frame whose command byte matches
// what it itself just asked for; any other COM-PORT-OPTION frame arriving
// in the meantime - an unsolicited NOTIFY_LINESTATE/NOTIFY_MODEMSTATE, or
// a SERVER_SET_* this client never requested - is silently discarded, not
// interpreted as an instruction. This is a deliberate design property,
// not an incidental one: a compromised or misbehaving access server has
// no channel here for pushing a baud change or a break the operator never
// asked for.
//
// # RULE 0 evidence, stated honestly
//
// This package's own test suite proves the IAC/COM-PORT-OPTION framing
// and negotiation logic against a real, in-process, real-TCP fake access
// server - the same "no mock, no Docker required" discipline
// pkg/remoteexec's own fake-SSH-server suite and pkg/serialtcp's own
// fake-TCP-server suite already establish, because nothing about parsing
// this wire protocol requires real hardware to exercise honestly. What it
// does NOT prove is that a real line's baud rate, parity, or modem
// control lines actually changed on a real console server: Phase 73's
// own bastion-proof workstream is what runs this Client against a real
// ser2net access server and observes the negotiated baud and an actual
// break on the far side - the one assertion that distinguishes RFC 2217
// from raw passthrough, and would pass identically for both if the
// distinction here were fake.
//
// A handful of conn.SetReadDeadline failures, and setDataSize/setParity/
// setStopSize's own sendSubneg write-error branches specifically (as
// opposed to setBaudRate's and setControl's, which real tests do force
// via an already-closed connection), are real defensive coverage but not
// reachable from this package's own test suite without fault injection
// this module does not fabricate - the identical, already-documented gap
// pkg/serialexec, pkg/serialtcp, and pkg/telnetexec each carry for the
// same reason. Left in place so a future net.Conn implementation's
// failure mode here gets a clear, named error instead of an opaque one.
package rfc2217

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// Telnet command bytes (RFC 854) this package's IAC filter recognizes.
const (
	iacByte  = 255
	dontByte = 254
	doByte   = 253
	wontByte = 252
	willByte = 251
	sbByte   = 250
	seByte   = 240
)

// comPortOption is the Telnet option number RFC 2217 registers for the
// Com Port Control Option.
const comPortOption = 44

// maxSBPayload bounds one COM-PORT-OPTION subnegotiation's payload
// accumulation. Every real payload this protocol defines is at most 4
// bytes (SET_BAUDRATE); this cap is generous headroom rather than a
// tight fit, existing only so a subnegotiation that never sends its own
// terminating IAC SE -- a compromised or malfunctioning access server
// holding the accumulator open for as long as one Options.ReadTimeout
// window allows -- fails closed instead of growing sbPayload without
// bound for that whole window.
const maxSBPayload = 256

// Client-to-access-server COM-PORT-OPTION subnegotiation commands
// (RFC 2217 §3-§10). An access server's acknowledgement of command N
// carries command byte N+serverAckOffset.
const (
	cmdSetBaudRate = 1
	cmdSetDataSize = 2
	cmdSetParity   = 3
	cmdSetStopSize = 4
	cmdSetControl  = 5
	cmdPurgeData   = 12

	serverAckOffset = 100
)

// SET-CONTROL sub-command values (RFC 2217 §7).
const (
	setControlReqBreak = 4
	setControlBreakOn  = 5
	setControlBreakOff = 6
	setControlReqDTR   = 7
	setControlDTROn    = 8
	setControlDTROff   = 9
	setControlReqRTS   = 10
	setControlRTSOn    = 11
	setControlRTSOff   = 12
)

const (
	// DefaultDialTimeout is how long Dial waits for the initial TCP
	// connection before giving up.
	DefaultDialTimeout = 10 * time.Second

	// DefaultReadTimeout is how long Client waits for a specific
	// negotiation reply or subnegotiation acknowledgement before giving
	// up - a request/response bound, not a "gone quiet" bound, so it
	// defaults longer than pkg/serialexec's or pkg/serialtcp's own
	// quiet-period timeouts.
	DefaultReadTimeout = 5 * time.Second

	// DefaultBreakDuration is how long SendBreak holds the line in the
	// break condition when the caller passes a zero duration.
	DefaultBreakDuration = 250 * time.Millisecond
)

// Options configures Dial.
type Options struct {
	// DialTimeout bounds the initial TCP connection attempt. Zero takes
	// DefaultDialTimeout.
	DialTimeout time.Duration

	// ReadTimeout bounds how long Client waits for a specific
	// negotiation reply or subnegotiation acknowledgement. Zero takes
	// DefaultReadTimeout.
	ReadTimeout time.Duration
}

// Client is a connected RFC 2217 session: a Telnet connection to a
// console server's access port, with the Com Port Control Option
// successfully negotiated. Construct one with Dial.
type Client struct {
	conn        net.Conn
	readTimeout time.Duration
	filter      iacFilter
	buffered    []byte
}

// Dial connects to host:port, negotiates Telnet binary-safe framing
// implicitly (RFC 2217 traffic is 8-bit clean by construction of the IAC
// escaping this package performs; see the package doc comment on why no
// separate BINARY option negotiation is attempted), and negotiates the
// Com Port Control Option by sending WILL COM-PORT-OPTION and waiting for
// the access server to reply DO. Every real console server this platform
// targets (ser2net, Digi, Lantronix) expects the client to initiate this
// way; a server that spontaneously sends WILL/DO COM-PORT-OPTION on its
// own, or replies with anything other than DO or DONT, is treated as
// protocol-nonconformant and Dial returns a clear error rather than
// guessing what it meant.
func Dial(ctx context.Context, host string, port int, opts Options) (*Client, error) {
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var d net.Dialer
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("rfc2217: dialing %s: %w", addr, err)
	}

	client, err := NewOverConn(ctx, conn, opts)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// NewOverConn negotiates the Com Port Control Option over conn directly
// instead of a connection this package dials itself, and is Dial's own
// negotiation logic - Dial is now a thin wrapper: dial, then call this.
//
// This exists for a caller who already has a live connection to the
// access server by some other means Dial cannot express - most
// concretely, pkg/remoteexec.Runner.DialThroughHops, which tunnels
// through a bastion chain and hands back a raw net.Conn with no SSH
// handshake on the final leg, exactly the shape a console server behind
// a bastion needs. On failure conn is left open; the caller retains
// ownership of it either way (matching how Dial itself only closes conn
// on the paths IT dialed, never leaving that decision to this function
// for a conn it did not open).
func NewOverConn(ctx context.Context, conn net.Conn, opts Options) (*Client, error) {
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
	c := &Client{conn: conn, readTimeout: readTimeout}

	if err := writeIAC(conn, willByte, comPortOption); err != nil {
		return nil, fmt.Errorf("rfc2217: announcing COM-PORT-OPTION: %w", err)
	}

	if err := c.awaitComPortAgreement(ctx); err != nil {
		return nil, err
	}

	return c, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// SetLine negotiates baud rate, data bits, parity, and stop bits over
// the control channel, in that order, verifying the access server's own
// acknowledgement of each matches what was requested before moving to
// the next - a mismatch is reported as an error naming which setting
// disagreed, rather than silently proceeding with a line that is not
// actually configured the way the caller asked.
func (c *Client) SetLine(ctx context.Context, line serialline.Config) error {
	if err := c.setBaudRate(ctx, line.BaudRate); err != nil {
		return err
	}
	if err := c.setDataSize(ctx, line.DataBits); err != nil {
		return err
	}
	if err := c.setParity(ctx, line.Parity); err != nil {
		return err
	}
	if err := c.setStopSize(ctx, line.StopBits); err != nil {
		return err
	}
	return nil
}

// AssertDTR sets the Data Terminal Ready modem control line on or off.
func (c *Client) AssertDTR(ctx context.Context, on bool) error {
	value := byte(setControlDTROff)
	if on {
		value = setControlDTROn
	}
	if err := c.setControl(ctx, value); err != nil {
		return fmt.Errorf("rfc2217: asserting DTR: %w", err)
	}
	return nil
}

// AssertRTS sets the Request To Send modem control line on or off.
func (c *Client) AssertRTS(ctx context.Context, on bool) error {
	value := byte(setControlRTSOff)
	if on {
		value = setControlRTSOn
	}
	if err := c.setControl(ctx, value); err != nil {
		return fmt.Errorf("rfc2217: asserting RTS: %w", err)
	}
	return nil
}

// SendBreak asserts a break condition on the line for duration, then
// clears it. A zero or negative duration takes DefaultBreakDuration.
func (c *Client) SendBreak(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		duration = DefaultBreakDuration
	}
	if err := c.setControl(ctx, setControlBreakOn); err != nil {
		return fmt.Errorf("rfc2217: sending break: %w", err)
	}
	select {
	case <-time.After(duration):
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := c.setControl(ctx, setControlBreakOff); err != nil {
		return fmt.Errorf("rfc2217: clearing break: %w", err)
	}
	return nil
}

// Read reads plain console data: whatever the access server wrote that
// is not itself telnet negotiation or a COM-PORT-OPTION subnegotiation.
// A subnegotiation frame arriving while the caller is in Read is
// dropped, not applied - see the package doc comment - and does not
// itself end the call: Read keeps reading (each underlying attempt
// bounded by Options.ReadTimeout) until at least one byte of plain data
// is available, ctx is done, or a genuine read error occurs, so a caller
// never has to re-implement this same retry loop just because a frame
// happened to arrive first. A read timeout or EOF with nothing yet
// accumulated returns (0, nil) rather than an error - the same
// "not a failure" treatment pkg/serialtcp's own doc comment gives a
// timeout or EOF - since a caller polling a live console session should
// treat that as "nothing new right now," not a stopping condition.
func (c *Client) Read(ctx context.Context, p []byte) (int, error) {
	if len(c.buffered) > 0 {
		n := copy(p, c.buffered)
		c.buffered = c.buffered[n:]
		return n, nil
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		if err := c.conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, fmt.Errorf("rfc2217: setting read deadline: %w", err)
		}

		n, err := c.conn.Read(buf)
		if n > 0 {
			data, _, _, ferr := c.filter.feed(c.conn, buf[:n])
			if ferr != nil {
				return 0, ferr
			}
			c.buffered = append(c.buffered, data...)
		}
		if len(c.buffered) > 0 {
			n2 := copy(p, c.buffered)
			c.buffered = c.buffered[n2:]
			return n2, nil
		}
		if err != nil {
			var netErr net.Error
			if (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, io.EOF) {
				return 0, nil
			}
			return 0, fmt.Errorf("rfc2217: %w", err)
		}
		// n == 0 with err == nil, or a read that contained only
		// negotiation/subnegotiation bytes: loop again.
	}
}

// Write writes plain console data, escaping any literal 0xFF byte as
// IAC IAC (RFC 854) so it cannot be misread as a telnet command.
func (c *Client) Write(p []byte) (int, error) {
	escaped := make([]byte, 0, len(p))
	for _, b := range p {
		if b == iacByte {
			escaped = append(escaped, iacByte, iacByte)
		} else {
			escaped = append(escaped, b)
		}
	}
	if _, err := c.conn.Write(escaped); err != nil {
		return 0, fmt.Errorf("rfc2217: %w", err)
	}
	return len(p), nil
}

// awaitComPortAgreement waits for the access server's DO or DONT reply
// to this Client's own WILL COM-PORT-OPTION, buffering any plain data
// bytes seen in the meantime (a server that sends a banner before
// completing negotiation is not unheard of) rather than discarding them.
func (c *Client) awaitComPortAgreement(ctx context.Context) error {
	deadline := time.Now().Add(c.readTimeout)
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("rfc2217: timed out waiting for the access server to agree to COM-PORT-OPTION")
		}
		if err := c.conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return fmt.Errorf("rfc2217: setting read deadline: %w", err)
		}

		n, err := c.conn.Read(buf)
		if n > 0 {
			data, _, verbs, ferr := c.filter.feed(c.conn, buf[:n])
			if ferr != nil {
				return ferr
			}
			c.buffered = append(c.buffered, data...)
			for _, verb := range verbs {
				switch verb {
				case doByte:
					return nil
				case dontByte:
					return errors.New("rfc2217: access server refused COM-PORT-OPTION (sent DONT)")
				default:
					return fmt.Errorf("rfc2217: access server sent negotiation verb %d for COM-PORT-OPTION, want DO or DONT in reply to WILL", verb)
				}
			}
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return errors.New("rfc2217: timed out waiting for the access server to agree to COM-PORT-OPTION")
			}
			return fmt.Errorf("rfc2217: %w", err)
		}
	}
}

// waitAck blocks until a COM-PORT-OPTION subnegotiation frame with
// command byte wantCmd arrives, buffering plain data bytes seen in the
// meantime, and discarding any OTHER COM-PORT-OPTION frame it sees along
// the way (see the package doc comment: an unsolicited frame is dropped,
// never applied).
func (c *Client) waitAck(ctx context.Context, wantCmd byte) ([]byte, error) {
	deadline := time.Now().Add(c.readTimeout)
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("rfc2217: timed out waiting for command %d acknowledgement", wantCmd)
		}
		if err := c.conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, fmt.Errorf("rfc2217: setting read deadline: %w", err)
		}

		n, err := c.conn.Read(buf)
		if n > 0 {
			data, frames, _, ferr := c.filter.feed(c.conn, buf[:n])
			if ferr != nil {
				return nil, ferr
			}
			c.buffered = append(c.buffered, data...)
			for _, fr := range frames {
				if fr.cmd == wantCmd {
					return fr.payload, nil
				}
				// Any other COM-PORT-OPTION frame here is either an ack
				// for a request this call never made, or an unsolicited
				// NOTIFY_LINESTATE/NOTIFY_MODEMSTATE/SERVER_SET_* the
				// access server sent on its own. Dropped, not applied.
			}
		}
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return nil, fmt.Errorf("rfc2217: timed out waiting for command %d acknowledgement", wantCmd)
			}
			return nil, fmt.Errorf("rfc2217: %w", err)
		}
	}
}

// sendSubneg writes one COM-PORT-OPTION subnegotiation frame:
// IAC SB 44 cmd arg... IAC SE, escaping any literal 0xFF byte within arg.
func (c *Client) sendSubneg(cmd byte, arg []byte) error {
	frame := make([]byte, 0, 6+len(arg)*2)
	frame = append(frame, iacByte, sbByte, comPortOption, cmd)
	for _, b := range arg {
		if b == iacByte {
			frame = append(frame, iacByte, iacByte)
		} else {
			frame = append(frame, b)
		}
	}
	frame = append(frame, iacByte, seByte)
	_, err := c.conn.Write(frame)
	return err
}

func (c *Client) setControl(ctx context.Context, value byte) error {
	if err := c.sendSubneg(cmdSetControl, []byte{value}); err != nil {
		return err
	}
	_, err := c.waitAck(ctx, cmdSetControl+serverAckOffset)
	return err
}

func (c *Client) setBaudRate(ctx context.Context, baud serialline.BaudRate) error {
	if baud <= 0 || baud > math.MaxUint32 {
		return fmt.Errorf("rfc2217: setting baud rate: %d is out of range for a 32-bit wire value", baud)
	}
	arg := make([]byte, 4)
	binary.BigEndian.PutUint32(arg, uint32(baud))
	if err := c.sendSubneg(cmdSetBaudRate, arg); err != nil {
		return fmt.Errorf("rfc2217: setting baud rate: %w", err)
	}
	got, err := c.waitAck(ctx, cmdSetBaudRate+serverAckOffset)
	if err != nil {
		return fmt.Errorf("rfc2217: setting baud rate: %w", err)
	}
	if len(got) != 4 || binary.BigEndian.Uint32(got) != uint32(baud) {
		return fmt.Errorf("rfc2217: setting baud rate: access server confirmed %v, want %d", got, baud)
	}
	return nil
}

func (c *Client) setDataSize(ctx context.Context, dataBits int) error {
	if dataBits < 0 || dataBits > 255 {
		return fmt.Errorf("rfc2217: setting data size: %d is out of range for a one-byte wire value", dataBits)
	}
	if err := c.sendSubneg(cmdSetDataSize, []byte{byte(dataBits)}); err != nil {
		return fmt.Errorf("rfc2217: setting data size: %w", err)
	}
	got, err := c.waitAck(ctx, cmdSetDataSize+serverAckOffset)
	if err != nil {
		return fmt.Errorf("rfc2217: setting data size: %w", err)
	}
	if len(got) != 1 || int(got[0]) != dataBits {
		return fmt.Errorf("rfc2217: setting data size: access server confirmed %v, want %d", got, dataBits)
	}
	return nil
}

func (c *Client) setParity(ctx context.Context, parity serialline.Parity) error {
	wire, err := parityToWire(parity)
	if err != nil {
		return fmt.Errorf("rfc2217: setting parity: %w", err)
	}
	if err := c.sendSubneg(cmdSetParity, []byte{wire}); err != nil {
		return fmt.Errorf("rfc2217: setting parity: %w", err)
	}
	got, err := c.waitAck(ctx, cmdSetParity+serverAckOffset)
	if err != nil {
		return fmt.Errorf("rfc2217: setting parity: %w", err)
	}
	if len(got) != 1 || got[0] != wire {
		return fmt.Errorf("rfc2217: setting parity: access server confirmed %v, want %d", got, wire)
	}
	return nil
}

func (c *Client) setStopSize(ctx context.Context, stopBits serialline.StopBits) error {
	wire, err := stopBitsToWire(stopBits)
	if err != nil {
		return fmt.Errorf("rfc2217: setting stop bits: %w", err)
	}
	if err := c.sendSubneg(cmdSetStopSize, []byte{wire}); err != nil {
		return fmt.Errorf("rfc2217: setting stop bits: %w", err)
	}
	got, err := c.waitAck(ctx, cmdSetStopSize+serverAckOffset)
	if err != nil {
		return fmt.Errorf("rfc2217: setting stop bits: %w", err)
	}
	if len(got) != 1 || got[0] != wire {
		return fmt.Errorf("rfc2217: setting stop bits: access server confirmed %v, want %d", got, wire)
	}
	return nil
}

// PurgeDirection names which side of a connection PurgeData should
// discard buffered data for.
type PurgeDirection byte

// The three purge directions RFC 2217's PURGE_DATA subnegotiation names.
const (
	PurgeAccessServerToClient PurgeDirection = 1
	PurgeClientToAccessServer PurgeDirection = 2
	PurgeBoth                 PurgeDirection = 3
)

// PurgeData purges buffered data in dir.
func (c *Client) PurgeData(ctx context.Context, dir PurgeDirection) error {
	if err := c.sendSubneg(cmdPurgeData, []byte{byte(dir)}); err != nil {
		return fmt.Errorf("rfc2217: purging data: %w", err)
	}
	_, err := c.waitAck(ctx, cmdPurgeData+serverAckOffset)
	if err != nil {
		return fmt.Errorf("rfc2217: purging data: %w", err)
	}
	return nil
}

// parityToWire converts serialline.Parity to RFC 2217's own SET_PARITY
// wire values explicitly, rather than relying on the two packages'
// enum orderings happening to coincide - the same discipline
// pkg/serialexec.parityFrom already established for go.bug.st/serial.
func parityToWire(p serialline.Parity) (byte, error) {
	switch p {
	case serialline.ParityNone:
		return 1, nil
	case serialline.ParityOdd:
		return 2, nil
	case serialline.ParityEven:
		return 3, nil
	case serialline.ParityMark:
		return 4, nil
	case serialline.ParitySpace:
		return 5, nil
	default:
		return 0, fmt.Errorf("unknown parity %v", p)
	}
}

// stopBitsToWire converts serialline.StopBits to RFC 2217's own
// SET_STOPSIZE wire values explicitly. Note the wire encoding is NOT the
// same ordering as serialline.StopBits' own iota (1.5 stop bits is wire
// value 3, not 1): translating with arithmetic instead of a named switch
// would have silently swapped OnePointFive and Two.
func stopBitsToWire(s serialline.StopBits) (byte, error) {
	switch s {
	case serialline.StopBitsOne:
		return 1, nil
	case serialline.StopBitsTwo:
		return 2, nil
	case serialline.StopBitsOnePointFive:
		return 3, nil
	default:
		return 0, fmt.Errorf("unknown stop bits %v", s)
	}
}

// writeIAC writes a two-byte telnet command (IAC cmd opt) to conn.
func writeIAC(conn io.Writer, cmd, opt byte) error {
	_, err := conn.Write([]byte{iacByte, cmd, opt})
	return err
}

// comPortFrame is one parsed COM-PORT-OPTION subnegotiation:
// IAC SB 44 cmd payload... IAC SE.
type comPortFrame struct {
	cmd     byte
	payload []byte
}

// filterState is where iacFilter is within one telnet command sequence,
// carried across separate feed calls (and therefore across separate
// net.Conn.Read calls) so a command split across two reads is still
// parsed correctly.
type filterState int

const (
	stateData filterState = iota
	stateIAC
	stateNegotiate
	stateSBOption
	stateSBPayload
	stateSBIAC
)

// iacFilter parses a raw telnet byte stream into three kinds of output:
// plain data bytes, parsed COM-PORT-OPTION subnegotiation frames, and
// negotiation verbs (DO/DONT/WILL/WONT) for COM-PORT-OPTION specifically,
// which the caller (awaitComPortAgreement) interprets rather than this
// filter auto-refusing them the way pkg/telnetexec's own filter refuses
// every option unconditionally. Any OTHER option offered is still
// auto-refused here exactly like pkg/telnetexec: a console server
// negotiating ECHO or SUPPRESS-GO-AHEAD alongside COM-PORT-OPTION is not
// unheard of, and refusing it is the same safe default.
type iacFilter struct {
	state       filterState
	negotiating byte
	sbOption    byte
	sbPayload   []byte
}

// feed processes raw, just-read bytes, writing any required negotiation
// replies to conn immediately (for options other than COM-PORT-OPTION),
// and returns the plain data bytes, any complete COM-PORT-OPTION frames,
// and any negotiation verbs seen for COM-PORT-OPTION itself.
func (f *iacFilter) feed(conn io.Writer, raw []byte) (data []byte, frames []comPortFrame, comPortVerbs []byte, err error) {
	for _, b := range raw {
		switch f.state {
		case stateData:
			if b == iacByte {
				f.state = stateIAC
			} else {
				data = append(data, b)
			}
		case stateIAC:
			switch b {
			case iacByte:
				data = append(data, 0xFF)
				f.state = stateData
			case doByte, dontByte, willByte, wontByte:
				f.negotiating = b
				f.state = stateNegotiate
			case sbByte:
				f.state = stateSBOption
			default:
				f.state = stateData
			}
		case stateNegotiate:
			if b == comPortOption {
				comPortVerbs = append(comPortVerbs, f.negotiating)
				f.state = stateData
				continue
			}
			switch f.negotiating {
			case doByte:
				if werr := writeIAC(conn, wontByte, b); werr != nil {
					return data, frames, comPortVerbs, werr
				}
			case willByte:
				if werr := writeIAC(conn, dontByte, b); werr != nil {
					return data, frames, comPortVerbs, werr
				}
			}
			f.state = stateData
		case stateSBOption:
			f.sbOption = b
			f.sbPayload = nil
			f.state = stateSBPayload
		case stateSBPayload:
			if b == iacByte {
				f.state = stateSBIAC
			} else {
				if len(f.sbPayload) >= maxSBPayload {
					return data, frames, comPortVerbs, fmt.Errorf("rfc2217: subnegotiation payload exceeded %d bytes without a terminating IAC SE", maxSBPayload)
				}
				f.sbPayload = append(f.sbPayload, b)
			}
		case stateSBIAC:
			switch b {
			case seByte:
				if f.sbOption == comPortOption && len(f.sbPayload) > 0 {
					frames = append(frames, comPortFrame{cmd: f.sbPayload[0], payload: append([]byte(nil), f.sbPayload[1:]...)})
				}
				f.state = stateData
			case iacByte:
				if len(f.sbPayload) >= maxSBPayload {
					return data, frames, comPortVerbs, fmt.Errorf("rfc2217: subnegotiation payload exceeded %d bytes without a terminating IAC SE", maxSBPayload)
				}
				f.sbPayload = append(f.sbPayload, 0xFF)
				f.state = stateSBPayload
			default:
				// Malformed: not IAC-escape or SE after IAC inside SB.
				// Fail closed by resuming as subnegotiation payload
				// rather than reinterpreting b as a fresh command, so a
				// truncated or hostile frame cannot desynchronize this
				// state machine into misreading attacker-controlled
				// bytes as arbitrary telnet commands.
				f.state = stateSBPayload
			}
		}
	}
	return data, frames, comPortVerbs, nil
}
