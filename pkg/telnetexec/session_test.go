// Tests of the interactive Telnet session and its login, against a device
// scripted on a real TCP listener to negotiate and ask as IOS's telnet
// server does (measured on the DevNet CML sandbox's IOL XE routers): IAC
// WILL ECHO, WILL SGA and DO TTYPE, then "User Access Verification",
// "Username:" and "Password:".
package telnetexec_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// iosNegotiation is what IOS sends on connect.
var iosNegotiation = []byte{0xFF, 0xFB, 0x01, 0xFF, 0xFB, 0x03, 0xFF, 0xFD, 0x18, 0xFF, 0xFD, 0x1F}

// lineReader reads one CR LF ended line from the client, dropping the
// three-byte negotiation replies the client sends.
type lineReader struct{ r *bufio.Reader }

func (l lineReader) line() (string, error) {
	var b strings.Builder
	for {
		c, err := l.r.ReadByte()
		if err != nil {
			return b.String(), err
		}
		switch {
		case c == 0xFF:
			if _, err := l.r.Discard(2); err != nil {
				return b.String(), err
			}
		case c == '\r':
		case c == '\n':
			return b.String(), nil
		default:
			b.WriteByte(c)
		}
	}
}

// iosDevice scripts one connection: a login for user/pass, then each line
// answered by answers (unknown lines echo back), with prompt after each.
// A wrong login is refused as IOS refuses it, then asked again.
func iosDevice(user, pass, prompt string, answers map[string]string) func(net.Conn) {
	return func(conn net.Conn) {
		defer conn.Close()
		lr := lineReader{bufio.NewReader(conn)}
		_, _ = conn.Write(iosNegotiation)
		_, _ = conn.Write([]byte("\r\n\r\nUser Access Verification\r\n\r\nUsername: "))
		for {
			u, err := lr.line()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("Password: "))
			p, err := lr.line()
			if err != nil {
				return
			}
			if u == user && p == pass {
				break
			}
			_, _ = conn.Write([]byte("\r\n% Login invalid\r\n\r\nUsername: "))
		}
		_, _ = conn.Write([]byte("\r\n" + prompt))
		for {
			l, err := lr.line()
			if err != nil {
				return
			}
			out, ok := answers[l]
			if !ok {
				out = l
			}
			_, _ = conn.Write([]byte(l + "\r\n" + out + "\r\n" + prompt))
		}
	}
}

// open dials the scripted device with login, with short timeouts.
func open(t *testing.T, port int, login telnetexec.Login) (*telnetexec.Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return telnetexec.Dial(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 300 * time.Millisecond}, login)
}

var r1Prompt = regexp.MustCompile(`(?m)^R1#\s*$`)

// TestSession_LogsInAndCarriesLines: the login is answered, the prompt the
// device printed after it is read first, and each line's answer follows.
func TestSession_LogsInAndCarriesLines(t *testing.T) {
	port := telnetServer(t, iosDevice("cisco", "s3cret", "R1#", map[string]string{"show clock": "*12:00:00.000 UTC Fri Oct 23 2026"}))
	s, err := open(t, port, telnetexec.Login{Username: "cisco", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.ReadUntil(ctx, r1Prompt, 1<<16); err != nil {
		t.Fatalf("the first prompt: %v", err)
	}
	if err := s.WriteLine(ctx, "show clock"); err != nil {
		t.Fatal(err)
	}
	out, err := s.ReadUntil(ctx, r1Prompt, 1<<16)
	if err != nil || !strings.Contains(out, "Fri Oct 23 2026") {
		t.Fatalf("show clock = %q, %v", out, err)
	}
	if err := s.WriteLine(ctx, "show clock\nreload"); err == nil {
		t.Error("a line with an embedded line feed was sent")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

// TestSession_RefusesALoginItCannotAnswer: a wrong password is refused by
// name, and a device that asks with no login given is refused before any
// name is sent.
func TestSession_RefusesALoginItCannotAnswer(t *testing.T) {
	port := telnetServer(t, iosDevice("cisco", "s3cret", "R1#", nil))
	if _, err := open(t, port, telnetexec.Login{Username: "cisco", Password: "wrong"}); !errors.Is(err, telnetexec.ErrLoginRefused) {
		t.Errorf("a wrong password = %v, want ErrLoginRefused", err)
	}
	port = telnetServer(t, iosDevice("cisco", "s3cret", "R1#", nil))
	if _, err := open(t, port, telnetexec.Login{}); !errors.Is(err, telnetexec.ErrLoginNeeded) || !strings.Contains(err.Error(), "Username:") {
		t.Errorf("no login = %v, want ErrLoginNeeded naming the question", err)
	}
}

// TestSession_ALinuxLoginAndADeviceThatAsksNothing: "login:" is a name
// prompt too, and a console that asks nothing (a GNS3 VPCS) opens with no
// login at all, its banner kept for the first read.
func TestSession_ALinuxLoginAndADeviceThatAsksNothing(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		lr := lineReader{bufio.NewReader(conn)}
		_, _ = conn.Write([]byte("debian login: "))
		if u, _ := lr.line(); u != "netops" {
			return
		}
		_, _ = conn.Write([]byte("Password: "))
		if p, _ := lr.line(); p != "pw" {
			_, _ = conn.Write([]byte("\r\nLogin incorrect\r\ndebian login: "))
			return
		}
		_, _ = conn.Write([]byte("\r\nnetops@debian:~$ "))
		time.Sleep(time.Second)
	})
	s, err := open(t, port, telnetexec.Login{Username: "netops", Password: "pw"})
	if err != nil {
		t.Fatalf("a Linux login: %v", err)
	}
	if out, err := s.ReadUntil(context.Background(), regexp.MustCompile(`\$ $`), 1024); err != nil || !strings.Contains(out, "netops@debian") {
		t.Errorf("the shell prompt = %q, %v", out, err)
	}
	_ = s.Close()

	port = telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = conn.Write([]byte("Welcome to Virtual PC Simulator\r\n\r\nVPCS> "))
		time.Sleep(time.Second)
	})
	s, err = open(t, port, telnetexec.Login{Username: "unused", Password: "unused"})
	if err != nil {
		t.Fatalf("a console that asks nothing: %v", err)
	}
	defer s.Close()
	if out, err := s.ReadUntil(context.Background(), regexp.MustCompile(`VPCS> $`), 1024); err != nil || !strings.Contains(out, "Virtual PC") {
		t.Errorf("the banner = %q, %v", out, err)
	}
}

