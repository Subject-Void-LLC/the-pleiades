// The legacy SCP wire protocol as pure functions over readers and writers.
package scpxfer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// The legacy SCP wire protocol, as OpenSSH's scp.c speaks it in its sink
// ("scp -t") and source ("scp -f") modes. Every message is one line or
// one byte:
//
//	C<mode> <size> <name>\n   a file record, followed by exactly size bytes
//	\x00                      acknowledged
//	\x01<message>\n           a warning, which this client treats as fatal
//	\x02<message>\n           a fatal error
//	D, E, T records           directories and timestamps, never accepted here
//
// This file holds that protocol as pure functions over readers and
// writers, so it can be fuzzed against arbitrary server bytes with no
// network at all.

const (
	// maxRecordBytes caps one record line. The longest honest record is
	// "C0644 " plus a 19-digit size, a space, a 255-byte name and a
	// newline; 4 KiB leaves room without letting a hostile server make
	// this process hold an endless line.
	maxRecordBytes = 4 << 10

	// maxMessageBytes caps a server's warning or error message.
	maxMessageBytes = 1 << 10

	// tempFileMode is the mode Put's header announces. It is only the
	// mode the file is CREATED with, inside an already private directory;
	// the requested mode is applied exactly by chmod afterward, since
	// the sink applies the header's mode less the account's umask.
	tempFileMode filexfer.Mode = 0o600
)

// errUnexpectedByte is a reply that is not a record, an acknowledgement
// or an error. The classic cause is a shell startup file on the device
// printing to standard output, which corrupts every SCP session there.
var errUnexpectedByte = errors.New("unexpected protocol byte (a shell startup file on the device may be printing to standard output)")

// RemoteError is a warning or fatal error the device's scp reported
// in-band, with its message rendered safely.
type RemoteError struct {
	// Fatal is true for a \x02 reply and false for a \x01 warning. Both
	// end the transfer: this client moves exactly one file, so there is
	// nothing a warning could be about that leaves the transfer sound.
	Fatal bool
	// Message is the device's text, truncated to maxMessageBytes.
	Message string
}

// Error renders the device's message quoted, so a control character in
// it is visible rather than printed raw into a log.
func (e *RemoteError) Error() string {
	kind := "warning"
	if e.Fatal {
		kind = "error"
	}
	return fmt.Sprintf("scp on the device reported an %s: %q", kind, e.Message)
}

// readAck reads one reply byte: nil for an acknowledgement, a
// *RemoteError for a warning or error, errUnexpectedByte otherwise.
func readAck(br *bufio.Reader) error {
	b, err := br.ReadByte()
	if err != nil {
		return fmt.Errorf("reading an acknowledgement: %w", noEOF(err))
	}
	switch b {
	case 0:
		return nil
	case 1, 2:
		msg, err := readLine(br, maxMessageBytes)
		if err != nil {
			return fmt.Errorf("reading the device's message: %w", err)
		}
		return &RemoteError{Fatal: b == 2, Message: msg}
	default:
		return fmt.Errorf("%w: got %q", errUnexpectedByte, b)
	}
}

// readLine reads up to and including a newline, returning the line
// without it, and refuses a line longer than limit.
func readLine(br *bufio.Reader, limit int) (string, error) {
	var sb strings.Builder
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", noEOF(err)
		}
		if b == '\n' {
			return sb.String(), nil
		}
		if sb.Len() >= limit {
			return "", fmt.Errorf("line longer than %d bytes", limit)
		}
		sb.WriteByte(b)
	}
}

// noEOF turns an end of stream in the middle of a message into
// io.ErrUnexpectedEOF, so a truncated reply is never mistaken for a
// clean finish.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// record is a parsed C record.
type record struct {
	mode filexfer.Mode
	size int64
	name string
}

// parseRecord parses "C<mode> <size> <name>" (the leading C already
// consumed by the caller is NOT expected here: line starts at the
// mode). It is strict: exactly four octal digits, a plain decimal size
// with no sign that fits int64, and a name that is one path segment.
func parseRecord(line string) (record, error) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) != 3 {
		return record{}, fmt.Errorf("malformed file record %q", line)
	}
	modeText, sizeText, name := fields[0], fields[1], fields[2]
	if len(modeText) != 4 || strings.Trim(modeText, "01234567") != "" {
		return record{}, fmt.Errorf("malformed mode in file record %q", line)
	}
	mode, _ := strconv.ParseUint(modeText, 8, 32)
	if sizeText == "" || len(sizeText) > 19 || strings.Trim(sizeText, "0123456789") != "" {
		return record{}, fmt.Errorf("malformed size in file record %q", line)
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil {
		return record{}, fmt.Errorf("size in file record %q: %w", line, err)
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return record{}, fmt.Errorf("file record %q names something other than one file", line)
	}
	return record{mode: filexfer.Mode(mode), size: size, name: name}, nil
}

