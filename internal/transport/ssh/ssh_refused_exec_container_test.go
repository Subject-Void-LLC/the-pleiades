// Package ssh: a command OpenSSH refuses to start is reported without the
// command, which can carry a secret, in its error.
package ssh

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestSSHContainer_ARefusedCommandIsNotInItsError: a second sshd in the
// container runs with the account's process limit at one, so the account's
// session process cannot fork the command and sshd refuses the exec. The
// error names the refusal; neither the value nor its shell-encoded form (which
// a mask by value does not match) is in it, for Run, a raw Run, and Start.
func TestSSHContainer_ARefusedCommandIsNotInItsError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	ctx := context.Background()
	srv, err := testsupport.StartSSHD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Terminate(context.Background()) })
	// The image's own sshd binary and config, on a port reached through the
	// container's sshd as a hop.
	if out, err := srv.RootExec(ctx, `sh -c 'ulimit -u 1; exec /usr/sbin/sshd.pam -p 2223 -f /config/sshd/sshd_config'`); err != nil {
		t.Fatalf("starting the limited sshd: %v\n%s", err, out)
	}
	auth, err := remoteexec.AuthFrom(testsupport.SSHDUser, testsupport.SSHDPassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	hops := []remoteexec.Hop{{Target: remoteexec.Target{Host: srv.Host, Port: srv.Port}, Auth: auth, InsecureSkipHostKeyVerify: true}}
	var conn *remoteexec.Conn
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err = remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(ctx, hops, remoteexec.Target{Host: "127.0.0.1", Port: 2223}, auth)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connecting to the limited sshd: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	secret := "p@ss'w$rd\\planted"
	command := "echo " + remoteexec.QuoteArg(secret)
	check := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, remoteexec.ErrCommandRefused) {
			t.Fatalf("%s = %v; want ErrCommandRefused", what, err)
		}
		for _, leak := range []string{"planted", "echo", "\\0047"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("%s's error holds %q: %v", what, leak, err)
			}
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = conn.Run(runCtx, command)
	check("Run", err)
	_, err = conn.Raw().Run(runCtx, command)
	check("a raw Run", err)
	if p, err := conn.Start(runCtx, command); err == nil {
		_ = p.Close()
		t.Fatal("Start of a refused command succeeded")
	} else {
		check("Start", err)
	}
}
