// Package serialexec runs a command over a local serial line and is the
// single place this platform does that.
//
// It exists for the reason pkg/remoteexec and pkg/winrmexec exist. A
// Collection may import pkg/ and the standard library and nothing else
// in this module, which internal/archtest enforces, so a Collection
// method reaching a local serial device cannot use an adapter living
// under internal/ no matter how much of the same work it needs.
//
// # No exit status, ever
//
// A serial console has no protocol-level concept of a remote command's
// exit status: the device wrote some bytes back, and that is everything
// anyone knows. Result mirrors internal/transport.Result's
// ExitStatusUnknown field (always true here) rather than importing that
// package -- pkg/ may not import internal/ -- so
// internal/transport/serial's translation is a field-for-field copy,
// not a second judgment call about what "no exit status" means.
//
// # How "the command finished" is decided with no delimiter at all
//
// Unlike SSH or WinRM, there is no protocol framing marking where a
// command's output ends. Exec writes the command, then reads with a
// bounded per-call timeout (Options.ReadTimeout, via
// go.bug.st/serial's own Port.SetReadTimeout) in a loop, accumulating
// bytes, until one read call returns zero bytes with no error -- a
// quiet period, go.bug.st/serial's own documented signal
// (serial_unix.go: "// Timeout happened; return 0, nil") that no more
// output is coming for now. That is the same shape this phase's own
// RULE 0 test proves against a real socat PTY pair: a read timeout
// genuinely fires rather than this package hanging forever waiting for
// a prompt it cannot recognize.
//
// Context cancellation is checked only BETWEEN read calls, not during
// one: go.bug.st/serial's blocking Read cannot be preempted mid-call
// without closing the port from another goroutine, which would race
// with a concurrent read. This bounds cancellation latency by
// Options.ReadTimeout, a documented trade-off rather than a silent gap.
package serialexec

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
	realserial "go.bug.st/serial"
)

// Options configures how Exec runs one command over a serial line.
type Options struct {
	// ReadTimeout bounds each individual read call while accumulating a
	// command's output; the quiet period after the last byte is what
	// tells Exec the device has finished writing. Zero takes
	// DefaultReadTimeout.
	ReadTimeout time.Duration

	// MaxOutputBytes bounds the total output accumulated before a run is
	// refused outright, so a device stuck emitting output forever cannot
	// exhaust memory. Zero takes DefaultMaxOutputBytes.
	MaxOutputBytes int
}

const (
	// DefaultReadTimeout is how long one read call waits for the next
	// byte before Exec decides output has gone quiet.
	DefaultReadTimeout = 500 * time.Millisecond

	// DefaultMaxOutputBytes bounds accumulated output. 1 MiB, the same
	// order of magnitude pkg/sdk's own structured-input caps use
	// elsewhere in this module for document-shaped content.
	DefaultMaxOutputBytes = 1 << 20
)

// Result is what running one command over a serial line produced.
type Result struct {
	// Stdout is everything the device wrote back before output went
	// quiet.
	Stdout string

	// ExitStatusUnknown is always true: see this package's own doc
	// comment for why a serial console can never report one.
	ExitStatusUnknown bool
}

// Exec opens device configured with line, writes command terminated by
// "\r\n" (the line ending a typed command over a terminal session
// expects), and reads back whatever the device writes until output goes
// quiet, ctx is canceled, or opts.MaxOutputBytes is reached.
//
// There is no authentication of any kind: local serial has no
// protocol-level login this package could perform. A device that
// prompts for a login over the wire is session content, not a transport
// credential -- see pkg/capability.SerialCapable's own doc comment.
func Exec(ctx context.Context, device serialline.Device, line serialline.Config, opts Options, command string) (Result, error) {
	mode, err := modeFrom(line)
	if err != nil {
		return Result{}, fmt.Errorf("serialexec: %w", err)
	}

	port, err := realserial.Open(string(device), mode)
	if err != nil {
		return Result{}, fmt.Errorf("serialexec: opening %s: %w", device, err)
	}
	defer func() { _ = port.Close() }()

	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	// These two error checks are real defensive coverage, not dead code,
	// but neither is reachable from this package's own test suite: a
	// freshly Open'd port's SetReadTimeout and a Write of a
	// realistically sized command essentially never fail on their own
	// against a real device (PTY-backed or otherwise) without a genuine
	// hardware or driver fault, which no test in this module fabricates.
	// Left in place because a future device or platform's failure mode
	// here is exactly the case a caller should see a clear, named error
	// for rather than a generic one from deeper in go.bug.st/serial.
	if err := port.SetReadTimeout(readTimeout); err != nil {
		return Result{}, fmt.Errorf("serialexec: setting read timeout: %w", err)
	}

	if _, err := port.Write([]byte(command + "\r\n")); err != nil {
		return Result{}, fmt.Errorf("serialexec: writing command: %w", err)
	}

	output, err := readUntilQuiet(ctx, port, maxOutput)
	if err != nil {
		return Result{}, fmt.Errorf("serialexec: %w", err)
	}

	return Result{Stdout: string(output), ExitStatusUnknown: true}, nil
}

// readUntilQuiet accumulates bytes from port until a read call returns
// zero bytes with no error (a quiet period: see this package's own doc
// comment), ctx is canceled, or the accumulated total exceeds maxOutput,
// in which case it is refused outright rather than silently truncated --
// the same posture this module's other document-shaped-input caps take.
func readUntilQuiet(ctx context.Context, port realserial.Port, maxOutput int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		n, err := port.Read(buf)
		if err != nil {
			return out, err
		}
		if n == 0 {
			return out, nil
		}

		out = append(out, buf[:n]...)
		if len(out) > maxOutput {
			return nil, fmt.Errorf("output exceeded %d bytes without going quiet", maxOutput)
		}
	}
}

// modeFrom converts line into the *realserial.Mode go.bug.st/serial
// expects, translating Parity and StopBits explicitly rather than
// relying on the two packages' enum orderings happening to coincide, so
// a future reordering on either side fails at this one seam instead of
// silently swapping settings.
func modeFrom(line serialline.Config) (*realserial.Mode, error) {
	parity, err := parityFrom(line.Parity)
	if err != nil {
		return nil, err
	}
	stopBits, err := stopBitsFrom(line.StopBits)
	if err != nil {
		return nil, err
	}
	return &realserial.Mode{
		BaudRate: int(line.BaudRate),
		DataBits: line.DataBits,
		Parity:   parity,
		StopBits: stopBits,
	}, nil
}

func parityFrom(p serialline.Parity) (realserial.Parity, error) {
	switch p {
	case serialline.ParityNone:
		return realserial.NoParity, nil
	case serialline.ParityOdd:
		return realserial.OddParity, nil
	case serialline.ParityEven:
		return realserial.EvenParity, nil
	case serialline.ParityMark:
		return realserial.MarkParity, nil
	case serialline.ParitySpace:
		return realserial.SpaceParity, nil
	default:
		return 0, fmt.Errorf("unknown parity %v", p)
	}
}

func stopBitsFrom(s serialline.StopBits) (realserial.StopBits, error) {
	switch s {
	case serialline.StopBitsOne:
		return realserial.OneStopBit, nil
	case serialline.StopBitsOnePointFive:
		return realserial.OnePointFiveStopBits, nil
	case serialline.StopBitsTwo:
		return realserial.TwoStopBits, nil
	default:
		return 0, fmt.Errorf("unknown stop bits %v", s)
	}
}
