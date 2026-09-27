package rfc2217_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/rfc2217"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
	"strings"
)

// This file is the RULE 0 evidence for pkg/rfc2217's negotiation and
// subnegotiation logic, against a real, in-process, real-TCP fake access
// server -- the same discipline pkg/remoteexec's own fake-SSH-server
// suite and pkg/serialtcp's own fake-TCP-server suite already establish.
// It proves the wire protocol Client speaks is genuinely correct. It
// does NOT prove a real console server's line actually changed baud,
// parity, or modem control state -- see the package doc comment for why
// that evidence is deferred to Phase 73's own bastion-proof workstream,
// which runs this Client against a real ser2net access server instead.

const (
	iacByte       = 255
	dontByte      = 254
	doByte        = 253
	wontByte      = 252
	willByte      = 251
	sbByte        = 250
	seByte        = 240
	comPortOption = 44

	cmdSetBaudRate = 1
	cmdSetDataSize = 2
	cmdSetParity   = 3
	cmdSetStopSize = 4
	cmdSetControl  = 5
	cmdPurgeData   = 12
	serverAckOff   = 100
)

// fakeAccessServer starts a real TCP listener on 127.0.0.1 that hands
// each accepted connection to handle, and returns the port to dial.
func fakeAccessServer(t *testing.T, handle func(net.Conn)) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handle(conn)
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

// negotiateComPortOption reads the client's IAC WILL COM-PORT-OPTION and
// replies IAC verb COM-PORT-OPTION.
func negotiateComPortOption(t *testing.T, conn net.Conn, verb byte) {
	t.Helper()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("reading WILL COM-PORT-OPTION: %v", err)
	}
	want := []byte{iacByte, willByte, comPortOption}
	if !bytes.Equal(buf, want) {
		t.Fatalf("client sent %v, want %v (IAC WILL COM-PORT-OPTION)", buf, want)
	}
	if _, err := conn.Write([]byte{iacByte, verb, comPortOption}); err != nil {
		t.Fatalf("writing negotiation reply: %v", err)
	}
}

// readSubnegFrame reads one IAC SB 44 cmd payload... IAC SE frame,
// handling escaped IAC bytes within the payload.
func readSubnegFrame(t *testing.T, conn net.Conn) (cmd byte, payload []byte) {
	t.Helper()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("reading subnegotiation header: %v", err)
	}
	if hdr[0] != iacByte || hdr[1] != sbByte || hdr[2] != comPortOption {
		t.Fatalf("subnegotiation header = %v, want IAC SB COM-PORT-OPTION ...", hdr)
	}
	cmd = hdr[3]

	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(conn, one); err != nil {
			t.Fatalf("reading subnegotiation payload: %v", err)
		}
		if one[0] != iacByte {
			payload = append(payload, one[0])
			continue
		}
		if _, err := io.ReadFull(conn, one); err != nil {
			t.Fatalf("reading byte after IAC in subnegotiation: %v", err)
		}
		if one[0] == seByte {
			return cmd, payload
		}
		if one[0] == iacByte {
			payload = append(payload, 0xFF)
			continue
		}
		t.Fatalf("unexpected byte %d after IAC inside subnegotiation", one[0])
	}
}

// writeAck writes IAC SB 44 ackCmd payload IAC SE, escaping any literal
// 0xFF byte in payload.
func writeAck(t *testing.T, conn net.Conn, ackCmd byte, payload []byte) {
	t.Helper()
	frame := []byte{iacByte, sbByte, comPortOption, ackCmd}
	for _, b := range payload {
		if b == iacByte {
			frame = append(frame, iacByte, iacByte)
		} else {
			frame = append(frame, b)
		}
	}
	frame = append(frame, iacByte, seByte)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("writing ack: %v", err)
	}
}

func baud4(b uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, b)
	return buf
}

