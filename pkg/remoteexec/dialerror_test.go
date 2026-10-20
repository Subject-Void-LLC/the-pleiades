// Tests for DialError: its message, its chain, and which dial failures are
// the device never answering.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// TestDialError_Unreached holds each dial failure to the rule.
func TestDialError_Unreached(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"a TCP dial that timed out":           {errors.New("retry: exhausted 3 attempt(s): tcp dial: dial tcp 10.0.0.1:22: i/o timeout"), true},
		"a handshake the device ended at EOF": {fmt.Errorf("ssh handshake: %w", io.EOF), true},
		"a refused credential":                {errors.New("ssh handshake: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password]"), false},
		"a refused host key":                  {fmt.Errorf("ssh handshake: %w", &knownhosts.KeyError{}), false},
		"the caller canceling":                {context.Canceled, false},
	} {
		if got := (&DialError{Addr: "10.0.0.1:22", Err: tc.err}).Unreached(); got != tc.want {
			t.Errorf("%s: Unreached() = %v, want %v", name, got, tc.want)
		}
	}
}

// TestDialError_KeepsItsWordsAndChain: the message is the one this package
// always wrote for a failed dial, and the cause stays reachable.
func TestDialError_KeepsItsWordsAndChain(t *testing.T) {
	err := &DialError{Addr: "10.0.0.1:22", Err: fmt.Errorf("ssh handshake: %w", io.EOF)}
	if !strings.HasPrefix(err.Error(), "remoteexec: dial 10.0.0.1:22: ssh handshake") || !errors.Is(err, io.EOF) {
		t.Errorf("err = %v; errors.Is(io.EOF) = %v", err, errors.Is(err, io.EOF))
	}
}
