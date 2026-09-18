//go:build linux

// Tests for the terminal reader, on a real pseudo terminal.
package prompt_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
)

// These tests drive a real pseudo terminal, because every property under
// test is a property of a terminal: echo on or off, and what a second read
// sees after a first one. A pipe standing in for a terminal would be
// refused by the code under test, which is itself one of the assertions.

// openPTY returns a Terminal on the child end of a new pseudo terminal, the
// parent end a test types into, and a buffer holding what the terminal
// echoed and printed.
func openPTY(t *testing.T) (*prompt.Terminal, *os.File, *syncBuffer) {
	t.Helper()
	parent, child, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo terminal available here: %v", err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })

	// Everything the terminal writes back (echo, prompts) is read off the
	// parent end, so the test can assert what the person would have seen.
	echoed := &syncBuffer{}
	go func() { _, _ = io.Copy(echoed, parent) }()

	terminal, err := prompt.NewTerminal(child, child)
	if err != nil {
		t.Fatalf("NewTerminal() error = %v", err)
	}
	return terminal, parent, echoed
}

// syncBuffer is a bytes.Buffer safe to write from one goroutine and read
// from another.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestTerminal_ALineThenASecretLoseNothingBetweenThem types both answers in
// one burst, the way a paste arrives, and requires each read to get exactly
// its own line. A reader that buffered ahead would hand the first call both
// lines and leave the second waiting forever.
func TestTerminal_ALineThenASecretLoseNothingBetweenThem(t *testing.T) {
	terminal, parent, echoed := openPTY(t)

	if _, err := parent.Write([]byte("destroy 3f9a-c21b\nthe-whole-key\n")); err != nil {
		t.Fatalf("typing: %v", err)
	}

	line, err := terminal.Line("Type the phrase: ")
	if err != nil || line != "destroy 3f9a-c21b" {
		t.Fatalf("Line() = %q, %v; want the first line exactly", line, err)
	}
	secret, err := terminal.Secret("Key: ")
	if err != nil || secret != "the-whole-key" {
		t.Fatalf("Secret() = %q, %v; want the second line exactly", secret, err)
	}

	// Everything in this burst was echoed, the secret included, because a
	// terminal echoes input when it arrives and the whole burst arrived
	// before echo was turned off. That is how a real terminal behaves with
	// type-ahead, and it is why TestTerminal_ASecretTypedAtItsPromptIsNotEchoed
	// types the secret only after its prompt appears.
	waitFor(t, echoed, "Type the phrase: ")
}

// TestTerminal_ASecretTypedAtItsPromptIsNotEchoed types the secret after the
// hidden prompt has appeared, which is when echo is off, and requires it to
// be absent from everything the terminal showed.
func TestTerminal_ASecretTypedAtItsPromptIsNotEchoed(t *testing.T) {
	terminal, parent, echoed := openPTY(t)

	got := make(chan string, 1)
	go func() {
		secret, err := terminal.Secret("Key: ")
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- secret
	}()

	waitFor(t, echoed, "Key: ")
	// term.ReadPassword turns echo off before it reads, and the prompt is
	// printed before it is called, so wait until the terminal reports echo
	// off rather than racing the switch.
	waitForEchoOff(t, parent)
	if _, err := parent.Write([]byte("the-whole-key\n")); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if secret := <-got; secret != "the-whole-key" {
		t.Fatalf("Secret() = %q, want the typed key", secret)
	}

	time.Sleep(100 * time.Millisecond)
	if strings.Contains(echoed.String(), "the-whole-key") {
		t.Fatalf("the secret was echoed to the terminal:\n%q", echoed.String())
	}
}

