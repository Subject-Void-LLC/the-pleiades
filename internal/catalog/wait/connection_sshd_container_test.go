// Tests for wait.connection over SSH against a real OpenSSH server in a
// container, the one place a refused login is counted the way a device
// counts it: sshd's own log of failed and accepted authentications. They
// prove a wrong password is sent once in a whole wait, a refused host key
// ends a wait at once, and a right password still waits through a host
// that comes up late.
package wait_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// sshdLog is where the sshd image's s6 logger writes sshd's own log.
const sshdLog = "/config/logs/openssh/current"

// startRealSSHD starts the repository's pinned OpenSSH container for one
// test and removes it when the test ends.
func startRealSSHD(t *testing.T) *testsupport.SSHD {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the container-backed sshd test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	s, err := testsupport.StartSSHD(ctx)
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the sshd container (is Docker running?): %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Terminate(context.Background()) })
	return s
}

// authCounts reads sshd's log and counts the authentications it judged
// for the test account: failed (a wrong password, by any method) and
// accepted.
func authCounts(t *testing.T, s *testsupport.SSHD) (failed, accepted int) {
	t.Helper()
	out, err := s.RootExec(context.Background(), "cat "+sshdLog)
	if err != nil {
		t.Fatalf("reading sshd's log: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, " for "+testsupport.SSHDUser+" ") {
			continue
		}
		switch {
		case strings.Contains(line, "Failed "):
			failed++
		case strings.Contains(line, "Accepted "):
			accepted++
		}
	}
	return failed, accepted
}

// sshdServer is the container as the wait tests' own server type, with the
// password given.
func sshdServer(s *testsupport.SSHD, password string) waitServer {
	return waitServer{host: s.Host, port: s.Port, username: testsupport.SSHDUser, password: password}
}

// TestConnection_AWrongSSHPasswordIsSentOnce: a wait with a wrong password
// stops at its first try, saying the device refused the login, and sshd
// counts exactly one failed authentication for it however long its
// timeout. A right password then logs in, and sshd's log shows that too,
// which is what proves the counts above were read from the right place.
// A wait that tried a refused password again every second would have sent
// it a dozen times in the timeout this one is given.
func TestConnection_AWrongSSHPasswordIsSentOnce(t *testing.T) {
	s := startRealSSHD(t)
	params := map[string]any{"timeout": 30, "sleep": 1, "connect_timeout": 5, sdk.ParamInsecureSkipHostKeyVerify: true}
	failedBefore, acceptedBefore := authCounts(t, s)

	wrong := sshdServer(s, testsupport.SSHDPassword+"-wrong")
	start := time.Now()
	_, err := wait.Connection(context.Background(), newWaitContext(wrong), newWaitTarget(wrong), params)
	if err == nil || !strings.Contains(err.Error(), "refused this login") || !strings.Contains(err.Error(), "stopped at try 1") {
		t.Fatalf("a wrong password: %v, want the refusal at the first try", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the refusal took %s to end the wait; it ends it at once, not at the timeout", took)
	}
	failed, _ := authCounts(t, s)
	if failed-failedBefore != 1 {
		t.Fatalf("sshd counted %d failed authentications for the wait, want exactly 1", failed-failedBefore)
	}

	right := sshdServer(s, testsupport.SSHDPassword)
	if _, err := wait.Connection(context.Background(), newWaitContext(right), newWaitTarget(right), params); err != nil {
		t.Fatalf("the right password: %v", err)
	}
	failedAfter, accepted := authCounts(t, s)
	if accepted-acceptedBefore != 1 || failedAfter != failed {
		t.Fatalf("after the right password sshd counts %d more accepted and %d more failed, want 1 and 0",
			accepted-acceptedBefore, failedAfter-failed)
	}
}

// TestConnection_ARightSSHPasswordWaitsForAHostComingUpLate: a refused
// connection is not a refused login, so a wait with the right password is
// tried again until the host is up, and logs in once it is.
func TestConnection_ARightSSHPasswordWaitsForAHostComingUpLate(t *testing.T) {
	s := startRealSSHD(t)
	right := sshdServer(s, testsupport.SSHDPassword)
	late := &lateTarget{waitTarget: newWaitTarget(right), dead: deadPort(t), after: 3}
	rc := newWaitContext(right)
	params := map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5, sdk.ParamInsecureSkipHostKeyVerify: true}
	if _, err := wait.Connection(context.Background(), rc, late, params); err != nil {
		t.Fatalf("a host coming up late: %v", err)
	}
	if rc.stats["tries"] != 4 || rc.stats["failures"] != 3 {
		t.Errorf("stats %v, want three refused connections and then the login", rc.stats)
	}
	if failed, accepted := authCounts(t, s); failed != 0 || accepted != 1 {
		t.Errorf("sshd counts %d failed and %d accepted, want 0 and 1", failed, accepted)
	}
}

// TestConnection_ARefusedSSHHostKeyEndsTheWait: a host key known_hosts
// refuses is this side's answer, not the network's, and will not change by
// being asked again, so the wait stops at its first try, before any
// credential is sent: sshd counts no authentication at all.
func TestConnection_ARefusedSSHHostKeyEndsTheWait(t *testing.T) {
	s := startRealSSHD(t)
	home := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	// Another key for the host under every name it is dialed by, so the
	// host is known and its real key is a mismatch.
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	var lines strings.Builder
	for _, host := range []string{s.Host, "127.0.0.1", "localhost"} {
		lines.WriteString("[" + host + "]:" + strconv.Itoa(s.Port) + " " + key + "\n")
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// USERPROFILE beside HOME: os.UserHomeDir reads it on Windows.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	right := sshdServer(s, testsupport.SSHDPassword)
	start := time.Now()
	_, err = wait.Connection(context.Background(), newWaitContext(right), newWaitTarget(right), map[string]any{"timeout": 30, "sleep": 1, "connect_timeout": 5})
	if err == nil || !strings.Contains(err.Error(), "stopped at try 1") {
		t.Fatalf("a refused host key: %v, want the wait stopped at its first try", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the refused host key took %s to end the wait", took)
	}
	if failed, accepted := authCounts(t, s); failed != 0 || accepted != 0 {
		t.Errorf("sshd counts %d failed and %d accepted authentications, want none: no credential goes to a refused host", failed, accepted)
	}
}
