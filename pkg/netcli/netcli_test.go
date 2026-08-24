package netcli_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"golang.org/x/crypto/ssh"
)

// scriptedIOS is a small, stateful fake IOS responder: it tracks
// configuration-mode across commands (exec / config / config-if) the
// same way a real device does, and only ever replies with the
// mode-correct prompt. This is what proves Session.Config reads back
// the CONFIGURATION-mode prompt while sending configuration lines and
// the EXEC-mode prompt only once it has actually left configuration
// mode: a hand-substituted fake that returned whatever prompt a test
// author expected would never catch Session reading the wrong one, but
// this responder, which genuinely will not produce an exec-mode prompt
// while "in" configuration mode, turns that class of bug into a hang
// instead of a silently wrong pass.
//
// Every line and prompt shape below matches what
// pkg/netcli/live_probe_test.go recorded against a real Cisco IOS XE
// device: the echo-then-CRLF-then-output-then-prompt structure, the
// "(config-if)#" sub-mode prompt "interface ..." produces, and the "%
// Invalid input detected at '^' marker." error line.
type scriptedIOS struct {
	hostname string

	mode string // "exec", "config", or "config-if"

	pagingDisabled bool
	commands       []string // every line received, in order, for assertions
}

func (s *scriptedIOS) prompt() string {
	switch s.mode {
	case "config":
		return s.hostname + "(config)#"
	case "config-if":
		return s.hostname + "(config-if)#"
	default:
		return s.hostname + "#"
	}
}

// serve implements one connection's worth of the scripted protocol: it
// writes the first prompt unprompted, then loops reading one
// "\r"-terminated line at a time and reacting to it exactly as the real
// device did.
func (s *scriptedIOS) serve(channel ssh.Channel) {
	reader := &crReader{r: channel}
	write := func(text string) { channel.Write([]byte(text)) }

	write(s.prompt())
	for {
		line, ok := reader.readLine()
		if !ok {
			return
		}
		s.commands = append(s.commands, line)
		write(line + "\r\n" + s.respond(line) + s.prompt())
	}
}

// respond returns the device's own output for line (empty for most
// commands, matching "terminal length 0" and a config-mode line's own
// observed real behavior), and updates s.mode exactly as the real
// device's own state machine does.
func (s *scriptedIOS) respond(line string) string {
	switch {
	case line == "terminal length 0":
		s.pagingDisabled = true
		return ""

	case line == "configure terminal" && s.mode == "exec":
		s.mode = "config"
		return "Enter configuration commands, one per line.  End with CNTL/Z.\r\n"

	case strings.HasPrefix(line, "interface ") && s.mode == "config":
		s.mode = "config-if"
		return ""

	case line == "exit" && s.mode == "config-if":
		s.mode = "config"
		return ""

	case line == "end":
		s.mode = "exec"
		return ""

	case line == "this-is-not-a-real-command":
		return "                ^\r\n% Invalid input detected at '^' marker.\r\n\r\n"

	default:
		return ""
	}
}

// crReader reads one "\r"-terminated line at a time from r, matching
// remoteexec.Shell.WriteLine's own terminator convention.
type crReader struct {
	r   io.Reader
	buf []byte
}

func (c *crReader) readLine() (string, bool) {
	for {
		if i := bytes.IndexByte(c.buf, '\r'); i >= 0 {
			line := string(c.buf[:i])
			c.buf = c.buf[i+1:]
			return line, true
		}
		chunk := make([]byte, 256)
		n, err := c.r.Read(chunk)
		if n > 0 {
			c.buf = append(c.buf, chunk[:n]...)
		}
		if err != nil {
			return "", false
		}
	}
}

