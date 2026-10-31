// Package remoteexec: tests of running a connection's commands as another
// account through sudo, against a fake SSH server that records each command.
package remoteexec

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// becomeRecorder is a fake SSH server's handler that records each command and
// answers sudo as a device would: a sudo that needs a password when refuse is
// set, and otherwise the wrapped command's own answer.
type becomeRecorder struct {
	mu       sync.Mutex
	commands []string
	refuse   bool
}

func (b *becomeRecorder) handle(cmd string) (string, string, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = append(b.commands, cmd)
	if strings.HasPrefix(cmd, "'sudo'") && b.refuse {
		return "", "sudo: a password is required\n", 1
	}
	return "ran:" + cmd, "", 0
}

// TestWithBecome_WrapsEveryCommandAndNothingElse: a view runs commands through
// sudo -n as the account asked, the original connection still runs as the
// login, and the account is the one named.
func TestWithBecome_WrapsEveryCommandAndNothingElse(t *testing.T) {
	rec := &becomeRecorder{}
	fake, _ := newFakeSSHServer(t, rec.handle)
	conn, err := newTestRunner(fake, Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	view, err := conn.WithBecome(context.Background(), Become{User: "netadmin"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Becomes() != "netadmin" || conn.Becomes() != "" {
		t.Errorf("view becomes %q, original %q", view.Becomes(), conn.Becomes())
	}
	// The proof of sudo is the first command, run before the view existed.
	if want := `'sudo' '-n' '-u' 'netadmin' '--' 'sh' '-c' 'true'`; rec.commands[0] != want {
		t.Errorf("the sudo check sent %q, want %q", rec.commands[0], want)
	}

	// A command with quotes, a pipe and a redirect stays one argument to sh -c.
	script := `echo 'a b' | tr a-z A-Z > "$HOME/x"; echo $?`
	res, err := view.Run(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	want := QuoteCommand([]string{"sudo", "-n", "-u", "netadmin", "--", "sh", "-c", script})
	if res.Stdout != "ran:"+want {
		t.Errorf("the view sent %q, want %q", res.Stdout, "ran:"+want)
	}
	if res, err = conn.Run(context.Background(), script); err != nil || res.Stdout != "ran:"+script {
		t.Errorf("the original connection sent %q, err %v: it must run as the login", res.Stdout, err)
	}
}

// TestWithBecome_ASudoThatAsksForAPasswordIsOneClearRefusal: the refusal is
// made when the view is asked for, names the account and the fix, carries
// sudo's own words, and leaves the connection usable as the login.
func TestWithBecome_ASudoThatAsksForAPasswordIsOneClearRefusal(t *testing.T) {
	rec := &becomeRecorder{refuse: true}
	fake, _ := newFakeSSHServer(t, rec.handle)
	conn, err := newTestRunner(fake, Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	view, err := conn.WithBecome(context.Background(), Become{})
	if err == nil || view != nil {
		t.Fatalf("a sudo that asks for a password gave a view: %v", err)
	}
	for _, want := range []string{"root", "NOPASSWD", "a password is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	if _, err := conn.Run(context.Background(), "echo still-here"); err != nil {
		t.Errorf("the connection is no longer usable as the login: %v", err)
	}
}

// TestWithBecome_RefusesWhatCannotBeElevated: a bad account name, a second
// become, and each use of the connection that sends no command.
func TestWithBecome_RefusesWhatCannotBeElevated(t *testing.T) {
	rec := &becomeRecorder{}
	fake, _ := newFakeSSHServer(t, rec.handle)
	conn, err := newTestRunner(fake, Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	for _, user := range []string{"Root", "a b", "x;rm -rf /", "-u", "$(id)", strings.Repeat("a", 40)} {
		if _, err := conn.WithBecome(context.Background(), Become{User: user}); err == nil {
			t.Errorf("become user %q was accepted", user)
		}
	}
	if len(rec.commands) != 0 {
		t.Errorf("a refused account name reached the device: %v", rec.commands)
	}

	view, err := conn.WithBecome(context.Background(), Become{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.WithBecome(context.Background(), Become{User: "other"}); err == nil {
		t.Error("a view became a second account")
	}
	if _, err := view.Shell(context.Background(), ShellOptions{}); err == nil || !strings.Contains(err.Error(), "become does not apply") {
		t.Errorf("Shell on a view: %v", err)
	}
	if _, err := view.Subsystem(context.Background(), "netconf"); err == nil || !strings.Contains(err.Error(), "become does not apply") {
		t.Errorf("Subsystem on a view: %v", err)
	}
	if _, err := view.DialTCP(context.Background(), "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "become does not apply") {
		t.Errorf("DialTCP on a view: %v", err)
	}
	if _, err := view.DialUnix(context.Background(), "/var/run/x.sock"); err == nil || !strings.Contains(err.Error(), "become does not apply") {
		t.Errorf("DialUnix on a view: %v", err)
	}
}
