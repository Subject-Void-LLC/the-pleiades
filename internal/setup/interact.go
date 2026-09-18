// The steps that need a person at a terminal: the outage question, the typed
// confirmation, and the possession check.
package setup

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// The terminal sequences the possession check uses.
const (
	// enterAlternateScreen switches to the terminal's alternate screen and
	// homes the cursor. What is drawn there is not part of the normal
	// screen's scrollback.
	enterAlternateScreen = "\x1b[?1049h\x1b[H"

	// leaveAndClear returns to the normal screen, then clears it and its
	// scrollback. The clearing is for a terminal that has no alternate
	// screen and so drew the key on the normal one.
	leaveAndClear = "\x1b[?1049l\x1b[H\x1b[2J\x1b[3J"
)

// possessionAttempts is how many times the key may be typed back.
const possessionAttempts = 3

// ErrInterrupted is returned when the operator stops setup at a terminal
// with a second Ctrl+C. Nothing has been written by then.
var ErrInterrupted = errors.New("setup was interrupted, and wrote nothing")

// Screen is an interactive terminal plus what it is currently showing, so
// that an interrupt can put both back.
type Screen struct {
	term *prompt.Terminal

	mu        sync.Mutex
	alternate bool
}

// NewScreen wraps an open terminal.
func NewScreen(term *prompt.Terminal) *Screen {
	return &Screen{term: term}
}

// GuardInterrupts restores the terminal if the process is interrupted while
// this command has it: echo back on, and off the alternate screen with the
// key cleared. Without it, a Ctrl-C during the hidden read leaves the
// operator's shell not echoing what they type, and a Ctrl-C while the key is
// displayed leaves the key on the screen. message says what was and was not
// written at the point the guard covers, since that differs before and
// after the file is written. exit is called with 130, the status a shell
// reports for an interrupt, after restoring. The returned function stops
// the guard.
func (s *Screen) GuardInterrupts(message string, exit func(int)) (stop func()) {
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
			s.mu.Lock()
			if s.alternate {
				fmt.Fprint(s.term.Out(), leaveAndClear)
				s.alternate = false
			}
			s.mu.Unlock()
			_ = s.term.Restore()
			fmt.Fprintln(s.term.Out(), "\n"+message)
			exit(130)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}

// show puts text on the alternate screen.
func (s *Screen) show(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprint(s.term.Out(), enterAlternateScreen+text)
	s.alternate = true
}

// clear leaves the alternate screen and clears what the key was shown on.
func (s *Screen) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alternate {
		fmt.Fprint(s.term.Out(), leaveAndClear)
		s.alternate = false
	}
}

// AskOutage asks the outage question until it gets an answer the controller
// accepts, three times at most. An empty answer takes the default.
func (s *Screen) AskOutage() (topology.OutageBudget, error) {
	var lastErr error
	for range 3 {
		answer, err := s.term.Line(OutageQuestion())
		if err != nil {
			return 0, err
		}
		budget, err := topology.ParseOutageBudget(answer)
		if err == nil {
			fmt.Fprintln(s.term.Out(), OutageExplanation(budget))
			return budget, nil
		}
		lastErr = err
		fmt.Fprintln(s.term.Out(), err)
	}
	return 0, refuse(fmt.Sprintf("No outage budget was accepted after three answers. The last refusal was: %v", lastErr))
}

// ConfirmReplace asks for the typed phrase that allows replacing a key. It
// allows one attempt: an empty line, a near miss and a wrong phrase all
// stop, because a second chance at a typed confirmation turns it into a
// thing to retry until it passes.
func (s *Screen) ConfirmReplace(file, database, fingerprint string, unknown int) error {
	answer, err := s.term.Line(confirmationPrompt(file, database, fingerprint, unknown))
	if err != nil {
		return err
	}
	if answer != confirmationPhrase(fingerprint) {
		return refuse("The phrase did not match, so the key was not replaced.")
	}
	return nil
}

// CheckPossession shows a newly generated key on the alternate screen,
// removes it from view, and has the operator type or paste it back.
//
// What this shows is narrow, and the messages say so: the operator held an
// exact copy a moment ago. It cannot show where the copy is, and a paste
// from a clipboard passes it. What it does rule out is the operator never
// having copied the key at all, which is the failure it exists for.
func (s *Screen) CheckPossession(key []byte, file string) error {
	encoded := crypto.EncodeKey(key)
	s.show(possessionScreen(encoded, crypto.Fingerprint(key), file))
	// Ctrl+C on this screen is most often an attempt to copy the key, which
	// in most terminals sends "stop" instead. So the first one explains and
	// keeps waiting, and only a second one ends the run.
	err := s.term.WaitForEnter(func() { fmt.Fprint(s.term.Out(), ctrlCNotice) })
	s.clear()
	if errors.Is(err, prompt.ErrInterrupted) {
		return ErrInterrupted
	}
	if err != nil {
		return err
	}

	for left := possessionAttempts - 1; left >= 0; left-- {
		entered, err := s.term.Secret(possessionPrompt)
		if err != nil {
			// An empty line or a failed read uses an attempt, the same as
			// a wrong key.
			if left > 0 {
				fmt.Fprintln(s.term.Out(), possessionMismatch(left))
			}
			continue
		}
		decoded, err := crypto.DecodeKey(entered, "the key you entered")
		if err == nil && subtle.ConstantTimeCompare(decoded, key) == 1 {
			fmt.Fprintln(s.term.Out(), possessionMatched)
			return nil
		}
		if left > 0 {
			fmt.Fprintln(s.term.Out(), possessionMismatch(left))
		}
	}
	return possessionFailed()
}

// AskAdminEmail asks for the first administrator's address. An empty answer
// skips creating one, and the caller says how to create it later.
func (s *Screen) AskAdminEmail() (string, error) {
	answer, err := s.term.Line("Email address for the first administrator (press Enter to skip): ")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}
