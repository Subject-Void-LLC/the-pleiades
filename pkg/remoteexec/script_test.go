// Tests of what a connection sends: every command encoded for sh
// (shline), as written on a Raw view, and nothing over the size one argument
// may hold. The harness runs each line through a real /bin/sh, as sshd runs
// it through a login shell that is sh; the other login shells are the
// release gates' (PLEIADES_TEST_LOGIN_SHELL) and internal/transport/ssh's.
package remoteexec

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/shline"
)

func TestConn_SendsEveryCommandForShUnlessRaw(t *testing.T) {
	conn, srv, _ := detachedServer(t)
	command := `x='it'\''s'; printf '%s|%s\n' "$x" "$(echo ok)"`
	res, err := conn.Run(context.Background(), command)
	if err != nil || res.Stdout != "it's|ok\n" {
		t.Fatalf("Run = %q, %v", res.Stdout, err)
	}
	if res, err := conn.RunWithStdin(context.Background(), `read l; echo "in:$l"`, strings.NewReader("data\n")); err != nil || res.Stdout != "in:data\n" {
		t.Fatalf("RunWithStdin = %q, %v: standard input must still reach the command", res.Stdout, err)
	}
	if res, err := conn.Raw().Run(context.Background(), "echo raw"); err != nil || res.Stdout != "raw\n" {
		t.Fatalf("Raw Run = %q, %v", res.Stdout, err)
	}
	wire := srv.WireCommands()
	if len(wire) != 3 || wire[0] != shline.For(command) || wire[2] != "echo raw" {
		t.Fatalf("sent %q; want the first two encoded for sh and the Raw one as written", wire)
	}
	if got := srv.Commands(); got[0] != command {
		t.Errorf("the harness decodes %q, want %q", got[0], command)
	}
}

func TestConn_RefusesALineOverOneArgumentBeforeSendingIt(t *testing.T) {
	conn, srv, _ := detachedServer(t)
	_, err := conn.Run(context.Background(), "echo '"+strings.Repeat("'", shline.MaxBytes/4)+"'")
	if err == nil || !strings.Contains(err.Error(), "send its data on standard input") {
		t.Fatalf("err = %v", err)
	}
	if _, err := conn.Start(context.Background(), strings.Repeat("$", shline.MaxBytes/4)); err == nil {
		t.Fatal("Start sent a line over the limit")
	}
	if len(srv.WireCommands()) != 0 {
		t.Errorf("sent %d lines", len(srv.WireCommands()))
	}
}
