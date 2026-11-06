// Package ssh: an async task's detached command against a real OpenSSH sshd,
// on accounts whose shell setup differs from the shared container's: one whose
// every command is preceded by startup output, and a BusyBox system with no
// setsid.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// startOwnSSHD starts an sshd container of the test's own, for a test that
// changes the system, and returns it with a connection to it.
func startOwnSSHD(t *testing.T) (*testsupport.SSHD, *remoteexec.Conn) {
	t.Helper()
	ctx := context.Background()
	srv, err := testsupport.StartSSHD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Terminate(context.Background()) })
	auth, err := remoteexec.AuthFrom(testsupport.SSHDUser, testsupport.SSHDPassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(ctx, nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn
}

// TestSSHContainer_ADetachedCommandSurvivesStartupOutput: sshd runs the
// account's ~/.ssh/rc before every command, and its output reaches the
// client ahead of the command's own (as a shell's startup files do). A
// detached command's polls read past it: the command finishes, its output
// is its own, and no job directory is left. Startup output that ends with no
// newline (a printf, or zsh's echo -n in .zshenv) runs into the first line of
// every answer, so each answer leads with one of its own.
func TestSSHContainer_ADetachedCommandSurvivesStartupOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	banner := "welcome to this host, " + strings.Repeat("=", 80)
	for name, rc := range map[string]string{
		"a banner line":            "echo " + banner + "\\n",
		"a banner with no newline": "printf " + banner,
	} {
		t.Run(name, func(t *testing.T) {
			srv, conn := startOwnSSHD(t)
			script := fmt.Sprintf("mkdir -p %[1]s/.ssh && printf '%%s\\n' '%[2]s' > %[1]s/.ssh/rc && chown -R %[3]s %[1]s/.ssh", testsupport.SSHDHome, rc, testsupport.SSHDUser)
			if out, err := srv.RootExec(context.Background(), script); err != nil {
				t.Fatalf("writing ~/.ssh/rc: %v\n%s", err, out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			res, err := conn.Run(remoteexec.WithDetach(ctx), "echo out; echo problem >&2")
			if err != nil {
				t.Fatalf("a detached command under startup output: %v", err)
			}
			if res.Stdout != "out\n" || res.Stderr != "problem\n" || res.ExitCode != 0 {
				t.Fatalf("a detached command under startup output = %+v; want its own output only", res)
			}
			left, err := srv.RootExec(context.Background(), "ls -d "+testsupport.SSHDHome+"/.pleiades-async-*/job.* 2>/dev/null | wc -l")
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(left) != "0" {
				t.Fatalf("%s job directories were left on the device", strings.TrimSpace(left))
			}
		})
	}
}

// TestSSHContainer_ADetachedCommandWithNoSetsidIsStillStopped: on BusyBox ash
// with no setsid, "set -m" cannot give the job a process group without a
// terminal, so a detached job could not be stopped as a whole. The command
// runs attached there instead, and the async limit still ends it: what it
// would do after its sleep never happens.
func TestSSHContainer_ADetachedCommandWithNoSetsidIsStillStopped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	srv, conn := startOwnSSHD(t)
	if out, err := srv.RootExec(context.Background(), `for p in $(which -a setsid 2>/dev/null); do rm -f "$p"; done; command -v setsid || echo gone`); err != nil || !strings.Contains(out, "gone") {
		t.Fatalf("removing setsid: %v\n%s", err, out)
	}
	marker := fmt.Sprintf("/tmp/after-limit-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := conn.Run(remoteexec.WithDetach(ctx), "sleep 5; touch "+marker); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v; want context.DeadlineExceeded", err)
	}
	time.Sleep(7 * time.Second)
	if out, _ := srv.RootExec(context.Background(), "[ -e "+marker+" ] && echo present || echo absent"); strings.TrimSpace(out) != "absent" {
		t.Fatalf("the command ran on past its limit: its marker is %s", strings.TrimSpace(out))
	}
}
