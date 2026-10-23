// Package ssh: tests that a command given standard input gets its end of
// input from a real OpenSSH sshd every time, including when the input is
// empty.
package ssh

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestSSHContainer_AnEmptyStdinEndsTheCommandsInput: "cat > file" given no
// input finishes, forty times over one connection. Its end of input used to
// be sent as soon as the copy started, before the exec request, and sshd
// never delivered it, so cat waited forever whenever the close won that race
// (net.frr.config's backup of an empty frr.conf hung under -race).
func TestSSHContainer_AnEmptyStdinEndsTheCommandsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	host, port := requireSSHContainer(t)
	auth, err := remoteexec.AuthFrom(containerSSHUser, containerSSHPassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: host, Port: port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close() }()

	for i := range 40 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		res, err := conn.RunWithStdin(ctx, fmt.Sprintf(`cat > /tmp/empty-stdin-%d && wc -c < /tmp/empty-stdin-%d`, i, i), strings.NewReader(""))
		cancel()
		if err != nil {
			t.Fatalf("run %d: %v; the command never saw the end of its input", i, err)
		}
		if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "0" {
			t.Fatalf("run %d: exit %d, stdout %q, stderr %q; want an empty file", i, res.ExitCode, res.Stdout, res.Stderr)
		}
	}
}