// waitFor polls until the terminal has shown want, or fails after five
// seconds.
func waitFor(t *testing.T, echoed *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(echoed.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it showed:\n%q", want, echoed.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForEchoOff polls the terminal's settings until echo is off, or fails
// after five seconds. The parent end reports the same line settings as the
// child on Linux.
func waitForEchoOff(t *testing.T, parent *os.File) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if echoIsOff(parent) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off for the hidden read")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTerminal_AnEmptyLineIsReturnedForTheCallerToRefuse pins that Line
// does not substitute a default for an empty answer.
func TestTerminal_AnEmptyLineIsReturnedForTheCallerToRefuse(t *testing.T) {
	terminal, parent, _ := openPTY(t)
	if _, err := parent.Write([]byte("\r\n")); err != nil {
		t.Fatalf("typing: %v", err)
	}
	line, err := terminal.Line("? ")
	if err != nil || line != "" {
		t.Fatalf("Line() = %q, %v; want an empty string and no error", line, err)
	}
}

// TestTerminal_RestorePutsEchoBack proves Restore undoes a terminal left with
// echo off, which is the state an interrupt during a hidden read leaves.
func TestTerminal_RestorePutsEchoBack(t *testing.T) {
	parent, child, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo terminal available here: %v", err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })

	terminal, err := prompt.NewTerminal(child, io.Discard)
	if err != nil {
		t.Fatalf("NewTerminal() error = %v", err)
	}
	if echoIsOff(child) {
		t.Fatal("control: a fresh pseudo terminal already has echo off")
	}
	if _, err := term.MakeRaw(int(child.Fd())); err != nil {
		t.Fatalf("MakeRaw() error = %v", err)
	}
	if !echoIsOff(child) {
		t.Fatal("control: MakeRaw did not turn echo off, so this test would prove nothing")
	}
	if err := terminal.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if echoIsOff(child) {
		t.Fatal("echo is still off after Restore")
	}
}

// TestNewTerminal_RefusesAPipe proves a pipe is not accepted as a terminal.
func TestNewTerminal_RefusesAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	if _, err := prompt.NewTerminal(r, io.Discard); !errors.Is(err, prompt.ErrNotATerminal) {
		t.Fatalf("NewTerminal(pipe) error = %v, want ErrNotATerminal", err)
	}
	if prompt.IsTerminal(r) {
		t.Fatal("IsTerminal(pipe) = true")
	}
}

// echoIsOff reports whether the terminal f belongs to has echo turned off.
func echoIsOff(f *os.File) bool {
	termios, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return false
	}
	return termios.Lflag&unix.ECHO == 0
}

// TestTerminal_WaitForEnterTreatsOneCtrlCAsAQuestion proves the first Ctrl+C
// does not end the wait, that Enter afterwards does, and that a second
// Ctrl+C returns ErrInterrupted. In both cases the terminal's settings are
// back as they were: echo on, and Ctrl+C an interrupt again.
func TestTerminal_WaitForEnterTreatsOneCtrlCAsAQuestion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keys  []byte
		want  error
		calls int
	}{
		{"ctrl-c then enter", []byte{0x03, '\r'}, nil, 1},
		{"ctrl-c twice", []byte{0x03, 'x', 0x03}, prompt.ErrInterrupted, 1},
		{"enter alone", []byte{'\n'}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminal, parent, _ := openPTY(t)
			calls := 0
			done := make(chan error, 1)
			go func() { done <- terminal.WaitForEnter(func() { calls++ }) }()

			// Wait for raw mode before typing, as a person's keys arrive
			// after the screen is up.
			deadline := time.Now().Add(5 * time.Second)
			for !echoIsOff(parent) {
				if time.Now().After(deadline) {
					t.Fatal("the terminal never entered raw mode")
				}
				time.Sleep(10 * time.Millisecond)
			}
			for _, k := range tc.keys {
				if _, err := parent.Write([]byte{k}); err != nil {
					t.Fatalf("typing: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := <-done; !errors.Is(err, tc.want) {
				t.Fatalf("WaitForEnter() = %v, want %v", err, tc.want)
			}
			if calls != tc.calls {
				t.Fatalf("the first-interrupt callback ran %d times, want %d", calls, tc.calls)
			}
			if echoIsOff(parent) {
				t.Fatal("WaitForEnter returned and left the terminal in raw mode")
			}
		})
	}
}