// formatRecord renders Put's file record. base has already passed
// filexfer.Resolve, which refuses a newline, so it cannot forge a
// second line.
func formatRecord(mode filexfer.Mode, size int64, base string) string {
	return fmt.Sprintf("C%s %d %s\n", mode, size, base)
}

// send runs the sink side of one file: wait until the device is ready,
// announce the file, stream exactly size bytes, and confirm each step.
// Not one byte of src is read until the device has accepted the
// record, so a refusal at any earlier step leaves the caller's source
// untouched.
func send(br *bufio.Reader, w io.Writer, src io.Reader, size int64, base string) error {
	if err := readAck(br); err != nil {
		return fmt.Errorf("waiting for the device to be ready: %w", err)
	}
	if _, err := io.WriteString(w, formatRecord(tempFileMode, size, base)); err != nil {
		return fmt.Errorf("sending the file record: %w", err)
	}
	if err := readAck(br); err != nil {
		return fmt.Errorf("the device refused the file record: %w", err)
	}
	if _, err := io.Copy(w, filexfer.ExactReader(src, size)); err != nil {
		return fmt.Errorf("sending the file: %w", err)
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return fmt.Errorf("ending the file: %w", err)
	}
	if err := readAck(br); err != nil {
		return fmt.Errorf("the device did not confirm the file: %w", err)
	}
	return nil
}

// receive runs the source side of one file: ask for it, accept exactly
// one C record naming exactly base, refuse a size over limit before any
// content moves, copy exactly that many bytes to dst, and require the
// device to confirm and then end. The name the device sends is checked
// and never used: bytes go only to dst, so a hostile server cannot
// choose where anything is written (the CVE-2019-6111 class).
func receive(br *bufio.Reader, w io.Writer, dst io.Writer, base string, limit int64) (int64, error) {
	if _, err := w.Write([]byte{0}); err != nil {
		return 0, fmt.Errorf("requesting the file: %w", err)
	}
	b, err := br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("reading the file record: %w", noEOF(err))
	}
	switch b {
	case 'C':
	case 1, 2:
		if err := br.UnreadByte(); err != nil {
			return 0, err
		}
		return 0, readAck(br)
	case 'D', 'E', 'T':
		return 0, fmt.Errorf("the device sent a %q record, and only one plain file is ever accepted", b)
	default:
		return 0, fmt.Errorf("%w: got %q", errUnexpectedByte, b)
	}
	line, err := readLine(br, maxRecordBytes)
	if err != nil {
		return 0, fmt.Errorf("reading the file record: %w", err)
	}
	rec, err := parseRecord(line)
	if err != nil {
		return 0, err
	}
	if rec.name != base {
		return 0, fmt.Errorf("the device sent %q when %q was requested", rec.name, base)
	}
	if rec.size > limit {
		return 0, fmt.Errorf("%w: the device announced %d bytes, over the limit of %d",
			filexfer.ErrLimitExceeded, rec.size, limit)
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return 0, fmt.Errorf("accepting the file record: %w", err)
	}
	n, err := io.CopyN(dst, br, rec.size)
	if err != nil {
		return n, fmt.Errorf("receiving the file: %w", noEOF(err))
	}
	if err := readAck(br); err != nil {
		return n, fmt.Errorf("the device did not end the file cleanly: %w", err)
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return n, fmt.Errorf("confirming the file: %w", err)
	}
	// Nothing more will be sent, so the input is closed before waiting
	// for the device to end. A server that reports a command's exit only
	// once its input has ended (os/exec behaves that way, and so do some
	// SSH servers) would otherwise wait for this client while this
	// client waits for it.
	if cw, ok := w.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err != nil {
			return n, fmt.Errorf("ending the request: %w", err)
		}
	}
	if extra, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		if err != nil {
			return n, fmt.Errorf("waiting for the device to finish: %w", err)
		}
		return n, fmt.Errorf("the device sent more after the one file requested (starting %q)", extra)
	}
	return n, nil
}