// TestDial_NegotiatesComPortOptionSuccessfully proves the happy path:
// the client announces WILL COM-PORT-OPTION, the access server agrees
// with DO, and Dial returns a usable Client.
func TestDial_NegotiatesComPortOptionSuccessfully(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		negotiateComPortOption(t, conn, doByte)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
}

// TestDial_RefusalReturnsAClearError proves an access server that
// refuses COM-PORT-OPTION (DONT) fails Dial with a clear, non-nil error
// rather than proceeding as if it had agreed.
func TestDial_RefusalReturnsAClearError(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		negotiateComPortOption(t, conn, dontByte)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: time.Second})
	if err == nil {
		t.Fatal("expected an error when the access server refuses COM-PORT-OPTION")
	}
}

// TestDial_TimesOutWaitingForAReply proves Dial does not hang forever
// waiting for a negotiation reply that never comes.
func TestDial_TimesOutWaitingForAReply(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: 200 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Dial took %v, expected the ReadTimeout to fire promptly", elapsed)
	}
}

// TestDial_FailureIsAClearError proves a connection refused (a closed
// listener) fails with a wrapped error, not a panic.
func TestDial_FailureIsAClearError(t *testing.T) {
	// Port 0, not a released listener's port: see
	// FAILURE_PATTERNS.md #123 and #177 for why the latter is a race
	// that has already failed in this repository twice.
	_, err := rfc2217.Dial(context.Background(), "127.0.0.1", 0, rfc2217.Options{})
	if err == nil {
		t.Fatal("expected an error dialing an address nothing can be listening on")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:0") {
		t.Errorf("error %q does not name the address it failed to reach", err)
	}
}

// TestDial_ContextCancellationIsPropagated proves Dial surfaces an
// already-canceled context as its own error rather than swallowing it.
func TestDial_ContextCancellationIsPropagated(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Dial error = %v, want it to wrap context.Canceled", err)
	}
}