func newScriptedSession(t *testing.T, s *scriptedIOS, dialect netcli.Dialect) *netcli.Session {
	t.Helper()
	target := newFakeIOSServer(t, s.serve)
	conn := dialFakeIOSServer(t, target)
	shell, err := conn.Shell(context.Background(), remoteexec.ShellOptions{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	t.Cleanup(func() { _ = shell.Close() })

	session, err := netcli.Open(context.Background(), shell, dialect, netcli.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return session
}

func TestOpen_DisablesPagingWhenDialectNamesOne(t *testing.T) {
	s := &scriptedIOS{hostname: "Cat8kv", mode: "exec"}
	newScriptedSession(t, s, netcli.IOS)

	if !s.pagingDisabled {
		t.Error("Open did not send the dialect's DisablePaging command")
	}
	if len(s.commands) != 1 || s.commands[0] != "terminal length 0" {
		t.Errorf("commands received = %v, want exactly [\"terminal length 0\"]", s.commands)
	}
}

func TestOpen_RefusesADialectWithNoPromptPattern(t *testing.T) {
	// dialect.Prompt is nil before any I/O happens, so this needs no
	// fake server or Shell at all: a nil Shell proves the refusal
	// happens before Open ever touches it.
	_, err := netcli.Open(context.Background(), nil, netcli.FromPrompt(""), netcli.Options{})
	if err == nil {
		t.Fatal("Open with an empty-prompt Dialect returned no error")
	}
	if !strings.Contains(err.Error(), "cli_prompt") {
		t.Errorf("error = %v, want it to name the cli_prompt property", err)
	}
}

func TestCommand_StripsTheEchoedLineAndTrailingPrompt(t *testing.T) {
	s := &scriptedIOS{hostname: "Cat8kv", mode: "exec"}
	session := newScriptedSession(t, s, netcli.IOS)

	out, err := session.Command(context.Background(), "this-is-not-a-real-command")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := "                ^\r\n% Invalid input detected at '^' marker."
	if out != want {
		t.Errorf("Command output = %q, want %q (echo and trailing prompt stripped)", out, want)
	}
}

// TestConfig_ReadsTheConfigModePromptNotTheExecPrompt is this package's
// own proof of the class of bug the fabricated Phase 86.5 spec claimed,
// without ever having built anything, to have already caught: Config
// entering configuration mode and a sub-mode, with the fake server
// refusing to ever produce an exec-mode prompt while "in" either one.
// If Session read the wrong prompt at any step, this test would hang
// until the test binary's own default timeout, not return a wrong
// answer quickly.
func TestConfig_ReadsTheConfigModePromptNotTheExecPrompt(t *testing.T) {
	s := &scriptedIOS{hostname: "Cat8kv", mode: "exec"}
	session := newScriptedSession(t, s, netcli.IOS)

	err := session.Config(context.Background(), []string{
		"interface Loopback8986",
		"description pleiades-test",
		"exit",
	})
	if err != nil {
		t.Fatalf("Config: %v", err)
	}

	want := []string{"terminal length 0", "configure terminal", "interface Loopback8986", "description pleiades-test", "exit", "end"}
	if strings.Join(s.commands, ",") != strings.Join(want, ",") {
		t.Errorf("commands received = %v, want %v", s.commands, want)
	}
	if s.mode != "exec" {
		t.Errorf("device mode after Config = %q, want %q (end must return to exec mode)", s.mode, "exec")
	}
}

// TestConfig_AbortsAndStillExitsConfigModeOnRejection proves a rejected
// line stops the batch AND still leaves configuration mode, rather than
// stranding the device there for whatever the caller does next.
func TestConfig_AbortsAndStillExitsConfigModeOnRejection(t *testing.T) {
	s := &scriptedIOS{hostname: "Cat8kv", mode: "exec"}
	session := newScriptedSession(t, s, netcli.IOS)

	err := session.Config(context.Background(), []string{
		"this-is-not-a-real-command",
		"description never-reached",
	})
	if err == nil {
		t.Fatal("Config with a rejected line returned no error")
	}
	if !strings.Contains(err.Error(), "this-is-not-a-real-command") {
		t.Errorf("error = %v, want it to name the rejected line", err)
	}

	want := []string{"terminal length 0", "configure terminal", "this-is-not-a-real-command", "end"}
	if strings.Join(s.commands, ",") != strings.Join(want, ",") {
		t.Errorf("commands received = %v, want %v (the second line must never be sent, and end must still run)", s.commands, want)
	}
	if s.mode != "exec" {
		t.Errorf("device mode after a rejected Config = %q, want %q", s.mode, "exec")
	}
}

func TestConfig_RefusesADialectWithNoConfigModeCommands(t *testing.T) {
	// A Session built around FromPrompt's own generic Dialect has no
	// EnterConfigMode; this check runs before any I/O, so a scripted
	// server that would otherwise hang forever waiting for a line that
	// never comes proves the refusal is genuinely immediate.
	s := &scriptedIOS{hostname: "Cat8kv", mode: "exec"}
	session := newScriptedSession(t, s, netcli.FromPrompt("Cat8kv#"))

	if err := session.Config(context.Background(), []string{"interface Loopback0"}); err == nil {
		t.Fatal("Config on a Dialect with no configuration-mode commands returned no error")
	}
}
