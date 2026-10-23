// Tests of pkg/telnetexec's session against a real telnetd and a real
// /bin/login (BusyBox's, in a container): the login answered, a CLI dialect
// carried over the session, and a wrong or missing login refused. They live
// here, not beside the session, because pkg/telnetexec is part of the
// external SDK, whose tests may not import internal/testsupport.
package telnet_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// TestSession_AgainstARealTelnetd: a real login, then netcli's generic
// dialect runs commands over the session; a wrong password and no login
// at all are each refused by name.
func TestSession_AgainstARealTelnetd(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a telnetd container")
	}
	srv := testsupport.StartTelnetd(t, "t3lnet-pw")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s, err := telnetexec.Dial(ctx, srv.Host, srv.Port, telnetexec.Options{}, telnetexec.Login{Username: testsupport.TelnetdUser, Password: srv.Password})
	if err != nil {
		t.Fatalf("logging in: %v", err)
	}
	defer s.Close()
	cli, err := netcli.Open(ctx, s, netcli.FromPrompt(testsupport.TelnetdPrompt), netcli.Options{})
	if err != nil {
		t.Fatalf("opening the generic dialect over Telnet: %v", err)
	}
	if out, err := cli.Command(ctx, "id -un"); err != nil || strings.TrimSpace(out) != testsupport.TelnetdUser {
		t.Errorf("id -un = %q, %v; want %s", out, err, testsupport.TelnetdUser)
	}
	if out, err := cli.Command(ctx, "echo over-telnet"); err != nil || strings.TrimSpace(out) != "over-telnet" {
		t.Errorf("echo = %q, %v", out, err)
	}

	if _, err := telnetexec.Dial(ctx, srv.Host, srv.Port, telnetexec.Options{}, telnetexec.Login{Username: testsupport.TelnetdUser, Password: "wrong"}); !errors.Is(err, telnetexec.ErrLoginRefused) {
		t.Errorf("a wrong password = %v, want ErrLoginRefused", err)
	}
	if _, err := telnetexec.Dial(ctx, srv.Host, srv.Port, telnetexec.Options{}, telnetexec.Login{}); !errors.Is(err, telnetexec.ErrLoginNeeded) {
		t.Errorf("no login = %v, want ErrLoginNeeded", err)
	}
}