// dialAgreeing dials against a fresh fake access server that always
// agrees to COM-PORT-OPTION, and hands the test the connected server
// side to script the rest of the exchange.
func dialAgreeing(t *testing.T, serverAfterNegotiation func(net.Conn)) *rfc2217.Client {
	t.Helper()
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		negotiateComPortOption(t, conn, doByte)
		serverAfterNegotiation(conn)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestClient_SetLine_RoundTrips proves all four line settings are sent
// in order, with the exact RFC 2217 wire encoding, and a Client that
// receives a matching acknowledgement for each reports success.
func TestClient_SetLine_RoundTrips(t *testing.T) {
	line := serialline.Config{
		BaudRate: 19200,
		DataBits: 8,
		Parity:   serialline.ParityEven,
		StopBits: serialline.StopBitsOnePointFive,
	}

	c := dialAgreeing(t, func(conn net.Conn) {
		cmd, payload := readSubnegFrame(t, conn)
		if cmd != cmdSetBaudRate || !bytes.Equal(payload, baud4(19200)) {
			t.Errorf("baud frame = cmd %d payload %v, want cmd %d payload %v", cmd, payload, cmdSetBaudRate, baud4(19200))
		}
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		if cmd != cmdSetDataSize || !bytes.Equal(payload, []byte{8}) {
			t.Errorf("data size frame = cmd %d payload %v, want cmd %d payload [8]", cmd, payload, cmdSetDataSize)
		}
		writeAck(t, conn, cmdSetDataSize+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		if cmd != cmdSetParity || !bytes.Equal(payload, []byte{3}) { // Even = 3
			t.Errorf("parity frame = cmd %d payload %v, want cmd %d payload [3]", cmd, payload, cmdSetParity)
		}
		writeAck(t, conn, cmdSetParity+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		if cmd != cmdSetStopSize || !bytes.Equal(payload, []byte{3}) { // OnePointFive = 3
			t.Errorf("stop size frame = cmd %d payload %v, want cmd %d payload [3]", cmd, payload, cmdSetStopSize)
		}
		writeAck(t, conn, cmdSetStopSize+serverAckOff, payload)

		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SetLine(ctx, line); err != nil {
		t.Fatalf("SetLine: %v", err)
	}
}

// TestClient_SetLine_MismatchIsDetected proves an access server that
// acknowledges a DIFFERENT baud rate than was requested is treated as a
// real failure, not silently accepted.
func TestClient_SetLine_MismatchIsDetected(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, _ = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, baud4(9600)) // wrong: client asked for 19200
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.SetLine(ctx, serialline.Config{BaudRate: 19200, DataBits: 8})
	if err == nil {
		t.Fatal("expected an error when the access server confirms a different baud rate than requested")
	}
}

// TestClient_SetLine_RefusesOutOfRangeBaudRate proves a baud rate that
// cannot fit in this protocol's own 32-bit wire value is refused before
// any subnegotiation is sent, rather than silently wrapping to an
// unrelated value (found during Phase 73's own gosec pass, G115
// integer-overflow conversion int -> uint32).
func TestClient_SetLine_RefusesOutOfRangeBaudRate(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		<-make(chan struct{}) // must never be asked to negotiate anything
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.SetLine(ctx, serialline.Config{BaudRate: -1, DataBits: 8}); err == nil {
		t.Fatal("expected an error for a negative baud rate")
	}
}

// TestClient_SetLine_RefusesOutOfRangeDataBits mirrors the baud-rate
// bound above for data bits, which this protocol sends as a single wire
// byte: a value outside 0-255 is refused rather than silently truncated
// (the same gosec G115 finding, for int -> byte).
func TestClient_SetLine_RefusesOutOfRangeDataBits(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, payload) // baud: honest ack, so the failure isolates to data bits
		<-make(chan struct{})                                   // must never be asked to negotiate data size
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.SetLine(ctx, serialline.Config{BaudRate: 9600, DataBits: 999}); err == nil {
		t.Fatal("expected an error for a data bits value that cannot fit in one wire byte")
	}
}

// TestClient_SetLine_DataSizeMismatchIsDetected mirrors
// TestClient_SetLine_MismatchIsDetected for the data-size setting.
func TestClient_SetLine_DataSizeMismatchIsDetected(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, payload) // baud: honest ack
		_, _ = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetDataSize+serverAckOff, []byte{7}) // wrong: asked for 8
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.SetLine(ctx, serialline.Config{BaudRate: 9600, DataBits: 8})
	if err == nil {
		t.Fatal("expected an error when the access server confirms a different data size than requested")
	}
}

// TestClient_SetLine_ParityMismatchIsDetected mirrors
// TestClient_SetLine_MismatchIsDetected for the parity setting.
func TestClient_SetLine_ParityMismatchIsDetected(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, payload)
		_, payload = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetDataSize+serverAckOff, payload)
		_, _ = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetParity+serverAckOff, []byte{2}) // wrong: asked for None(1)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.SetLine(ctx, serialline.Config{BaudRate: 9600, DataBits: 8, Parity: serialline.ParityNone})
	if err == nil {
		t.Fatal("expected an error when the access server confirms a different parity than requested")
	}
}

// TestClient_SetLine_StopBitsMismatchIsDetected mirrors
// TestClient_SetLine_MismatchIsDetected for the stop-bits setting.
func TestClient_SetLine_StopBitsMismatchIsDetected(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetBaudRate+serverAckOff, payload)
		_, payload = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetDataSize+serverAckOff, payload)
		_, payload = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetParity+serverAckOff, payload)
		_, _ = readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetStopSize+serverAckOff, []byte{2}) // wrong: asked for One(1)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.SetLine(ctx, serialline.Config{BaudRate: 9600, DataBits: 8, Parity: serialline.ParityNone, StopBits: serialline.StopBitsOne})
	if err == nil {
		t.Fatal("expected an error when the access server confirms different stop bits than requested")
	}
}

