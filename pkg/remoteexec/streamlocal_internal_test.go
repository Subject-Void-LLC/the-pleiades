// In-process tests of the Unix-socket channel: how sshd's refusals are
// described, and when a channel taints the login it rode on.
package remoteexec

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestDescribeChannelRefusal: each answer sshd can give a channel open is
// named in an operator's terms, the original kept beneath; anything that is
// not a channel refusal passes through untouched.
func TestDescribeChannelRefusal(t *testing.T) {
	plain := errors.New("connection reset")
	if got := describeChannelRefusal(plain); got != plain {
		t.Errorf("a non-refusal became %v", got)
	}
	for reason, want := range map[ssh.RejectionReason]string{
		ssh.Prohibited:       "refuses forwarding to a Unix socket",
		ssh.ConnectionFailed: "AllowStreamLocalForwarding no",
	} {
		refusal := &ssh.OpenChannelError{Reason: reason, Message: "refused"}
		got := describeChannelRefusal(refusal)
		var open *ssh.OpenChannelError
		if !strings.Contains(got.Error(), want) || !errors.As(got, &open) {
			t.Errorf("reason %v: %v, want it to say %q and keep the refusal", reason, got, want)
		}
	}
	other := &ssh.OpenChannelError{Reason: ssh.ResourceShortage, Message: "busy"}
	if got := describeChannelRefusal(other); got != error(other) {
		t.Errorf("an answer with no better description became %v", got)
	}
}

// TestUnixChannel_TaintsOnlyWhenBroken: a channel whose reads ended at the
// far side's clean end of stream leaves its login lendable; one whose read
// or write failed taints it at Close.
func TestUnixChannel_TaintsOnlyWhenBroken(t *testing.T) {
	channel := func() (*unixChannel, *lease, net.Conn) {
		near, far := net.Pipe()
		l := &lease{}
		return &unixChannel{Conn: near, owner: &Conn{lease: l}}, l, far
	}

	clean, cleanLease, far := channel()
	go func() { _, _ = far.Write([]byte("ok")); _ = far.Close() }()
	if _, err := io.ReadAll(clean); err != nil {
		t.Fatal(err)
	}
	_ = clean.Close()
	if cleanLease.tainted {
		t.Error("a channel that ended cleanly tainted its login")
	}

	broken, brokenLease, far := channel()
	_ = far.Close()
	if _, err := broken.Write([]byte("x")); err == nil {
		t.Fatal("a write to a closed pipe succeeded")
	}
	_ = broken.Close()
	if !brokenLease.tainted {
		t.Error("a channel whose write failed left its login lendable")
	}
}
