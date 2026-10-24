// Tests of a Session over Telnet (telnetexec.Session), against an IOS
// device scripted on a real TCP listener as the DevNet CML sandbox's IOL XE
// routers behaved (2026-10-23): a login that lands at "R1>", "enable"
// asking "Password:" up to three times, and "configure terminal" refused
// outside privileged mode.
package netcli_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// telnetIOS serves one IOS login on a loopback listener and returns its
// port. The account lands in user mode unless privileged; enableSecret is
// what enable takes.
func telnetIOS(t *testing.T, privileged bool, enableSecret string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		line := func() (string, bool) {
			var b strings.Builder
			for {
				c, err := r.ReadByte()
				if err != nil {
					return "", false
				}
				switch {
				case c == 0xFF:
					_, _ = r.Discard(2)
				case c == '\r':
				case c == '\n':
					return b.String(), true
				default:
					b.WriteByte(c)
				}
			}
		}
		write := func(s string) { _, _ = conn.Write([]byte(s)) }
		write("\xff\xfb\x01\xff\xfb\x03\r\n\r\nUser Access Verification\r\n\r\nUsername: ")
		if _, ok := line(); !ok {
			return
		}
		write("Password: ")
		if _, ok := line(); !ok {
			return
		}
		mode := "R1>"
		if privileged {
			mode = "R1#"
		}
		write("\r\n" + mode)
		for {
			l, ok := line()
			if !ok {
				return
			}
			write(l + "\r\n")
			switch {
			case l == "enable" && mode == "R1>":
				tries := 0
				for {
					write("Password: ")
					secret, ok := line()
					if !ok {
						return
					}
					if secret == enableSecret {
						mode = "R1#"
						break
					}
					if tries++; tries == 3 {
						write("% Bad secrets\r\n\r\n")
						break
					}
				}
			case l == "configure terminal" && mode == "R1>":
				write("                    ^\r\n% Invalid input detected at '^' marker.\r\n\r\n")
			case l == "configure terminal":
				mode = "R1(config)#"
			case strings.HasPrefix(l, "interface ") && strings.HasPrefix(mode, "R1(config"):
				mode = "R1(config-if)#"
			case l == "end":
				mode = "R1#"
			case l == "show privilege":
				if mode == "R1#" {
					write("Current privilege level is 15\r\n")
				} else {
					write("Current privilege level is 1\r\n")
				}
			}
			write(mode)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// openTelnet logs in to the scripted device and opens an IOS Session on
// it with enableSecret.
func openTelnet(t *testing.T, port int, enableSecret string) (*netcli.Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shell, err := telnetexec.Dial(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 200 * time.Millisecond}, telnetexec.Login{Username: "cisco", Password: "cisco"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Close() })
	return netcli.Open(ctx, shell, netcli.IOS, netcli.Options{EnableSecret: enableSecret})
}

// TestTelnet_UserModeRunsCommandsAndRefusesConfiguration: with no enable
// secret, a user-mode session runs commands and refuses configuration by
// name, sending none of it.
func TestTelnet_UserModeRunsCommandsAndRefusesConfiguration(t *testing.T) {
	s, err := openTelnet(t, telnetIOS(t, false, "en4ble"), "")
	if err != nil {
		t.Fatalf("Open in user mode: %v", err)
	}
	ctx := context.Background()
	if out, err := s.Command(ctx, "show privilege"); err != nil || out != "Current privilege level is 1" {
		t.Errorf("show privilege = %q, %v", out, err)
	}
	if err := s.Config(ctx, []string{"interface Loopback99"}); err == nil || !strings.Contains(err.Error(), "--enable-password") {
		t.Errorf("Config in user mode = %v, want a refusal naming the enable secret", err)
	}
}

// TestTelnet_TheEnableSecretReachesPrivilegedMode: the secret answers
// enable, and configuration then runs through its sub-modes.
func TestTelnet_TheEnableSecretReachesPrivilegedMode(t *testing.T) {
	s, err := openTelnet(t, telnetIOS(t, false, "en4ble"), "en4ble")
	if err != nil {
		t.Fatalf("Open with the enable secret: %v", err)
	}
	ctx := context.Background()
	if out, err := s.Command(ctx, "show privilege"); err != nil || out != "Current privilege level is 15" {
		t.Errorf("show privilege = %q, %v", out, err)
	}
	if err := s.Config(ctx, []string{"interface Loopback99", "description pleiades"}); err != nil {
		t.Errorf("Config: %v", err)
	}
}

// TestTelnet_AWrongEnableSecretIsRefusedWithoutNamingIt: the device asks
// again, the session clears its questions, and the error says the secret
// was refused without carrying it.
func TestTelnet_AWrongEnableSecretIsRefusedWithoutNamingIt(t *testing.T) {
	_, err := openTelnet(t, telnetIOS(t, false, "en4ble"), "not-the-secret")
	if err == nil || !strings.Contains(err.Error(), "refused the enable secret") || strings.Contains(err.Error(), "not-the-secret") {
		t.Errorf("a wrong secret = %v, want refused without the secret", err)
	}
}

// TestTelnet_APrivilegedLoginSendsNoEnable: an account that lands at "#"
// is never sent enable, its secret or not.
func TestTelnet_APrivilegedLoginSendsNoEnable(t *testing.T) {
	s, err := openTelnet(t, telnetIOS(t, true, ""), "unused")
	if err != nil {
		t.Fatalf("Open at privilege 15: %v", err)
	}
	if err := s.Config(context.Background(), []string{"interface Loopback99"}); err != nil {
		t.Errorf("Config: %v", err)
	}
}
