//go:build linux

// Tests for the interactive steps, on a real pseudo terminal.
package setup_test

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

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// These tests drive setup's interactive steps through a real pseudo
// terminal, because each property is a property of what a person at a
// terminal sees: whether the key is on the screen, whether what they type
// is echoed, and what a wrong answer does.

// terminalRig is a Screen on the child end of a pseudo terminal, and the
// parent end a test types into and reads the screen from.
type terminalRig struct {
	screen *setup.Screen
	parent *os.File
	shown  *lockedBuffer
}

// lockedBuffer is a buffer safe for one writer and one reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newRig opens a pseudo terminal and a Screen on it.
func newRig(t *testing.T) *terminalRig {
	t.Helper()
	parent, child, err := pty.Open()
	if err != nil {
		t.Skipf("no pseudo terminal available here: %v", err)
	}
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })
	shown := &lockedBuffer{}
	go func() { _, _ = io.Copy(shown, parent) }()
	term, err := prompt.NewTerminal(child, child)
	if err != nil {
		t.Fatalf("NewTerminal() error = %v", err)
	}
	return &terminalRig{screen: setup.NewScreen(term), parent: parent, shown: shown}
}

// waitShown waits until the terminal has shown want.
func (r *terminalRig) waitShown(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.shown.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it showed:\n%q", want, r.shown.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// typeHidden waits until echo is off, then types line, so a hidden answer is
// typed the way a person types it: after its prompt.
func (r *terminalRig) typeHidden(t *testing.T, line string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		termios, err := unix.IoctlGetTermios(int(r.parent.Fd()), unix.TCGETS)
		if err == nil && termios.Lflag&unix.ECHO == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("echo was never turned off for the hidden answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.typeLine(t, line)
}

// typeLine types line and a newline.
func (r *terminalRig) typeLine(t *testing.T, line string) {
	t.Helper()
	if _, err := r.parent.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("typing: %v", err)
	}
}

// TestCheckPossession_TheKeyLeavesTheScreenBeforeItIsTypedBack is the
// possession check's proof: the key appears only on the alternate screen,
// that screen is left and cleared before the operator types, and typing the
// exact key back passes.
func TestCheckPossession_TheKeyLeavesTheScreenBeforeItIsTypedBack(t *testing.T) {
	r := newRig(t)
	key := []byte(strings.Repeat("p", 32))
	encoded := crypto.EncodeKey(key)

	done := make(chan error, 1)
	go func() { done <- r.screen.CheckPossession(key, "/srv/.env") }()

	r.waitShown(t, "Press Enter when you have stored the key")
	r.typeLine(t, "")
	r.waitShown(t, "Type or paste the key you stored")
	r.typeHidden(t, encoded)
	if err := <-done; err != nil {
		t.Fatalf("CheckPossession() error = %v", err)
	}
	r.waitShown(t, "matches, byte for byte")

	shown := r.shown.String()
	enter := strings.Index(shown, "\x1b[?1049h")
	leave := strings.Index(shown, "\x1b[?1049l")
	at := strings.Index(shown, encoded)
	if enter < 0 || leave < 0 || at < enter || at > leave {
		t.Fatalf("the key was not confined to the alternate screen: enter at %d, key at %d, leave at %d", enter, at, leave)
	}
	if strings.Count(shown, encoded) != 1 {
		t.Fatalf("the key reached the screen %d times; typing it back must not echo it", strings.Count(shown, encoded))
	}
	if !strings.Contains(shown[leave:], "\x1b[3J") {
		t.Fatal("leaving the alternate screen did not clear the scrollback")
	}
}

// TestCheckPossession_ThreeWrongKeysRefuse proves a wrong key is not
// accepted and the run stops after three.
func TestCheckPossession_ThreeWrongKeysRefuse(t *testing.T) {
	r := newRig(t)
	key := []byte(strings.Repeat("q", 32))
	done := make(chan error, 1)
	go func() { done <- r.screen.CheckPossession(key, "/srv/.env") }()

	r.waitShown(t, "Press Enter")
	r.typeLine(t, "")
	for _, wrong := range []string{"not-the-key", crypto.EncodeKey([]byte(strings.Repeat("r", 32))), crypto.EncodeKey(key)[:40]} {
		r.typeHidden(t, wrong)
		time.Sleep(50 * time.Millisecond)
	}
	err := <-done
	var refusal *setup.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Message, "does not match") {
		t.Fatalf("CheckPossession() error = %v, want the refusal after three wrong keys", err)
	}
}

