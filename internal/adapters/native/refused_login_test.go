// Tests that a Runner stops sending a login its device refused: the
// refusal crosses from the per-task child to the Runner's engine as one
// (wire.ChildResponse.Refused), so an until task is not tried again and a
// loop runs no later item. The proof is the server's own count of the
// passwords it refused, through the whole adapter, with persistence on and
// off, and against the repository's real OpenSSH server, counted from its
// own log.
package native

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// refusedRunbook is two tasks that log in over SSH: an until task allowed
// three more tries, whose failure is ignored so the second runs, and a loop
// of three items. Each sends its login once when the device refuses it, and
// sent four and three before.
const refusedRunbook = `id: refused-1
tasks:
  - name: wait for it
    nativesessiontest.connect:
      insecure_skip_host_key_verify: true
    register: r
    until: ["result.r.pid != ''"]
    retries: 3
    delay: 0
    ignore_errors: true
  - name: once per item
    nativesessiontest.connect:
      insecure_skip_host_key_verify: true
    loop: [a, b, c]
`

// TestChildAnswer_ARefusalStaysOne proves the parent's error for a response
// marked refused is one the engine reads as a refusal, with its text masked
// as any failure's is, and an unmarked one is not.
func TestChildAnswer_ARefusalStaysOne(t *testing.T) {
	secrets := []string{"hunter2"}
	_, _, err := childAnswer("m", collection.ModeExecute, wire.ChildResponse{Error: "login as hunter2 refused", Refused: true}, secrets)
	if err == nil || !engine.Refused(err) || engine.Unreached(err) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("a marked refusal became %v (Refused %v), want a masked refusal", err, engine.Refused(err))
	}
	_, _, err = childAnswer("m", collection.ModeExecute, wire.ChildResponse{Error: "command exited 1"}, secrets)
	if err == nil || engine.Refused(err) {
		t.Fatalf("an unmarked failure became %v (Refused %v), want an ordinary failure", err, engine.Refused(err))
	}
}

// TestAdapter_Execute_ARefusedLoginIsSentOncePerTask runs refusedRunbook
// through the whole adapter with a password the server refuses, with
// persistence on and off, and counts the passwords the server refused: one
// for each task.
func TestAdapter_Execute_ARefusedLoginIsSentOncePerTask(t *testing.T) {
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	adapter, err := NewAdapter(&mockBus{}, writeRunbook(t, "refused-1", refusedRunbook), nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := sessionDevice(srv).Payload()
	payload.RunbookID = "refused-1"
	payload.Secrets[wire.SecretPassword] = "wrong"
	for _, persist := range []bool{true, false} {
		before := srv.Refusals()
		payload.PersistConnections = persist
		if _, err := adapter.Execute(context.Background(), payload); err == nil {
			t.Fatalf("persist %v: a run whose logins were refused succeeded", persist)
		}
		if got := srv.Refusals() - before; got != 2 {
			t.Errorf("persist %v: the server refused %d passwords for two tasks, want 2: the until task or the loop sent the refused login again", persist, got)
		}
	}
}

// TestAdapter_Execute_SSHDCountsOneFailedLoginPerTask is the same run
// against the repository's pinned OpenSSH server, whose own log is what a
// lockout policy reads: it counts one failed login per task. Before, the
// tries and items reached its per-source penalty within one run.
func TestAdapter_Execute_SSHDCountsOneFailedLoginPerTask(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real sshd container")
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

	adapter, err := NewAdapter(&mockBus{}, writeRunbook(t, "refused-1", refusedRunbook), nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := wire.DispatchPayload{
		JobID: "job-1", DeviceID: "d1", DeviceName: "web1", RunbookID: "refused-1",
		DeviceHost: s.Host, SSHPort: s.Port,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      map[string]string{wire.SecretUsername: testsupport.SSHDUser, wire.SecretPassword: testsupport.SSHDPassword + "-wrong"},
	}
	before := sshdFailedLogins(t, s, -1)
	if _, err := adapter.Execute(ctx, payload); err == nil {
		t.Fatal("a run whose logins were refused succeeded")
	}
	if got := sshdFailedLogins(t, s, before+2) - before; got != 2 {
		t.Fatalf("sshd logged %d failed logins for two tasks, want 2", got)
	}
}

// sshdLogPath is where the pinned sshd image's s6 logger writes sshd's log.
const sshdLogPath = "/config/logs/openssh/current"

// sshdFailedLogins counts the failed authentications sshd's log records for
// the test account. With want at or above zero it waits for the count to
// reach want and then hold still for half a second, since s6-log writes a
// moment after sshd emits each line.
func sshdFailedLogins(t *testing.T, s *testsupport.SSHD, want int) int {
	t.Helper()
	count := func() int {
		out, err := s.RootExec(context.Background(), "cat "+sshdLogPath+" 2>/dev/null || true")
		if err != nil {
			t.Fatalf("reading sshd's log: %v", err)
		}
		n := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Failed ") && strings.Contains(line, " for "+testsupport.SSHDUser+" ") {
				n++
			}
		}
		return n
	}
	if want < 0 {
		return count()
	}
	deadline := time.Now().Add(10 * time.Second)
	last, stableSince := -1, time.Now()
	for time.Now().Before(deadline) {
		n := count()
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
