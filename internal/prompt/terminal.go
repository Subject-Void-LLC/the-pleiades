// Reading an echoed line and a hidden one from one terminal, one byte at a time,
// so neither read takes input meant for the other.
package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNotATerminal is returned by OpenTerminal when standard input is not a
// terminal. A command that needs a person at a keyboard, to type a
// confirmation or re-enter a key, refuses rather than reading a pipe,
// because piped input is exactly what a person at a keyboard is being asked
// to prove they are not.
var ErrNotATerminal = errors.New("standard input is not a terminal")

// maxLineBytes caps one echoed line. A confirmation or an email address is
// far shorter; the cap exists so a stuck key or an endless paste ends.
const maxLineBytes = 4096

// Terminal is an interactive terminal: prompts go to out, and input is read
// from in one byte at a time.
//
// One byte at a time is the property this type exists for. term.ReadPassword
// reads its line from the file descriptor the same way, and a command that
// asks for an echoed line and then a hidden one, or the reverse, must not
// have the first read swallow bytes the second needs. A bufio.Reader over
// stdin would read ahead and do exactly that as soon as input arrived faster
// than it was consumed, which a paste does. Neither reader here buffers, so
// they can alternate on one descriptor with nothing lost between them.
type Terminal struct {
	in    *os.File
	out   io.Writer
	state *term.State
}

// OpenTerminal returns the process's own terminal: standard input for
// reading and standard error for prompts. It refuses when standard input is
// not a terminal.
func OpenTerminal() (*Terminal, error) {
	return NewTerminal(os.Stdin, os.Stderr)
}

// NewTerminal returns a Terminal reading in and prompting on out. in must be
// a terminal; a test passes the child end of a pseudo terminal.
//
// The terminal's settings are captured here, so Restore can put them back if
// the process is interrupted while echo is off.
func NewTerminal(in *os.File, out io.Writer) (*Terminal, error) {
	fd := int(in.Fd()) // #nosec G115 -- a file descriptor always fits in an int
	if !term.IsTerminal(fd) {
		return nil, ErrNotATerminal
	}
	state, err := term.GetState(fd)
	if err != nil {
		return nil, fmt.Errorf("failed to read the terminal's settings: %w", err)
	}
	return &Terminal{in: in, out: out, state: state}, nil
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) // #nosec G115 -- a file descriptor always fits in an int
}

// Out is where prompts and messages meant for the person at the terminal go.
func (t *Terminal) Out() io.Writer { return t.out }

// Line prints promptText and reads one line with echo on.
//
// It returns the line without its line ending, and an empty string for an
// empty line: whether that is an answer or a refusal is the caller's
// decision, and every caller in this repository that asks for a
// confirmation treats it as a refusal.
func (t *Terminal) Line(promptText string) (string, error) {
	fmt.Fprint(t.out, promptText)

	var line []byte
	var b [1]byte
	for {
		n, err := t.in.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			if len(line) >= maxLineBytes {
				return "", fmt.Errorf("the line is longer than %d bytes", maxLineBytes)
			}
			line = append(line, b[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				break
			}
			return "", fmt.Errorf("failed to read a line from the terminal: %w", err)
		}
	}
	return strings.TrimSuffix(string(line), "\r"), nil
}

// Secret prints promptText and reads one line with echo off. It refuses an
// empty secret, as SecretFrom does.
func (t *Terminal) Secret(promptText string) (string, error) {
	return SecretFrom(t.out, int(t.in.Fd()), promptText) // #nosec G115 -- a file descriptor always fits in an int
}

// Restore puts the terminal's settings back to what they were when it was
// opened. A command calls it on an interrupt, since a process killed while
// term.ReadPassword has echo off would otherwise leave the operator's shell
// not showing what they type.
func (t *Terminal) Restore() error {
	return term.Restore(int(t.in.Fd()), t.state) // #nosec G115 -- a file descriptor always fits in an int
}

// ErrInterrupted is returned by WaitForEnter when Ctrl+C is pressed a second
// time.
var ErrInterrupted = errors.New("interrupted at the terminal")

// ctrlC is the byte a terminal sends for Ctrl+C when it is not turning that
// key into a signal.
const ctrlC = 0x03

// WaitForEnter waits for Enter, reading keys with the terminal in raw mode so
// that Ctrl+C arrives as a key rather than as an interrupt.
//
// It exists for a screen that asks the operator to copy something off it. In
// most terminals Ctrl+C is not copy, it is stop, so the gesture a person
// reaches for to copy a key would otherwise end the program and discard the
// key they were shown. Here the first Ctrl+C calls firstInterrupt, which says
// so, and waiting continues; a second one returns ErrInterrupted. Every other
// key is ignored. The terminal's settings are restored before it returns.
//
// firstInterrupt writes while the terminal is in raw mode, where a line feed
// does not return the cursor to the start of the line, so it should end lines
// with "\r\n".
func (t *Terminal) WaitForEnter(firstInterrupt func()) error {
	fd := int(t.in.Fd()) // #nosec G115 -- a file descriptor always fits in an int
	state, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to read keys from the terminal: %w", err)
	}
	defer func() { _ = term.Restore(fd, state) }()

	interrupts := 0
	var b [1]byte
	for {
		n, err := t.in.Read(b[:])
		if err != nil {
			return fmt.Errorf("failed to read keys from the terminal: %w", err)
		}
		if n != 1 {
			continue
		}
		switch b[0] {
		case '\r', '\n':
			return nil
		case ctrlC:
			interrupts++
			if interrupts > 1 {
				return ErrInterrupted
			}
			firstInterrupt()
		}
	}
}