// TestClient_SetControl_TimesOutWaitingForAnAcknowledgement proves
// waitAck does not hang forever when the access server accepts the
// subnegotiation but never acknowledges it.
func TestClient_SetControl_TimesOutWaitingForAnAcknowledgement(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, _ = readSubnegFrame(t, conn)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := c.AssertDTR(ctx, true)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error waiting for the SET_CONTROL acknowledgement")
	}
	if elapsed > 3*time.Second {
		t.Errorf("AssertDTR took %v, expected the ReadTimeout to fire promptly", elapsed)
	}
}

// TestClient_SendBreak_ContextCancellationDuringTheHoldIsPropagated
// proves a canceled context interrupts SendBreak while it is holding the
// break condition, rather than always running the hold to completion.
func TestClient_SendBreak_ContextCancellationDuringTheHoldIsPropagated(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.SendBreak(ctx, 5*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("SendBreak error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestClient_Read_ContextCancellationIsPropagated proves Read surfaces
// an already-canceled context rather than blocking.
func TestClient_Read_ContextCancellationIsPropagated(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		<-make(chan struct{})
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	buf := make([]byte, 16)
	_, err := c.Read(ctx, buf)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Read error = %v, want it to wrap context.Canceled", err)
	}
}

// TestClient_Read_GenuineErrorIsSurfaced proves a real read failure (a
// TCP RST, forced deterministically via SO_LINGER=0) is surfaced as an
// error rather than treated as "nothing new right now" -- the same
// distinction FAILURE_PATTERNS.md #172 records pkg/serialtcp getting
// wrong on its first draft.
func TestClient_Read_GenuineErrorIsSurfaced(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		negotiateComPortOption(t, conn, doByte)
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			return
		}
		_ = tcpConn.SetLinger(0)
		_ = tcpConn.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	buf := make([]byte, 16)
	if _, err := c.Read(ctx, buf); err == nil {
		t.Fatal("expected a genuine read error (connection reset) to be surfaced")
	}
}

// TestClient_SetLine_SendFailureIsWrapped and
// TestClient_SetControl_SendFailureIsWrapped and
// TestClient_PurgeData_SendFailureIsWrapped each prove a real sendSubneg
// write failure (writing to an already-closed connection) reaches the
// caller as a wrapped, non-nil error, for the three distinct call sites
// (setBaudRate, setControl, PurgeData) cheap to force this way. The
// remaining sendSubneg call sites (setDataSize, setParity, setStopSize)
// are structurally identical but only reachable mid-SetLine, after a
// successful baud exchange, which this closed-connection trick cannot
// isolate to a single later step -- the same class of gap this module
// already accepts elsewhere (pkg/serialexec.Exec's SetReadTimeout/Write
// checks, pkg/serialtcp.readUntilQuiet's SetReadDeadline check) rather
// than one this suite chases with fault injection it does not otherwise
// use.
func TestClient_SetLine_SendFailureIsWrapped(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) { <-make(chan struct{}) })
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.SetLine(context.Background(), serialline.Config{BaudRate: 9600, DataBits: 8}); err == nil {
		t.Fatal("expected an error when sendSubneg fails on an already-closed connection")
	}
}

func TestClient_SetControl_SendFailureIsWrapped(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) { <-make(chan struct{}) })
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.AssertDTR(context.Background(), true); err == nil {
		t.Fatal("expected an error when sendSubneg fails on an already-closed connection")
	}
	if err := c.AssertRTS(context.Background(), true); err == nil {
		t.Fatal("expected an error when sendSubneg fails on an already-closed connection")
	}
	if err := c.SendBreak(context.Background(), time.Millisecond); err == nil {
		t.Fatal("expected an error when sendSubneg fails on an already-closed connection (break-on)")
	}
}

