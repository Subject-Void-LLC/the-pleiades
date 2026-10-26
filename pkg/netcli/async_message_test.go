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
	"time"

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
		case "configure terminal":
			// With no service timestamps log, a message is its mnemonic alone.
			out += "\r\n%SYS-5-CONFIG_I: Configured from console by netops on vty0\r\n" + s.prompt()
		case "show ip route":
			out = line + "\r\nC        172.31.0.0/30 is directly connected, Tunnel0\r\n" + s.prompt()
		case "show clock":
			// An answer with no echo at all.
			out = "*04:51:30.001 UTC Mon Oct 26 2026\r\n" + s.prompt()
		}
		write(out)
	}
}

// openOn opens an IOS session on serve.
func openOn(t *testing.T, serve func(ssh.Channel)) *netcli.Session {
	t.Helper()
	conn := dialFakeIOSServer(t, newFakeIOSServer(t, serve))
	shell, err := conn.Shell(context.Background(), remoteexec.ShellOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	session, err := netcli.Open(context.Background(), shell, netcli.IOS, netcli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestCommand_PassesOverTheDevicesOwnMessages(t *testing.T) {
	session := openOn(t, (&loggingIOS{scriptedIOS{hostname: "P2", mode: "exec"}}).serve)
	out, err := session.Command(context.Background(), "show ip route")
	if err != nil || out != "C        172.31.0.0/30 is directly connected, Tunnel0" {
		t.Fatalf("show ip route = %q, %v", out, err)
	}
	out, err = session.Command(context.Background(), "show clock")
	if err != nil || !strings.Contains(out, "UTC Mon Oct 26 2026") {
		t.Fatalf("an answer with no echo = %q, %v", out, err)
	}
}

// TestDialog_PassesOverTheDevicesOwnMessages: the dialog path (net.ios.command
// and every configuration line) passes over a message too, one with no
// timestamp included.
func TestDialog_PassesOverTheDevicesOwnMessages(t *testing.T) {
	session := openOn(t, (&loggingIOS{scriptedIOS{hostname: "P2", mode: "exec"}}).serve)
	res, err := session.Dialog(context.Background(), "show ip route", netcli.DialogOptions{})
	if err != nil || res.Output != "C        172.31.0.0/30 is directly connected, Tunnel0" {
		t.Fatalf("show ip route = %q, %v", res.Output, err)
	}
	if err := session.Config(context.Background(), []string{"interface Loopback9"}); err != nil {
		t.Fatalf("a configuration after an untimestamped message: %v", err)
	}
}

// silentIOS never echoes a line: it answers each with a syslog-shaped line,
// as show logging | include LINK prints, and its prompt.
func silentIOS(channel ssh.Channel) {
	reader := &crReader{r: channel}
	_, _ = channel.Write([]byte("R9#"))
	for {
		if _, ok := reader.readLine(); !ok {
			return
		}
		_, _ = channel.Write([]byte("*Oct 26 04:51:29.496: %LINK-3-UPDOWN: Interface Ethernet0/1, changed state to up\r\nR9#"))
	}
}

// TestCommand_ADeviceThatNeverEchoesIsReadAtOnce: on a session whose device
// has never echoed, a syslog-shaped answer is the answer, never waited past.
func TestCommand_ADeviceThatNeverEchoesIsReadAtOnce(t *testing.T) {
	session := openOn(t, silentIOS)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := session.Command(ctx, "show logging | include LINK")
	if err != nil || !strings.Contains(out, "%LINK-3-UPDOWN") {
		t.Fatalf("show logging = %q, %v", out, err)
	}
}
