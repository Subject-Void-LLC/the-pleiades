// Tests for how a wait sorts a failed SSH login: a host key or algorithm this
// side refused, and a login the device refused, each end the wait saying
// which side refused, and a device that never answered is one more try.
package wait

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestSSHTryFailed_SaysWhichSideRefused proves each refusal stops the wait
// and is worded for the side that refused, and that silence and a
// cancellation do not stop it.
func TestSSHTryFailed_SaysWhichSideRefused(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	changed := &remoteexec.HostKeyError{Addr: "192.0.2.1:22", Presented: key, Known: []knownhosts.KnownKey{{Key: key, Filename: "kh", Line: 1}}}
	legacy := &remoteexec.LegacyRefusedError{Addr: "192.0.2.1:22", What: "key exchange", Offered: []string{"old"}, Property: remoteexec.LegacyKeyExchangeProperty}
	login := errors.New("ssh: unable to authenticate, attempted methods [none password]")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a changed host key", &remoteexec.DialError{Addr: "192.0.2.1:22", Err: changed}, "this side refused what it presented"},
		{"an old offer", &remoteexec.DialError{Addr: "192.0.2.1:22", Err: legacy}, "this side refused what it presented"},
		{"a refused login", &remoteexec.DialError{Addr: "192.0.2.1:22", Err: login}, "the device answered and refused this login"},
	} {
		var stop *stopWaiting
		if got := sshTryFailed(tc.err); !errors.As(got, &stop) || !strings.Contains(got.Error(), tc.want) {
			t.Errorf("%s: %v, want the wait stopped saying %q", tc.name, got, tc.want)
		}
	}
	for _, tc := range []error{
		&remoteexec.DialError{Addr: "192.0.2.1:22", Err: errors.New("tcp dial: connection refused")},
		&remoteexec.DialError{Addr: "192.0.2.1:22", Err: context.Canceled},
		errors.New("not a dial"),
	} {
		var stop *stopWaiting
		if got := sshTryFailed(tc); errors.As(got, &stop) {
			t.Errorf("%v stopped the wait", tc)
		}
	}
}