// TestConfirmReplace_OnlyTheExactPhrasePasses covers the typed confirmation:
// the exact phrase passes, and an empty line or a near miss stops, with no
// second attempt.
func TestConfirmReplace_OnlyTheExactPhrasePasses(t *testing.T) {
	fp := crypto.Fingerprint([]byte(strings.Repeat("c", 32)))
	phrase := "destroy " + fp[:4] + "-" + fp[4:8]
	cases := map[string]bool{
		phrase:                        true,
		"":                            false,
		"yes":                         false,
		"y":                           false,
		strings.ToUpper(phrase):       false,
		phrase + " ":                  false,
		"destroy " + fp[:4] + fp[4:8]: false,
	}
	for answer, pass := range cases {
		r := newRig(t)
		done := make(chan error, 1)
		go func() { done <- r.screen.ConfirmReplace("/srv/.env", "the database", fp, 0) }()
		r.waitShown(t, "Type exactly")
		r.typeLine(t, answer)
		err := <-done
		if pass && err != nil {
			t.Errorf("ConfirmReplace(%q) error = %v, want it accepted", answer, err)
		}
		if !pass && err == nil {
			t.Errorf("ConfirmReplace(%q) accepted it", answer)
		}
	}
}

// TestAskOutage_TakesAnAnswerOrTheDefault covers the outage question.
func TestAskOutage_TakesAnAnswerOrTheDefault(t *testing.T) {
	cases := map[string]topology.OutageBudget{
		"20m": topology.OutageBudget(20 * time.Minute),
		"":    topology.DefaultOutageBudget,
	}
	for answer, want := range cases {
		r := newRig(t)
		type result struct {
			b   topology.OutageBudget
			err error
		}
		done := make(chan result, 1)
		go func() {
			b, err := r.screen.AskOutage()
			done <- result{b, err}
		}()
		r.waitShown(t, "longest link outage")
		r.typeLine(t, answer)
		got := <-done
		if got.err != nil || got.b != want {
			t.Errorf("AskOutage(%q) = %v, %v; want %v", answer, got.b, got.err, want)
		}
	}

	r := newRig(t)
	done := make(chan error, 1)
	go func() { _, err := r.screen.AskOutage(); done <- err }()
	for range 3 {
		r.waitShown(t, "longest link outage")
		r.typeLine(t, "forever")
		time.Sleep(50 * time.Millisecond)
	}
	if err := <-done; err == nil {
		t.Fatal("AskOutage accepted three invalid answers")
	}
}

// typeKeys waits for echo to be off, which on the key screen means raw
// mode, then sends keys exactly as given, with no line ending added.
func (r *terminalRig) typeKeys(t *testing.T, keys string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		termios, err := unix.IoctlGetTermios(int(r.parent.Fd()), unix.TCGETS)
		if err == nil && termios.Lflag&unix.ECHO == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the terminal never left echo mode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.parent.Write([]byte(keys)); err != nil {
		t.Fatalf("typing: %v", err)
	}
}

// TestCheckPossession_ACtrlCOnTheKeyScreenAsksBeforeItStops is the case a
// person copying the key with Ctrl+C presents: the first one explains how to
// copy and leaves the key on the screen, Enter carries on, and only a second
// Ctrl+C ends the run.
func TestCheckPossession_ACtrlCOnTheKeyScreenAsksBeforeItStops(t *testing.T) {
	key := []byte(strings.Repeat("c", 32))

	r := newRig(t)
	done := make(chan error, 1)
	go func() { done <- r.screen.CheckPossession(key, "/srv/.env") }()
	r.waitShown(t, "Press Enter when you have stored the key")
	r.typeKeys(t, "\x03")
	r.waitShown(t, "Nothing has stopped")
	r.typeKeys(t, "\r")
	r.waitShown(t, "Type or paste the key you stored")
	r.typeHidden(t, crypto.EncodeKey(key))
	if err := <-done; err != nil {
		t.Fatalf("CheckPossession() after one Ctrl+C = %v, want the check to carry on and pass", err)
	}

	r = newRig(t)
	go func() { done <- r.screen.CheckPossession(key, "/srv/.env") }()
	r.waitShown(t, "Press Enter when you have stored the key")
	r.typeKeys(t, "\x03")
	r.waitShown(t, "Nothing has stopped")
	r.typeKeys(t, "\x03")
	select {
	case err := <-done:
		if !errors.Is(err, setup.ErrInterrupted) {
			t.Fatalf("CheckPossession() after two Ctrl+C = %v, want ErrInterrupted", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second Ctrl+C did not end the check")
	}
	r.waitShown(t, "\x1b[?1049l")
}