// TestSession_AReadThatCannotMatchEnds: past its bound, or when its
// context ends, a read closes the session and keeps what it read.
func TestSession_AReadThatCannotMatchEnds(t *testing.T) {
	port := telnetServer(t, iosDevice("cisco", "s3cret", "R1#", nil))
	s, err := open(t, port, telnetexec.Login{Username: "cisco", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReadUntil(context.Background(), regexp.MustCompile(`never`), 2)
	var re *telnetexec.ReadError
	if !errors.As(err, &re) || !strings.Contains(string(re.PartialRead()), "R1#") || !strings.Contains(err.Error(), "bound") {
		t.Errorf("past the bound = %v", err)
	}

	port = telnetServer(t, iosDevice("cisco", "s3cret", "R1#", nil))
	if s, err = open(t, port, telnetexec.Login{Username: "cisco", Password: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if _, err := s.ReadUntil(ctx, regexp.MustCompile(`never`), 1<<16); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an ended context = %v", err)
	}
	if err := s.WriteLine(ctx, "show clock"); err == nil {
		t.Error("a line was written after the context ended")
	}
}

// TestExec_LogsInWhenAsked: the one-shot Exec answers the login from its
// options before it sends the command.
func TestExec_LogsInWhenAsked(t *testing.T) {
	port := telnetServer(t, iosDevice("cisco", "s3cret", "R1#", map[string]string{"show version": "Cisco IOS XE Software, Version 17.15.01"}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 300 * time.Millisecond, Login: telnetexec.Login{Username: "cisco", Password: "s3cret"}}, "show version")
	if err != nil || !strings.Contains(res.Stdout, "Version 17.15.01") || strings.Contains(res.Stdout, "s3cret") {
		t.Errorf("Exec = %q, %v; want the version and never the password", res.Stdout, err)
	}
}

// TestSession_LoginsThatGoWrong: a closed port, a password-only device
// with no login given, a device that asks for the name again after the
// password, one that keeps asking, and a session used after its device
// hung up or it was closed.
func TestSession_LoginsThatGoWrong(t *testing.T) {
	ctx := context.Background()
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", 1, telnetexec.Options{DialTimeout: time.Second}, telnetexec.Login{}); err == nil || !strings.Contains(err.Error(), "dialing") {
		t.Errorf("a closed port = %v", err)
	}
	asks := func(first string, after func(n int) string) int {
		return telnetServer(t, func(conn net.Conn) {
			defer conn.Close()
			lr := lineReader{bufio.NewReader(conn)}
			_, _ = conn.Write([]byte(first))
			for n := 1; ; n++ {
				if _, err := lr.line(); err != nil {
					return
				}
				_, _ = conn.Write([]byte(after(n)))
			}
		})
	}
	short := telnetexec.Options{ReadTimeout: 200 * time.Millisecond, LoginTimeout: time.Second}
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", asks("Password: ", func(int) string { return "" }), short, telnetexec.Login{}); !errors.Is(err, telnetexec.ErrLoginNeeded) {
		t.Errorf("a password-only device with no login = %v", err)
	}
	again := asks("Username: ", func(n int) string {
		if n == 1 {
			return "Password: "
		}
		return "\r\nUsername: "
	})
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", again, short, telnetexec.Login{Username: "u", Password: "p"}); !errors.Is(err, telnetexec.ErrLoginRefused) || !strings.Contains(err.Error(), "name again") {
		t.Errorf("asked for the name again = %v", err)
	}
	// IOS re-prompts a wrong line password with no refusal text: the
	// password is sent once and the second question is the refusal, so a
	// login never counts twice against the device's lockout.
	var passwords atomic.Int32
	reprompt := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		lr := lineReader{bufio.NewReader(conn)}
		_, _ = conn.Write([]byte("Password: "))
		for {
			if _, err := lr.line(); err != nil {
				return
			}
			passwords.Add(1)
			_, _ = conn.Write([]byte("\r\nPassword: "))
		}
	})
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", reprompt, short, telnetexec.Login{Password: "p"}); !errors.Is(err, telnetexec.ErrLoginRefused) || !strings.Contains(err.Error(), "password again") {
		t.Errorf("a password asked for again = %v", err)
	}
	if n := passwords.Load(); n != 1 {
		t.Errorf("the password was sent %d times, want once", n)
	}
	forever := asks("Username: ", func(int) string { return "\r\nUsername: " })
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", forever, short, telnetexec.Login{Username: "u", Password: "p"}); !errors.Is(err, telnetexec.ErrLoginRefused) || !strings.Contains(err.Error(), "kept asking") {
		t.Errorf("a device that keeps asking = %v", err)
	}
	long := asks(strings.Repeat("x", 500)+" Username: ", func(int) string { return "" })
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", long, short, telnetexec.Login{}); !errors.Is(err, telnetexec.ErrLoginNeeded) || len(err.Error()) > 300 {
		t.Errorf("a long question is quoted whole: %d bytes: %v", len(err.Error()), err)
	}

	hangup := telnetServer(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("VPCS> "))
		time.Sleep(300 * time.Millisecond)
		_ = conn.Close()
	})
	s, err := telnetexec.Dial(ctx, "127.0.0.1", hangup, short, telnetexec.Login{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadUntil(ctx, regexp.MustCompile(`never`), 1<<16); err == nil || !strings.Contains(err.Error(), "reading from") {
		t.Errorf("a device that hung up = %v", err)
	}
	if err := s.WriteLine(ctx, "show ip"); err == nil {
		t.Error("a line was written to a closed session")
	}
	if _, err := s.ReadUntil(ctx, regexp.MustCompile(`never`), 1<<16); err == nil {
		t.Error("a closed session was read")
	}
}

// TestSession_ADeviceThatNeverGoesQuietIsCutOff: a device sending only
// negotiation never adds to the output, and one sending a byte at a time
// never goes quiet; both are ended by the raw byte bound or the phase's
// own deadline, at login and while reading to a prompt, rather than holding
// the run for as long as its context allows.
func TestSession_ADeviceThatNeverGoesQuietIsCutOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	negotiates := func(prompt string) int {
		return telnetServer(t, func(conn net.Conn) {
			defer conn.Close()
			if prompt != "" {
				_, _ = conn.Write([]byte(prompt))
				time.Sleep(400 * time.Millisecond)
			}
			for {
				// IAC WILL ECHO, over and over: refused each time, never data.
				if _, err := conn.Write([]byte{0xFF, 0xFB, 0x01}); err != nil {
					return
				}
			}
		})
	}
	dribbles := func(prompt string) int {
		return telnetServer(t, func(conn net.Conn) {
			defer conn.Close()
			if prompt != "" {
				_, _ = conn.Write([]byte(prompt))
				time.Sleep(400 * time.Millisecond)
			}
			for {
				if _, err := conn.Write([]byte("x")); err != nil {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
	small := telnetexec.Options{ReadTimeout: 200 * time.Millisecond, MaxOutputBytes: 256, MaxReadDuration: 1500 * time.Millisecond}
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", negotiates(""), small, telnetexec.Login{}); err == nil || !strings.Contains(err.Error(), "negotiation included") {
		t.Errorf("negotiation without end at login = %v", err)
	}
	if _, err := telnetexec.Dial(ctx, "127.0.0.1", dribbles(""), small, telnetexec.Login{}); err == nil || !strings.Contains(err.Error(), "without going quiet") {
		t.Errorf("a dribble at login = %v", err)
	}
	prompt := regexp.MustCompile(`R1#\s*$`)
	for name, port := range map[string]int{"negotiation": negotiates("R1> "), "a dribble": dribbles("R1> ")} {
		s, err := telnetexec.Dial(ctx, "127.0.0.1", port, small, telnetexec.Login{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		start := time.Now()
		_, err = s.ReadUntil(ctx, prompt, 256)
		if err == nil || time.Since(start) > 10*time.Second {
			t.Errorf("%s while reading to a prompt = %v after %s", name, err, time.Since(start))
		}
		_ = s.Close()
	}
}
