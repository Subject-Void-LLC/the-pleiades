// A refused password against a real OpenSSH server, counted by the
// server's own log: one task is one failed login.
package ssh

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	tcexec "github.com/testcontainers/testcontainers-go/exec"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// sshdLog is where the shared container's sshd writes its log, through
// the image's s6-log service (/etc/s6-overlay/s6-rc.d/log-openssh-server).
const sshdLog = "/config/logs/openssh/current"

// TestSSHContainer_ARejectedPasswordIsPresentedOnce proves, against a
// real OpenSSH server and by the server's own account, that one task with
// a wrong password is one failed login.
//
// The count is read from sshd's log, which is what a lockout policy
// counts too: pam_faillock at deny=3 locks an account on the third
// "Failed password", and the dial retry used to produce three from a
// single task (measured here: 3 before the fix, 1 after).
//
// OpenSSH itself now counts them as well. Since 9.8 sshd's
// PerSourcePenalties charges a source address five seconds per failed
// login and refuses every connection from it once fifteen accrue, which
// the old retry reached in one task, so a single wrong password blocked
// the Runner's address from that host for every job it ran. That is also
// why the matching circuit-breaker proof, that a wrong password never
// opens the circuit for a right one, runs against a real x/crypto server
// in pkg/remoteexec (TestConnect_ARejectedPasswordDoesNotOpenTheCircuit)
// rather than here: against this sshd, enough wrong passwords to test the
// breaker earn a penalty that refuses the right one too, which is sshd's
// policy and not ours. This package's tests stay well under that budget
// (this test and TestSSHContainer_WrongPasswordFails spend one failure
// each) so they cannot block each other.
func TestSSHContainer_ARejectedPasswordIsPresentedOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	target := containerTarget(t)
	before := failedPasswords(t)

	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 5 * time.Second})
	wrong := credential.Credential{Username: containerSSHUser, Password: "not-" + containerSSHPassword}
	_, err := tr.Exec(context.Background(), target, wrong, "true")
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("error = %v, want sshd's refusal", err)
	}

	if got := settledFailedPasswords(t, before+1) - before; got != 1 {
		t.Fatalf("sshd logged %d failed passwords for one task, want 1", got)
	}
}

// failedPasswords returns how many "Failed password" lines the shared
// container's sshd has logged so far.
func failedPasswords(t *testing.T) int {
	t.Helper()
	code, reader, err := sharedContainer.Exec(context.Background(),
		[]string{"sh", "-c", "grep -c 'Failed password' " + sshdLog + " || true"}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("reading the sshd log: %v", err)
	}
	out, err := io.ReadAll(reader)
	if err != nil || code != 0 {
		t.Fatalf("reading the sshd log: exit %d, %v: %s", code, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("the sshd log count %q is not a number: %v", out, err)
	}
	return n
}

// settledFailedPasswords waits for the count to reach at least want and
// then to stop moving, because s6-log writes the log a moment after sshd
// emits each line. Every attempt a task makes has happened by the time
// Exec returns, so a count that holds still for half a second is final.
func settledFailedPasswords(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last, stableSince := -1, time.Now()
	for time.Now().Before(deadline) {
		n := failedPasswords(t)
		if n != last {
			last, stableSince = n, time.Now()
		}
		if n >= want && time.Since(stableSince) >= 500*time.Millisecond {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}
