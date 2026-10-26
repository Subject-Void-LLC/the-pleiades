// Tests that a message IOS prints on its own on a console line, and the
// prompt it prints after one, are passed over rather than read as the next
// command's answer: the login's %SEC_LOGIN-5-LOGIN_SUCCESS, measured through
// the CML console, ahead of show ip route's echo. A device that answers
// without an echo is still read at once.
package netcli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"golang.org/x/crypto/ssh"
)

// loggingIOS answers as scriptedIOS does, and once the session has
// disabled wrapping it prints a syslog line and a prompt of its own, as a
// console line does after the login.
type loggingIOS struct{ scriptedIOS }

func (s *loggingIOS) serve(channel ssh.Channel) {
	reader := &crReader{r: channel}
	write := func(text string) { _, _ = channel.Write([]byte(text)) }
	write(s.prompt())
	for {
		line, ok := reader.readLine()
		if !ok {
			return
		}
		s.commands = append(s.commands, line)
		out := line + "\r\n" + s.respond(line) + s.prompt()
		switch line {
		case "terminal width 512":
			out += "\r\n*Oct 26 04:51:29.496: %SEC_LOGIN-5-LOGIN_SUCCESS: Login Success [user: netops] [Source: LOCAL]\r\n" + s.prompt()
		case "show ip route":
			out = line + "\r\nC        172.31.0.0/30 is directly connected, Tunnel0\r\n" + s.prompt()
		case "show clock":
			// An answer with no echo at all.
			out = "*04:51:30.001 UTC Mon Oct 26 2026\r\n" + s.prompt()
		}
		write(out)
	}
}

func TestCommand_PassesOverTheDevicesOwnMessages(t *testing.T) {
	d := &loggingIOS{scriptedIOS{hostname: "P2", mode: "exec"}}
	target := newFakeIOSServer(t, d.serve)
	conn := dialFakeIOSServer(t, target)
	shell, err := conn.Shell(context.Background(), remoteexec.ShellOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	session, err := netcli.Open(context.Background(), shell, netcli.IOS, netcli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := session.Command(context.Background(), "show ip route")
	if err != nil || out != "C        172.31.0.0/30 is directly connected, Tunnel0" {
		t.Fatalf("show ip route = %q, %v", out, err)
	}
	out, err = session.Command(context.Background(), "show clock")
	if err != nil || !strings.Contains(out, "UTC Mon Oct 26 2026") {
		t.Fatalf("an answer with no echo = %q, %v", out, err)
	}
}
