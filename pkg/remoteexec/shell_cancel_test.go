// A Shell call's context ends the session only while the call runs: a
// caller that cancels it the moment the call returns, as net.cli does with
// each line's own timeout, must find the session still open.
package remoteexec

import (
	"bufio"
	"context"
	"regexp"
	"runtime"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestShell_ACancelAfterTheCallLeavesTheSessionOpen: each call's watcher
// is still choosing, with both its channels ready, when the caller cancels
// right after the call returns; one CPU makes that the ordinary case. A
// watcher that closed the session then turned the next read into EOF
// (FAILURE_PATTERNS 633, seen on PR #89's fast job).
func TestShell_ACancelAfterTheCallLeavesTheSessionOpen(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	dial := newFakePTYSSHServer(t, func(channel ssh.Channel) {
		_, _ = channel.Write([]byte("R1#"))
		r := bufio.NewReader(channel)
		for {
			if _, err := r.ReadString('\r'); err != nil {
				return
			}
			if _, err := channel.Write([]byte("\r\nok\r\nR1#")); err != nil {
				return
			}
		}
	})
	conn := newTestConn(t, dial)
	defer conn.Close()
	shell, err := conn.Shell(context.Background(), ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer shell.Close()
	prompt := regexp.MustCompile(`R1#$`)
	if _, err := shell.ReadUntil(context.Background(), prompt, 1<<20); err != nil {
		t.Fatalf("the first prompt: %v", err)
	}
	for i := range 200 {
		ctx, cancel := context.WithCancel(context.Background())
		if err := shell.WriteLine(ctx, "show clock"); err != nil {
			t.Fatalf("line %d: write: %v", i, err)
		}
		cancel()
		ctx, cancel = context.WithCancel(context.Background())
		if _, err := shell.ReadUntil(ctx, prompt, 1<<20); err != nil {
			t.Fatalf("line %d: read after the previous call's context was cancelled: %v", i, err)
		}
		cancel()
	}
}