// TestClient_SendBreak_ClearFailureIsWrapped proves the SECOND
// sendSubneg call inside SendBreak (clearing the break, after the hold)
// has its own failure wrapped too, distinct from the break-on call
// TestClient_SetControl_SendFailureIsWrapped already covers: the
// connection is closed from the server side only after the break-on
// acknowledgement, so the hold genuinely starts before the break-off
// send fails.
func TestClient_SendBreak_ClearFailureIsWrapped(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		_, payload := readSubnegFrame(t, conn)
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)
		_ = conn.Close()
	})

	err := c.SendBreak(context.Background(), 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error clearing the break on a connection the far end already closed")
	}
}

func TestClient_PurgeData_SendFailureIsWrapped(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) { <-make(chan struct{}) })
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.PurgeData(context.Background(), rfc2217.PurgeBoth); err == nil {
		t.Fatal("expected an error when sendSubneg fails on an already-closed connection")
	}
}

// TestClient_Write_ErrorIsWrapped proves a real write failure (writing
// to an already-closed connection) reaches the caller as a wrapped,
// non-nil error.
func TestClient_Write_ErrorIsWrapped(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		<-make(chan struct{})
	})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("expected an error writing to an already-closed connection")
	}
}

// TestDial_UnexpectedNegotiationVerbIsAClearError proves an access
// server that replies to this client's own WILL COM-PORT-OPTION with
// anything other than DO or DONT (here, WONT - a malformed reply no real
// console server this platform targets sends) is treated as
// protocol-nonconformant rather than guessed at.
func TestDial_UnexpectedNegotiationVerbIsAClearError(t *testing.T) {
	port := fakeAccessServer(t, func(conn net.Conn) {
		defer conn.Close()
		negotiateComPortOption(t, conn, wontByte)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := rfc2217.Dial(ctx, "127.0.0.1", port, rfc2217.Options{ReadTimeout: time.Second})
	if err == nil {
		t.Fatal("expected an error for an unexpected negotiation verb (WONT) in reply to WILL")
	}
}

// TestClient_AssertDTR_SendsCorrectControlValues proves DTR on/off send
// RFC 2217's own SET_CONTROL values 8/9.
func TestClient_AssertDTR_SendsCorrectControlValues(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		cmd, payload := readSubnegFrame(t, conn)
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{8}) {
			t.Errorf("DTR-on frame = cmd %d payload %v, want cmd %d payload [8]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{9}) {
			t.Errorf("DTR-off frame = cmd %d payload %v, want cmd %d payload [9]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.AssertDTR(ctx, true); err != nil {
		t.Fatalf("AssertDTR(true): %v", err)
	}
	if err := c.AssertDTR(ctx, false); err != nil {
		t.Fatalf("AssertDTR(false): %v", err)
	}
}

// TestClient_AssertRTS_SendsCorrectControlValues proves RTS on/off send
// RFC 2217's own SET_CONTROL values 11/12.
func TestClient_AssertRTS_SendsCorrectControlValues(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		cmd, payload := readSubnegFrame(t, conn)
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{11}) {
			t.Errorf("RTS-on frame = cmd %d payload %v, want cmd %d payload [11]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{12}) {
			t.Errorf("RTS-off frame = cmd %d payload %v, want cmd %d payload [12]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.AssertRTS(ctx, true); err != nil {
		t.Fatalf("AssertRTS(true): %v", err)
	}
	if err := c.AssertRTS(ctx, false); err != nil {
		t.Fatalf("AssertRTS(false): %v", err)
	}
}

// TestClient_SendBreak_AssertsThenClearsAfterTheRequestedDuration proves
// SendBreak sends SET_CONTROL break-on (5), waits roughly the requested
// duration, then sends break-off (6) -- the real break condition a
// console server would observe, timed on the wire, not just called
// twice back to back.
func TestClient_SendBreak_AssertsThenClearsAfterTheRequestedDuration(t *testing.T) {
	var onAt, offAt time.Time
	c := dialAgreeing(t, func(conn net.Conn) {
		cmd, payload := readSubnegFrame(t, conn)
		onAt = time.Now()
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{5}) {
			t.Errorf("break-on frame = cmd %d payload %v, want cmd %d payload [5]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		cmd, payload = readSubnegFrame(t, conn)
		offAt = time.Now()
		if cmd != cmdSetControl || !bytes.Equal(payload, []byte{6}) {
			t.Errorf("break-off frame = cmd %d payload %v, want cmd %d payload [6]", cmd, payload, cmdSetControl)
		}
		writeAck(t, conn, cmdSetControl+serverAckOff, payload)

		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const want = 150 * time.Millisecond
	if err := c.SendBreak(ctx, want); err != nil {
		t.Fatalf("SendBreak: %v", err)
	}
	if got := offAt.Sub(onAt); got < want || got > want+500*time.Millisecond {
		t.Errorf("break held for %v, want roughly %v", got, want)
	}
}

// TestClient_PurgeData_RoundTrips proves PurgeData sends the requested
// direction byte and succeeds on a matching acknowledgement.
func TestClient_PurgeData_RoundTrips(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		cmd, payload := readSubnegFrame(t, conn)
		if cmd != cmdPurgeData || !bytes.Equal(payload, []byte{byte(rfc2217.PurgeBoth)}) {
			t.Errorf("purge frame = cmd %d payload %v, want cmd %d payload [%d]", cmd, payload, cmdPurgeData, rfc2217.PurgeBoth)
		}
		writeAck(t, conn, cmdPurgeData+serverAckOff, payload)
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.PurgeData(ctx, rfc2217.PurgeBoth); err != nil {
		t.Fatalf("PurgeData: %v", err)
	}
}

// TestClient_Read_UnsolicitedFrameIsDroppedNotDelivered proves the
// package doc comment's central adversarial-relevant claim end to end:
// an unsolicited COM-PORT-OPTION subnegotiation the client never
// requested (here, a NOTIFY_LINESTATE-shaped frame) is silently
// discarded rather than surfaced as console data or acted on, and plain
// data arriving afterward is still delivered correctly with nothing
// leaked or corrupted.
func TestClient_Read_UnsolicitedFrameIsDroppedNotDelivered(t *testing.T) {
	c := dialAgreeing(t, func(conn net.Conn) {
		// An unsolicited frame this Client never asked for.
		writeAck(t, conn, 6 /* NOTIFY_LINESTATE */, []byte{0x60})
		_, _ = conn.Write([]byte("console output\r\n"))
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(3 * time.Second)
	for len(got) < len("console output\r\n") && time.Now().Before(deadline) {
		n, err := c.Read(ctx, buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got = append(got, buf[:n]...)
	}

	if string(got) != "console output\r\n" {
		t.Errorf("Read accumulated %q, want exactly %q (the unsolicited frame must not leak into it)", got, "console output\r\n")
	}
}

// TestClient_Write_EscapesLiteralIACBytes proves Write escapes a literal
// 0xFF data byte on the wire, and the plain text around it survives
// intact.
func TestClient_Write_EscapesLiteralIACBytes(t *testing.T) {
	received := make(chan []byte, 1)
	c := dialAgreeing(t, func(conn net.Conn) {
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		if err != nil {
			received <- nil
			return
		}
		received <- append([]byte(nil), buf[:n]...)
		<-make(chan struct{})
	})

	n, err := c.Write([]byte{'a', 0xFF, 'b'})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 3 {
		t.Errorf("Write returned n=%d, want 3 (the logical byte count, not the escaped wire count)", n)
	}

	got := <-received
	want := []byte{'a', iacByte, iacByte, 'b'}
	if !bytes.Equal(got, want) {
		t.Errorf("wire bytes = %v, want %v", got, want)
	}
}
